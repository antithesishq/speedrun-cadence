# RCA: race-1 never closes (endless decision heartbeats)

Run 6e868ff1a0ea33104c55aee1a6f6af93-67-6 (cadence-v1.4.5), failure moment
input_hash=5337532354675634409 vtime=401.2113032224588, property
"every started workflow closes once faults stop".
Straggler: race-1 / 6eaae673-fe8a-4b7c-9a46-874904920a86, history_length 294.

**Classification: real bug in the Go client SDK (go.uber.org/cadence v1.4.0). It is not a Cadence
server bug: for the server tally this is a false-positive.** Our workflow code is not at fault. The
workflow would recover by itself after `history.decisionHeartbeatTimeout`, which defaults to 30 min,
but the driver waits only 5 min.

## Evidence

- History, fetched with `cadence wf show --of`, executed at the failure moment in run
  7955602fabbe861dfc4c63b9314f45b8-67-6 with `--source-run-id`:
  `antithesis/scratchbook/logs/race-stuck/history.json`. The raw exec output, with the base64 gzip,
  is `exec3.out` in the same directory.
- `wf desc` at the failure moment shows `PendingDecision{State: STARTED, ScheduleID: 293}`, no
  pending activities, and taskStartToCloseTimeoutSeconds=4. The workflow has an open decision and
  is heartbeating.
- Event counts: DecisionTaskScheduled 96, DecisionTaskStarted 96, DecisionTaskCompleted 95,
  MarkerRecorded 2, TimerStarted 2, TimerCanceled 1, TimerFired 1, WorkflowExecutionStarted 1.
  The 294 events are about 93 empty decision heartbeats, one every ~3.2 s (0.8 × 4 s) from
  19:51:05 to 19:56:02. There are no DecisionTaskFailed or DecisionTaskTimedOut events, and no
  non-determinism errors.
- Timer 2 has TimerCanceled only (ev 11). Timer 4 has TimerFired only (ev 16). The #8595
  signature (both TimerCanceled and TimerFired for one timer) is not present.
- The client container logs contain nothing about race-1: no panic, no "unknown event", no
  "Failed to process decision task". A heartbeat that loops forever logs nothing, so this fits.
- The workflow's own LA retry loop (workflows.go:315-327) never ran. A retry would add a second
  marker for the same round plus a 1 s `workflow.Sleep` timer, and the history has neither.

Input: rounds = [{2500ms timer, 100ms LA}, {200ms→1s timer, 4500ms LA},
{2500ms→3s timer, 7000ms LA}, {4000ms timer, 4500ms LA}].

## Sequence (history events and logs, cadence2 owns shard 2)

| time (UTC) | event | meaning |
|---|---|---|
| 19:49:59.5 | DT 2/3 started by client1 | round 1: LA "1" (100 ms) finishes. Round 2: timer "2" and LA "3" (4500 ms) start |
| 19:50:03.033 | 4 DTCompleted + 5 marker(LA1) + 6 TimerStarted(2) + 7/8 DT sched/started inline (`request-from-RespondDecisionTaskCompleted`) | decision heartbeat at 3.2 s (it carries decisions, and the next DT comes back inline) |
| ~19:50:04.3 | (worker) LA3 result arrives | round 2 ends: cancel timer 2. Round 3: timer "4" and LA "5" (7000 ms) start. LA5 is sent to the LA tunnel, so it is no longer in `unstartedLaTasks` |
| ~19:50:06.3 | (worker) heartbeat RespondDecisionTaskCompleted{decisions: marker3, cancel2, start4; Force=true; ReturnNewDecisionTask=true} | postgres DNS fails on cadence2 (`AppendHistoryEvents ... lookup postgres ... no such host`, straggle.log:4774) |
| 19:50:16 to 19:50:56 | frontend `RespondDecisionTaskCompleted` timeout or cancel for race-1 every 10 s (straggle.log:4772, 5620, 5794, 5903, 5964) | the Go client retries the same request object |
| 19:51:02.688 | 9 DTCompleted + 10 marker(LA3) + 11 TimerCanceled(2) + 12 TimerStarted(4). **No DT in the same transaction** | a retry succeeds, but with ReturnNewDecisionTask=**false** (see the root cause). The server schedules the forced DT through the transfer queue and returns no DecisionTask |
| 19:51:05.220 / .222 | 13 DTScheduled (sticky tasklist, own taskId) / 14 DTStarted with a real poll requestId | the DT arrives through a normal sticky poll |
| 19:51:08.4 onward | 15 DTCompleted (empty) + 17/18 inline … through 294 | endless empty heartbeats: the worker waits for an LA5 result that can never arrive |

