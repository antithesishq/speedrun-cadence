# RCA: "a second start of an open workflow ID is rejected" (hunt runs)

Property: driver/main.go:409 (`RuleDuplicateStart` callback). It fails when a start of a workflow ID that the
driver model believes is open returns "ok".

## Counts

| run | version | failing events (`runs search --count`) | passing | distinct WIDs (one origin each, replicated across branches) |
|---|---|---|---|---|
| f04f3907…-67-6 | v1.4.5 hunt | 125 | 6.6k | counter-45 (113), counter-30 (12) |
| bba4d2f0…-67-6 | v1.4.5 hunt -b | 163 | 7.5k | counter-33 (163) |
| 3e9720ca…-67-6 | master hunt | 55 | n/a (API error) | counter-64 (44), counter-25 (4), counter-39 (4), counter-34 (3) |
| c26baba5…-67-6 | master hunt -b | 125 | 6.7k | counter-40 (125) |
| 1a0d055c…-67-6 | fix-verify (v1.4.5 + 3 fixes) | 0 | 4.0k | none ("a closed workflow ID is reused" hit 2.9k times, so the precondition was exercised) |

Raw: antithesis/scratchbook/logs/hunt-second-start/ (fail-<run>.ndjson = all failing events with details;
log_*.ndjson = timelines for the moments below).

## Signatures

There is one root cause. It shows up in three variants, depending on which closing operation lands on the new run.

| signature | runs (failing events) | classification |
|---|---|---|
| S1: a straggler `finish` signal sent to the previous run of a reused workflow ID lands on the new run and completes it. The duplicate start (reuse=1) is then accepted. | c26b counter-40 (125), bba4 counter-33 (163), 3e97 counter-34/-25/-39 (11) | false positive (driver) |
| S2: a straggler `cancel` sent to the previous run lands on the new run and cancels it. The start (reuse=0 or 1) is then accepted. | 3e97 counter-64 (44), f04f counter-30 (12) | false positive (driver) |
| S3: a straggler `terminate` sent to the previous run lands on the new run. The start (reuse=0/1) is then accepted. | f04f counter-45 (113) | false positive (driver) |

None of these is on the fix-verify run (0 failures). That fits a driver race that depends on timing, not on a fixed finding,
and the shorter 400 CH run gave it less chance to occur (see "Fix-verify" below).

## Mechanism (all signatures)

1. Workflow ID W has run R1. The driver sends a closing op for R1 (`RuleFinish`: signal "finish"; `RuleCancel`;
   `RuleTerminate`) to some client. All of them address the workflow **by ID only**: client/main.go sends
   `SignalWorkflow / CancelWorkflow / TerminateWorkflow(wid, "" runID)`. The op stays in flight, either queued
   in a paused client or retried inside the Go client during an outage.
2. R1 closes some other way (another finish/cancel/terminate). The driver reads its result and retires the WF.
3. `RuleReuseClosed` (driver/main.go:687) starts a new run R2 on W with a **fresh WF model object**. The new object's
   FinishSent/CancelSent/TerminateSent are false. Its candidate filter checks `DupPending == 0`, but it does **not**
   check for close ops still in flight to W.
4. The straggler op is processed by Cadence now. With an empty RunID it targets the **current** run, which is R2, so R2 closes.
   This is the documented Cadence semantics: "If runID is empty, the currently running execution is used."
5. `RuleDuplicateStart` picks the R2 model, which it believes open (no FinishSent/CancelSent/TerminateSent), and starts W again.
   Cadence accepts the start because R2 is closed (reuse=1 AllowDuplicate; or reuse=0 AllowDuplicateFailedOnly after a
   cancel/terminate). Then `closing` is false, so the assertion fails.

That the op belonged to R1 follows from the code alone. RuleFinish/RuleCancel/RuleTerminate set the flag on the WF
they target **before** sending (main.go:247, 310, 325). The DupStart callback reads that flag on the R2 object
(main.go:392). A finish/cancel/terminate sent for R2 would therefore have suppressed the assertion. So the closing op came
from the R1 object (eventually() is excluded, because the callback returns when `d.stopping` is set).

## Evidence per signature

