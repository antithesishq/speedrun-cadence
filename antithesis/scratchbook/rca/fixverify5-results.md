# Fix-verify-5 results: run fc87ca1b65b52553e7c27afe9f6897ef-67-6 (combined-fixverify3.patch)

Build: Cadence v1.4.5 plus `antithesis/fixes/combined-fixverify3.patch` and the staged changes in the worktree
`src/cadence-fixverify3`. The worktree has six changes: v2 reset dedup (the reset run's CreateRequestID is set to the
reset request ID), `copySignalRequestedIDs` (reset copies the base run's dedup set), `isResetRunPredecessor` (reset
dedup across continue-as-new), the acked-signal-lost-on-reset reapply of flushed events, the nil guards for
stop-during-start, and a `conn.go` Close that no longer panics. It does not include `signal-dedup-across-can.diff`.
Fast tier. Property table: `runs/props-fc87ca1b.txt`. Baselines: f04f3907 and bba4d2f0 (unpatched v1.4.5), and
1a0d055c (the earlier partial patch set).

Raw data is in `antithesis/scratchbook/logs/fixverify5/`:
- `*.json`: every failing example of each property (search `--limit 999`). The totals equal the counterexample counts (410, 114, 100, 27).
- `reset-rpc.txt`: the driver's `reset` rpc lines (300-line sample).
- `h-*.txt`: `wf list` and full JSON histories, collected in cadence1 at each failing moment.
- `tl_*.ndjson`, `crash_*.ndjson`: timeline logs and crash slices.
- `reset-probe.txt`: a manual reset run with the CLI.

The histories and the probe came from debugging session 1980e34452d9b31d6806525fee40027b-67-6, which I started
and then **cancelled**.

## Headline: the patch set breaks every reset (REGRESSION)

The Sometimes property **"a workflow reset succeeds" was never hit (0/0)**. It passed 6.0k, 7.1k and 3.3k times in
the three baselines. Every reset in the run failed:

| reset rpc outcome (driver, 300 sampled) | count |
|---|---|
| `failed bad_request` | 269 |
| `unknown timeout` | 20 |
| `failed no_reset_point` | 10 |
| `unknown other` | 1 |
| `ok` | **0** (`--count` over the whole run: 0) |

**Cause:** `copySignalRequestedIDs` (from `reset-copies-base-dedup-set.diff`, `service/history/reset/resetter.go:365`)
reads the base run through `r.shard.GetExecutionManager().GetWorkflowExecution(...)`. That is the raw execution
manager, and the request does not set `ShardID`. The SQL store rejects any such request
(`common/persistence/sql/sql_execution_store.go:96-99`, `resolveShardID`) with
`BadRequestError{"execution persistence request missing shard ID"}`. As a result, `prepareResetWorkflow` fails for
every reset.

Evidence:
- The server logs `"execution persistence request missing shard ID" operation-name=GetWorkflowExecution` (store
  warning, `sql_execution_store.go:106`) 181,679 times in this run, and **0** times in 1a0d. On the counter-23
  timeline, the warning at 150.975 comes 0.1 s before `rpc client2 reset wid=counter-23 -> failed bad_request` at 151.07.
- CLI probe at moment -6058873216720422899 @ 495.52 (`reset-probe.txt`):
  `cadence wf reset -w counter-119 -r b01a5c49… --event_id 5` → `Error: reset failed … execution persistence request missing shard ID`.
  Afterwards `wf desc` shows the run still open and unchanged. The failed reset has no persisted effect. The
  "Workflow execution terminated … antithesis-reset" lines the server logs for these resets are in-memory only, and
  the context is released with the error.
- The unit test `TestCopySignalRequestedIDs` passed only because its `ExecutionMgr.On("GetWorkflowExecution", …)`
  mock expects the same request without `ShardID`.
- The `isResetRunPredecessor` path is not affected: `LoadWorkflowExecution` goes through
  `shardContext.GetWorkflowExecution`, which sets ShardID and RangeID.

