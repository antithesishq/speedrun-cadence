# RCA: "an acknowledged signal is applied exactly once" — counter-19 missing s97 (run 75b0745bcdbcc355168db60a0ae5c940-67-6, master @572e7e2c)

**Classification: real** (Cadence bug, present in master @572e7e2c and v1.4.5).

s97 was accepted by the reset's base run 8d04c8f3 and acknowledged to the client. It arrived while a decision was in flight, so Cadence put it in the mutable state's **buffered events**, not in persisted history. The reset that followed (holding the base/current run lock) read `baseNextEventID` from mutable state (25, which does not count buffered events) and reapplied signals only from persisted history `[20, 25)`. Terminating the base run then failed the in-flight decision, which **flushed s97 into the terminated base run as event 26**. The new run bb513a4f never got s97, so no run in the chain ever applied it.

## Failure

- Moment: input_hash `-318482339591177353`, vtime `34.675` (`driver model.go:checkCounter:331`)
- Details: `{"applied_n":11,"missing":["s97"],"resets":1,"runs":3,"sent_n":13,"wid":"counter-19"}`
- Log: `antithesis/scratchbook/logs/iter7m/ack.log`
- Histories, collected with `snouty runs exec` on cadence1 at `-318482339591177353 34.68` (`cadence --address 10.20.20.11:7833 --transport grpc --do antithesis wf list -w counter-19 | wf show -w counter-19 -r <run> [--of]`):
  `logs/iter7m/list.txt`, `logs/iter7m/hist.txt` (table output, all 4 runs), `logs/iter7m/hist.json.txt` (JSON for 8d04c8f3 and bb513a4f, with timestamps and task IDs)

## Runs of counter-19

| run | status | events | role |
|---|---|---|---|
| aa87df2b | CONTINUED_AS_NEW | 9 | SignalWithStart s75, then s76, s78, s79; CAN after 3+ signals |
| 8d04c8f3 | **TERMINATED** (antithesis-reset) | 27 | CAN successor of aa87df2b; **base of the reset**; holds s97 as ev 26 |
| bb513a4f | CONTINUED_AS_NEW | 41 | reset run (fork of 8d04c8f3 at event 19, `DecisionFinishEventId=20`); **no s97** |
| c4828f8c | COMPLETED | 13 | CAN successor of bb513a4f; finish, s107. Result `{"applied":["s75","s76","s78","s79","s81","s85","s89","act-s91","act-s93","s102","s107"],"sum":590,"runs":3,"acts":389}` |

The checker's `applied_n:11, runs:3` is exactly c4828f8c's result. That is the correct final run of the chain that follows the reset (reset run, then its CAN successor). **The driver read the right run.** This is not the earlier result-attribution bug (rca/signal-lost-during-reset.md, rca/result-from-wrong-run.md). No result with this payload could come from the terminated base run.

## Sequence (UTC 20:32:xx; event timestamps and task IDs from the JSON histories)

| time | run / event | what happened |
|---|---|---|
| 55.6155 | 8d04 ev 16 | DecisionTaskFailed UNHANDLED_DECISION (decision 14/15) |
| 55.6092 | 8d04 ev 17 (task 4194380) | Signaled act-s91 (buffered, then flushed) |
| 55.6159 | 8d04 ev 18/19 | DecisionTaskScheduled/Started |
| 55.6220 | 8d04 ev 20 (task 4194387) | DecisionTaskCompleted, which **the reset later uses as its point** (`point:20`); ev 21 ActivityTaskScheduled |
| 55.6186 | 8d04 ev 22 (task 4194389) | Signaled act-s93 (buffered during decision 18/19, flushed with ev 20) |
| 55.6220 | 8d04 ev 23/24 | DecisionTaskScheduled/Started (sticky, client2): **a decision is in flight** |
| **55.6270** | 8d04 (buffered) | **Signal s97 (client2, req `ea97fe4e-…`) accepted into the buffered events** because decision 23/24 is in flight. Persisted as a buffered event; NextEventID stays 25. |
| **55.628132** | driver | `rpc client2 signal wid=counter-19 sig=s97 -> ok` (**acknowledged**) |
| ~55.628–55.630 | history (cadence3, shard 6) | Reset request `counter-19-8d04c8f3-…-20` takes the 8d04 lock (base == current). `baseNextEventID = 25` (`reset_workflow_execution.go:143`), `currentNextEventID = 25` (`resetter.go:131`). |
| 55.6303 | 8d04 ev 25 (task 4194398) | `terminateWorkflow` → `FailDecision` adds DecisionTaskFailed **FORCE_CLOSE_DECISION**, identity history-service, then **`FlushBufferedEvents`** … |
| (55.6270) | 8d04 **ev 26** (task 4194399) | … which writes **Signaled s97** into the base run (its timestamp is the original buffered time) |
| 55.6303 | 8d04 ev 27 (task 4194404) | WorkflowExecutionTerminated reason=antithesis-reset (cadence3 log "Workflow execution terminated" at 55.630Z) |
| 55.6326 | bb51 ev 20 (task 4194400) | DecisionTaskFailed RESET_WORKFLOW, BaseRunID 8d04, NewRunID bb51 |
| 55.6326 | bb51 ev 21 (task 4194401) | Signaled **act-s93** reapplied (no RequestID). This is the only signal reapplied: base events `[20,25)` contain just act-s93. **s97 is not reapplied.** |
| 55.636445 | driver | `rpc client3 reset -> ok {base_run_id:8d04c8f3, point:20, run_id:bb513a4f}` |
| 55.6554 | bb51 ev 26 | Signaled s102 |
| 55.666 | bb51 ev 41 | CAN → c4828f8c |
| 55.86 | c482 ev 5, 9, 13 | finish, s107, Completed (result above, without s97) |
| 58.82 | driver | s111 → not_exists (workflow closed) |
| 59.81 (vtime 34.675) | driver | `checkCounter`: s97 missing |

