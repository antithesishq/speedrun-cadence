# Guarantees: Uber (Cadence)

Written for Uber's Cadence engineers. Each row is a promise Cadence makes to the services that run
workflows on it, stated in plain technical language. Context: [system brief](system-brief.md).
Ranked: faults/concurrency-sensitivity × business impact, with client-checkability as the tie-breaker.

| Guarantee | Statement | Source | Breaks under | Property slug | Type |
|---|---|---|---|---|---|
| [Guarantee 1](#guarantee-1) | For a domain and workflow ID, at most one run is open at any time; a start for an ID with an open run is rejected with `WorkflowExecutionAlreadyStarted` (unless the reuse policy is `TerminateIfRunning`, which closes the old run first), and a retried start with the same request ID returns the same run ID (workflow ID uniqueness, idempotent start) | [Workflows doc][wf]: "there could be only one workflow (across all workflow types) with a given ID open per domain at any time"; [IDL `StartWorkflowExecution`][idl]: "It will return 'WorkflowExecutionAlreadyStartedError', if an instance already exists with same workflowId" | shard movement (history kill/pause/partition), DB latency, concurrent starts / signal-with-start on the same ID | `single-open-run-per-id` | Safety |
| [Guarantee 2](#guarantee-2) | A signal (or signal-with-start) that was acknowledged to the client appears in the target run's history exactly once, even when the client retries with the same request ID after a timeout, and is never lost when the run keeps going or continues-as-new (exactly-once signal delivery, request-ID deduplication) | [Go client signals doc][sig]: "When a signal is received for a running workflow, Cadence persists the event and the payload in the workflow history ... without the risk of losing the information"; [IDL `SignalWithStartWorkflowExecution`][idl]: "is used to ensure sending signal to a workflow" | lost responses + client retries (partition, latency), history crash after commit, shard movement | `acked-signals-exactly-once` | Safety |
| [Guarantee 3](#guarantee-3) | A workflow's event history is well-formed and append-only: event IDs run 1..N without gaps, it starts with `WorkflowExecutionStarted`, every started timer / scheduled activity / initiated child has at most one resolution event, a closed run ends in exactly one close event, and any later read returns the earlier read as a prefix (history consistency) | [Timers doc][timers]: "a TimerFired event means the timer fired, while a TimerCanceled event means it was canceled"; [PR #8595][pr8595]: "Timers can have a single resolution event, either canceled or fired"; [PR #6966][pr6966]: duplicate events "cause issues in the client, resulting in the workers reporting non-determinism" | history crash/restart, shard movement (mutable state reload), events arriving during an in-flight decision | `history-well-formed` | Safety |
| [Guarantee 4](#guarantee-4) | Every workflow whose start was acknowledged eventually closes with the result its code determines from the inputs, signals and activity results it saw: activity and child completions are recorded, the parent is told when its child closes, durable timers fire, never before their deadline, and a closed run stays closed (fault-oblivious execution) | [Workflows doc][wf]: "The workflow code is completely oblivious to any failures and downtime of workers or even the Cadence service itself"; [Timers doc][timers]: "The wait is durable: after the timer is created, a worker can restart or be removed from cache" | history/matching kills, shard and task list movement, partitions, clock jitter | `started-workflows-complete` | Safety + Liveness |
| [Guarantee 5](#guarantee-5) | Once failures stop, a fresh workflow (start, one activity, one timer, one signal) completes again within 120 s, and every workflow already started makes progress again in the same window (availability after recovery) | [Workflows doc][wf] (same quote as Guarantee 4); window inferred: no documented recovery time; ringpop `maxJoinDuration` is 30 s ([`development.yaml`][devcfg]) | faults stopping | `recovers-after-faults` | Liveness |
| [Guarantee 6](#guarantee-6) | With DB visibility, `DescribeWorkflowExecution`, `ListOpenWorkflowExecutions` and `ListClosedWorkflowExecutions` converge to the history's truth after faults stop: a closed run is listed closed with the same close status, never listed open again, and no run is missing (eventual consistency of visibility) | [`visibility_manager_interfaces.go`][vis]: "This is a secondary store that is eventually consistent with the main executions store"; [IDL][idl]: "ListOpenWorkflowExecutions is a visibility API to list the open executions in a specific domain" | transfer-queue task loss or reordering under history kills, DB partitions | `visibility-converges` | Safety (eventual) |

## Notes per guarantee

<a id="guarantee-1"></a>
### Guarantee 1: single open run per workflow ID
- **The check, in one sentence:** Drivers start / signal-with-start / continue-as-new over a small
  pool of workflow IDs with random reuse policies and fixed request IDs on retry; record every
  acknowledged run ID with its open and close times as seen from history; `Always` no two runs of
  the same ID have overlapping open intervals (start event time ≤ other's close event time, using
  `GetWorkflowExecutionHistory` of every run found via `DescribeWorkflowExecution` and visibility),
  `Always` a start that returned success with request ID R returns the same run ID on every retry
  with R, and `Always` a rejection is `WorkflowExecutionAlreadyStarted` only when the model says a
  run was open.
- **Driver records:** (workflow ID, request ID, reuse policy, response run ID or error) per call;
  per run: first and last history event.
- **Assertions:** `Always` (no overlap, idempotent run ID); `Sometimes` (a start was rejected
  because the ID was in use; a start retried after an ambiguous error).
- **Inherited from:** customer's own.
- **Why it matters to Uber:** workflow ID is the business key (trip, payment); two open runs mean
  double execution.

<a id="guarantee-2"></a>
### Guarantee 2: acknowledged signals exactly once
- **The check, in one sentence:** Each signal carries a unique payload (driver ID + sequence);
  workflows record every signal they receive and return the list as their result; after quiescence,
  `Always` every acknowledged signal payload appears exactly once across the run chain's histories
  (`WorkflowExecutionSignaled` events) and in the workflow result, and unacknowledged ones at most
  once.
- **Driver records:** (workflow ID, payload, request ID, ack/err/ambiguous) per signal and
  signal-with-start call.
- **Assertions:** `Always` (no duplicate payload; no lost acked payload); `Sometimes` (a signal was
  retried after an ambiguous error; a signal-with-start hit a running run).
- **Inherited from:** customer's own.
- **Why it matters to Uber:** signals carry external events (payment callbacks); a duplicate or a
  lost one changes business outcomes.

<a id="guarantee-3"></a>
### Guarantee 3: history is well-formed
- **The check, in one sentence:** After each workflow closes (and again at the end), page through
  `GetWorkflowExecutionHistory` twice; `Always` event IDs are contiguous from 1, there is one
  start event first and one close event last, no timer/activity/child/cancel-request has more
  than one resolution (`TimerFired`/`TimerCanceled`, `ActivityTask{Completed,Failed,TimedOut,Canceled}`,
  `ChildWorkflowExecution*` closed), every resolution references an earlier initiating event, and
  the first read is a prefix of the second.
- **Driver records:** the history pages it read and when.
- **Assertions:** `Always` per invariant; `Sometimes` (a timer was canceled; a decision heartbeat
  happened, detected as `DecisionTaskCompleted` followed directly by `DecisionTaskScheduled`
  within one local-activity run).
- **Inherited from:** customer's own.
- **Why it matters to Uber:** a malformed history makes every worker replay fail with
  non-determinism; the workflow is stuck until an operator resets it.

<a id="guarantee-4"></a>
### Guarantee 4: started workflows complete correctly
- **The check, in one sentence:** Workflows are small deterministic programs (activities, local
  activities, timers, children, signals, cancel, continue-as-new) whose expected result the driver
  can compute from what it sent; after quiescence, `Always` every acknowledged run chain is closed
  with the model's result, every `TimerFired` event's timestamp is ≥ its `TimerStarted` timestamp +
  duration (tolerance: the clock jitter Antithesis injects), and a run seen closed is never seen
  open or with a different close event later.
- **Driver records:** start inputs, signals sent, cancel/terminate requests; per run the close
  event and result.
- **Assertions:** `Always` (result matches model; no early timer; no resurrection);
  `AlwaysOrUnreachable` for the child-completion check when no child closed; `Sometimes` (a child
  completed, a timer fired after a history restart).
- **Inherited from:** customer's own.
- **Why it matters to Uber:** the core promise of Cadence; a parent stuck waiting for a child
  that already finished is an incident.

<a id="guarantee-5"></a>
### Guarantee 5: recovers after faults
- **The check, in one sentence:** In an `eventually_` command after faults stop, start a fresh
  workflow that runs one activity, one 1 s timer and receives one signal, retrying for up to 120 s;
  `Always` it completes within the window, and every workflow the driver started earlier has
  either closed or recorded a new event in the window.
- **Driver records:** time faults stopped (command start), first-attempt error, completion time.
- **Assertions:** `Always` (completed within 120 s, window inferred); `Sometimes` (first attempt
  failed and a later one succeeded).
- **Inherited from:** customer's own.
- **Why it matters to Uber:** a cluster that never recovers passes every safety check; Cadence is
  the control plane for many services.

<a id="guarantee-6"></a>
### Guarantee 6: visibility converges
- **The check, in one sentence:** After quiescence, poll `ListOpenWorkflowExecutions` /
  `ListClosedWorkflowExecutions` (filtered by workflow ID) and `DescribeWorkflowExecution` for
  every run the driver knows about, for up to 120 s; `Always` they agree with the history's close
  event (status and close time), and no run whose history is closed is still listed open.
- **Driver records:** the set of run IDs and their closed state from history.
- **Assertions:** `Always` (converged within the window); `Sometimes` (a run was first seen
  inconsistent and later converged).
- **Inherited from:** customer's own.
- **Why it matters to Uber:** operators and batch tooling act on list results (terminate, reset
  all open runs).

[wf]: https://cadenceworkflow.io/docs/concepts/workflows
[sig]: https://cadenceworkflow.io/docs/go-client/signals/
[timers]: https://cadenceworkflow.io/docs/concepts/timers/
[idl]: https://github.com/cadence-workflow/cadence-idl/blob/master/thrift/cadence.thrift
[pr8595]: https://github.com/cadence-workflow/cadence/pull/8595
[pr6966]: https://github.com/cadence-workflow/cadence/pull/6966
[vis]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/common/persistence/visibility_manager_interfaces.go#L41
[devcfg]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/config/development.yaml
