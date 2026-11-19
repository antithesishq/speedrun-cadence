# RCA: residual failures in fix-verify run 1a0d055cc5831d95536f58a8dbed5d68-67-6

Run: Cadence v1.4.5 plus three patches (worktree `src/cadence-fixverify-signal`):
`retried-signal-duplicated-after-reset.patch` (v1, commit ffbc65c2f), `acked-signal-lost-on-reset.patch`
(a7da2f80c), and `sigterm-during-startup-panics.patch` (uncommitted nil guards in
`service/history/handler/handler.go` and `service/frontend/service.go`). Same hunt driver as the
v1.4.5 baselines f04f3907…-67-6 and bba4d2f0…-67-6.

Raw data is in `antithesis/scratchbook/logs/fixverify-residuals/`:
- `reset-dup.json`, `reset-lost.json`, `crash.json`: every failing example (search, limit 999).
- `*.moments`: the first moment of each distinct incident.
- `tl/*.ndjson`: timeline logs up to the first failing moment; `tl/*.drv`: the driver rpc lines for the workflow.
- `h-*.txt`: `wf list` plus the JSON history of every run, collected with `snouty runs exec` in cadence1
  at the failing moment.
- `*.diff`: the proposed additional changes, described below.

Histories were collected through the ready debugging session b7267a78…-67-6, using `--source-run-id`.
It belongs to another agent and I did not cancel it. My own `snouty debug` launches failed on the API
side ("upstream request failed", "rejected the API key"), so none of them was created and none is
left running.

Counting: a failing "example" is one failing assertion evaluation. One incident (one workflow, one
signal) usually repeats on every later branch of its timeline. In the tables below, "examples" is the
example count of that incident.

## Summary

| Property | fix-verify (baselines) | Mechanisms found | Patch verdict |
|---|---|---|---|
| a signal is applied at most once in a workflow that was reset | 168 (196 / 280) | **Gap B** (dedup set not carried across continue-as-new): 94 examples, 5 incidents. **New gap in v1**: reapplied signal's dedup ID lost on a second reset: 37 examples, 1 incident | v1 **partially verified** |
| an acknowledged signal survives a reset of its workflow | 373 (666 / 787) | **Gap A** (retried reset re-executed): 362 examples, 6 incidents. 5 of them are fixed by v2; 1 (counter-1) happens after the reset run continued as new, which v2 does not cover | acked-lost patch **verified**: none of its mechanism in the samples. Its residual is Gap A |
| a cadence server process crashes | 119 (1.4k / 1.4k) | 1 incident (cadence3, vtime 404.70): `panic: cannot close connection` at `tools/sql/conn.go:121`, during startup schema verification, `stopping:0`. **0** nil-pointer panics | sigterm patch **verified** |

No sampled residual shows the original mechanism of any of the three patches still at work: a
single reset reapply followed by a retry, a buffered signal flushed by termination, or a nil
dereference in Stop(). What remains comes from known, unpatched gaps (A and B), from one real hole in
the v1 signal-dedup patch, and from a different, pre-existing panic.

---

## 1. "a signal is applied at most once in a workflow that was reset" (168 examples)

Incidents (all 168 examples): counter-26 s103 (40), counter-150 s665/s667/s668 (37, history check
`model.go:419`), counter-8 s79 (22), counter-172 s981 (15), counter-24 s40 (12), counter-58 act-s93 (12),
counter-37 s65 (8), counter-47 s91 (7), counter-36 act-s142 (3), counter-32 s65 (2), and 10 singletons.
I sampled 6 incidents, covering 131 examples:

