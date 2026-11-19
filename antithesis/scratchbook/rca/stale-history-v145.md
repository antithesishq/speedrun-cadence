# RCA: "a workflow history is well formed" fails with "0 close events" (chain-17, fix-verify build)

Run `41bf06723b005a4e1c4cc37b560fb195-67-6` (cadence-fixverify = v1.4.5 + `antithesis/fixes/*.patch`; the
patches touch signal dedup on reset and nil guards in `Stop()`, not history reads). The property has one failing example:
input_hash `-2175461847364005768`, vtime 96.5063, `{"n":5,"problems":["0 close events"],"run_id":"496d4621-…","wid":"chain-17"}`.
Raw data is in `antithesis/scratchbook/logs/stale-v145/`: `full.ndjson` (the whole timeline up to 97.47), `exec1.txt`/`exec2.txt`
(`wf desc`, `wf show` and `adm wf describe` through each frontend at 96.506), `pg1.txt` (Postgres at 96.506),
`exec3.txt` (the same reads 10 s and 20 s later), and `events10.ndjson` (events in the 10 s after the moment).

**Verdict: same mechanism as `rca/history-missing-close-event.md`. Classification: suspect** (real Cadence
behaviour, not the harness and not the target bug). For a short time two history hosts both held shard 3. The
host that had lost the shard (cadence1, range 6) answered `PollMutableState` from a stale cached mutable state
(NextEventID 6, running). Meanwhile the real owner (cadence2, range 7) had already committed the completion. The
trigger differs from the earlier run. Here there was no partition: the faults had been stopped at 92.34 and all three nodes
were up. The split came from ring convergence while cadence1 rejoined after a restart.

## Facts

- chain-17 (run `496d4621`, the only run, started 93.01 by client3) is on **shard 3**: both the logs and `adm wf describe` show `shardId 3`.
- Postgres at 96.506 (`pg1.txt`): `shards.range_id(3) = 7`. `executions.next_event_id = 12`. `current_executions` has
  state 2 and close_status 1 (completed). History nodes 1, 3, 4 were written in range 4's block, and nodes 6, 9, 10 in range 7's block
  (`txn_id` −7340038 … −7340046). The completion is committed by the range-7 owner.
- The driver reads the result with `run_id = FirstRunID` and a 3 s timeout (`driver/main.go:438-445`). It got `completed`, 979000 = want
  (assertion "a completed workflow returns the result its inputs determine", HIT at 96.50575). The history read for
  the same run ID followed 0.6 ms later (`driver/main.go:461`) and got 5 events (96.50631).

## Timeline (vtime; shard 3)

| vtime | event |
|---|---|
| 90.29 | cadence1's server fails to start (`creating operational dynamic config store: timeout error on fetch`, the node-exits issue). It restarts around 94.5. |
| 92.11-92.78 | Fault kill of cadence3, which restarts. 92.34: driver `eventually: stopping faults`. No faults are injected after this. |
| 92.57 | cadence3: "Shard belongs to current host? true", range **4**. chain-17's first events and its activity-retry state are written in range 4. |
| 93.58 | cadence2 (its ring is {12,13}): "Shard belongs… true", range **5**. |
| 94.03 | cadence3: `UpdateWorkflowExecution failed due to stolen shard` (4 vs 5). It closes the shard. The ring now says the owner is .12. |
| 94.61 | cadence1 history starts. Its ring is {11,12,13}: "Shard belongs to current host? true" (owner 10.20.20.11), range **6**. cadence1 loads chain-17 into its cache (NextEventID 6, activity 5 scheduled, attempt 1). |
| 94.637 | cadence2 writes chain-17 (`update-wf-execution`) and the write fails: "Previous range ID: 5; new range ID: 6". It closes shard 3 and recreates it. Its ring does **not yet contain cadence1**, so "Shard belongs to current host? true" (owner .12), and cadence2 takes range **7**. |
| ≈94.66 (ts 20:35:05.814) | cadence2 (range 7) runs the activity retry and completes chain-17: events 6..11, `WorkflowExecutionCompleted`. cadence1 makes no write to shard 3, so it does not learn that it lost the shard. |
| 94.977 | cadence2's history ring adds 10.20.20.11:7934, and "Acquiring shards" runs. That pass only *adds* shards that this host owns. It does not drop shard 3, so cadence2 keeps serving it as well. |
| 96.5057 | Result read returns `completed`. It must have been served by cadence2: a close-event long poll on cadence1 would block, because cadence1's cached state is running. |
| 96.5063 | History read is served from cadence1's cache, which returns events 1..5 and no close event. checkHistory fails. |
| 98.89 | Healing: cadence1's periodic `updateShardInfoLocked` fails ("Previous range ID: 6; new range ID: 7"), and cadence1 closes shard 3. At +10 s and +20 s (`exec3.txt`) every frontend routes to .11 and gets cache = DB = completed, NextEventID 12. |

## State at the failing moment (`snouty runs exec` at 96.5063, from cadence1)

