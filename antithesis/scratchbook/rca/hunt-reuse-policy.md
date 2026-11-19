# RCA: "a start on a closed workflow ID follows its reuse policy" (hunt runs)

Property: driver/main.go:719 and :723 (callback of `RuleReuseClosed`, main.go:687). The rule picks a closed counter
workflow, draws a reuse policy (0 = AllowDuplicateFailedOnly, 1 = AllowDuplicate, 2 = RejectDuplicate) and starts
the same ID again. The assertion fails when the start is accepted although the policy forbids it, or rejected with
`already_started` although the policy allows it.

**Verdict: false positive (driver), two variants. No new Cadence bug, and no link to any known finding.**

## Counts (all failing events, from a fold query over every timeline; `--limit 999`, never reached)

| run | version | failing events | distinct WIDs | direction |
|---|---|---|---|---|
| f04f3907…-67-6 | v1.4.5 hunt | 76 | 39 | all `already_started` while allowed |
| bba4d2f0…-67-6 | v1.4.5 hunt -b | 75 | 44 | same |
| 3e9720ca…-67-6 | master hunt | 72 | 42 | same |
| c26baba5…-67-6 | master hunt -b | 62 | 36 | same |
| 1a0d055c…-67-6 | fix-verify (v1.4.5 + 3 fixes) | 59 | 35 | same |

None of the 344 failures is in the other direction (a start accepted although the policy forbids it). The policy and
previous status mix is broad on every run (policy 0 after canceled/terminated, policy 1 after
completed/canceled/terminated), for example f04f: 21× 0/canceled, 13× 0/terminated, 14× 1/canceled, 13× 1/completed,
15× 1/terminated.

## Signatures

| # | signature | f04f | bba4 | 3e97 | c26b | 1a0d (fix-verify) | classification |
|---|---|---|---|---|---|---|---|
| S1 | allowed reuse rejected `already_started`. In the same timeline, another `start` on that ID returned ok after the old run's last cancel/terminate and in the previous 25 s (median gap < 1 s) | 74 | 74 | 65 | 60 | 58 | **false positive**: two concurrent `RuleReuseClosed` workers pick the same closed WF |
| S2 | allowed reuse rejected `already_started`. No ok start on the ID is visible beforehand. The ID had a start whose outcome was unknown (timeout), or a start whose reply is not yet logged | 2 | 1 | 7 | 2 | 1 | **false positive**: a straggler start, still running in the server, creates the new run |

Both signatures also occur on the fix-verify run (59 events), so neither is explained by the three fixed findings. That
fits the cause: `RuleReuseClosed` excludes every WF with resets (main.go:695), and nothing here involves signals.

Raw data: antithesis/scratchbook/logs/reuse-policy/ (`fold_*.ndjson`/`q.txt` = the earlier ok start per failure;
`fold2_*.ndjson`/`q2.txt` = start/cancel/terminate RPCs on the ID in the 25 s before; `cls.py` = the S1/S2 split;
`log_*.ndjson` = timelines; `exec_*.txt` = cadence CLI output from debug sessions).

## S1: concurrent `RuleReuseClosed` on the same closed workflow (driver TOCTOU)

Mechanism:
- The driver runs `hegel.RunStateful(..., hegel.WithBoundedConcurrency(6))` (main.go:65-68). Per the hegel docs
  (hegel@v0.9.38 stateful.go:41-50), "Workers invoke rule methods concurrently on the same machine value".
- `RuleReuseClosed` builds its candidate list under `d.m.mu` (main.go:691-700): `!active && !w.Reused && ...`. It then
  **releases the lock**, makes two hegel draws (`pickOne` main.go:702, `hegel.Draw` policy main.go:706), and only then
  re-locks to set `old.Reused = true` and `d.m.wfs[wid] = w` (main.go:708-712). It does not re-check `Reused` or `active`.
- Two workers in the same round can therefore both pick the same `old` and send two starts on the same ID.
  Cadence accepts the first and correctly rejects the second with `WorkflowExecutionAlreadyStarted` ("already running",
  start_workflow_execution.go:976-980 in `applyWorkflowIDReusePolicyHelper`), because the first start has just made a
  new run current. The second callback then asserts `!allowed == true`, which fails.

