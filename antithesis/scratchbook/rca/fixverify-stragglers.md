# RCA: "every started workflow closes once faults stop" with CLOSED stragglers (fix-verify run)

Run 41bf06723b005a4e1c4cc37b560fb195-67-6 (Cadence v1.4.5 + retried-signal-duplicated-after-reset.patch +
sigterm-during-startup-panics.patch). FAIL at vtime 465.93 (moments 6652577969041795740, -1826839295900939790,
-7181909216762585410, 8668487818062240540, 5311664311032078649: sibling branches of one eventually).
Stragglers (capped at 10): counter-14, counter-16, counter-4, parent-10, race-17, race-18, race-2, race-20, race-22,
race-23. `describe` shows every one CLOSED (completed, or canceled for race-20).

**Classification: false-positive (driver bookkeeping bug). It has nothing to do with the fix patch, and Cadence
behaved correctly.** The same symptom has since shown up in the v1.4.5 run (f3f927373e9dff17bfad9fd1d1f1a923-67-6,
3 failing moments at vtime 711.7, first straggler counter-7) and the master run (75b0745bcdbcc355168db60a0ae5c940-67-6,
8 failing moments, first straggler counter-3). In both, the first straggler has an `unknown timeout` reset whose
base is the run `describe` reports.

Raw data: antithesis/scratchbook/logs/fixverify-stragglers/ (log_6652577969041795740.ndjson is the full timeline,
c14.json holds the driver RPC lines for counter-14 across the run, exec2.json holds the moment exec).

## Timeline for counter-14 (the timeline of moment 6652577969041795740)

| vtime | event |
|---|---|
| 36.59 | `rpc client2 signal_with_start wid=counter-14 sig=s1 -> ok {"run_id":"2982917c-…"}` |
| 84.6, 97.35 | signals s10, s6 `unknown timeout`; act-s3 `ok` |
| 108.32 | cadence1 history logs `Workflow execution terminated … run 2982917c … reason antithesis-reset` (an in-memory mutable-state transition inside the reset) |
| 112.7–118.4 | `ResetWorkflowExecution` for 2982917c fails again and again: `AppendHistoryEvents operation failed. Error: context canceled` (handler.go:1292), `getActivityInfoMap … context canceled`, `grpc: the client connection is closing`. Faults: postgres clogs, a partition of ALL at 116.7, client2 paused |
| 117.35 | `rpc client3 reset wid=counter-14 -> unknown timeout {"base_run_id":"2982917c-…","point":5}` |
| 163.79 | `eventually: stopping faults` |
| 165.82 | `rpc client1 signal wid=counter-14 signal=finish -> ok` |
| 165.8 to 465.9 | eventually re-reads counter-14 for the full 5-minute deadline and never accepts a result (the driver does not log `result` RPCs) |

## What Cadence holds (exec at 465.93 on client1's own socket; exec2.json)

- `result {wid:counter-14, run_id:""}` returns `ok`, `status: completed`, `run_id: 2982917c-…`,
  `applied: [s1, act-s3, s24, s29]`, `sum 209, runs 1`.
- `list` returns one run only, 2982917c, closed and completed. No reset run exists.
- `history` of 2982917c has 21 events: Started, Signaled s1, decision 3-5, Signaled act-s3, DecisionTaskTimedOut, …,
  Signaled s24, s29, finish, activity, then `WorkflowExecutionCompleted` (event 21). It has **no**
  `WorkflowExecutionTerminated`.

So the reset never committed. The history service had applied the termination to the in-memory mutable state, but the
`AppendHistoryEvents` write failed, so the transaction was discarded. That is correct, atomic behavior. The base
run stayed current, received `finish` from eventually, and completed.

## Why the driver never accepts it

The `reset` callback (main.go RuleReset) runs on any outcome. For the timed-out reset it did `ResetsPending--`,
`Resets++` and `ResetBases["2982917c"] = true`. The client puts `base_run_id` into the reply even on error
(client/main.go `reset`: `res.Value = {"base_run_id": runID, "point": pt}`).

From then on, `readResult` sends `run_id: ""` because `Resets > 0`. Every reply names 2982917c, which is completed,
and `resultDone` (model.go:217) rejects it:

```go
if w.ResetsPending > 0 || (w.Resets > 0 && (r.RunID == "" || w.ResetBases[r.RunID])) {
    return false // a reset's base run (or an unnamed run): not the final outcome; read again
}
```

