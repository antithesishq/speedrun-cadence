# RCA: "an acknowledged signal is applied exactly once" (counter-18, missing s4/s16/s26)

Run 90246acc1a1bdee18be05fe17b060fb0-67-6 (Cadence v1.4.5, iter-5). FAIL at vtime 181.1026 (moment 5954438654504968097).
Details: `{"applied_n":0,"missing":["s4","s16","s26"],"resets":0,"runs":1,"sent_n":3,"wid":"counter-18"}`.

**Classification: false-positive (driver harness bug).** Cadence and the Go client behaved correctly. The driver read the
result of the *current* run of counter-18 (`run_id=""`). By then the current run was f8aafd84, a new run started by
RuleDuplicateStart (AllowDuplicate) after the signal chain had completed. The driver used `run_id=""` because
`w.ResetsPending` was stuck at 1: a `reset` RPC for counter-18 was never sent (most likely `Clients.Call` returned
`NotSent`), and RuleReset never undid its `ResetsPending++`.

Raw outputs: antithesis/scratchbook/logs/wrongrun/exec2.txt to exec6.txt.

## What Cadence actually holds (from `cadence wf listall` and `wf show` at vtime 181.14, after the failure)

| run | start | close | content |
|---|---|---|---|
| 8c8362cb | 20:15:23 SignalWithStart (client2, s4, `can_every=3`) | 20:17:08 **CONTINUED_AS_NEW** (event 14) | Signaled s4 (ev 2), s16 (ev 6), s26 (ev 10). CAN input `applied:[s4,s16,s26], sum 85, runs 1` |
| 74744ed2 | 20:17:08 (CAN of 8c8362cb, FirstExecutionRunID 8c8362cb) | 20:17:17.99 COMPLETED | `finish` from client3 (the 171.80 signal). Result `{"applied":["s4","s16","s26"],"sum":85,"runs":2}` |
| f8aafd84 | 20:17:18.61 (client3, reuse=1, `can_every=0`): the 172.50 RuleDuplicateStart | 20:17:27.10 COMPLETED | `finish` from client1 (the 180.94 signal from eventually). Result `{"applied":null,"sum":0,"runs":1}` |

The failing check saw `runs:1, applied_n:0`, which is exactly f8aafd84's result. The chain that holds the
acknowledged signals (8c8362cb, then 74744ed2) applied each of s4, s16 and s26 exactly once and completed with them.
f8aafd84 was a legitimate new run: the ID had no open run after 20:17:17.99, and AllowDuplicate permits a new one.

## Which read returned it

The driver does not log `result` RPCs (rpc.go:120 excludes them), so I replayed the read against the deployed client binary
in the run, at vtime 181.14 (exec4.txt), with the JSON-RPC sent over `/clients/client1.sock` from the driver container:

| `run_id` param | reply |
|---|---|
| `8c8362cb-…` (FirstRunID) | `completed`, `applied:[s4,s16,s26]`, `runs:2`. The Go client follows CAN to 74744ed2: correct |
| `""` | `completed`, `applied:null`, `runs:1`, `run_id: f8aafd84`: **the failing value** |
| `f8aafd84-…` | same as `""` |

So the failing read used `run_id=""`. A read pinned to 8c8362cb cannot return f8aafd84: `workflowRunImpl.Get`
(go.uber.org/cadence@v1.4.0 internal/internal_workflow_client.go:1372-1418) follows only
`WorkflowExecutionContinuedAsNew.NewExecutionRunId`, and the server's PollMutableState
(service/history/engine/engineimpl/poll_mutable_state.go:181-228) keys on the requested RunID. There is no Go client or
server bug. (A direct read of 74744ed2 sometimes hit the 3 s `timeout_ms` because postgres was slow at that moment. With
10 s it returned the right result (exec6.txt), so this is latency, not a wrong answer.)

## Why the driver sent `run_id=""`

The source in antithesis/driver changed at 20:13, after the 20:11 launch, so I disassembled the **deployed** driver
binary (pushed image `cadence-driver:latest` ca940ff5, built 20:10:52) with `go tool objdump`.
WF field offsets: Resets 0x90, ResetsPending 0x98, PrevRunID 0xf8, FirstRunID 0x108. The deployed build has no ResetBases field.
- Deployed `readResult` (main.go:390-409): the only guard is `PrevRunID != "" && FirstRunID == ""` (main.go:392). Then
  `run := w.FirstRunID; if w.Resets > 0 || w.ResetsPending > 0 { run = "" }` (main.go:403-407). It has no early
  return on `ResetsPending > 0`.
- Deployed `resultDone` (model.go:193-): its first check is the PrevRunID check (model.go:203), then `Closed` (model.go:210).
  It has no `ResetsPending > 0` bail-out either, so it went straight to `checkCounter` (model.go:244).
