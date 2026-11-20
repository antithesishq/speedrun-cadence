# Briefing: Uber · Cadence v1.4.5

<!-- Internal: for the customer-facing engineer who will turn this into a demo, a blog post or a
presentation. Never share this file with the customer. -->

**Status:** COMPLETE: all runs finished; nothing is still running. Fix-verify-6 `68cc2e9d` verified
the reset and signal patches and the shutdown guards except one more site, now guarded in
[combined-final.patch](../antithesis/fixes/combined-final.patch) (not run end to end). Remaining open
items: the [unconfirmed lost activity task](#lost-activity-task-after-shard-move); the client-container
exit-code-2 property (94 / 21 in the long runs), never triaged; the cross-cluster (XDC) harness, never
run in Antithesis; and the findings, not yet reported upstream. No stop code recorded (no state
file).
**Document version:** final edition, written 2026-11-20. It replaces the third edition (2026-11-19
about 21:00 UTC), the second (about 16:20 UTC) and the first (14:52 UTC).
**Dates:** 2026-11-18 18:52 UTC → 2026-11-20 (the last run, fix-verify-6, ended 01:13 UTC; deadline
was 2026-11-19 14:00 UTC; the session was paused for about 17 to 18 hours while runs finished, then
resumed for triage; see Method notes) · **Skill:**
speedrun v0.8, variant not recorded (no state file)
**Budget (both versions, this report and the master report together):** **4,370.7 of 5,000**
compute hours used by the 25 runs in the [run list](../antithesis/scratchbook/runs.tsv) that used
compute ([usage](../antithesis/scratchbook/runs/usage.txt), summed over the run IDs in the run list).
29 runs launched: 25 finished or cancelled after using compute, 4 cancelled before they started.
Debugging sessions used for triage are billed separately and not in this figure. This report's
share: 20 runs, about 3,120 compute hours (3,119.8): 6 iteration runs on v1.4.5 (52.5), 2 long hunts
(1,202.2), 4 known-bug runs shared with the master report (203.3), 7 fix-check runs (1,360.6,
including fix-verify-5 at 402.0 and fix-verify-6 at 402.8), and the heartbeat confirmation run
(301.2).
Elapsed: about 30 hours to the end of the last run.
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
is the consistency story, now with a window of at least 140 seconds. Fix-verify-6 `68cc2e9d` is
the good news: with resets succeeding, the reset-duplicate property passed (0 against 196 / 280),
"survives a reset" fell to 107 (two incidents, both our driver), and no failing signal example was a
duplicate. The caveats you must not forget: the recommended patch is
[combined-final.patch](../antithesis/fixes/combined-final.patch), and its last shutdown guards
(frontend `service.go:335`, history `service.go` `PrepareToStop`/`Stop`) passed unit tests only, not
an end-to-end run; fix-verify-5 tested **none** of the reset patches, because our own base-dedup-copy
patch broke every reset (say so plainly if asked); the continue-as-new dedup is a semantics change;
the hunt counts for the reset and continue-as-new findings overstate
production frequency; and the first edition is already public in the
[repository](#sharing-the-report). Two findings are in the Go client, not the server: say so. The
Go-client heartbeat stall is no longer a single occurrence: four more workflows in `37b22144`.

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
    [heartbeat-stall-hb1.md](../antithesis/scratchbook/rca/heartbeat-stall-hb1.md),
    [node-exits.md](../antithesis/scratchbook/rca/node-exits.md),
    [fixverify-residuals.md](../antithesis/scratchbook/rca/fixverify-residuals.md),
    [fixverify5-results.md](../antithesis/scratchbook/rca/fixverify5-results.md),
    [fixverify6-results.md](../antithesis/scratchbook/rca/fixverify6-results.md),
    [shard-unavailable-after-faults.md](../antithesis/scratchbook/rca/shard-unavailable-after-faults.md),
    and, from the tests document, [hunt-unexpected-close.md](../antithesis/scratchbook/rca/hunt-unexpected-close.md)
    and [hunt-timer-early.md](../antithesis/scratchbook/rca/hunt-timer-early.md);
  - adversarial reviews: all ten files in [adversarial/](../antithesis/scratchbook/adversarial/);
  - patches and diffs: all eleven patch files and the README in [fixes/](../antithesis/fixes/)
    (the recommended one is [combined-final.patch](../antithesis/fixes/combined-final.patch);
    combined-fixverify3.patch breaks every reset and must not be applied as is);
  - background: [system-brief.md](../antithesis/scratchbook/system-brief.md),
    [guarantees.md](../antithesis/scratchbook/guarantees.md),
    [positive-control.md](../antithesis/scratchbook/positive-control.md);
  - harness: [driver/](../antithesis/driver/), [client/](../antithesis/client/),
    [server/](../antithesis/server/), [config-template/](../antithesis/config-template/), and the
    config directories the Builds table links (`config-v1.4.5`, `config-v1.4.5-b`, `config-v1.4.5-hb`,
    `config-prefix`, `config-fixverify`, `config-fixverify2`, `config-fixverify3`, `config-fixverify4`).
- **The repository is public:** the whole workspace is at
  [github.com/antithesishq/speedrun-cadence](https://github.com/antithesishq/speedrun-cadence), and
  the first edition is already pushed (commit `0cb8539`, `main` = `origin/main`). That commit
  includes this briefing, the scratchbook (`speedrun-log.md`, `report-facts.md`,
  `runs/report-urls.tsv`) and the run report links. Consequences:
  - **report links carry access tokens** (`?auth=…`), so anyone who reads the repository can open
    every linked run report; decide whether to rotate them or remove them before a later edition
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
| [Retried reset applied twice](speedrun-report.md#retried-reset-applied-twice) | [Guarantee 2](speedrun-report.md#guarantee-2) | no own property; inside "an acknowledged signal survives a reset" (666 / 787); 362 of 373 sampled in `1a0d055c` | **confirmed** on unpatched v1.4.5: `bba4d2f0` counter-115 with histories ([2nd case](../antithesis/scratchbook/rca/retried-reset-applied-twice-2nd.md)), `f04f3907` counter-6 ([v1.4.5 analysis](../antithesis/scratchbook/rca/acked-signal-lost-on-reset-v145.md)); master-b counter-50 from logs only | stands, medium-high ([review](../antithesis/scratchbook/adversarial/retried-reset-applied-twice.md)) | [regression from #5821](../antithesis/scratchbook/rca/fixverify-reset-dup.md#gap-a-reset-retry-dedup-is-dead-code-new-bug) | v2 **verified** in `68cc2e9d` (782 `Duplicated reset request` dedups, no double reset in collected histories); CAN lookback verified indirectly; not exercised in `fc87ca1b` (no reset committed) |
| [Signal lost on reset](speedrun-report.md#acked-signal-lost-on-reset) | [Guarantee 2](speedrun-report.md#guarantee-2) | 666 / 787, **mixed** with the retried reset; 373 in `1a0d055c` (all sampled were the retried reset) | **confirmed** on v1.4.5: `f04f3907` counter-35 with histories ([v1.4.5 analysis](../antithesis/scratchbook/rca/acked-signal-lost-on-reset-v145.md)) | stands, medium ([review](../antithesis/scratchbook/adversarial/acked-signal-lost-on-reset.md)) | [new](../antithesis/scratchbook/rca/signal-lost-at-reset-master.md#root-cause-code) | **verified** in `68cc2e9d`: 107 (vs 666 / 787) = 2 incidents, both driver false positives (reset base not the model's run, current run created by the driver's duplicate start); earlier by absence in a 362-example sample (`1a0d055c`); not exercised in `fc87ca1b` |
| [Signals reordered](speedrun-report.md#buffered-signals-reordered-on-reload) | [Guarantee 2](speedrun-report.md#guarantee-2) (order is checked, not documented) | 169 / 175; 67 | all 5 runs, both versions; 2 cases checked in Postgres | stands, medium ([review](../antithesis/scratchbook/adversarial/buffered-signals-reordered-on-reload.md)) | [new](../antithesis/scratchbook/rca/hunt-signal-order.md#s1--new-buffered-signals-reordered-on-reload) | one-line fix, not in any build nor in combined-final.patch; still fails in `68cc2e9d` (99) |
| [Signal duplicated across continue-as-new](speedrun-report.md#retried-signal-duplicated-after-continue-as-new) | [Guarantee 2](speedrun-report.md#guarantee-2) | 41 / 79; 22 (587 across 5 runs); rates overstated by frequent continue-as-new | all 5 runs; master counter-4 from histories | stands, medium ([review](../antithesis/scratchbook/adversarial/retried-signal-duplicated-after-continue-as-new.md)) | [new](../antithesis/scratchbook/rca/hunt-signal-once-nonreset.md#s2-duplicate-across-continue-as-new-cadence-not-a-driver-bug) (was a lead in the first edition) | **verified** in `68cc2e9d`: 0 duplicates in 182 examples of the non-reset signal properties (semantics change); all 6 sampled duplicate incidents in `fc87ca1b` (138 examples) were this gap |
| [Stale history read](speedrun-report.md#stale-history-after-shard-steal) | [Guarantee 3](speedrun-report.md#guarantee-3), [Guarantee 4](speedrun-report.md#guarantee-4) | 68 / 88 (close event), 44 / 82 (describe); 54 and 31 | all 5 runs; long-window case `bba4d2f0` parent-4 | stands, medium ([review](../antithesis/scratchbook/adversarial/stale-history-after-shard-steal.md)) | [new](../antithesis/scratchbook/rca/stale-history-v145.md#root-cause-srccadence-v145) | no patch |
| [Parent stuck after cancelling children](speedrun-report.md#child-cancel-ack-after-close) | [Guarantee 4](speedrun-report.md#guarantee-4), [Guarantee 5](speedrun-report.md#guarantee-5) | 66 / 27; 2 | all 5 runs (client panic logged in every run) | stands, medium, Go client ([review](../antithesis/scratchbook/adversarial/child-cancel-ack-after-close.md); corrects which server step failed) | [new; client v1.4.0](../antithesis/scratchbook/rca/hunt-liveness.md#c2-new-real-go-client-child-cancel-ack-after-close) | no patch |
| [Retried signal applied twice after reset](speedrun-report.md#retried-signal-duplicated-after-reset) | [Guarantee 2](speedrun-report.md#guarantee-2) | 196 / 280 (property also catches the continue-as-new duplicate); 168 | many cases, both versions | stands, low-medium ([review](../antithesis/scratchbook/adversarial/retried-signal-duplicated-after-reset.md)) | [new](../antithesis/scratchbook/rca/reset-duplicates-signal.md#code) | **verified** in `68cc2e9d`: property passes, 0 (vs 196 / 280); v1 partially verified in `1a0d055c`; not exercised in `fc87ca1b` (the base-dedup copy broke every reset) |
| [Go-client decision-heartbeat stall](speedrun-report.md#retried-decision-heartbeat-stalls-workflow) | [Guarantee 4](speedrun-report.md#guarantee-4), [Guarantee 5](speedrun-report.md#guarantee-5) | 5 workflows in 2 runs: 1 in `6e868ff1`; 4 in `37b22144` (default 30m timeout; detector failed 230 examples) | **confirmed** in `37b22144`: race-62, race-59, race-55, race-51, two still stuck after 95+ empty cycles at the end-of-run check ([hb-1 analysis](../antithesis/scratchbook/rca/heartbeat-stall-hb1.md)); not found in the hunts, whose 1-minute timeout cuts the path off ([search](../antithesis/scratchbook/rca/heartbeat-stall-2nd.md)) | stands, medium-low, Go client ([review](../antithesis/scratchbook/adversarial/retried-decision-heartbeat-stalls-workflow.md)) | [new; client v1.4.0](../antithesis/scratchbook/rca/race-workflow-stuck.md#root-cause-go-client-gouberorgcadence-v140) | no patch |
| [Shutdown during startup panics](speedrun-report.md#sigterm-during-startup-panics) | none (crash safety) | about 1.4k / 1.4k; 0 nil-pointer panics in `1a0d055c`; 25 of 27 crash examples in `fc87ca1b` at three unguarded sites; 86 in `68cc2e9d`, all at one more site | many, all versions; three more `Stop()` sites found in `fc87ca1b` (frontend `service.go:341` adminHandler, matching `service.go:153` handler, `rpc/factory.go:206` peerLister), also present in the baselines ([analysis](../antithesis/scratchbook/rca/fixverify5-results.md#2-a-cadence-server-process-crashes-27-examples)) | stands, low ([review](../antithesis/scratchbook/adversarial/sigterm-during-startup-panics.md)) | [new](../antithesis/scratchbook/rca/node-exits.md#q2-exit-code-2) | **partially verified**: guarded sites at 0 (`1a0d055c`, `fc87ca1b`, `68cc2e9d`); in `68cc2e9d` all 39 panics (86 examples) at unguarded frontend `service.go:335`, guarded with history `service.go` `PrepareToStop`/`Stop` in combined-final.patch, unit-tested only |
| [Schema-check close panics](speedrun-report.md#sql-admin-conn-close-panic) | none (crash safety) | 119 (one incident) in `1a0d055c`; 8 of a 999 sample in `f04f3907` (claimed, raw sample not kept) | one incident with raw evidence | stands, low ([review](../antithesis/scratchbook/adversarial/sql-admin-conn-close-panic.md)) | [new](../antithesis/scratchbook/rca/fixverify-residuals.md#patch-verdicts) | 0 in `fc87ca1b` and `68cc2e9d` (verified weakly: one incident before) |

**Caveats:**
- The root-cause analysis was blinded only procedurally: the model may have seen a known bug in its
  training data. All ten look new; none matches a known upstream fix.
- **"An acknowledged signal survives a reset" mixes two findings.** Don't quote 666 / 787 as the
  lost-signal count; a timing check suggests most are the lost signal, but the split was not
  measured.
- **Hunt rates overstate production frequency** for the continue-as-new duplicate (the review says
  so explicitly: the driver continues as new every 3 or every 10 signals) and, by the same logic, for the
  reset findings (the driver resets often).
- The [decision-heartbeat stall](speedrun-report.md#retried-decision-heartbeat-stalls-workflow) no
  longer rests on one workflow: `37b22144` (default 30m timeout) showed it in four more, each traced
  through its history and the frontend errors ([hb-1 analysis](../antithesis/scratchbook/rca/heartbeat-stall-hb1.md)).
  It is a Go-client bug; both runs used the race workflows' 4-second decision timeout.
- The schema-check panic rests on one incident on disk; the baseline recurrence (8 in a f04f sample)
  is the analysis's statement only.
- The lost-signal patch is verified by `68cc2e9d`, where resets succeeded (4.3k) and all 107
  remaining "survives a reset" examples are two driver false positives, both traced through the
  histories. `fc87ca1b` committed no reset, so "survives a reset" passed vacuously there; don't
  quote its 0.
- What is **not** verified end to end: the last shutdown guards in combined-final.patch (frontend
  `service.go:335`, history `service.go` `PrepareToStop`/`Stop`; the history ones were latent, never
  seen panicking), the `ORDER BY` fix, and anything for the stale read or the Go-client findings.
- Signal order is not a documented Cadence guarantee; the reordering finding stands because Cadence
  preserves order on its normal path and its own code calls reordering a defect. Expect pushback.
- Settings that help: small caches, short ack and shard-update intervals, a 4-second decision
  timeout for race workflows. The adversarial reviews found that none of them is needed for any
  finding (the cache makes the reordering more frequent; for the stale read the tuning shrinks the
  window).
- The fix-check builds are local builds of v1.4.5 plus patches (worktrees
  `src/cadence-fixverify-signal` and `src/cadence-fixverify3`, changes uncommitted or staged); their
  commits are not on GitHub. `combined-fixverify3.patch` is the unstaged part only, on top of the v2
  dedup, lost-signal and nil-guard changes staged in that worktree. fix-verify-6 ran the whole
  `src/cadence-fixverify3` worktree (config `config-fixverify4`); `combined-fixverify4.patch` first
  held only its unstaged diff (missing the staged history and frontend guards, the v1 replicate
  dedup and part of the lost-signal reapply) and was regenerated with `git diff HEAD`, so it now
  matches what ran ([fixverify6-results.md](../antithesis/scratchbook/rca/fixverify6-results.md)).
  `combined-final.patch` = that plus the last Stop guards.

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
- **A startup data race in tchannel-go** (third party): one `fatal error: concurrent map read and map
  write` in `tchannel-go@v1.34.4/json/handler.go:100`, an inbound call reading the JSON handler map
  while handlers were still being registered, on cadence2 during startup in fix-verify-5
  [`fc87ca1b`][run-fc87ca1b] (vtime 661.02, right after "matching started"). Single occurrence, not
  seen in the three baselines, unrelated to our patches
  ([analysis](../antithesis/scratchbook/rca/fixverify5-results.md#2-a-cadence-server-process-crashes-27-examples)).
  Not a finding.
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
| [Go-client stall](speedrun-report.md#retried-decision-heartbeat-stalls-workflow) | `6e868ff1` (cancelled, no report); [`37b22144`][run-37b22144] | `5337532354675634409` / `401.2113032224588`; race-62 `8357354436728454509` / `241.4759`, race-59 `-7422523944834323835` / `661.5423`, race-55 `-5442527833525018112` / `945.1766`, race-51 `4070884268431302921` / `1468.3026` | not built | [logs/race-stuck/](../antithesis/scratchbook/logs/race-stuck/), [logs/heartbeat-stall-2nd/](../antithesis/scratchbook/logs/heartbeat-stall-2nd/), [logs/heartbeat-stall-hb1/](../antithesis/scratchbook/logs/heartbeat-stall-hb1/) |
| [Shutdown during startup panics](speedrun-report.md#sigterm-during-startup-panics) | `89a272ed` (cancelled); [`f04f3907`][run-f04f3907]; [`fc87ca1b`][run-fc87ca1b] (three further sites); [`68cc2e9d`][run-68cc2e9d] (frontend `service.go:335`) | hunt property sample `-2980147589376330513` / `300.41704890760593` (stack not checked individually); `68cc2e9d`: cadence3 at vtime 662.20 | not built | [logs/node-exits/](../antithesis/scratchbook/logs/node-exits/), [logs/fixverify5/](../antithesis/scratchbook/logs/fixverify5/) (`crash_*.ndjson`), [logs/fixverify6/](../antithesis/scratchbook/logs/fixverify6/) (`crash1.ndjson`, `panics.json`) |
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
  driver (fault exclusion `^driver$`), Postgres included; standard performance tier, except the
  runs after the deadline (`fc87ca1b`, `37b22144`, `68cc2e9d`: fast tier).
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
  was not updated after the first edition: its 7 rows are all still "pending adversarial review",
  and it lacks buffered-signals-reordered-on-reload, retried-signal-duplicated-after-continue-as-new
  and child-cancel-ack-after-close; [report-facts.md](../antithesis/scratchbook/report-facts.md)
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
- **Fix-verify-5 was invalid as a check of the reset patches (our regression).** `fc87ca1b` ran
  v1.4.5 plus `combined-fixverify3.patch`. Our `reset-copies-base-dedup-set` change
  (`copySignalRequestedIDs`, `resetter.go:365`) read the base run through the raw execution manager
  without a `ShardID`; the SQL store rejects that request ("execution persistence request missing
  shard ID"), so `prepareResetWorkflow` failed for every reset: 0 successful resets ("a workflow
  reset succeeds" 0/0, against 3.3k to 7.1k in the baselines), 269 of 300 sampled reset calls
  `bad_request`, 181,679 store warnings, reproduced with the CLI. Its unit test passed only because
  the mock expected the same request without a shard ID. Consequences: v2 dedup, base-dedup copy,
  reset dedup across continue-as-new and the lost-signal reapply were **not exercised**; "an
  acknowledged signal survives a reset" passed (3.3k/0) vacuously; "every started workflow closes"
  rose to 410 through driver false positive C1 (failed resets leave a terminated base run in the
  driver's ResetBases), not a Cadence liveness bug; the 114 + 100 signal-property examples sampled
  were the continue-as-new gap (6 incidents, 138 examples) and the known driver false positive S3,
  none reset-caused. What it did show: the guarded shutdown sites gone, three more unguarded `Stop()`
  sites, 0 schema-check close panics (weak), and the tchannel-go lead. The run log first recorded
  the passing "survives a reset" (speedrun-log.md 2026-11-19T20:25Z) and then the correction
  (20:51Z) ([analysis](../antithesis/scratchbook/rca/fixverify5-results.md)). Fix:
  read through `r.shard.GetWorkflowExecution` (sets ShardID and RangeID), in
  [combined-fixverify4.patch](../antithesis/fixes/combined-fixverify4.patch) together with the three
  Stop guards and signal dedup across continue-as-new; checked in fix-verify-6 `68cc2e9d` (next
  note).
- **Fix-verify-6 `68cc2e9d` was valid** (fast tier, 402.8 compute hours, 2026-11-19 21:01 to
  2026-11-20 01:13 UTC). 0 missing-shard-ID lines (fix-verify-5: 181,679); "a workflow reset
  succeeds" 4.3k. Per patch ([analysis](../antithesis/scratchbook/rca/fixverify6-results.md#per-patch-verdicts)):
  base-dedup copy verified; v2 reset dedup verified; reset dedup across continue-as-new verified
  indirectly; v1 signal dedup verified; signal dedup across continue-as-new verified; lost-signal
  reapply verified; Stop guards partially (gap at frontend `service.go:335`, guarded afterwards in
  combined-final.patch, unit-tested only); SQL close verified weakly. No regression found in the
  patched areas; other properties were out of scope and not re-triaged, and "a workflow ends only as
  completed, or as canceled or terminated on request" (99 here, 0 in `1a0d055c` and `fc87ca1b`) was not
  examined (probably the known driver false positive, 199 / 187 in the long hunts). The
  remaining signal-property failures are driver false positives: S1 (late ack or unsent after an ID
  reuse), S3 (signal routed to a run created by the driver's own duplicate start), and a new shape,
  a reset whose base is not the model's run (driver fix in the analysis; not applied or rerun).
- **Debugging sessions:** several were used for histories at failing moments; one borrowed session
  ended mid-triage and new ones were refused by the API, which left 11 + 37 fix-check examples
  unsampled. For fix-verify-6, histories came from session `729151c50c43e538b92c7e0f4c9cb229-67-6`,
  started and then cancelled.
- **Cross-cluster replication:** a two-cluster replication harness was built and validated locally
  on both versions ([antithesis-xdc/README.md](../antithesis-xdc/README.md)) but not run in
  Antithesis.
- **Document review:** earlier editions had a link check only; the final edition also had a
  fresh-reviewer truth check against the sources, and its findings were applied (one material: the
  base-dedup diff was linked without a warning that it breaks resets).

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

## Finished since the third edition

Nothing is still running. Finished since the third edition and now in the report:
**`68cc2e9d`** (fix-verify-6, `68cc2e9daf03e54787bfa9e0b8cf7e50-67-6`, [report][run-68cc2e9d]):
completed, 402.8 compute hours, fast tier; v1.4.5 plus
[combined-fixverify4.patch](../antithesis/fixes/combined-fixverify4.patch) (config
[config-fixverify4](../antithesis/config-fixverify4/)). Reset and signal patches verified, Stop
guards partially, SQL close weakly (see Method notes and
[fixverify6-results.md](../antithesis/scratchbook/rca/fixverify6-results.md)). The final patch,
[combined-final.patch](../antithesis/fixes/combined-final.patch), adds the Stop guards for the one
remaining site and for history `service.go` `PrepareToStop`/`Stop`; those were not run end to end.

Finished before the third edition: `fc87ca1b` (fix-verify-5, completed, 402.0 compute hours; invalid
as a reset-patch check, see Method notes) and `37b22144` (hb-1, completed, 301.2 compute hours;
stall confirmed in four more workflows).

## Open questions

- The [unconfirmed lost activity task](#lost-activity-task-after-shard-move) needs a targeted run.
- The client-container exit-code-2 property (94 / 21 in the long runs; 35 / 28 / 28 for client1-3
  in `68cc2e9d`) was never triaged.
- Cross-cluster replication (XDC): the harness was built and validated locally but never run in
  Antithesis.
- Upstream disclosure: none of the findings has been reported upstream; who reports, and when,
  given the public repository.
- The last Stop guards in combined-final.patch were not run end to end.
- The driver corrections for the hunt-triage false positives and for the fix-verify-6 reset
  bookkeeping are written up but not applied or rerun.
- No end-to-end check of the `ORDER BY` fix; the continue-as-new dedup (verified in fix-verify-6) is
  a semantics change Uber may not want.
- No patch for the stale read or for either Go client finding; whether a newer Go client fixes
  either.

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
| Build provenance | the [report's appendix](speedrun-report.md#builds), plus [config-v1.4.5/](../antithesis/config-v1.4.5/), [config-v1.4.5-b/](../antithesis/config-v1.4.5-b/), [config-v1.4.5-hb/](../antithesis/config-v1.4.5-hb/), [config-prefix/](../antithesis/config-prefix/), [config-fixverify/](../antithesis/config-fixverify/), [config-fixverify2/](../antithesis/config-fixverify2/), [config-fixverify3/](../antithesis/config-fixverify3/), [config-fixverify4/](../antithesis/config-fixverify4/) |

[c-master]: https://github.com/cadence-workflow/cadence/commit/572e7e2c786999df887f73ae46e59c72897a223b
[run-1a0d055c]: https://orbitinghail.antithesis.com/report/xN9Vj4U-36rrB2EGnmdrfioQ/QME7BrLqb1wMUqQZ_d7YrLxBhjYAnqTP7fAgJwKsiWQ.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDA0OjI1OjQ3LjYyNzEwODc2M1oiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiUU1FN0JyTHFiMXdNVXFRWl9kN1lyTHhCaGpZQW5xVFA3ZkFnSndLc2lXUS5odG1sIiwicmVwb3J0X2lkIjoieE45Vmo0VS0zNnJyQjJFR25tZHJmaW9RIn19favCyE4Utz3UgFQMlJMfMxrcal1_ucEel2g_f4YLh39_8wiydLxDlENLSsMIlTBGXI-s1LdcuIRGnqjm8s8CbAk
[run-41bf0672]: https://orbitinghail.antithesis.com/report/Ie4vOZxovWROfzayOSpNtShi/eLjnz7F_vDSB3n6eaOdMzCETUOyH1cFkim3NEWOkL8c.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE4VDIyOjQ4OjA2LjcyNjIyNTMyNFoiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiZUxqbno3Rl92RFNCM242ZWFPZE16Q0VUVU95SDFjRmtpbTNORVdPa0w4Yy5odG1sIiwicmVwb3J0X2lkIjoiSWU0dk9aeG92V1JPZnpheU9TcE50U2hpIn19fY73Z3acMhrO0ZzQOb-lafwX7od9KnJkzAZ2ve0NgdnhMnLUjQhh2GCLmMX42OqmG7mQRxDAW6a97al9AcRZTQs
[run-bba4d2f0]: https://orbitinghail.antithesis.com/report/mhssSs1i5EkMNSQHcVCPTzP9/Zrc1EgScZT43yY45AVfd0u4JP_Cg9AiF8TUs4qwzcIw.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDA5OjE4OjA5LjA2MDA2MDg0MFoiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiWnJjMUVnU2NaVDQzeVk0NUFWZmQwdTRKUF9DZzlBaUY4VFVzNHF3emNJdy5odG1sIiwicmVwb3J0X2lkIjoibWhzc1NzMWk1RWtNTlNRSGNWQ1BUelA5In19fRUBQvQ0iEjvyDFSQPGt83_js26ZyPXhhfdjAh7tx-njZmKtDic_lxU0jQ_VkPs_Hc_antm4HlZJV7nX8zr57g8
[run-f04f3907]: https://orbitinghail.antithesis.com/report/utHYY91d38r96H5gf-sYaMDe/vPClAGzQ3Uq2Kx0N6Wl0k3kHpcBDAfaTe5taQDtno8s.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDA5OjE3OjUwLjg0MjcxODU1NFoiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoidlBDbEFHelEzVXEyS3gwTjZXbDBrM2tIcGNCREFmYVRlNXRhUUR0bm84cy5odG1sIiwicmVwb3J0X2lkIjoidXRIWVk5MWQzOHI5Nkg1Z2Ytc1lhTURlIn19fVmwK1MIUiq4aD8_CS2kC16GH_eRoW1B7-PfkeakUA1GmOqmWR4jr3C7cB0V6Ce2SsjzSoQF9vtZVUWvi1ENqQU
[run-3e9720ca]: https://orbitinghail.antithesis.com/report/7hhEamcFv0jBPyIrsJWLmBsG/Jeb3agMIb7xHXvXT9nGWM_0NZ11M5mTMqfMjiaAbdGk.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDA5OjE4OjM5LjY0Mzg3MTU2M1oiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiSmViM2FnTUliN3hIWHZYVDluR1dNXzBOWjExTTVtVE1xZk1qaWFBYmRHay5odG1sIiwicmVwb3J0X2lkIjoiN2hoRWFtY0Z2MGpCUHlJcnNKV0xtQnNHIn19fZ1gkiwU1Kl8LZsAxyfNG66cLv07jhdY92GNtsGSJAI1SIU28W6K6kcS8AjgoNsNBipANRx7JKP41_2vPOY_dAY
[run-c26baba5]: https://orbitinghail.antithesis.com/report/wO_En085myHQmXR9je-NrxN_/WT_OG4On7fiNT5aGtjbH1FVwOWACRBMACZwaIMrfsCI.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDA5OjE5OjEwLjYxODM1NTkxNloiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiV1RfT0c0T243ZmlOVDVhR3RqYkgxRlZ3T1dBQ1JCTUFDWndhSU1yZnNDSS5odG1sIiwicmVwb3J0X2lkIjoid09fRW4wODVteUhRbVhSOWplLU5yeE5fIn19fZ4rU75H0q7pN8z0HJ2A211sZYt1M0VJPLrh4EJLDGOnDVBiAFIXO2_0959rLaOrdeRu-KAuVTRoe0v0ErcjMQY
[run-68cc2e9d]: https://orbitinghail.antithesis.com/report/EyMBLogszMC8DukBb7aZtC-0/siqDt5m0_XX4EcLmw6S1Ok-G8mfSnwxTpdakmohTThQ.html?auth=v2.public.eyJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0Ijoic2lxRHQ1bTBfWFg0RWNMbXc2UzFPay1HOG1mU253eFRwZGFrbW9oVFRoUS5odG1sIiwicmVwb3J0X2lkIjoiRXlNQkxvZ3N6TUM4RHVrQmI3YVp0Qy0wIn19LCJuYmYiOiIyMDI2LTExLTIwVDAwOjI0OjU2LjQ1ODc4Nzk5MloifYudU8JKiYOJS8640j7na_BBau94dUnDIUegh6AiOe4X5wqFe5yow5v-IocAFRUYJw61NIXr_kLUF78dFSbORQI
[run-fc87ca1b]: https://orbitinghail.antithesis.com/report/DTscNsuhSUVFM_Qdi1bMI6fQ/Tw7yJFhciYbFVDtTyPExBqLyYmOvMPUnC6eTvnC_eeI.html?auth=v2.public.eyJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiVHc3eUpGaGNpWWJGVkR0VHlQRXhCcUx5WW1Pdk1QVW5DNmVUdm5DX2VlSS5odG1sIiwicmVwb3J0X2lkIjoiRFRzY05zdWhTVVZGTV9RZGkxYk1JNmZRIn19LCJuYmYiOiIyMDI2LTExLTE5VDE5OjI1OjA0LjczNjExODA3N1oifX1CRXJPixytqD7JRDDfi0LUT94rSGtUizAJVytAZHT1EiY_yfzUMwWz3_MDSp_moH7SbzAzem8dIDJUzrBBwQo
[run-37b22144]: https://orbitinghail.antithesis.com/report/9zqT5qsDKT8wCLgBeP7M22qD/k-avt8ndPRXAsuQ2OvF_8J9ZVGf6LXK9VdWdHwC9DvE.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDE4OjIzOjQ0Ljc4NDY2MDI4MloiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0Ijoiay1hdnQ4bmRQUlhBc3VRMk92Rl84SjlaVkdmNkxYSzlWZFdkSHdDOUR2RS5odG1sIiwicmVwb3J0X2lkIjoiOXpxVDVxc0RLVDh3Q0xnQmVQN00yMnFEIn19fQG0yT3HZWDsY7qruTeczfQ2pKTjndyNt2Ulz9QzP_0_tBcMOMwsP1GB2uNm4QJC-Snve71nVppZxBcwnxRcYgE
