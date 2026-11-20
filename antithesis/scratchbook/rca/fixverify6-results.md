# Fix-verify-6 results: run 68cc2e9daf03e54787bfa9e0b8cf7e50-67-6 (combined-fixverify4)

Build: image `cadence-server:fixverify4`, built by `scripts/build-server.sh` from the **whole worktree**
`src/cadence-fixverify3` (v1.4.5 `ecd2b70c` plus its staged and unstaged changes; file mtimes are 20:51Z, before the
20:57Z launch). Property table: `runs/props-68cc2e9d.txt`. Baselines: `props-f04f3907.txt`, `props-bba4d2f0.txt`
(unpatched v1.4.5) and `rca/fixverify5-results.md` (fc87ca1b).

> **Packaging defect in `antithesis/fixes/combined-fixverify4.patch`.** The patch file is byte-identical to
> `git -C src/cadence-fixverify3 diff`, the **unstaged** changes only. It is missing the staged hunks that the image
> did contain: the history `handler.go` Stop/PrepareToStop guards, the frontend `stopRatelimiters` guard,
> `ReplicateWorkflowExecutionSignaled` → `AddSignalRequested` (v1 dedup), and part of `resetter.go`, including the
> acked-lost reapply of flushed events (`getFlushedEvents`, `currentFlushedEvents`) and the `AddSignalRequested` call
> in `reapplyEvents`. A user who applies the patch file to v1.4.5 gets **less** than this run tested. Regenerate it
> with `git -C src/cadence-fixverify3 diff HEAD > antithesis/fixes/combined-fixverify4.patch`. That is 762 lines, 11
> files; the current file has 364 lines and 9 files. The verdicts below apply to the worktree (= the image), not to the
> current patch file.

Raw data is in `antithesis/scratchbook/logs/fixverify6/`:
- `*.json`: every failing example of the four properties (search, limit 999; the totals 107/92/90/86 equal the property counts).
- `h-counter-*.txt`: `wf list` plus the JSON history of every run, collected in cadence1 at the failing moment.
- `tl_c23.ndjson`, `tl_c42.ndjson`: full timelines. `drv_*.ndjson`: driver lines of 90 s slices before other failures.
- `stackframes.json`, `panics.json`, `crash1.ndjson`: crash stacks.

Histories came from debugging session `729151c50c43e538b92c7e0f4c9cb229-67-6`, which I started and then **cancelled**.

## Headline

| Property | fixverify-6 | baselines f04f / bba4 | fixverify-5 |
|---|---|---|---|
| a workflow reset succeeds (Sometimes) | **4.3k hits** | 6.0k / 7.1k | 0 (regression) |
| a signal is applied at most once in a workflow that was reset | **0** | 196 / 280 | 114 |
| an acknowledged signal survives a reset of its workflow | 107 (2 incidents) | 666 / 787 | 0 (vacuous) |
| an acknowledged signal is applied exactly once | 92 | see `rca/hunt-signal-once-nonreset.md` | 100 |
| a query shows each delivered signal once | 90 | | |
| a cadence server process crashes | 86 (39 panics, one site) | 1.4k / 1.4k | 27 |

- The shard-ID fix holds: there are 0 `execution persistence request missing shard ID` lines (fixverify-5: 181,679),
  and resets succeed.
- v2 reset dedup is active: the server logged `Duplicated reset request` 782 times. No history I collected has two
  `RESET_WORKFLOW` events with the same request ID.
- **Not one failing example in the run is a duplicate.** All 92 + 90 examples of the two non-reset signal properties
  are `late_ack`, `unsent` or `missing`. Their `duplicates`/`dups` fields are empty. The reset-duplicate property passes.

## 1. "an acknowledged signal survives a reset of its workflow": 107 examples = 2 incidents, both driver false positives

Only two incidents exist in the whole run, so both were sampled in full: counter-42 (99 examples) and counter-23
(8 examples). Neither one shows a buffered signal flushed into a terminated base, or a retried reset applied twice.
In both, the reset's base run was **not a run that the failing model owned**, and the "lost" signal was never in the
reset's chain.

### counter-42 (99 examples; `missing: s168,s169,s176,s180,s186,act-s192`, resets=1, runs=1, applied_n=3)

History (`h-counter-42.txt`):

