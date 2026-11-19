# Briefing: Uber · Cadence master

<!-- Internal: for the customer-facing engineer who will turn this into a demo, a blog post or a
presentation. Never share this file with the customer. -->

**Status:** PARTIAL: written after the deadline; hunt failures still being triaged (first edition; an update will follow once these are triaged)
**Dates:** 2026-11-18T18:52Z → deadline 2026-11-19T14:00Z (09:00 America/New_York); written 2026-11-19 ~15:10Z, after the long runs finished · **Skill:** speedrun, two versions (v1.4.5 and master) in one workspace
**Budget used (both versions together):** 26 runs launched, 22 of which ran (4 were cancelled before they started): 13 test iteration (6 v1.4.5, 7 master), 4 long runs (2 per version), 4 known-bug check, 5 fix check · 3265 / 5000 compute hours ([usage.txt](../antithesis/scratchbook/runs/usage.txt); debugging sessions extra) · elapsed about 14.5 h to the end of the long runs (documents written about 20 h after the start)
**Customer report:** [speedrun-report.md](speedrun-report.md) · **Tests:** [speedrun-tests.md](speedrun-tests.md)

## The short version

The results are strong on the reset path. Three server findings break acknowledged-signal delivery around `ResetWorkflowExecution`, all present on master, all with default configuration, and each survived adversarial review. Lead with [a reset retried by the client is applied a second time](speedrun-report.md#retried-reset-applied-twice): a regression since #5821 (April 2024), a one-line fix, and it loses acknowledged signals. Pair it with [an acknowledged signal lost when a reset races an in-flight decision](speedrun-report.md#acked-signal-lost-on-reset), which needs no faults at all, and with the [known-bug check](speedrun-report.md#known-bug-check), which reproduced PR #8595 cleanly (201 examples). The caveats not to forget: **no fix is verified end to end** (the fix-check run still failed the same checks, being analyzed), **many long-run failures are still untriaged** (listed in the report as under investigation, not as findings), and the per-check counts for master run [`3e9720ca`][r-3e97] were unavailable (API errors). Two findings were seen only once ([reset applied twice](speedrun-report.md#retried-reset-applied-twice) and the [Go-client stall](speedrun-report.md#retried-decision-heartbeat-stalls-workflow)); both have deterministic code paths confirmed by reading the code.

## Sharing the report

- **Safe to share as is:** [speedrun-report.md](speedrun-report.md) and [speedrun-tests.md](speedrun-tests.md). Their relative links point into `../antithesis/`, so share the directory tree with them, or at least these files:
  - root causes: [signal-lost-at-reset-master.md](../antithesis/scratchbook/rca/signal-lost-at-reset-master.md), [acked-signal-lost-on-reset-fix.md](../antithesis/scratchbook/rca/acked-signal-lost-on-reset-fix.md), [fixverify-reset-dup.md](../antithesis/scratchbook/rca/fixverify-reset-dup.md), [reset-duplicates-signal.md](../antithesis/scratchbook/rca/reset-duplicates-signal.md), [query-shows-duplicate-signal.md](../antithesis/scratchbook/rca/query-shows-duplicate-signal.md), [stale-history-v145.md](../antithesis/scratchbook/rca/stale-history-v145.md), [history-missing-close-event.md](../antithesis/scratchbook/rca/history-missing-close-event.md), [node-exits.md](../antithesis/scratchbook/rca/node-exits.md), [race-workflow-stuck.md](../antithesis/scratchbook/rca/race-workflow-stuck.md), [shard-unavailable-after-faults.md](../antithesis/scratchbook/rca/shard-unavailable-after-faults.md);
  - adversarial reviews: all six in [adversarial/](../antithesis/scratchbook/adversarial/);
  - patches: [fixes/](../antithesis/fixes/) (four files);
  - [guarantees.md](../antithesis/scratchbook/guarantees.md), the harness ([driver/](../antithesis/driver/), [client/](../antithesis/client/), [server/](../antithesis/server/), [config-master/](../antithesis/config-master/), [config-master-b/](../antithesis/config-master-b/)) and the early-results log [stream-hunt1-master.log](../antithesis/scratchbook/logs/stream-hunt1-master.log).
  - The root-cause and review files are written for engineers and mention internal file paths and a few internal labels; skim them before sending.
- **The report's run links carry access tokens.** Anyone with one can open that run's report, which shows every failing property, including the untriaged ones. Remove them before publishing anything publicly.
- **Not for the customer:** this briefing and the rest of the scratchbook (speedrun-log.md, report-facts.md, runs/, logs/). The report lists the untriaged long-run failures under "Still under investigation" without calling them findings, because the run reports show them anyway. The logs-explorer links need a tenant login: fine to leave in, but they won't open for the customer.
- **Disclosure:** none of the six findings has been reported upstream (the user decided at the start: no GitHub filing). Before any public content, coordinate disclosure with the Cadence maintainers or let Uber file them. The root-cause files have no ready-made issue text yet; [node-exits.md](../antithesis/scratchbook/rca/node-exits.md#recommendations) suggests a title for the shutdown panic.
- **Licence:** Cadence is Apache License 2.0 ([LICENSE](../src/cadence-v1.4.5/LICENSE); the same on master), not MIT. Apache 2.0 has no clause restricting publication of benchmarks or test results, so the results may be shared and published; patches contributed upstream would go under Apache 2.0 and Cadence's contribution terms. The Go client is also open source.

## How confident we are

Counts are failing examples of the finding's dedicated check in the long runs (master [`c26baba5`][r-c26b]; v1.4.5 `f04f3907` / `bba4d2f0`, see the v1.4.5 documents), the fix check [`1a0d055c`][r-1a0d] and the known-bug build [`ccd99ed4`][r-ccd9], from [report-facts.md](../antithesis/scratchbook/report-facts.md) and [runs/props-*.txt](../antithesis/scratchbook/runs/). A dedicated check counts every failure of its kind; only a few examples per finding were traced by hand.

| Finding | Guarantee broken | Failing examples / runs | Adversarial verdict | Root cause | Fix check |
|---|---|---|---|---|---|
| [signal lost on reset](speedrun-report.md#acked-signal-lost-on-reset) | [Guarantee 2](speedrun-report.md#guarantee-2) | master-b 1.0k; master: failed, count unavailable; v1.4.5 666 / 787; before the fix 138; root-caused once (master `75b0745b`) | stands ([review](../antithesis/scratchbook/adversarial/acked-signal-lost-on-reset.md)) | [new](../antithesis/scratchbook/rca/signal-lost-at-reset-master.md#root-cause-code) | unit test passes; [`1a0d055c`][r-1a0d] still 373 failures, being analyzed |
| [reset applied twice](speedrun-report.md#retried-reset-applied-twice) | [Guarantee 2](speedrun-report.md#guarantee-2) | 1 workflow in 1 run ([`41bf0672`][r-41bf]); no dedicated check | stands ([review](../antithesis/scratchbook/adversarial/retried-reset-applied-twice.md)) | [new; regression from #5821](../antithesis/scratchbook/rca/fixverify-reset-dup.md#gap-a-reset-retry-dedup-is-dead-code-new-bug) | v2 patch passes `go test ./service/history/reset/`; never in a fix-check run |
| [signal duplicated after reset](speedrun-report.md#retried-signal-duplicated-after-reset) | [Guarantee 2](speedrun-report.md#guarantee-2), [Guarantee 3](speedrun-report.md#guarantee-3) | master-b 701; master: failed, count unavailable; v1.4.5 196 / 280; before the fix 73 | stands ([review](../antithesis/scratchbook/adversarial/retried-signal-duplicated-after-reset.md)) | [new](../antithesis/scratchbook/rca/reset-duplicates-signal.md#code) | v1 patch in [`1a0d055c`][r-1a0d]: still 168 failures, being analyzed |
| [stale history after a shard steal](speedrun-report.md#stale-history-after-shard-steal) | [Guarantee 3](speedrun-report.md#guarantee-3) | master-b 116; v1.4.5 68 / 88; fix check 54; root-caused in 3 workflows, 2 runs | stands ([review](../antithesis/scratchbook/adversarial/stale-history-after-shard-steal.md)) | [new](../antithesis/scratchbook/rca/stale-history-v145.md#root-cause-srccadence-v145) | no patch |
| [shutdown panic during startup](speedrun-report.md#sigterm-during-startup-panics) | none (crash safety) | master-b 1.1k; master: failed, count unavailable; v1.4.5 1.4k / 1.4k; before the fix 514 | stands ([review](../antithesis/scratchbook/adversarial/sigterm-during-startup-panics.md)) | [new](../antithesis/scratchbook/rca/node-exits.md#history-preparetostop-nil-controller--real-low-severity-v145-and-master) | patch in [`1a0d055c`][r-1a0d]: still 119 crashes, being analyzed |
| [Go client: stalled workflow](speedrun-report.md#retried-decision-heartbeat-stalls-workflow) | [Guarantee 4](speedrun-report.md#guarantee-4), [Guarantee 5](speedrun-report.md#guarantee-5) | 1 workflow in 1 v1.4.5 run (`6e868ff1`); the stall detector never fired in the long runs | stands ([review](../antithesis/scratchbook/adversarial/retried-decision-heartbeat-stalls-workflow.md)) | [new; newer client releases not checked](../antithesis/scratchbook/rca/race-workflow-stuck.md#root-cause-go-client-gouberorgcadence-v140) | no patch |

**Caveats:**
- The root-cause analysis was blinded procedurally: the model may have seen a known bug in its training data.
- The dedicated checks count every failure of their kind, so a count is an upper bound on the finding's occurrences. "Survives a reset" and "at most once after a reset" each mix at least two mechanisms (the reset-retry finding feeds both).
- The fix-check runs are v1.4.5-based. Master has identical code on each finding's path (checked by `diff` of the files and `git apply --check` of all four patches on master).
- [Stale history](speedrun-report.md#stale-history-after-shard-steal): the checker asks for more than Cadence documents (no promise of monotonic reads across shard moves). The adversarial review kept it because the read contradicts a completed result 0.6 ms earlier, and it reproduced with no faults at all. Present it as a consistency gap, medium severity.
- [Go-client stall](speedrun-report.md#retried-decision-heartbeat-stalls-workflow): the assertion's 5-minute window is stricter than Cadence's 30-minute backstop. The defect (a dropped local-activity result, a no-progress loop) stands; the stall is bounded.
- [Shutdown panic](speedrun-report.md#sigterm-during-startup-panics): Antithesis stops nodes during startup far more often than production does; impact is low.

## Unconfirmed findings

Failures from the long runs whose cause is not yet established (no adversarial review yet). The report lists them under [Still under investigation](speedrun-report.md#still-under-investigation) as possible bugs or test-code issues, not as findings. Triage is in progress (`rca/hunt-*.md`, not written yet); raw material so far is in [logs/](../antithesis/scratchbook/logs/) (hunt-second-start, hunt-signal-order, hunt-unknown-signals, reuse-policy, timer-early, fixverify-residuals).

The specific doubt for all of them: about 15 earlier failures of these same checks turned out to be driver false positives (see [Method notes](#method-notes)), so a test-code cause is not ruled out. What would settle each: root-cause two or three examples per check with `snouty runs exec` histories, as for the findings, then an adversarial review.

Failing examples per run (master-b [`c26baba5`][r-c26b]; v1.4.5 `f04f3907` / `bba4d2f0`; fix check [`1a0d055c`][r-1a0d]; before the fix [`ccd99ed4`][r-ccd9]; master [`3e9720ca`][r-3e97] unavailable). Source: [runs/props-*.txt](../antithesis/scratchbook/runs/).

| Check | Guarantee | master-b | v1.4.5 | v1.4.5-b | fix check | before the fix |
|---|---|---|---|---|---|---|
| <a id="timer-early"></a>a timer never fires before its duration | [Guarantee 4](speedrun-report.md#guarantee-4) | 172 | 143 | 126 | 67 | 0 |
| <a id="unexpected-close"></a>a workflow ends only as completed, or as canceled or terminated on request | [Guarantee 4](speedrun-report.md#guarantee-4) | 42 | 199 | 187 | 0 | 0 |
| <a id="second-start-accepted"></a>a second start of an open workflow ID is rejected | [Guarantee 1](speedrun-report.md#guarantee-1) | 125 | 125 | 163 | 0 | 0 |
| <a id="reuse-policy"></a>a start on a closed workflow ID follows its reuse policy | [Guarantee 1](speedrun-report.md#guarantee-1) | 62 | 76 | 75 | 59 | 26 |
| <a id="signal-order"></a>signals are applied in the order they were acknowledged | [Guarantee 2](speedrun-report.md#guarantee-2) | 171 | 169 | 175 | 67 | 58 |
| <a id="unknown-signals"></a>a workflow only applies signals that clients sent | [Guarantee 2](speedrun-report.md#guarantee-2) | 447 | 364 | 421 | 420 | 102 |
| a completed workflow returns the result its inputs determine | [Guarantee 4](speedrun-report.md#guarantee-4) | 447 | 364 | 421 | 420 | 102 |
| <a id="closed-keeps-outcome"></a>a closed workflow keeps its outcome | [Guarantee 4](speedrun-report.md#guarantee-4) | 62 | 44 | 82 | 31 | 1 |
| <a id="liveness"></a>every client is served again once faults stop | [Guarantee 5](speedrun-report.md#guarantee-5) | 207 | 173 | 346 | 127 | 65 |
| every started workflow closes once faults stop | [Guarantee 4](speedrun-report.md#guarantee-4) | 258 | 230 | 255 | 154 | 122 |
| <a id="non-reset-signal"></a>an acknowledged signal is applied exactly once (no reset) | [Guarantee 2](speedrun-report.md#guarantee-2) | 284 | 177 | 216 | 83 | 70 |
| a query shows each delivered signal once | [Guarantee 2](speedrun-report.md#guarantee-2) | 180 | 186 | 228 | 149 | 54 |
| client container exit code 2 (client1+2+3) | harness | 22 | 94 | 21 | 9 | 9 |

- "Only applies signals that clients sent" and "returns the result its inputs determine" have identical counts in every run, so they likely share one cause (an unknown signal ID also breaks the counter's sum).
- The non-reset duplicates may include the continue-as-new dedup gap noted under [Leads](#leads).
- "A timer never fires before its duration" is 0 on the before-the-fix build but fails on both newer versions and the fix check. That is either a real regression or a change in how the driver read timer results; check the driver build each run used before calling it either.
- The client exits with code 2 are our own client program; an earlier false positive with the same exit code (SIGTERM before the handler) was fixed, so these need a fresh look.
- Fix-check residuals ("survives a reset" 373, "at most once after a reset" 168, crashes 119 in [`1a0d055c`][r-1a0d]): analysis in progress (`rca/fixverify-residuals.md`, not written yet; raw data in [logs/fixverify-residuals/](../antithesis/scratchbook/logs/fixverify-residuals/)). Until it lands, don't claim any fix works.

## Leads

- **Signal dedup is not carried across continue-as-new:** `SignalRequestedIDs` is not copied to the successor run ([`mutable_state_builder.go:1223`/`:1287`](../antithesis/scratchbook/rca/fixverify-reset-dup.md#gap-b-dedup-set-is-per-run-and-not-carried-by-continue-as-new-separate-known-limitation)). A client retry can duplicate a signal across continue-as-new with no reset. It contributed to the duplicate in the reset-retry case; not separately reproduced or reviewed. Possibly a design limitation rather than a bug.
- **Shard acquisition uses `context.Background()`** (`shard/context.go:1136,1806`): one blocked `UpdateShard` freezes a host's `acquireShards` loop ([shard-unavailable-after-faults.md](../antithesis/scratchbook/rca/shard-unavailable-after-faults.md#root-cause)). Seen together with orphaned Postgres locks, which we then removed with Postgres timeouts; not reproduced since.

## Evidence for each finding

Logs-explorer links need the run's session ID. It could be looked up only for completed runs; for cancelled runs and the long runs on master the lookup returned none, so those rows give the run and moment instead.

| Finding | Run | Failing moment | Log | Local log |
|---|---|---|---|---|
| [signal lost on reset](speedrun-report.md#acked-signal-lost-on-reset) | `75b0745b` (master, cancelled, no report) | input_hash `-318482339591177353`, vtime `34.675028239842504` | session ID unavailable | [logs/iter7m/ack.log](../antithesis/scratchbook/logs/iter7m/ack.log), [hist.json.txt](../antithesis/scratchbook/logs/iter7m/hist.json.txt) |
| same, long run | [`c26baba5`][r-c26b] | input_hash `3180257910619170844`, vtime `34.95771721797064` | session ID unavailable | none; spot-checked: counter-3 reset at 20:50:27.078, signals acknowledged around it; which signal went missing was not retrievable (API errors) |
| [reset applied twice](speedrun-report.md#retried-reset-applied-twice) | [`41bf0672`][r-41bf] | input_hash `4272893864207768579`, vtime `474.5656898911111` | [logs explorer][log-resetdup] | [logs/fixverify-reset-dup/](../antithesis/scratchbook/logs/fixverify-reset-dup/) |
| [signal duplicated after reset](speedrun-report.md#retried-signal-duplicated-after-reset) | `89a272ed` (v1.4.5, cancelled, no report) | input_hash `-5028251366505688799`, vtime `70.4274695077911` | session ID unavailable | [logs/reset-dup/](../antithesis/scratchbook/logs/reset-dup/), [logs/query-dup/](../antithesis/scratchbook/logs/query-dup/) |
| same, long run | [`c26baba5`][r-c26b] | input_hash `4569408548377247411`, vtime `63.16192661551759` | session ID unavailable | none (not analyzed) |
| [stale history](speedrun-report.md#stale-history-after-shard-steal) | [`41bf0672`][r-41bf] | input_hash `-2175461847364005768`, vtime `96.5063121241983` | [logs explorer][log-stale] | [logs/stale-v145/](../antithesis/scratchbook/logs/stale-v145/), [logs/hist-close/](../antithesis/scratchbook/logs/hist-close/) |
| same, long run | [`c26baba5`][r-c26b] | input_hash `3873568898649092660`, vtime `46.73148331954144` | session ID unavailable | none; spot-checked: "stolen shard" closes logged just before |
| [shutdown panic](speedrun-report.md#sigterm-during-startup-panics) | `02612d74` (master, cancelled, no report) | input_hash `-8467436622163236132`, vtime `334.5371497955639` | session ID unavailable | [logs/iter3m/crash.log](../antithesis/scratchbook/logs/iter3m/crash.log), [logs/node-exits/panics.txt](../antithesis/scratchbook/logs/node-exits/panics.txt) |
| same, long run | [`c26baba5`][r-c26b] | input_hash `5493115663274640094`, vtime `48.06544893165119` | session ID unavailable | none; spot-checked with `snouty runs logs`: same frontend stack (`collection.go:255` from `stopRatelimiters`) |
| [Go-client stall](speedrun-report.md#retried-decision-heartbeat-stalls-workflow) | `6e868ff1` (v1.4.5, cancelled, no report) | input_hash `5337532354675634409`, vtime `401.2113032224588` | session ID unavailable | [logs/race-stuck/](../antithesis/scratchbook/logs/race-stuck/) |
| known bug | [`ccd99ed4`][r-ccd9] | input_hash `-5653455983605853828`, vtime `62.99922230280936` | [logs explorer][log-known] | [logs/stream-prefix4.log](../antithesis/scratchbook/logs/stream-prefix4.log) |

## Method notes

- **False positives caught:** about 15, all fixed in the driver or harness before the long runs (listed in [speedrun-log.md](../antithesis/scratchbook/speedrun-log.md) and [report-facts.md](../antithesis/scratchbook/report-facts.md#false-positives-caught-all-fixed-in-the-driverharness-speedrun-logmd-lists-each)). The most instructive:
  - [a result read from a reset's base run](../antithesis/scratchbook/rca/signal-lost-during-reset.md) looked exactly like a lost signal; the driver now pins result reads to the current run and tracks reset bases;
  - [a timed-out, uncommitted reset recorded as a base](../antithesis/scratchbook/rca/fixverify-stragglers.md) produced "workflow never closes" stragglers;
  - [orphaned Postgres locks from killed servers](../antithesis/scratchbook/rca/shard-unavailable-after-faults.md) held shard rows for hours; fixed with production-like Postgres timeouts, not by excluding Postgres from faults.
- **Harness gap:** the history checker at first did not record which timer a `TimerCanceled` refers to, so the known bug was undetectable ([history-missing-close-event.md](../antithesis/scratchbook/rca/history-missing-close-event.md#harness-gap-the-known-bug-signature-8595-is-not-detectable)); fixed before the known-bug run that reproduced it.
- **Iteration runs to reach green:** 7 on master (5 that ran for more than a minute), 6 on v1.4.5; each was cancelled once its false positives were understood. The long-run driver was then frozen.
- **Fault types used:** Antithesis `high_availability` (kill, stop, pause, partitions, degraded links, CPU throttling, clock jitter) on everything but the driver, in every run.
- **Fault types needed but unavailable:** none identified. Cross-cluster replication was not exercised (see XDC below).
- **Recurring warnings:** the client containers' exit code 2 in every long run (table above).
- **API trouble at report time:** `snouty runs properties` for [`3e9720ca`][r-3e97] failed or timed out repeatedly, so its counts are missing ([props-3e9720ca.txt](../antithesis/scratchbook/runs/props-3e9720ca.txt)). The early stream ([stream-hunt1-master.log](../antithesis/scratchbook/logs/stream-hunt1-master.log)) shows which checks failed early in that run.
- **XDC:** a two-cluster replication harness was built and validated locally on both versions ([antithesis-xdc/README.md](../antithesis-xdc/README.md)) but not run in Antithesis before the deadline.
- **Clock note:** speedrun-log timestamps after 19:30Z ran about 30 minutes ahead of real UTC; the run table uses Antithesis usage times.
- **Document review:** these documents have not yet had the fresh-reviewer truth check.

## Content hooks

- "One API call, two resets": a retried `ResetWorkflowExecution` is committed twice because its dedup check has been dead code since a 2024 change; the fix is one line.
- "Acknowledged, then gone, with no faults": a signal acknowledged 8 ms before a reset finished was written into the run being terminated, and never seen by any run after it.
- "Completed, then running again 0.6 ms later": after a node rejoin, two of three frontends read a finished workflow from a host that had silently lost its shard.
- Candidate diagram: the buffered-signal timeline (signal buffered during a decision → reset reads next event ID → termination flushes the buffer → reapply misses it), from [signal-lost-at-reset-master.md](../antithesis/scratchbook/rca/signal-lost-at-reset-master.md#sequence-utc-2032xx-event-timestamps-and-task-ids-from-the-json-histories).
- Angle: resets are an operator's recovery tool, used exactly when clients are retrying, which is where all three signal bugs live.
- Angle for testing method: the known-bug check found a gap in our own checker before it found the bug, a good illustration of why we run it.

## Still running at the deadline

None. Every run had finished before the 14:00Z deadline (the long runs at about 09:24Z by usage time, with status "incomplete"; the last fix check at about 05:15Z). The documents were written after the deadline because the session was paused while the runs finished.

## Open questions

- Triage of the [unconfirmed findings](#unconfirmed-findings) and of the fix-check residuals; then the second edition of the report.
- Per-check counts for [`3e9720ca`][r-3e97], once the API answers.
- An end-to-end check of the v2 reset patch, and fix checks on master rather than v1.4.5.
- Whether newer Go client releases fix the decision-heartbeat retry.
- XDC (cross-cluster replication) runs.

## Artifacts

| What | Where |
|---|---|
| Customer report | [speedrun-report.md](speedrun-report.md) |
| How the tests work | [speedrun-tests.md](speedrun-tests.md) |
| Facts / log | [report-facts.md](../antithesis/scratchbook/report-facts.md), [speedrun-log.md](../antithesis/scratchbook/speedrun-log.md), [runs.tsv](../antithesis/scratchbook/runs.tsv), [violations.tsv](../antithesis/scratchbook/violations.tsv) |
| Guarantees, system | [guarantees.md](../antithesis/scratchbook/guarantees.md), [system-brief.md](../antithesis/scratchbook/system-brief.md), [positive-control.md](../antithesis/scratchbook/positive-control.md) |
| Harness | [README.md](../antithesis/README.md), [config-template/](../antithesis/config-template/), [server/](../antithesis/server/), [client/](../antithesis/client/), [driver/](../antithesis/driver/) |
| Findings | [adversarial/](../antithesis/scratchbook/adversarial/), [rca/](../antithesis/scratchbook/rca/), [fixes/](../antithesis/fixes/) |
| Runs | [runs/](../antithesis/scratchbook/runs/) (report links with tokens in [report-urls.tsv](../antithesis/scratchbook/runs/report-urls.tsv)); the full run table is in the [report's appendix](speedrun-report.md#runs) |
| Build provenance | the [report's appendix](speedrun-report.md#builds), plus [config-master/](../antithesis/config-master/), [config-master-b/](../antithesis/config-master-b/), [config-prefix/](../antithesis/config-prefix/), [config-fixverify/](../antithesis/config-fixverify/), [config-fixverify2/](../antithesis/config-fixverify2/) |

[r-3e97]: https://orbitinghail.antithesis.com/report/7hhEamcFv0jBPyIrsJWLmBsG/Jeb3agMIb7xHXvXT9nGWM_0NZ11M5mTMqfMjiaAbdGk.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDA5OjE4OjM5LjY0Mzg3MTU2M1oiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiSmViM2FnTUliN3hIWHZYVDluR1dNXzBOWjExTTVtVE1xZk1qaWFBYmRHay5odG1sIiwicmVwb3J0X2lkIjoiN2hoRWFtY0Z2MGpCUHlJcnNKV0xtQnNHIn19fZ1gkiwU1Kl8LZsAxyfNG66cLv07jhdY92GNtsGSJAI1SIU28W6K6kcS8AjgoNsNBipANRx7JKP41_2vPOY_dAY
[r-c26b]: https://orbitinghail.antithesis.com/report/wO_En085myHQmXR9je-NrxN_/WT_OG4On7fiNT5aGtjbH1FVwOWACRBMACZwaIMrfsCI.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDA5OjE5OjEwLjYxODM1NTkxNloiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiV1RfT0c0T243ZmlOVDVhR3RqYkgxRlZ3T1dBQ1JCTUFDWndhSU1yZnNDSS5odG1sIiwicmVwb3J0X2lkIjoid09fRW4wODVteUhRbVhSOWplLU5yeE5fIn19fZ4rU75H0q7pN8z0HJ2A211sZYt1M0VJPLrh4EJLDGOnDVBiAFIXO2_0959rLaOrdeRu-KAuVTRoe0v0ErcjMQY
[r-41bf]: https://orbitinghail.antithesis.com/report/Ie4vOZxovWROfzayOSpNtShi/eLjnz7F_vDSB3n6eaOdMzCETUOyH1cFkim3NEWOkL8c.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE4VDIyOjQ4OjA2LjcyNjIyNTMyNFoiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiZUxqbno3Rl92RFNCM242ZWFPZE16Q0VUVU95SDFjRmtpbTNORVdPa0w4Yy5odG1sIiwicmVwb3J0X2lkIjoiSWU0dk9aeG92V1JPZnpheU9TcE50U2hpIn19fY73Z3acMhrO0ZzQOb-lafwX7od9KnJkzAZ2ve0NgdnhMnLUjQhh2GCLmMX42OqmG7mQRxDAW6a97al9AcRZTQs
[r-ccd9]: https://orbitinghail.antithesis.com/report/399LZ1zr5hmULQAkyWMx_-B7/T3vpaYJs2OtponHSuMcZrtf4_4hvrkASDmQ8kqS3Sbs.html?auth=v2.public.eyJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiVDN2cGFZSnMyT3Rwb25IU3VNY1pydGY0XzRodnJrQVNEbVE4a3FTM1Nicy5odG1sIiwicmVwb3J0X2lkIjoiMzk5TFoxenI1aG1VTFFBa3lXTXhfLUI3In19LCJuYmYiOiIyMDI2LTExLTE4VDIyOjU4OjEzLjE0MTUyNjA4MFoifeOTb0xfLB2TQxBFfNkK2DuetkPWcT7r0qxpaiyICeTG5XIe1EFl6b8hck0OKLBHE6yBJzt3i08L2a8ps4C9qg8
[r-1a0d]: https://orbitinghail.antithesis.com/report/xN9Vj4U-36rrB2EGnmdrfioQ/QME7BrLqb1wMUqQZ_d7YrLxBhjYAnqTP7fAgJwKsiWQ.html?auth=v2.public.eyJuYmYiOiIyMDI2LTExLTE5VDA0OjI1OjQ3LjYyNzEwODc2M1oiLCJzY29wZSI6eyJSZXBvcnRTY29wZVYxIjp7ImFzc2V0IjoiUU1FN0JyTHFiMXdNVXFRWl9kN1lyTHhCaGpZQW5xVFA3ZkFnSndLc2lXUS5odG1sIiwicmVwb3J0X2lkIjoieE45Vmo0VS0zNnJyQjJFR25tZHJmaW9RIn19favCyE4Utz3UgFQMlJMfMxrcal1_ucEel2g_f4YLh39_8wiydLxDlENLSsMIlTBGXI-s1LdcuIRGnqjm8s8CbAk
[log-known]: https://orbitinghail.antithesis.com/search?search=v6veyJxIjp7Im4iOnsiciI6eyJoIjpbeyJoIjpbeyJjIjoibW9tZW50LnZ0aW1lIiwiZiI6ImdlbmVyYWwuY3VzdG9tIiwibyI6Im1hdGNoZXMiLCJ2IjoiNjIuOTk5MjIyMzAyODA5MzYifV0sIm8iOiJvciJ9LHsiaCI6W3siYyI6Im1vbWVudC5pbnB1dF9oYXNoIiwiZiI6ImdlbmVyYWwuY3VzdG9tIiwibyI6Im1hdGNoZXMiLCJ2IjoiLTU2NTM0NTU5ODM2MDU4NTM4MjgifV0sIm8iOiJvciJ9XSwibyI6ImFuZCJ9LCJ0Ijp7ImciOmZhbHNlLCJtIjoiIn0sInkiOiJub25lIn19LCJzIjoiZmQyMDVjMmI2NGJjNGM4OTA3NGQxY2ZjNjBjMTRjMWItNjctNiJ9
[log-resetdup]: https://orbitinghail.antithesis.com/search?search=v6veyJxIjp7Im4iOnsiciI6eyJoIjpbeyJoIjpbeyJjIjoibW9tZW50LnZ0aW1lIiwiZiI6ImdlbmVyYWwuY3VzdG9tIiwibyI6Im1hdGNoZXMiLCJ2IjoiNDc0LjU2NTY4OTg5MTExMTEifV0sIm8iOiJvciJ9LHsiaCI6W3siYyI6Im1vbWVudC5pbnB1dF9oYXNoIiwiZiI6ImdlbmVyYWwuY3VzdG9tIiwibyI6Im1hdGNoZXMiLCJ2IjoiNDI3Mjg5Mzg2NDIwNzc2ODU3OSJ9XSwibyI6Im9yIn1dLCJvIjoiYW5kIn0sInQiOnsiZyI6ZmFsc2UsIm0iOiIifSwieSI6Im5vbmUifX0sInMiOiJkNTdjMTgwZmFjMTAxNGFiM2M5MDRhNTkzYmIxZjZmZi02Ny02In0
[log-stale]: https://orbitinghail.antithesis.com/search?search=v6veyJxIjp7Im4iOnsiciI6eyJoIjpbeyJoIjpbeyJjIjoibW9tZW50LnZ0aW1lIiwiZiI6ImdlbmVyYWwuY3VzdG9tIiwibyI6Im1hdGNoZXMiLCJ2IjoiOTYuNTA2MzEyMTI0MTk4MyJ9XSwibyI6Im9yIn0seyJoIjpbeyJjIjoibW9tZW50LmlucHV0X2hhc2giLCJmIjoiZ2VuZXJhbC5jdXN0b20iLCJvIjoibWF0Y2hlcyIsInYiOiItMjE3NTQ2MTg0NzM2NDAwNTc2OCJ9XSwibyI6Im9yIn1dLCJvIjoiYW5kIn0sInQiOnsiZyI6ZmFsc2UsIm0iOiIifSwieSI6Im5vbmUifX0sInMiOiJkNTdjMTgwZmFjMTAxNGFiM2M5MDRhNTkzYmIxZjZmZi02Ny02In0