| incident | examples | mechanism | evidence (history) |
|---|---|---|---|
| counter-26 s103 | 40 | Gap B | s103 req `938d1aef` applied in 3f7e4a3f ev8 (20:55:53.806). That run continued as new at 57.300, and its CAN input `applied` contains s103. The client retry, with the same req, lands on successor 170dd777 as ev5 (20:56:00.342). The resets that follow copy both copies. The driver logs one `signal … sig=s103 -> ok` at 234.208. |
| counter-8 s79 | 22 | Gap B | s79 req `007f55a7` applied in ff6ef5c0 ev10 (20:55:58.814). CAN to 740f7b4a at 56:00.365, with s79 in its input. The retry lands on 740f7b4a as ev5 (56:00.713) with the same req. |
| counter-24 s40 | 12 | Gap B, reached through a reset | s40 req `2d021c68` applied in ac0508bd ev10. Reset to 1337a561 reapplies it at ev7 (v1 adds the ID to 1337a561's dedup set). 1337a561 continues as new to 2f7d55e7, with s40 in its input. The retry lands on 2f7d55e7 as ev9 (20:54:24.344) with the same req. The reset hop was deduplicated correctly; the CAN hop was not. |
| counter-58 act-s93 | 12 | Gap B | req `63a323c0` in 41ee9f35 ev19 (58:31.506). CAN at 31.512. The retry lands on ab5f4277 as ev5 (31.825). |
| counter-37 s65 | 8 | Gap B | req `35efd239` in b3d34012 ev19 (56:24.379). CAN at 24.393. The retry lands on 3e4da50d as ev5 (25.034). |
| counter-150 s665, s667, s668 | 37 | **New: v1 dedup ID lost on a second reset** | See below. |

The unsampled incidents (37 examples, mostly singletons) are not classified. The five Gap B incidents
need no reset at all: Gap B is the known limitation from `rca/fixverify-reset-dup.md`. They are
reported under this property only because the driver had also reset the workflow (`model.go:317`).
No sampled duplicate was caused by Gap A.

### New gap in the v1 patch: a reapplied signal loses its dedup ID on the next reset (counter-150)

Histories: `h-d-c150.txt`. Driver: `tl/d-c150.ndjson`.

| time (UTC 21:17) | run / event | what happened |
|---|---|---|
| 30.917–30.950 | 19659714 ev43/44/48 | s667, s665 and s668 applied with req `072a26fe`, `a9a39855` and `460aa64b`. The replies are lost and the client keeps retrying (the driver logs ok only at vtime 1529.03, 1529.05 and 1533.65). |
| 30.972 | reset #1 → cd09831f | Base 19659714, point 22. Reapplies the three signals as ev33/34/36 **with no RequestID**. v1 calls `AddSignalRequested(req)`, so cd09831f's persisted dedup set holds the three IDs. |
| 35.042 | reset #2 → 98d9126c | Base cd09831f, **point 39, which comes after ev33–36**. The three events are now part of the copied prefix (98d9126c ev33/34/36, no RequestID). The rebuild replays them through `ReplicateWorkflowExecutionSignaled`. v1 derives the dedup set from the event's RequestID, but these events have none, so 98d9126c's set does not contain the three IDs. They are not reapplied either, because they come before the point. |
| 35.177–35.965 | 98d9126c ev48/49/50 | The three client retries arrive with the original req IDs, are not deduplicated, and are applied again. The history check reports "recorded twice (events 33 and 48)" and so on. |

Upstream deliberately leaves the reapplied event's RequestID empty (`resetter.go:677`). With strong
idempotency enabled, a RequestID on the event would register a duplicate WorkflowRequest. The v1
patch therefore puts the ID only in the mutable state's dedup set. That state is not derivable from
history, and a rebuild from history is exactly what the next reset does.

**Additional change (exact diff, on top of the worktree; it also composes with v2: the v2 resetter
hunk applies with an offset):** carry the base run's persisted dedup set into the reset run.
`logs/fixverify-residuals/reset-copies-base-dedup-set.diff`:

```diff
--- a/service/history/reset/resetter.go
+++ b/service/history/reset/resetter.go
@@ -223,6 +223,14 @@
 
 	resetMutableState := resetWorkflow.GetMutableState()
 
+	// The rebuild derives the dedup set from the copied history, but signals that an earlier
+	// reset reapplied into the base run carry no request ID in history: their IDs exist only
+	// in the base run's dedup set. Carry that set over, or a client retry of such a signal
+	// is applied again once a second reset copies it.
+	if err := r.copySignalRequestedIDs(ctx, domainID, workflowID, baseRunID, resetMutableState); err != nil {
+		return nil, err
+	}
+
 	baseLastEventVersion := resetMutableState.GetCurrentVersion()
 	if baseLastEventVersion > resetWorkflowVersion {
 		return nil, &types.InternalServiceError{
@@ -342,6 +350,32 @@
 	)
 }
 
+// copySignalRequestedIDs adds the base run's persisted signal dedup set to the reset run.
+func (r *workflowResetterImpl) copySignalRequestedIDs(
+	ctx context.Context,
+	domainID string,
+	workflowID string,
+	baseRunID string,
+	resetMutableState execution.MutableState,
+) error {
+	domainName, err := r.domainCache.GetDomainName(domainID)
+	if err != nil {
+		return err
+	}
+	resp, err := r.shard.GetExecutionManager().GetWorkflowExecution(ctx, &persistence.GetWorkflowExecutionRequest{
+		DomainID:   domainID,
+		Execution:  types.WorkflowExecution{WorkflowID: workflowID, RunID: baseRunID},
+		DomainName: domainName,
+	})
+	if err != nil {
+		return err
+	}
+	for requestID := range resp.State.SignalRequestedIDs {
+		resetMutableState.AddSignalRequested(requestID)
+	}
+	return nil
+}
+
 func (r *workflowResetterImpl) replayResetWorkflow(
 	ctx context.Context,
 	domainID string,
```

The diff file also adds `TestCopySignalRequestedIDs` to `resetter_test.go`. Notes:
- The base's set is a superset: it also holds the IDs of base signals after the reset point, which
  are reapplied (and added) anyway. With `SkipSignalReapply`, a retry of a dropped signal is then
  deduplicated rather than applied. That is acceptable, because the signal was already applied once,
  in the base run.
- The read uses persistence directly because the engine already holds the base run's lock.
- Checked with `go build`/`go test -overlay …` on `./service/history/reset/ ./service/history/execution/
  ./service/history/engine/engineimpl/`: all ok. I did not run it end to end.

### Gap B (known, unpatched, the dominant duplicate cause): optional diff

`logs/fixverify-residuals/signal-dedup-across-can.diff`. It copies the predecessor's dedup set into
the continue-as-new successor:

```diff
--- a/service/history/execution/mutable_state_builder.go
+++ b/service/history/execution/mutable_state_builder.go
@@ -1301,6 +1301,12 @@
 	); err != nil {
 		return nil, nil, &types.InternalServiceError{Message: "Failed to add workflow execution started event."}
 	}
+	// Carry the signal dedup set into the new run: a client retry of a signal this run
+	// already applied (and passed on in the continue-as-new input) is routed to the new
+	// current run and must still be deduplicated there.
+	for requestID := range e.pendingSignalRequestedIDs {
+		newStateBuilder.AddSignalRequested(requestID)
+	}
 
 	if err = e.ReplicateWorkflowExecutionContinuedAsNewEvent(
 		firstEventID,
```

Caveats:
- The set grows across a CAN chain without bound. Cap it in practice, for example to the IDs added
  within the client retry horizon.
- The carried IDs are not in the successor's history. A rebuild from history (a reset of the
  successor) recovers them only together with the `copySignalRequestedIDs` change above.
