Verdict: stands

# Adversarial review: retried-signal-duplicated-after-continue-as-new

Finding: one `SignalWorkflow` call has one Go-client RequestID. That call is recorded in run A, and A then
continues as new with the signal's effect in its CAN input. The client's internal retry, with the same
RequestID, is routed to the CAN successor B and is applied a second time. No reset is involved.
Analysis: `rca/hunt-signal-once-nonreset.md` (S2), `rca/fixverify-reset-dup.md:83-97` (Gap B),
`rca/fixverify-residuals.md:50-58,144-175`. Raw data: `logs/hunt-signal-once/all.jsonl`, `c4_hist.txt`.

## Evidence re-checked

- `logs/hunt-signal-once/c4_hist.txt` (master `3e9720ca…-67-6`, counter-4). In run 5f641059, ev12
  `WorkflowExecutionSignaled` carries s19 with `requestId 8d9f77ff-e823-45d1-b60b-000cf3abac41`, identity client2.
  ev19 `ContinuedAsNew` goes to e4b257b1, and its input already has `applied:[…,"s19",…]`. In run e4b257b1,
  ev5 is `WorkflowExecutionSignaled` s19 with the **same** `requestId 8d9f77ff-…`, identity client2. The
  completion result (ev20) contains s19 twice. Neither run has a reset or a RESET_WORKFLOW event.
- `all.jsonl`: there are 587 duplicate examples across the 5 runs: f04f 41, bba4 79, 3e97 192, c26b 253 and
  fix-verify 1a0d 22. Every example carries `runs>=2`: runs=2 in 189, 3 in 71, 4 in 108, 5 in 34, 6 in 65
  and 7 in 1. None has runs=1. Resets are 0 where the field is present; the query-side rows have no field.
  The fix-verify run adds 5 sampled incidents of the same pattern, each with a matching RequestID on both
  sides of the CAN (`rca/fixverify-residuals.md:50-54`).

## (1) Harness intervention

- The driver sends each signal ID once. `RuleSignal` at `driver/main.go:217-233` makes one `signal` RPC. The
  client issues one `cc.SignalWorkflow(ctx, wid, runID, …)` per RPC (`client/main.go:340`) under a 20 s
  context (`client/main.go:277-281`). RunID is empty, so the call targets the current run. That is the
  documented default (go.uber.org/cadence@v1.4.0 `client/client.go:186`).
- Both copies share one RequestID. That ID is created once, before the retry loop, by the Go client
  (`internal/internal_task_handlers.go:1662`, `RequestId: uuid.New()`). The harness re-sends nothing. It does
  not restart, edit or reset the workflow between the two copies.
- The continue-as-new is the workflow's own decision (`client/workflows.go:114-120`).
- **Not harness-caused.**

## (2) Configuration and tuning

- `config-template/dynamicconfig.yaml` sets cache size and TTL, task-list partitions, ack intervals, drain=0s
  and decisionHeartbeatTimeout. None of these touches signal dedup. The dedup set
  (`pendingSignalRequestedIDs`, `execution/mutable_state_builder.go:118`) is persisted with mutable state
  (`:331`, `:359`), so a small cache does not lose it.
- The loss is structural. `AddContinueAsNewEvent` (`mutable_state_builder.go:1223`) builds the successor with
  a fresh `NewMutableStateBuilderWithVersionHistories` (`:1287`) and copies no signal request IDs. v1.4.5
  and master are the same.
- Strong idempotency (`history.enableStrongIdempotency`, left at its default) applies only to start
  (`engineimpl/start_workflow_execution.go:254`), not to signals.
- Tuning that makes it **more frequent**: the driver continues as new every 3 or 10 signals
  (`driver/main.go:149`), so CAN falls inside a retry window far more often than in a production workflow that
  continues as new every few thousand events. That changes how often it happens, not whether it can.
- **It would happen with Cadence defaults.**

## (3) Faults beyond the guarantee

- The required fault is one lost or late reply to a signal that was committed. In counter-4, the
  frontends of cadence1 and cadence2 log "Frontend request timedout" / "client connection is closing" at
  23.39 for that SignalWorkflowExecution. The Go client then retries within its own context. Guarantee 2
  (`guarantees.md:10`) lists "lost responses + client retries (partition, latency)" as covered. That is the
  textbook reason RequestID dedup exists.
- Clock jitter plays no part. Ordering comes from history event IDs and the same RequestID, not from
  timestamps.
