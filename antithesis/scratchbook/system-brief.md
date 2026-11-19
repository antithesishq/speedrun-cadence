# System brief: Cadence (Uber)

Cadence is Uber's open-source workflow orchestration engine ([repo][repo], [docs][docs]). Clients
write workflows as ordinary code (Go/Java SDKs). Cadence persists every state change as an
append-only **event history** and replays it on workers, so workflow code is "completely oblivious
to any failures and downtime of workers or even the Cadence service itself" ([workflows doc][wf]).
Versions under test: release `v1.4.5` (`ecd2b70ca`, 2026-10-22) and master `572e7e2c` (2026-11-18,
67 commits later).

Scope for this speedrun: one cluster (no cross-DC replication), Postgres via the SQL plugin for
both the default and the visibility store, black-box drivers (Go SDK `go.uber.org/cadence`,
frontend gRPC/Thrift, `cadence` CLI), Antithesis faults (partitions, latency, kills, pauses,
clock jitter). Config and dynamic config may change; source may not.

## How Uber uses it

Cadence was built at Uber (successor of SWF-style orchestration) and runs a large share of Uber's
backend orchestration: payments and money movement, trip/order lifecycles, driver onboarding,
infra automation, ML pipelines ([Cadence 1.0 announcement][uberblog], [ADOPTERS.md][adopters]).
The promises that matter there are "exactly one run per business key" (workflow ID), "a signal I
got an ack for is in the history", "the workflow eventually finishes with the right answer", and
"durable timers" (sleep 30 days safely).

## Architecture

Four services in one binary (`cadence-server start --services=frontend,history,matching,worker`,
[`cmd/server`][cmdserver]); each scales horizontally and finds peers via **ringpop** membership
([`common/membership`][membership], `ringpop:` block in [`config/development.yaml`][devcfg]).

| Service | Role | Key code |
|---|---|---|
| frontend | stateless public API (gRPC 7833 / Thrift 7933); validation, rate limits, routes by workflow ID → history shard, task list → matching host | [`service/frontend/api/handler.go`][fe] |
| history | owns workflow **mutable state** + history events; `numHistoryShards` fixed shards (workflow ID hash → shard), each owned by one host via the ring; runs per-shard **transfer** (immediate) and **timer** (scheduled) queues | [`service/history/engine/engineimpl/`][engine], [`execution/mutable_state_builder.go`][msb], [`shard/context.go`][shardctx], [`queue/`][queue], [`queuev2/`][queuev2] |
| matching | task lists: durable backlog + sync match of decision/activity tasks to long-polling workers; task list partitions; each task list owned by one host via ring + DB lease | [`service/matching/tasklist/`][tasklist] (`task_list_manager.go`, `db.go`, `task_reader.go`, `task_writer.go`, `matcher.go`) |
| worker | system workflows: parent-close policy, scanners/fixers, archiver, batcher, failover | [`service/worker/`][worker] |

**Persistence (SQL/Postgres).** Tables `shards`, `executions`, `current_executions`,
`history_node`/`history_tree`, `transfer_tasks`, `timer_tasks`, `tasks`/`task_lists`, plus
`executions_visibility` in the visibility DB ([`schema/postgres`][schema],
[`common/persistence/sql`][sql], [`sqlplugin/postgres`][pg]). Every execution write is one
transaction that first takes `SELECT range_id FROM shards ... FOR UPDATE` and compares the shard's
`range_id` ([`lockShard`][lockshard], [`shard.go`][pgshard]); the current-run row is locked with
`FOR UPDATE` for start/continue-as-new ([`lockCurrentExecutionIfExists`][lockcur]).

## Where the hard problems are

- **Shard ownership and range IDs.** A host acquiring a shard bumps `range_id`
  ([`renewRangeLocked`][renew]); writes from the old owner fail `ShardOwnershipLost`. Fault paths:
  a paused/partitioned old owner with a cached (stale) mutable state; transfer/timer tasks
  re-executed by the new owner (must be idempotent); shard movement on every ring change.
  History caches mutable state (`history.cacheMaxSize`), so stale-cache bugs surface only on reload.
- **Task list ownership.** Matching leases a task list by bumping `task_lists.range_id`
  ([`db.go` `RenewLease`][tldb]); task writes/ack-level updates are conditional on it; a
  `ConditionFailed` stops the manager ([`handleErr`][tlm]). Two hosts briefly believing they own
  the list, or a stuck manager, show up as lost/duplicated or stalled decision/activity tasks.
