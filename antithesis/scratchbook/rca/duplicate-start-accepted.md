# RCA: "a second start of an open workflow ID is rejected" (counter-10)

Run 89a272ed819536288c2f7b7835443da6-67-6 (Cadence v1.4.5). Failing moment: input_hash 1403238972604447951, vtime 135.5375.
Details: `{"reuse":1,"value":"{\"run_id\":\"2ddb5231-...\"}","wid":"counter-10"}`; this is the only failing example (342 pass).

**Classification: false-positive (driver/model bug).** Cadence did not have two open runs. The driver's own
`eventually()` phase sent the `finish` signal to counter-10 without setting `w.FinishSent`. The run closed as
COMPLETED, and about 0.54 s later a `RuleDuplicateStart` call that was already in flight (AllowDuplicate) reached
the server. Cadence accepted it, which is correct because no run was open.

## Runs of counter-10 (from `cadence wf list/show --prt` at and around the moment)

| run | start | close | how |
|---|---|---|---|
| 251bb6da-... | (never persisted) | n/a | 19:16:27.218 create-wf-execution failed: "Failed to lock shard. Previous range ID: 2; new range ID: 3" (cadence2, shard 5). This was an attempt of the original SignalWithStart. `wf show` returns "not found". |
| de56401c-... | 19:16:28.396 (SignalWithStart by client2, signal s3, can_every=3) | 19:16:52.443 TERMINATED by history-service, reason "antithesis" | reset base run |
| 119c2b53-... | reset of de56401c at 19:16:52.443 (DecisionTaskFailed cause RESET_WORKFLOW, NewRunID 119c2b53). Start time is inherited as 19:16:28. | **19:18:08.123971 COMPLETED** (result applied=[s3,s23], sum 98) | `finish` signal at 19:18:08.117875 from **client1** (event 13) |
| 2ddb5231-... | **19:18:08.661984** (client2, can_every=0, exec timeout 604800 s = the RuleDuplicateStart params) | open | the "duplicate" start |

At every moment, counter-10 had at most one open run:
- vtime 134.94 (server clock 19:18:08.041): open = {119c2b53}. de56401c closed TERMINATED.
- vtime 135.33 (19:18:08.436): open = {} (119c2b53 COMPLETED at 19:18:08). 2ddb5231 does not exist yet.
- vtime 135.59 (after the failure): open = {2ddb5231}.

Raw outputs: antithesis/scratchbook/logs/dupstart/exec1.txt ... exec4.txt.

## Sequence

1. 19:16:28: counter-10 started (SignalWithStart, client2). The driver model has StartState OK.
2. 19:16:52: reset de56401c -> 119c2b53. The workflow ID still has exactly one open run. A reset whose reply was "failed" may not have
   been counted in this driver version, but that does not matter here.
3. vtime 134.734 (driver 19:18:07.859): `eventually: stopping faults`. `d.stopping=true`, so `busy()` blocks *new* rules.
   A RuleDuplicateStart that had already passed `busy()` before this was still in flight.
4. vtime ~134.99: the probe workflow completes (the "a new workflow completes within 2 minutes" HIT is at 134.993). `eventually()` step 3
   sends `finish` to every open counter. The first try uses `d.names[0]` = client1, which matches event 13 of 119c2b53 (Identity:client1).
   driver/main.go:515-528 does **not** set `w.FinishSent`.
5. 19:18:08.124 (~vtime 135.0): 119c2b53 COMPLETED. counter-10 now has no open run.
6. 19:18:08.662 (~vtime 135.537): the in-flight duplicate start (client2, RequestID 32dd5cf0..., reuse=1 AllowDuplicate,
   can_every=0, exec timeout 7 d) reaches history. The current run is in state Completed, so the start is accepted and run 2ddb5231 is created.
7. vtime 135.5375: the callback in RuleDuplicateStart (driver/main.go:358-367) computes
   `closing = FinishSent||CancelSent||TerminateSent||Closed` = false. It reads FinishSent, which eventually never set, and Closed, which
   is not yet set because results are read only after all finishes are sent. So it asserts on an "ok" outcome -> FAIL.

The duplicate start arrived about 0.8 s after `stopping` was set. Why it took that long is not visible: neither the driver nor the client logs RPCs.
It may have been a retry in the Go client after a transient error on shard 5 (cadence3 logged "context canceled" errors on shard 5 at
134.52-134.74), or a delay between the rule's `busy()` check, its hegel draw and the send. Either way, the request that created
2ddb5231 was processed after 119c2b53 closed. The server timestamps and the state at vtime 135.33 show this directly.

## Check against Cadence code

service/history/engine/engineimpl/start_workflow_execution.go:
- :245 `CreateWorkflowExecution` in BrandNew mode fails with `WorkflowExecutionAlreadyStartedError` when a current row exists.
  :270 handles that error, and :341 calls `applyWorkflowIDReusePolicyHelper` with the current run's State.
- :977-980 rejects the start if the current run is Created or Running. :981 allows Completed. :997 `WorkflowIDReusePolicyAllowDuplicate`
  needs no further check. :352 creates the run in `CreateWorkflowModeWorkflowIDReuse`, which is conditional on prevRunID being the current run.
The current run 119c2b53 was Completed at 19:18:08.124, so accepting the start at 19:18:08.662 with AllowDuplicate is the documented
behavior. Nothing points to a current_executions anomaly: there was never more than one open run, and the old run's close was
committed (history event 17, visibility CLOSED) before the new start.

## False-positive review

- Check stricter than semantics / checker bug: yes. The model treats counter-10 as open while the driver itself had sent `finish`.
- Not a Cadence bug: every observed state is consistent, with one open run at a time and the close before the new start.

## Proposed driver changes

1. In `eventually()` step 3 (driver/main.go:~517), mark the workflow before sending:
   ```go
   d.m.mu.Lock(); w.FinishSent = true; d.m.mu.Unlock()
   ```
   The closing guard in RuleDuplicateStart (and the guards of any other in-flight callbacks) then skips the check.
2. Defense in depth: in the RuleDuplicateStart callback, also return early if `d.stopping.Load()` is set. Alternatively, when the outcome is
   "ok", confirm the violation before asserting: describe the previous run (the last known run ID, or list closed runs of the WID) and
   fail only if its close time is after the new run's start time or it is still open. A real double-open would survive that check.
3. Optional: log each RPC the driver sends (method, wid, client, rpc id) and each reply, so in-flight overlaps like this one can be
   seen in the logs.