**Fix (one line, plus the test):** read through the shard context, which also sets RangeID:
```go
resp, err := r.shard.GetWorkflowExecution(ctx, &persistence.GetWorkflowExecutionRequest{
	DomainID:   domainID,
	Execution:  types.WorkflowExecution{WorkflowID: workflowID, RunID: baseRunID},
	DomainName: domainName,
})
```
Alternatively, keep the current call and add `ShardID: common.Ptr(r.shard.GetShardID())`. Update
`TestCopySignalRequestedIDs` to match the request that carries a shard ID. The same fix applies to
`antithesis/fixes/reset-copies-base-dedup-set.diff` and to `combined-fixverify3.patch`.

**Consequence for this fix-verify:** no reset committed anywhere in the run. Every reset-side patch (v2 dedup,
copy of the base dedup set, reset dedup across continue-as-new, acked-signal-lost-on-reset) went **untested**. The
property "an acknowledged signal survives a reset" passes (3.3k/0) only because nothing was ever reset.

## 1. Signal properties: 114 ("at most once in a workflow that was reset") and 100 ("exactly once")

Because no reset committed, a duplicate in this run **cannot** come from a reset path. The 114 examples are
reported under the reset property only because the driver counts every reset attempt (`w.Resets++` for any outcome,
`driver/main.go:349`).

Incidents for property 114 (every example is at `model.go:319` and has `runs>=2`): counter-82 s303 (51), counter-22
s58 (18), counter-77 s401 (15), counter-75 s435 (10), counter-54 s355 (9), counter-58 s443 (2), and 8 singletons.
For property 100: counter-68 act-s207 (35, duplicate, resets=0, runs 3–6); counter-30 (52, missing, runs=1), with
s61/s59/s63/s62/s109/s77 and others; counter-50 s227 (14, missing); counter-63 and counter-40 (1 each, missing);
counter-21 act-s143 (1, `late_ack`).

Histories sampled (8 incidents, 172 of the 214 examples; `h-*.txt`):

| incident | examples | mechanism | evidence |
|---|---|---|---|
| counter-82 s303 (114) | 51 | Gap B | req `1c89490c` in f4ac23b2 ev13 (16:07:27.992). CAN at 29.923, input `applied` contains s303. The retry with the same req lands on bcd60330 as ev5 (31.106). 17 reset attempts, all `bad_request`. No `RESET_WORKFLOW` event in any run |
| counter-22 s58 (114) | 18 | Gap B | req `c7a6998f` in 01ffecca ev13 (16:03:11.572). CAN at 13.247. The retry lands on a9420062 as ev5 (13.463) |
| counter-77 s401 (114) | 15 | Gap B | req `be4a481f` in 69587fe2 ev16 (16:16:27.828). CAN at 28.066. The retry lands on 712335eb as ev5 (33.701) |
| counter-75 s435 (114) | 10 | Gap B | req `65653198` in 02c67451 ev29 (16:10:24.593). CAN at 24.599. The retry lands on 3cb3e941 as ev3 (24.668) |
| counter-54 s355 (114) | 9 | Gap B | req `773d0988` in a5bbe85c ev10 (16:03:55.092). CAN at 16:04:00.061. The retry lands on d42ba44e as ev5 (01.780) |
| counter-68 act-s207 (100) | 35 | Gap B | req `2d9d9281` in 6ab9045a ev13 (16:07:03.399). CAN at 08.620. The retry lands on 9fd6e2cf as ev5 (09.058) |
| counter-30 s61 (100, missing) | 20 | S3, driver false positive (duplicate start after close) | Run 940d9eed completes at 16:04:40.479. A duplicate start in flight (`start reuse=1 -> ok 66a5b0e1` at vtime 318.04) creates a new run at 16:04:41.132. s61 is acked at 320.24 and lands on 66a5b0e1 ev5. The model read 940d9eed's result |
| counter-50 s227 (100, missing) | 14 | S3 (same) | b4b03fb4 completes at 16:05:39.865, and a new run a6c195fa (no predecessor) starts at 39.864. s227 lands on a6c195fa ev5 |

