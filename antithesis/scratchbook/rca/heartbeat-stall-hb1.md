# retried-decision-heartbeat-stalls-workflow: occurrences in hb-1 (default decisionHeartbeatTimeout)

Run 37b22144050fa198dba4d8a45fe63b29-67-6 (cadence-v1.4.5-hb, completed). Here
`history.decisionHeartbeatTimeout` is the 30m default. The finding is described in rca/race-workflow-stuck.md.

**Verdict: the mechanism is confirmed for all 4 workflows: race-62, race-59, race-55 and race-51.** This is the same Go-client
(go.uber.org/cadence v1.4.0) bug, so for the server it is a false-positive. The finding now has 5 occurrences, counting race-1 in 6e868ff1.

Raw material is in `logs/heartbeat-stall-hb1/`:
- `log_race*.ndjson`: the timeline logs up to each failing moment.
- `race*.json.raw`: the `wf show --of` histories.
- `race*-summary.txt`: event id, timestamp, type and key attributes for each history.
- `race-55.out` / `race-51.out`: the `wf desc` PendingDecision.

Histories were fetched in debugging session 42eec67fc27e5815f89808a8aafb1d5f-67-6, which is now **cancelled**. The CLI ran inside the cadence2 container
with `podman exec`, because the client containers have no `cadence` binary.

## Signature checked in each workflow

1. A decision RespondDecisionTaskCompleted fails or times out at the frontend. The Go client retries it, and the retry
   succeeds several seconds later. The resulting DecisionTaskCompleted has **no inline next decision**: the next
   DecisionTaskStarted has a real poll RequestID, not `request-from-RespondDecisionTaskCompleted`. That is the
   ReturnNewDecisionTask=false retry.
2. A local activity that should already have finished never produces a MarkerRecorded. Race LAs are ≤7 s.
3. From then on there is an uninterrupted run of empty DTCompleted → DTScheduled → DTStarted(inline) cycles
   every ~3.2 s (0.8 × the 4 s decision timeout) on the same sticky task list and worker.
4. Legitimate heartbeating of a running 7 s LA gives at most 2–3 empty cycles. Every run below is far longer.

Client logs: a heartbeat retry that **succeeds** logs nothing on the worker. `backoff.Retry` in respondTaskCompleted
is silent, and "error sending decision heartbeat" is printed only when the call finally fails. So the retry shows
up only on the server side, as frontend `RespondDecisionTaskCompleted` errors for the workflow. In race-59, the stuck worker's
next heartbeat after the terminate is visible on the client.

## race-62 (run 7732efe6…, closed COMPLETED, n=97, empty_cycles=20): CONFIRMED

Moment: input_hash 8357354436728454509 @ 241.4759 (earliest failure). Input: 4 rounds, the last is {timer 4000, LA 7000 ms}.
Start: driver 139.70 (20:52:12.83Z). vtime ≈ wall − 20:49:53.13.

| time (Z) | event / log | meaning |
|---|---|---|
| 20:52:19.27 | ev 13 TimerStarted(6), round 4. LA "7" (7 s) starts | |
| 20:52:23 / 26.5 / 36.3 | ev 16 DT timeout, DT 18-20 (client1, replay plus 1 heartbeat), DT 21/22 timed out at ev 23 | faults (client1 throttled from 167.0) |
| 20:52:36.326 | ev 24/25: DT attempt 1 on **client2** (replay). LA 7 is restarted and due by ~20:52:43.3 | |
| 20:52:39.746 | ev 26 DTCompleted, empty, with an inline DT 27/28 | legitimate heartbeat 1 |
| 20:52:43.143 (vt 170.01) | cadence1 frontend `RespondDecisionTaskCompleted` race-62 **"Shutting down"** (cadence1 restarting; container start at 169.78/170.28). At 170.19, cadence2 history logs "shard closed" for race-62 | the heartbeat at ~42.95 fails, and the client retries |
| 20:52:43.409 | ev 29 DTCompleted (empty). **ev 30 DTScheduled / ev 31 DTStarted RequestID f7d5fda8… (poll, not inline)** | the retry succeeds with ReturnNewDecisionTask=false. The LA 7 result (~43.3) is dropped |
| 20:52:46.6 → 20:53:42.595 | ev 32 … 83: 18 more empty cycles, every 3.2 s, on client2's sticky list | stall |
| vt 232.60 (20:53:45.7) | fault: **client2 stop** 4.56 s | stall is broken by the fault, not by the server |
| 20:53:46.597 | ev 86 DecisionTaskTimedOut. ev 87/88 attempt 1 on client3: replay, LA 7 runs again | |
| 20:53:53.94 / 54.066 | ev 96 Marker(LA 7). ev 97 WorkflowExecutionCompleted | |

