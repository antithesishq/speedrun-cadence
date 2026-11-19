# RCA: liveness properties in the long hunts ("once faults stop")

Properties covered: "every client is served again once faults stop" (driver/main.go:553),
"every started workflow closes once faults stop" (main.go:646), and "a new workflow completes within 2 minutes
once faults stop" (main.go:579).

Runs: v1.4.5 f04f3907…-67-6 (hunt), v1.4.5-b bba4d2f0…-67-6, master 3e9720ca…-67-6, master-b c26baba5…-67-6,
fix-verify 1a0d055c…-67-6 (v1.4.5 + the three non-v2 fix patches).
Raw data is in antithesis/scratchbook/logs/hunt-liveness/. `served_<run>.json` and `closes_<run>.json` hold every
failing evaluation, with details, taken from `runs search --limit 999`; the totals equal the counterexample counts.
`log_*.ndjson` are the timelines, and `p26_exec.txt` / `chain57_exec.txt` hold the debug-session output.

## Property status

| property | f04f v1.4.5 | bba4 v1.4.5-b | 3e97 master | c26b master-b | 1a0d fix-verify |
|---|---|---|---|---|---|
| a new workflow completes within 2 minutes | pass (8.6k/0) | pass | pass | pass | pass |
| every client is served again | 173 fail | 346 | 159 | 207 | 127 |
| every started workflow closes | 230 fail | 255 | 250 | 258 | 154 |

The probe property never fails, so it is not discussed further.

## Signatures

The counts are failing examples, meaning moments, and most of them are sibling branches of a few origins. One
"closes" example can list stragglers from several signatures, so the row counts can add up to more than the total.

| # | signature | f04f | bba4 | 3e97 | c26b | 1a0d (fix-verify) | classification |
|---|---|---|---|---|---|---|---|
| L1 | served: every client is NotSent (status 3, `outstanding:{}`), or one client times out with `"round-robin" peer list has 3 peers but none are responsive` | 173 | 346 | 159 | 207 | 127 | **false positive (harness/environment)**: every SUT container gets SIGTERM and is never restarted |
| C1 | closes: counter, current run `terminated`, resets>0, terminate sent | 162 | 159 | 37 | 166 | 115 | **false positive (driver)**: the driver's own terminate closes a run that is in ResetBases only because a reset of it did not commit |
| C2 | closes: parent open, cancel sent, no pending children | 66 | 27 | 104 | 59 | 2 | **NEW real bug, Go client v1.4.0**: `child-cancel-ack-after-close` |
| C3 | closes: counter open, history_length 4, no resets | 0 | 1 | 98 | 18 | 0 | **false positive (driver)**: eventually's `finish` lands before the in-flight start creates the workflow |
| C4 | closes: counter whose current run is a Foreign run (completed or canceled) | 9 | 76 | 14 | 24 | 37 | **false positive (driver)**: the read with `run_id:""` resolves to a duplicate-start run that the model rejects forever |
| C5 | closes: chain-57 open, 1 pending activity in SCHEDULED state | 0 | 0 | 0 | 7 | 0 | **suspect**: a retried activity task never reaches a worker after a shard move. One origin, master-b only; see below |

None of the signatures is a consequence of the three fixed findings. Every signature except C3 and C5 also appears
on the fix-verify run.

---

## L1: the whole SUT is shut down, then eventually runs against dead containers (harness)

Evidence, from three runs, each looked at on its failing timeline:

- f04f, moment -4704302780345159393 @ 436.27 (log_f04_served_from200.ndjson):
  - 256.225: postgres logs `received smart shutdown request`.
  - 256.225–256.229: the driver logs `client client1/2/3 connection closed`.
  - 256.25: `eventually: stopping faults`.
  - 256.78: `containers_meta died` for all three cadence-client containers.
  - 286.78: the cadence server dies, 30 s later (docker stop grace period).

  Nothing restarts afterwards: no init/start events, and only cadence1 logs until it dies. cadence2 and cadence3
  had been `stop`ped earlier and never came back. The fault injector logs **no** fault for this.
- bba4, 640906171981816584 @ 385.41: at 201.56 postgres logs `received fast shutdown request` and dies. At 205.33 all
  three client connections close, at 205.38 eventually starts, and at 205.49 the three client containers die.