- The standby-cluster CAN path (replication) needs the same treatment.

Existing tests pass with it; I did not add a dedicated test. This is a design change to Cadence's
semantics, not a fix to one of the three patches. Alternatively, report CAN-crossing duplicates under
their own property.

## 2. "an acknowledged signal survives a reset of its workflow" (373 examples)

Incidents: counter-22 s67 (322), counter-37 s172 (12), counter-59 s611 (11), counter-3 s20 (8),
counter-88 s242 (8), counter-75 s361 (2), and 10 singletons. I sampled 6 incidents, covering 362/373
examples. Every one of them is **Gap A**: the history shows two `DecisionTaskFailed cause=RESET_WORKFLOW`
events with the **same reset request ID** in two different reset runs. Each time, the acknowledged
signal sits in the first reset run (or in a CAN successor of it). The second execution terminates
that run and rebuilds from the same base, and the CAN-only chain walk
(`reapplyResetAndContinueAsNewWorkflowEvents`) never reaches the discarded run.

| incident | examples | reset request executed twice | lost signal | v2 dedups the retry? |
|---|---|---|---|---|
| counter-22 s67 | 322 | `counter-22-522382f5-…-10`: bb9caa59 (20:54:40.055), then 9dd37989 (45.491) | s67 req `24f7e73e` in bb9caa59 ev14 (41.318). Driver: `signal sig=s67 -> ok` at 156.19, `reset -> unknown timeout` at 160.95 | yes: current run = bb9caa59 = reset run |
| counter-3 s20 | 8 | `counter-3-be8f9e57-…-5`: e43538c8 (20:53:37.196), then f8db2595 (49.024) | s20 in e43538c8 ev8 (48.513). Driver: s20 ok at 102.883, reset ok at 102.886 (the reply to the retry) | yes |
| counter-37 s172 | 12 | `counter-37-fd5ce795-…-8`: 076d2a94 (20:58:02.977), then 240cfc9c (03.918) | s172 in 076d2a94 ev10 (02.979) | yes |
| counter-88 s242 | 8 | `counter-88-6dc09e29-…-8`: fa0d8dca (21:02:15.946), then d47d8cf9 (18.424) | s242 in fa0d8dca ev10 (16.067) | yes |
| counter-59 s611 | 11 | `counter-59-b0c03787-…-15`: 3f1daab4 (21:09:26.464), then 11dc8b49 (37.683) | s611 in 3f1daab4 ev19 (33.384) | yes |
| counter-1 s5 | 1 | `counter-1-d4a4507b-…-5`: 488d30d8 (20:52:28.546), then 94f01cb1 (36.515) | s5 req `d399838d` in 488d30d8 ev10. **488d30d8 continued as new to 65dc0dd9 (34.160) before the retry arrived**, and the retry terminated 65dc0dd9 | **no**: the current run is 65dc0dd9, whose CreateRequestID is its CAN request, not the reset ID |