**Verdict:**
- All sampled duplicates (6 incidents, 138 examples) are **Gap B**: one request ID, applied in run N, carried by the
  CAN input, and applied again in run N+1 when the client retries. That is the gap `signal-dedup-across-can.diff`
  would close, and it was not included.
- The sampled "missing" examples are the known driver false positive S3 (`rca/hunt-signal-once-nonreset.md`).
- None shows a reset path, and none can: there are 0 `DecisionTaskFailed cause=RESET_WORKFLOW` events in any history
  I collected.
- The ~14 unsampled singletons are not classified. They cannot be reset-caused either, because no reset committed
  in the run.
- This run **cannot** show whether the reset dedup patches are complete. That needs a rerun after the shard-ID fix.

## 2. "a cadence server process crashes" (27 examples)

| line / site | node(s) | examples | classification |
|---|---|---|---|
| `panic: … nil pointer dereference`, `stopping:1`, `service/frontend/service.go:341` = `s.adminHandler.Stop()` in `(*Service).Stop` (addr 0x128, an itab slot of a nil `admin.Handler` interface) | cadence1/2/3 | 18 | **site the sigterm patch missed** |
| same panic, `service/matching/service.go:153` = `s.handler.Stop()` in matching `(*Service).Stop` | — | 6 | **site the sigterm patch missed** |
| same panic, `common/rpc/factory.go:206` (`d.peerLister.Unsubscribe`) ← `resource_impl.go:488` ← `frontend/service.go:349` (`s.Resource.Stop()`) | cadence2 (970.1) | 1 | **site the sigterm patch missed** |
| `fatal error: concurrent map read and map write` in `tchannel-go@v1.34.4/json/handler.go:100` (`json.Register` handler map read by an inbound call while handlers are still being registered), `stopping:0`, during startup (cadence2 @ 661.02, right after "matching started" and "Starting hashring") | cadence2 | 1 | new in our data, third-party startup race (tchannel/ringpop), not related to the patches |
| `panic: cannot close connection` (`tools/sql/conn.go:121`) | — | **0** | sql close fix holds (weak evidence: 1a0d had 1 incident, 119 examples) |

The three Stop sites are the same family as the original finding: Stop() reached before Start() assigned the field.
They **pre-exist** in the unpatched baselines. Log-line counts: f04f has frontend:341 ×17, matching:153 ×14,
factory.go:206 ×1; bba4 has 7 / 2 / 0. 1a0d (the earlier partial patch) has 0. They were in the minority there next to
the history `handler.go` and ratelimiter `collection.go` sites, which the patch guards, and which are **gone** here
(0 occurrences). The concurrent-map fatal did not appear in any of the three baselines.

Fix to complete the sigterm patch: guard each remaining Stop dereference.
- `frontend/service.go`: `if s.handler != nil { s.handler.Stop() }` and `if s.adminHandler != nil { s.adminHandler.Stop() }`.
- `matching/service.go`: `if s.handler != nil { s.handler.Stop() }`.
- `common/rpc/factory.go` `Stop()`: skip the Unsubscribe loop when `d.peerLister == nil`.

A more robust fix makes Stop wait for, or skip after, a Start that has not finished. One way is to set a
`started` flag at the end of Start, or to hold a mutex across Start and Stop.

## 3. "every started workflow closes once faults stop" (410 examples, baselines 230 / 255 / 154)

I classified every straggler in all 410 examples (`every_started_workflow_closes_.json`, using the signatures in
`rca/hunt-liveness.md`):