- c26b, -8035818672372402696 @ 735.01: at 539.95 postgres logs `smart shutdown`. At 539.98 clients 2 and 3 close,
  and at 540.52 the client containers die. eventually starts at 555.00. client1 survives, but no frontend answers it,
  so it shows the "round-robin … none responsive" variant. The servers die at 570.5.

- 3e97 master, 5769113687084749989 @ 434.35 (log_3e97_served_150_236.ndjson + log_3e97_served.ndjson): at 177.26–177.30
  the client connections close, and at 177.51 three cadence-client containers die. No fault event comes with this:
  the only faults are 10 ms throttles. Then the servers die at 210.3, 218.8 and 233.3, and none restarts. The last
  cadence/client log line is at 232.6. At 234.34 eventually starts, and from then on only postgres and the driver
  log. Postgres stays up here.

Postgres shutdown on its own is **not** the marker: `received smart/fast shutdown request` happens ~200 times per run,
because postgres `stop` faults are routine. The marker is: all client containers die together without a fault event,
the server containers die within about 30–55 s (a stop grace period), and **nothing restarts**.

Mechanism: some agent other than the fault injector SIGTERMs every container except the fault-excluded driver, the
way `docker stop` does. It looks like environment teardown of the branch. Once every client socket is gone,
`Clients.Call` returns NotSent at once (rpc.go:166). Rule steps then cost nothing, so `RuleEventually`'s 0.3% chance
fires within milliseconds, and eventually spends 3 minutes calling dead sockets. Cadence plays no part: postgres
itself receives the shutdown.

Fix (driver, main.go `eventually` step 1): before asserting, check whether the environment is gone. For example, if
no client socket can be dialed (`/clients/<name>.sock` is missing, or every attempt returns NotSent) for the whole
window, call `assert.Reachable`/log "environment shut down" and `os.Exit(0)` without asserting. Report a client as
down only if its socket was reachable at least once during the window, or require a `start` reply with
`ErrKind=="timeout"` for a client before counting it as down. Also skip `RuleEventually` while `d.cs` has no live
connection. Antithesis should still be asked why the branch is torn down mid-timeline: the timing is random (vtime
201 / 256 / 540) and no fault event is logged.

## C1: the driver terminates a run left in ResetBases by an uncommitted reset (driver)

This is the "remaining edge case" already predicted in rca/fixverify-stragglers.md.

Evidence, f04f counter-106, moment -1219360447517997486 @ 1020.43 (log_f04_c106.ndjson), from the driver rpc lines:

```
473.29 client3 reset -> ok {"base_run_id":"7f6fd7a5…","run_id":"c34724f8…"}
496.28 client3 reset -> unknown timeout {"base_run_id":"c34724f8…","point":8}   <- ResetBases[c34724f8]=true
505.37 cadence2: "Workflow execution terminated." run c34724f8 wf-termination-reason="antithesis"   (driver's terminate, not "antithesis-reset")
506.62 client1 terminate -> failed not_exists   (the Go client's internal retry after the first attempt succeeded)
509.9 … signals/finish -> not_exists (workflow closed)
```

The straggler details show `describe` = c34724f8 current, closed, terminated, `resets=4`, `terminate=true`. Every
read uses `run_id:""` → c34724f8 `terminated`. `resultDone` (model.go:219) rejects it, because c34724f8 is in
ResetBases and the status is terminated. The 496 reset never committed: its only "terminated" log line was the
in-memory transition, there is no new run, and c34724f8 stayed current.

The same pattern appears on fix-verify: counter-12, moment -2252663147603764577 @ 928.13. Lines
`Workflow execution terminated` with reason `antithesis-reset` for run 66074a49 at 118/122/124 are in-memory only:
the reset at 125 is `unknown timeout`, and 66074a49 is still current at 928. The real close is at 446,
`reason=antithesis`, and matches `rpc client1 terminate -> ok "terminated"`.

Fix: the termination reason is the discriminator. A reset terminates with "antithesis-reset" (client/main.go:614),
and the driver terminates with "antithesis" (client/main.go:350).

1. client/main.go `getResultOfRun`, terminated branch: read the close event and return its reason.
   ```go
   case errors.As(err, &term):
       out := map[string]interface{}{"status": "terminated"}
       it := cc.GetWorkflowHistory(ctx, p.WID, p.RunID, false, s.HistoryEventFilterTypeCloseEvent)
       if it.HasNext() { if ev, e := it.Next(); e == nil && ev.WorkflowExecutionTerminatedEventAttributes != nil {
           out["reason"] = ev.WorkflowExecutionTerminatedEventAttributes.GetReason() } }
       return ok(out)
   ```