Evidence:
- f04f3907…-67-6, input_hash 180923854027465604, vtime 24.730 (counter-3; log_f04_180923854027465604.ndjson):
  ```
  23.875 rpc client2 signal counter-3 finish -> ok                    (R1 6cda4c10 completes)
  24.687 rpc client2 start counter-3 reuse=1 -> ok run 9f7bdf77
  24.687 ASSERT reuse-policy cond=true  {policy:1, prev_status:completed, wid:counter-3}
  24.729 rpc client1 start counter-3 reuse=1 -> failed already_started
  24.730 ASSERT reuse-policy cond=false {policy:1, prev_status:completed, wid:counter-3}
  ```
  Both reuse-policy evaluations come from `RuleReuseClosed` callbacks with the same `prev_status` on the same ID, 42 ms
  apart. That can only happen when two rule invocations picked the same `old` WF object.
- bba4d2f0…-67-6, input_hash 4803227217212665893, vtime 470.204 (counter-36, the most common WID on that run, 34 events across branches):
  `434.16 cancel -> ok` (R1 a0f0620d), then `466.38 client2 start reuse=1 -> ok a4326e30`, then
  `470.20 client2 start reuse=1 -> failed already_started` (policy 1, prev canceled).
- Across all runs, 331 of 344 failures have such an ok start shortly before (fold2 + cls.py).

## S2: a straggler start lands after the old run closed

Mechanism: some start on the same ID (a `RuleDuplicateStart` sent while the old run was open, or a second
`RuleReuseClosed`) is still being processed by the server when the old run closes. The client may already have given up
on it (outcome `unknown`), or its reply is not yet back. Under the request's own policy, Cadence then legitimately
creates a new run, and the driver's later allowed reuse start is correctly rejected. A request whose client timed out
can still take effect. Cadence documents a timeout as an unknown outcome, and the client wrapper classifies it as
`unknown` (client/main.go:137-176). The driver does not account for this: `RuleReuseClosed` checks only
`DupPending == 0` and `len(Foreign) == 0` (main.go:695-696). `DupPending` is decremented when the client reports
"unknown", even though the request is still running in the server.

Evidence (proof, master):
- 3e9720ca…-67-6, counter-28, failing moments: input_hash 3193799471131491518 vtime 233.234, and -3319066136188459468 vtime
  268.290, among others. Timeline (log_3e97_m3319066136188459468_from_180.ndjson):
  ```
  143.04 client1 start counter-28 reuse=1 -> ok R1 40a03cc0
  208.25 client1 start reuse=2 -> unknown timeout        (RuleDuplicateStart while R1 open)
  210.25 client3 start reuse=1 -> unknown timeout        (RuleDuplicateStart while R1 open)
  223.45 client1 signal finish -> ok                      (R1 completes)
  223.98 cadence1 frontend: StartWorkflowExecution counter-28 "context canceled" (ts 20:53:36.119; cadence1 throttled 217.8-234.3)
  268.29 client1 start reuse=1 -> failed already_started   (RuleReuseClosed, prev completed)  <- assertion
  ```
  Debug session 9363395a…-67-6 at (-3319066136188459468, 268.46), `cadence wf list/show`. Open run
  **6554a2ec**, started 20:53:36 by **Identity:client3**, RequestID cc6369ba…, input `can_every:0, state:{}` (the
  DupStart/reuse input). Closed run 40a03cc0 (R1) ended 20:53:31 COMPLETED. client3's only start on this ID was the
  DupStart that timed out at 210.25, sent while R1 was open, so `RuleReuseClosed` could not have sent it. The server
  committed it about 14 s after the client's deadline, after R1 completed. AllowDuplicate permitted that, so Cadence
  behaved correctly.
- The other S2 moments I checked show the same picture: the current run is a new, unknown run, not the closed run
  the driver passed as `old`.
  - bba4 counter-91 (-5488588577362047517, 260.733): open run 380f0e6e started 20:54:12 by client1, while the failing
    start came from client3 at 20:54:12.875. The previous chain dea0f492 → (CAN) 049148f1 was COMPLETED at 20:53:56.
  - c26b counter-22 (632360567412760541, 397.076): open run b63f94f2 started 20:56:16 by client2, while the failing
    start came from client1 at 20:56:29. The old run ac3fd140 was CANCELED at 20:55:15.
  - f04f counter-88 (-7383897907967764785, 617.612): open run c09f20e5 started 21:00:09 by client3. The old run a90d3b2f
    was CANCELED at 20:54:51. Here the failing start also came through client3, so I checked that a retry inside the Go
    client cannot reject its own first attempt. Request IDs are fixed per call (internal_workflow_client.go:151, 405).
    A retry after a commit is deduplicated (start_workflow_execution.go:270-286, StartRequestID == RequestID). A
    retry racing its own uncommitted attempt gets `CurrentWorkflowConditionFailedError` (sql_execution_store.go:198-219).
    The history handler converts that to `InternalServiceError` (handler.go:2147), which the Go client retries; it is
    never `AlreadyStarted`. The master code is the same (handler.go:2196, start_workflow_execution.go:271). So
    c09f20e5 came from a different request, made concurrently.
  These three runs were created 0-13 s before the rejection by a request whose reply the timeline had not logged yet.
  That is S1 with the winner's reply still in flight, or a straggler. Either way the driver started it.

