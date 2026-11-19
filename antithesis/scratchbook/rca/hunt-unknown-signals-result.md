# Hunt triage: "a workflow only applies signals that clients sent" and "a completed workflow returns the result its inputs determine"

Runs: v1.4.5 f04f3907…-67-6 (hunt), v1.4.5-b bba4d2f0…-67-6, master 3e9720ca…-67-6, master-b c26baba5…-67-6,
fix-verify 1a0d055c…-67-6 (v1.4.5 + the three non-v2 patches). Raw data: antithesis/scratchbook/logs/hunt-unknown-signals/
(det-unk-*.txt and det-res-*.txt are details.sh output; log_*.ndjson are timelines; c117.txt and reuse-check.txt are extracts).

## Result: one signature, a driver false positive

| signature | f04 v1.4.5 | bba4 v1.4.5-b | 3e97 master | c26b master-b | 1a0d fix-verify | classification |
|---|---|---|---|---|---|---|
| S1 `unknown_ids` = a signal the driver sent to an **earlier incarnation of a reused workflow ID** that was still in flight when the ID was reused. It lands in the new run. | 364 | 421 | 487 | 447 | 420 | **false positive (driver)** |
| S1' `sum` mismatch on the same counter. got−want equals the value of the unknown signal (for `act-` signals, also 3v+1 in acts). | 364 | 421 | 487 | 447 | 420 | **false positive (driver)**, same cause as S1 |

The counts are counterexample counts from `snouty runs properties`. Both properties have identical counts in every run, and every
sampled `sum` failure carries exactly the value of the unknown ID. For example, bba4 counter-117 act-s339 shows got 1181/1381 and
want 1158/1311, a difference of 23 in the sum and 70 = 3·23+1 in acts. So the result-property failures are S1 seen through the sum.
No chain, parent or race failures of the result property appear. If any existed, the result count would be higher than the
unknown-IDs count.

Top workflows in the samples (details.sh, 200 events):
- f04 v1.4.5: counter-106 s340, counter-269 s1009, counter-60 s182
- bba4 v1.4.5-b: counter-117 act-s339, counter-138 s265, counter-6 s34, counter-280 act-s1274
- 3e97 master: counter-1 s8, counter-85 s222, counter-5 s6
- c26b master-b: counter-42 s288, counter-5 s3, counter-40 act-s111+s103, counter-45 act-s163
- 1a0d fix-verify: counter-2 s16 (nearly every sample)

**The signature also appears on the fix-verify run.** That fits a driver cause. It is not explained by, and does not depend on, the three fixed findings.

## Why this is the only possible source of an unknown ID

- The client's counter workflow only appends the payload ID of an "add" signal. Every payload ID is a driver-generated, globally unique `s<N>` or `act-s<N>`.
- A `WF` model object is created only in `newWF` (model.go:81, unique WID) and in the reuse rule (main.go:710, the **same WID, with an empty `SigByID`**).
- I checked every sampled unknown ID with `snouty runs search` for its `rpc … signal` line. Each was sent to the **same WID** (reuse-check.txt: counter-269/s1009, counter-60/s182, counter-106/s340, counter-138/s265, counter-2/s16, counter-117/act-s339, counter-1/s8). So it belongs to an earlier `WF` object of that WID, which means an earlier incarnation of a reused ID. No sample showed delivery to a different workflow ID.

## Evidence: five timelines, one per run, show the same sequence

In each, the old incarnation is closed, its result is read and it is retired. The reuse rule then starts a new run of the ID. A signal
the old incarnation's model had sent, which was still outstanding, is **acknowledged after the new run's start returned**. A later query
or result of the new run contains that signal, and the new `WF` does not know it. Every case also fires
`an acknowledged signal is applied exactly once {late_ack:true}` on the old `WF`, the same false positive seen from the other side.