```
FE 10.20.20.11 -> historyAddr 10.20.20.11:7934, shard 3
  mutableStateInCache    State 1 (running) NextEventID 6  LastEventTaskID 4194347 (range 4)  activity 5 pending
  mutableStateInDatabase State 2 Close 1   NextEventID 12 LastEventTaskID 7340045 (range 7)
FE 10.20.20.12 -> historyAddr 10.20.20.11:7934   (identical to the above)
FE 10.20.20.13 -> historyAddr 10.20.20.12:7934, shard 3
  mutableStateInCache    State 2 Close 1 NextEventID 12
  mutableStateInDatabase State 2 Close 1 NextEventID 12
```
`wf desc` through .11 and .12 returns `CloseStatus null, HistoryLength 5, pending activity SCHEDULED id 5`. `wf show` returns events 1..5.
`wf desc` through .13 returns `CloseStatus COMPLETED, HistoryLength 11`. The frontends disagree on the shard owner (2 of 3 route to cadence1),
and both history hosts hold a live engine for shard 3.

Which frontend served which read is not logged: the client round-robins `CADENCE_FRONTENDS` (`client/main.go:59-66`).
It is still determined by the results. Only the cadence2 path could return a completion. The 5-event history equals
cadence1's cached NextEventID 6 exactly, and cadence1 serves through FE .11 and FE .12.

## Root cause (src/cadence-v1.4.5)

- `service/frontend/api/handler.go:1903-1915`: `GetWorkflowExecutionHistory.queryHistory` asks history for
  `PollMutableState` and takes `NextEventID` and the running/closed state from that reply. At `:2077-2086` ("return all events")
  it reads the history branch from the database only up to that `NextEventID`. The events are in the database (nodes 6..10)
  but are cut off by the stale bound.
- `service/history/engine/engineimpl/poll_mutable_state.go:75-87`: `getMutableState` takes the answer from
  `executionCache.GetOrCreateWorkflowExecution` + `LoadWorkflowExecution`. At `:219` it returns at once, because the
  expected next event ID is less than the cached NextEventID.
- `service/history/execution/context.go:359`: `LoadWorkflowExecutionWithTaskVersion` reads the database only when
  `c.mutableState == nil`. A cached state is returned with no check of the shard's range ID.
- `service/history/shard/controller.go:301-307`: `getOrCreateHistoryShardItem` returns an existing valid shard item
  without consulting membership. The ownership check at `:331-333` runs only when an item is created.
  `acquireShards` (`:430-480`, test at `:465`) only adds the shards a host owns and never releases one it no longer
  owns. Only a failed write fences a host that lost a shard: a failed `UpdateWorkflowExecution`, or the periodic
  `updateShardInfoLocked` (`shard/context.go`, `history.shardUpdateMinInterval` = 1 s in this harness, which in practice ran
  about every ack update, 5 s). Reads are never fenced.

**src/cadence-master (572e7e2c):** no change on this path. `poll_mutable_state.go`, `execution/context.go`,
`execution/cache.go` and `shard/controller.go` are identical to v1.4.5. The `frontend/api/handler.go` diff only adds the
semaphore handler and poll tracker and leaves `queryHistory` unchanged. The `shard/context.go` diff only adds
`EnqueueAsyncWorkflowMessage`. The behaviour is the same on master.

## Trigger (minimal) and client-visible impact

**Trigger:** while ring membership is converging, history host A takes shard S (range n) and loads workflow W into its
mutable-state cache. Host B, which has not yet seen A in its ring, fails one write, sees itself as owner again, steals S
(range n+1) and advances W. A then makes no write to S. A keeps a valid shard item and a cached W, and frontends whose ring
maps S to A send reads of W to A. Here a partition is not needed: one node restarting and rejoining was enough
(cadence1 at 94.6), and the fault injector had already stopped.

**Impact:** during the window (≈94.66 → 98.89, about 4 s of vtime here), `GetWorkflowExecutionHistory`, `DescribeWorkflowExecution`
(and so `wf show`, `wf desc`) through 2 of the 3 frontends report a workflow that has already completed as **running**,
with a shorter history (5 of 11 events, no close event, an activity still "scheduled"). Meanwhile the third frontend and
visibility report it as completed. A client can see `completed` and then, on the very next call, `running`. This
breaks monotonic reads and read-your-writes across calls. No data is lost or corrupted: the database is correct, and the
stale host heals itself when its next shard write fails.

## False-positive review

- Client/driver use is correct: a pinned run ID, the history read made after the result read, round-robin across frontends.
- The check is stricter than documented semantics: Cadence does not promise linearizable reads across shard movement.
  This is why it is classified **suspect** and not real, the same as the pre-fix RCA. It is a genuine user-visible inconsistency.
- The fix-verify patches are not involved (no reset, no signal on chain-17, and no `Stop()` before `Start()` at this point).
- Harness: the restart of cadence1 comes from its startup failure on a dynamic-config fetch timeout (node-exits issue). That makes
  rejoin churn more frequent, but it is a normal operational event (a node restarting), not a misconfiguration.