- **Decision/activity task delivery.** Transfer task → matching `AddTask` (sync match or backlog)
  → poll → history `RecordDecisionTaskStarted` (dedups by request ID) → respond. At-least-once
  dispatch; history must turn duplicates into no-ops. Sticky task lists and decision heartbeats
  (`ForceCreateNewDecisionTask`, used by long local activities) are separate paths.
- **Timers.** User timers, activity/decision timeouts, workflow timeouts, retry backoff, cron and
  DelayStart all go through the timer queue; events that arrive while a decision is in flight are
  **buffered** and flushed later, which is where duplicate/resurrected events come from (see
  [the known-bug check](positive-control.md)).
- **Workflow ID uniqueness / reuse policy.** `current_executions` holds one row per workflow ID;
  reuse policies `AllowDuplicateFailedOnly` (default), `AllowDuplicate`, `RejectDuplicate`,
  `TerminateIfRunning`; start is idempotent by request ID
  ([`start_workflow_execution.go`][start]). Orphaned-history cleanup on failed starts was added in
  2026 and needed two follow-up fixes.
- **Signal-with-start.** Signals the running run or starts a new one atomically; dedups by request
  ID in mutable state (`AddSignalRequested`, fixed for signal-with-start only in 2024).
- **Reset, continue-as-new, child workflows, cancellation.** Reset forks a new history branch and
  reapplies signals across the continue-as-new chain ([`service/history/reset`][reset]); child
  start/complete is a cross-shard handshake via transfer tasks
  ([`record_child_execution_completed.go`][rcc]); cancellation is a request event + decision.
- **Mutable state vs events.** Mutable state is a projection of history kept in separate rows;
  checksums and a 2026 "corruption detection and repair" feature (`fc09cb31b`) exist because they
  drift. Cross-DC replication of either is out of scope here.
- **Visibility.** With DB visibility, open/closed records are written asynchronously by transfer
  tasks; the store "is eventually consistent with the main executions store"
  ([`visibility_manager_interfaces.go`][vis]).

## Existing test assets

- **`/simulation`** ([README][simreadme]): docker-compose black-box scenario suites. `replication/`
  (multi-cluster failover, active-active; the only one in CI), `matching/` (throughput,
  partitions, isolation, load balancers; performance, not correctness), `history/` (queue v2
  cache-shadow and split scenarios with [dynamic config overrides][simhistdc]). Scenarios are
  scripted YAML timelines, without fault injection; Cassandra-based.
- **Canary** ([`canary/`][canary]): long-running health-check workflows covering echo, signal,
  timeout, local activity, cancellation, retry, reset, query, cron, child/concurrent execution,
  visibility, batch. Good source of workflow shapes for our driver; it checks liveness, not
  invariants.
- **Host integration tests** ([`host/`][host]): in-process onebox, includes
  `TestSignalDoesNotOverrideDelayStart` etc.

## Running a multi-node cluster on Postgres

- [`docker/docker-compose-postgres.yml`][composepg]: Postgres 17.4 + one
  `ubercadence/server:*-auto-setup` container (schema setup + all services). Env:
  `DB=postgres`, `POSTGRES_SEEDS`, `DB_PORT`, `DYNAMIC_CONFIG_FILE_PATH`.
- [`docker/config_template.yaml`][tmpl] (rendered by `entrypoint.sh`/`start-cadence.sh`):
  `SERVICES=frontend|history|matching|worker` per container, `NUM_HISTORY_SHARDS` (default 4),
  `RINGPOP_SEEDS` (comma list, `bootstrapMode: hosts`), `BIND_ON_IP`. So a multi-node cluster is
  N containers of the same image with different `SERVICES` and shared `RINGPOP_SEEDS`. Schema
  setup should run once (auto-setup image or `cadence-sql-tool`), not from every node.
- [`config/development_postgres.yaml`][devpg]: datastore block for running from source.
- Dynamic config is file-based ([`config/dynamicconfig/development.yaml`][devdc],
  [README][dcreadme]); keys are defined in [`dynamicproperties/constants.go`][dckeys].

### Dynamic config that makes testing more effective (all exist in v1.4.5)

