# Hunt triage: "an acknowledged signal is applied exactly once" / "a query shows each delivered signal once" (non-reset variants)

Runs: v1.4.5 `f04f3907…-67-6` (hunt), `bba4d2f0…-67-6` (hunt -b); master `3e9720ca…-67-6`, `c26baba5…-67-6` (-b);
fix-verify `1a0d055c…-67-6` (v1.4.5 + the three v1 fix patches).
Raw data: `antithesis/scratchbook/logs/hunt-signal-once/` (`all.jsonl` = every failing example of both properties in
all five runs, 2023 rows, with moment and details; downloaded timelines `*_log_*.ndjson`, `m_*.ndjson`, `u_*.ndjson`;
`c4_hist.txt` = cadence CLI histories of counter-4 from debugging session `18b4714b1a412b3ed222029cc60ac29a-67-6`).

Both properties fire only for workflows the driver believes were never reset (resets=0, resets_pending=0); the reset
variants go to "a signal is applied at most once in a workflow that was reset" / "an acknowledged signal survives a reset".

## Signatures (failing examples per run)

| # | signature (detail shape) | v1.4.5 f04f | v1.4.5-b bba4 | master 3e97 | master-b c26b | fix-verify 1a0d | classification |
|---|---|---|---|---|---|---|---|
| S1 | query `unsent:[id]` (186/228/224/103/149) + result `late_ack:true` (27/28/57/5/49): a signal sent to a workflow ID before it closed is acknowledged after the driver **reused the ID**, and lands on the new run | 213 | 256 | 281 | 108 | 198 | **driver false positive** |
| S2 | result `duplicates:[id]` with `runs>=2` (41/79/150/176/22) + query `dups:[id]` (0/0/42/77/0): one signal appears twice in a counter that **continued-as-new** (never reset) | 41 | 79 | 192 | 253 | 22 | **real Cadence bug (c), not a consequence of the three fixed findings**: signal RequestID dedup does not cross continue-as-new (described as "Gap B" in rca/fixverify-reset-dup.md and as a LEAD in the speedrun log, never filed as a finding) |
| S3 | result `missing:[ids]` with resets=0: acknowledged signals absent from the completed result, after a **duplicate start** succeeded (started a new run on the ID after the original closed) | 109 | 109 | 47 | 103 | 12 | **driver false positive** |

All three signatures are present on the fix-verify run, at similar rates, so none of them is explained by the three
fixed findings (as expected: none involves a reset).

## S1: late signal lands on the reused ID's new run (driver false positive)

Mechanism. The driver's `RuleSignal` sends `SignalWorkflowExecution(wid, runID="")`. Under faults the Go client retries
the call (same RequestID) for many seconds. Meanwhile the run closes (finish / cancel / terminate), the driver reads the
result, marks the model closed, and `RuleReuseClosed` starts a new run on the same ID with a **new** WF model
(`main.go:687-715`; it requires no in-flight signals). The pending retry now targets the current run, i.e. the new run,
and Cadence applies it there (correct: an empty RunID means "current run", `signal_workflow_execution.go:54`). The old
model then gets a late ack whose signal is not in its closed result (`model.go:162-167`, `late_ack`), and the new model's
query sees an ID it never sent (`main.go:281-295`, `unsent`). The signal is applied exactly once — just to the run that
was current when the server processed it. Had any earlier attempt reached the first run, the retry would have been a
no-op there (dedup runs before the "already completed" check, `signal_workflow_execution.go:66-75`) and the old result
would contain it; it does not.

Evidence:
- v1.4.5 f04f, counter-28 (input_hash 5732701007942740251, vtime 358.87, query `unsent:["act-s84"]`; late_ack at
  344.22): run 83ab76de; `finish` acked 20:54:01; act-s84 sent ~20:54:15–20:54:43 (between s81 and act-s94, which got
  `not_exists`); `start wid=counter-28 reuse=1 -> ok 7d16d70f` at 20:55:26 (vtime 333.97); `signal sig=act-s84 -> ok` at
  20:55:36 (344.22), 10 s after the reuse.
- master 3e97, counter-1 (6014420666600315459, 31.97, late_ack s8): run 387fef83 finished 20:50:14; reuse start
  `-> ok 093a7504` at 31.68; s8 acked at 31.97.
