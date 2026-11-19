# RCA: "a closed workflow keeps its outcome" (hunt runs + fix-verify)

Property: `driver/main.go:507` (`RuleDescribeClosed`). After the driver has seen a run close (result read),
it calls `DescribeWorkflowExecution(wid, closedRunID)` and asserts `closed && status == want`. The second
copy of the property, `driver/model.go:225` (a second result read disagrees), has **no** failures in any run.

Raw data: `antithesis/scratchbook/logs/hunt-closed-outcome/`. Files: `fail-<run8>.ndjson` (every failing event),
`closeev-<run8>.ndjson` (failures of "a history read after a workflow closed includes its close event"), `log_*.ndjson`
(timelines), `exec_*`/`pg_*` (commands run at the parent-4 moment).

## Verdict

**One signature, which is a consequence of the known finding `stale-history-after-shard-steal`.** Every failing
example (279 events, 30 workflow IDs, 5 runs) has the same detail shape `{"closed":false,"status":""}`. The describe
reports a run that the driver had seen closed as **running**. It is served by a history host that still holds a
shard item it has lost (range ID superseded) and answers from its cached mutable state. The driver is not at fault:
it describes a pinned run ID that really is closed in Postgres. No new Cadence bug.

New information for the known finding: the stale window is **not always a few seconds**. On bba4d2f0 (parent-4) it
lasted at least **140 s of vtime** (119 → 258.8). Two frontends still returned the completed workflow as running,
with 4 of its 17 events, while Postgres had `state=2 close_status=1 next_event_id=18`. The stale host heals only when
it next writes to that shard. When the host has nothing to write, nothing fences it.

## Signatures table

| signature | f04f3907 v1.4.5 | bba4d2f0 v1.4.5 -b | 3e9720ca master | c26baba5 master -b | 1a0d055c fix-verify | classification |
|---|---|---|---|---|---|---|
| S1: describe of a closed run returns `closed=false` right after the result read. The shard is ping-ponging between two hosts, and the history read just before also lacks the close event. | 44 ev / 4 wid (timer-91 ×39, race-38, timer-53, timer-60) | 82 ev / 9 wid (parent-4 ×34, timer-162 ×17, race-33 ×11, race-7 ×8, race-97 ×5, race-146 ×4, …) | 60 ev / 7 wid (chain-24 ×31, counter-35 ×24, …) | 62 ev / 6 wid (counter-3 ×47, timer-3 ×5, …) | 31 ev / 4 wid (chain-131 ×24, race-25 ×4, parent-55 ×2, timer-40) | known finding `stale-history-after-shard-steal` |
| S1b (same root cause, long window): a later re-describe after a paused history host resumes with a lost shard item | included above (bba4 parent-4) | | | | | known finding (severity note: ≥140 s window) |

Counts are failing assertion events (many are replicated branches of one moment). Of the 30 WIDs, 17 also failed
"a history read after a workflow closed includes its close event" (`0 close events`), the same stale-read signature
as `rca/stale-history-v145.md`. The rest (race-38, race-146, chain-21, counter-325, …) hit the stale host only on
the describe. Two of those that I checked (race-146, counter-325) show the same shard steal just before (below).
**Present on all four hunt runs, on both versions, and on the fix-verify run**, as expected: none of the three fix
patches touch shard ownership or read paths.

## Evidence

### S1, c26baba5…-67-6 (master -b), counter-3, input_hash 5601049405782880159, vtime 111.7446 (shard 1)
```
108.936 cadence3  Shard belongs to current host? true; range 4
109.139 cadence1  Shard belongs to current host? true; range 5
109.076 driver    rpc client1 signal counter-3 finish -> ok
109.765 cadence3  Closing shard: UpdateWorkflowExecution failed due to stolen shard (4 vs 5)  wf counter-3 run e07144fd
109.765 cadence3  re-acquires: range 6
111.089 cadence2  Shard belongs to current host? true; range 7
111.510 ASSERT a workflow ends only as completed ... true (status completed, result 52/97 = want)
111.510 ASSERT a history read after a workflow closed includes its close event FALSE (n=5, "0 close events")
111.745 ASSERT a closed workflow keeps its outcome FALSE {"closed":false,"run":"e07144fd…","want":"completed"}
```
Three hosts claim shard 1 within 2.2 s (ranges 4 → 7). The result read saw the committed completion. The history read
and the describe 0.2 s later went to a host whose shard item had been superseded, and saw the cached running state.

### S1, f04f3907…-67-6 (v1.4.5), timer-91, input_hash 5779779253952039191, vtime 333.6489 (shard 3)
```
325.498 cadence3 range 13; 326.448 cadence2 range 14; 327.249 cadence3 closes (stolen 13 vs 14)
328.249 driver start timer-91 -> run edd9705e
331.895 cadence1 (restarted after stop fault) range 15
332.953 cadence2 closes: UpdateWorkflowExecution stolen (14 vs 15); 333.634 cadence2 re-acquires range 16
333.636 ASSERT history read includes close event FALSE (n=8); 333.649 keeps-its-outcome FALSE (closed=false)
```

