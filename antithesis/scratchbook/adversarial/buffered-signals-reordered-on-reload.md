Verdict: stands

# Adversarial review: buffered-signals-reordered-on-reload

Finding: `signals are applied in the order they were acknowledged` fails (antithesis/driver/model.go:342-359).
RCA: antithesis/scratchbook/rca/hunt-signal-order.md. Evidence: antithesis/scratchbook/logs/hunt-signal-order/.
I tried to explain it away. None of the defenses holds. The one that comes closest is that Cadence never
documents signal order explicitly (see check 4). That affects severity, not whether the finding is valid.

## 1. Harness intervention: does not hold
- The harness does not touch Postgres or the history service. Postgres runs unmodified
  (config-template/docker-compose.yaml), and the server is upstream source plus coverage instrumentation (triage-context.md).
- The inversion is visible below the harness, in two places. History (`c70-hist.txt`): `34 DecisionTaskFailed, 35 s623, 36 s617 ... 43 s622`.
  Postgres before the flush (`pg-buffered-ids.txt`): `107|(0,15)|s623` comes ahead of `99|(1,12)|s617 ... 106|(1,19)|s622`.
  The BIGSERIAL ids give insertion order (99 < 107). The heap-scan order (107, 99..106) matches history order 35..43 exactly.
- Driver timing (`c70.log:22060`, `:22068`): s617 is acked at 403.7852 and s623 at 403.8075, both through client2.
  The decision fails at 403.8085 (`c70.log:22069`, "Failing the decision", shard 8).

## 2. Configuration/tuning: makes it more frequent, does not cause it
- `history.cacheMaxSize: 20` (dynamicconfig.yaml:5-6; the default is 512, src/cadence-v1.4.5/common/dynamicconfig/dynamicproperties/constants.go:974,4123)
  causes more reloads from cache eviction.
- The counter-70 trigger does not depend on the cache. `failDecisionHelper` always runs `wfContext.Clear()` and then
  `LoadWorkflowExecution` (src/cadence-v1.4.5/service/history/decision/handler.go:878-882; master is the same).
  That happens on every UNHANDLED_DECISION, which is an ordinary outcome when a signal arrives while a decision
  that closes the workflow or continues it as new is in flight.
- Shard handoff (counter-141) and decision timeout (1a0d counter-36) also reload state, and they happen under default config.
- Heap-slot reuse after `DELETE FROM buffered_events` on each flush (postgres/execution.go:150) is ordinary Postgres behaviour.

## 3. Faults beyond the guarantee: does not hold
- The reload paths are failed decisions, shard movement, decision timeout and cache eviction. These are routine
  operations, not faults outside what Cadence is meant to tolerate.
- counter-70, counter-557, counter-58 and counter-101 have resets=0. 56 of 175 evaluations in bba4 and 60 of 171 in c26b have resets=0.
- Counter-70 needed no kill or partition. The trigger is the UNHANDLED_DECISION reload on a healthy shard.
- Clock jitter cannot explain it. The driver is excluded from faults, Go `time.Before` uses the monotonic clock,
  and the database ids show the inversion without relying on any clock.

## 4. Checker or workflow code: the closest defense, and it does not hold
- Workflow code (client/workflows.go:73-112): all signals that count go through one channel, `add`.
  `finish` is never appended to `Applied`, so the checker skips it (`oka` is false).
- A Cadence channel is FIFO, and the client delivers signals in history order. Inside `apply`, the activity `Get`
  blocks while new signals wait in the channel, which keeps FIFO order. So `Applied` order equals history order,
  and the history itself is inverted. The workflow code cannot cause this.
- Checker (model.go:342-359): it compares only pairs where `a.Acked < b.Sent`, so concurrent sends are excluded.
  The latent weakness is that duplicates record their last position (model.go:298), which could turn a duplicate
  into a false inversion. The inspected cases contain no duplicates (RCA, Driver/checker notes).
- Documented semantics (strongest defense): guarantees.md does not list signal order as a guarantee, and Cadence
  docs never state it in so many words. Why the defense still fails:
  - On the normal in-memory path, Cadence preserves arrival order: `updateBufferedEvents` is appended in order
    (mutable_state_builder.go:466-505). On Cassandra the buffer is an ordered list. So the order depends on the
    backend and on whether a reload happened.
  - Cadence's own code treats reordering as a defect: the comment at mutable_state_builder.go:~2440 reads
    "Sometimes we see buffered events are out of order when read back from database", and the code there patches
    the order for activity events only.
  - History is the record of what happened. Signals A then B, both persisted (acked) and strictly sequential, end
    up recorded as B then A.
  - The fix is a one-line `ORDER BY id`.

## 5. Reproduction: reproduces everywhere
- The finding fails in all 5 runs: v1.4.5 (f04f: 169 evaluations, bba4: 175), master (3e97: 151, c26b: 171), and
  the fix-verify run (1a0d: 67 evaluations, counter-35/36).
- The source is the same in both versions:
  - The query has no ORDER BY in either version (src/cadence-{v1.4.5,master}/common/persistence/sql/sqlplugin/postgres/execution.go:151).
  - `getBufferedEvents` is unchanged in both (common/persistence/sql/workflow_state_non_maps.go:149-171).
  - The index is on (shard_id, domain_id, workflow_id, run_id) only (schema/postgres/cadence/schema.sql:85).
- None of the 3 applied fixes touches the buffer, which matches the fix-verify run still failing.

## Severity for Uber engineers
Medium. Cadence silently records signals that were acknowledged in sequence in a different order in history, so
workflow logic that depends on order (state machines, last-writer-wins, "finish after data") can produce wrong
results with no error, because replay agrees with the wrong history. Three things lower it:
- Only SQL persistence is affected, and in practice only Postgres. InnoDB returns secondary-index matches in primary-key order, and Cassandra stores the buffer as an ordered list.
- Signal order is not an explicitly documented guarantee.
- It only affects signals buffered while a decision is in flight and then reloaded before the flush.

The fix is trivial (`ORDER BY id` in the Postgres and MySQL `getBufferedEventsQuery`).