2. driver/model.go:219 resultDone: reject a terminated base run only when it was terminated **by a reset**.
   ```go
   resetTerm := r.Status == "terminated" && r.Reason != "antithesis"   // "antithesis-reset" or unknown
   if w.ResetsPending > 0 || (w.Resets > 0 && (r.RunID == "" || (w.ResetBases[r.RunID] && resetTerm))) {
   ```
   Add `Reason string \`json:"reason"\`` to resultVal. Make the matching change at model.go:233
   (`r.Status=="terminated" && … && !w.TerminateSent`): with a reason available, use `r.Reason=="antithesis-reset"`.
   If the history read fails, a smaller fallback is to accept a terminated base when `w.TerminateSent` is set and
   a fresh `describe` still names that run as current.
3. model_test.go: `WF{Resets:1, ResetBases:{"A":true}, TerminateSent:true}` + `{"status":"terminated","run_id":"A","reason":"antithesis"}` → true;
   the same with `"reason":"antithesis-reset"` → false.

## C2 (NEW, real, Go client): `child-cancel-ack-after-close`

**Title:** a parent workflow that cancels its children gets stuck forever when a child closes as canceled before
the server records the parent's cancel-request acknowledgement.

**Mechanism.**
1. The parent (ParentWorkflow, client/workflows.go:261) is cancelled. The Go client cancels each started child with
   a RequestCancelExternalWorkflowExecution decision (ChildWorkflowOnly=true), and the server records
   `RequestCancelExternalWorkflowExecutionInitiated`.
2. The transfer task (service/history/task/transfer_active_task_executor.go:603-653 in v1.4.5, :685-727 in
   master) first cancels the child, then records `ExternalWorkflowExecutionCancelRequested` in the parent. Under
   faults the second step fails ("Failed to cancel external workflow execution") and the task is retried. Even
   without faults, the two steps are separate cross-shard operations.
3. Meanwhile the child handles the cancel and closes as canceled, and the parent records `ChildWorkflowExecutionCanceled`.
4. The task retry calls RequestCancel on the closed child again. Because the request ID matches, the server
   answers Noop success (engine/engineimpl/request_cancel_workflow_execution.go:57-63, same at master :62) and
   records `ExternalWorkflowExecutionCancelRequested` in the parent **after** `ChildWorkflowExecutionCanceled`.
   Both events are legitimate. A non-transient error would record `RequestCancelExternalWorkflowExecutionFailed`
   instead, with the same effect.
5. The Go client (go.uber.org/cadence v1.4.0, internal/internal_decision_state_machine.go) handles the events as follows:
   - `ChildWorkflowExecutionCanceled` in state CancellationDecisionSent → base `handleCanceledEvent` :415-422 →
     `moveState(decisionStateCompleted)`. `moveState` :318-322 **deletes** the child's state machine.
   - The later `ExternalWorkflowExecutionCancelRequested` → `handleExternalWorkflowExecutionCancelRequested`
     :901-913 → `getDecision(ChildWorkflow, wid)` :691-697 → `panicIllegalState("unknown decision … nondeterministic")`.
     A late `…CancelFailed` takes the same path (:915-929).
   - With the default NonDeterministicWorkflowPolicy (block), every decision task fails with
     `WORKFLOW_WORKER_UNHANDLED_FAILURE`. The parent never closes; only its 2 h execution timeout would end it.

   The asymmetry is the bug: a *completion* event in CancellationDecisionSent goes to
   `CompletedAfterCancellationDecisionSent` (:389-390) and keeps the machine to await the cancel outcome. A
   *canceled* event drops it. Upstream master is unchanged (checked github cadence-go-client master: the same
   handleCanceledEvent and handleExternalWorkflowExecutionCancelRequested).

