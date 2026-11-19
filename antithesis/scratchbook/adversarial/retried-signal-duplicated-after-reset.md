Verdict: stands

# Adversarial review: retried-signal-duplicated-after-reset

Finding: one `SignalWorkflow` call (one Go-client RequestID) is recorded twice in the same run. The
first copy is the reset's reapply (no RequestID). The second is the client's internal retry, which
carries the original RequestID and reaches the new reset run, where it is not deduplicated.
Evidence: `rca/reset-duplicates-signal.md`, `rca/query-shows-duplicate-signal.md`,
`rca/duplicate-signal-without-reset.md`, `logs/reset-dup/`, `logs/query-dup/`, `logs/dup-signal-no-reset/`.

## (1) Harness intervention between fault and symptom

- The harness does two things here: one `SignalWorkflow` call (`client/main.go:332`) and one
  `ResetWorkflow` call (`client/main.go:591-627`). Reset uses defaults: no `ResetReapplyType`
  override, so signals are reapplied by default. Its RequestID is `<wid>-<run>-<point>` (`:613`).
  It targets the current run's DecisionTaskCompleted event, which is ordinary operator or tool use.
- The driver never re-sends a signal ID. The second copy has the same RequestID as the first
  delivery in the base run (`3d2bd746-…`: 64a6 ev22 and 1d03 ev18; `07824866-…` and `fde01833-…` in
  counter-59). Only the Go client's own `backoff.Retry` re-sends that request with that ID
  (go.uber.org/cadence@v1.4.0 `internal/internal_task_handlers.go:1662-1669`: `RequestId: uuid.New()` is
  created once, before the retry loop). The harness did not retry the signal.
- The harness does not restart, edit or rewrite anything between the reset and the retry.
  **Not harness-caused.**

## (2) Configuration and tuning

- `config-template/dynamicconfig.yaml` changes cache size and TTL, task-list partitions, ack intervals,
  drain durations and decision heartbeat timeout. None of these affects the signal dedup set or reset
  rebuild. The dedup set (`pendingSignalRequestedIDs`) is persisted in mutable state, so a smaller cache
  does not lose it. The loss is structural: `reset/resetter.go:632-637` (v1.4.5, the same at master :636)
  passes `""` and never calls `AddSignalRequested`. The rebuild path `ReplicateWorkflowExecutionSignaled`
  (`execution/mutable_state_builder_methods_signal.go:79-88`) also never adds it.
- `history.enableStrongIdempotency` is left at its default (false). Turning it on would not change this
  path.
- The client RPC timeout is 20 s (`client/main.go:276`). That is the normal context deadline, and the Go
  client retries inside it, as it does with default settings.
  **It would happen with Cadence defaults.**

## (3) Faults beyond what the guarantee promises

- What the faults had to do here was lose a response, through a network clog on client3 lasting at most
  7.5 s, or Postgres slowness and timeouts in counter-59. Guarantee 2 (`guarantees.md:10`) names "lost
  responses + client retries (partition, latency)" as covered faults. A lost RPC response is the
  textbook case for RequestID dedup.
- The RCA's minimal trigger needs no faults: signal with fixed RequestID R, reset to before it, then
  signal again with R (raw gRPC/Thrift call).
  **The faults are within scope.**

## (4) Can the checker fire on documented behaviour?

- Reapplying a signal on reset is documented, and the checker does not flag it on its own. With no client
  retry, s45 appears once (1d03 ev13). The duplicate exists only because the retry was accepted again.
- The server code itself shows the dedup intent: `engine/engineimpl/signal_workflow_execution.go:65-67`
  dedups by RequestID "before workflow running check so that ... won't be returned for duplicated signals
  even if the workflow is closed". That is, retries are meant to be no-ops even after the run is
  terminated. After a reset, `UpdateCurrentWithActionFunc` (`:54`) routes the retry to the new current
  run, which lacks the ID.
- The current driver counts a reset on any outcome (`driver/main.go:337`, `w.Resets++`). So the
  categorization mistake in `duplicate-signal-without-reset.md` (an older driver build) does not
  inflate the count: it is the same finding.
  **The checker is not stricter than the documented semantics.** There is one gap, covered below: no
  Cadence document *explicitly* promises dedup across a reset.

## (5) Reproduction

- v1.4.5: run `89a272ed819536288c2f7b7835443da6-67-6`, counter-34/s45. Three properties fail: query,
  result and history checks.
- Prefix c889f8319: run `22fa5e0b537415326992eb290a37b6da-67-6`, counter-59. Two duplicates (s259,
  act-s257), with the same mechanism.
- Master @572e7e2c: the code is identical (`resetter.go:636`, `ndc/events_reapplier.go:103`). By code
  inspection the bug is not fixed. I have no master timeline of my own in the evidence.
- Fix verification (`rca/fixverify-reset-dup.md`): with `fixes/retried-signal-duplicated-after-reset.patch`,
  the remaining duplicate came from a *different* mechanism (a reset retry that was not deduplicated, plus
  the continue-as-new dedup gap). Neither copy was a reapply. This suggests the patch removes this
  mechanism. It does not prove it.

## Strongest defense and why it fails

Defense: "A reset is an operator action that rewrites history. Cadence promises signal dedup only
within a run, and nothing promises exactly-once across a reset. An operator who resets during
an outage accepts at-least-once delivery." This is the most credible argument. Guarantee 2's sources
(Go signals doc, IDL) do not mention reset.

Why it does not hold:
- The duplicate is in one run's history, and one acknowledged API call produced two deliveries in that
  run.
- The caller cannot avoid it. The RequestID is generated inside the Go client, so the only fix is
  app-level dedup.
- Cadence carries the signal's *effect* into the new run (reapply) but drops its *idempotency key*. That
  is an inconsistency inside Cadence, not a semantic choice. The server comment at
  `signal_workflow_execution.go:65-67` shows the designers intended retries to be no-ops even against
  closed runs.
- Failing over to a reset is a common incident-recovery action, and it is exactly when client retries
  are in flight.

## Severity to state to Uber engineers

Low to medium. This is a correctness bug in request-ID dedup, and it is present on master. It needs a
reset to commit while a signal RPC is retrying: a lost response plus a reset within the client's retry
window, which is seconds. That combination is likely during incident recovery, including batch resets.
The impact is a duplicated signal in one run: a payment callback or counter applied twice, with no
error to any party. Fix (small, local): call `AddSignalRequested(requestID)` for reapplied signals in
`resetter.go reapplyEvents` and `ndc/events_reapplier.go`. Optionally do the same in
`ReplicateWorkflowExecutionSignaled`.