- A minimal trigger needs no Antithesis faults:
  1. Signal with fixed RequestID R.
  2. Let the workflow continue as new.
  3. Signal again with R (empty RunID).

  Any frontend timeout, LB reset or history failover after the commit gives the same result in production.
- **The faults are within scope.**

## (4) Checker and workflow code

- Workflow code: `CounterWorkflow` (`client/workflows.go:67-120`) appends each received signal to the
  `Applied` list once. Before it continues as new, it drains pending signals (`:115`, `drain` at `:91-99`) and
  carries its state forward, which is the standard CAN pattern. It does not apply anything twice itself. The
  second copy is a separate `WorkflowExecutionSignaled` event in the successor's history, so the duplicate is
  in Cadence's history and not only in the workflow's state.
- Checker: `driver/model.go:291-321` and `driver/main.go:277-295` flag an ID that appears twice in the
  result or query. These reads are the workflow's own state, built from history. The checker is correct.
- Documented semantics: Cadence's public docs do not *explicitly* promise RequestID dedup across a
  continue-as-new. The server code does show the intent, though. `signal_workflow_execution.go:65-67` (v1.4.5
  and master) dedups "before workflow running check so that already completed error won't be returned for
  duplicated signals even if the workflow is closed". So a retry is meant to be a no-op even after the run
  that took it has closed. Routing the retry to the current run (`:54`, `UpdateCurrentWithActionFunc`) sends
  it to successor B, whose set is empty (`:68-75` checks, `:112-114` adds). That defeats this intent.
- **The assertion does not fire on documented, intended behaviour. The workflow code uses the API
  correctly.**

## (5) Reproduction

| Run | Version | Duplicate examples |
|---|---|---|
| f04f | v1.4.5 hunt | 41 |
| bba4 | v1.4.5 hunt-b | 79 |
| 3e97 | master @572e7e2c | 192 |
| c26b | master-b | 253 |
| 1a0d | fix-verify (v1.4.5 + 3 patches) | 22 |

- The finding reproduces on all 4 hunts and both versions. The code is identical on both versions.
- It persists on fix-verify, as expected, because no patch touches CAN dedup.
- An optional fix diff exists: `logs/fixverify-residuals/signal-dedup-across-can.diff`. It has not been
  verified in a run.

## Strongest defense and why it does not hold

**Defense:** Continue-as-new creates a new execution, and RequestID dedup is per execution. A signal with an
empty RunID goes to "the running execution", and a retry that arrives after the CAN reaches a run that has
never seen it. The workflow chose to carry the signal's effect forward, so the server cannot know about it.
At-least-once delivery with idempotent handlers is the general Cadence advice. Temporal is believed to share
the same per-run design (not verified here).

**Why it fails:**

1. The duplicate is not a re-delivery from the workflow's point of view. It is a second
   `WorkflowExecutionSignaled` event for one acknowledged API call with one RequestID. The RequestID exists
   precisely so that a client's retry is invisible.
2. The server's own comment (`signal_workflow_execution.go:65-67`) intends retries to be no-ops after the
   target run closes. CAN closes the run, and routing to the current run bypasses that check.
3. The caller has no workaround that keeps the API simple:
   - Pinning RunID would break signals across CAN.
   - Dedup in the handler needs an application-level ID and an unbounded set in workflow state, which is
     exactly what Cadence's RequestID is meant to spare users.
   - The Go client hides the RequestID, so the user cannot even see it.

The defense does reduce severity: this is a gap in a guarantee that is implied, not explicitly documented,
and an upstream fix has a real cost (it would mean carrying a bounded set across CAN, against CAN's purpose of
bounding state).

## Severity for Uber engineers

Medium. It silently delivers one acknowledged signal twice, and the second copy is a real history event,
which matters for non-idempotent handlers such as counters, payments and state transitions. It needs a lost
or late reply plus a CAN inside the client's retry window, which is roughly seconds up to the RPC context
deadline. Workflows that continue as new often and get signals at a high rate are the most exposed (entity
or actor-style workflows). The hunt rates (41-253 examples per 600-CH run) overstate production frequency,
because the driver continues as new every 3 or 10 signals.

A fix could take either of two forms:
- Copy a bounded, recent set of the predecessor's `SignalRequestedIDs` into the successor in
  `AddContinueAsNewEvent`. This also needs the same treatment on the replication or standby CAN path.
- Or, document the limitation explicitly.