The server log signature matches: "Workflow execution terminated." for every run the duplicate reset
killed (for example counter-22 bb9caa59 at 158.95 and 159.30, which is the commit retried after a
persistence timeout).

The acked-signal-lost patch's own mechanism (a buffered signal flushed into the terminated base at
event ID ≥ the captured next event ID) did not appear in any sampled history. In every sample the lost
signal is a normal persisted event of a run that a re-executed reset discarded. The residual is Gap A,
which is fixed in the v2 signal patch (5 of the 6 incidents, 361 examples), except for the counter-1
shape.

**Additional change needed for v2 (exact diff, applies on top of v2 or the worktree):**
`logs/fixverify-residuals/reset-dedup-across-can.diff` (code part shown; the file also updates
`reset_workflow_execution_test.go`):

```diff
--- a/service/history/engine/engineimpl/reset_workflow_execution.go
+++ b/service/history/engine/engineimpl/reset_workflow_execution.go
@@ -39,6 +39,10 @@
 
 const (
 	DefaultPageSize = 100
+
+	// maxResetDedupLookback bounds how many continue-as-new predecessors of the current run
+	// are checked when deduplicating a retried reset request.
+	maxResetDedupLookback = 10
 )
 
 func (e *historyEngineImpl) ResetWorkflowExecution(
@@ -122,7 +126,15 @@
 	}
 
 	// dedup by requestID
-	if currentMutableState.GetExecutionInfo().CreateRequestID == request.GetRequestID() {
+	duplicate := currentMutableState.GetExecutionInfo().CreateRequestID == request.GetRequestID()
+	if !duplicate && currentRunID != baseRunID {
+		// the run this reset created may already have continued as new
+		duplicate, err = e.isResetRunPredecessor(ctx, domainID, workflowID, baseRunID, currentMutableState, request.GetRequestID())
+		if err != nil {
+			return nil, err
+		}
+	}
+	if duplicate {
 		e.logger.Info("Duplicated reset request",
 			tag.WorkflowID(workflowID),
 			tag.WorkflowRunID(currentRunID),
@@ -294,3 +306,51 @@
 
 	return fmt.Errorf("reset event ID %v not found in the history events", resetEventID)
 }
+
+// isResetRunPredecessor reports whether a continue-as-new predecessor of the current run was
+// created by the reset with this request ID (its CreateRequestID is the reset request ID), i.e.
+// whether the reset was already applied and the run it created has since continued as new.
+// The walk stops at the base run: a run created by this reset starts after the base run.
+func (e *historyEngineImpl) isResetRunPredecessor(
+	ctx context.Context,
+	domainID string,
+	workflowID string,
+	baseRunID string,
+	currentMutableState execution.MutableState,
+	requestID string,
+) (bool, error) {
+	if requestID == "" {
+		return false, nil
+	}
+	mutableState := currentMutableState
+	for i := 0; i < maxResetDedupLookback; i++ {
+		startEvent, err := mutableState.GetStartEvent(ctx)
+		if err != nil {
+			return false, err
+		}
+		previousRunID := startEvent.GetWorkflowExecutionStartedEventAttributes().GetContinuedExecutionRunID()
+		if previousRunID == "" || previousRunID == baseRunID {
+			return false, nil
+		}
+		// A continue-as-new predecessor is closed and no longer changes, so it is read from
+		// the database without taking its lock (the base and current runs are already locked).
+		previousContext := execution.NewContext(
+			domainID,
+			types.WorkflowExecution{WorkflowID: workflowID, RunID: previousRunID},
+			e.shard,
+			e.executionManager,
+			e.logger,
+		)
+		mutableState, err = previousContext.LoadWorkflowExecution(ctx)
+		if err != nil {
+			if _, ok := err.(*types.EntityNotExistsError); ok {
+				return false, nil
+			}
+			return false, err
+		}
+		if mutableState.GetExecutionInfo().CreateRequestID == requestID {
+			return true, nil
+		}
+	}
+	return false, nil
+}
```

