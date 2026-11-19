# RCA: fix-verify still fails "a signal is applied at most once in a workflow that was reset" (counter-182, s609)

Run `41bf06723b005a4e1c4cc37b560fb195-67-6` (v1.4.5 + `fixes/retried-signal-duplicated-after-reset.patch`
+ Stop() nil guards; worktree `src/cadence-fixverify-signal` @ffbc65c2f, without a7da2f80c).
Moment `input_hash=4272893864207768579 vtime=474.5656898911111`.
Details: `{"applied_n":17,"duplicates":["s609"],"resets":2,"runs":2,"sent_n":20,"wid":"counter-182"}`.

**Classification: real, but this is a different bug from the one the patch fixes. The patch is not incomplete
for this case.** Neither copy of s609 is a reset reapply. Both copies are deliveries of the client's own retry,
each with the original RequestID. Two separate Cadence gaps combined to cause it:

1. **New bug: a retried `ResetWorkflowExecution` is not deduplicated** (regression from #5821, baac62184,
   April 2024; it is also on master). The client's retry of a reset that had already committed reset the same base
   run a second time. That threw away the first reset run, together with the signals it had accepted.
2. **Known limitation: the signal dedup set does not cross continue-as-new.** A retry that lands on the CAN
   successor is applied again, even though the predecessor applied it and passed it on in the CAN input.
   This can happen without any reset.

Raw data: `antithesis/scratchbook/logs/fixverify-reset-dup/` (`out1.txt` is `wf list` plus the JSON history of every
run, fetched with `snouty runs exec` in cadence1 at the failing moment. `parsed.txt` is the signal/reset/CAN digest.
`timeline.log` is the timeline up to the failure).

## Runs of counter-182