| run | how it started | events | close |
|---|---|---|---|
| 5ad5f8a9 | start, req 57e5a879 (21:09:04.633) | s168, s169, s176 | CAN 21:09:26 |
| ba217b3e | CAN from 5ad5f8a9 | s180, s186, act-s192 | CAN 21:09:39 |
| 6076e13c | CAN from ba217b3e, input applied = all six | finish | **COMPLETED 21:10:03.838, applied = [s168, s169, s176, s180, s186, act-s192]** |
| d3fbd6c3 | **new start, no predecessor**, req efc0540a, client1 (21:10:15.140) | — | terminated by reset 21:10:20.596 |
| c771c03f | reset of **d3fbd6c3** @4, req `counter-42-d3fbd6c3-…-4` | s219, act-s221, s223, finish | COMPLETED 21:10:26.123, applied = [s219, act-s221, s223] |

Every "missing" signal is applied, exactly once, in the model's own completed chain. Driver timeline: finish acked at
591.72; `start reuse=1 -> ok d3fbd6c3` at 603.03; `reset -> ok {base d3fbd6c3, point 4}` at 608.50.
The reset could only have been sent before the finish (`RuleReset` requires `!FinishSent`, `driver/main.go:337`).
The client's `reset` RPC describes the ID with an empty run ID (`client/main.go:592`), and under faults that describe
resolved after 21:10:15. By then the current run was d3fbd6c3, which a duplicate start of the same model had created
once its own run completed (the S3 pattern). The model therefore recorded a reset whose base it does not own. Because
it has `Resets>0`, `readResult` reads the current run (`run_id=""`, `main.go:446-448`), gets c771c03f, and
`resultDone` accepts it: the Foreign check (`model.go:214`) looks only at the run that was read (c771c03f), not at the
foreign run that the reset was based on. **Cadence is correct.**

### counter-23 (8 examples; `missing: s66`, resets 1–6, runs=1)

History (`h-counter-23.txt`):

| run | how it started | events | close |
|---|---|---|---|
| c6c666e2 → 8175d687 | signal_with_start, CAN | s21, s25, s30, s31 | COMPLETED 21:02:01.606 |
| 21961a69 | reuse start, req f05670fc (21:02:07.786) | cancel requested 21:02:10.983 | **CANCELED 21:02:12.996** |
| 0abe1ff8 | **new start, no predecessor**, req cffbb97f, client2 (21:03:12.678) | **s66 ev3 (16.164)** | terminated by reset 21:03:16.213 |
| 5dc2ac24 | reset of **21961a69** @4, req `counter-23-21961a69-…-4` | — | terminated by second reset 16.667 |
| 396b959d | reset of 5dc2ac24 @7 | s71, finish | COMPLETED 21:03:19.582, applied = [s71] |

The reset named 21961a69, which had been closed (CANCELED) for a minute. The frontend logged that reset request
(`ResetWorkflowExecution wf-run-id=21961a69`, retried after "client connection is closing") at 21:03:11.624, before
0abe1ff8 existed. It committed at 16.213. By then the driver's own `start reuse=1` (client2, ok at 183.91) had created
0abe1ff8, an unrelated run with no continue-as-new link to 21961a69, and s66 had been applied to it. As
`ResetWorkflowExecution` is specified, the reset terminated the current run and rebuilt from the named base. Signals
are reapplied only along the base's continue-as-new chain (`resetter.go` `reapplyResetAndContinueAsNewWorkflowEvents`),
and 0abe1ff8 is not on that chain.

The failing model is 21961a69's model. Its details (applied_n = 1 = s71, only s66 missing, `resets>=1`) fit this
reading: the reset, a duplicate start, and the cancel were sent back to back while the run was open (≈116–119 s). The
cancel returned at once. The reset and the duplicate start sat in faulted clients for about 65 s. The model never
closed, because `resultDone` returns false while `ResetsPending>0` (`model.go:221`). It kept signalling (s55
`not_exists`, then s66), and s66 reached 0abe1ff8, the run created by its own duplicate start (S3). The driver's
send times are not logged, so the exact send order is inferred. The server-side facts above are not.

### Verdict for item 1