### Follow-up adversarial check (all remaining S2 origins without a visible unknown start)

To rule out a rejection that names the closed run itself, I checked four more IDs with the cadence CLI (debug sessions
6b0f47d8…-67-6 for master and 5b16e191…-67-6 for fix-verify). In each one, the current run is a new run and not the
driver's closed run:

| run | WID, moment | closed run (driver's `old`) | current open run | started by | failing start from |
|---|---|---|---|---|---|
| 3e97 | counter-31 (-1035736544479600478, 305.159) | 4dbeeb96 TERMINATED 20:54:36 | addd36f5, 20:54:57 | client1 | client1 |
| 3e97 | counter-1 (8245685479156348011, 359.487) | f31dd07a COMPLETED 20:50:19 | ffa572b1, 20:55:46 | client3 | client3 |
| 3e97 | counter-116 (-1021129056077309220, 580.509) | 8d8994a6 TERMINATED 20:58:03 | cd39e601, 20:58:06 | client1 | client2 |
| 1a0d (fix-verify) | counter-18 (3486381607590776759, 77.149) | 14c54c96 CANCELED 20:53:11 | 4c212337, 20:53:22 | client1 | client2 |

counter-31 turned out to be S1 that my heuristic missed. Its timeline (log_3e97_c31.ndjson) has **two** `client1 start
reuse=0` replies in the same microsecond (driver ts 20:54:57.296470): one `failed already_started` and one `ok addd36f5`.
The ok line is printed after the failure line, so the 25 s look-back did not see it. That makes the S1/S2 split a
lower bound for S1. Neither the split nor the false-positive verdict depends on it.

In total, the CLI checked 8 IDs across all 5 runs (counter-28/-1/-31/-116 master, counter-91 v1.4.5-b, counter-22
master-b, counter-88 v1.4.5, counter-18 fix-verify). Every rejection was caused by a newer run that a driver request
had created.

## Why this is not a Cadence bug

The property could only expose a server bug if the rejection named the closed run the driver chose. In every
moment checked by CLI (8 IDs, all 5 runs), Cadence rejected because of a **newer, open run** that a driver request created.
That is exactly what `applyWorkflowIDReusePolicyHelper` must do for a running current run
(start_workflow_execution.go:974-980). Across 344 events there is no case where the policy was violated by acceptance.

## Proposed driver fix

1. **Claim atomically** (fixes S1). In `RuleReuseClosed` (main.go:702-712), make the draws first. Then, in one critical
   section, re-check `!old.Reused`, check that no WF with that ID is in `d.m.wfs`, and only then set `old.Reused = true`
   and insert `w`. Return if the claim fails. Also keep a per-WID "reuse in flight" set, so that a second closed object
   with the same ID (an older generation) cannot be picked while a reuse is pending.
2. **Assert against the run Cadence names** (fixes S1 and S2, defense in depth). In the client (client/main.go:154),
   return `already.GetRunId()` (WorkflowExecutionAlreadyStartedError.RunId, the current run) in the result, for example
   `Value: {"current_run_id": ...}`. In the callback (main.go:722-724), assert `!allowed` only when the current run named
   by the error is `old.ClosedRunID` (or one of old's known run IDs). Otherwise the rejection came from a run the model
   does not own. Record that run as foreign, add `assert.Sometimes(true, "a reuse start meets a run the model doesn't know", ...)`,
   and drop `w`.
3. (Optional, S2) Track starts with an unknown outcome per workflow ID (`UnknownStartAt`, set from the
   RuleStart/RuleDuplicateStart/RuleReuseClosed callbacks when the outcome is unknown/lost). Skip reuse candidates
   whose ID had such a start in the last ~60 s, in the same way as the close-op stragglers in rca/hunt-second-start.md.

## New bugs

None.

## Debug sessions

All started by this triage, and all cancelled: 9363395a…-67-6, 15c5a45b…-67-6, 245d8693…-67-6, a010f91b…-67-6,
6b0f47d8…-67-6, 5b16e191…-67-6.