| run | role | end |
|---|---|---|
| 5f57dec3 | original | ev5 Terminated (reset #1, 20:39:11) |
| d112c482 | reset #1 run (base 5f57, point 4) | ev44 Terminated by reset attempt A, 20:41:06.084 |
| 939f9c1d | **reset attempt A** of d112 to point 15, request `counter-182-d112c482-…-15` | ev28 Terminated by reset attempt B, 20:41:15.621 |
| 1129a719 | **reset attempt B** of d112 to point 15, **same request ID** | ev28 ContinuedAsNew to 2b24, 20:41:15.684 |
| 2b249c85 | CAN successor | ev36 Completed, 20:41:25.458 |

The driver logged only one reset of d112 (`rpc client2 reset … -> ok {"base_run_id":"d112…","point":15,"run_id":"1129…"}`
at 20:41:15.623). It made one `cc.ResetWorkflow` call (`client/main.go:613-621`, `RequestId = <wid>-<run>-<point>`).
The Go client retries a reset with the same request when it gets a transient error
(`go.uber.org/cadence@v1.4.0/internal/internal_workflow_client.go:788-805`). Attempts A and B both carry the identical
`requestId` in their `DecisionTaskFailed cause=RESET_WORKFLOW` event (939f ev4/ev15 and 1129 ev15).

## Where s609 (client3, RequestID 726f22a2-4c2b-4d20-a426-281be2207b91, v=12) landed

| time (UTC) | run / event | how it got there |
|---|---|---|
| 20:41:10.636 | 939f ev24 | first delivery. Reset attempt A held the d112 lock from 06.084 to 10.634, under network partitions and postgres timeouts, so the reply never reached client3 and it retried. |
| 20:41:15.621 | - | reset attempt B terminates 939f and builds 1129 from d112. It reapplies d112 ev16-43 (s489…s612, no RequestID). **939f's own signals (s609, s617, s620, s621) are not reapplied** (see below). |
| 20:41:15.623 | 1129 ev24 | client retry, with the RequestID. 1129 never had s609, so it is correctly not deduplicated. The reply is lost again. |
| 20:41:15.684 | 1129 ev28 | the workflow continues as new after 10 signals. Its CAN input `applied` includes s609. |
| 20:41:16.773 | **2b24 ev5** | client retry, with the same RequestID. 2b24 is a new run with an empty `SignalRequestedIDs`, so s609 is **applied a second time**. Driver: `rpc client3 signal … sig=s609 -> ok` at 16.775. |

Final result of 2b24: `applied` = […, "s612", "s609", "s623", **"s609"**, "s635", …]. The acknowledged signals s617, s620 and s621
are not in it either. They existed only in the discarded run 939f, so this reset retry also **lost acknowledged signals**.
The timeline ends at the first assertion, but the missing-signal check on the same counter fires right after it.

## Mechanism and code (worktree `src/cadence-fixverify-signal`; v1.4.5 and master @572e7e2c have the same code)

### Gap A: reset retry dedup is dead code (new bug)

- `service/history/engine/engineimpl/reset_workflow_execution.go:124-133` deduplicates a reset by comparing the
  **current run's** `ExecutionInfo.CreateRequestID` with the reset request ID.
- The reset run's mutable state is rebuilt from the base history (`reset/resetter.go:371` `Rebuild(…, resetRequestID)`).
  Rebuild replays the copied `WorkflowExecutionStarted` event through
  `execution/mutable_state_builder_methods_started.go:192-198`:
  ```go
  if event.GetRequestID() != "" {
      // prefer requestID from history event ...
      requestID = event.GetRequestID()
  }
  e.executionInfo.CreateRequestID = requestID
  ```
  The start event's requestID is always set (here `f3da4ee1…`, the original StartWorkflow request), so the reset run's
  `CreateRequestID` is the *start* request ID and never the reset request ID. The check at `:125` therefore
  never matches, and a retried reset runs again. Nothing in `service/history/reset/` sets CreateRequestID.
  The "prefer the event" override came in with baac62184 "Update mutable state to generate workflow requests (#5821)".
  Before that, Rebuild's requestID argument (the reset request ID) became CreateRequestID and the dedup worked.
  The only other dedup is the strong-idempotency `DuplicateRequestError` path at `:177-185`. It is off by default
  (`history.enableStrongIdempotency`) and not available on SQL.
- The second reset runs with base d112 (closed) and current 939f. `reapplyResetAndContinueAsNewWorkflowEvents`
  (`reset/resetter.go:487-572` at ffbc65c2f) reapplies the base's events after the reset point. It then follows only
  **ContinuedAsNew** links (`reapplyWorkflowEvents` returns `nextRunID` only for a CAN last event, `:614-619`). d112 ended in
  `Terminated` (by reset A), so the chain stops there, and the current run 939f's events are never read
  (this is the upstream `TODO … fetching all workflow [baseWorkflow, currentWorkflow]` at `:501-502`). The
  retried reset therefore drops everything the first reset run accepted.

### Gap B: dedup set is per run and not carried by continue-as-new (separate, known limitation)

- `engineimpl/signal_workflow_execution.go:54` sends a signal with an empty RunID to the **current** run. `:66-75`
  deduplicates with `mutableState.IsSignalRequested(requestID)`, and `:112-114` adds the ID. The set
  (`pendingSignalRequestedIDs`, `execution/mutable_state_builder.go:118`) belongs to one run.
- `execution/mutable_state_builder.go:1223` `AddContinueAsNewEvent` builds the successor with a fresh
  `NewMutableStateBuilderWithVersionHistories` (`:1287`) and copies no `SignalRequestedIDs`.
- So with no reset at all: a signal is applied in run A and the reply is lost. A's next decision continues as new and passes
  the signal's effect on in the CAN input. The client's retry (same RequestID) reaches successor B and is applied
  again. Cadence cannot know that the application carried the effect forward, but from the API caller's side one
  acknowledged `SignalWorkflowExecution` was delivered twice. That is the same violation the RequestID is meant to prevent.

### Why the v1 patch did not, and should not, catch this

- Gap (2), a reset reapply across a CAN chain: does not apply. The only reapply was reset B's reapply of d112's events, and
  s609 was never in d112.
- Gap (3), a copy before the reset point through another path: does not apply. 1129 ev1-15 contain no s609.
- Gap (4), a different RequestID: no. All three deliveries carry `726f22a2-…`, so it is one client call.
- Gap (1), CAN: **yes**. It is the proximate duplicate. But it only became reachable here because Gap A discarded 939f.
  If reset B had been deduplicated, the 15.623 retry would have hit 939f, which holds `726f22a2` in its set
  (`signal_workflow_execution.go:113`), and it would have been a no-op.

## Proposed fix

`antithesis/fixes/retried-signal-duplicated-after-reset.v2.patch` = the v1 patch + a Gap A fix. It is standalone against
ecd2b70ca, like v1, and the new hunk also applies on top of ffbc65c2f/a7da2f80c. In `replayResetWorkflow`, after
`Rebuild`:

```go
resetMutableState.GetExecutionInfo().CreateRequestID = resetRequestID
```

This restores the pre-#5821 behaviour, so `reset_workflow_execution.go:125` recognises a retried reset and returns the
existing reset run. The `TestReplayResetWorkflow` assertion is extended. Checked with
`go test -overlay … ./service/history/reset/` (pass). Trade-off: a StartWorkflow retry that carries the *original* start
request ID and arrives while the reset run is current is no longer treated as a duplicate start by the reuse-policy
check (`start_workflow_execution.go:950`). That is the same as before #5821. Remaining gap: a reset retry that arrives after
the reset run has itself been reset or continued-as-new is still not deduplicated. A complete fix would record reset
request IDs as a set, like `SignalRequestedIDs`, or would require strong idempotency.

Gap B is not in the patch. It is a separate design question. A sketch: in `AddContinueAsNewEvent`, copy
`e.pendingSignalRequestedIDs` into `newStateBuilder` with `AddSignalRequested`, perhaps capped to the most recent N or
to IDs younger than the client retry horizon, because CAN exists to bound state growth. The CAN successor
created on a standby cluster through replication would need the same treatment.

## False-positive review

- Client misuse: none. The driver sent s609 once and called reset once. Every repeated delivery has the same Go-client
  RequestID, so they are transparent retries.
- Check stricter than the docs: no. A reset request ID exists to make the reset idempotent, and the server even has the
  dedup code, which is dead because of the regression. One reset call produced two resets, lost three
  acknowledged signals and set up the duplicate.
- Harness: correct. The driver's `resets:2` undercounts the server-side resets (3), which does not matter here.

## Driver note

Duplicates that come only from Gap B (no reset involved) would show up under "an acknowledged signal is applied exactly
once". Until Gap B is fixed, consider reporting CAN-crossing duplicates under their own property name, so they do not
mask other duplicates.
