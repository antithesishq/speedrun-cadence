Verdict: stands

# Adversarial review: acked-signal-lost-on-reset

Counterexample reviewed: master @572e7e2c, run 75b0745b…-67-6, input_hash -318482339591177353, vtime 34.675,
counter-19 missing s97 (`rca/signal-lost-at-reset-master.md`). Evidence: `logs/iter7m/ack.log:4381-4385,4523`,
`logs/iter7m/hist.txt`, `logs/iter7m/hist.json.txt`.

## (1) Harness intervention between fault and symptom
- No fault is involved. Nothing fault-related appears in `ack.log` between the s97 ack (`:4381`, 20:32:55.628132) and the reset reply (`:4385`, .636445).
- The only harness action in the window is the reset itself. `client/main.go:591-622` calls a plain `ResetWorkflow`: base = the current run from Describe, the point is a real DecisionTaskCompleted (20), `SkipSignalReapply` is left at its default (false), and RequestId is deterministic.
- This is ordinary use of a public API. The reset run's ev 21 holds a reapplied act-s93, which shows reapply was enabled.
- The driver read the right run. `applied_n:11, runs:3` matches the result of c4828f8c, the CAN successor of the reset run bb513a4f (RCA table). The earlier look-alike failures were driver result-attribution bugs (`rca/signal-lost-during-reset.md`, `rca/result-from-wrong-run.md`). They do not apply here, because the terminated base run has no result.
- Defense fails.

## (2) Configuration / tuning
- `config-template/dynamicconfig.yaml` changes only class-level settings: cache size 20, TTL, task-list partitions, ack intervals, drain 0s, decisionHeartbeatTimeout.
- None of them touches reset, buffered events or decision handling. The trigger is "a signal arrives while a decision is in flight, then a reset runs before that decision completes". That only needs a sticky decision in flight. It happens with default config whenever a reset races normal signal traffic.
- Defense fails.

## (3) Faults beyond the guarantee
- No faults are needed. This is an in-process race on one history host under the workflow lock (RCA: "pure in-process race").
- Guarantee 2 (`guarantees.md:10`) lists only CAN/run continuation and does not name reset explicitly. However, Cadence's reset contract is that signals after the reset point are reapplied unless `SkipSignalReapply` is set (`resetter.go:250-265`, `reapplyWorkflowEvents`). s97 sits after the point in the base run (ev 26 > 20).
- Defense: weak at best. It amounts to "reset is outside the guarantee table", and the check is a separate, labelled property (`driver/model.go:335-337`).

## (4) Can the checker fire on documented behaviour?
Strongest defense: "s97 is not lost. It is durably persisted in the base run's history (ev 26), which is all the signal doc promises. A signal that races a reset may land in the run being reset."

Why it does not hold:
- The ack (.628) came before the reset completed (.636), and before the reset took effect. The signal was buffered while the reset was not yet holding the lock.
- The documented reset semantics say post-point signals in the base are reapplied. s97 is a post-point signal in the base, and Cadence's own code writes it there.
- The reset drops it only because of an implementation gap:
  - `baseNextEventID`/`currentNextEventID` are read before termination: `engineimpl/reset_workflow_execution.go:143`, `reset/resetter.go:130`.
  - `terminateWorkflow` (`resetter.go:138-147`, `:472-485`) → `FailDecision` → `FlushBufferedEvents` (`execution/mutable_state_util.go:106`) assigns s97 an ID ≥ 25.
  - Reapply then reads persisted `[20,25)` only (`resetter.go:487-525`).
- No decision of the terminated run ever processed s97, so the workflow never observed it.
- With the same timing, a signal sent a few ms earlier, after the decision completed, would have been reapplied. The outcome hinges on an internal buffering detail that the caller cannot see.
- The checker (`driver/model.go:323-337`) flags only acks with State == StOK whose ID is absent from the chain's final result. It does not fire on unknown-outcome signals.
- Defense fails.

## (5) Reproduction
- Root-caused once on master (above). The code is identical in v1.4.5: `diff` of `resetter.go` is empty, and the cited line numbers match.
- The same property also failed in hunt1 master (two runs), hunt1 v1.4.5 (two runs) and prefix4 (`logs/stream-hunt1-*.log`, `logs/stream-prefix4.log`, property 35aaba6a). Those counterexamples were not individually root-caused. The v1.4.5 counter-115 case (s410/s413) is marked "likely same" in `speedrun-log.md:25`.
- A fix patch exists with a failing-then-passing unit test (`rca/acked-signal-lost-on-reset-fix.md`). That test reproduces the mechanism deterministically, without Antithesis. The fix has not been verified end to end.

## Severity for Uber engineers
Medium:
- A signal acknowledged to the client is silently dropped from the workflow's logical execution. It survives only in the terminated base run's history, so a payment-callback-style event is never acted on.
- It needs an operator or automation reset that races live signal traffic while a decision is in flight. The window is narrow, but it occurs without faults and with default config, in both v1.4.5 and master.
- It is limited to reset, an administrative action, which is why I would not rate it high. Uber runs batch resets (bad-binary auto-reset, `cadence wf reset-batch`) against live workflows, which makes the race realistic.