**Evidence.** f04f parent-26 run 4e4fd0aa, moment 9190701013436246952 @ 600.93. The debug session
4fa7263f… ran `cadence wf show` (p26_exec.txt):
```
16 RequestCancelExternalWorkflowExecutionInitiated {parent-26/c0, ChildWorkflowOnly:true}
17 RequestCancelExternalWorkflowExecutionInitiated {parent-26/c2, ChildWorkflowOnly:true}
18 ChildWorkflowExecutionCanceled  {parent-26/c0, InitiatedEventID:5, StartedEventID:8}
20 ExternalWorkflowExecutionCancelRequested {InitiatedEventID:17, parent-26/c2}
22 ExternalWorkflowExecutionCancelRequested {InitiatedEventID:16, parent-26/c0}     <- after 18
24 DecisionTaskFailed {Cause:WORKFLOW_WORKER_UNHANDLED_FAILURE, Details:[unknown decision DecisionType: ChildWorkflow, ID: parent-26/c0 …]}
29 DecisionTaskFailed (same)   … repeats on every worker (client1 295.1, 305.1, 315.2; client3 368.1; client2 482.7 …)
```
Server log: at 257.56 cadence2 logs `Failed to cancel external workflow execution` for parent-26, which is the
retry of step 2. In every run the client logs `unknown decision DecisionType: ChildWorkflow` (4 log lines per
occurrence; event counts f04f 3340, bba4 1752, 3e97 5988, c26b 3036, 1a0d 1172). The WIDs with this panic include
every parent straggler sampled: f04f parent-26/67/94/111, bba4 parent-41/300, c26b parent-7/35/36/48,
1a0d parent-3, and 3e97 parent-2 (3392 matching log events).

**Severity: medium.** Liveness only, with no data loss. Any workflow that cancels a child (or a parent that is
cancelled with started children) can wedge permanently, needing a manual terminate or reset. The race only needs
the child to close before the parent records the ack, which happens easily under load or with a transfer-task
retry. The fix belongs in the client. Two options:
- (a) In `childWorkflowDecisionStateMachine.handleCanceledEvent`, from CancellationDecisionSent move to
  `CompletedAfterCancellationDecisionSent`, as for completion, so that the ack or failure event finds the machine.
- (b) In `handleExternalWorkflowExecutionCancelRequested` / `…Failed`, ignore a child ID whose machine is already
  gone.

A server-side mitigation is also possible: skip recording the ack when the parent's mutable state has no pending
child for that initiated ID. Our workflow code is not at fault: the parent only calls ExecuteChildWorkflow and
Get, with the default WaitForCancellation=false.

**Not explained by known findings:** it is present on fix-verify (parent-3, parent-15, …), and no reset or
signal path is involved.

## C3: eventually's finish signal beats an in-flight start (driver)

master 3e97 counter-13, moment 7016960955083321322 @ 352.78 (log_3e97_c13.ndjson):
```
46.84 eventually: stopping faults
47.62 rpc client1 signal wid=counter-13 signal=finish -> failed not_exists
47.82 rpc client1 start wid=counter-13 reuse=2 -> ok {"run_id":"47d2fd8f…"}   (start was in flight)
```
The finish loop (main.go:597-604) stops on `not_exists`, and the start succeeds 0.2 s later. The counter waits for a
finish that never comes (describe: open, history_length 4). The same pattern shows for c26b counter-33
(`finish -> not_exists` at 210 after `start -> ok` at 183 on another branch) and 3e97 counter-30/82.

Fix (main.go eventually step 3): before sending finishes, wait until no start is outstanding. For example, poll
until `w.StartState != StPending` for every counter, or wait for `d.cs.Outstanding()` to drain, with a bound of
60 s. Also send finish again inside the straggler loop: if a counter is not closed and its finish reply was
`not_exists`, send finish again before every `readResult`. The signal is idempotent for the check.

## C4: the read lands on a Foreign run that the model rejects forever (driver)

fix-verify 1a0d counter-93, moment 806859002284794927 @ 1103.38 (log_1a0d_c93_from300.ndjson):
```
736.35 client2 reset -> ok {"base_run_id":"abd42f8b…","run_id":"ec85786b…"}    (own run)
738.02 client2 signal finish -> ok                                               (ec85786b completes)
741.09 client1 start reuse=1 -> ok {"run_id":"85f1a900…"}   (RuleDuplicateStart, closing=true -> Foreign[85f1a900])
744.47 client3 signal finish -> ok                                               (lands on 85f1a900, completes it)
```
Because Resets>0, `readResult` sends `run_id:""`. The client pins the read to the current run, 85f1a900, and
`resultDone` returns false at model.go:214 (`w.Foreign[r.RunID]`) on every read. The model's own last run, ec85786b,
completed and is never read. The same shape appears in every run (f04f counter-86/77, bba4 counter-10, c26b counter-40/379,
3e97 counter-10/47), often after the hunt-second-start straggler race (rca/hunt-second-start.md).

