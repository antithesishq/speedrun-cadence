# Hunt triage: "a timer never fires before its duration"

Runs: v1.4.5 f04f3907…-67-6 (hunt), bba4d2f0…-67-6 (hunt -b); master 3e9720ca…-67-6, c26baba5…-67-6 (-b);
fix-verify 1a0d055c…-67-6 (v1.4.5 + three fix patches). Raw data: `logs/timer-early/` (fail-*.ndjson =
failing assertion events with moments; w-*/w2-*/slice_* = log windows around T0..T1).

**Verdict: false positive (checker stricter than the property it was designed to check). No new Cadence bug.**
Every failing example examined has an Antithesis clock skip (affected_nodes ALL) exactly at the moment
one of the two timestamps the check depends on was taken.

## What the check measures

- `client/workflows.go:233-239`: T0 = `workflow.Now` before `workflow.Sleep(ms)`, T1 = `workflow.Now` after.
- Go client v1.4.0 `internal_event_handlers.go:864-866`: `workflow.Now` is the timestamp of the last
  DecisionTaskStarted event; `:552-556` ignores a replay time earlier than the current one (time never goes
  backward in the workflow, so T1 == T0 when the later event is stamped earlier).
- Server timestamps are history-host wall-clock reads (`timeSource.Now()`), e.g. decision started
  `execution/mutable_state_decision_task_manager.go:517`; timer deadline =
  TimerStarted event timestamp + StartToFireTimeoutSeconds (`execution/mutable_state_builder_methods_timer.go:75-76`,
  same in master, with the upstream comment "TODO: Time skew need to be taken in to account");
  the timer is processed only when the wall clock reaches it (`queue/timer_queue_processor_base.go:552-557`
  isProcessNow, full precision; the 1-second `IsExpired` in `execution/timer_sequence.go:109-125` cannot fire
  early because the task is dispatched only after isProcessNow; each timer workflow has one timer).
- `driver/model.go:267-271` asserts `T1 - T0 >= ms` with no tolerance, although guarantees.md:73 defines the
  property with "tolerance: the clock jitter Antithesis injects".

So with a monotonic wall clock T0 <= TimerStarted.ts <= deadline <= T1. Any `T1 - T0 < ms` requires the
wall clock to step backward between T0 and T1. Antithesis `skip`/`clock` faults (affected_nodes ALL,
offsets observed up to -3.76 s, durations 0.1 ms – 10 s, reversed at the end) do exactly that.

## Signatures

