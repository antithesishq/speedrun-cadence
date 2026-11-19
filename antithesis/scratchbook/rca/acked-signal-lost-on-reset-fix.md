# Fix: acknowledged signal lost on reset (buffered signal flushed by termination)

RCA: `rca/signal-lost-at-reset-master.md`. Patch: `antithesis/fixes/acked-signal-lost-on-reset.patch`
(commit a7da2f80c in `src/cadence-fixverify-signal`, on top of ffbc65c2f "dedup reapplied signals on reset").
Only `service/history/reset/resetter.go` and its test change.

## Change

`ResetWorkflow` already captures `currentNextEventID` before it mutates anything. It then terminates the
current run. `TerminateWorkflow` → `FailDecision` → `FlushBufferedEvents` writes any buffered signals into
the current run's pending history batch, with IDs >= `currentNextEventID`.

- After `terminateWorkflow`, the new `getFlushedEvents(currentMutableState, currentNextEventID)` collects
  every event in `GetHistoryBuilder().GetHistory().Events` with `ID >= currentNextEventID`. This is the
  pending batch, not yet persisted: the forced DecisionTaskFailed, the flushed buffered events and
  WorkflowExecutionTerminated.
- These events go through `prepareResetWorkflow` into `reapplyResetAndContinueAsNewWorkflowEvents`, which
  now records whether the reset chain reached the current run: `baseRunID == currentRunID`, or a CAN link
  reaches `currentRunID`. If it did, the events go through `reapplyEvents` after all persisted events, so
  the order is preserved. `reapplyEvents` reapplies only `WorkflowExecutionSignaled` and ignores the other
  events. It keeps the earlier fix: it adds no RequestID to the new event and calls `AddSignalRequested`
  with the original request ID, so a client retry is still deduplicated.
- This covers both the base == current case and the CAN-chain case (base is an earlier run whose CAN chain
  ends at the current run). The persisted range read for the current run is still `[.., currentNextEventID)`,
  so no signal is reapplied twice.
- If the chain does not reach the current run, its flushed signals are not reapplied. This matches how the
  current run's persisted signals are already handled.

## Tests (`service/history/reset/resetter_test.go`)

- `TestReapplyContinueAsNewWorkflowEvents_BaseIsCurrent_BufferedSignal`: base == current. The persisted
  history `[20,25)` holds one signal. The pending batch after termination is
  `{25 DecisionTaskFailed, 26 buffered Signaled, 27 Terminated}`. The test checks that `getFlushedEvents`
  returns the batch, and that both signals are reapplied in order, each with `AddSignalRequested(requestID)`.
  It fails without the fix ("missing call ... buffered signal").
- `TestReapplyContinueAsNewWorkflowEvents_CurrentNotReached_FlushedEventsIgnored`: when the chain does not
  reach the current run, the flushed events are not reapplied.
- The two existing callers in the tests pass `nil` for the new parameter.

## Results

- `go build ./...` succeeds.
- `go test ./service/history/reset/... ./service/history/execution/... ./service/history/engine/...`: all ok.
- `go vet ./service/history/reset/` reports unkeyed `types.History` literals in `TestPagination`. This
  warning was already there before the fix.

Not verified end to end: no Docker image was built and no Antithesis run was launched.
