# Briefing: Uber · Cadence v1.4.5

<!-- Internal: for the customer-facing engineer who will turn this into a demo, a blog post or a
presentation. Never share this file with the customer. -->

**Status:** PARTIAL: two confirmation runs still running (the complete-patch-set fix check
`fc87ca1b` and the heartbeat-stall confirmation `37b22144`); every failing property from the long
runs is triaged except the client-container exits (94 / 21). No stop code recorded (no state file).
**Document version:** second edition, written 2026-11-19 about 16:20 UTC, after the deadline. It
replaces the first edition (2026-11-19 14:52 UTC). A third edition should follow once the two
running runs finish.
**Dates:** 2026-11-18 18:52 UTC → 2026-11-19 16:20 UTC (deadline was 2026-11-19 14:00 UTC; the
session was paused for about 17 to 18 hours while runs finished, then resumed for triage; see
Method notes) · **Skill:**
speedrun v0.8, variant not recorded (no state file)
**Budget (both versions, this report and the master report together):** 3,265 compute hours used
by the 22 runs in the [run list](../antithesis/scratchbook/runs.tsv) that used compute
([usage](../antithesis/scratchbook/runs/usage.txt)), plus 700 committed to the two runs still
running (400 + 300): **3,965 of 5,000**. 28 runs launched: 22 finished or cancelled after using
compute, 4 cancelled before they started, 2 running. Debugging sessions used for triage are billed
separately and not in this figure. This report's share: 19 runs, about 2,714 compute hours: 6
iteration runs on v1.4.5 (52.5), 2 long hunts (1,202.2), 4 known-bug runs shared with the master
report (203.3), 5 finished fix-check runs (555.8), and the 2 running runs (700 committed).
Elapsed: about 21.5 hours.
**Customer report:** [speedrun-report.md](speedrun-report.md) · **Tests:** [speedrun-tests.md](speedrun-tests.md)

## The short version