| run | moment (input_hash, vtime) | sequence (vtime) |
|---|---|---|
| master 3e97 | -5127699471893889288, 181.1454 | 28.82 old counter-1 (run 387fef83) result read, s8 not applied. 31.68 `start counter-1 reuse=1 -> ok 093a7504`. 32.10 `signal counter-1 sig=s8 -> ok` (late_ack). 36.79 query of the new run shows `unsent:[s8]`. Result check fails at 181.15 |
| fix-verify 1a0d | 1140796875513436815, 73.0749 | 36.25 old counter-2 (b6a301b4) result checked, s16 not applied. 54.77 `start counter-2 reuse=1 -> ok d36071cd`. 66.89 `signal sig=s16 -> ok` (late_ack). 67.05 reset of d36071cd. 70.30 query shows `unsent:[s16]`. 73.07 FAIL `unknown_ids:[s16]`, resets=1 (the reset reapplies s16 correctly) |
| v1.4.5-b bba4 | -2308660613165119196, 599.7729 | 378.67 old counter-117 terminated. 441.22 `start counter-117 reuse=0 -> ok 7236ca15`. 442.24 `signal sig=act-s339 -> ok`. 495.0 query shows `unsent:[act-s339]`. Then 2 resets. 599.77 FAIL |
| master-b c26b | 7586751906794967490, 438.7232 | 187.01 cancel of the old counter-42 (172e0a6a). 262.58 `start counter-42 reuse=0 -> ok edf1d77f`. 277.67 `signal sig=s288 -> ok` (15 s after the new start). 305.34 query of the new run shows `unsent:[s288]`. Checked at 438.72. No late_ack assert, because the old run was canceled and the driver never set its `Applied` |
| v1.4.5 f04 | -3540395189125934, 1037.554 (slice from 900) | 1004.78 finish on the old counter-269. 1006.00 `start counter-269 reuse=1 -> ok e3b43f3c`. 1006.16 `signal sig=s1009 -> ok` (late_ack). 1026.84 query shows `unsent:[s1009]`. 1037.55 FAIL |

Cadence behaved as documented here. The driver's "add" signal carries no RunID (client/main.go:340
`SignalWorkflow(ctx, wid, p.RunID="", …)`), and a signal with no run ID goes to the *current* run of the ID when the server
processes it. A request whose RPC was still running when the new run started may be processed after that start. The causes include a paused client,
a client writer backlog, or a Go-client retry after a transient error. Applying it to the new run is correct and linearizable.
In every inspected case the ack arrived *after* the new start returned, so no signal was acknowledged before the reuse and then
applied to the new run.

The resets in S1 (resets 1–8) have nothing to do with it. Reuse candidates require `Resets == 0` on the *old* incarnation, and the
resets happen later, on the new one. The reset correctly reapplies the carried-over signal.

## Cause in the driver

main.go:690-712 (reuse rule): the candidate filter checks `!active && !Reused && Resets==0 && ResetsPending==0 && ClosedRunID!="" &&
DupPending==0 && len(Foreign)==0`. It does **not** require the old incarnation's signals to be settled. The new `WF` starts with an
empty `SigByID` and no link to the old one. checkCounter (model.go:298-303) and RuleQuery (main.go:283) then call any carried-over
signal "unknown", and its value breaks the sum check (model.go:362). The old `WF`'s late-ack check (model.go:163-168) fires for the same
signal.

## Proposed driver fix

1. In the reuse rule, keep a link to the old incarnation and the time the reuse start was sent:
   `w := &WF{…, PrevRunID: old.ClosedRunID, Prev: old, StartSentAt: time.Now()}`.
2. Add a helper `carried(w, id) *Sig` that returns `w.Prev.SigByID[id]` when the ID is not in `w.SigByID` and the old signal was
   not acknowledged before the reuse start was sent (`s.State != StOK || !s.Acked.Before(w.StartSentAt)`).
   In checkCounter and RuleQuery, count such an ID as sent: add `s.V` to the expected sum, add `3v+1` to acts for `act-`, and do not list it as unknown. Put it in a
   `carried` detail field and add `assert.Sometimes(len(carried)>0, "a signal sent before an ID was reused lands on the new run")`.
   Keep a real check for the dangerous case: if the old signal **was** acked before the reuse start was sent and it shows up in the
   new run, call `assert.Always(false, "a signal acknowledged for one run is not applied to a later run", …)`.
3. In `signalDone`, when the late ack is for a `WF` with `Reused == true`, skip the `late_ack` exactly-once check (or check it against the
   successor's result instead). The ack can come from the new run.
4. Optionally make collisions rarer: require every old signal to be `!= StPending` before picking a reuse candidate. Steps 1–3 are still
   needed, because a signal whose RPC timed out can be applied by the server later.

## New Cadence bugs

None. No signature needs a known finding to explain it.

## Residual risk (adversarial review)

- Second pass (after the first report): I added the master-b timeline (counter-42/s288), so every run now has one inspected timeline.
  All five show an ack after the new start. I inspected the timelines of 5 of about 15 sampled workflow/signal pairs. I checked the other sampled pairs for "same WID" with search only. I did not
  rule out, in every one of the ~2100 counterexamples, a case where an old signal was acked **before** the reuse start and still
  applied to the new run, which would be a real bug. Fix step 2 turns that case into its own assertion, so a rerun with the fixed driver settles it.
- No debug sessions were started, so none needed cancelling.