How it works:
- It relies on v2: the reset run's CreateRequestID equals the reset request ID.
- The new walk runs only when the base is not the current run, so the common base == current path
  costs nothing extra.
- For counter-1, the walk goes 65dc0dd9 (start event `continuedExecutionRunId` = 488d30d8), then loads
  488d30d8, whose CreateRequestID is the reset request ID, and treats the retry as a duplicate.

Tests:
- New case "Duplicate Request after the reset run continued as new". It fails without the change
  (verified) and passes with it.
- "Success using previous version" gets a `withStartEvent("")` stub.
- `go test -overlay … ./service/history/engine/engineimpl/ ./service/history/execution/ ./service/history/reset/`
  passes. I did not run it end to end.

Remaining limits:
- The walk is bounded at 10 hops.
- A retry that arrives after the reset run was itself **reset** (not continued as new) is still not
  deduplicated. A complete fix needs reset request IDs recorded as a set that is carried forward, or
  strong idempotency.

## 3. "a cadence server process crashes" (119 examples)

All 119 examples are **one incident**: cadence3 at vtime 404.6955, input_hash 6152778289480475374
(first failing moment 404.898). Its details are `{"exit":2,"line":"panic: cannot close connection","node":"cadence3","stopping":0}`.
Stack, from `crash-slice.ndjson`:

```
panic: cannot close connection
goroutine 72 [running]:
github.com/uber/cadence/tools/sql.(*Connection).Close(...)            tools/sql/conn.go:121
github.com/uber/cadence/tools/sql.doCheckCompatibleVersion(...)       tools/sql/handler.go:125   (defer connection.Close())
github.com/uber/cadence/tools/sql.CheckCompatibleVersion(...)         tools/sql/handler.go:88
github.com/uber/cadence/tools/sql.VerifyCompatibleVersion(...)        tools/sql/handler.go:57
github.com/uber/cadence/cmd/server/cadence.(*App).verifySchema(...)   cmd/server/cadence/fx.go:163
go.uber.org/fx/internal/lifecycle.(*Lifecycle).runStartHook(...)      fx@v1.23.0 lifecycle.go:256
```