Key facts:
```
8d04c8f3 ev25  DecisionTaskFailed  cause=FORCE_CLOSE_DECISION identity=history-service   ts .630337 task 4194398
8d04c8f3 ev26  WorkflowExecutionSignaled {"id":"s97","v":18} client2 req ea97fe4e-…    ts .627012 task 4194399   <- buffered, flushed by the reset's termination
8d04c8f3 ev27  WorkflowExecutionTerminated reason=antithesis-reset                      ts .630338 task 4194404
bb513a4f ev20  DecisionTaskFailed RESET_WORKFLOW base=8d04c8f3 new=bb513a4f            ts .632567 task 4194400
bb513a4f ev21  WorkflowExecutionSignaled act-s93 (reapplied)                           <- only signal in base [20,25)
```
s97's timestamp (.627012) comes before the termination's events, and its task ID falls between ev 25 and the reset run's events. This is the signature of a buffered event flushed by the reset's own FailDecision. The driver's ack at .628132 comes before the reset reply (.636445), so the signal was strictly accepted before the reset completed.

## Root cause (code)

`src/cadence-master` (line numbers identical in `src/cadence-v1.4.5`):

1. `service/history/engine/engineimpl/reset_workflow_execution.go:54-67`: it locks and loads the base run. When base == current (`:101-103`), the same context/mutable state is the current workflow, so the lock covers concurrent signals. **The lock is not the problem.** s97 was buffered before the reset took it.
2. `reset_workflow_execution.go:143`: `baseNextEventID := baseMutableState.GetNextEventID()`. This does **not** include buffered events (they have no event ID yet).
3. `service/history/reset/resetter.go:130-131`: `currentNextEventID` is captured "before any in-memory mutations", again without the buffered events.
4. `resetter.go:138-147` → `terminateWorkflow` (`resetter.go:472-485`) → `execution.TerminateWorkflow` (`service/history/execution/workflow_execution_util.go:34-42`): it fails the in-flight decision with `FailDecision` (`service/history/execution/mutable_state_util.go:84-107`), which ends with **`mutableState.FlushBufferedEvents()` (`mutable_state_util.go:106`)**. The buffered signal becomes event 26 of the base run in the same transaction that terminates it.
5. `resetter.go:250-265` → `reapplyResetAndContinueAsNewWorkflowEvents` (`resetter.go:487-516`) → `reapplyWorkflowEvents(…, baseRebuildLastEventID+1, baseNextEventID, …)` (`resetter.go:574-620`). It pages **persisted** history `[20, 25)` only. The flushed buffered events (ID ≥ 25) are never seen, and neither is the in-memory batch. The same gap exists for the CAN-chain branch (`resetter.go:518-525` uses the pre-mutation `currentNextEventID` when the chain reaches the current run).

So whenever the current run has buffered signals (a signal that arrived during an in-flight decision) at reset time, and the reset point is in the run that is terminated, those signals are written to the terminated run and dropped from the reset run. The signal had already been acknowledged to the sender. This breaks the documented reset contract (signals after the reset point are reapplied unless `SkipSignalReapply`).

## False-positive review

- Wrong run read: **ruled out**. The checked result is c4828f8c's (the end of the reset chain). The base was terminated and has no result.
- Signal sent after the reset: **ruled out**. Ack at .628132 < reset reply .636445, and s97 is in the base run's history, accepted before termination.
- Check stricter than semantics: Cadence reapplies signals after the reset point by default (`SkipSignalReapply=false` in `client/main.go reset`). s97 is after point 20 in the base run and was neither reapplied nor processed by any decision in the base (the base was terminated right after the flush). The workflow never observed it.
- Client retry or duplicate ID: not involved (one request ID, one event).
- Faults: none needed. This is a pure in-process race between a buffered signal and the reset.

## Proposed fix (Cadence)

In `resetter.go ResetWorkflow`, make the events that terminating the current run flushes eligible for reapply:

- After `r.terminateWorkflow(currentMutableState, …)` (`resetter.go:139`), collect the `WorkflowExecutionSignaled` events in `currentMutableState.GetHistoryBuilder()`'s pending batch with `ID >= currentNextEventID`. These are the buffered signals flushed by `FailDecision`. Reapply them to the reset run if the reset chain reaches the current run, which is always true when base == current. One way is to append them to `additionalReapplyEvents` (reapplied at `resetter.go:270`, with no RequestID, same as other reapplied signals).
- Alternatively, before capturing `currentNextEventID`, fetch the current run's buffered events (add a `GetBufferedEvents()` accessor to `MutableState`) and include their signals in the reapply set.

Add a unit test in `service/history/reset/resetter_test.go`: current == base, a decision in flight, one buffered signal, reset to an earlier DecisionTaskCompleted. Assert that the reset run contains the signal.

Harness: no change is needed. The driver's verdict is correct.