Counts are failing examples (all, from `snouty runs search … --limit 300`; equal to the property's counterexample counts).

| # | Signature | f04f v1.4.5 | bba4 v1.4.5-b | 3e97 master | c26b master-b | 1a0d fix-verify | Classification |
|---|-----------|-----|-----|-----|-----|-----|----|
| A | T1 stamped during a backward clock skip; timer fired on time in real (v)time; shortfall = skip size minus latency (e.g. timer-101 −11 ms, timer-131 −184, timer-68 −425, timer-173 −65) | timer-101 (85) | timer-173 (31) | timer-131 (80) | timer-68 (83) | timer-9 (23) | false positive (clock jitter) |
| B | TimerStarted (deadline) stamped during a backward skip, or T0 stamped during a forward skip → deadline computed early, timer fires early in real time by up to the skip size (timer-10 −1.16 s, timer-61 −3.47 s, timer-6 −0.05..−0.72 s, timer-227 −0.97 s of a 1 s server timer) | timer-10, timer-61 | timer-6 (~59) | (timer-43 likely; in a skip storm, not mapped) | timer-227 (69) | (part of timer-9 storm) | false positive (Cadence uses wall clock by design; expected under clock steps) |
| C | left = 0 (T1 == T0): next DecisionTaskStarted stamped at or before T0, only possible with a wall-clock regression (client clamps) | timer-64 (1) | – | timer-24 (1) | timer-121 (1) | – | false positive (same cause) |
| | **Total (all one classification: clock-jitter false positive; per-example A/B split computed only for the wids shown)** | **143** | **126** | **128** | **172** | **67** | |

Per-run grouping of the most common examples (wid, shortfall ms, count):
- f04f: timer-101 11 ×85, timer-52 52 ×36, timer-59 1706 ×6, timer-10 724–1157 ×~10, timer-61 3467, timer-64 100 (left 0), others ×1.
- bba4: timer-173 65 ×31, timer-6 3–724 ×~59 (branches), timer-74 210 ×20, timer-54 115 ×7, timer-188 67 ×4.
- 3e97: timer-131 184 ×80, timer-24 18 ×25 (+651, +1000 left 0), timer-2 181/342 ×17, timer-43 2045 ×3, timer-211 52.
- c26b: timer-68 425 ×83, timer-227 72 ×69, timer-49 3/25 ×17, timer-99 3252 ×2, timer-121 1000 (left 0).
- 1a0d (fix-verify): timer-9 194 ×23, timer-13 3–26 ×25, timer-136 12 ×14, others ×1.

Same wid + same T0 with different T1 = different branches of one timeline (counts are branch multiplicity, not independent occurrences).

## Evidence (vtime mapping from driver log lines `YYYY/MM/DD hh:mm:ss`; vt ≈ wall − 74992.14 s on these runs)

1. **f04f timer-101 (85 ex.)**, moment -4643848433731978276 @269.79, window `w0-f04f-t101.ndjson`.
   Start rpc vt 172.434 (wall 20:52:44.576), T0 = 44.596 (vt ≈172.45). T1 = 20:52:54.585. Clock skip
   −0.480 s at vt 182.830 for 0.118 s. During that window (vt 182.923–182.928) postgres/cadence3/cadence2 log
   wall times 20:52:54.584–54.589 — i.e. T1 was stamped at vt ≈182.92 under the skip. Real elapsed
   182.92 − 172.45 = 10.47 s ≥ 10 s. Sig A.
2. **3e97 timer-131 (80 ex., master)**, moment -5467126386344104024 @1124.76, window `w-3e97-t131.ndjson`.
   Start vt 236.776 (T0 20:53:48.916). Skip −0.199 s at vt 246.792 for 0.197 s; T1 = 58.732 maps to vt
   246.59 unskewed / 246.79 under the skip = deadline. Shortfall 184 ms ≈ 199 ms skip. Sig A.
3. **c26b timer-68 (83 ex.)**, moment 6803487350378467879 @109.42, `w2-c26b-t68.ndjson`. Start vt 80.977
   (T0 20:51:13.115). Skip −0.434 s at vt 90.988 for 0.011 s; T1 = 20:51:22.690 = vt 90.99 − 0.434. Sig A.
4. **1a0d timer-9 (23 ex., fix-verify)**, moment 1400030559165475801 @326.96, `w-1a0d-t9.ndjson`: 911 clock
   skips in vt 50–91 (storm of ±0.2–0.96 s jitters) spanning the whole timer. Sig A/B.
5. **f04f timer-10**, moment -4730899953305220648 @26.42, `log_v145_a.ndjson` (full timeline). Start vt 17.494
   (T0 20:50:09.693, vt ≈17.55); skip −1.267 s at vt 17.609 for 0.045 s covers the decision completion
   (TimerStarted). Result assertion at vt 26.42: the 10 s timer completed ≈8.85 s of vtime after T0, i.e. it
   genuinely fired 1.16 s early in real time, by the skip size. Sig B.
6. **f04f timer-61 (−3467 ms)**, moment 3809214249990345720 @1082.19, `w-f04f-t61.ndjson`: T0 vt ≈772.83, skip
   −3.760 s at vt 772.962 for 1.386 s → TimerStarted stamped 3.76 s early. Sig B.
7. **c26b timer-227 (69 ex.)**, moment 8447914668393825433 @768.34, `w2-c26b-t227.ndjson`: start vt 757.953
   (T0 21:02:30.098); skip −1.271 s at vt 757.960 for 4.4 ms → deadline 30.098−1.27+1 s already past when the
   skip reverts; T1 = 30.126 (28 ms later). Sig B.
8. **bba4 timer-6**, moment -7695483161413907665 @22.10, `w-bba4-t6.ndjson`: skip +1.130 s at vt 18.265 for
   0.531 s; T0 (11.967, vt 18.695) stamped under the forward skip, decision completed around the reversal →
   shortfall 3–724 ms by branch. Sig B. **bba4 timer-173** (`w-bba4-t173.ndjson`): skip −0.070 s at vt 518.243
   = T0+30 s, shortfall 65. Sig A.
9. **c26b timer-121 (left 0)**, `w-c26b-t121.ndjson`: skips +1.43, +1.34, −0.83, +2.69, −1.61, −2.58 s within
   vt 403–407.3 around a 1 s timer started at vt 404.17. Sig C.

10. **1a0d (fix-verify) timer-13 (25 ex.)** `slice_1a0d-t13.ndjson`: T0 vt ≈31.35 inside a run of ±0.05–0.18 s skips
   (vt 31.23–32.49), shortfalls 3–26 ms. **timer-136 (14)** `w-1a0d-t136.ndjson`: T0 vt 389.02, skip −0.0144 s at vt
   390.022 for 3 ms = T1 instant, shortfall 12 (sig A). **timer-116** `w-1a0d-t116.ndjson`: T0 vt 244.90, skip
   −0.0207 s at vt 244.907 for 11 ms covers TimerStarted, shortfall 11 (sig B).

Coverage (second pass): the examined wids account for ≈ 85% of f04f, 75% of 3e97, 90% of c26b, 70% of bba4,
94% of 1a0d examples; every one examined had a skip at the T0/TimerStarted/T1 instant. The remaining examples have the
same shape (shortfall ≤ 3.5 s, matching skip magnitudes in these runs); not individually checked.

## Fix-verify

Present on fix-verify 1a0d (67) at a rate similar to v1.4.5 (143/5.7k vs 67/3.0k examples). Expected: the
three fixes touch reset/signal dedup/shutdown, not timers (`antithesis/fixes/*.patch` contain no timer code).

## Known findings

None of the known findings (violations.tsv) explains this; it is not a consequence of them. It is not the
#8595 signature (that one is "timer N resolved twice", a different assertion).

## Proposed driver fix (driver/model.go:267-271)

Implement the tolerance guarantees.md already specifies. Observed skips reach 3.76 s, so:

```go
case "timer":
    var tr struct{ T0, T1 int64 }
    _ = json.Unmarshal(r.Result, &tr)
    const clockJitterMs = 5000 // Antithesis clock skips step the wall clock by up to ~4 s; Cadence timers use the wall clock
    d := map[string]any{"wid": w.WID, "t0": tr.T0, "t1": tr.T1, "ms": w.TimerMs}
    assert.AlwaysGreaterThanOrEqualTo(tr.T1-tr.T0, int64(w.TimerMs)-clockJitterMs,
        "a timer never fires before its duration (within clock jitter)", d)
    assert.Sometimes(tr.T1-tr.T0 < int64(w.TimerMs), "a timer appears early by the wall clock (clock jitter)", d)
```

This leaves the short timers (100 ms – 3 s) unchecked for early firing. To keep a meaningful check for them,
add a gross check that does not depend on clocks: the timer workflow must not complete in the same decision
that started it, i.e. history must contain TimerStarted then TimerFired before WorkflowExecutionCompleted
(already partly covered by checkHistory's "resolves at most once, after it started"). A strict timing check
needs a clock-fault-free window, which the driver cannot observe.

## Side note (not a finding)

Cadence derives timer deadlines from the history host's wall clock at the moment it writes TimerStarted
(`mutable_state_builder_methods_timer.go:75-76`, both versions), so a backward wall-clock step fires timers
early in real time by the step size (sig B, up to 3.76 s here). This is documented upstream as a TODO and is
standard for wall-clock-based schedulers; it is not a violation of Cadence's stated semantics, which assume
synchronized clocks.