What it shows:
- This is not a shutdown path, and none of the nil-guard sites is involved. It happens at **startup**,
  in the fx OnStart hook `verifySchema`, with no SIGTERM pending (`stopping:0`).
- cadence3 had just been restarted. The schema check opens a SQL admin connection, and closing it
  returned an error under the active partition and latency faults (cadence3 and postgres were in a
  Slowed partition from 400.48).
- `Connection.Close` turns any close error into `panic("cannot close connection")` (`tools/sql/conn.go:117-123`,
  identical in v1.4.5).
- It is pre-existing: baseline f04f3907 shows the same `cannot close connection` panic 8 times, on all
  three nodes, alongside 990 nil-pointer panics, in a 999-example sample of its 1.4k.

**Fix-verify of the sigterm patch:** the baselines had 1.4k crash examples each. In 999-example samples
they are 990/999 (f04f) and 999/999 (bba4) `panic: runtime error: invalid memory address or nil pointer dereference` with `stopping:1` (the
`handler.go:234` / `collection.go:255` family). The fix-verify run has **none**: all 119 examples are
the startup Close panic. Verdict: **verified**. The guards in `handler.go` and `frontend/service.go`
cover every nil site the run reached. No new Stop() site was missed.

This is a separate low-severity finding, "SQL schema check panics when closing its connection fails".
Suggested fix, not part of any of the three patches: in `tools/sql/conn.go`, `Close()` should ignore
or log the close error rather than panic, because a failed close of a one-shot admin connection is
harmless:

```diff
--- a/tools/sql/conn.go
+++ b/tools/sql/conn.go
@@ -116,9 +116,8 @@
 // Close closes the sql client
 func (c *Connection) Close() {
 	if c.adminDb != nil {
-		err := c.adminDb.Close()
-		if err != nil {
-			panic("cannot close connection")
-		}
+		// A failed close (e.g. the server already dropped the connection) leaves nothing to
+		// clean up; crashing the server during startup over it is worse than ignoring it.
+		_ = c.adminDb.Close()
 	}
 }
```

## Patch verdicts

| Patch | Verdict | What is missing |
|---|---|---|
| `retried-signal-duplicated-after-reset.patch` (v1) | **Partially verified** | (1) Its dedup ID is lost when a later reset copies the reapplied signal, because the point is after it (counter-150, 37 examples): add `reset-copies-base-dedup-set.diff`. (2) Gap A, a re-executed reset, is fixed only in v2: switch to v2 and add `reset-dedup-across-can.diff` for the case where the reset run has already continued as new (counter-1). (3) Gap B, the dedup set not crossing continue-as-new, is out of scope and is the main residual cause of duplicates (94 examples sampled): optional `signal-dedup-across-can.diff`, or report it under its own property. |
| `acked-signal-lost-on-reset.patch` | **Verified**, for its own mechanism | No sampled loss (362/373 examples) is a buffered-and-flushed signal. All of them are Gap A, which belongs to the signal patch (v2 plus the CAN lookback above). Absence of its mechanism in a sample is weaker evidence than a positive hit. A targeted check would be one run with v2 + `reset-dedup-across-can.diff`, where this property should drop to about zero. |
| `sigterm-during-startup-panics.patch` | **Verified** | 0 nil-pointer panics, down from about 1.4k per baseline. The residual is a different, pre-existing startup panic at `tools/sql/conn.go:121` (fix above). |

Suggested next fix-verify: v2 + `reset-dedup-across-can.diff` + `reset-copies-base-dedup-set.diff`
+ acked-lost + sigterm (+ the `conn.go` change). Optionally also `signal-dedup-across-can.diff`, or
the driver change that reports CAN-crossing duplicates separately.

## Coverage limits

I did not fetch histories for the unsampled singleton incidents: counter-39/6/58/81 for the lost
property, and counter-45/36/32/70 for the duplicate property. The borrowed debugging session ended
while those execs were waiting, and the API refused new `snouty debug` sessions. They account for 11
of 373 lost examples and 37 of 168 duplicate examples, and they stay unclassified.
