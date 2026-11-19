# RCA: acked-signal-lost-on-reset on v1.4.5 ("an acknowledged signal survives a reset of its workflow")

Run: v1.4.5 hunt `f04f3907fc63a679646c90a9dc10c042-67-6` (666 failing examples). The master analysis is in
`rca/signal-lost-at-reset-master.md` and the retried-reset analysis in `rca/fixverify-reset-dup.md`.

**Verdict: confirmed on v1.4.5. The property fails through two different mechanisms.**
- Example B (counter-35, s134) is the **same mechanism as master**: the signal was buffered during an in-flight
  decision, and the reset's `FailDecision` flushed it into the terminated base run. It was never reapplied.
- Example A (counter-6, s5) is **retried-reset-applied-twice**: the client retried the reset, the retry ran again,
  terminated the first reset run (which held s5), and rebuilt from the same base. s5 was a normal event there, not
  a buffered one.

A timing triage of 6 more examples (below) suggests that most failures are the buffered-signal mechanism, and a
minority are retried or slow resets.

Histories were fetched with `snouty runs exec` in a debugging session `dfd4e4adaefb21b6e657cd68a3cc5aa9-67-6`
(now cancelled), on cadence1: `cadence --address 10.20.20.11:7833 --transport grpc --do antithesis wf list|show`.
Raw data: `antithesis/scratchbook/logs/ack-lost-v145/` (`hist_counter6.txt`, `hist_counter35.txt` hold the table
and JSON histories of every run; `log_<hash>.txt` hold the timelines; `s_<hash>.txt` hold the sampled timelines).

## Example B: counter-35, s134 (buffered signal flushed into the terminated base; same as master)

- Moment `-924893256983270391` vtime `165.343`. Details: `{"applied_n":7,"missing":["s134"],"resets":1,"runs":3,"sent_n":9,"wid":"counter-35"}`

| run | status | role |
|---|---|---|
| b2acde34 | CONTINUED_AS_NEW | s79, act-s85, s94 |
| 997aac7e | CONTINUED_AS_NEW | s107, s115, s122 |
| 234c17b4 | **TERMINATED** (antithesis-reset) | current run and **base of the reset** (point 4); **holds s134 as ev 15** |
| 66871c1b | COMPLETED | reset run; result `applied=[s79,act-s85,s94,s107,s115,s122,act-s132]`, **no s134** |

Sequence (UTC 20:52:32; taskIDs from the JSON history):
```
234c ev12 DecisionTaskScheduled                         .255923  5243646
234c ev13 DecisionTaskStarted  client1                  .266163  5243650   <- decision in flight
     s134 (client3, req c8130590) buffered              .266200            <- event timestamp
driver  rpc client3 signal sig=s134 -> ok               .268899            (acknowledged)
234c ev14 DecisionTaskFailed FORCE_CLOSE_DECISION history-service .268910 5243653
234c ev15 WorkflowExecutionSignaled s134               (.266200) 5243654   <- flushed by the reset's FailDecision
234c ev16 WorkflowExecutionTerminated antithesis-reset  .268911  5243659
6687 ev4  DecisionTaskFailed RESET_WORKFLOW base=234c new=6687 req counter-35-234c17b4-…-4  .268919 5243655
6687 ev5  WorkflowExecutionSignaled act-s132 (reapplied, no RequestID)   <- only signal in persisted base [4,13)
driver  rpc client3 reset -> ok {base 234c17b4, point 4, run 66871c1b}   .270608
```
s134's timestamp (.266200) is 37 µs after decision 12/13 started and before the termination. Its task ID falls
between FORCE_CLOSE_DECISION and Terminated. There is only one RESET_WORKFLOW event, so only one reset was
applied. This is exactly the master signature: `baseNextEventID`/`currentNextEventID` = 14 do not count the
buffered event (`reset_workflow_execution.go:143`, `resetter.go:130-131`). `terminateWorkflow` → `FailDecision`
→ `FlushBufferedEvents` (`mutable_state_util.go:106`) writes it as base ev15, and the reapply reads persisted
`[4,14)` only. The code is identical in `src/cadence-v1.4.5` (checked `resetter.go:125-147`). There were no
faults on this path.

## Example A: counter-6, s5 (retried reset applied twice; different mechanism)