- **No remaining gap in the patch.** In neither incident was the signal in the base run, in a run on the base's
  continue-as-new chain, or in a buffered batch. Both show the reset-base-not-the-current-run shape, and in both the
  current run was a foreign run created by the driver's own duplicate start. That is test-code behaviour (S3 combined
  with reset bookkeeping), not Cadence.
- **acked-signal-lost-on-reset fix: verified.** The property fell from 666/787 to 107, and all 107 are the two driver
  false positives. **retried-reset-applied-twice (v2 + `isResetRunPredecessor`): verified.** There are 782
  `Duplicated reset request` dedups and no double `RESET_WORKFLOW` in the sampled histories.
- Optional Cadence hardening (not a bug fix; it changes semantics, so do not include it in the fix patch): when
  `reachedCurrentRun` is false at the end of `reapplyResetAndContinueAsNewWorkflowEvents`
  (`service/history/reset/resetter.go`, after the CAN loop, around line 627), the terminated current run's signals are
  dropped. Upstream's own TODO at the top of that function ("fetching all workflow [baseWorkflow, currentWorkflow]")
  covers this. A sketch:
  ```diff
  -	if reachedCurrentRun {
  -		if err := r.reapplyEvents(resetMutableState, currentFlushedEvents); err != nil {
  -			return err
  -		}
  -	}
  +	if !reachedCurrentRun && currentRunID != "" {
  +		// the current run is not a continue-as-new descendant of the base (e.g. a new start
  +		// on a reused ID): carry its persisted signals too, instead of silently dropping them
  +		if err := r.reapplyWorkflowEvents(ctx, resetMutableState, domainID, workflowID, currentRunID,
  +			currentBranchToken, common.FirstEventID, currentNextEventID); err != nil {
  +			return err
  +		}
  +	}
  +	if err := r.reapplyEvents(resetMutableState, currentFlushedEvents); err != nil {
  +		return err
  +	}
  ```
  (Check the `reapplyWorkflowEvents` signature against the file; this is a sketch, not a tested patch.)
- **Driver fix (recommended):**
  1. `client/main.go` `reset`: accept an expected `run_id` from the driver and send it in the describe. Fail
     (`not_current`) instead of resetting whatever run is current.
  2. `driver/main.go` `RuleReset` (line 337): also require `w.DupPending == 0 && len(w.Foreign) == 0` and `!w.Closed`.
     In the reset callback, when `rv.Base` is in `w.Foreign` or is not one of `w.RunIDs`, mark the returned `run_id`
     as Foreign and do not count the reset.
  3. Apply the S3 fix from `rca/hunt-signal-once-nonreset.md`: `RuleSignal` skips models with `DupPending>0 ||
     len(Foreign)>0`.

## 2. "an acknowledged signal is applied exactly once" (92) and "a query shows each delivered signal once" (90): no duplicates

**Duplicates: 0 of 182 examples.** All 92 + 90 have empty `duplicates`/`dups`. In the baselines, the duplicate
signature S2 (signal dedup not carried across continue-as-new) was 41–253 per run, and Gap B was the whole duplicate
residual of fixverify-5 (138 sampled examples). Carrying `pendingSignalRequestedIDs` in `AddContinueAsNewEvent` (plus
the v1/v2 reset dedup) removes it.

Breakdown:

| signature | examples | incidents | class |
|---|---|---|---|
| `late_ack:true` (applied-once) | 78 | counter-2 s16 (43), counter-160 s636 (29), counter-179, -97, -53, -1 | **S1** driver reuse false positive |
| `unsent:[id]` (query) | 90 | counter-110 s381 (28), counter-2 s16 (42), counter-3 act-s3 (12), 8 singletons | **S1** |
| `missing:[…]`, resets=0 | 14 | 11 workflows (one example each, except counter-53 ×5) | **S3** duplicate start, see below |

