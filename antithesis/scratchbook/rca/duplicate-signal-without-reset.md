# RCA: counter-59 "signal recorded twice" without a reset in the driver's model (run 22fa5e0b537415326992eb290a37b6da-67-6, cadence-prefix c889f8319)

**Classification: real. It is the same finding as `rca/query-shows-duplicate-signal.md` (option (a)).** counter-59 **was** reset. The reset's ResetWorkflowExecution RPC timed out for the client after Cadence had already committed the reset on an internal retry, and this driver version did not count a reset whose RPC returned "failed". So the driver filed the duplicate as not reset-related. I found no new Cadence path: no dedup loss across shard movement, reload or continue-as-new.

## Failure

- Property "a workflow history is well formed", 7 failing moments on one timeline, vtime 674.89–695.79. The earliest is input_hash `8687931440270089545`, vtime `674.8885516289156`.
- Details: `{"wid":"counter-59","run_id":"","n":33,"problems":["signal s259 recorded twice (events 11 and 15)","signal act-s257 recorded twice (events 10 and 19)"]}`

## Evidence

Debugging session `7b136ffbf15fcd6a7dc100cb596f96b4-67-6`. The cadence CLI ran in cadence1 at the failing moment. Raw output: `antithesis/scratchbook/logs/dup-signal-no-reset/hist.txt` (history JSON and describe output for both runs). Timeline log: `logs/dup-signal-no-reset/log_8687931440270089545.ndjson`.

counter-59 has two runs (`wf list --wid counter-59`):

| run | events | how it closed |
|---|---|---|
| `1150e1d6` | 13 | ev 13 WorkflowExecutionTerminated, reason "antithesis", identity **history-service** (reset), 19:29:52 |
| `5b2289b7` | 33 | reset run; ev 33 Terminated by client2 (driver terminate), 19:31:24 |

Base run `1150e1d6`:
- ev 9 Signaled `s255`, RequestID bc815646…, 19:29:39
- ev 11 Signaled `act-s257`, client3, **RequestID fde01833-be36-4f10-b40e-4d7b1eef48cb**, 19:29:51
- ev 12 Signaled `s259`, client3, **RequestID 07824866-7aef-45c9-b087-f569db55d33a**, 19:29:52
- ev 13 Terminated (reset), 19:29:52