### S1, 3e9720ca…-67-6 (master), chain-24, input_hash 2266657897359693102, vtime 147.5711 (shard 2)
Ping-pong between cadence1 and cadence2, ranges 10 → 16, between 142.68 and 147.56 (cadence3 stopped at 138.7).
At 147.562 cadence1 takes range 16 and commits the completion. cadence2 (range 15) still holds its item. The history
read (147.567, n=9, no close event) and the describe (147.571, closed=false) both follow the commit.

### S1, 1a0d055c…-67-6 (fix-verify), chain-131, input_hash -4718126598746973308, vtime 1442.1300 (shard 4)
Ping-pong between cadence1 and cadence2, ranges 61 → 66, between 1441.27 and 1441.29, after `stop cadence2` at 1437.25.
History read at 1441.292 (n=27, no close event). Describe at 1442.130: closed=false.

### S1 (describe only), bba4 race-146 (input_hash -7330800329589208717, vtime 790.3706, shard 8) and c26b counter-325 (input_hash 4783966127998756931, vtime 627.7911, shard 2)
- race-146: cancel acked at 785.33. cadence3 takes range 18 at 786.17. cadence1 closes (stolen 17 vs 18) and takes range 19 at 786.71. Describe at 790.37: closed=false.
- counter-325 (5 resets; the closed run is the last reset run 7b55dbc9): cadence2 takes range 25 at 623.52. cadence1 closes (24 vs 25) and takes range 26 at 623.55. The finish is acked at 623.91. Describe at 627.79: closed=false.

### S1b, bba4d2f0…-67-6 (v1.4.5 -b), parent-4 run fb0f9996, input_hash -6618152698056279952, vtime 258.8111 (shard 4)
Timeline of the 258.81 branch (`log_bba4d2f0_parent-4_late_to119.ndjson` + `log_bba4d2f0_parent-4_late_from119.ndjson`;
the 124.89 branch, `log_bba4d2f0_parent-4.ndjson`, shares the same history up to 119 and fails 5.9 s after the unpause):
```
 11.22  cadence2 owns shard 4 (range 2); parent-4 (started 20.09) is loaded and run on cadence2
 16.10  cadence3 closes its shard-4 item (stolen 1 vs 2)
 71.91  fault: pause cadence2 for 46.9 s (until 119.03)
 87.29  cadence1 acquires shard 4 (range 3)
 98.78  result read: completed, correct result; 103.97 history read includes close event (true)
107.10  ASSERT keeps its outcome TRUE  (describe: closed, completed)
119.03  cadence2 unpaused: its range-2 shard item is still "valid"; no shard-4 log line from it (or anyone) afterwards
196.3   cadence1 killed (restarts); 198.7 postgres stopped 20 s
258.81  ASSERT keeps its outcome FALSE (closed=false)   <- first describe of parent-4 on this branch after 107.1
```
Commands run at the 258.81 moment (debug session 2aeba42c…-67-6, cancelled):
- Postgres (`pg_parent4_258.txt`): `shards.range_id(4) = 3` (no host has acquired shard 4 since 87.3).
  `current_executions(parent-4) = run fb0f9996, state 2, close_status 1`. `executions.next_event_id = 18`.
- `cadence wf desc -w parent-4 -r fb0f9996` through FE 10.20.20.12 and 10.20.20.13: `"CloseStatus": null, "HistoryLength": 4`.
  FE .11 was unreachable at that moment (cadence1 restarting or faulted). `adm wf describe` timed out on every attempt,
  so the historyAddr was not captured directly. The host follows by elimination. Postgres range 3 means no host created
  a shard-4 item after cadence1 at 87.29. cadence1's item died when cadence1 was killed at 196.3, and cadence3 closed its
  item at 16.10. The only live shard-4 item is cadence2's range-2 item from 11.22, which was paused across the completion.
  A 4-event history matches parent-4 before the pause.

## Root cause (known finding, same code in v1.4.5 and master @572e7e2c)
- `service/history/shard/controller.go:301-307`: `getOrCreateHistoryShardItem` returns an existing valid item without
  checking membership. `acquireShards` (`:430-480`) only adds shards and never drops one that this host no longer owns.
- `service/history/engine/engineimpl/describe_workflow_execution.go:48,54` → `execution/context.go:359`:
  `LoadWorkflowExecution` reads the database only when nothing is cached. The range ID is never checked on a read.
  (`poll_mutable_state.go:75-87` is the same for the history read.)
- Fencing happens only on a failed write: a failed `UpdateWorkflowExecution` or the periodic `updateShardInfoLocked`, which
  writes only when ack levels move. A stale host with no work for that shard (parent-4: no pending tasks after completion)
  therefore keeps serving stale reads indefinitely.

## Driver
No driver fix is needed for correctness: the check is the guarantee "a closed run never reads back open". Optional:
add `history_length` to the details (the client already returns it), so that triage can tell the stale-cache case apart
at a glance. Optional: dedupe this property against the history-read close-event property in reports, since both have one cause.

## Severity note for `stale-history-after-shard-steal`
Raise it from "transient, ~4 s" to "can persist for minutes". Seen on all four hunts (both versions, both topologies)
and on fix-verify. Trigger: a history host is paused or partitioned long enough for another host to take its shards,
then resumes. No data is lost (Postgres is correct), but clients see completed → running for ≥140 s.