The run of empty DTCompleted events is ev 26 … 83, 20 consecutive, which matches empty_cycles=20. Of these, ev 26 is the legitimate heartbeat, and the stall
is ev 29–83: 19 cycles, about 63 s, after an LA that was due at about 20:52:43.3.

## race-59 (run 68f98e9a…, closed TERMINATED by the driver, n=85, empty_cycles=24): CONFIRMED

Moment: -7422523944834323835 @ 661.5423. Input: 1 round, {timer 1000, LA 7000 ms}. Start: driver 555.60 (20:59:08.73Z).

| time (Z) | event / log | meaning |
|---|---|---|
| 20:59:16.345 | ev 10/11: DT on client3 (after ev 8 timeout). LA "1" (7 s) starts and is due ~20:59:23.35 | |
| 20:59:19.567 | ev 12 DTCompleted empty, inline DT 13/14 | legitimate heartbeat 1 |
| ~20:59:22.8 | the next heartbeat is sent | |
| 20:59:32.780 (vt 579.65) | cadence3 frontend `RespondDecisionTaskCompleted` race-59 `code:cancelled` (10 s client timeout) | attempt 1 fails, and the client retries |
| 20:59:39.373 (vt 586.24) | cadence1 history `AppendHistoryEvents operation failed. context canceled` race-59 | another attempt fails |
| 20:59:39.426 | ev 15 DTCompleted (empty). **ev 16/17 DTStarted RequestID acbafd37… (poll)** | the retry succeeds with no inline DT. The LA result was dropped |
| 20:59:42.709 → 21:00:50.069 | ev 18 … 81: 22 empty cycles every 3.2 s, client3 sticky list | stall (~71 s) |
| 21:00:51.451 / .453 | ev 84 DecisionTaskFailed FORCE_CLOSE_DECISION, ev 85 WorkflowExecutionTerminated (driver terminate at vt 658.33) | |
| vt 660.14 | client3: `error sending decision heartbeat EntityNotExistsError{Message: Workflow execution already completed.}` | the stuck worker was still heartbeating race-59 |

There is no MarkerRecorded anywhere: the only LA never completes in history. The empty DTCompleted events ev 12 … 81 are 24 consecutive, which matches empty_cycles=24.

## race-55 (run fd3c6888…, straggler, open, history_length 329): CONFIRMED

Moment: -5442527833525018112 @ 945.1766. Input: rounds {1000/4500}, {2500/7000}, {2500/7000}, {200/100}. Start: driver 483.39.
At the moment, `wf desc` shows `PendingDecision{STARTED, ScheduleID 328, 21:05:36Z}` and no pending activities.

- Round 1 completes (ev 9 Marker LA 1). Round 2 (LA "3", 7 s) starts at ev 7/8 (20:58:04). Heavy faults follow. The
  frontend shows RespondDecisionTaskCompleted cancels and timeouts every ~10 s from 20:58:17 to 20:59:11, which are
  heartbeat retries. Decisions time out at ev 14, 21, 27 and 33, and each new attempt replays and restarts LA 3.
- **ev 35** DTStarted 21:00:06.995 on client3 (attempt 1, poll). LA 3 restarts and is due by ~21:00:14.
- **21:00:21.576 (vt 628.44)** cadence3 frontend `RespondDecisionTaskCompleted` race-55 "Frontend request timedout".
  **21:00:22.804 (vt 629.67)** cadence3 history `AppendHistoryEvents timed out` race-55.
