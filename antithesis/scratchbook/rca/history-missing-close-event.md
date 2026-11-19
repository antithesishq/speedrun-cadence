# RCA: "a workflow history is well formed" fails with "0 close events" (parent-10, parent-7)

Run `22fa5e0b537415326992eb290a37b6da-67-6` (cadence-prefix, c889f8319, before PR #8595; Postgres;
high_availability faults). The run was cancelled during this investigation. Raw data is in
`antithesis/scratchbook/logs/hist-close/` (`exec1.txt`, `pg1-3.txt`, `adm1.txt`, `adm2.txt`,
`p7_slice.log`). The full timeline for parent-10 is in `logs/prefix1/hist.log`.

**Classification (parent-10, parent-7): suspect.** Cadence did this, not the harness, but it is not
the target bug. The workflow did complete. The completion is committed in Postgres and served
correctly by the history host that owns the shard. The history read went to a second history host
that also believed it owned the shard, after ringpop membership split. That host served
**stale cached mutable state** (NextEventID 32, decision started), so the frontend returned only
events 1..31. Shard ownership in Cadence fences writes with the range ID but not reads. A host that
lost the shard keeps serving cached state until one of its own writes fails. A client can see
"completed" and then, on the next call, see "running" with a shorter history. This is a
monotonic-read and read-your-writes violation across calls, and the logic is the same in v1.4.5 and master.

**No example matches the known-bug signature** (a timer resolved twice: TimerStarted followed by both
TimerCanceled and TimerFired). Also, **the checker cannot currently detect that signature**: see
"Harness gap" below.

## Failing examples of the property (8 at the time of the query; the request said 7)

From `snouty runs search` (assert condition false):

| # | input_hash | vtime | wid | n | problems |
|---|---|---|---|---|---|
| 1 | -1535614395710740674 | 251.30 | parent-10 | 31 | `0 close events` |
| 2 | -8174707967280476638 | 253.25 | parent-7 | 18 | `0 close events` |
| 3 | 8687931440270089545 | 674.89 | counter-59 | 33 | `signal s259 recorded twice (events 11 and 15)`, `signal act-s257 recorded twice (events 10 and 19)` |
| 4 | -2885396379798079575 | 674.89 | counter-59 | 33 | same as 3 |
| 5 | 2798997212142570157 | 675.01 | counter-59 | 33 | same as 3 |
| 6 | -8328623451406401185 | 675.02 | counter-59 | 33 | same as 3 |
| 7 | -8781410378924335531 | 679.27 | counter-59 | 33 | same as 3 |
| 8 | -510454931118251611 | 695.79 | counter-59 | 33 | same as 3 |

There are two signatures:
- **A. "0 close events"** (parent-10, parent-7). This RCA covers it.
- **B. duplicate signal** (counter-59, six branches of one timeline). The duplicates landed in
  `problems`, not in `resetDups`, so the driver did not know of any reset of counter-59
  (`model.go` checkHistory, `wasReset` false). That makes it a different case from
  `rca/reset-duplicates-signal.md`. It was **not investigated here** and needs its own RCA. It may come
  from the same shard split-brain, or from the client retry path, as in the reset RCA.
- **None** has `timer N resolved twice` or `... which is not a started timer`.

## parent-10: sequence

wall ≈ vtime + 19:22:53.14 (cadence1 logged ts 19:26:04.372 at vtime 251.23).
cadence2 has been dead since vtime 61 (startup failure, see `rca/node-exits.md`). Only cadence1 and
cadence3 run history, and each runs its own frontend.

