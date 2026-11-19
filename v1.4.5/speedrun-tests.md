# How the tests work: Cadence v1.4.5

This document explains the test code (workload) used against Cadence v1.4.5, so its checks can be
judged without reading all of the code. It goes with the [test report](speedrun-report.md).

**Document version:** third edition, 2026-11-19, matching the third edition of the
[test report](speedrun-report.md). Its content is unchanged from the second edition.

The workload is a single long-running driver program written in Go with
[hegel](https://hegel.dev) (a property-based, stateful test library), not a set of separate test
commands. The driver holds a model of everything it asked Cadence to do, picks its next action at
random from a set of rules, and checks every reply against the model when the reply arrives.
Antithesis supplies the randomness behind every choice, so any failure can be replayed exactly.

## How a test run goes

1. **Setup.** Postgres starts, a one-shot container creates the Cadence schemas
   ([`setup-schema.sh`](../antithesis/server/setup-schema.sh)), and three Cadence nodes start under
   a small supervisor ([`entrypoint.sh`](../antithesis/server/entrypoint.sh)). The driver then
   waits until all three client containers answer, registers the test domain, starts a worker in
   each client, and runs one workflow end to end so every service is serving
   ([`startup`](../antithesis/driver/main.go#L98)). Only then does it tell Antithesis that setup is
   complete (`setup_complete`, [main.go:50](../antithesis/driver/main.go#L50)).
2. **Rules under faults.** Antithesis runs the only test command,
   [`first_start_workload`](../antithesis/driver/first_start_workload), which just tells the driver
   to begin. From then on the driver applies its [rules](#rules) up to six at a time while
   Antithesis injects faults (kills, stops, pauses, network partitions, slow networks, CPU
   throttling, clock jitter) into every container except the driver. Every reply is checked as it
   arrives, however late, so replies that faults delay are still checked.
3. **Eventually: faults stop.** At a random moment (about 0.3% of rule draws) the driver runs its
   [eventually phase](#eventually-phase): it asks Antithesis to stop faults, then checks that the
   system recovers, that every workflow it started closes, and that visibility lists every closed
   run, and ends the timeline.

## The ideas the rules share

- **The model** ([`model.go`](../antithesis/driver/model.go)). For each workflow ID the driver
  records what it sent (start input, every signal with a unique ID and value, cancel, terminate,
  finish and reset requests), what Cadence acknowledged, and what it later read back (result,
  history, close status). Every workflow is a small deterministic program
  ([`workflows.go`](../antithesis/client/workflows.go)) whose result is a pure function of its
  inputs and the signals Cadence delivered, so the driver can compute the right answer itself.
  Each signal ID is sent exactly once; the driver never re-sends one.
- **Unknown outcomes** ([`classify`](../antithesis/client/main.go#L136)). Every Cadence call ends as
  `ok`, `failed` or `unknown`. Only errors that by contract mean "did not take effect and never
  will" (already started, does not exist, already completed, bad request) count as `failed`. A
  timeout, an unavailable service or an internal error is `unknown`: the request may or may not
  have taken effect. The checks treat unknown requests as "may appear at most once": an
  acknowledged signal must appear exactly once, and an unknown one at most once
  ([`checkCounter`](../antithesis/driver/model.go#L284)).
- **Stateful clients** ([`rpc.go`](../antithesis/driver/rpc.go),
  [client `main.go`](../antithesis/client/main.go)). Each of the three client containers runs one
  long-lived official Go client (`go.uber.org/cadence` v1.4.0) and a worker, and serves the driver
  over a JSON-RPC Unix socket. So the client library's own retries, sticky caches and reconnects
  are part of what is tested. A rule never waits more than 2 seconds for a reply; replies that come
  later are delivered to the same check. A closed socket means the client container restarted
  (`Reachable "a client restarts"`, [rpc.go:144](../antithesis/driver/rpc.go#L144)), and the
  client restarts its worker on boot.
- **Reads are pinned to the right run.** Workflow IDs can be reused and runs can be reset or
  continue as new, so result and history reads name the run they mean wherever the model knows it,
  and reads that cannot be attributed to one run are skipped rather than guessed
  ([`readResult`](../antithesis/driver/main.go#L433), [`resultDone`](../antithesis/driver/model.go#L196)).
- **Known findings get their own property.** Once a failure was root-caused as a Cadence bug, its
  check was split into a separately named property, so other failures of the same kind stay
  visible: "a signal is applied at most once in a workflow that was reset", "an acknowledged
  signal survives a reset of its workflow", and "a history read after a workflow closed includes
  its close event" ([model.go:317-339](../antithesis/driver/model.go#L317),
  [model.go:398-410](../antithesis/driver/model.go#L398)). Each of these collects every failure of
  its kind, not only those of the known mechanism.
- **Workflow types** ([`workflows.go`](../antithesis/client/workflows.go)): `counter` (signals,
  queries, continue-as-new, cancellation, reset target), `chain` (activities with retries,
  heartbeats, parallel and local activities), `timer`, `parent` (child workflows) and `race`
  (timers racing long local activities with a 4-second decision timeout, which forces decision
  heartbeats).

<a id="rules"></a>
## Rules

All rules are methods of `Driver` in [`driver/main.go`](../antithesis/driver/main.go). Each one
draws its choices from hegel, so Antithesis controls every one of them.

| Rule | What it does | Guarantees |
|---|---|---|
| [`RuleStart`](../antithesis/driver/main.go#L139) | Starts a new workflow of a random type with a random ID-reuse policy; 30% of counter starts are signal-with-start | [1](speedrun-report.md#guarantee-1), [2](speedrun-report.md#guarantee-2), [4](speedrun-report.md#guarantee-4) |
| [`RuleSignal`](../antithesis/driver/main.go#L217) | Sends an "add" signal with a fresh ID and value to an open counter workflow (15% ask the workflow to run an activity) | [2](speedrun-report.md#guarantee-2) |
| [`RuleFinish`](../antithesis/driver/main.go#L238) | Asks a counter workflow with at least three signals to complete | [4](speedrun-report.md#guarantee-4) |
| [`RuleQuery`](../antithesis/driver/main.go#L253) | Queries a counter's state: no signal twice, no signal that was never sent | [2](speedrun-report.md#guarantee-2) |
| [`RuleCancel`](../antithesis/driver/main.go#L302) | Requests cancellation of an open workflow | [4](speedrun-report.md#guarantee-4) |
| [`RuleTerminate`](../antithesis/driver/main.go#L317) | Terminates an open workflow | [4](speedrun-report.md#guarantee-4) |
| [`RuleReset`](../antithesis/driver/main.go#L332) | Resets a counter workflow to a random completed decision of its current run, with Cadence's default signal reapply; one reset per workflow at a time | [2](speedrun-report.md#guarantee-2), [3](speedrun-report.md#guarantee-3) |
| [`RuleDuplicateStart`](../antithesis/driver/main.go#L373) | Starts the ID of an open workflow again with a random reuse policy: Cadence must reject it | [1](speedrun-report.md#guarantee-1) |
| [`RuleResult`](../antithesis/driver/main.go#L421) | Reads the final result of a workflow that should be finishing, checks it against the model, then reads and checks that run's full history | [2](speedrun-report.md#guarantee-2), [3](speedrun-report.md#guarantee-3), [4](speedrun-report.md#guarantee-4) |
| [`RuleDescribeClosed`](../antithesis/driver/main.go#L488) | Describes a recently closed run: it must still be closed with the same status | [4](speedrun-report.md#guarantee-4) |
| [`RuleReuseClosed`](../antithesis/driver/main.go#L687) | Starts a new run on the ID of a closed workflow with a random reuse policy: the start must succeed or be rejected exactly as the policy says | [1](speedrun-report.md#guarantee-1) |
| [`RuleEventually`](../antithesis/driver/main.go#L513) | Rarely (0.3% of draws): runs the [eventually phase](#eventually-phase) | [4](speedrun-report.md#guarantee-4), [5](speedrun-report.md#guarantee-5), [6](speedrun-report.md#guarantee-6) |

<a id="eventually-phase"></a>
### Eventually phase

[`eventually`](../antithesis/driver/main.go#L522) stops faults, then, in order:

1. every client must serve a real request (a workflow start) again within 3 minutes;
2. a fresh workflow (two activities) must complete within 120 seconds;
3. every open counter workflow is asked to finish, and every workflow the driver started must close
   within 5 minutes, each read at least three times;
4. every closed run must be listed as closed in visibility within 2 minutes.

## Assertions

Every check the workload makes, with the guarantee it belongs to. `Always` means the condition
must hold every time it is evaluated; `Sometimes` means it must hold at least once in the run
(proof that a path was exercised); `Reachable`/`Unreachable` mean a line must, or must never, be
reached.

| Assertion (property name) | Kind | Where | Guarantee |
|---|---|---|---|
| a second start of an open workflow ID is rejected | Always | [main.go:409](../antithesis/driver/main.go#L409) | [1](speedrun-report.md#guarantee-1) |
| a start on a closed workflow ID follows its reuse policy | Always | [main.go:719](../antithesis/driver/main.go#L719), [main.go:723](../antithesis/driver/main.go#L723) | [1](speedrun-report.md#guarantee-1) |
| a duplicate start is rejected as already started | Sometimes | [main.go:411](../antithesis/driver/main.go#L411) | [1](speedrun-report.md#guarantee-1) |
| a closed workflow ID is reused | Sometimes | [main.go:720](../antithesis/driver/main.go#L720) | [1](speedrun-report.md#guarantee-1) |
| a reuse policy rejects a start on a closed workflow ID | Sometimes | [main.go:724](../antithesis/driver/main.go#L724) | [1](speedrun-report.md#guarantee-1) |
| a start returns an unknown outcome | Sometimes | [main.go:205](../antithesis/driver/main.go#L205) | [1](speedrun-report.md#guarantee-1) |
| an acknowledged signal is applied exactly once | Always | [model.go:166](../antithesis/driver/model.go#L166) (late acknowledgement), [model.go:321](../antithesis/driver/model.go#L321) (duplicate), [model.go:339](../antithesis/driver/model.go#L339) (missing) | [2](speedrun-report.md#guarantee-2) |
| an acknowledged signal survives a reset of its workflow | Always | [model.go:337](../antithesis/driver/model.go#L337) | [2](speedrun-report.md#guarantee-2) |
| a signal is applied at most once in a workflow that was reset | Always | [main.go:293](../antithesis/driver/main.go#L293) (query), [model.go:319](../antithesis/driver/model.go#L319) (result), [model.go:419](../antithesis/driver/model.go#L419) (history) | [2](speedrun-report.md#guarantee-2) |
| a query shows each delivered signal once | Always | [main.go:295](../antithesis/driver/main.go#L295) | [2](speedrun-report.md#guarantee-2) |
| a workflow only applies signals that clients sent | Always | [model.go:323](../antithesis/driver/model.go#L323) | [2](speedrun-report.md#guarantee-2) |
| signals are applied in the order they were acknowledged | Always | [model.go:359](../antithesis/driver/model.go#L359) | [2](speedrun-report.md#guarantee-2) |
| a signal is acknowledged | Sometimes | [model.go:162](../antithesis/driver/model.go#L162) | [2](speedrun-report.md#guarantee-2) |
| a signal with an unknown outcome is applied | Sometimes | [model.go:341](../antithesis/driver/model.go#L341) | [2](speedrun-report.md#guarantee-2) |
| a query sees applied signals | Sometimes | [main.go:297](../antithesis/driver/main.go#L297) | [2](speedrun-report.md#guarantee-2) |
| a workflow history is well formed | Always | [model.go:395](../antithesis/driver/model.go#L395), [model.go:417](../antithesis/driver/model.go#L417); the invariants are in [`historyChecks`](../antithesis/driver/model.go#L434): event IDs 1..N, started event first, one close event and it is last, each timer, activity and child resolved at most once and only after it started, no signal recorded twice | [3](speedrun-report.md#guarantee-3) |
| a history read after a workflow closed includes its close event | Always | [model.go:410](../antithesis/driver/model.go#L410) | [3](speedrun-report.md#guarantee-3) |
| a history contains a canceled timer | Sometimes | [model.go:423](../antithesis/driver/model.go#L423) | [3](speedrun-report.md#guarantee-3) |
| a workflow reset succeeds | Sometimes | [main.go:362](../antithesis/driver/main.go#L362) | [2](speedrun-report.md#guarantee-2), [3](speedrun-report.md#guarantee-3) |
| a reset workflow completes | Sometimes | [model.go:365](../antithesis/driver/model.go#L365) | [2](speedrun-report.md#guarantee-2), [3](speedrun-report.md#guarantee-3) |
| a completed workflow returns the result its inputs determine | Always | [model.go:261](../antithesis/driver/model.go#L261) (chain, parent), [model.go:276](../antithesis/driver/model.go#L276) (race), [model.go:362](../antithesis/driver/model.go#L362) (counter: sum of signal values and activity results) | [4](speedrun-report.md#guarantee-4) |
| a workflow ends only as completed, or as canceled or terminated on request | Always | [model.go:240](../antithesis/driver/model.go#L240) | [4](speedrun-report.md#guarantee-4) |
| a closed workflow keeps its outcome | Always | [model.go:225](../antithesis/driver/model.go#L225) (result read again), [main.go:507](../antithesis/driver/main.go#L507) (describe) | [4](speedrun-report.md#guarantee-4) |
| a timer never fires before its duration | AlwaysGreaterThanOrEqualTo | [model.go:270](../antithesis/driver/model.go#L270) | [4](speedrun-report.md#guarantee-4) |
| a workflow does not heartbeat decisions without progress | AlwaysLessThan (20 empty decision cycles in a row) | [model.go:415](../antithesis/driver/model.go#L415) | [4](speedrun-report.md#guarantee-4), [5](speedrun-report.md#guarantee-5) |
| every started workflow closes once faults stop | Always | [main.go:646](../antithesis/driver/main.go#L646) | [4](speedrun-report.md#guarantee-4), [5](speedrun-report.md#guarantee-5) |
| a cancel request closes a workflow as canceled | Sometimes | [model.go:247](../antithesis/driver/model.go#L247) | [4](speedrun-report.md#guarantee-4) |
| a terminate request closes a workflow | Sometimes | [model.go:249](../antithesis/driver/model.go#L249) | [4](speedrun-report.md#guarantee-4) |
| a parent workflow completes with all its children | Sometimes | [model.go:263](../antithesis/driver/model.go#L263) | [4](speedrun-report.md#guarantee-4) |
| an activity workflow completes | Sometimes | [model.go:265](../antithesis/driver/model.go#L265) | [4](speedrun-report.md#guarantee-4) |
| a race workflow cancels a timer | Sometimes | [model.go:278](../antithesis/driver/model.go#L278) | [3](speedrun-report.md#guarantee-3), [4](speedrun-report.md#guarantee-4) |
| a race workflow cancels timers in consecutive rounds | Sometimes | [model.go:279](../antithesis/driver/model.go#L279) | [3](speedrun-report.md#guarantee-3), [4](speedrun-report.md#guarantee-4) |
| a signal workflow continues as new and completes | Sometimes | [model.go:364](../antithesis/driver/model.go#L364) | [2](speedrun-report.md#guarantee-2), [4](speedrun-report.md#guarantee-4) |
| every client is served again once faults stop | Always | [main.go:553](../antithesis/driver/main.go#L553) | [5](speedrun-report.md#guarantee-5) |
| a new workflow completes within 2 minutes once faults stop | Always | [main.go:579](../antithesis/driver/main.go#L579) | [5](speedrun-report.md#guarantee-5) |
| a closed workflow appears in visibility once faults stop | Always | [main.go:679](../antithesis/driver/main.go#L679) | [6](speedrun-report.md#guarantee-6) |
| a cadence server process crashes | Unreachable | [entrypoint.sh:12-16](../antithesis/server/entrypoint.sh) (the supervisor reports any `panic:` or `fatal error:` line in the server's output) | none: crash safety ([finding](speedrun-report.md#sigterm-during-startup-panics)) |
| the workload starts | Reachable | [main.go:59](../antithesis/driver/main.go#L59) | test harness |
| a client restarts | Reachable | [rpc.go:144](../antithesis/driver/rpc.go#L144) | test harness |
| a client can't run a request | Unreachable | [rpc.go:115](../antithesis/driver/rpc.go#L115) | test harness |
| a workflow result can't be decoded | Unreachable | [model.go:202](../antithesis/driver/model.go#L202), [model.go:287](../antithesis/driver/model.go#L287) | test harness |

Antithesis also reports its own default properties, such as "No unexpected container exits", next
to these.

## Where the checks are narrower than the guarantees

- [Guarantee 1](speedrun-report.md#guarantee-1): the workload checks that a second start of an open
  ID is rejected and that reuse policies are honoured. It does not compare the open intervals of
  all runs of an ID, or check that a retried start returns the same run ID.
- [Guarantee 6](speedrun-report.md#guarantee-6): the workload checks that every closed run is
  listed as closed. It does not check that a closed run is never listed open again, or the close
  time.
- [Guarantee 3](speedrun-report.md#guarantee-3): each history is read once per run, so the "an
  earlier read is a prefix of a later one" part is checked only through the close-event check
  (a read after the server reported the run closed must contain the close event).

<a id="checks-that-were-stricter-than-cadence"></a>
## Checks that were stricter than Cadence

Triage of the long runs ([report](speedrun-report.md#what-the-long-runs-showed)) found that some
checks, as run, were stricter than Cadence's documented behaviour. The corrections below are
identified; the long runs and the fix checks used the code as listed above.

- **Reused workflow IDs.** [`RuleReuseClosed`](../antithesis/driver/main.go#L687) starts a new run on
  a closed ID while a signal, finish, cancel or terminate sent earlier to that ID (with no run ID) may
  still be retrying inside the Go client. Cadence applies such a request to the run that is current
  when it arrives, the new one, which the driver's model for the new run never asked for. Two
  concurrent reuse attempts on the same ID, and a duplicate start that succeeded as the old run
  closed, have the same effect. This produced the failures of "a second start of an open workflow ID
  is rejected", "a start on a closed workflow ID follows its reuse policy", "a workflow ends only as
  completed, or as canceled or terminated on request", "a workflow only applies signals that clients
  sent", "a completed workflow returns the result its inputs determine", and part of "an
  acknowledged signal is applied exactly once" and "a query shows each delivered signal once".
  Correction: count in-flight requests per workflow and reuse an ID only when none remain, with a
  grace period ([analysis](../antithesis/scratchbook/rca/hunt-unexpected-close.md#proposed-driver-fix)).
- **Timer timing.** "a timer never fires before its duration" ([model.go:270](../antithesis/driver/model.go#L270))
  compares two workflow clock readings with no tolerance, although the guarantee allows for the clock
  jitter Antithesis injects. Correction: allow the jitter (steps of up to about 4 s were observed)
  ([analysis](../antithesis/scratchbook/rca/hunt-timer-early.md#proposed-driver-fix-drivermodelgo267-271)).
- **Eventually phase.** "every client is served again once faults stop" ran against containers that
  the test environment had already shut down at the end of a timeline; "every started workflow closes
  once faults stop" also counted workflows the driver itself had left unfinishable (a finish sent
  before the start completed, a terminate on a run a failed reset had left in place, a read pinned to
  a run the model does not own). Corrections are in the
  [analysis](../antithesis/scratchbook/rca/hunt-liveness.md#notes-on-false-positive-review).