Sampled S1 (driver timelines): counter-2: `start reuse=1 -> ok dfadc242` at 41.12, s16 acked after it, assertion at
43.29. counter-160: `start reuse=1 -> ok e53af9d9` at 706.16, s636 late-acked at 708.57. counter-3: `terminate -> ok`
39.33, `start reuse=0 -> ok a2ef54eb` 39.34, `act-s3 -> ok` 39.34, then `unsent act-s3` 39.55. counter-110: s381 (an
ID older than the model's s424) acked at 434.99 under the new model. Each matches `rca/hunt-signal-once-nonreset.md` S1.

Sampled `missing` (driver timelines plus histories):

| incident | evidence | class |
|---|---|---|
| counter-96 (`missing: s588, s612, act-s613, s615, act-s616, s617`, runs=1) | `h-counter-96.txt`: the model's run 59bf51c1 COMPLETED 21:04:49.732 with s576…s584. A run with **no predecessor**, 745a3a28 (start req 8fae4d6f), started 21:04:49.643 and holds all six "missing" signals (ev5 s588 … ev14 s617; each applied once). It COMPLETED 21:06:28 applied=[s588, act-s616, s615, act-s613, s612, s617]. The driver issued about 40 duplicate starts on counter-96, some with unknown outcomes | S3 |
| counter-45 (`missing: s72`, runs=2) | `h-counter-45.txt`: model chain 57396fa8 → 2bab9f0b (CAN) COMPLETED 21:04:27.863 with s29, s33, s46, s53. s72 is ev5 of a7900289, a fresh start (req cc71e54d, no predecessor, 21:04:36.137) that the driver saw as `start reuse=1 -> ok a7900289` at 264.04 | S3 |
| counter-39 (`missing: s165, act-s168, s169`, runs=2) | `h-counter-39.txt`: model chain 84d8eb78 → 01da0566 COMPLETED 21:09:34.148 with s148, s154, s159. The three signals are in 4ff87d94, a fresh start at 21:09:39.807 (`start reuse=1 -> ok 4ff87d94` at 567.79), which the driver's terminate then closed | S3 |
| counter-89 (`missing: s237`) | driver: `finish -> ok` 172.284, `start reuse=1 -> ok 24353c4e` 172.290 (a duplicate start that raced the finish), `s237 -> ok` 172.294 | S3 (log only) |
| counter-35 (`missing: act-s88`) | driver: finish 196.83, `start reuse=1 -> ok 0842f367` 197.42, `act-s88 -> ok` 212.04 | S3 (log only) |
| counter-53 (`missing: s196`, runs=2; also a `late_ack` row) | `h-counter-53.txt`: model chain 44ae8448 → f69c1e2d COMPLETED 21:07:30.002 with s135, s147, s154, s170, s180. s196 is ev5 of 4e384afb, a fresh start (req 2bf43922, no predecessor, 21:07:34.497) created by one of the two duplicate starts that ended in `unknown timeout` (443.09, 453.14) | S3 |

In the four history-checked incidents, every "missing" signal is applied exactly once, in a run that has no
continue-as-new link to the model's chain and was created by a start the driver issued on the same ID. So carrying the
dedup set across continue-as-new does **not** cause these losses: the signals are not suppressed, they are in another
run. The remaining 5 `missing` singletons (counter-108, -38, -80, -216, -42 s225) were not opened. They have the same
shape (resets=0, runs=1–2).

**Verdict for item 2:** the duplicates are gone. Everything left is the known driver false positives S1 (late ack or
unsent after an ID reuse) and S3 (a signal routed to a run created by the driver's own duplicate start). The fixes are
in `rca/hunt-signal-once-nonreset.md`.

## 3. "a cadence server process crashes" (86 examples): one remaining unguarded site

All crash output in the run (search over cadence1–3) is **39 panics, every one
`panic: runtime error: invalid memory address or nil pointer dereference`, every one with the same stack**:

```
github.com/uber/cadence/service/frontend/api.(*WorkflowHandler).UpdateHealthStatus(...)   service/frontend/api/handler.go:179
github.com/uber/cadence/service/frontend.(*Service).Stop                                  service/frontend/service.go:335
github.com/uber/cadence/cmd/server/cadence.(*server).Stop                                 cadence/server.go:116
… fx lifecycle runStopHook
```
Example: cadence3 at vtime 662.20 (`crash1.ndjson`). The fx OnStop hook ran 0.08 s after "matching starting", and
`Updating rpc health status to ShuttingDown` came right before the panic. There is no `fatal error`, no
`cannot close connection`, and no other panic text.

| site (log-line count) | f04f baseline | fixverify-5 | fixverify-6 |
|---|---|---|---|
| frontend/service.go:335 `s.handler.UpdateHealthStatus` | 4 | 0 | **39** |
| frontend/service.go:341 `adminHandler.Stop` | 17 | 18 | 0 |
| matching/service.go:153 `handler.Stop` | 14 | 6 | 0 |
| rpc/factory.go:206 `peerLister.Unsubscribe` | 1 | 1 | 0 |
| history handler.go / ratelimiter collection.go | ~1.4k (examples) | 0 | 0 |

**Gap (file:line):** `service/frontend/service.go:335` (worktree and v1.4.5 alike). Before the guarded
`s.handler.Stop()` at 340-342, `Stop()` already calls `s.handler.UpdateHealthStatus(...)` through the same possibly-nil
`*api.WorkflowHandler` (it is assigned at `service.go:141` in `Start`). The new nil check at 340 is therefore dead code
for the case it targets: a Stop that comes before `Start` reaches line 141 now panics 5 lines earlier. The site already
existed in the baseline (4 lines). It became the only site because every later one is guarded.

```diff
--- a/service/frontend/service.go
+++ b/service/frontend/service.go
@@ -332,7 +332,9 @@ func (s *Service) Stop() {
 	failureDetectionTime := max(0, s.config.ShutdownDrainDuration()-requestDrainTime)
 
 	s.GetLogger().Info("ShutdownHandler: Updating rpc health status to ShuttingDown")
-	s.handler.UpdateHealthStatus(api.HealthStatusShuttingDown)
+	if s.handler != nil { // nil if Stop arrives before Start finished
+		s.handler.UpdateHealthStatus(api.HealthStatusShuttingDown)
+	}
```
Latent, not observed: `service/history/service.go:156` (`s.handler.PrepareToStop`) and `:161` (`s.handler.Stop()`)
dereference the history service's `s.handler` interface, which `Start` sets at `:107`. The window before that line is
small (only the workflow-ID cache is built first), but the same guard belongs there. A structural fix would make each
`Service.Stop` return early, or wait, unless `Start` has completed (a `started` flag or channel set at the end of
`Start`).

## Per-patch verdicts

| Patch (worktree = image) | Verdict | Evidence |
|---|---|---|
| reset copies base dedup set (shard-scoped read, the corrected fixverify-5 regression) | **Verified** | 0 missing-shard-ID lines (fixverify-5: 181,679). "a workflow reset succeeds" hit 4.3k times. Duplicates in reset workflows: 0 |
| v2 reset dedup (CreateRequestID = reset request ID) | **Verified** | 782 `Duplicated reset request` lines. No double `RESET_WORKFLOW` with one request ID in any collected history. "survives a reset" has no retried-reset incident |
| reset dedup across continue-as-new (`isResetRunPredecessor`) | **Verified (indirectly)** | No residual of its shape. Its specific path (reset run already continued as new) is not separately attributable in the logs |
| v1 signal dedup (`AddSignalRequested` on reapply and replicate) | **Verified** | "a signal is applied at most once in a workflow that was reset" passes 3.8k/0 (baselines 196/280, fixverify-5 114) |
| signal dedup across continue-as-new (`AddContinueAsNewEvent` carries `pendingSignalRequestedIDs`) | **Verified** | 0 duplicates in 182 examples of the non-reset signal properties (S2 was 41–253 per baseline run; Gap B was the entire fixverify-5 duplicate residual). No sampled `missing` comes from over-deduplication |
| acked-signal-lost-on-reset (reapply flushed buffered events) | **Verified** | 107 examples = 2 incidents, both driver false positives (reset base not the model's run; signal in a foreign run). 0 buffered-and-flushed losses |
| stop-during-start nil guards (history, frontend ratelimiters, frontend handlers, matching, rpc factory) | **Partially verified; remaining gap** | Every guarded site is at 0. 39/39 panics are at the unguarded `frontend/service.go:335` `s.handler.UpdateHealthStatus`. Diff above |
| sql conn close no panic | **Verified** (weakly) | 0 `cannot close connection` |
| `combined-fixverify4.patch` (the file) | **Packaging defect** | Contains only the unstaged diff. Regenerate with `git diff HEAD` |

No regression found. The other failing properties in `props-68cc2e9d.txt` (stale history, reorder, liveness C1–C4,
reuse policy, second start) are the known findings and driver false positives listed in `report-facts.md`. They were
out of scope here.
