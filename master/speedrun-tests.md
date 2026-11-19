# How the tests work: Cadence under Antithesis

**Document version:** third edition, 2026-11-19, to match the third edition of the [report](speedrun-report.md). The tests are unchanged; the check "a workflow does not heartbeat decisions without progress" ([below](#checks-that-must-hold-every-time)) is the one that flagged the [Go-client stall](speedrun-report.md#retried-decision-heartbeat-stalls-workflow) again in run `37b22144`, which used the default 30-minute decision heartbeat timeout.

The workload behind the [Cadence master report](speedrun-report.md). It is one Go program, the driver ([driver/](../antithesis/driver/)), which uses the hegel property-based testing library to run randomized operations (rules) against Cadence through three client containers ([client/](../antithesis/client/)). Every outcome is checked against a model of what the driver sent and what Cadence acknowledged. The checks are Antithesis SDK assertions: `Always` (must hold every time), `Unreachable` (must never happen), and `Sometimes` / `Reachable` (an interesting case must happen at least once, which shows the test reached it).

## How a test run goes

1. **Setup** ([`startup`](#setup)). The driver waits until every client container answers, registers the `antithesis` domain, starts a workflow worker in each client, and runs one workflow end to end, so every Cadence service is serving. It then signals that setup is complete, and Antithesis starts injecting faults (fault injection).
2. **Rules under faults.** hegel draws rules at random and runs up to 6 at a time ([main.go:64-69][d-main63]): start workflows of five types, signal, query, finish, cancel, terminate and reset them, start a second run on an open ID, reuse a closed ID, read results and histories, and re-read closed runs. A rule never waits more than 2 s for a reply; a reply that arrives later is still checked when it arrives ([rpc.go][d-rpc]). Antithesis meanwhile kills, pauses and partitions the Cadence nodes, Postgres and the client containers.
3. **Eventually** ([`RuleEventually`](#ruleeventually)). Rarely (a 0.3% chance each time the rule is drawn), the driver stops faults, then checks that every client is served again, that a fresh workflow completes within 120 s, that every workflow it started closes, and that every closed run is listed in visibility. Then it ends the timeline.

The workflows themselves are in [client/workflows.go](../antithesis/client/workflows.go): counter ([`CounterWorkflow`][c-counter]: applies "add" signals, answers queries, continues-as-new every few signals, finishes on a "finish" signal), chain ([`ChainWorkflow`][c-chain]: activities with retries, heartbeats, parallel or local activities), timer ([`TimerWorkflow`][c-timer]), parent ([`ParentWorkflow`][c-parent]: child workflows) and race ([`RaceWorkflow`][c-race]: timers racing long local activities, with a 4 s decision timeout so the client must send decision heartbeats).

## The ideas the rules share

- **The model** ([model.go](../antithesis/driver/model.go)). The driver records every workflow it started (`WF`) with its input, the expected result it computed from that input, every signal it sent (`Sig`, each with a unique ID and value), every reset, cancel and terminate it asked for, and every run ID Cadence reported. All checks compare what Cadence returns against this record, when the reply arrives ([`startDone`][d-startdone], [`signalDone`][d-signaldone], [`resultDone`][d-resultdone], [`checkCounter`][d-checkcounter], [`checkHistory`][d-checkhistory]).
- **Unknown outcomes.** A call that timed out or failed in transit may or may not have taken effect. The client classifies each error ([`classify`][c-classify]): only errors that by contract mean "never took effect" (already started, not found, bad request and so on) are `failed`; everything else is `unknown`. The checks treat the two differently. An acknowledged signal must appear exactly once; a signal with an unknown outcome may appear at most once; a workflow whose start outcome is unknown may not exist.
- **Stateful clients.** Each client container is a long-lived process holding one Cadence Go client and one worker. The driver talks to it over a Unix socket with JSON-RPC ([rpc.go][d-rpc]; client side [`dispatch`][c-dispatch]). When a client container restarts, its outstanding calls become unknown and the driver reconnects. Clients send requests to the three frontends in turn.
- **Reset handling.** A reset terminates the old run and starts a new one, so workflows that were reset report signal loss and duplicates through their own checks ("an acknowledged signal survives a reset of its workflow", "a signal is applied at most once in a workflow that was reset"). This keeps each known bug from hiding other failures. Results read from a reset's terminated base run are ignored and read again ([model.go:217-221][d-resetbase]).
- **Randomness.** Every choice (workflow type, inputs, which workflow, which client, reset point, reuse policy) is drawn from hegel (`hegel.Draw`), which Antithesis controls, so a failing timeline can be replayed.

## Rules

| Rule | What it does | Code |
|---|---|---|
| <a id="rulestart"></a>`RuleStart` | Starts a workflow of a random type (counter twice as likely), sometimes as signal-with-start, with a random reuse policy; records the expected result | [main.go:139][d-rulestart] |
| <a id="rulesignal"></a>`RuleSignal` | Sends a uniquely numbered "add" signal to an open counter (sometimes one that also runs an activity) | [main.go:217][d-rulesignal] |
| <a id="rulefinish"></a>`RuleFinish` | Sends "finish" to a counter so it completes | [main.go:238][d-rulefinish] |
| <a id="rulequery"></a>`RuleQuery` | Queries a counter's applied signals: no duplicates, nothing unsent | [main.go:253][d-rulequery] |
| <a id="rulecancel"></a>`RuleCancel` | Requests cancellation of an open workflow | [main.go:302][d-rulecancel] |
| <a id="ruleterminate"></a>`RuleTerminate` | Terminates an open workflow | [main.go:317][d-ruleterminate] |
| <a id="rulereset"></a>`RuleReset` | Resets an open counter to a random completed decision of its current run (signals after the point are reapplied, the Cadence default); at most one reset per workflow in flight | [main.go:332][d-rulereset], client [`reset`][c-reset] |
| <a id="ruleduplicatestart"></a>`RuleDuplicateStart` | Starts the ID of an open workflow again; Cadence must reject it | [main.go:373][d-ruleduplicatestart] |
| <a id="ruleresult"></a>`RuleResult` | Reads the result of a workflow that should be finishing, checks it, then reads and checks the closed run's history | [main.go:421][d-ruleresult] |
| <a id="ruledescribeclosed"></a>`RuleDescribeClosed` | Re-reads a closed run: it must still be closed with the same status | [main.go:488][d-ruledescribeclosed] |
| <a id="rulereuseclosed"></a>`RuleReuseClosed` | Starts a new run on a closed counter's ID with a random reuse policy; the outcome must follow the policy | [main.go:687][d-rulereuseclosed] |
| <a id="ruleeventually"></a>`RuleEventually` | Rarely: stops faults and runs the recovery, completion and visibility checks ([`eventually`][d-eventually]), then ends the timeline | [main.go:513][d-ruleeventually] |

<a id="setup"></a>**Setup:** [`startup`][d-startup] (health of each client, domain registration, workers, one end-to-end workflow), then `SetupComplete`. The server containers run under a supervisor ([entrypoint.sh](../antithesis/server/entrypoint.sh)) that restarts `cadence-server` after it exits and reports a Go panic as an assertion.

## Checks and the guarantees they test

Guarantees are as in the [report](speedrun-report.md#guarantees). Each line links the code that makes the check.

### Checks that must hold every time

| Check (assertion message) | Type | Guarantee | Where |
|---|---|---|---|
| a second start of an open workflow ID is rejected | Always | [Guarantee 1](speedrun-report.md#guarantee-1) | [main.go:409][a-m409] |
| a start on a closed workflow ID follows its reuse policy | Always | [Guarantee 1](speedrun-report.md#guarantee-1) | [main.go:719][a-m719], [main.go:723][a-m723] |
| an acknowledged signal is applied exactly once | Always | [Guarantee 2](speedrun-report.md#guarantee-2) | [model.go:166][a-o166] (late ack), [model.go:321][a-o321] (duplicates), [model.go:339][a-o339] (missing) |
| an acknowledged signal survives a reset of its workflow | Always | [Guarantee 2](speedrun-report.md#guarantee-2) | [model.go:337][a-o337] |
| a signal is applied at most once in a workflow that was reset | Always | [Guarantee 2](speedrun-report.md#guarantee-2), [Guarantee 3](speedrun-report.md#guarantee-3) | [main.go:293][a-m293] (query), [model.go:319][a-o319] (result), [model.go:419][a-o419] (history) |
| a query shows each delivered signal once | Always | [Guarantee 2](speedrun-report.md#guarantee-2) | [main.go:295][a-m295] |
| a workflow only applies signals that clients sent | Always | [Guarantee 2](speedrun-report.md#guarantee-2) | [model.go:323][a-o323] |
| signals are applied in the order they were acknowledged | Always | [Guarantee 2](speedrun-report.md#guarantee-2) | [model.go:359][a-o359] |
| a workflow history is well formed | Always | [Guarantee 3](speedrun-report.md#guarantee-3) | [model.go:395][a-o395] (empty history), [model.go:417][a-o417]; the individual checks are in [`historyChecks`][d-historychecks] |
| a history read after a workflow closed includes its close event | Always | [Guarantee 3](speedrun-report.md#guarantee-3) | [model.go:410][a-o410] |
| a completed workflow returns the result its inputs determine | Always | [Guarantee 4](speedrun-report.md#guarantee-4) | [model.go:261][a-o261] (chain, parent), [model.go:276][a-o276] (race), [model.go:362][a-o362] (counter) |
| a workflow ends only as completed, or as canceled or terminated on request | Always | [Guarantee 4](speedrun-report.md#guarantee-4) | [model.go:240][a-o240] |
| a closed workflow keeps its outcome | Always | [Guarantee 4](speedrun-report.md#guarantee-4) | [model.go:225][a-o225] (result read again), [main.go:507][a-m507] (describe) |
| a timer never fires before its duration | AlwaysGreaterThanOrEqualTo | [Guarantee 4](speedrun-report.md#guarantee-4) | [model.go:270][a-o270] |
| a workflow does not heartbeat decisions without progress | AlwaysLessThan (20 empty decisions in a row) | [Guarantee 4](speedrun-report.md#guarantee-4) | [model.go:415][a-o415] |
| every started workflow closes once faults stop | Always | [Guarantee 4](speedrun-report.md#guarantee-4) | [main.go:646][a-m646] |
| every client is served again once faults stop | Always | [Guarantee 5](speedrun-report.md#guarantee-5) | [main.go:553][a-m553] |
| a new workflow completes within 2 minutes once faults stop | Always | [Guarantee 5](speedrun-report.md#guarantee-5) | [main.go:579][a-m579] |
| a closed workflow appears in visibility once faults stop | Always | [Guarantee 6](speedrun-report.md#guarantee-6) | [main.go:679][a-m679] |
| a workflow result can't be decoded | Unreachable | test sanity | [model.go:202][a-o202], [model.go:287][a-o287] |
| a client can't run a request | Unreachable | test sanity | [rpc.go:115][a-r115] |
| a cadence server process crashes | Unreachable | crash safety (findings: [shutdown panic](speedrun-report.md#sigterm-during-startup-panics), [connection-close panic](speedrun-report.md#sql-admin-conn-close-panic)) | [entrypoint.sh `report_crash`](../antithesis/server/entrypoint.sh) |

"A workflow history is well formed" checks, per closed run: event IDs run 1..N, the first event is `WorkflowExecutionStarted`, a close event is last and (for a run reported closed) there is exactly one, each timer, activity and child resolves at most once and only after it started, and no signal is recorded twice ([model.go:434-497][d-historychecks]). A missing close event is reported separately, as "a history read after a workflow closed includes its close event".

### Checks that an interesting case happened

| Check (assertion message) | Type | What it shows | Where |
|---|---|---|---|
| the workload starts | Reachable | setup finished and rules ran | [main.go:59][a-m59] |
| a start returns an unknown outcome | Sometimes | faults made a start ambiguous | [main.go:205][a-m205] |
| a query sees applied signals | Sometimes | queries see real state | [main.go:297][a-m297] |
| a workflow reset succeeds | Sometimes | resets happen | [main.go:362][a-m362] |
| a duplicate start is rejected as already started | Sometimes | the duplicate-start check is exercised | [main.go:411][a-m411] |
| a closed workflow ID is reused | Sometimes | reuse is exercised | [main.go:720][a-m720] |
| a reuse policy rejects a start on a closed workflow ID | Sometimes | rejection is exercised | [main.go:724][a-m724] |
| a signal is acknowledged | Sometimes | signals get through | [model.go:162][a-o162] |
| a cancel request closes a workflow as canceled | Sometimes | cancel is exercised | [model.go:247][a-o247] |
| a terminate request closes a workflow | Sometimes | terminate is exercised | [model.go:249][a-o249] |
| a parent workflow completes with all its children | Sometimes | child workflows complete | [model.go:263][a-o263] |
| an activity workflow completes | Sometimes | activities complete | [model.go:265][a-o265] |
| a race workflow cancels a timer | Sometimes | timers race local activities (the known bug's trigger) | [model.go:278][a-o278] |
| a race workflow cancels timers in consecutive rounds | Sometimes | same, repeatedly | [model.go:279][a-o279] |
| a signal with an unknown outcome is applied | Sometimes | ambiguous signals are exercised | [model.go:341][a-o341] |
| a signal workflow continues as new and completes | Sometimes | continue-as-new is exercised | [model.go:364][a-o364] |
| a reset workflow completes | Sometimes | reset workflows reach the result check | [model.go:365][a-o365] |
| a history contains a canceled timer | Sometimes | the timer-resolution check is exercised | [model.go:423][a-o423] |
| a client restarts | Reachable | client container restarts happen | [rpc.go:144][a-r144] |

[d-main63]: ../antithesis/driver/main.go#L64-L69
[d-rpc]: ../antithesis/driver/rpc.go
[d-startup]: ../antithesis/driver/main.go#L98
[d-eventually]: ../antithesis/driver/main.go#L522
[d-startdone]: ../antithesis/driver/model.go#L126
[d-signaldone]: ../antithesis/driver/model.go#L155
[d-resultdone]: ../antithesis/driver/model.go#L196
[d-resetbase]: ../antithesis/driver/model.go#L217-L221
[d-checkcounter]: ../antithesis/driver/model.go#L284
[d-checkhistory]: ../antithesis/driver/model.go#L392
[d-historychecks]: ../antithesis/driver/model.go#L434-L497
[d-rulestart]: ../antithesis/driver/main.go#L139
[d-rulesignal]: ../antithesis/driver/main.go#L217
[d-rulefinish]: ../antithesis/driver/main.go#L238
[d-rulequery]: ../antithesis/driver/main.go#L253
[d-rulecancel]: ../antithesis/driver/main.go#L302
[d-ruleterminate]: ../antithesis/driver/main.go#L317
[d-rulereset]: ../antithesis/driver/main.go#L332
[d-ruleduplicatestart]: ../antithesis/driver/main.go#L373
[d-ruleresult]: ../antithesis/driver/main.go#L421
[d-ruledescribeclosed]: ../antithesis/driver/main.go#L488
[d-ruleeventually]: ../antithesis/driver/main.go#L513
[d-rulereuseclosed]: ../antithesis/driver/main.go#L687
[c-classify]: ../antithesis/client/main.go#L138
[c-dispatch]: ../antithesis/client/main.go#L263
[c-reset]: ../antithesis/client/main.go#L591
[c-counter]: ../antithesis/client/workflows.go#L67
[c-chain]: ../antithesis/client/workflows.go#L155
[c-timer]: ../antithesis/client/workflows.go#L233
[c-parent]: ../antithesis/client/workflows.go#L261
[c-race]: ../antithesis/client/workflows.go#L304
[a-m59]: ../antithesis/driver/main.go#L59
[a-m205]: ../antithesis/driver/main.go#L205
[a-m293]: ../antithesis/driver/main.go#L293
[a-m295]: ../antithesis/driver/main.go#L295
[a-m297]: ../antithesis/driver/main.go#L297
[a-m362]: ../antithesis/driver/main.go#L362
[a-m409]: ../antithesis/driver/main.go#L409
[a-m411]: ../antithesis/driver/main.go#L411
[a-m507]: ../antithesis/driver/main.go#L507
[a-m553]: ../antithesis/driver/main.go#L553
[a-m579]: ../antithesis/driver/main.go#L579
[a-m646]: ../antithesis/driver/main.go#L646
[a-m679]: ../antithesis/driver/main.go#L679
[a-m719]: ../antithesis/driver/main.go#L719
[a-m720]: ../antithesis/driver/main.go#L720
[a-m723]: ../antithesis/driver/main.go#L723
[a-m724]: ../antithesis/driver/main.go#L724
[a-o162]: ../antithesis/driver/model.go#L162
[a-o166]: ../antithesis/driver/model.go#L166
[a-o202]: ../antithesis/driver/model.go#L202
[a-o225]: ../antithesis/driver/model.go#L225
[a-o240]: ../antithesis/driver/model.go#L240
[a-o247]: ../antithesis/driver/model.go#L247
[a-o249]: ../antithesis/driver/model.go#L249
[a-o261]: ../antithesis/driver/model.go#L261
[a-o263]: ../antithesis/driver/model.go#L263
[a-o265]: ../antithesis/driver/model.go#L265
[a-o270]: ../antithesis/driver/model.go#L270
[a-o276]: ../antithesis/driver/model.go#L276
[a-o278]: ../antithesis/driver/model.go#L278
[a-o279]: ../antithesis/driver/model.go#L279
[a-o287]: ../antithesis/driver/model.go#L287
[a-o319]: ../antithesis/driver/model.go#L319
[a-o321]: ../antithesis/driver/model.go#L321
[a-o323]: ../antithesis/driver/model.go#L323
[a-o337]: ../antithesis/driver/model.go#L337
[a-o339]: ../antithesis/driver/model.go#L339
[a-o341]: ../antithesis/driver/model.go#L341
[a-o359]: ../antithesis/driver/model.go#L359
[a-o362]: ../antithesis/driver/model.go#L362
[a-o364]: ../antithesis/driver/model.go#L364
[a-o365]: ../antithesis/driver/model.go#L365
[a-o395]: ../antithesis/driver/model.go#L395
[a-o410]: ../antithesis/driver/model.go#L410
[a-o415]: ../antithesis/driver/model.go#L415
[a-o417]: ../antithesis/driver/model.go#L417
[a-o419]: ../antithesis/driver/model.go#L419
[a-o423]: ../antithesis/driver/model.go#L423
[a-r115]: ../antithesis/driver/rpc.go#L115
[a-r144]: ../antithesis/driver/rpc.go#L144