- Moment `3558643188807945681` vtime `98.726`. Details: `{"applied_n":3,"missing":["s5"],"resets":1,"runs":1,"sent_n":9,"wid":"counter-6"}`

| run | status | role |
|---|---|---|
| 3e24d71d | CONTINUED_AS_NEW (ev11) | original; s1, s2, s4; **base of the reset**, point 5 |
| a8dae7d1 | TERMINATED by reset attempt A, 20:50:11.927 | CAN successor (current at the time of reset A); no signals |
| 083121f5 | **TERMINATED by reset attempt B**, 20:50:16.774 | **reset attempt A** run (ev5 RESET_WORKFLOW at 11.994, req `counter-6-3e24d71d-…-5`); **holds s5 as ev 9** |
| 3074b8b8 | COMPLETED | **reset attempt B** run (ev5 RESET_WORKFLOW at 16.814, **same request ID**, same base 3e24); result `applied=[s1,s2,s4]`, **no s5** |

Sequence:
```
11.994  reset A: 3e24@5 -> 083121f5 (terminates current a8dae7d1); reapplies s2, s4
12.056  0831 ev8 DecisionTaskScheduled (never started; no decision in flight)
12.143  0831 ev9 WorkflowExecutionSignaled s5 (client3, req 1445247f)  -- persisted normally, not buffered
12.158  driver: rpc client3 signal sig=s5 -> ok   (acknowledged)
16.774  0831 ev10 WorkflowExecutionTerminated antithesis-reset   <- reset attempt B, same request ID
16.814  3074 ev5 RESET_WORKFLOW base=3e24 new=3074; reapplies s2, s4 from base only
16.900  driver: rpc client2 reset -> ok {base 3e24d71d, point 5, run 3074b8b8}   (one call, ~5 s, faults)
```
The driver made one reset call. The Go client retried it with the same request ID
(`internal_workflow_client.go:788-805`). The dedup at `reset_workflow_execution.go:124-133` never matches,
because the reset run's `CreateRequestID` is the start event's request ID (701483b8, see 3074/0831 ev1), not the
reset request ID (regression #5821). Attempt B had base 3e24 and current 083121f5. Its reapply follows only the
base's continue-as-new chain (3e24 → a8dae7d1, which has no signals) and never reads the current run 083121f5,
so s5 was dropped. The signal sits in a discarded reset run, not in the base run. It was not buffered, and no
decision was in flight. This is `rca/fixverify-reset-dup.md`'s mechanism, here on unpatched v1.4.5. The
fix-verify `acked-signal-lost-on-reset.patch` would not cover it. `retried-signal-duplicated-after-reset.v2.patch`
(the CreateRequestID stamp) would.

## Timing triage of 6 more examples (logs only, not history-verified)

Gap = the driver's ack of the missing signal → the next reset reply on the same workflow.

| moment | wid / signal | gap | likely mechanism |
|---|---|---|---|
| -8289543446213575713 | counter-30 s51 | 20 ms, reset ok | buffered/flush (master) |
| 6077385986865348785 | counter-89 s645 | 92 ms, reset ok | buffered/flush |
| -2861459871432788198 | counter-95 act-s629 | 39 ms, reset ok | buffered/flush |
| -5843518372760614053 | counter-89 s514 | 3 ms, reset ok | buffered/flush |
| -2673776369571164040 | counter-58 s254 | 1.8 s, reset ok | unclear (slow or retried reset) |
| -3057983585204475935 | counter-46 s129 | reset → `unknown timeout` twice | unclear (retried/uncommitted reset) |

The finding's description ("acknowledged while a decision is in flight … lost when the workflow is reset at
that moment") matches the dominant v1.4.5 failure. The 666/787 failing-example counts for this property also
include retried-reset-applied-twice cases, so they should not be attributed to acked-signal-lost-on-reset alone.

## False-positive review

- Wrong run read: no. Both checked results are from the final completed run of the reset chain (66871c1b and 3074b8b8).
- Signal sent after the reset: no. s134 was acknowledged at .268899, before the reset reply at .270608, and it is in
  the base history before Terminated. s5 was acknowledged 4.7 s before the reset reply and is in reset A's run.
- Client retry of the signal: no. There is one event per signal, with its RequestID.
- Classification: **real**, for both (two distinct Cadence bugs).
