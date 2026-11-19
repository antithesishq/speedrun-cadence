# RCA: "a query shows each delivered signal once" (run 89a272ed819536288c2f7b7835443da6-67-6)

**Classification: real** (a gap in Cadence's server-side signal dedup when a reset happens; low severity, narrow window). It is **the same root cause** as the "signal recorded twice in history after two resets" failure in this run: both are on workflow `counter-34`, signal `s45`, events 13 and 18 of the same run.

## Failure

- Moment: input_hash `-3108405920331378075`, vtime `62.17268377612345`
- `driver/main.go:276` FAIL, details `{"dups":["s45"],"n":4,"unsent":null,"wid":"counter-34"}`
- At vtime 70.43 on the same branch (`logs/iter1/hist-signal.log`): `model.go:checkHistory:418` "signal s45 recorded twice (events 13 and 18)" for `counter-34`, plus "an acknowledged signal is applied exactly once".

## Evidence (cadence CLI at the failing moment, run in the cadence1 container)

Raw output: `antithesis/scratchbook/logs/query-dup/hist1.txt` (full histories) and `hist-ts.txt` (event timestamps).

`counter-34` had three runs, linked by **two resets** and no continue-as-new (`can_every:0`):

| run | how it ended |
|---|---|
| `b712ee4d` | terminated by reset 1 (request `counter-34-b712ee4d-…-4`), 19:16:24 |
| `64a6b2e4` | terminated by reset 2 (request `counter-34-64a6b2e4-…-11`, reset point is DecisionTaskCompleted event 11), 19:16:51 |
| `1d03f964` | current run, open |

In run `64a6b2e4`:
- ev 22 `WorkflowExecutionSignaled {"id":"s45","v":33}` Identity client3, **RequestID 3d2bd746-a5f8-4ea0-86ca-a8883739bc29**, 19:16:48

In current run `1d03f964`:
- ev 11 DecisionTaskFailed cause RESET_WORKFLOW, BaseRunID 64a6b2e4, 19:16:51
- ev 12 Signaled `act-s38` (reapplied, **no RequestID**)
- ev 13 Signaled `s45` (reapplied, **no RequestID**), 19:16:51
- ev 18 Signaled `s45` Identity client3, **RequestID 3d2bd746-a5f8-4ea0-86ca-a8883739bc29**, 19:16:54

The query returned `n=4` (`s30, act-s38, s45, s45`). That matches the current run's history exactly, so the Go client's query replay worked correctly. The duplicate is already in the history.

## Sequence

1. At 19:16:48 the driver sends `s45` once, through client3. The Go client creates **one** RequestID per `SignalWorkflow` call and reuses it for every internal retry (`go.uber.org/cadence@v1.4.0/internal/internal_task_handlers.go:1662` `RequestId: uuid.New()`, then `:1665` `backoff.Retry(...)` with the same request). The client RPC context is 20 s (`client/main.go:270-273`). RunID is empty, so each attempt goes to the current run.
2. The first attempt is applied to run `64a6b2e4` (ev 22), but the client gets a transient error instead of the reply. Faults were active at the time: throttles on postgres, cadence3 and client2 at vtime 55.99–57.70, and `readLockShard timed out` on cadence3 at vtime 54.94. Shard 5, which owns `counter-34`, lives on cadence3. The client keeps retrying with the same RequestID.
3. At 19:16:51, while that signal RPC is still retrying, the driver's `RuleReset` resets `64a6b2e4` to event 11, which comes before s45. Cadence creates run `1d03f964` and reapplies the signals that came after the reset point, s45 among them, with an **empty RequestID** (ev 13).
4. At 19:16:54 a retry from step 1 reaches the new current run `1d03f964`. Cadence dedups signals only through that run's own `pendingSignalRequestedIDs`, which does not contain `3d2bd746…`. The signal is therefore appended again (ev 18), and the RPC returns OK. The driver records `s45` as acknowledged once.
5. The workflow handles both copies. The query at vtime 62.17 shows `s45` twice.

Any retry that reached run `64a6b2e4` before 19:16:51 would have been deduped (a no-op returning success), so all of those attempts must have failed. The retry that succeeded is the one after the reset.

## Code references (Cadence v1.4.5; the same code is in master @572e7e2c)

- `service/history/engine/engineimpl/signal_workflow_execution.go:66-75`: dedup by `IsSignalRequested(requestID)`. The set lives in **per-run** mutable state (`execution/mutable_state_builder_methods_signal.go:33-41`). The ID is added at `:113`.
- `service/history/reset/resetter.go:623-645` `reapplyEvents`: reapplied signals get `""` as their RequestID (line 636: "Do not set requestID for requests reapplied, because they have already been applied previously"). The reset run's mutable state is rebuilt from the base history up to the reset point and never inherits the base run's `SignalRequestedIDs`. After a reset, the new run therefore holds the signal but not the ID that would dedup a retry of it.
- `history.enableStrongIdempotency` (`common/dynamicconfig/dynamicproperties/constants.go:5269`, default false) only records `WorkflowRequest`s per run. It does not change this.

## False-positive review

- **Client API misuse:** none. One `SignalWorkflow` call with the default client retry. The driver never re-sends a signal ID, and the duplicate carries the same Go-client RequestID, so it came from the client's own transparent retry.
- **Go client query replay:** ruled out. The duplicate is in the persisted history (two Signaled events).
- **Workflow code (`client/workflows.go:65-125` CounterWorkflow):** it applies each received signal exactly once. With CanEvery=0 there is no continue-as-new.
- **Check stricter than the docs:** Cadence documents that a reset reapplies signals after the reset point, and that a SignalWorkflowExecution RequestID dedups retries. The harness relies on both. Nothing in the docs says a retry that crosses a reset may be applied twice. From the caller's side, one acknowledged API call produced two deliveries, with no way to prevent it except app-level dedup. Combining a concurrent reset with an in-flight retry is unusual, which is why the severity is low.
- **Harness topology/config:** nothing relevant. The behaviour does not depend on the config.

## Proposed fix (Cadence)

When building the reset run, carry the base run's dedup state:

- in `reapplyEvents`, call `AddSignalRequested(attr.GetRequestID())` for each reapplied signal that has a non-empty RequestID, and also pass that RequestID into the reapplied event (or at least into the dedup set), and
- copy the base run's `pendingSignalRequestedIDs` from before the reset point into the reset mutable state.

Either change makes a retry with the same RequestID a no-op after reset. Reapplying events from the current run when it differs from the base run (`reapplyWorkflowEvents`, `resetter.go:574`) needs the same treatment.

## Harness options (if we prefer not to keep hitting this known gap)

Keep the property. Optionally exempt this exact race in `driver/main.go` `RuleReset` (`:312-336`): skip a reset while that workflow has a signal RPC in flight. Add `SignalsPending int` to `WF` in `model.go`, increment it in `RuleSignal` before `d.cs.Call` and decrement it in the callback, then add `&& w.SignalsPending == 0` to the `RuleReset` filter at `main.go:317`. This hides the bug rather than fixing it, so only do it if this is accepted as known. Otherwise leave the driver as is and record this as a finding.
