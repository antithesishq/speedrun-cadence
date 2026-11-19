# hunt-unexpected-close: "a workflow ends only as completed, or as canceled or terminated on request"

Verdict: **false-positive (driver)**, one root cause behind every signature. A cancel or terminate RPC
is sent by workflow ID only (no run ID). It is still in flight when the driver closes that run in its
model and `RuleReuseClosed` starts a new run on the same ID. Cadence resolves an empty run ID to the
*current* run (documented behaviour), so the late request cancels or terminates the new run. The new
run's model object never asked for it. Not a Cadence bug, and not one of the known findings.

## Signatures (failing-example sample from scripts/details.sh, limit 200; total counterexamples in brackets)

| signature (status / cancel_sent / terminate_sent / resets) | v1.4.5 f04f3907 [199] | v1.4.5-b bba4d2f0 [187] | master 3e9720ca [158] | master-b c26baba5 [42] | fix-verify 1a0d055c [0] | classification |
|---|---|---|---|---|---|---|
| terminated / cancel=true / term=false / 0 | 145 (counter-45) | – | – | 42 (counter-1) | 0 | FP: late terminate lands on the reused run |
| terminated / cancel=false / term=false / 0 | 44 (counter-45) | – | – | – | 0 | FP: same (cancel not yet sent on that branch) |
| canceled / cancel=false / term=true / 0 | 3 (counter-30) | 159 (counter-1 125, counter-43 34) | 2 (counter-64) | – | 0 | FP: late cancel lands on the reused run (its own terminate then hits a closed run) |
| canceled / cancel=false / term=false / 0 | 7 (counter-30) | 28 (counter-43) | 6 (counter-64) | – | 0 | FP: late cancel lands on the reused run |
| canceled / cancel=false / term=false / resets=1 | – | – | 150 (counter-64) | – | 0 | FP: late cancel lands on the reused run, which the driver then resets (it may reset, since its object has no CancelSent) |

Every log-checked example (counter-30, counter-45, v1.4.5-b counter-1, counter-64, master-b counter-1) is on a counter
workflow whose ID was reused (`RuleReuseClosed`). v1.4.5-b counter-43 was not log-checked. It is classified by its
identical details shape. Each run has only 1–3 distinct IDs, and Antithesis branches multiply them. Fix-verify: 0 of 5.5k. Reuse is just as frequent
there ("a closed workflow ID is reused" 2.9k vs 3.6k on f04f3907). The race (an in-flight cancel or
terminate RPC crossing a reuse start) simply did not happen in that run. It has nothing to do with the three
fixed findings, and none of the known findings (reset / signal dedup / shard steal) is involved.

## Mechanism (driver)

- `RuleCancel` / `RuleTerminate` (driver/main.go:301-329) pick any `!Closed` workflow, set
  `CancelSent` / `TerminateSent` on *that model object*, and call `cancel` / `terminate` with
  `{"wid": ...}` only. The client (client/main.go:344-350) passes `p.RunID == ""` to
  `CancelWorkflow` / `TerminateWorkflow`.
- The Go client retries these RPCs internally under faults, so a reply can arrive seconds later.
- Meanwhile `RuleResult` reads the old run's outcome and closes it in the model. `retire` moves it to
  `m.closed`. `RuleReuseClosed` (main.go:687-733) then picks it (no check for in-flight cancel,
  terminate or signal RPCs on the old object), creates a fresh `WF` with no `CancelSent` /
  `TerminateSent`, and starts a new run.
- Cadence: with an empty run ID (and no FirstExecutionRunID), cancel and terminate resolve to the
  current run: src/cadence-v1.4.5/service/history/engine/engineimpl/request_cancel_workflow_execution.go:44-49,
  terminate_workflow_execution.go:42-47 (same in master). The late request hits the new run, which is
  correct for a request that names no run.
- The new object's result read returns canceled/terminated, so `model.go:237-240` fires.

## Evidence

