# Briefing: Uber · Cadence v1.4.5

<!-- Internal: for the customer-facing engineer who will turn this into a demo, a blog post or a
presentation. Never share this file with the customer. -->

**Status:** PARTIAL: written after the deadline; hunt failures still being triaged
**Document version:** first edition. An update will follow once the failures under
[Unconfirmed findings](#unconfirmed-findings) are triaged.
**Dates:** 2026-11-18 18:52 UTC → 2026-11-19 14:52 UTC (deadline was 2026-11-19 14:00 UTC; the
session was paused for about 17 hours while runs finished) · **Skill:** speedrun v0.8, variant not
recorded (no state file)
**Budget used (both versions, this report and the master report together):** 3,265 of 5,000
compute hours, over 22 runs that used compute (26 launched; 4 were cancelled before they started)
([usage](../antithesis/scratchbook/runs/usage.txt), [run list](../antithesis/scratchbook/runs.tsv)).
This report's share: 17 runs, about 2,014 compute hours: 6 iteration runs on v1.4.5 (52.5), 2 long
hunts (1,202.2), 4 known-bug runs shared with the master report (203.3), 5 fix-check runs (555.8).
Elapsed: about 20 hours.
**Customer report:** [speedrun-report.md](speedrun-report.md) · **Tests:** [speedrun-tests.md](speedrun-tests.md)

## The short version

Strong results on the real system, not a stand-in: six findings, each with a root cause in the
source and an adversarial review that let it stand. Lead with the reset family, all on
[Guarantee 2](speedrun-report.md#guarantee-2): [a retried reset applied twice](speedrun-report.md#retried-reset-applied-twice)
(a regression since #5821, seen once but deterministic in code),
[an acknowledged signal lost on reset](speedrun-report.md#acked-signal-lost-on-reset) (no faults
needed, 666 and 787 failing examples in the two long runs), and
[a retried signal applied twice after a reset](speedrun-report.md#retried-signal-duplicated-after-reset).
[The stale history read](speedrun-report.md#stale-history-after-shard-steal) is a good consistency
story (completed, then running, 0.6 ms apart, with no faults injected). The caveats you must not
forget: the fixes are **not** verified end to end (the fix-check run still failed the same
properties, under analysis), and a dozen other failing properties from the hunts are not triaged
yet, so the report is a first edition. One finding is in the Go client, not the server: say so.

## Sharing the report

- **Safe to share as is:** [speedrun-report.md](speedrun-report.md) and
  [speedrun-tests.md](speedrun-tests.md). Their relative links point into `../antithesis/`, so the
  report must travel with the same directory layout (the `v1.4.5/` folder next to `antithesis/`)
  and these files:
  - root causes: [fixverify-reset-dup.md](../antithesis/scratchbook/rca/fixverify-reset-dup.md),
    [signal-lost-at-reset-master.md](../antithesis/scratchbook/rca/signal-lost-at-reset-master.md),
    [acked-signal-lost-on-reset-fix.md](../antithesis/scratchbook/rca/acked-signal-lost-on-reset-fix.md),
    [stale-history-v145.md](../antithesis/scratchbook/rca/stale-history-v145.md),
    [history-missing-close-event.md](../antithesis/scratchbook/rca/history-missing-close-event.md),
    [reset-duplicates-signal.md](../antithesis/scratchbook/rca/reset-duplicates-signal.md),
    [query-shows-duplicate-signal.md](../antithesis/scratchbook/rca/query-shows-duplicate-signal.md),
    [race-workflow-stuck.md](../antithesis/scratchbook/rca/race-workflow-stuck.md),
    [node-exits.md](../antithesis/scratchbook/rca/node-exits.md),
    [shard-unavailable-after-faults.md](../antithesis/scratchbook/rca/shard-unavailable-after-faults.md);
  - adversarial reviews: all six files in [adversarial/](../antithesis/scratchbook/adversarial/);
  - patches: all four files in [fixes/](../antithesis/fixes/);
  - background: [system-brief.md](../antithesis/scratchbook/system-brief.md),
    [guarantees.md](../antithesis/scratchbook/guarantees.md),
    [positive-control.md](../antithesis/scratchbook/positive-control.md);
  - harness: [driver/](../antithesis/driver/), [client/](../antithesis/client/),
    [server/](../antithesis/server/), [config-template/](../antithesis/config-template/), and the
    config directories the Builds table links (`config-v1.4.5`, `config-v1.4.5-b`, `config-prefix`,
    `config-fixverify`, `config-fixverify2`).
- **Check before sending:** the root-cause and adversarial files are internal working notes. They
  name local log paths, the scratchbook's own vocabulary ("suspect", "harness"), and the stale-history
  root-cause files still say "suspect" where the adversarial review later let the finding stand.
  They are accurate, but read them once with the customer in mind.
- **The report's run links carry access tokens.** Anyone with one can open that run's report. Remove
  them before publishing anything publicly.
- **Not for the customer:** this briefing and the rest of the scratchbook (speedrun-log.md,
  triage-context.md, runs/, logs/, violations.tsv, report-facts.md).
- **Unconfirmed findings in the report:** the report lists every [unconfirmed finding](#unconfirmed-findings)
  under "Still under investigation", by property name only, labelled as possibly test-code issues,
  with no counts (the linked run reports show them).
- **Disclosure:** none of the findings has been reported upstream (the user decided at the start:
  no GitHub filing). Five are server bugs present on v1.4.5 and on master
  [`572e7e2c`][c-master]; one is in the Go client v1.4.0. Coordinate with Uber (who maintain
  Cadence) before any public content. Only [node-exits.md](../antithesis/scratchbook/rca/node-exits.md#recommendations)
  drafts an upstream report title; the others have no issue text yet.
- **Licence:** Cadence server and the Go client are both Apache License 2.0
  (`src/cadence-v1.4.5/LICENSE`, and the `go.uber.org/cadence@v1.4.0` module's LICENSE), not MIT.
  Apache 2.0 places no limit on publishing benchmarks or test results; only the usual attribution
  and notice terms apply to redistributed code (the patches are derivative of Cadence source).

## How confident we are

Failing examples are from the property that collects each kind of failure in the two long runs,
[`f04f3907`][run-f04f3907] / [`bba4d2f0`][run-bba4d2f0]; individual examples were not each
root-caused.

| Finding | Guarantee broken | Failing examples / runs | Adversarial verdict | Root cause | Fix check |
|---|---|---|---|---|---|
| [Retried reset applied twice](speedrun-report.md#retried-reset-applied-twice) | [Guarantee 2](speedrun-report.md#guarantee-2) | 1 workflow in 1 run ([`41bf0672`][run-41bf0672]); no dedicated property | stands ([review](../antithesis/scratchbook/adversarial/retried-reset-applied-twice.md)) | [new; regression from #5821](../antithesis/scratchbook/rca/fixverify-reset-dup.md#gap-a-reset-retry-dedup-is-dead-code-new-bug) | v2 patch passes the reset package's unit tests; not run end to end |
| [Signal lost on reset](speedrun-report.md#acked-signal-lost-on-reset) | [Guarantee 2](speedrun-report.md#guarantee-2) | 666 / 787 in 2 hunts; also 1.0k on master-b, 138 on the pre-fix build | stands ([review](../antithesis/scratchbook/adversarial/acked-signal-lost-on-reset.md)) | [new](../antithesis/scratchbook/rca/signal-lost-at-reset-master.md#root-cause-code) | unit tests pass; end-to-end run [`1a0d055c`][run-1a0d055c] still 373 failures (under analysis) |
| [Stale history read](speedrun-report.md#stale-history-after-shard-steal) | [Guarantee 3](speedrun-report.md#guarantee-3) | 68 / 88 in 2 hunts; 116 on master-b | stands, medium severity ([review](../antithesis/scratchbook/adversarial/stale-history-after-shard-steal.md)) | [new](../antithesis/scratchbook/rca/stale-history-v145.md#root-cause-srccadence-v145) | no patch |
| [Retried signal applied twice](speedrun-report.md#retried-signal-duplicated-after-reset) | [Guarantee 2](speedrun-report.md#guarantee-2) | 196 / 280 in 2 hunts; 701 on master-b, 73 on the pre-fix build | stands ([review](../antithesis/scratchbook/adversarial/retried-signal-duplicated-after-reset.md)) | [new](../antithesis/scratchbook/rca/reset-duplicates-signal.md#code) | v1 in [`1a0d055c`][run-1a0d055c]: still 168 failures (under analysis); v2 not run end to end |
| [Go-client decision-heartbeat stall](speedrun-report.md#retried-decision-heartbeat-stalls-workflow) | [Guarantee 4](speedrun-report.md#guarantee-4), [Guarantee 5](speedrun-report.md#guarantee-5) | 1 workflow in 1 run (`6e868ff1`); hunts ran with a 1-minute timeout and the detector passed | stands, as a Go client bug ([review](../antithesis/scratchbook/adversarial/retried-decision-heartbeat-stalls-workflow.md)) | [new; client v1.4.0](../antithesis/scratchbook/rca/race-workflow-stuck.md#root-cause-go-client-gouberorgcadence-v140) | no patch |
| [Shutdown during startup panics](speedrun-report.md#sigterm-during-startup-panics) | none (crash safety) | about 1.4k / 1.4k in 2 hunts, all while stopping | stands, low ([review](../antithesis/scratchbook/adversarial/sigterm-during-startup-panics.md)) | [new](../antithesis/scratchbook/rca/node-exits.md#q2-exit-code-2) | patch has no unit test; [`1a0d055c`][run-1a0d055c] still 119 crash reports (under analysis) |

**Caveats:**
- The root-cause analysis was blinded only procedurally: the model may have seen a known bug in its
  training data. All six look new; none matches a known upstream fix.
- Two findings rest on one observed workflow each: the [retried reset](speedrun-report.md#retried-reset-applied-twice)
  and the [Go-client stall](speedrun-report.md#retried-decision-heartbeat-stalls-workflow). Both
  mechanisms were confirmed step by step in the code, and the reset case shows identical request IDs
  on both resets in the history.
- The [acknowledged-signal loss](speedrun-report.md#acked-signal-lost-on-reset) was analysed step by
  step on master; v1.4.5 has identical code (empty diff of `resetter.go`), and a v1.4.5 case
  (counter-115) is marked "likely same" but was not analysed.
- Hunt counts are property counts. The fix-check run shows how much they can include: after the
  patches, the same properties still failed hundreds of times, and we don't yet know whether that
  is the same bug, another bug, or our test code.
- Settings that help: small caches, short ack and shard-update intervals, a 4-second decision
  timeout for race workflows. The adversarial reviews found that none of them is needed for any
  finding, and that for the stale read the tuning shrinks the window.
- The fix-check builds are local builds of v1.4.5 plus patches (worktree
  `src/cadence-fixverify-signal`, commits `ffbc65c2f` and `a7da2f80c` plus uncommitted nil guards);
  their commits are not on GitHub.

## Unconfirmed findings

Failing properties from the two long runs whose cause is not yet established. They may be further
Cadence bugs or test-code issues: about 15 earlier failures of the same checks turned out to be
our own test code ([Method notes](#method-notes)). The report lists each by name under "Still under
investigation", without counts. Root-cause files (`rca/hunt-*.md`) are in progress and don't exist
yet; raw evidence collected so far is linked. Counts are failing examples in
[`f04f3907`][run-f04f3907] / [`bba4d2f0`][run-bba4d2f0]
([props-f04f3907.txt](../antithesis/scratchbook/runs/props-f04f3907.txt),
[props-bba4d2f0.txt](../antithesis/scratchbook/runs/props-bba4d2f0.txt)).

<a id="timer-fires-early"></a>
### A timer fires before its duration

- **What was seen:** "a timer never fires before its duration", 143 / 126; also 172 on master-b and
  67 in the fix check, but 0 on the pre-fix build. [Guarantee 4](speedrun-report.md#guarantee-4).
- **The specific doubt:** the check compares two `workflow.Now` readings (the times of the decisions
  before and after the sleep, which different hosts may stamp) with no tolerance at all, although
  the guarantee allows for the clock jitter Antithesis injects
  ([TimerWorkflow](../antithesis/client/workflows.go), [model.go:270](../antithesis/driver/model.go#L270)).
  It never failed on the pre-fix build, which points either to a newer Cadence change or to chance.
- **What was tried:** evidence collected; no root cause yet.
- **What would settle it:** the TimerStarted and TimerFired event times of one failing example
  against the injected clock jitter at that moment.
- **Evidence:** [logs/timer-early/](../antithesis/scratchbook/logs/timer-early/)

<a id="workflow-closes-unrequested"></a>
### A workflow closes in a way nobody requested

- **What was seen:** "a workflow ends only as completed, or as canceled or terminated on request",
  199 / 187 ([Guarantee 4](speedrun-report.md#guarantee-4)); it passed in the fix check.
- **The specific doubt:** resets terminate their base run; reads attributed to a reset's base run
  were the cause of several earlier false positives, and this check depends on the same attribution.
- **What was tried:** evidence collected; no root cause yet.
- **What would settle it:** the run chain (list and histories) of one failing workflow.
- **Evidence:** [logs/hunt-unexpected-close/](../antithesis/scratchbook/logs/hunt-unexpected-close/)

<a id="second-start-accepted"></a>
### A second start of an open workflow ID is accepted

- **What was seen:** "a second start of an open workflow ID is rejected", 125 / 163
  ([Guarantee 1](speedrun-report.md#guarantee-1)); it passed in the fix check.
- **The specific doubt:** an earlier failure of this check was our own bug (the eventually phase
  didn't record its finish signal, [duplicate-start-accepted.md](../antithesis/scratchbook/rca/duplicate-start-accepted.md));
  the check skips workflows that may be closing, but a workflow can still close between the pick
  and the start.
- **What was tried:** failing details and logs for one workflow per hunt collected.
- **What would settle it:** whether the earlier run was still open (history close time) when the
  second start was accepted.
- **Evidence:** [logs/hunt-second-start/](../antithesis/scratchbook/logs/hunt-second-start/)

<a id="reuse-policy-violated"></a>
### A start on a closed ID breaks its reuse policy

- **What was seen:** "a start on a closed workflow ID follows its reuse policy", 76 / 75
  ([Guarantee 1](speedrun-report.md#guarantee-1)).
- **The specific doubt:** the model's idea of the previous run's close status (completed or not)
  must match Cadence's; a reset or a foreign run could make it wrong.
- **What was tried:** evidence collected; no root cause yet.
- **What would settle it:** the previous run's close status from Cadence for one failing example.
- **Evidence:** [logs/reuse-policy/](../antithesis/scratchbook/logs/reuse-policy/)

<a id="signals-out-of-order"></a>
### Signals applied out of acknowledgement order

- **What was seen:** "signals are applied in the order they were acknowledged", 169 / 175
  ([Guarantee 2](speedrun-report.md#guarantee-2)).
- **The specific doubt:** a reset reapplies signals and a retried signal can land later
  ([retried signal applied twice](speedrun-report.md#retried-signal-duplicated-after-reset)), so
  these may be consequences of known findings rather than a separate bug.
- **What was tried:** failing moments and duplicate lists from all four hunts and the fix check
  collected.
- **What would settle it:** the run chain of one failing workflow with no reset.
- **Evidence:** [logs/hunt-signal-order/](../antithesis/scratchbook/logs/hunt-signal-order/)

<a id="unknown-signal-or-wrong-result"></a>
### A workflow applies a signal nobody sent, or returns the wrong result

- **What was seen:** "a workflow only applies signals that clients sent", 364 / 421, and "a
  completed workflow returns the result its inputs determine", 364 / 421
  ([Guarantee 2](speedrun-report.md#guarantee-2), [Guarantee 4](speedrun-report.md#guarantee-4)).
  The equal counts suggest the same examples.
- **The specific doubt:** a signal "nobody sent" usually means the result was read from a run the
  model doesn't own (a reused ID or a duplicate start), which was the cause of several earlier false
  positives.
- **What was tried:** evidence collected; no root cause yet.
- **What would settle it:** the run IDs in one failing result against the model's run IDs.
- **Evidence:** [logs/hunt-unknown-signals/](../antithesis/scratchbook/logs/hunt-unknown-signals/)

<a id="closed-workflow-changes-outcome"></a>
### A closed workflow is later seen with a different outcome

- **What was seen:** "a closed workflow keeps its outcome", 44 / 82
  ([Guarantee 4](speedrun-report.md#guarantee-4)).
- **The specific doubt:** it may be the [stale history read](speedrun-report.md#stale-history-after-shard-steal)
  seen through describe, or a read attributed to the wrong run.
- **What was tried:** not yet examined.
- **What would settle it:** which host served the later read, and its cached state.
- **Evidence:** the run reports above.

<a id="liveness-after-faults"></a>
### Clients or workflows don't recover after faults stop

- **What was seen:** "every client is served again once faults stop", 173 / 346, and "every started
  workflow closes once faults stop", 230 / 255 ([Guarantee 5](speedrun-report.md#guarantee-5),
  [Guarantee 4](speedrun-report.md#guarantee-4)). "a new workflow completes within 2 minutes once
  faults stop" passed in both.
- **The specific doubt:** earlier failures of these checks were harness problems (no supervisor,
  workers not restarted, orphaned Postgres locks); the 3- and 5-minute windows are our choice.
- **What was tried:** not yet examined in the hunts.
- **What would settle it:** the stragglers listed in one failing example's details, and the state
  of their shards and task lists.
- **Evidence:** the run reports above.

<a id="signal-once-without-reset"></a>
### A signal duplicated or lost in a workflow that was never reset

- **What was seen:** "an acknowledged signal is applied exactly once", 177 / 216, and "a query shows
  each delivered signal once", 186 / 228 ([Guarantee 2](speedrun-report.md#guarantee-2)).
- **The specific doubt:** the lead [signal dedup not carried across continue-as-new](#leads) would
  produce exactly such duplicates without any reset, and Cadence does not document that dedup
  crosses continue-as-new.
- **What was tried:** not yet examined in the hunts.
- **What would settle it:** whether the duplicate copies straddle a continue-as-new with the same
  request ID.
- **Evidence:** the run reports above; [Gap B in fixverify-reset-dup.md](../antithesis/scratchbook/rca/fixverify-reset-dup.md#gap-b-dedup-set-is-per-run-and-not-carried-by-continue-as-new-separate-known-limitation).

<a id="client-container-exits"></a>
### Client containers exit with code 2

- **What was seen:** Antithesis's "No unexpected container exits" for client1-3, 94 / 21 (29+37+28
  and 6+9+6). The client containers run our test client, not Cadence.
- **The specific doubt:** an earlier version exited with code 2 when a stop arrived before its
  signal handler was installed; that was fixed, but exits remain. Most likely a harness issue.
- **What was tried:** not yet examined.
- **What would settle it:** the client's last output before one exit.
- **Evidence:** the run reports above.

## Leads

- **Signal dedup not carried across continue-as-new.** `AddContinueAsNewEvent` builds the new run
  without copying the signal request IDs (`mutable_state_builder.go:1223/1287`), so a client retry
  that lands on the new run is applied again, with no reset involved. It contributed to the
  [retried reset](speedrun-report.md#retried-reset-applied-twice) case. Cadence does not document
  cross-continue-as-new dedup, so it may be a known limitation
  ([analysis](../antithesis/scratchbook/rca/fixverify-reset-dup.md#gap-b-dedup-set-is-per-run-and-not-carried-by-continue-as-new-separate-known-limitation)).
- **Shard acquisition uses `context.Background()`** (`shard/context.go:1136,1806`): one blocked
  `UpdateShard` freezes a host's shard-acquire loop. Seen as a shard unavailable for hours when
  killed processes left Postgres locks held; fixed on the harness side with Postgres timeouts
  ([analysis](../antithesis/scratchbook/rca/shard-unavailable-after-faults.md)).

## Evidence for each finding

Logs-explorer links could not be built: the logs explorer needs each run's session ID, which the
Antithesis API did not return for these runs. Each row gives the run and the moment instead.

| Finding | Run | Failing moment | Log | Local log |
|---|---|---|---|---|
| [Retried reset applied twice](speedrun-report.md#retried-reset-applied-twice) | [`41bf0672`][run-41bf0672] | input_hash `4272893864207768579`, vtime `474.5656898911111` | not built (no session ID) | [logs/fixverify-reset-dup/](../antithesis/scratchbook/logs/fixverify-reset-dup/) |
| [Signal lost on reset](speedrun-report.md#acked-signal-lost-on-reset) | `75b0745b` (master, cancelled, no report); hunt example [`f04f3907`][run-f04f3907] | `-318482339591177353` / `34.675`; hunt `-7563240092365204529` / `18.993259294657037` | not built | [logs/iter7m/](../antithesis/scratchbook/logs/iter7m/) |
| [Stale history read](speedrun-report.md#stale-history-after-shard-steal) | [`41bf0672`][run-41bf0672]; hunt example [`f04f3907`][run-f04f3907] | `-2175461847364005768` / about `96.5063`; hunt `2114059415789563007` / `64.21569897816516` | not built | [logs/stale-v145/](../antithesis/scratchbook/logs/stale-v145/), [logs/hist-close/](../antithesis/scratchbook/logs/hist-close/) |
| [Retried signal applied twice](speedrun-report.md#retried-signal-duplicated-after-reset) | `89a272ed` (cancelled, no report); hunt example [`f04f3907`][run-f04f3907] | `-5028251366505688799` / `70.4274695077911`; hunt `5673641315105240353` / `72.79605940612964` | not built | [logs/reset-dup/](../antithesis/scratchbook/logs/reset-dup/), [logs/query-dup/](../antithesis/scratchbook/logs/query-dup/) |
| [Go-client stall](speedrun-report.md#retried-decision-heartbeat-stalls-workflow) | `6e868ff1` (cancelled, no report) | `5337532354675634409` / `401.2113032224588` | not built | [logs/race-stuck/](../antithesis/scratchbook/logs/race-stuck/) |
| [Shutdown during startup panics](speedrun-report.md#sigterm-during-startup-panics) | `89a272ed` (cancelled); hunt example [`f04f3907`][run-f04f3907] | hunt `-436440139682652554` / `30.29681889573112` | not built | [logs/node-exits/](../antithesis/scratchbook/logs/node-exits/) |

## Method notes

- **False positives caught:** about 15, all fixed in the driver or harness before the hunts
  ([speedrun-log.md](../antithesis/scratchbook/speedrun-log.md)). The most instructive:
  - [a signal "lost" during a reset](../antithesis/scratchbook/rca/signal-lost-during-reset.md):
    the driver accepted the result of the reset's terminated base run;
  - [results read from the wrong run](../antithesis/scratchbook/rca/result-from-wrong-run.md): a
    pending-reset counter leaked when a reset was never sent;
  - [straggler workflows that had in fact closed](../antithesis/scratchbook/rca/fixverify-stragglers.md):
    a timed-out reset that never committed was recorded as a reset base.
  Others: resets counted only on success; reused-ID reads hitting the previous run; the eventually
  phase not recording its finish signal ([duplicate-start-accepted.md](../antithesis/scratchbook/rca/duplicate-start-accepted.md));
  continue-as-new describe; cancel with an activity in flight; client SIGTERM before its handler;
  no server supervisor (startup fail-fast left nodes dead); workers not restarted after a client
  restart; the recovery probe without retries; orphaned Postgres locks.
- **Iteration runs to reach green:** 6 on v1.4.5 (all cancelled once a false positive was found),
  alongside 7 on master and 3 on the pre-fix build. The hunts used a frozen driver.
- **Fault types used:** Antithesis `high_availability` on every run, all containers except the
  driver (fault exclusion `^driver$`), Postgres included; standard performance tier.
- **Fault types needed but unavailable:** none identified.
- **Harness gaps:** the history checker did not record which timer a `TimerCanceled` referred to, so
  the known bug was undetectable until fixed (before the third known-bug run). The logs explorer
  links can't be built without session IDs. Reads cannot tell which frontend served them (the
  client round-robins), which slowed the stale-read analysis.
- **Recurring warnings:** "a cadence server process crashes" fired about 1.4k times per hunt, all
  during stops (the shutdown finding), which can hide a different crash; client container exits
  with code 2 in every hunt ([above](#client-container-exits)).
- **Runs cancelled early:** every iteration run, as soon as it showed a test-code problem; three
  runs (`fc9ee8d0`, `ad86b559`, `5a1ac4c2`) before they used any compute. The long hunts are marked
  "incomplete" by Antithesis although they used their full 600-hour budget.
- **Stale or inconsistent working files:** [positive-control.md](../antithesis/scratchbook/positive-control.md)
  still says "research only, nothing built" (the check did run and reproduce);
  [stale-history-v145.md](../antithesis/scratchbook/rca/stale-history-v145.md) classifies the
  finding "suspect", superseded by the adversarial review; [violations.tsv](../antithesis/scratchbook/violations.tsv)
  still says "pending adversarial review" for all six. speedrun-log.md timestamps after 19:30 ran
  about 30 minutes ahead of real UTC.
- **Patches and unit tests:** the lost-signal patch adds two unit tests that fail without it, and the
  reset, execution and engine packages pass; the v2 dedup patch passes the reset package. No unit
  test result is recorded for the first dedup patch alone or for the shutdown nil guards.
- **Cross-cluster replication:** a two-cluster replication harness was built and validated locally
  on both versions ([antithesis-xdc/README.md](../antithesis-xdc/README.md)) but not run in
  Antithesis before the deadline.
- **Document review:** these documents had a link check only; the fresh-reviewer truth check was not
  run yet.

## Content hooks

- "One reset call, two resets: a lost reply and the Go client's standard retry make Cadence reset
  the same workflow twice and silently drop the signals in between, a regression hiding since
  April 2024."
- "Completed, then running, 0.6 ms later": the stale-read timeline after a node rejoin, with no
  faults injected. Candidate diagram: two hosts both holding shard 3, the frontends split two to one.
- Candidate diagram: the buffered-signal race (decision in flight → signal buffered and acked →
  reset terminates the run → flush into the dead run → reapply misses it).
- Angle: exactly-once delivery is only as strong as its idempotency key; three findings are about
  request IDs that don't survive a reset.
- Angle: the known-bug check re-found a real Cadence bug (PR #8595) about a minute into a timeline,
  and the same test passed on the fixed release.

## Still running at the deadline

None: every run had finished before the reports were written.

## Open questions

- The [unconfirmed findings](#unconfirmed-findings) from the hunts: triage is in progress; the report
  promises an update.
- The fix-check residuals (`rca/fixverify-residuals.md`, not yet written; raw evidence in
  [logs/fixverify-residuals/](../antithesis/scratchbook/logs/fixverify-residuals/)).
- The v2 dedup patch (retried reset) has not been run end to end.
- No patch for the stale read; no patch for the Go client; whether a newer Go client fixes the stall.
- Upstream disclosure: who reports, and when.
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
| Build provenance | the [report's appendix](speedrun-report.md#builds), plus [config-v1.4.5/](../antithesis/config-v1.4.5/), [config-v1.4.5-b/](../antithesis/config-v1.4.5-b/), [config-prefix/](../antithesis/config-prefix/), [config-fixverify/](../antithesis/config-fixverify/), [config-fixverify2/](../antithesis/config-fixverify2/) |

[c-master]: https://github.com/cadence-workflow/cadence/commit/572e7e2c786999df887f73ae46e59c72897a223b
[run-1a0d055c]: https://orbitinghail.antithesis.com/report/xN9Vj4U-36rrB2EGnmdrfioQ/QME7BrLqb1wMUqQZ_d7YrLxBhjYAnqTP7fAgJwKsiWQ.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDA0OjI1OjQ3LjYyNzEwODc2M1oiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiUU1FN0JyTHFiMXdNVXFRWl9kN1lyTHhCaGpZQW5xVFA3ZkFnSndLc2lXUS5odG1sIiwicmVwb3J0X2lkIjoieE45Vmo0VS0zNnJyQjJFR25tZHJmaW9RIn19favCyE4Utz3UgFQMlJMfMxrcal1_ucEel2g_f4YLh39_8wiydLxDlENLSsMIlTBGXI-s1LdcuIRGnqjm8s8CbAk
[run-41bf0672]: https://orbitinghail.antithesis.com/report/Ie4vOZxovWROfzayOSpNtShi/eLjnz7F_vDSB3n6eaOdMzCETUOyH1cFkim3NEWOkL8c.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE4VDIyOjQ4OjA2LjcyNjIyNTMyNFoiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiZUxqbno3Rl92RFNCM242ZWFPZE16Q0VUVU95SDFjRmtpbTNORVdPa0w4Yy5odG1sIiwicmVwb3J0X2lkIjoiSWU0dk9aeG92V1JPZnpheU9TcE50U2hpIn19fY73Z3acMhrO0ZzQOb-lafwX7od9KnJkzAZ2ve0NgdnhMnLUjQhh2GCLmMX42OqmG7mQRxDAW6a97al9AcRZTQs
[run-bba4d2f0]: https://orbitinghail.antithesis.com/report/mhssSs1i5EkMNSQHcVCPTzP9/Zrc1EgScZT43yY45AVfd0u4JP_Cg9AiF8TUs4qwzcIw.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDA5OjE4OjA5LjA2MDA2MDg0MFoiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiWnJjMUVnU2NaVDQzeVk0NUFWZmQwdTRKUF9DZzlBaUY4VFVzNHF3emNJdy5odG1sIiwicmVwb3J0X2lkIjoibWhzc1NzMWk1RWtNTlNRSGNWQ1BUelA5In19fRUBQvQ0iEjvyDFSQPGt83_js26ZyPXhhfdjAh7tx-njZmKtDic_lxU0jQ_VkPs_Hc_antm4HlZJV7nX8zr57g8
[run-f04f3907]: https://orbitinghail.antithesis.com/report/utHYY91d38r96H5gf-sYaMDe/vPClAGzQ3Uq2Kx0N6Wl0k3kHpcBDAfaTe5taQDtno8s.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDA5OjE3OjUwLjg0MjcxODU1NFoiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoidlBDbEFHelEzVXEyS3gwTjZXbDBrM2tIcGNCREFmYVRlNXRhUUR0bm84cy5odG1sIiwicmVwb3J0X2lkIjoidXRIWVk5MWQzOHI5Nkg1Z2Ytc1lhTURlIn19fVmwK1MIUiq4aD8_CS2kC16GH_eRoW1B7-PfkeakUA1GmOqmWR4jr3C7cB0V6Ce2SsjzSoQF9vtZVUWvi1ENqQU
