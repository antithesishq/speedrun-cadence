# RCA: "an acknowledged signal is applied exactly once" — counter-4 missing s5 (run 815b813d55dad0911d777ecef231977c-67-6)

**Classification: false-positive** (checker bug). The driver checked the result of the wrong run. Cadence put s5 in the run created by the reset, and that run applied it. The driver read the result of the base run, which had completed earlier.

## Failure

- Moment: input_hash `-7174138143895305175`, vtime `18.6024` (`driver model.go:checkCounter:323`)
- Details: `{"applied_n":3,"missing":["s5"],"resets":1,"runs":1,"sent_n":4,"wid":"counter-4"}` (`runs` is the workflow's own `st.Runs` counter, not a count of Cadence runs)
- Log: `antithesis/scratchbook/logs/iter4m/ack.log`
- Histories, collected with `snouty runs exec` on cadence1 from 18.61 to 19.07: `antithesis/scratchbook/logs/signal-lost/exec1.txt` (both runs at 18.61, plus `wf desc`) and `exec2.txt` (new run at 18.92, plus `wf list`)

## Evidence: both runs of counter-4

Times are Cadence event timestamps (UTC 20:00:xx). The driver's wall clock is within about 0.1 s of them.

**Base run `8745220b`** (18 events, CLOSED **COMPLETED**, not terminated):

| ev | time | event |
|---|---|---|
| 3 | 25.465 | Signaled act-s1 (req f49bd08c) |
| 4 | 25.552 | Signaled s2 |
| 6 | 25.787 | DecisionTaskCompleted (this is the later reset point) |
| 8 | 25.663 | Signaled s3 (buffered, flushed after ev 6) |
| 15 | 26.115 | Signaled **finish** (req 4f50636a) |
| 16/17 | 26.212 / 26.325 | DecisionTaskStarted / DecisionTaskCompleted |
| 18 | **26.329** | **WorkflowExecutionCompleted** `{"applied":["act-s1","s2","s3"],"sum":159}` |

**Reset run `d840f767`** (22 events, CLOSED COMPLETED):

| ev | time | event |
|---|---|---|
| 1–5 | copied | from base up to the reset point (act-s1, s2) |
| 6 | **26.566** | DecisionTaskFailed RESET_WORKFLOW, base 8745220b, request `counter-4-8745220b-…-6` |
| 7 | 26.604 | Signaled s3 (reapplied, no RequestID) |
| 8 | 26.610 | Signaled finish (reapplied) |
| 10/11 | 26.672 / 26.791 | first decision (applies act-s1 and schedules its activity) |
| **13** | **26.701** | **Signaled s5**, identity client1, req `be8f3013-f7c0-431a-9353-847f80387578` (buffered during decision 10–11) |
| 14–16 | 26.799–26.864 | decision |
| 18 | 26.876 | ActivityTaskCompleted |
| 20/21 | 26.897 / 26.956 | final decision |
| 22 | **26.958** | **WorkflowExecutionCompleted `{"applied":["act-s1","s2","s3","s5"],"sum":206}`** |

`wf list -w counter-4` shows both runs as COMPLETED. **s5 landed in the reset run (ev 13), and that run applied it.** The signal was not lost and no unhandled signal was dropped.

## Sequence

| driver vtime / wall | what happened |
|---|---|
| 17.22 / 25.363 | start counter-4 → run 8745220b |
| 17.41–17.55 | act-s1, s2, s3 acknowledged |
| < 18.04 | `RuleReset` picks counter-4 (its filter needs `!FinishSent`) and sets `ResetsPending=1`. The `reset` RPC begins: Describe, read history, pick point 6. |
| 18.04 / 26.177 | `RuleFinish` sets `FinishSent`, and finish is acknowledged (ev 15 at 26.115) |
| ≥ 18.04 | `RuleResult` becomes eligible (FinishSent). `readResult` sends `run_id=""` because `ResetsPending>0` (`driver/main.go:404-406`). |
| 26.329 (Cadence) | base run handles finish and **completes** with [act-s1,s2,s3] |
| 26.566 (Cadence) | the reset reaches history. The current run is already closed, so `resetter.go:138` skips terminating it and the reset forks from point 6 into the new run d840f767. s3 and finish are reapplied. |
| 18.515 / 26.654 | reset reply ok, `Resets=1` |
| 18.572 / 26.710 | s5 acknowledged. Cadence recorded it at 26.701 as ev 13 of d840f767, the current open run. |
| **18.600 / ~26.739** | the result read returns **completed** with the base run's payload [act-s1,s2,s3]. `checkCounter` reports s5 missing. |
| 26.958 (Cadence) | d840f767 completes with [act-s1,s2,s3,s5] |

The read could only have produced that payload before 26.958 by resolving to the base run. With `run_id=""`, the Go client's `GetWorkflow(...).Get` long-polls the run that is current when the poll starts. Here that was 8745220b, either still open or completed at 26.329, but in either case before the reset committed at 26.566. `getResult` then sets `run_id` from `DescribeWorkflowExecution(wid, "")` only if the current run is closed (`client/main.go:440-445`). At that point the current run was d840f767, still open, so the reply carried `run_id: ""`. The driver had nothing to tell it that the read came from the base run.

## Why the checker accepted it

`driver/model.go:resultDone` (around lines 192-245) skips a read that might come from a reset's base run only when its status is **terminated** (`model.go:220`). That covers the usual case, where the reset terminates a running base. Here the finish signal (sent concurrently, after the reset rule had already been chosen) let the base run **complete** before the reset was applied. Cadence resets closed workflows too, and it creates a new run that reapplies the signals after the reset point, including finish. A "completed" read of the base run therefore looks final to the checker. The checker marks the workflow Closed with `ClosedRunID=""` and validates it against every signal acknowledged for the workflow ID, including s5. s5 was delivered to the new run, as Cadence guarantees, and the new run's result includes it.

## Candidates

- (a) s5 accepted by the base run, then lost by the reset: **ruled out**. The base history has no s5. s5 is ev 13 of d840f767, written 135 ms after the reset committed.
- (b) the new run completed with an unhandled signal: **ruled out**. s5 was buffered during decision 10–11, flushed as ev 13, and applied. The run's final result contains s5.
- (c) harness/checker artifact: **confirmed**. The driver attributed the base run's result to the workflow, as described above.

## Cadence code (no defect found)

- `service/history/reset/resetter.go:137-147` (master; same in v1.4.5): the current run is terminated only if it is running. A closed run is left closed and the reset still forks. This is documented behaviour (reset is allowed on closed workflows).
- `service/history/engine/engineimpl/reset_workflow_execution.go:97-101`: the reset resolves the current run and then forks from the base run. Signals sent after the reset reply go to the new current run (`engineimpl/signal_workflow_execution.go`), which is what happened.

## Proposed fix (harness)

1. **Pin the run in `getResult`** (`client/main.go:437`). When `p.RunID == ""`, first call `DescribeWorkflowExecution(wid, "")`, read its run ID, then call `GetWorkflow(wid, thatRunID)`, and always return that run ID. Every result then names the run it came from. (Continue-as-new: use `run.GetRunID()` after `Get`, or follow the chain the way the Go client does.)
2. **Reject results from a reset's base run.** In the reset reply callback (`driver/main.go:340-348`), record `base_run_id` in a new `w.ResetBases` set and the new run ID in `w.RunIDs`/`w.LatestRunID`. In `resultDone` (`model.go:220`), skip any read whose `r.RunID` is in `ResetBases` or empty while `Resets>0 || ResetsPending>0`, regardless of status, and read again later. This replaces the terminated-only check.
3. Cheap extra guard: in `readResult` (`main.go:383`), return `Pending` while `w.ResetsPending > 0`. That way no read is issued while a reset is in flight.

After the fix, this timeline reads d840f767's result [act-s1,s2,s3,s5] and the property passes.