### S1, c26baba5…-67-6 (master -b), counter-40, input_hash 3959925780484902406, vtime 186.13959
Driver "rpc" lines (each is logged when its reply arrives):
```
125.658 client3 start counter-40 reuse=2 -> ok run 7bb78549                      (R1)
150.613 client3 signal counter-40 finish -> ok                                    (R1 completes)
162.624 client2 signal act-s114 -> failed not_exists; 165.600 s124 -> not_exists   (R1 closed)
165.602 client2 start counter-40 reuse=1 -> ok run b8f14d01                        (RuleReuseClosed: R2)
165.616..176.610 signals s127, act-s174, act-s179, s182 -> ok                      (R2 open)
178.171 client1 signal act-s111 -> ok
178.174 client1 signal counter-40 finish -> ok         <- straggler
178.175 client1 signal s103 -> ok
184.663 s195 -> not_exists; 186.138 s197 -> not_exists                            (R2 closed)
186.140 client3 start counter-40 reuse=1 -> ok run 10e63bcc   -> ASSERTION FAILS
```
Fault: `pause client1` from vtime 132.19 for 45.96 s, so until about 178.15. client1 returns all three replies 0.02 s after it
resumes. s103/act-s111 have lower sequence numbers than s124, so they were sent to R1 while client1 was frozen. The finish
was sent during the same window. It cannot have reached Cadence before 178.15: client1 was frozen. It cannot have been
applied to R1 either: client3's finish was accepted by R1 at 150.6, so R1 was open until then, and after that a signal to a closed R1
would fail with not_exists, not return ok. So the finish was applied exactly once, to R2.

### S1, 3e9720ca…-67-6 (master), counter-34, input_hash 1271476539303813343, vtime 260.07585
```
187.889 client3 signal_with_start counter-34 -> ok run 330bb06c                    (R1)
207.98..227.55 signals s113..s176 -> ok (R1 open)
227.569 client3 terminate -> ok                                                     (R1 terminated)
237.800 client1 start reuse=1 -> ok run 3f527d93                                    (R2)
239.8..246.3 signals s203..s282 -> ok
253.094..253.105 client2: act-s117, s115, s118, finish, cancel -> all ok            <- stragglers
260.033 client2 s325 -> not_exists                                                  (R2 closed)
260.074 client3 start reuse=1 -> ok run e68dd7f6   -> ASSERTION FAILS
```
Fault: `pause client2` at vtime 203 for 49.35 s, so until about 252.7. R1's signals s130..s176 were accepted after 203, so
client2's finish had not closed R1. All of client2's queued R1 ops ran against R2. Signals s115/s117/s118 from R1 also landed in
R2, which shows the same cross-run straggler effect.

### S1, bba4d2f0…-67-6 (v1.4.5 -b), counter-33, input_hash 5953826427138377917, vtime 354.71714
R1 d71ebbef was terminated at 96.42. Signals to it fail not_exists at 211.5, 248.4 and 293.4. RuleReuseClosed started R2 c958a7bd at 323.35
(R2 had no signals, so RuleFinish could not have picked it: it needs ≥3). At 325.27 `client1 signal finish -> ok`, which is the straggler
R1 finish. At 354.717 start reuse=1 is accepted and the assertion fails. 163 failing events are copies of this one origin across
branches (one WID; one run ID 5f020554 for 160 of them, two others for the rest).

Also on 3e97: counter-25 (R2 8eb712c5 at 243.18, straggler client3 finish at 264.24, failure 294.19) and counter-39 (R2 de1c07b3
at 627.16, straggler client2 finish at 627.23, failure 627.24).

### S2, 3e9720ca…-67-6 (master), counter-64, input_hash 527762185824317500, vtime 259.92086
R1 6fc9bb21 was cancelled at 224.11 (client2), and s163 was not_exists at 225.17. RuleReuseClosed started R2 b1a5febd at 259.40. At 259.884
`client3 cancel -> ok cancel_requested` arrived. This is a straggler: R2's model had CancelSent=false. At 259.92 start reuse=1 was accepted and the
assertion fails. The reuse=0 failing examples on this WID (4) are consistent: R2 closed as *canceled*, so AllowDuplicateFailedOnly
accepts.
f04f counter-30 (input_hash 1485287926154275737, vtime 130.26): R1 b802fcca completed (finish 80.8). R2 e6631ad5 was started by reuse at
121.73. A straggler client3 cancel was ok at 127.65; act-s82/s84 were not_exists at 130.05/130.25; then start reuse=0 was accepted and the assertion fails.