1. v1.4.5 f04f3907, moment 7800478010011978542 @148.11 (logs/hunt-unexpected-close/v145-7800478010011978542.log), counter-30:
   run b802fcca started 62.4. `finish` at 80.8 (the object then has FinishSent, and RuleCancel does not exclude that). Reuse start
   (AllowDuplicate) at 121.73 created run e6631ad5. `cancel wid=counter-30` was already in flight (frontend RequestCancel
   errors at 122.51 and 127.53, run-id "") and returned `cancel_requested` at 127.65. At 138.58 a
   `RuleDuplicateStart` on the new object succeeded (run 63cc9fe6), and "a second start of an open workflow
   ID is rejected" FAILED. That assertion runs only if the object has no CancelSent (`closing` false,
   main.go:392-409). So the cancel that closed e6631ad5 was not sent by e6631ad5's object. It came from the
   old object (b802fcca's), the only other object for the ID. The result read at 148.11 then reports
   `canceled, cancel_sent=false`.
2. v1.4.5-b bba4d2f0, moment -242302737537104366 @113.33 (v145b--242302737537104366.log), counter-1: reuse start at 99.62
   (run 5772096c). `cancel` → `cancel_requested` at 100.74, 1.1 s later. The new object's own `terminate` at 108.31
   → `not_exists` (the run was already canceled), so terminate_sent=true. Result: `canceled, cancel_sent=false,
   terminate_sent=true`.
3. master 3e9720ca, moment -1414099584247572294 @279.41 (master-c64--1414099584247572294.log), counter-64: run 6fc9bb21
   canceled at 224.1. Reuse start at 259.87 (run 3b76d704). A second `cancel` → `cancel_requested` at 260.48. `RuleReset`
   then reset 3b76d704 at 262.58 (new run cd628764). RuleReset excludes CancelSent objects, which shows
   this cancel was not the new object's. Result: `canceled, cancel_sent=false, resets=1`.
4. master-b c26baba5, moment -7912655328598155536 @43.17 (masterb-c1.log), counter-1: run caed9173 terminated at 37.35
   (`Workflow execution terminated` on caed9173). Reuse start (policy 0) at 41.82 created run bac971a5. A
   `terminate` → `terminated` at 42.15, and the server logs `Workflow execution terminated` for **bac971a5**.
   The new object has terminate_sent=false and a cancel of its own in flight. Result: `terminated, cancel_sent=true`.
5. v1.4.5 f04f3907, moment 1564061829298631577 @527.10 (v145-c45-1564061829298631577.log), counter-45: three runs
   (dd0e0d72 @179, e7ea196c @226 after a cancel, 5b9f9dbc @277.9 after another cancel). `terminate` → `terminated`
   at 284.49 hit the third run. Its object has cancel_sent=true and terminate_sent=false.

Adversarial check (alternatives ruled out): each cancel/terminate reply is a separate driver RPC, logged at reply
time. A server-initiated cancel would not produce a `cancel_requested` reply. The old object's result read is pinned
to its FirstRunID, so it cannot misattribute the new run's outcome to itself. The failing object is the new one: it was
reset in (3), and its start reply in (2), (4) and (5) is logged before the close.

No history read was needed: the server log in (4) names the run that was hit, and in (1) and (3) the
driver's own guards (DuplicateStart and Reset exclude CancelSent) prove the cancel was not issued
by the new object.

## Proposed driver fix

1. Track in-flight targeting RPCs per model object: add `WF.Inflight int`. Increment it before
   `cancel`, `terminate`, `signal` (add and finish) and `reset`, and decrement it in the completion
   callback, or right away when `Call` returns `NotSent`. In `RuleReuseClosed`, require
   `w.Inflight == 0 && time.Since(w.ClosedAt) > 15*time.Second`. The grace period covers a request that
   the frontend or history is still processing after the client gave up.
2. Better, or in addition: pin cancel and terminate to the chain. The client sends
   `FirstExecutionRunID = w.FirstRunID` using the raw service client. Cadence then rejects the request
   with EntityNotExists when the current run belongs to another chain
   (request_cancel_workflow_execution.go:70-82, terminate_workflow_execution.go:69-82). This keeps
   continue-as-new working, whereas pinning the plain run ID would not. Continue-as-new carries the first run ID.
   For a reset run, check that the rebuilt run keeps the base chain's FirstExecutionRunID
   (mutable_state_builder_methods_started.go:208-211). Not verified here. Reused IDs are never reset
   *before* reuse, so fix 1 alone is enough. The Go client v1.4.0
   `CancelWorkflow` / `TerminateWorkflow` do not expose this field.
3. Until fixed, the checker can tolerate it. If an earlier object for the same WID has a cancel or
   terminate whose reply arrived after this object's start was sent, accept that status.

Collateral (same race, other properties, not triaged here): "a second start of an open workflow ID is
rejected" (125 failures on f04f3907, including example 1). Possibly some "a query shows each delivered
signal once" `unsent` reports and "a start on a closed workflow ID follows its reuse policy" failures,
where late signals or starts cross a reuse. Fix 1 covers signals too.

## New bugs

None.