| vtime | event |
|---|---|
| 124.7-126.0 | parent-10 is started (client2). Its run is `221b5f55-…`, the only run (`wf list` and the `executions` table have one row). An earlier create attempt on shard 6 (run fd95861b) failed with `driver: bad connection` and was never persisted. |
| 229-246 | Partitions and clogs (`network/partition Jammed nodes=ALL` at 229.5 and 237.5; clogs on cadence1). Ringpop on cadence1 and cadence3 disagrees: **both log "Shard belongs to current host? true" for shard 6** (and shard 0). |
| 240.9-247.5 | Shard 6 ping-pongs between the two. Each acquire bumps range_id (11 c3, 12 c1, 13 c1, 14 c3, 15 c1, 16 c3, 17 c1, 18 c3). The other host finds out only when one of its writes fails ("Closing shard: UpdateWorkflowExecution failed due to stolen shard"). |
| ≈247.3 | **cadence1 (range 17)** records event 31 `DecisionTaskStarted` (history_node 31, txn_id 17825799, in range 17's block). cadence1's cached mutable state is NextEventID=32, decision 30 started. |
| 247.465 | cadence3 (range 16) tries to write events 32-33 (history_node 32, txn 16777229). The execution update fails: range 16 vs 17. |
| 247.47 | **cadence3 acquires range 18** and then commits decision completion and `WorkflowExecutionCompleted` (history_node 32, txn 18874370, range 18's block). DB `executions.next_event_id=34`, `current_executions.state=2 close_status=1`. Visibility shows COMPLETED with 33 events, end 19:26:00. cadence1 makes no further write on shard 6, so it **never learns** that it lost the shard. |
| 251.246 | Driver `result` call: the Go client's `GetWorkflow("parent-10","").Get` returns **completed** with result 54003 = want (children 20001+16001+18001). The client round-robins frontends (`client/main.go:62-66`), and this long-poll must have hit cadence3's frontend. `run_id` in the result is `""`: the follow-up `DescribeWorkflowExecution` in `getResult` (`client/main.go:443-446`) either failed or saw an open run (as the stale host would report), so no run ID was recorded. |
| 251.300 | Driver `history` call, runID `""` (`driver/main.go:400-403`), goes to a frontend that routes to cadence1. cadence1 returns 31 events with no close event, and checkHistory reports **`0 close events`**. |

### Evidence at the failing moment (`snouty runs exec`, 251.30)

```
FE 10.20.20.11 (routes to history 10.20.20.11:7934), shard 6
  mutableStateInCache    NextEventID 32, State 1 (running), DecisionStartedID 31
  mutableStateInDatabase NextEventID 34, State 2, CloseStatus 1 (completed)
FE 10.20.20.13 (routes to history 10.20.20.13:7934), shard 6
  mutableStateInCache    NextEventID 34, State 2, CloseStatus 1
  mutableStateInDatabase NextEventID 34, State 2, CloseStatus 1
```

`wf desc` through 10.20.20.11 returns `CloseStatus: null, HistoryLength 31, PendingDecision STARTED ScheduleID 30`.
`wf desc` through 10.20.20.13 returns `CloseStatus COMPLETED, HistoryLength 33`. `wf list` (visibility) returns COMPLETED, 33 events.
`shards.range_id` for shard 6 is 18, which is cadence3's. `wf show` through 10.20.20.11 returns events 1..31 and nothing after 31.

### Answers to the questions

- **Status the result call reported:** `completed` (`resultDone` HIT "a completed workflow returns the
  result its inputs determine", got 54003 = want 54003; cancel_sent=false, terminate_sent=false).
- **Different run?** No. parent-10 has exactly one run (221b5f55). Children are separate workflow IDs
  (`parent-10/c0..c2`), and runID `""` resolves to the same run. The result is consistent with this run's children.
- **Stale history read or misreported result?** **The history read is stale.** The result is correct: the
  database and the real shard owner both have the completion. The read went to a history host (cadence1)
  that kept a stale shard context and mutable-state cache after ringpop split and the shard was stolen at range 18.

## Root cause (code, src/cadence-prefix; the same file is identical in v1.4.5 and master)

- `service/frontend/api/handler.go:1895-1945` (`GetWorkflowExecutionHistory.queryHistory`) asks
  history for `PollMutableState`, then reads the history branch only up to the returned `NextEventID`.
- `service/history/engine/engineimpl/poll_mutable_state.go:75-105` (`getMutableState`) answers from
  `executionCache.GetOrCreateWorkflowExecution` + `LoadWorkflowExecution`.
- `service/history/execution/context.go:350-372` (`LoadWorkflowExecutionWithTaskVersion`) reads the
  database only when `c.mutableState == nil`. A cached mutable state is returned as is, and nothing checks
  that this host still holds the shard's range ID.
- `service/history/shard/controller.go:332-333` decides ownership from the local ringpop view.
  Under a partition, two hosts each think they own the shard. Range ID fences only writes
  (`Failed to lock shard. Previous range ID …`), so the host that loses the shard keeps answering reads
  from cache until one of its own writes fails.

## False-positive review

- **Client/driver misuse:** none. Reading history with an empty runID after a completed `Get` is
  normal use. The check `wantClosed && nClose != 1` (`driver/model.go:398-400`) asks only that the history of a
  workflow that the server just reported closed contains its close event.
- **Checker stricter than Cadence semantics?** Arguably. Cadence does not document linearizable or monotonic
  reads across shard movement, and stale reads during membership disagreement follow from its
  design (writes fenced, reads not). That is why this is **suspect** and not real. It is a genuine,
  user-visible inconsistency (the same frontend reported "running" for at least 8 more vtime seconds after
  completion, and it does not heal until cadence1 writes to shard 6), but it is a known class of behaviour, not a regression.
- **Harness/topology:** cadence2 dead from vtime 61 (the node-exits issue) leaves a two-node ring, which
  makes split-brain easier to reach. It does not cause it.
- If the driver should tolerate this, it could retry the history read until it sees a close event, or
  record and use the run ID. Such a change would hide this observation instead of fixing anything.

## parent-7 (example 2)

Same signature: n=18, `0 close events`, result HIT "completed" at 253.18 just before. Logs
(`p7_slice.log`, 230-253.25) show the same pattern on **shard 0**. cadence1 and cadence3 alternately
steal it (range 10…21, with "Shard belongs to current host? true" on both) up to 253.25, with failing
`parent-7` updates on both hosts. It could not be confirmed by exec, because the run was cancelled before it was checked.
Likely the same root cause: **suspect**.

## Harness gap: the known-bug signature (#8595) is not detectable

`client/main.go` getHistory (≈ lines 485-537) sets `Ref` for `TimerFired` (`:516-518`) but has **no
case for `EventTypeTimerCanceled`**, so a TimerCanceled event has `Ref == 0`. checkHistory skips
events with `Ref == 0` (`driver/model.go:426`, `if kind, ok := resolveOf[e.Type]; ok && e.Ref != 0`).
As a result, "TimerStarted followed by TimerCanceled and TimerFired" is **never** reported as "timer N resolved
twice". Only a doubled TimerFired would be. Fix: add
`case s.EventTypeTimerCanceled: ev.Name = …GetTimerId(); ev.Ref = e.TimerCanceledEventAttributes.GetStartedEventId()`.
Until that fix, the absence of this signature in this run says nothing about whether #8595 reproduced.
The `race` workflow's result check (`Fired+Canceled == Rounds`) is the only other place the bug could show.