Strong results on the real system, not a stand-in: ten findings (eight server, two Go client), each
with a root cause in the source and an adversarial review that let it stand, and every failing
property in the long runs now explained except the client-container exits, which are our own test
client. Lead with the signal family on
[Guarantee 2](speedrun-report.md#guarantee-2): [a retried reset applied twice](speedrun-report.md#retried-reset-applied-twice)
(a regression since #5821, now confirmed on unpatched v1.4.5), [an acknowledged signal lost on
reset](speedrun-report.md#acked-signal-lost-on-reset) (no faults needed), and
[signals reordered by a missing `ORDER BY`](speedrun-report.md#buffered-signals-reordered-on-reload)
(no faults needed, every run, a one-line fix). The [stale history read](speedrun-report.md#stale-history-after-shard-steal)
is the consistency story, now with a window of at least 140 seconds. The caveats you must not
forget: only the shutdown patch is verified end to end; the lost-signal patch is verified only by
the absence of its mechanism in a sample, and the complete patch set is still running; the hunt counts for the reset and continue-as-new findings overstate
production frequency; and the first edition is already public in the
[repository](#sharing-the-report). Two findings are in the Go client, not the server: say so.

## Sharing the report

- **Safe to share as is:** [speedrun-report.md](speedrun-report.md) and
  [speedrun-tests.md](speedrun-tests.md). Their relative links point into `../antithesis/`, so the
  report must travel with the same directory layout (the `v1.4.5/` folder next to `antithesis/`)
  and these files:
  - root causes: [fixverify-reset-dup.md](../antithesis/scratchbook/rca/fixverify-reset-dup.md),
    [retried-reset-applied-twice-2nd.md](../antithesis/scratchbook/rca/retried-reset-applied-twice-2nd.md),
    [signal-lost-at-reset-master.md](../antithesis/scratchbook/rca/signal-lost-at-reset-master.md),
    [acked-signal-lost-on-reset-v145.md](../antithesis/scratchbook/rca/acked-signal-lost-on-reset-v145.md),
    [acked-signal-lost-on-reset-fix.md](../antithesis/scratchbook/rca/acked-signal-lost-on-reset-fix.md),
    [hunt-signal-order.md](../antithesis/scratchbook/rca/hunt-signal-order.md),
    [hunt-signal-once-nonreset.md](../antithesis/scratchbook/rca/hunt-signal-once-nonreset.md),
    [stale-history-v145.md](../antithesis/scratchbook/rca/stale-history-v145.md),
    [history-missing-close-event.md](../antithesis/scratchbook/rca/history-missing-close-event.md),
    [hunt-closed-keeps-outcome.md](../antithesis/scratchbook/rca/hunt-closed-keeps-outcome.md),
    [hunt-liveness.md](../antithesis/scratchbook/rca/hunt-liveness.md),
    [reset-duplicates-signal.md](../antithesis/scratchbook/rca/reset-duplicates-signal.md),
    [query-shows-duplicate-signal.md](../antithesis/scratchbook/rca/query-shows-duplicate-signal.md),
    [race-workflow-stuck.md](../antithesis/scratchbook/rca/race-workflow-stuck.md),
    [heartbeat-stall-2nd.md](../antithesis/scratchbook/rca/heartbeat-stall-2nd.md),
    [node-exits.md](../antithesis/scratchbook/rca/node-exits.md),
    [fixverify-residuals.md](../antithesis/scratchbook/rca/fixverify-residuals.md),
    [shard-unavailable-after-faults.md](../antithesis/scratchbook/rca/shard-unavailable-after-faults.md),
    and, from the tests document, [hunt-unexpected-close.md](../antithesis/scratchbook/rca/hunt-unexpected-close.md)
    and [hunt-timer-early.md](../antithesis/scratchbook/rca/hunt-timer-early.md);
  - adversarial reviews: all ten files in [adversarial/](../antithesis/scratchbook/adversarial/);
  - patches and diffs: all nine files in [fixes/](../antithesis/fixes/);
  - background: [system-brief.md](../antithesis/scratchbook/system-brief.md),
    [guarantees.md](../antithesis/scratchbook/guarantees.md),
    [positive-control.md](../antithesis/scratchbook/positive-control.md);
  - harness: [driver/](../antithesis/driver/), [client/](../antithesis/client/),
    [server/](../antithesis/server/), [config-template/](../antithesis/config-template/), and the
    config directories the Builds table links (`config-v1.4.5`, `config-v1.4.5-b`, `config-v1.4.5-hb`,
    `config-prefix`, `config-fixverify`, `config-fixverify2`, `config-fixverify3`).
- **The repository is public:** the whole workspace is at
  [github.com/antithesishq/speedrun-cadence](https://github.com/antithesishq/speedrun-cadence), and
  the first edition is already pushed (commit `0cb8539`, `main` = `origin/main`). That commit
  includes this briefing, the scratchbook (`speedrun-log.md`, `report-facts.md`,
  `runs/report-urls.tsv`) and the run report links. Consequences:
  - **report links carry access tokens** (`?auth=…`), so anyone who reads the repository can open
    every linked run report; decide whether to rotate them or remove them before the second edition
    is pushed;
  - **the findings are public before upstream has seen them** (below);
  - "never share this file with the customer" no longer holds by itself while the briefing sits in a
    public repository; decide whether internal files belong there.
- **Check before sending:** the root-cause and adversarial files are internal working notes. They
  name local log paths, debugging-session IDs and the scratchbook's own vocabulary ("suspect",
  "harness", "false positive", short signature codes), and the stale-history root-cause
  files still say "suspect" where the adversarial review later let the finding stand. The
  child-cancel review corrects the root cause's account of which server step failed (same outcome).
  They are accurate, but read them once with the customer in mind.
- **Not for the customer:** this briefing and the rest of the scratchbook (speedrun-log.md,
  triage-context.md, runs/, logs/, violations.tsv, report-facts.md), and the hunt root-cause files
  for our own test code (hunt-second-start.md, hunt-unknown-signals-result.md, hunt-reuse-policy.md),
  which the report does not link.
- **Test-code errors in the report:** the report now says plainly, in one table, which failing
  properties were our own test code or test environment, without counts beyond the property totals
  the run reports show anyway. The tests document lists the corrections.
- **Disclosure:** none of the findings has been reported upstream (the user decided at the start:
  no GitHub filing), and the first edition, with six of them, is already in the public repository.
  Eight are server bugs present on v1.4.5 and on master [`572e7e2c`][c-master]; two are in the Go
  client v1.4.0. Coordinate with Uber (who maintain Cadence) before any further public content. Only
  [node-exits.md](../antithesis/scratchbook/rca/node-exits.md#recommendations) drafts an upstream
  report title; the others have no issue text yet.
- **Licence:** Cadence server and the Go client are both Apache License 2.0
  (`src/cadence-v1.4.5/LICENSE`, and the `go.uber.org/cadence@v1.4.0` module's LICENSE), not MIT.
  Apache 2.0 places no limit on publishing benchmarks or test results; only the usual attribution
  and notice terms apply to redistributed code (the patches are derivative of Cadence source).

## How confident we are

Failing examples: long runs [`f04f3907`][run-f04f3907] / [`bba4d2f0`][run-bba4d2f0], then the
fix-check run [`1a0d055c`][run-1a0d055c]. For the four new findings the counts are of the finding's
signature, separated from other causes in the hunt triage; for the others they are the dedicated
property's totals.

| Finding (by severity) | Guarantee broken | Failing examples / runs | Second case | Adversarial verdict | Root cause | Fix check |
|---|---|---|---|---|---|---|
| [Retried reset applied twice](speedrun-report.md#retried-reset-applied-twice) | [Guarantee 2](speedrun-report.md#guarantee-2) | no own property; inside "an acknowledged signal survives a reset" (666 / 787); 362 of 373 sampled in `1a0d055c` | **confirmed** on unpatched v1.4.5: `bba4d2f0` counter-115 with histories ([2nd case](../antithesis/scratchbook/rca/retried-reset-applied-twice-2nd.md)), `f04f3907` counter-6 ([v1.4.5 analysis](../antithesis/scratchbook/rca/acked-signal-lost-on-reset-v145.md)); master-b counter-50 from logs only | stands, medium-high ([review](../antithesis/scratchbook/adversarial/retried-reset-applied-twice.md)) | [regression from #5821](../antithesis/scratchbook/rca/fixverify-reset-dup.md#gap-a-reset-retry-dedup-is-dead-code-new-bug) | v2 + CAN lookback pass unit tests; in `fc87ca1b`, running |
| [Signal lost on reset](speedrun-report.md#acked-signal-lost-on-reset) | [Guarantee 2](speedrun-report.md#guarantee-2) | 666 / 787, **mixed** with the retried reset; 373 in `1a0d055c` (all sampled were the retried reset) | **confirmed** on v1.4.5: `f04f3907` counter-35 with histories ([v1.4.5 analysis](../antithesis/scratchbook/rca/acked-signal-lost-on-reset-v145.md)) | stands, medium ([review](../antithesis/scratchbook/adversarial/acked-signal-lost-on-reset.md)) | [new](../antithesis/scratchbook/rca/signal-lost-at-reset-master.md#root-cause-code) | **verified** for its own mechanism (by absence in a 362-example sample) |
| [Signals reordered](speedrun-report.md#buffered-signals-reordered-on-reload) | [Guarantee 2](speedrun-report.md#guarantee-2) (order is checked, not documented) | 169 / 175; 67 | all 5 runs, both versions; 2 cases checked in Postgres | stands, medium ([review](../antithesis/scratchbook/adversarial/buffered-signals-reordered-on-reload.md)) | [new](../antithesis/scratchbook/rca/hunt-signal-order.md#s1--new-buffered-signals-reordered-on-reload) | one-line fix, not in any build |
| [Signal duplicated across continue-as-new](speedrun-report.md#retried-signal-duplicated-after-continue-as-new) | [Guarantee 2](speedrun-report.md#guarantee-2) | 41 / 79; 22 (587 across 5 runs); rates overstated by frequent continue-as-new | all 5 runs; master counter-4 from histories | stands, medium ([review](../antithesis/scratchbook/adversarial/retried-signal-duplicated-after-continue-as-new.md)) | [new](../antithesis/scratchbook/rca/hunt-signal-once-nonreset.md#s2-duplicate-across-continue-as-new-cadence-not-a-driver-bug) (was a lead in the first edition) | diff only (semantics change), not in any build |
| [Stale history read](speedrun-report.md#stale-history-after-shard-steal) | [Guarantee 3](speedrun-report.md#guarantee-3), [Guarantee 4](speedrun-report.md#guarantee-4) | 68 / 88 (close event), 44 / 82 (describe); 54 and 31 | all 5 runs; long-window case `bba4d2f0` parent-4 | stands, medium ([review](../antithesis/scratchbook/adversarial/stale-history-after-shard-steal.md)) | [new](../antithesis/scratchbook/rca/stale-history-v145.md#root-cause-srccadence-v145) | no patch |
| [Parent stuck after cancelling children](speedrun-report.md#child-cancel-ack-after-close) | [Guarantee 4](speedrun-report.md#guarantee-4), [Guarantee 5](speedrun-report.md#guarantee-5) | 66 / 27; 2 | all 5 runs (client panic logged in every run) | stands, medium, Go client ([review](../antithesis/scratchbook/adversarial/child-cancel-ack-after-close.md); corrects which server step failed) | [new; client v1.4.0](../antithesis/scratchbook/rca/hunt-liveness.md#c2-new-real-go-client-child-cancel-ack-after-close) | no patch |
| [Retried signal applied twice after reset](speedrun-report.md#retried-signal-duplicated-after-reset) | [Guarantee 2](speedrun-report.md#guarantee-2) | 196 / 280 (property also catches the continue-as-new duplicate); 168 | many cases, both versions | stands, low-medium ([review](../antithesis/scratchbook/adversarial/retried-signal-duplicated-after-reset.md)) | [new](../antithesis/scratchbook/rca/reset-duplicates-signal.md#code) | v1 **partially verified**; v2 + base-dedup copy in `fc87ca1b`, running |
| [Go-client decision-heartbeat stall](speedrun-report.md#retried-decision-heartbeat-stalls-workflow) | [Guarantee 4](speedrun-report.md#guarantee-4), [Guarantee 5](speedrun-report.md#guarantee-5) | 1 workflow in 1 run (`6e868ff1`) | **not found** in the hunts: their 1-minute timeout cuts the path off ([search](../antithesis/scratchbook/rca/heartbeat-stall-2nd.md)); `37b22144` with the default timeout is running | stands, medium-low, Go client ([review](../antithesis/scratchbook/adversarial/retried-decision-heartbeat-stalls-workflow.md)) | [new; client v1.4.0](../antithesis/scratchbook/rca/race-workflow-stuck.md#root-cause-go-client-gouberorgcadence-v140) | no patch |
| [Shutdown during startup panics](speedrun-report.md#sigterm-during-startup-panics) | none (crash safety) | about 1.4k / 1.4k; 0 nil-pointer panics in `1a0d055c` | many, all versions | stands, low ([review](../antithesis/scratchbook/adversarial/sigterm-during-startup-panics.md)) | [new](../antithesis/scratchbook/rca/node-exits.md#q2-exit-code-2) | **verified** |
| [Schema-check close panics](speedrun-report.md#sql-admin-conn-close-panic) | none (crash safety) | 119 (one incident) in `1a0d055c`; 8 of a 999 sample in `f04f3907` (claimed, raw sample not kept) | one incident with raw evidence | stands, low ([review](../antithesis/scratchbook/adversarial/sql-admin-conn-close-panic.md)) | [new](../antithesis/scratchbook/rca/fixverify-residuals.md#patch-verdicts) | in `fc87ca1b`, running |

**Caveats:**
- The root-cause analysis was blinded only procedurally: the model may have seen a known bug in its
  training data. All ten look new; none matches a known upstream fix.
- **"An acknowledged signal survives a reset" mixes two findings.** Don't quote 666 / 787 as the
  lost-signal count; a timing check suggests most are the lost signal, but the split was not
  measured.
- **Hunt rates overstate production frequency** for the continue-as-new duplicate (the review says
  so explicitly: the driver continues as new every 3 or every 10 signals) and, by the same logic, for the
  reset findings (the driver resets often).
- The [decision-heartbeat stall](speedrun-report.md#retried-decision-heartbeat-stalls-workflow) still
  rests on one observed workflow; its mechanism was confirmed step by step in the code.
- The schema-check panic rests on one incident on disk; the baseline recurrence (8 in a f04f sample)
  is the analysis's statement only.
- The lost-signal patch is "verified" by the absence of its mechanism in a sample, not by a clean
  run; the running `fc87ca1b` is the real test.
- Signal order is not a documented Cadence guarantee; the reordering finding stands because Cadence
  preserves order on its normal path and its own code calls reordering a defect. Expect pushback.
- Settings that help: small caches, short ack and shard-update intervals, a 4-second decision
  timeout for race workflows. The adversarial reviews found that none of them is needed for any
  finding (the cache makes the reordering more frequent; for the stale read the tuning shrinks the
  window).
- The fix-check builds are local builds of v1.4.5 plus patches (worktrees
  `src/cadence-fixverify-signal` and `src/cadence-fixverify3`, changes uncommitted or staged); their
  commits are not on GitHub. `combined-fixverify3.patch` is the unstaged part only, on top of the v2
  dedup, lost-signal and nil-guard changes staged in that worktree.

## Unconfirmed findings

<a id="lost-activity-task-after-shard-move"></a>
### A retried activity task is never delivered after a shard moves

- **What was seen:** master-b [`c26baba5`][run-c26baba5] only, chain-57, 7 failing examples of
  "every started workflow closes once faults stop", all from one origin. The retried attempt 1 of
  activity 1 stayed SCHEDULED for more than 5 minutes after faults stopped; no transfer task pending,
  no matching task row, all workers polling. Shard 12 moved twice just before.
- **The specific doubt:** a sync-matched task whose `RecordActivityTaskStarted` failed may simply be
  dropped and recovered by the schedule-to-start timeout (1 hour here), which Cadence documents.
  Seen once, on master only.
- **What was tried:** histories, Postgres rows and task-list state collected in a debugging session
  at the failing moment.
- **What would settle it:** a targeted run with matching debug logging (`AddActivityTask`,
  sync-match, `RecordActivityTaskStarted` errors), or schedule-to-start set to 60 s to see recovery by
  timeout.
- **Evidence:** [hunt-liveness.md, chain-57 section](../antithesis/scratchbook/rca/hunt-liveness.md#c5-chain-57-master-b-only-7-examples-one-origin-suspect),
  [logs/hunt-liveness/](../antithesis/scratchbook/logs/hunt-liveness/).

The report lists it as unconfirmed, with this doubt. Client container exits with code 2 (94 / 21 in
the long runs) were not triaged individually; they come from our test client containers, not
Cadence, and the report says so.

## Leads

- **Shard acquisition uses `context.Background()`** (`shard/context.go:1136,1806`): one blocked
  `UpdateShard` freezes a host's shard-acquire loop. Seen as a shard unavailable for hours when
  killed processes left Postgres locks held; fixed on the harness side with Postgres timeouts
  ([analysis](../antithesis/scratchbook/rca/shard-unavailable-after-faults.md)).
- **A reset retry after the reset run was itself reset** is still not deduplicated, even with the v2
  patch and the continue-as-new lookback; a complete fix needs a carried set of reset request IDs
  ([analysis](../antithesis/scratchbook/rca/fixverify-residuals.md#patch-verdicts)). Not seen failing.
- **Timers use the history host's wall clock** (`mutable_state_builder_methods_timer.go:75-76`, an
  upstream TODO): a backward clock step fires a timer early by the step size, seen up to 3.5 s under
  Antithesis clock jitter. Consistent with Cadence's assumption of synchronised clocks, so not a
  finding ([analysis](../antithesis/scratchbook/rca/hunt-timer-early.md#side-note-not-a-finding)).
- The continue-as-new lead from the first edition is now the finding
  [signal duplicated across continue-as-new](speedrun-report.md#retried-signal-duplicated-after-continue-as-new).

## Evidence for each finding

Logs-explorer links could not be built: the logs explorer needs each run's session ID, which the
Antithesis API did not return for these runs. Each row gives the run and the moment instead.

| Finding | Run | Failing moment (input_hash / vtime) | Log | Local log |
|---|---|---|---|---|
| [Retried reset applied twice](speedrun-report.md#retried-reset-applied-twice) | [`41bf0672`][run-41bf0672]; [`bba4d2f0`][run-bba4d2f0]; [`f04f3907`][run-f04f3907] | `4272893864207768579` / `474.5656898911111`; counter-115 `-1266433295011687029` / `622.3960968970787`; counter-6 `3558643188807945681` / `98.726` | not built (no session ID) | [logs/fixverify-reset-dup/](../antithesis/scratchbook/logs/fixverify-reset-dup/), [logs/reset-dup-2nd/](../antithesis/scratchbook/logs/reset-dup-2nd/), [logs/ack-lost-v145/](../antithesis/scratchbook/logs/ack-lost-v145/) |
| [Signal lost on reset](speedrun-report.md#acked-signal-lost-on-reset) | [`f04f3907`][run-f04f3907]; `75b0745b` (master, cancelled, no report) | counter-35 `-924893256983270391` / `165.343`; master `-318482339591177353` / `34.675` | not built | [logs/ack-lost-v145/](../antithesis/scratchbook/logs/ack-lost-v145/), [logs/iter7m/](../antithesis/scratchbook/logs/iter7m/) |
| [Signals reordered](speedrun-report.md#buffered-signals-reordered-on-reload) | [`bba4d2f0`][run-bba4d2f0]; [`f04f3907`][run-f04f3907] | counter-70 `9004181159347822236` / `412.9155` (Postgres at `-7623883170619249068` / `403.808`); counter-104 `-6321992211471165745` / `469.37` | not built | [logs/hunt-signal-order/](../antithesis/scratchbook/logs/hunt-signal-order/) |
| [Signal duplicated across continue-as-new](speedrun-report.md#retried-signal-duplicated-after-continue-as-new) | [`3e9720ca`][run-3e9720ca] (master); [`f04f3907`][run-f04f3907]; [`bba4d2f0`][run-bba4d2f0] | counter-4 `5552845235527353960` / `24.357`; counter-69 `3651770994228528517` / `283.1587579736952`; counter-24 `7717577421291024984` / `442.5627213662956` | not built | [logs/hunt-signal-once/](../antithesis/scratchbook/logs/hunt-signal-once/) (`c4_hist.txt`, `all.jsonl`) |
| [Stale history read](speedrun-report.md#stale-history-after-shard-steal) | [`41bf0672`][run-41bf0672]; [`f04f3907`][run-f04f3907]; [`bba4d2f0`][run-bba4d2f0] | `-2175461847364005768` / about `96.5063`; `2114059415789563007` / `64.21569897816516`; parent-4 `-6618152698056279952` / `258.8111` | not built | [logs/stale-v145/](../antithesis/scratchbook/logs/stale-v145/), [logs/hist-close/](../antithesis/scratchbook/logs/hist-close/), [logs/hunt-closed-outcome/](../antithesis/scratchbook/logs/hunt-closed-outcome/) |
| [Parent stuck after cancelling children](speedrun-report.md#child-cancel-ack-after-close) | [`f04f3907`][run-f04f3907] | parent-26 `9190701013436246952` / `600.93` | not built | [logs/hunt-liveness/](../antithesis/scratchbook/logs/hunt-liveness/) (`p26_exec.txt`) |
| [Retried signal applied twice after reset](speedrun-report.md#retried-signal-duplicated-after-reset) | `89a272ed` (cancelled, no report); [`f04f3907`][run-f04f3907] | `-5028251366505688799` / `70.4274695077911`; property sample `2992591194688045504` / `378.16502781794406` (mechanism not checked individually) | not built | [logs/reset-dup/](../antithesis/scratchbook/logs/reset-dup/), [logs/query-dup/](../antithesis/scratchbook/logs/query-dup/) |
| [Go-client stall](speedrun-report.md#retried-decision-heartbeat-stalls-workflow) | `6e868ff1` (cancelled, no report) | `5337532354675634409` / `401.2113032224588` | not built | [logs/race-stuck/](../antithesis/scratchbook/logs/race-stuck/), [logs/heartbeat-stall-2nd/](../antithesis/scratchbook/logs/heartbeat-stall-2nd/) |
| [Shutdown during startup panics](speedrun-report.md#sigterm-during-startup-panics) | `89a272ed` (cancelled); [`f04f3907`][run-f04f3907] | hunt property sample `-2980147589376330513` / `300.41704890760593` (stack not checked individually) | not built | [logs/node-exits/](../antithesis/scratchbook/logs/node-exits/) |
| [Schema-check close panics](speedrun-report.md#sql-admin-conn-close-panic) | [`1a0d055c`][run-1a0d055c] | `6152778289480475374` / `404.6955` | not built | [logs/fixverify-residuals/](../antithesis/scratchbook/logs/fixverify-residuals/) (`crash.json`, `crash-slice.ndjson`) |

## Method notes

- **False positives caught before the hunts:** about 15, all fixed in the driver or harness
  ([speedrun-log.md](../antithesis/scratchbook/speedrun-log.md)). The most instructive:
  - [a signal "lost" during a reset](../antithesis/scratchbook/rca/signal-lost-during-reset.md):
    the driver accepted the result of the reset's terminated base run;
  - [results read from the wrong run](../antithesis/scratchbook/rca/result-from-wrong-run.md): a
    pending-reset counter leaked when a reset was never sent;
  - [straggler workflows that had in fact closed](../antithesis/scratchbook/rca/fixverify-stragglers.md):
    a timed-out reset that never committed was recorded as a reset base.
- **False positives found in the hunt triage (the frozen hunt driver; not yet fixed or rerun):**
  - **reused-ID races**, the big one: a signal, finish, cancel or terminate still retrying inside the
    Go client lands on the new run that `RuleReuseClosed` started on the same ID
    ([hunt-unexpected-close.md](../antithesis/scratchbook/rca/hunt-unexpected-close.md),
    [hunt-second-start.md](../antithesis/scratchbook/rca/hunt-second-start.md),
    [hunt-unknown-signals-result.md](../antithesis/scratchbook/rca/hunt-unknown-signals-result.md),
    [hunt-signal-once-nonreset.md](../antithesis/scratchbook/rca/hunt-signal-once-nonreset.md)).
    It explains six failing properties, including Guarantee 1's duplicate-start check (the other
    Guarantee 1 check, the reuse policy, failed on the next race);
  - two concurrent `RuleReuseClosed` picks of the same closed ID, and straggler starts
    ([hunt-reuse-policy.md](../antithesis/scratchbook/rca/hunt-reuse-policy.md));
  - the timer check with zero tolerance for injected clock jitter
    ([hunt-timer-early.md](../antithesis/scratchbook/rca/hunt-timer-early.md));
  - the eventually phase running after the environment shut every container down, and straggler
    bookkeeping (a terminate on a run a failed reset left current, a finish before an in-flight start,
    reads pinned to a foreign run) ([hunt-liveness.md](../antithesis/scratchbook/rca/hunt-liveness.md)).
  Lesson: in-flight request tracking per workflow ID is the single biggest gap in this driver.
- **Iteration runs to reach green:** 6 on v1.4.5 (all cancelled once a false positive was found),
  alongside 7 on master and 3 on the pre-fix build. The hunts used a frozen driver, which is why the
  hunt-triage false positives are still in their reports.
- **Fault types used:** Antithesis `high_availability` on every run, all containers except the
  driver (fault exclusion `^driver$`), Postgres included; standard performance tier, except the two
  confirmation runs (fast tier).
- **Fault types needed but unavailable:** none identified.
- **Harness gaps:** the history checker did not record which timer a `TimerCanceled` referred to, so
  the known bug was undetectable until fixed (before the third known-bug run). The logs explorer
  links can't be built without session IDs. Reads cannot tell which frontend served them (the
  client round-robins), which slowed the stale-read analysis. The decision-heartbeat detector sees
  only histories read after a workflow closed, not stragglers. The order check records a duplicated
  signal's last position, which could turn a duplicate into a false inversion (none seen).
- **Environment teardown mid-timeline:** in every long run, at a random moment, all containers
  except the driver were SIGTERMed with no fault event and never restarted
  ([hunt-liveness.md, environment shutdown](../antithesis/scratchbook/rca/hunt-liveness.md#l1-the-whole-sut-is-shut-down-then-eventually-runs-against-dead-containers-harness)).
  Worth asking Antithesis why.
- **Recurring warnings:** "a cadence server process crashes" fired about 1.4k times per hunt, almost
  all during stops (the shutdown finding), which hid the schema-check panic until the fix check
  removed them; client container exits with code 2 in every hunt, not triaged.
- **Runs cancelled early:** every iteration run, as soon as it showed a test-code problem; four runs
  (`90e135dd`, `fc9ee8d0`, `ad86b559`, `5a1ac4c2`) before they used any compute. The long hunts are
  marked "incomplete" by Antithesis although they used their full 600-hour budget.
- **Stale or inconsistent working files:** [positive-control.md](../antithesis/scratchbook/positive-control.md)
  still says "research only, nothing built" (the check did run and reproduce);
  [stale-history-v145.md](../antithesis/scratchbook/rca/stale-history-v145.md) classifies the
  finding "suspect", superseded by the adversarial review; [violations.tsv](../antithesis/scratchbook/violations.tsv)
  was not updated for the second edition; [report-facts.md](../antithesis/scratchbook/report-facts.md)
  links `hunt-liveness.md#c5`, which is not an anchor; [hunt-liveness.md](../antithesis/scratchbook/rca/hunt-liveness.md)
  says step 2 of the child-cancel race failed, which the adversarial review corrects to step 1.
  speedrun-log.md timestamps after 19:30 on 2026-11-18 ran about 30 minutes ahead of real UTC.
  The pause length is approximate: report-facts.md says about 17 hours; speedrun-log.md has no entry
  between 2026-11-18 21:40 and 2026-11-19 15:56 UTC (about 18 hours), and the first edition was
  written at 14:52, so the session resumed before then.
- **Sample moments replaced in the second-edition review:** the f04f3907 moments first cited for the
  retried signal after reset and the shutdown panic were in no scratchbook file; they were replaced
  with property samples from logs/stream-hunt1-v145.log, whose individual mechanism was not checked.
- **Patches and unit tests:** the lost-signal patch adds two unit tests: one fails without it, the
  other checks that flushed events are ignored when the run chain does not reach the current run; the
  reset, execution and engine packages pass. The v2 dedup patch passes the reset package. The
  base-dedup copy and the CAN lookback each add a test and pass the reset, execution and engine
  packages. No unit test for the first dedup patch alone, the shutdown nil guards, the SQL close
  change, the continue-as-new diff or the `ORDER BY` fix.
- **Debugging sessions:** several were used for histories at failing moments; one borrowed session
  ended mid-triage and new ones were refused by the API, which left 11 + 37 fix-check examples
  unsampled.
- **Cross-cluster replication:** a two-cluster replication harness was built and validated locally
  on both versions ([antithesis-xdc/README.md](../antithesis-xdc/README.md)) but not run in
  Antithesis.
- **Document review:** these documents had a link check only; the fresh-reviewer truth check was not
  run for either edition.

## Content hooks

- "One reset call, two resets: a lost reply and the Go client's standard retry make Cadence reset
  the same workflow twice and silently drop the signals in between, a regression hiding since
  April 2024."
- "A missing `ORDER BY`": Postgres returns buffered signals in heap order, and Cadence writes them to
  history that way. One line of SQL, found by a property nobody thought was at risk. Candidate
  diagram: the buffered-events rows with their ids and heap slots next to the history.
- "Completed, then running, for 140 seconds": a paused history host comes back still believing it
  owns a shard. Candidate diagram: two hosts both holding shard 4, the frontends split.
- Candidate diagram: the buffered-signal race on reset (decision in flight → signal buffered and
  acked → reset terminates the run → flush into the dead run → reapply misses it).
- Angle: exactly-once delivery is only as strong as its idempotency key; three findings are about
  request IDs that don't survive a reset or a continue-as-new.
- Angle, honest: a long run's failing properties are not all bugs. Of the 15 failing checks, 8
  traced to our own test code or environment, and triage separated them; the known-bug check
  re-found PR #8595 about a minute into a timeline.

## Still running when this edition was written

- **`fc87ca1b`** (fixverify-5, `fc87ca1b65b52553e7c27afe9f6897ef-67-6`): started 2026-11-19 15:57
  UTC, fast tier, 400 compute hours committed. v1.4.5 plus the complete patch set
  ([Builds](speedrun-report.md#builds)). Tests whether the reset-signal properties drop to near
  zero and the crash property to zero. No finish time estimated.
- **`37b22144`** (hb-1, `37b22144050fa198dba4d8a45fe63b29-67-6`): started 2026-11-19 16:03 UTC, fast
  tier, 300 compute hours committed. v1.4.5 with the default 30-minute decision-heartbeat timeout,
  to seek a second case of the [Go-client stall](speedrun-report.md#retried-decision-heartbeat-stalls-workflow).
  No finish time estimated.

Their results are not in the report.

## Open questions

- The results of the two running runs, and a third edition.
- The driver corrections for the hunt-triage false positives are written up but not applied or
  rerun.
- The [unconfirmed lost activity task](#lost-activity-task-after-shard-move) needs a targeted run.
- No end-to-end check of the `ORDER BY` fix or the continue-as-new diff (a semantics change Uber may
  not want).
- No patch for the stale read or for either Go client finding; whether a newer Go client fixes
  either.
- Upstream disclosure: who reports, and when, given the public repository.
- Cross-cluster replication was never run in Antithesis.

## Artifacts

| What | Where |
|---|---|
| Customer report | [speedrun-report.md](speedrun-report.md) |
| How the tests work | [speedrun-tests.md](speedrun-tests.md) |
| Facts sheet / log | [report-facts.md](../antithesis/scratchbook/report-facts.md), [speedrun-log.md](../antithesis/scratchbook/speedrun-log.md) (no state file) |
| Guarantees | [guarantees.md](../antithesis/scratchbook/guarantees.md) |
| Harness | [README.md](../antithesis/README.md), [driver/](../antithesis/driver/), [client/](../antithesis/client/), [server/](../antithesis/server/), [config-template/](../antithesis/config-template/), [scripts/](../antithesis/scripts/) |
| Findings | [violations.tsv](../antithesis/scratchbook/violations.tsv), [adversarial/](../antithesis/scratchbook/adversarial/), [rca/](../antithesis/scratchbook/rca/), [fixes/](../antithesis/fixes/) |
| Runs | [runs.tsv](../antithesis/scratchbook/runs.tsv), [runs/](../antithesis/scratchbook/runs/) (property tables, report URLs, usage); the full run table is in the [report's appendix](speedrun-report.md#runs) |
| Build provenance | the [report's appendix](speedrun-report.md#builds), plus [config-v1.4.5/](../antithesis/config-v1.4.5/), [config-v1.4.5-b/](../antithesis/config-v1.4.5-b/), [config-v1.4.5-hb/](../antithesis/config-v1.4.5-hb/), [config-prefix/](../antithesis/config-prefix/), [config-fixverify/](../antithesis/config-fixverify/), [config-fixverify2/](../antithesis/config-fixverify2/), [config-fixverify3/](../antithesis/config-fixverify3/) |

[c-master]: https://github.com/cadence-workflow/cadence/commit/572e7e2c786999df887f73ae46e59c72897a223b
[run-1a0d055c]: https://orbitinghail.antithesis.com/report/xN9Vj4U-36rrB2EGnmdrfioQ/QME7BrLqb1wMUqQZ_d7YrLxBhjYAnqTP7fAgJwKsiWQ.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDA0OjI1OjQ3LjYyNzEwODc2M1oiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiUU1FN0JyTHFiMXdNVXFRWl9kN1lyTHhCaGpZQW5xVFA3ZkFnSndLc2lXUS5odG1sIiwicmVwb3J0X2lkIjoieE45Vmo0VS0zNnJyQjJFR25tZHJmaW9RIn19favCyE4Utz3UgFQMlJMfMxrcal1_ucEel2g_f4YLh39_8wiydLxDlENLSsMIlTBGXI-s1LdcuIRGnqjm8s8CbAk
[run-41bf0672]: https://orbitinghail.antithesis.com/report/Ie4vOZxovWROfzayOSpNtShi/eLjnz7F_vDSB3n6eaOdMzCETUOyH1cFkim3NEWOkL8c.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE4VDIyOjQ4OjA2LjcyNjIyNTMyNFoiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiZUxqbno3Rl92RFNCM242ZWFPZE16Q0VUVU95SDFjRmtpbTNORVdPa0w4Yy5odG1sIiwicmVwb3J0X2lkIjoiSWU0dk9aeG92V1JPZnpheU9TcE50U2hpIn19fY73Z3acMhrO0ZzQOb-lafwX7od9KnJkzAZ2ve0NgdnhMnLUjQhh2GCLmMX42OqmG7mQRxDAW6a97al9AcRZTQs
[run-bba4d2f0]: https://orbitinghail.antithesis.com/report/mhssSs1i5EkMNSQHcVCPTzP9/Zrc1EgScZT43yY45AVfd0u4JP_Cg9AiF8TUs4qwzcIw.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDA5OjE4OjA5LjA2MDA2MDg0MFoiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiWnJjMUVnU2NaVDQzeVk0NUFWZmQwdTRKUF9DZzlBaUY4VFVzNHF3emNJdy5odG1sIiwicmVwb3J0X2lkIjoibWhzc1NzMWk1RWtNTlNRSGNWQ1BUelA5In19fRUBQvQ0iEjvyDFSQPGt83_js26ZyPXhhfdjAh7tx-njZmKtDic_lxU0jQ_VkPs_Hc_antm4HlZJV7nX8zr57g8
[run-f04f3907]: https://orbitinghail.antithesis.com/report/utHYY91d38r96H5gf-sYaMDe/vPClAGzQ3Uq2Kx0N6Wl0k3kHpcBDAfaTe5taQDtno8s.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDA5OjE3OjUwLjg0MjcxODU1NFoiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoidlBDbEFHelEzVXEyS3gwTjZXbDBrM2tIcGNCREFmYVRlNXRhUUR0bm84cy5odG1sIiwicmVwb3J0X2lkIjoidXRIWVk5MWQzOHI5Nkg1Z2Ytc1lhTURlIn19fVmwK1MIUiq4aD8_CS2kC16GH_eRoW1B7-PfkeakUA1GmOqmWR4jr3C7cB0V6Ce2SsjzSoQF9vtZVUWvi1ENqQU
[run-3e9720ca]: https://orbitinghail.antithesis.com/report/7hhEamcFv0jBPyIrsJWLmBsG/Jeb3agMIb7xHXvXT9nGWM_0NZ11M5mTMqfMjiaAbdGk.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDA5OjE4OjM5LjY0Mzg3MTU2M1oiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiSmViM2FnTUliN3hIWHZYVDluR1dNXzBOWjExTTVtVE1xZk1qaWFBYmRHay5odG1sIiwicmVwb3J0X2lkIjoiN2hoRWFtY0Z2MGpCUHlJcnNKV0xtQnNHIn19fZ1gkiwU1Kl8LZsAxyfNG66cLv07jhdY92GNtsGSJAI1SIU28W6K6kcS8AjgoNsNBipANRx7JKP41_2vPOY_dAY
[run-c26baba5]: https://orbitinghail.antithesis.com/report/wO_En085myHQmXR9je-NrxN_/WT_OG4On7fiNT5aGtjbH1FVwOWACRBMACZwaIMrfsCI.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDA5OjE5OjEwLjYxODM1NTkxNloiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiV1RfT0c0T243ZmlOVDVhR3RqYkgxRlZ3T1dBQ1JCTUFDWndhSU1yZnNDSS5odG1sIiwicmVwb3J0X2lkIjoid09fRW4wODVteUhRbVhSOWplLU5yeE5fIn19fZ4rU75H0q7pN8z0HJ2A211sZYt1M0VJPLrh4EJLDGOnDVBiAFIXO2_0959rLaOrdeRu-KAuVTRoe0v0ErcjMQY