The rule assumes that a run used as a reset base was really reset. A reset whose outcome is unknown or failed may
never have taken effect, and then its base *is* the final run. It stays stuck forever: `w.Closed` never becomes
true. The other fields are fine: `ResetsPending` is 0, `PrevRunID` is empty and `Foreign` is empty.

## Why the other nine stragglers are listed

They are collateral damage. `eventually` step 3 walks the workflows in ID order (`pick` sorts by WID), with **one shared**
5-minute `deadline`. counter-14 sorts first among the unclosed ones (counter-14 < counter-16 < counter-4 < parent-10 <
race-…), and it uses up the whole deadline. For every later workflow that is not yet `Closed` in the model, the
first loop iteration hits `time.Now().After(deadline)`, so it is reported as a straggler without a single
`readResult`. counter-16 has no resets at all, and its `finish` was acked at 97.35 and 165.82. counter-4's current
run 6b3459a7 is not in its ResetBases. Both would have been accepted on their first read.

## Relation to the fix patch

There is none. The patch only adds `AddSignalRequested(requestID)` in `ReplicateWorkflowExecutionSignaled` and in
`reapplyEvents`. The reset failed on a persistence write canceled by faults (`AppendHistoryEvents … context
canceled`). It did not fail on any validation or dedup error. The v1.4.5 and master runs show the same pattern:

- v1.4.5: counter-7, base d1e179e6, `reset … -> unknown timeout {"base_run_id":"d1e179e6…"}`
- master: counter-3, base 0c463fd3, several `unknown timeout` resets of 0c463fd3

These had simply not reached that point in those runs when the fix-verify run first failed.

## Fix (driver/model.go, resultDone)

A reset closes its base run as **terminated**. So a base run reported closed with any other status (completed,
canceled, failed, timed_out) was not reset, and its result is final. Also, with `Resets > 0` the read always targets
the *current* run (`run_id: ""`), so a reply that names a base run means no later run exists. Reject a base-run reply
only when the base was terminated:

```diff
-	if w.ResetsPending > 0 || (w.Resets > 0 && (r.RunID == "" || w.ResetBases[r.RunID])) {
-		return false // a reset's base run (or an unnamed run): not the final outcome; read again
+	if w.ResetsPending > 0 || (w.Resets > 0 && (r.RunID == "" || (w.ResetBases[r.RunID] && r.Status == "terminated"))) {
+		// A reset closes its base run as terminated: that is not the final outcome; read again.
+		// A base run that closed any other way was never reset (the reset failed or timed out
+		// without committing), so it is the current run and its result is final.
+		return false
 	}
```

Remaining edge case, not seen here: if a reset fails *and* the driver also terminated the base (TerminateSent), the
base stays rejected. To cover it, accept a terminated base when `w.TerminateSent` is true. The later
`r.Status == "terminated" && … && !w.TerminateSent` check already treats that case that way.

Recommended diagnostics (main.go eventually step 3): give each workflow at least one read after the shared deadline,
so that one stuck workflow does not mark every later one as a straggler. Also record the model state of each
straggler:

```diff
-			if time.Now().After(deadline) {
+			if time.Now().After(deadline) && tried {
 				_, o := d.cs.Call(d.names[0], "describe", map[string]any{"wid": w.WID}, 20*time.Second, nil)
-				stragglers = append(stragglers, map[string]any{"wid": w.WID, "type": w.Type, "start": state, "describe": string(o.Value), "err": o.Error})
+				d.m.mu.Lock()
+				stragglers = append(stragglers, map[string]any{"wid": w.WID, "type": w.Type, "start": state, "describe": string(o.Value), "err": o.Error,
+					"resets": w.Resets, "resets_pending": w.ResetsPending, "reset_bases": w.ResetBases, "foreign": w.Foreign,
+					"prev_run": w.PrevRunID, "first_run": w.FirstRunID, "in_flight": w.ResultInFlight})
+				d.m.mu.Unlock()
 				break
 			}
-			d.readResult(d.names[0], w, 15*time.Second)
+			d.readResult(d.names[0], w, 15*time.Second)
+			tried = true
```

(`tried := false` is declared at the top of the per-workflow `for _, w := range wfs` body.)

A model_test.go case for the main fix: `WF{Resets:1, ResetBases:{"A":true}}` with
`resultDone({"status":"completed","run_id":"A",…})` must return true, and with `"status":"terminated"` must return
false.
