# retried-reset-applied-twice: second occurrence (outside fix-verify)

**Result: confirmed. The same mechanism shows up on unpatched v1.4.5 (run bba4d2f0, v1.4.5-b), with history evidence.
On master (c26baba5) the log signature is the same, but no histories were fetched for it.**
First occurrence: rca/fixverify-reset-dup.md (fix-verify run 41bf0672, counter-182).
Raw data: `antithesis/scratchbook/logs/reset-dup-2nd/`.

## How it was found

A `snouty runs search` fold ran over each hunt (`q2.txt`). It collected the cadence-history
`"Workflow execution terminated." … "wf-termination-reason":"antithesis-reset"` lines for each wid and reported every driver
`rpc … reset wid=X -> ok` line that came after two or more distinct terminated runs. The output is in `fold2-*.json`.
`candidates.txt` lists the cases where the base run was terminated first and a *different* run after it:
36 for f04f3907, 49 for bba4d2f0 and 42 for c26baba5. These counts come from a sample (limit 300), and they include false positives, such as a
CAN successor that was the current run at the time of the reset, and termination logs from transaction attempts that never committed. Only the two cases below
were checked by hand.

## Case 1 (confirmed with histories): v1.4.5-b run bba4d2f05a517054ee304b6265bbc541-67-6, counter-115

Moment `input_hash=-1266433295011687029 vtime=622.3960968970787`. Timeline: `tl-bba-c115.log`.
Histories were fetched with `cadence wf show` in cadence1 at that moment (debug session b876bb18, now cancelled): `exec4.txt`, `exec5.txt`.

Driver: there is **one** reset call. `21:00:14.540 rpc client3 reset wid=counter-115 -> ok {"base_run_id":"0463ef5e…","point":8,"run_id":"4dda0601…"}`

| time (UTC) | source | event |
|---|---|---|
| 21:00:14.435 | cadence1 frontend | `ResetWorkflowExecution` wid=counter-115 run=0463ef5e: `context canceled`. The first attempt's reply is lost, so the Go client retries. |
| 21:00:14.436 | cadence1 history | 0463ef5e (current run) terminated, reason antithesis-reset. This is **reset attempt A**. |
| 21:00:14.539 | cadence1 history | **44b2e857** terminated, reason antithesis-reset. This is **reset attempt B**, the client retry. |
| 21:00:14.540 | driver | reset ok, run_id=4dda0601 (the driver never sees 44b2e857) |

Histories:
- **44b2e857**, ev8: `DecisionTaskFailed Cause:RESET_WORKFLOW BaseRunID:0463ef5e… NewRunID:44b2e857… RequestID:counter-115-0463ef5e…-8`.
  Next come the reapplied s278 and s280 (ev9-10), then **ev12 `WorkflowExecutionSignaled act-s286` (client3, RequestID 455c16b2…)**, and last
  **ev13 `WorkflowExecutionTerminated Reason:antithesis-reset`**.
- **4dda0601**, ev8: `DecisionTaskFailed Cause:RESET_WORKFLOW BaseRunID:0463ef5e… NewRunID:4dda0601…` with the **same
  `RequestID:counter-115-0463ef5e…-8`**. Next come s278 and s280 (ev9-10) and DecisionTaskScheduled (ev11). There is **no act-s286**.

So one `ResetWorkflowExecution` request (one request ID, base 0463ef5e, point 8) produced two reset runs. The second one
terminated the first. This is the mechanism from counter-182: `reset_workflow_execution.go:124-133` compares the current run's
CreateRequestID with the request ID, and that never matches, because Rebuild takes CreateRequestID from the copied start event
(`mutable_state_builder_methods_started.go:192-198`). The reapply step follows only CAN links from the base, so
the signal act-s286 that the discarded run 44b2e857 had accepted is not carried into 4dda0601. The case was checked only up to this moment.
It was not checked whether the activity later re-sent act-s286, or whether the driver flagged it. The failing details for counter-115 in this run
(`missing s492` at vtime 521, `duplicates s1112` at vtime 836) are on other timelines and cannot be attributed to this event.

## Case 2 (log evidence only): master-b run c26baba5062af99fb01cbb7300de3bc6-67-6, counter-50

Moment `input_hash=-3299546417317744105 vtime=387.69001244124956`. Timeline: `tl-c26-c50.log`.

| time (UTC) | source | event |
|---|---|---|
| 20:56:18.760 | cadence3 frontend | `ResetWorkflowExecution` wid=counter-50 run=365e6375: `grpc: the client connection is closing` (the client retries) |
| 20:56:19.573 | cadence2 history | 365e6375 (base and current run) terminated, antithesis-reset |
| 20:56:19.728 | cadence2 history | **63919e12** terminated, antithesis-reset. The driver never knew about this run. |
| 20:56:19.827 | driver | the only reset call: `rpc client3 reset wid=counter-50 -> ok {"base_run_id":"365e6375…","point":28,"run_id":"98a9d8c4…"}` |

This is the same pattern as case 1, and master has the same code (rca/fixverify-reset-dup.md). The histories of 63919e12 and 98a9d8c4 were not fetched,
so it is not proven at the history level that both share BaseRunID 365e6375, point 28 and the request ID.

## Classification

Real. This is the same finding as retried-reset-applied-twice. It occurs on unpatched v1.4.5, not only on the fix-verify build, and probably also
on master. The trigger is common under faults: the frontend's reply to a reset is lost or cancelled, and the Go client retries with the same
request ID. The log-signature search shows dozens of candidates in each hunt. Fix: `fixes/retried-signal-duplicated-after-reset.v2.patch`
(CreateRequestID = reset request ID after Rebuild).