- master-b c26b, counter-5 (5031791395894130905, 44.29, query `unsent:["s3"]`): run adf99d7f canceled 43.36 (s2/s4/act-s5
  then fail `not_exists`); reuse start `-> ok 16bbd6f9` at 44.09; `sig=s3 -> ok` at 44.17.
- v1.4.5-b bba4, counter-6 (1767405055980390830, 102.49, `unsent:["s34"]`): run cf7bf666 closed by 48.1 (s21
  `not_exists`); reuse start `-> ok 71f30fc9` at 86.61; s34 (sent before the reuse, ID between s21 and s38) acked at 99.55.
- Counting unsent signal IDs over all examples: 160/189 (f04f), 194/233 (3e97), 119/155 (1a0d), 36/231 (bba4), 0/128
  (c26b) name a signal that also has a late_ack failure in the same run. The others (checked: bba4 counter-6, c26b
  counter-5) are the same pattern where the old model closed as canceled/terminated or read its result after the ack, so
  the late_ack check never ran.

Driver fix (`driver/main.go` RuleReuseClosed, `driver/model.go`):
1. Track in-flight signal calls per model (`w.SigsInFlight`, ++ when `RuleSignal`/`RuleFinish` send, -- in the
   completion callback, including NotSent). In `RuleReuseClosed` require `old.SigsInFlight == 0` (as it already requires
   `DupPending == 0`).
2. Defensive: when creating the reused model, link it to the old one (`w.Prev = old`) and in `RuleQuery` / `checkCounter`
   treat an ID found in `w.Prev.SigByID` whose ack came after the reuse start as "late, from the previous run" (report
   under a separate Sometimes, not as unsent); in `signalDone` skip the `late_ack` Always when the ID has been reused
   since the result was read (`d.m.wfs[w.WID] != w`).

## S2: duplicate across continue-as-new (Cadence; not a driver bug)

Mechanism (Gap B, `rca/fixverify-reset-dup.md:83-97`). Signal dedup is per run: `signal_workflow_execution.go:66-75`
(v1.4.5 and master identical) checks `mutableState.IsSignalRequested(requestID)` of the **current** run, and `:113` adds
it. `AddContinueAsNewEvent` (`execution/mutable_state_builder.go:1223`, successor built at `:1287` with
`NewMutableStateBuilderWithVersionHistories`) copies no `pendingSignalRequestedIDs`. So: attempt 1 is applied to run A,
its reply is lost (frontend timeout), the workflow continues as new carrying the signal's effect in its state, and the
client's retry (same RequestID) reaches successor B and is applied again. One acknowledged SignalWorkflowExecution →
two applications. Every S2 example has `resets:0` and `runs>=2` (run counts 2–7), none has runs=1.

Evidence: master 3e97 counter-4 (input_hash 5552845235527353960, vtime 24.357, `duplicates:["s19"], runs:2`):
- 23.39 cadence1/cadence2 frontend "Frontend request timedout" / "client connection is closing" for
  `SignalWorkflowExecution wf-id=counter-4 wf-run-id=""` (the client retries);
- 24.26 driver `signal wid=counter-4 sig=s19 -> ok`; 24.36 result read: s19 applied twice across the CAN.
- Histories (cadence CLI in cadence1 at the failing moment, debugging session `18b4714b…-67-6`, `c4_hist.txt`):

  | run | event | what |
  |---|---|---|
  | 5f641059 | ev12 20:50:14.992 | Signaled add `s19`, client2, **RequestID 8d9f77ff-e823-45d1-b60b-000cf3abac41** |
  | 5f641059 | ev19 20:50:15.832 | ContinuedAsNew → e4b257b1 (state carries s19) |
  | e4b257b1 | ev5 20:50:16.397 | Signaled add `s19`, client2, **same RequestID 8d9f77ff-…** |
  | e4b257b1 | ev20 20:50:16.491 | Completed (Applied has s19 twice) |

  No reset (two runs linked only by ContinuedAsNew, no RESET_WORKFLOW DecisionTaskFailed). The second copy is the Go
  client's retry of the one SignalWorkflow call (same RequestID, same identity), which the driver saw acknowledged at
  20:50:16.397. Run e4b257b1's dedup set did not contain 8d9f77ff because it was created by continue-as-new.

Cadence intends retries to be deduplicated even after the run that took the signal has moved on: the comment at
`signal_workflow_execution.go:64-66` says dedup runs before the running check "so that already completed error won't be
returned for duplicated signals even if the workflow is closed". Continue-as-new defeats this, because the retry then
targets the successor.

