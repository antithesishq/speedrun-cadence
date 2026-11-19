# RCA: signal s45 applied twice on counter-34 after two resets

Run `89a272ed819536288c2f7b7835443da6-67-6` (Cadence v1.4.5, Postgres, in progress), moment
`input_hash=-5028251366505688799 vtime=70.4274695077911`.

Failing properties (all workflow counter-34):
- 62.17 `a query shows each delivered signal once`: `{"dups":["s45"],"n":4}` (same root cause, found earlier by a query)
- 70.43 `an acknowledged signal is applied exactly once`: `{"applied_n":6,"duplicates":["s45"],"resets":2,"runs":1,"sent_n":6}`
- 70.43 `a workflow history is well formed`: `{"n":45,"problems":["signal s45 recorded twice (events 13 and 18)"]}`

**Classification: real.** This is a Cadence bug. Reset reapplying s45 is correct reset semantics.
The duplicate comes from somewhere else: the Go client's own retry of the *same* `SignalWorkflowExecution`
request (same `RequestId`) reached the new reset run, and that run did not deduplicate it.
`ResetWorkflowExecution` builds the new run's mutable state without the base run's signal request IDs.
The reapplied signal event has no request ID, so a client retry that lands after the reset is accepted
as a new signal. Master (`src/cadence-master` @572e7e2c) has the same code.

## Evidence

Raw data: `antithesis/scratchbook/logs/reset-dup/` (`listing.txt`, `histories.txt` (table and JSON
from `cadence wf show`), `run_297.json`, `run_299.json`, `run_301.json`). Fetched with `snouty runs exec`
in container cadence1 at the failing moment:
`cadence --address 10.20.20.11:7833 --transport grpc --do antithesis wf list -w counter-34 | wf desc | wf show -w counter-34 -r <run> --of`.

Runs of counter-34 (`wf list`):

| run | status | events | role |
|---|---|---|---|
| b712ee4d-aa06-4824-b66f-f6d5cac0642f | TERMINATED | 5 | original run, base of reset #1 |
| 64a6b2e4-22b5-4d93-903a-d9d805ede8d2 | TERMINATED | 26 | reset #1 run, base of reset #2 |
| 1d03f964-eb0a-4422-98f9-ff8102464e3b | COMPLETED | 45 | reset #2 run, current. **s45 appears twice here** |

Wall time to vtime mapping: cadence3 logged the reset-#2 termination at ts `19:16:51.390Z` at vtime 58.266,
so vtime ≈ wall − 19:15:53.12. Cadence nodes share a clock, so this is good to about 0.1 s.

### Sequence