Reset run `5b2289b7`:
- ev 8 DecisionTaskFailed cause RESET_WORKFLOW, baseRunId 1150e1d6, newRunId 5b2289b7, requestId `counter-59-1150e1d6-…-8` (the harness's reset request ID), 19:29:52
- ev 9/10/11 Signaled `s255`, `act-s257`, `s259`: reapplied, **no RequestID**, 19:29:52
- ev 15 Signaled `s259`, client3, **RequestID 07824866-…** (the same as base ev 12), 19:29:56
- ev 19 Signaled `act-s257`, client3, **RequestID fde01833-…** (the same as base ev 11), 19:29:57

Cadence logs (vtime; all shard 1 on cadence2):
- 467.86 (19:29:41) "Workflow execution terminated." 1150e1d6, reason antithesis (reset attempt 1). At 475.61 "AppendHistoryEvents timed out", so it was not persisted.
- 477.42 (19:29:50) reset attempt 2 terminates 1150e1d6 again. At 478.04 "createTransferTasks operation failed … context canceled", so it was not persisted.
- 479.55 (19:29:52) reset attempt 3 terminates 1150e1d6. **This one committed** and created run 5b2289b7 (it matches ev 13 and ev 8 above).
- 484.07 / 485.10 / 486.10 "Frontend request timedout | ResetWorkflowExecution | counter-59 | 1150e1d6": the client's reset call ended in deadline-exceeded, so the driver saw "failed".
- 477.10, 481.10, … "SignalWorkflowExecution … context canceled" on the frontends: signal RPCs were timing out and being retried at the same time.
- 485.82 another in-memory termination of 5b2289b7. It was never persisted, because the shard closed at 497.51 after "createTimerTasks timed out". The history shows 5b2289b7 running until ev 33, so this attempt does not affect the result.

## Sequence

1. Around 19:29:41 the driver's RuleReset calls ResetWorkflowExecution on counter-59, base 1150e1d6, to DecisionTaskCompleted ev 8. Postgres faults slow the shard-1 writes. Attempts 1 and 2 fail to persist, and the Go client retries the reset with the same RequestId.
2. In the same window the driver sends `act-s257` and `s259` through client3. Each SignalWorkflow call has one Go-client RequestID, reused for every internal retry. Both are applied to base run 1150e1d6 (ev 11 and ev 12), but client3 gets a transient error and keeps retrying.
3. At 19:29:52 reset attempt 3 commits. Cadence terminates 1150e1d6 and creates 5b2289b7, rebuilt to ev 7. It reapplies the base's signals after the reset point (s255, act-s257, s259) **with an empty RequestID** (`resetter.go:636`). The new run's mutable state has no SignalRequestedIDs from the base.
4. At 19:29:56–57 the pending retries of s259 and act-s257 (RunID empty, so they go to the current run) reach 5b2289b7. `IsSignalRequested` finds neither ID in that run's set, so both signals are appended again (ev 15, ev 19) and the calls return OK.
5. The reset RPC itself returns deadline-exceeded to the driver. This driver version incremented `w.Resets` only on success, so `wasReset` was false. The duplicates then went to "a workflow history is well formed" instead of the reset-specific property.

## Root cause (Cadence; identical in prefix c889f8319, v1.4.5 and master @572e7e2c)

- `service/history/reset/resetter.go:623-645` `reapplyEvents`: line 636 passes `""` as the RequestID of every reapplied signal and never calls `AddSignalRequested`. The reset run is rebuilt from base history up to the reset point and does not inherit the base run's `SignalRequestedIDs`.
- `service/history/engine/engineimpl/signal_workflow_execution.go:66-75`: dedup works only through the target run's `IsSignalRequested(requestID)`. The ID is added at `:113`, per run.
- Result: a SignalWorkflowExecution retry that started before a reset and lands after it is applied twice.

I checked the other paths and ruled them out for this case. There was no continue-as-new: the counter is not CAN'd, and there are only two runs, linked by the RESET_WORKFLOW DecisionTaskFailed. Both duplicates carry the exact RequestID of a signal that the base run had already recorded, and both landed 4–5 s after the reset committed. That fits the reset path and nothing else. A loss of dedup after shard movement or mutable-state reload would show a duplicate within a single run whose first copy also has the RequestID. Here the first copies in 5b2289b7 have no RequestID, so they are reset reapplications.

## False-positive review

- Client API misuse: none. One SignalWorkflow call per signal ID. The duplicates come from the Go client's own transparent retry with the same RequestID.
- Check stricter than the docs: no. One acknowledged signal is delivered twice to the open run, and Cadence's RequestID dedup is meant to prevent exactly that.
- Harness: the driver's *categorization* is wrong. It under-counted the reset whose RPC failed after the server committed it. The duplicate itself is real. The current driver (`driver/main.go:337`, `w.Resets++` on any outcome) already fixes the categorization, so the same event would now be reported under "a signal is applied at most once in a workflow that was reset".

## Proposed fix

Cadence: the same fix as in `query-shows-duplicate-signal.md`. In `reapplyEvents` (and in the path that reapplies the current run's events, `reapplyWorkflowEvents`), call `mutableState.AddSignalRequested(attr.GetRequestID())` for every reapplied signal with a non-empty RequestID, and/or copy the base run's `pendingSignalRequestedIDs` into the reset mutable state. A retry with the original RequestID is then a no-op on the reset run.

Harness: none needed beyond the `Resets++`-on-any-outcome change that is already in place. Track this as a duplicate of the known finding, not as a new bug.