- Deployed `startDone` sets FirstRunID if it is empty (model.go:133-134). FirstRunID was therefore 8c8362cb from 57.67 on.
  RuleDuplicateStart's callback does not touch the model.

Evidence that ResetsPending > 0 and Resets == 0 for counter-18:
- At vtime 111.318 the driver hit `"a signal is applied at most once in a workflow that was reset"` for counter-18
  (`{"dups":null,"n":1,"unsent":null}`). That assertion is in RuleQuery's callback, under
  `wasReset := w.Resets > 0 || w.ResetsPending > 0` (deployed main.go:283 compares 0x90 and 0x98). The final details say `resets:0`.
  Resets only ever increases, so the flag came from ResetsPending.
- No `rpc … reset wid=counter-18` reply is logged at any point in the run. rpc.go logs every reset reply, whatever its outcome,
  and the only one in the log is for counter-24 at 115.39.
- The client's `reset` (client/main.go `reset`) runs under a 20 s context, and the only client restart was at 39.87,
  before counter-18 existed. A reset that was sent would therefore have returned (or come back as "lost") within about 20 s.
- No server-side reset shows up for counter-18. 8c8362cb's history has no reset-related decision failure or termination.

So the reset request never reached a client, yet its completion callback never ran:
- `RuleReset` (main.go:335-339, unchanged in the deployed build) does `w.ResetsPending++` and then ignores the Status
  returned by `d.cs.Call`.
- `Clients.Call` (rpc.go:164-176) returns `NotSent` **without calling `complete`** when it cannot dial the socket or when
  `len(c.outstanding) >= MaxInFlight` (16). The second case is plausible here: around 100-136 s, queries to counter-18 and
  other workflows were timing out at 20 s on cadence1/2/3.
- A second, less likely path: rpc.go:182-186 drops the message when `wq` is full (`default:`) and leaves the op outstanding
  forever, so `complete` never runs either.

The logs cannot tell these two apart, because neither path logs anything. Both are driver bugs with the same effect.

Timeline: around 100 s (before 111.3) RuleReset picks counter-18, sets ResetsPending=1 and the call is not sent. 171.80
RuleFinish completes 74744ed2. 172.50 RuleDuplicateStart (AllowDuplicate) starts f8aafd84, which is legitimate. 180.94
eventually sends `finish`, because `w.Closed` is false (no read had succeeded yet), and that completes f8aafd84. About
181.0 eventually calls `readResult` with `run=""`, the client pins the current run f8aafd84, and `checkCounter` reports
s4, s16 and s26 as missing.

## Fixes (harness only)

1. **driver/main.go RuleReset**: check the result of the call and roll back on NotSent:
   ```go
   st, _ := d.cs.Call(d.client(tc), "reset", ..., func(...) {...})
   if st == NotSent {
       d.m.mu.Lock(); w.ResetsPending--; d.m.mu.Unlock()
   }
   ```
   Audit the other callers that change the model *before* `Call` and rely on the callback in the same way.
2. **driver/rpc.go Call**: in the `default:` branch of the `wq` send (rpc.go:182-186), delete the op from `c.outstanding`
   and return `NotSent`, instead of keeping an op that can never complete. Otherwise make sure that `complete` always
   eventually runs (for example, a "lost" outcome after a deadline).
3. **Reading the current run after a reset is unsafe whenever the ID can be reused.** RuleDuplicateStart, and
   eventually's `finish` to a run the model does not know about, can put a foreign run at the head of the ID. Fix it as follows:
   - The current source (20:13, after this launch) already makes `readResult` return NotSent while `ResetsPending > 0`, and
     makes `resultDone` ignore results in that state. With bug 1 still present, that only turns this false FAIL into a
     straggler ("every started workflow closes once faults stop"), so fix 1 is still required.
   - After a reset, read the reset's new run (`run_id` from the reset reply) rather than `""`. Alternatively, have
     `client/main.go getResult` also return the `FirstExecutionRunID` of the run it read (from that run's
     WorkflowExecutionStarted event), and have `resultDone` discard a result whose FirstExecutionRunID is not
     `w.FirstRunID` (reset and CAN runs keep it; f8aafd84 has its own).
   - When RuleDuplicateStart unexpectedly gets "ok" after close, it should record that run ID as foreign (for example
     `w.ForeignRuns[id]=true`), and `resultDone` should skip such runs.

## False-positive review

- Cadence semantics: CAN to 74744ed2 at the 3rd signal (`can_every=3`) is the workload's design. Starting f8aafd84 with
  AllowDuplicate after 74744ed2 completed is documented behavior, the same as in duplicate-start-accepted.md.
- Go client: `GetWorkflow(wid, runID).Get` with a specific run ID returned the correct chain result when replayed in the run.
- The checker is attributing another run's result to this workflow, because of its own stale `ResetsPending` state.