### S3, f04f3907…-67-6 (v1.4.5), counter-45, input_hash 2087319117120241482, vtime 302.67525
dd0e0d72 was cancelled at 216.6. Reuse e7ea196c at 226.14 (policy 0) was cancelled at 245.7 (its own cancel). Reuse 5b9f9dbc at 277.87 (R2).
At 284.49 `client3 terminate -> ok` is a straggler from the e7ea196c model (R2's TerminateSent was false). It arrived during cadence1/cadence3
pauses (250+11 s, 271+11.6 s). s92 was not_exists at 300.26. At 302.675 start reuse=0 was accepted, as allowed after a terminated run, and the assertion fails.

## Not a Cadence bug: false-positive review

- In each case, the closing op was applied to exactly one run (shown above for counter-40 and counter-34, where the client was frozen until after
  R2 started). This is not a cross-run duplicate of an op that was already applied, so it is not the known
  retried-signal-duplicated-after-reset pattern.
- Signal/Cancel/Terminate with an empty RunID resolve to the current run when the request is processed. This is documented
  Cadence (and Go client) behavior. The driver addressed these ops by workflow ID while it meant one specific run.
- Cadence accepted the starts only after the current run had closed. reuse=2 (RejectDuplicate) never fails, and reuse=0 accepts only
  after a cancel/terminate close. That matches start_workflow_execution.go `applyWorkflowIDReusePolicyHelper` (see
  rca/duplicate-start-accepted.md for the code path).
- Related property "a closed workflow ID is reused" passes on all runs; "a start on a closed workflow ID follows its reuse policy" is
  not in the failing set.

## Proposed driver fix

1. (Primary) Track in-flight ops per workflow ID. Add `w.OpsPending`, incremented before every signal/finish/cancel/terminate/reset
   `Call` and decremented in its completion callback. Every op resolves through that callback: a reply, an "unknown" timeout from the client,
   or "lost" when the client restarts (rpc.go:139-156). Decrement immediately on NotSent. In `RuleReuseClosed` (main.go:687), skip a
   candidate whose old model has `OpsPending > 0`, or whose last op ended "unknown" less than about 10 s ago. The second condition covers a
   request the server is still processing after the client gave up.
2. (Defense in depth) In the `RuleDuplicateStart` callback (main.go:390), when `w.PrevRunID != ""` (the WF came from RuleReuseClosed)
   and the outcome is ok, describe the model's run before asserting, and assert only if it is still Running. A real double-open
   survives this check.
3. (Optional) Pin close ops to a run: the client already passes `p.RunID` to Signal/Cancel/TerminateWorkflow
   (client/main.go:340-350), but the driver never sends `run_id` for these. Pinning stragglers to R1 would make them fail with not_exists.
   A counter with can_every>0 changes run ID on continue-as-new, though, so only pin when the run ID is known to be current
   (for example can_every=0). This is why (1) is the primary fix.

## Fix-verify

1a0d055c (fixverify-4, the same frozen hunt driver, 400 CH versus 600 CH for the hunts) has 0 failures. The race needs a client to hold a close op
across a reuse of the same ID. That is timing luck: each hunt run has only 1 to 4 origins. The fix-verify absence does not tie the
signature to any of the three fixed findings, and the mechanism above does not involve resets at all. RuleReuseClosed
excludes WFs with resets, and DupStart requires Resets==0.

## Not done / limits

- No server-side history check. The `snouty debug` launch on c26baba5 @ 3959925780484902406/186.1396 returned "the Antithesis API
  failed to complete the request" (proxy trace 2274cca86cb659416c4d2438af5b4b43), and later `runs list` calls timed out. It is unknown whether
  a session titled "second-start triage counter-40" was created; if one was, cancel it. The classification rests on the driver code
  invariant (flags set before send) plus the client pause windows. A history of R2 b8f14d01 should show WorkflowExecutionSignaled "finish"
  with identity client1 at about 20:52:50, plus R1's signals s103/act-s111.
- Side effect to expect elsewhere: the same stragglers make R2 close as canceled/terminated without a request in its model, and R1 signals
  (s103, s115...) appear in R2. "a workflow ends only as completed, or as canceled or terminated on request" and the
  signal-provenance checks may fail on these same WIDs for the same driver reason.