| Knob | Why |
|---|---|
| `NUM_HISTORY_SHARDS` 8–16 (static) with 2–3 history hosts | more shards per host change owner on each ring change |
| `history.cacheMaxSize` small (e.g. 10–50) / `history.cacheTTL` short | forces mutable state reload from DB, exposing stale-cache and buffered-event bugs |
| `matching.numTasklistWritePartitions` / `ReadPartitions` 2–4 | exercises partition forwarding and multiple owners per logical task list |
| `matching.idleTasklistCheckInterval`, `matching.updateAckInterval`, `matching.longPollExpirationInterval` short | more task list unload/reload, ack-level writes and poll churn |
| `history.timerProcessorMaxPollInterval`, `history.transferProcessorMaxPollInterval`, `*UpdateAckInterval` short | queue ack-level persistence and re-reads happen often |
| `history.shardUpdateMinInterval` short | more shard-info writes racing with range ID changes |
| `system.maxDecisionStartToCloseSeconds` / workflow decision timeout small (2–5 s) | decision timeouts, transient decisions and decision heartbeats occur within a test |
| `history.decisionHeartbeatTimeout` short | bounds heartbeat chains |
| `frontend.shutdownDrainDuration`, `history.shutdownDrainDuration` 0 | kills behave like crashes |
| `history.enableCleanupOrphanedHistoryBranchOnWorkflowCreation` (default true on v1.4.5) | leave at default; it is a live code path in failed-start handling |

Workload-side: short activity timeouts and retry policies, local activities near the decision
timeout, timers of 1–10 s, many workflows sharing a small pool of workflow IDs (to hit reuse
policies), child workflows that finish immediately.

[repo]: https://github.com/cadence-workflow/cadence
[docs]: https://cadenceworkflow.io/docs/
[wf]: https://cadenceworkflow.io/docs/concepts/workflows
[uberblog]: https://www.uber.com/blog/announcing-cadence/
[adopters]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/ADOPTERS.md
[cmdserver]: https://github.com/cadence-workflow/cadence/tree/v1.4.5/cmd/server
[membership]: https://github.com/cadence-workflow/cadence/tree/v1.4.5/common/membership
[devcfg]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/config/development.yaml
[fe]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/service/frontend/api/handler.go
[engine]: https://github.com/cadence-workflow/cadence/tree/v1.4.5/service/history/engine/engineimpl
[msb]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/service/history/execution/mutable_state_builder.go
[shardctx]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/service/history/shard/context.go
[renew]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/service/history/shard/context.go#L1125
[queue]: https://github.com/cadence-workflow/cadence/tree/v1.4.5/service/history/queue
[queuev2]: https://github.com/cadence-workflow/cadence/tree/v1.4.5/service/history/queuev2
[tasklist]: https://github.com/cadence-workflow/cadence/tree/v1.4.5/service/matching/tasklist
[tldb]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/service/matching/tasklist/db.go#L96
[tlm]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/service/matching/tasklist/task_list_manager.go
[worker]: https://github.com/cadence-workflow/cadence/tree/v1.4.5/service/worker
[schema]: https://github.com/cadence-workflow/cadence/tree/v1.4.5/schema/postgres
[sql]: https://github.com/cadence-workflow/cadence/tree/v1.4.5/common/persistence/sql
[pg]: https://github.com/cadence-workflow/cadence/tree/v1.4.5/common/persistence/sql/sqlplugin/postgres
[pgshard]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/common/persistence/sql/sqlplugin/postgres/shard.go#L42
[lockshard]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/common/persistence/sql/sql_shard_store.go#L200
[lockcur]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/common/persistence/sql/sql_execution_store_util.go#L550
[start]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/service/history/engine/engineimpl/start_workflow_execution.go
[reset]: https://github.com/cadence-workflow/cadence/tree/v1.4.5/service/history/reset
[rcc]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/service/history/engine/engineimpl/record_child_execution_completed.go
[vis]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/common/persistence/visibility_manager_interfaces.go#L41
[simreadme]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/simulation/README.md
[simhistdc]: https://github.com/cadence-workflow/cadence/tree/v1.4.5/simulation/history/dynamicconfig
[canary]: https://github.com/cadence-workflow/cadence/tree/v1.4.5/canary
[host]: https://github.com/cadence-workflow/cadence/tree/v1.4.5/host
[composepg]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/docker/docker-compose-postgres.yml
[tmpl]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/docker/config_template.yaml
[devpg]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/config/development_postgres.yaml
[devdc]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/config/dynamicconfig/development.yaml
[dcreadme]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/config/dynamicconfig/README.md
[dckeys]: https://github.com/cadence-workflow/cadence/blob/v1.4.5/common/dynamicconfig/dynamicproperties/constants.go