## Root cause (Go client go.uber.org/cadence v1.4.0)

1. **Retrying a decision completion turns off ReturnNewDecisionTask.**
   `internal/internal_task_pollers.go:507-514` (handleDecisionTaskCompletedRequest):
   ```go
   if request.StickyAttributes == nil && !wtp.disableStickyExecution {
       request.StickyAttributes = &s.StickyExecutionAttributes{...}
   } else {
       request.ReturnNewDecisionTask = common.BoolPtr(false)
   }
   ```
   `respondTaskCompleted` (`internal_task_pollers.go:453-462`) retries with `backoff.Retry` and
   passes the **same request object** each time. The first attempt sets StickyAttributes, so every
   retry goes into the `else` branch and sends ReturnNewDecisionTask=false. The server
   (`service/history/decision/handler.go:500-535, 638`) then creates the forced decision without
   starting it inline and returns no `StartedResponse`.

2. **A heartbeat response without a decision task leaves cached state that cannot make progress.**
   `internal_task_pollers.go:369-371`: when `heartbeatResponse.DecisionTask == nil`, the heartbeat
   function returns `(nil, nil)`. `internal_task_handlers.go:857-859` then returns `nil, nil` from
   ProcessWorkflowTask. Because the error is nil, `Unlock` (`internal_task_handlers.go:511-533`)
   keeps the workflow context in the sticky cache. `processWorkflowTask` returns and closes
   `doneCh` (`internal_task_pollers.go:355`). When LA5's result arrives, `ProcessTask` drops it
   because `doneCh` is closed (`internal_task_pollers.go:604-611`).

3. **The next sticky decision task reuses that context.** It passes `ResetIfStale`: the first event
   is 9, which is previousStartedEventID 8 + 1. LA5 is still in `pendingLaTasks` but not in
   `unstartedLaTasks`, so `CompleteDecisionTask` (`internal_task_handlers.go:1151-1173`) never
   dispatches it again. The wait loop at `internal_task_handlers.go:834-871` gets no result on
   `laResultCh` and sends a forced heartbeat every 3.2 s. These heartbeats succeed on the first
   attempt, so they do return an inline DT, and the loop continues.

4. The loop ends only when the server's `history.decisionHeartbeatTimeout` (default 30 min,
   `common/dynamicconfig/dynamicproperties/constants.go:6022-6025`) times out the decision. The
   server then clears stickiness and returns EntityNotExists, the client evicts the cache and
   replays, and LA5 runs again. Our check gives up after 5 min.

The fault only sets this up: the heartbeat RPC has to need a retry, and a later retry has to
succeed. After that the workflow stays stuck even though no faults are running.

The server behaved according to the request it received. The workflow code is correct: no LA
retry happened, and the timer cancel path is clean.

## Proposed fix / workarounds

- **Client fix (upstream go.uber.org/cadence):** do not change `ReturnNewDecisionTask` on retry.
  For example, compute the sticky decision once before `backoff.Retry`, or test
  `wtp.disableStickyExecution` instead of `StickyAttributes == nil`. Also, in the heartbeat path
  (`internal_task_pollers.go:369-371`), treat a nil DecisionTask as a `decisionHeartbeatError`, or
  evict the cached context, so that pending local activities are dispatched again on the next
  task. Check whether newer client releases already fix either part.
- **Harness, to keep heartbeat coverage without the 30-min stall:** set
  `history.decisionHeartbeatTimeout` in `config-template/dynamicconfig.yaml` to about 1m, so the
  server breaks the loop well inside the 5-min straggler window. Alternatively, tag
  race stragglers that are open with a STARTED decision and no failures as a known client issue.
- Do not use the alternative of `DisableStickyExecution`: it changes much more of the worker's
  behaviour.