- **ev 36** DTCompleted 21:00:25.554 (empty, 18.6 s after its start). **ev 37/38 DTStarted RequestID 2fd08b25… (poll)**.
  This is the successful retry, without an inline DT.
- ev 39 … 327: **97 consecutive empty cycles** every 3.2 s on client3's sticky list, from 21:00:28.758 to 21:05:36.089. Every
  DTStarted after ev 38 is inline. There is no further marker, timer or failure. The workflow was still heartbeating
  when the driver gave up. The stall had lasted over 5 min, and it would last until the 30 min heartbeat timeout.

## race-51 (run 49bc9262…, straggler, open, history_length 297, cancel requested): CONFIRMED

Moment: 4070884268431302921 @ 1468.3026. Input: {200/7000}, {4000/3500}. Driver start at vt 1141.05 (21:08:54.19Z) and
cancel at 1141.08. At the moment, `wf desc` shows `PendingDecision{STARTED, ScheduleID 296, 21:14:21Z}`.

- ev 3 DTStarted 21:08:54.202 on client2. LA "1" (7 s) starts and is due by ~21:09:01.2. ev 6 CancelRequested 21:08:54.219
  is buffered.
- **21:09:07.423 (vt 1154.29)** cadence2 frontend `RespondDecisionTaskCompleted` race-51 "Frontend request timedout".
  **21:09:09.962 (vt 1156.82)** cadence2 history `readLockShard ... context canceled` race-51.
- **ev 4** DTCompleted 21:09:13.861, 19.7 s after its start (TimerStarted 0). **ev 7/8 DTStarted RequestID 859d36d1… (poll)**.
  The first heartbeat, which carried the timer start, was retried and succeeded without an inline DT. The LA 1 result arrived during the retries and
  was dropped.
- ev 9 (21:09:17.19): the stale context processes the cancel request and cancels timer 0 (ev 10). It keeps waiting for
  `la.Get`, but neither the LA result nor the LA cancellation can arrive.
- ev 13 … 295: **95 consecutive empty cycles** every 3.2 s on client2's sticky list, from 21:09:20.393 to 21:14:21.314, and still running.
  Variant: the stall begins at the workflow's first decision, and it also blocks the workflow's cancellation.

## Distinguishing from legitimate LA heartbeating

| wid | LA due by | stall start (retry success, no inline DT) | stall length | legit cycles before |
|---|---|---|---|---|
| race-62 | ~20:52:43.3 | ev 29, 20:52:43.409 | 19 cycles / 63 s (ended by client2 stop) | 1 (ev 26) |
| race-59 | ~20:59:23.4 | ev 15, 20:59:39.426 | 23 cycles / 71 s (ended by terminate) | 1 (ev 12) |
| race-55 | ~21:00:14 | ev 36, 21:00:25.554 | ≥97 cycles / ≥5 min (open) | 0 |
| race-51 | ~21:09:01.2 | ev 4, 21:09:13.861 | ≥95 cycles / ≥5 min (open) | 0 |

None of these runs can be a still-running LA: a 7 s LA gives ≤2–3 cycles. Every stall starts at a DTCompleted that
landed 10–20 s after its decision started and that matches frontend RespondDecisionTaskCompleted errors for the same
workflow. Each is followed by a non-inline DTStarted. The next 19–97+ cycles are all inline, on the same worker.

## Notes

- The two stragglers of "every started workflow closes once faults stop" are this bug: race-55 and race-51. With the
  30m default, the stall outlasts the driver's 5 min window. The other stragglers in that property (chain-9, parent-36,
  counter-*) were not examined.
- The emptyDecisionRun failures come only from workflows that closed for an unrelated reason while still stalled: a node stop
  for race-62, and a driver terminate for race-59.
- This adds weight to the client fix and to the harness workaround in rca/race-workflow-stuck.md, which is
  `history.decisionHeartbeatTimeout` ≈ 1m in the hunts.