Fix: track the model's latest own run ID (`w.CurRunID`, set from the start reply and from each reset reply's
`run_id`). In `readResult`, when Resets>0 and the last reply named a Foreign run, read `run_id: w.CurRunID`
instead of `""`. Alternatively, when the current run is Foreign and closed, read `list` and take the newest
non-Foreign run.

## C5: chain-57 (master-b only, 7 examples, one origin), suspect

c26b moment -7207163401555546203 @ 601.42. chain-57 has been open since about 280: history_length 11 and 1 pending
activity. At 280.21 client2 reports a planned activity failure. cadence2 logs `Encounter stale mutable state in
RecordActivityTaskFailed` and then `Failed to lock shard … range 6 → 7` for shard 12. Shard 12 moves to cadence3 at
280.19. After that, nothing about chain-57 appears in any log until 601. The activity has StartToClose 15 s and retry
2 h, so the activity timeout timer, or the retry, should have fired on cadence3 within about 15 s.

C5 debug: session d6516961…, exec at the failing moment. The output is in chain57_exec.txt, chain57_db.txt and
chain57_tl.txt.
- `wf desc`: PendingActivities = [{ActivityID 1, **State SCHEDULED**, Attempt 1, ScheduledTimestamp 20:54:33,
  LastFailure "planned failure" from client2}]. There is no pending decision. The DB clock is 20:59:53.
  For 5 min 20 s after faults stopped, the retried attempt was never started, although all three workers were
  polling tl-main and other workflows made progress.
- History: 11 events. Event 11 schedules activity 1, with StartToClose 15 s, ScheduleToStart 3600 s, and retry of
  1–3 s for 2 h.
- postgres, shard 12 (range 9 now): **no transfer_tasks** are pending. The earliest timer_task is 8388609, due at
  21:54:33.322. That is the ScheduleToStart timer of attempt 1 (scheduled at 20:54:33 plus 3600 s), written at
  range 8. So the retry timer fired, the attempt was scheduled, and its activity transfer task was processed and
  deleted. There is no row in matching `tasks` for tl-main. The only 7 rows belong to a sticky decision list of
  client3. The tl-main activity task_list (range 18) has no backlog.
- Conclusion so far: history handed the attempt-1 activity task to matching, or believed it had done so. Matching
  neither persisted it nor delivered it to a worker that recorded the start. The task is lost until the
  ScheduleToStart timeout, 1 h later. One origin only, on master-b (1 task-list partition, 16 shards). Shard 12
  moved twice: from cadence2 to cadence3 at 280.19 (range 7→8), then once more to range 9.
- Classification: **suspect, unresolved**. It may be a lost activity task in matching sync-match or AddTask during
  matching or shard ownership churn. That would be a real liveness bug, but the current data cannot rule out a task
  that was sync-matched to a poller whose `RecordActivityTaskStarted` failed and was then dropped. That is
  documented-ish behavior: Cadence relies on the ScheduleToStart timeout to recover. Next step: a targeted run with
  matching debug logging (`AddActivityTask`, sync-match, `RecordActivityTaskStarted` errors) for tl-main, or
  ScheduleToStart set to 60 s, to see whether recovery happens by timeout.

## Notes on false-positive review

- L1 is not a Cadence crash. The shutdown reaches postgres (`smart/fast shutdown request`) and the clients at the
  same instant, with no fault event, and nothing restarts. That is why the probe property never fails: it runs only
  when step 1 passes.
- C1, C3, C4: in each case the Cadence state the driver read is consistent with the requests it sent. Every
  rejection is a driver model rule.
- C2: the event order is produced by legitimate server behavior, namely transfer-task retry plus idempotent cancel
  by request ID. The crash is the client's state machine, so this counts as a Go client finding, like
  retried-decision-heartbeat-stalls-workflow.

## Debugging sessions

Both sessions were started for this triage and are now cancelled: 4fa7263f2c34dde70377f2ac63973746-67-6 (f04f
parent-26) and d6516961dcb68060c30d8fbbb19e9a41-67-6 (c26b chain-57).