| signature | stragglers | examples containing it |
|---|---|---|
| **C1**: counter, current run `terminated`, resets>0, the driver's terminate sent | 1569 | **408 / 410** |
| C4: the current run is a Foreign closed run | 46 | 40 (always together with C1) |
| C2: parent open, cancel sent (child-cancel-ack-after-close) | 2 | 2 |
| C3: counter open, history length 4 | 1 | 1 |
| L1, or any straggler that is open or stuck in Cadence | 0 | 0 |

Each C1 straggler is a **closed** run (`describe`: closed, `close_status: terminated`) that the driver terminated
itself (`terminate: true`). In 1493 of the 1603 cases it is the workflow's first run. The others are continue-as-new
successors. Not one C1 straggler has a committed reset.

Mechanism: every reset attempt now fails with `bad_request`. The reply still carries `base_run_id`, so the driver
puts that run in `ResetBases` and increments `Resets` (`driver/main.go:347-357`). When the driver later terminates
the workflow, `resultDone` (`model.go:219`) rejects a terminated run that is in ResetBases, forever. That is driver
false positive C1. It runs at about 100% here because **every** reset attempt leaves a ResetBases entry and no reset
ever replaces the base run. In the baselines most resets committed and moved the current run away from the base.

**Verdict:** the increase from 230/255 to 410 is **caused by the patch regression (resets always fail), surfacing
through a driver false positive**. It is not a new Cadence liveness bug: no workflow is stuck open, and every C1
straggler has closed as the driver requested. No signature beyond C1–C4 appears, and none involves the reset-dedup
or acked-lost code paths actually running. This run therefore says **nothing** about the liveness of the reset
patches when resets succeed. Re-verify after the shard-ID fix. The C1 driver fix in `rca/hunt-liveness.md` (the
terminate-reason discriminator) would also remove this noise.

## Per-patch verdicts

| Patch (in combined-fixverify3) | Verdict | Evidence |
|---|---|---|
| v2 reset dedup (reset run's CreateRequestID = reset request ID) | **Not tested.** No reset committed | 0 successful resets |
| reset copies base dedup set (`copySignalRequestedIDs`) | **REGRESSION FOUND**: every reset fails with BadRequest "execution persistence request missing shard ID" | 269/300 sampled resets `bad_request`, 0 `ok`. 181k store warnings. CLI probe reproduces it. Fix: `r.shard.GetWorkflowExecution(...)`, or set ShardID |
| reset dedup across continue-as-new (`isResetRunPredecessor`) | **Not tested** (no reset reached it with a CAN predecessor). Its own read path is correct (it uses the shard context) | — |
| acked-signal-lost-on-reset (reapply flushed events) | **Not tested** in this run (it was verified for its own mechanism in 1a0d) | "survives a reset" passes 3.3k/0, but only because nothing was ever reset |
| stop-during-start nil guards | **Partially verified**: the guarded sites (history handler, frontend ratelimiters) are gone. Three unguarded sites remain: frontend `adminHandler.Stop` (18), matching `handler.Stop` (6), rpc factory `peerLister` (1). All of them pre-exist in the baselines | Stacks in `crash_*.ndjson`, `stacklines.json` |
| sql close no panic | **Verified** (weakly) | 0 `cannot close connection` (1a0d: 119 examples from one incident; f04f: 11 log lines) |
| (not included) signal dedup across continue-as-new | — | All 6 sampled duplicate incidents (138 examples) are Gap B. This patch is still needed for both properties 114 and 100 |

## Recommendation

1. Do **not** ship `combined-fixverify3.patch` as is: it disables ResetWorkflowExecution. Apply the one-line fix to
   `copySignalRequestedIDs` and update its unit test.
2. Add the three remaining Stop guards to the sigterm patch.
3. Rerun fix-verify, ideally with `signal-dedup-across-can.diff` included. Only that run can verify the reset patches
   and check the liveness of the reset paths. Expected outcomes: "a workflow reset succeeds" passes again; the reset
   duplicate and "survives a reset" properties go to about 0 (any remainder would be a reset-after-reset gap); C1
   returns to baseline levels.