Classification: this is the "signal-dedup-not-carried-across-continue-as-new" lead already named in the speedrun log
(21:40Z) and documented as Gap B; it is not one of the three fixed findings, which is why it persists on fix-verify
(22 examples: counter-29 act-s80 runs=3, counter-156 s853 runs=4, counter-36, counter-39). It should be promoted to its
own finding (below) or explicitly accepted as a documented limitation; the driver should not suppress it.

Proposed finding:
- name: `retried-signal-duplicated-after-continue-as-new`
- title: a signal retried by the client across a continue-as-new is applied twice
- severity: medium (silent double delivery of an acknowledged signal; needs a lost reply + CAN in the retry window;
  common under faults: 41–253 examples per 600-CH hunt, all four hunts and fix-verify)
- versions: v1.4.5, master (identical code)
- file:line: `service/history/engine/engineimpl/signal_workflow_execution.go:66-75,113`;
  `service/history/execution/mutable_state_builder.go:1223,1287`
- fix sketch: carry the predecessor's `pendingSignalRequestedIDs` into the CAN successor in `AddContinueAsNewEvent`
  (bounded set; could be limited to IDs added in the last N minutes), or have the frontend/history dedup consult the
  previous run of the chain when the current run was created by continue-as-new.

## S3: signals land on a run created by the driver's own duplicate start (driver false positive)

Mechanism. `RuleDuplicateStart` (`main.go:373-419`) starts an open counter's ID again with a drawn reuse policy and
expects `already_started`. When that start races with the original run closing (finish/cancel/terminate), or is retried
past the close, it succeeds and creates a new run (recorded only in `w.Foreign`). The model stays open and `RuleSignal`
keeps signaling the ID with an empty RunID; Cadence routes those to the current run — the foreign one. The result read is
pinned to `FirstRunID` (the original run), which does not contain them → `missing`.

Evidence:
- master-b c26b counter-3 (3154234530032195079, 44.66, `missing:["s7"]`): run 917ae775; finish acked 38.10; act-s6
  `not_exists` 38.10; `start wid=counter-3 reuse=1 -> ok 08036a4c` 38.10 (duplicate start: the model is unchanged — sent_n
  includes s7); `sig=s7 -> ok` 38.31 → applied to 08036a4c.
- fix-verify 1a0d counter-1 (6099553595031047181, 19.57, `missing:["s6","s7"]`): run 468b753c canceled/finished
  19.55–19.56; `start reuse=1 -> ok 138294ce` 19.568; s6, s7 acked 19.569/19.571 → on 138294ce.
- v1.4.5-b bba4 counter-5 (2044901303732086106, 37.37, `missing:["act-s7"]`): run c9b12586 finished 34.92 (s6
  `not_exists` 36.70); `start reuse=1 -> ok a21bfcef` 36.83; act-s7 acked 37.25.
- v1.4.5-b bba4 counter-33 (-2791764429923680754, 364.83, `applied_n:0, missing:["act-s130"]`): `start reuse=1 -> ok
  6e16ca4a` at 354.72 (a second run while the model's run c958a7bd had completed), act-s130 acked 355.14.

Driver fix:
1. `RuleSignal` / `RuleFinish` / `RuleQuery`: skip a model with `DupPending > 0 || len(Foreign) > 0` (its ID may now name
   a run the model does not own).
2. `checkCounter`: record at send time `s.MayBeForeign = w.DupPending > 0 || len(w.Foreign) > 0`, and when a duplicate
   start returns ok mark every signal of the model that is not yet acked as `MayBeForeign`; exclude those from `missing`
   (count them in a Sometimes "a signal reaches a run started by a duplicate start").
   Alternatively pass `run_id = FirstRunID` on signals once known — but that breaks continue-as-new chains, so (1)+(2)
   is preferred.

## False-positive review summary
- S1, S3: check stricter than Cadence semantics (an empty RunID targets the current run at processing time); the driver
  itself created the competing run (ID reuse / duplicate start) while calls were in flight. Each signal was applied once.
- S2: no API misuse (one SignalWorkflow call per signal ID; the duplicate is the Go client's own same-RequestID retry),
  harness topology irrelevant (any CAN), checker correct (the result is the workflow's own state). Real Cadence behaviour.