| vtime (≈) | wall (UTC) | event |
|---|---|---|
| 31.54 | 19:16:24.661 | counter-34 started (identity client2), run b712. Decision 2-3-4 completes at 19:16:24.674. |
| 31.59 | 19:16:24.713 | **Reset #1**: base b712, `DecisionFinishEventId=4`, request id `counter-34-b712…-4`. New run 64a6 is forked at event 3, with `4 DecisionTaskFailed cause=RESET_WORKFLOW baseRunId=b712 newRunId=64a6`. The base has no events after 4, so nothing is reapplied. b712 gets `5 WorkflowExecutionTerminated reason=antithesis identity=history-service` (cadence3 log line 3462 of hist-signal.log). |
| 31.64 | 19:16:24.768 | 64a6 ev 8: signal `s30` (client2, requestId 318cfab2…). Decision 9-10-11 completes. |
| 50.28 | 19:16:43.407 | 64a6 ev 12: signal `act-s38` (client3, requestId 5043a69e…). Activity runs (ev 16-18). |
| 53.84 | | FAULT network/clog Stopped max=7.5s nodes=schema,client3,client1 (ends ≈61.34) |
| 54.94 | 19:16:48.060 | **64a6 ev 22: signal `s45` v=33, identity client3, requestId `3d2bd746-a5f8-4ea0-86ca-a8883739bc29`.** Decision 23-24-25 applies it. client3 is clogged, so the response never reaches it. Its RPC fails as transient, and `backoff.Retry` in the Go client will send the same request again. |
| 58.27 | 19:16:51.390 | **Reset #2**: base 64a6, `DecisionFinishEventId=11`, request id `counter-34-64a6…-11`. New run 1d03 is forked at events 1-10, with `11 DecisionTaskFailed cause=RESET_WORKFLOW baseRunId=64a6 newRunId=1d03`. Signals after event 11 of the base are reapplied **without request IDs**: `12 Signaled act-s38` and `13 Signaled s45` (no `requestId` field, identity client3). 64a6 gets `26 WorkflowExecutionTerminated` (cadence3 log line 3843). |
| ≈61.53 | 19:16:54.651 | **1d03 ev 18: signal `s45` v=33, identity client3, requestId `3d2bd746-a5f8-4ea0-86ca-a8883739bc29`.** This is the same request ID as 64a6 ev 22, so it is the Go client's retry of the one `SignalWorkflow` call, not a second send. It targets the current run (empty RunID) and lands on 1d03, where it is not deduplicated. It is buffered while decision 14-16 is in flight, which is why its timestamp is earlier than ev 16/17. client3 now gets success, so the driver records s45 as acknowledged. |
| 62.17 | | driver query → `dups:["s45"]` |
| 62.25 | 19:16:55.368 | 1d03 completes with `applied:["s30","act-s38","s45","s45","act-s67","s72"]`, sum 275 (s45's 33 counted twice). |
| 70.43 | | driver result and history checks fail. |

The reset requests came through the harness `reset` RPC (`client/main.go:550`). The request-id format
`<wid>-<baseRun>-<point>` identifies the base run and reset point. The driver log does not record which
client container sent each reset, and it does not matter here.

### Key fact

```
64a6 ev22  WorkflowExecutionSignaled {"id":"s45","v":33} identity=client3 requestId=3d2bd746-a5f8-4ea0-86ca-a8883739bc29
1d03 ev13  WorkflowExecutionSignaled {"id":"s45","v":33} identity=client3 (no requestId)        <- reset reapply (correct)
1d03 ev18  WorkflowExecutionSignaled {"id":"s45","v":33} identity=client3 requestId=3d2bd746-…  <- client retry, accepted again (bug)
```

## Why it is not a false positive

- **Harness misuse:** none. The driver sends each signal ID once (`driver/main.go:217-227`). The client calls
  `cc.SignalWorkflow` once (`client/main.go:332`). Go client v1.4.0 `signalWorkflow`
  (`internal/internal_task_handlers.go:1653-1670`) creates one `RequestId: uuid.New()` and retries with
  that same request on transient errors. Ev 22 and ev 18 have the same request ID, which proves both came
  from one API call. The RunID is empty, so the retry goes to the current run, which is normal client use.
- **Reset semantics:** reapplying signals after the reset point is documented, default behaviour, and
  1d03 ev 13 is exactly that. The checker does *not* fail on reapply alone: if the client had not retried,
  s45 would appear once (ev 13). The extra copy comes only from Cadence not recognising a retried request
  it already accepted. `RequestId` exists on `SignalWorkflowExecutionRequest` to make signal retries
  idempotent, and the server deduplicates on it (`signal_workflow_execution.go:65-74`, which even
  deduplicates against closed runs).
- **Topology/config:** nothing special. Postgres persistence; `history.enableStrongIdempotency` is at its
  default (false), and the workflow_requests table it uses exists only on the NoSQL path anyway.

## Code

v1.4.5 (`src/cadence-v1.4.5`):

- `service/history/engine/engineimpl/signal_workflow_execution.go:54` `UpdateCurrentWithActionFunc` (signal
  goes to the current run); `:68-74` dedup `if mutableState.IsSignalRequested(requestID) → Noop`; `:112-114`
  `mutableState.AddSignalRequested(requestID)`. The dedup set is per run, in mutable state
  (`pendingSignalRequestedIDs`, `execution/mutable_state_builder.go:118`, `:359`).
- `service/history/reset/resetter.go:371` `newStateRebuilder().Rebuild(...)` rebuilds the reset run from base
  history events 1..point. Rebuild goes through `state_builder.go:427` →
  `ReplicateWorkflowExecutionSignaled` (`execution/mutable_state_builder_methods_signal.go:76-88`), which
  increments SignalCount and calls `insertWorkflowRequest` (strong idempotency only) but **never calls
  `AddSignalRequested`**. So even signals *before* the reset point lose their dedup entries. A retry of s30
  after reset #2 would have been duplicated in the same way.
- `service/history/reset/resetter.go:250-264` → `reapplyResetAndContinueAsNewWorkflowEvents` (`:487`) →
  `reapplyEvents` (`:623-645`):
  ```go
  mutableState.AddWorkflowExecutionSignaled(attr.GetSignalName(), attr.GetInput(), attr.GetIdentity(),
      "", // Do not set requestID for requests reapplied, because they have already been applied previously
  )
  ```
  The request ID is dropped from the event, and nothing records it in the new run's
  `SignalRequestedIDs`. The new run contains the signal's *effect* but not its *idempotency key*.
- The same pattern is in `service/history/ndc/events_reapplier.go:99-104` (NDC conflict reapply).

Master (`src/cadence-master` @572e7e2c): `reset/resetter.go:632-636`, `signal_workflow_execution.go:69,113`,
`mutable_state_builder_methods_signal.go:76-88` and `ndc/events_reapplier.go:103` match v1.4.5 for this
logic. The only recent resetter change, c30d52796 "fix deadlock in workflow reset signal reapplication
(#7913)", does not touch dedup. **Not fixed on master.**

## Minimal trigger

1. Start workflow W. Let one decision complete (event P = DecisionTaskCompleted).
2. `SignalWorkflowExecution(W, runId="", RequestId=R)`. The server records it after P, but the response is
   lost or times out at the client.
3. `ResetWorkflowExecution(W, base=current, DecisionFinishEventId=P)` with default signal reapply. The new
   run gets the signal reapplied without R.
4. The client retries step 2 with the same `RequestId=R`. It goes to the new current run, `IsSignalRequested(R)`
   is false there, and the signal is recorded a second time in the same run.

No faults are needed if steps 2 and 4 are sent by hand with a fixed RequestId (raw gRPC/Thrift call; the CLI
always creates a new uuid). In this run, the network clog on client3 (53.84-61.34) produced the lost
response, and a reset fell inside that window.

## Proposed fix (Cadence)

Keep the reapplied event's request ID empty, so the strong-idempotency workflow_requests row stays with the
base run and its uniqueness check does not fire. Carry the request ID into the new run's dedup set instead,
both for reapplied signals and for signals rebuilt up to the reset point:

```diff
--- a/service/history/reset/resetter.go
+++ b/service/history/reset/resetter.go
@@ func (r *workflowResetterImpl) reapplyEvents(
 		case types.EventTypeWorkflowExecutionSignaled:
 			attr := event.GetWorkflowExecutionSignaledEventAttributes()
 			if _, err := mutableState.AddWorkflowExecutionSignaled(
 				attr.GetSignalName(),
 				attr.GetInput(),
 				attr.GetIdentity(),
 				"", // Do not set requestID for requests reapplied, because they have already been applied previously
 			); err != nil {
 				return err
 			}
+			// The signal is carried into this run, so a client retry of the original
+			// SignalWorkflowExecution (same request ID) must be deduplicated here too.
+			if requestID := attr.GetRequestID(); requestID != "" {
+				mutableState.AddSignalRequested(requestID)
+			}
 		default:
--- a/service/history/execution/mutable_state_builder_methods_signal.go
+++ b/service/history/execution/mutable_state_builder_methods_signal.go
@@ func (e *mutableStateBuilder) ReplicateWorkflowExecutionSignaled(
 	// Increment signal count in mutable state for this workflow execution
 	e.executionInfo.SignalCount++
+	// Keep the dedup set derivable from history, so a run rebuilt from events (reset,
+	// rebuild, standby) still deduplicates retries of signals it already contains.
+	if requestID := event.WorkflowExecutionSignaledEventAttributes.RequestID; requestID != "" {
+		e.AddSignalRequested(requestID)
+	}
 	e.insertWorkflowRequest(persistence.WorkflowRequest{
```

The first hunk alone fixes this trigger (a signal after the reset point). The second covers signals before
the reset point and other rebuild paths. On the active signal path it re-adds an ID that
`signal_workflow_execution.go:113` already added, which is idempotent (a set). Apply the same change as the
first hunk to `ndc/events_reapplier.go:99-104`. Regression test: in `resetter_test.go`, reapply a Signaled
event with RequestID "r1" and assert `resetMutableState.IsSignalRequested("r1")`. As an integration test:
signal with a fixed request ID, reset to before it, signal again with the same request ID, and assert one
reapplied Signaled event and no second one.

Known remaining gap, out of scope here: the signal dedup set is still per run, so a retry that lands after
a continue-as-new is applied in the new run. In that case, though, the signal is not *copied* into the new
run, so it is not duplicated within one run.

## Driver/checker

No change needed: the checks are correct and the burden of proof is met. Optional: log the signal RPC's
outcome and client in the driver, and have the client log the Go client's request ID. That would make the
retry visible without fetching histories.
