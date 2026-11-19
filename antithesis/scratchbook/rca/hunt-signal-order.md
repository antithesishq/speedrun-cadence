# Hunt triage: "signals are applied in the order they were acknowledged"

Runs: v1.4.5 f04f3907…-67-6 (hunt), bba4d2f0…-67-6 (hunt -b); master 3e9720ca…-67-6, c26baba5…-67-6 (-b);
fix-verify 1a0d055c…-67-6 (v1.4.5 + acked-signal-lost-on-reset, retried-signal-duplicated-after-reset (v1),
sigterm-during-startup-panics). Raw material: antithesis/scratchbook/logs/hunt-signal-order/
(moments-*.json = failing evaluations with moments; h-*.txt / *.compact = histories; pg-*.txt = Postgres rows).

Check (driver/model.go:342-359): for acked signals a, b with a.Acked < b.Sent (driver clock), a must
precede b in the counter result's `applied` list (= order the workflow received them = history order).

## Signatures

| # | signature | f04f (v1.4.5) | bba4 (v1.4.5 -b) | 3e97 (master) | c26b (master -b) | 1a0d (fix-verify) | classification |
|---|-----------|---|---|---|---|---|---|
| S1 | a batch of signals buffered while a decision was in flight is written to history in Postgres heap-scan order, not arrival order (one or more later-acked signals land before earlier-acked ones in the same flushed batch) | 169 ev / 12 wids | 175 / 5 | 151 / 5 | 171 / 11 | 67 / 2 | **NEW Cadence server bug** `buffered-signals-reordered-on-reload` |

Counts are failing evaluations returned by `snouty runs search` (limit 300; every run returned fewer, so complete),
deduplicated across branches only by wid in the "wids" figure. Workflows with resets=0 make up 2/169, 56/175,
2/151, 60/171, 0/67 evaluations; the rest had resets, but resets are not needed (counter-70, -557, -58, -101 have none).

Every history inspected (9 workflows, all 5 runs, both versions, fix-verify included) shows the same shape: the
inverted signals sit inside one contiguous block of WorkflowExecutionSignaled events that was flushed from the
buffer in one transaction (right after a DecisionTaskFailed/TimedOut/Completed, before the next
DecisionTaskScheduled), and within that block the order is a permutation of arrival order. Two cases were checked
directly in Postgres at a moment before the flush. No example was a driver/checker artefact.

It appears on the fix-verify run (counter-35, counter-36), as expected: none of the three fixes touches the buffer.

## S1 — NEW: buffered-signals-reordered-on-reload

**Title:** Signals that arrive while a decision is in flight are added to history out of order when the
workflow's mutable state is reloaded from Postgres before they are flushed.

**Mechanism.**
1. A signal that arrives while a decision task is started is buffered: it gets `BufferedEventID` and is persisted
   as one row of `buffered_events` (BIGSERIAL `id`, so ids follow arrival order).
2. When the decision closes (completed, failed, timed out) the buffered events are flushed into history in the order
   of `mutableStateBuilder.bufferedEvents` (service/history/execution/mutable_state_builder.go:466-505, v1.4.5 and
   master identical; `allBufferedEvents = append(allBufferedEvents, e.bufferedEvents...)` at :486).
3. If the mutable state was (re)loaded from the database in between, `bufferedEvents` comes from
   `getBufferedEvents` (common/persistence/sql/workflow_state_non_maps.go:149-171), whose query has no ORDER BY:
   `SELECT data, data_encoding FROM buffered_events WHERE shard_id = $1 AND domain_id = $2 AND workflow_id = $3 AND run_id = $4`
   (common/persistence/sql/sqlplugin/postgres/execution.go:151, same in master; the MySQL query at mysql/execution.go:148 also has none).
   Postgres returns rows in physical (heap) order. `buffered_events` rows are deleted on every flush, so new rows
   reuse freed slots in earlier pages, and a later signal is returned before earlier ones.
4. `reorderAndFilterDuplicateEvents` (mutable_state_builder.go:2294-2460) only moves activity/child completions to
   the tail. Its comment at :2440 says "Sometimes we see buffered events are out of order when read back from
   database": the symptom is known, but it was fixed only for activity events, not for signals.

Reloads that trigger it: `failDecisionHelper` (service/history/decision/handler.go:878-882, `wfContext.Clear()` +
`LoadWorkflowExecution`) on every UnhandledDecision/other decision failure; shard movement (new owner loads from
DB); history-cache eviction (the default config has a cache of 20, which makes eviction likely); decision timeout.

**Effect.** The workflow receives signals in an order that differs from the order Cadence acknowledged them. The
recorded history is wrong (replay is consistent with it, so no nondeterminism error). Any workflow whose logic
depends on signal order (state machines, last-writer-wins updates, "finish" after data) can compute a wrong result.
Postgres-only in practice: MySQL InnoDB returns secondary-index matches in primary-key order, and Cassandra stores
the buffer as an ordered list.

**Evidence.**
- bba4d2f0…-67-6 (v1.4.5 -b), counter-70, failing moment input_hash 9004181159347822236 vtime 412.9155.
  Driver: acks of s617, s616, act-s615, act-s618, act-s620, act-s619, s621, s622 at vtime 403.785–403.805, s623 ack
  403.8075; decision 32/33 failed (UNHANDLED_DECISION) at 403.8085 ("Failing the decision", handler.go:490).
  History run 8f0997b3…: `34 DecisionTaskFailed, 35 s623, 36 s617, 37 s616, 38 act-s615, 39 act-s618, 40 act-s620,
  41 act-s619, 42 s621, 43 s622, 44 DecisionTaskScheduled` (logs/hunt-signal-order/c70-hist.txt).
  Postgres at (-7623883170619249068, 403.808), before the flush (pg-buffered-ids.txt):
  ```
  107|(0,15)|s623    <- newest id, in a free slot on page 0, so a heap scan returns it first
   99|(1,12)|s617
  100|(1,13)|s616
  101|(1,14)|act-s615 ... 106|(1,19)|s622
  ```
  The query plan is a Seq Scan. The scan order (107, 99..106) is exactly history order 35..43.
- bba4d2f0…-67-6, counter-141 (resets=6, 83 evaluations), moment 3445706489751872340 / 602.83. s1064 acked
  570.0838, s1065 acked 570.0874, while cadence3 was shutting down and handing off shards (569.80). CAN run
  22396de3…: `8 DecisionTaskCompleted, 9 ActivityTaskScheduled, 10 s1065, 11 s1064, 12 DecisionTaskScheduled`.
  Postgres at (5423172945900646180, 570.09): `222|(0,39)|s1065`, `221|(1,10)|s1064` (pg-c141.txt). Same mechanism
  (flushed at decision completion on the new shard owner).
- c26baba5…-67-6 (master -b), counter-557 (resets=0, 53 evaluations), moment 3402323338086942580 / 1267.38:
  `49 DecisionTaskFailed UNHANDLED_DECISION, 50 s2433, 51 s2431, 52 DecisionTaskScheduled`.
- 3e9720ca…-67-6 (master), counter-24 (80 evaluations), moment 4272327629315203937 / 619.35: flushed block
  `20 s206, 21 s207, 22 finish, 23 s216, 24 s212, 25 s215, 26 DecisionTaskScheduled`.
- 1a0d055c…-67-6 (fix-verify), counter-36 (66 evaluations), moment -9013431005685762769 / 415.74:
  `6 DecisionTaskTimedOut, 7 s436, 8 s432, 9 s433, 10 s434, 11 s430, 12 s429, 13 s399, 14 s400, 15 finish, 16 DecisionTaskScheduled`.
- f04f3907…-67-6 (v1.4.5), counter-104 (resets=6, 39 evaluations; the pattern where an *earlier* signal shows up late),
  moment -6321992211471165745 / 469.37: CAN run d146a957…: `17 DecisionTaskFailed UNHANDLED_DECISION, 18 s850,
  19 s851, 20 s853, 21 s854, 22 s849, 23 s852, 24 DecisionTaskScheduled`.
- f04f3907…-67-6, counter-51 (84 inversion pairs, the largest), moment -2272794824535279707 / 482.69: after
  `9 DecisionTaskTimedOut`, one flushed block of ~50 signals arrives in page-sized ascending runs
  (`… s562, s565, s601, s602 … s617, s567, s570, s572, s580 … s619 …`), as a heap scan over several pages returns them.
- f04f3907…-67-6 (v1.4.5), counter-125 (68 evaluations), moment -3661906927096012441 / 340.34: reset run 0c1f42c9…
  reapplies `… s458, s467, act-s461, act-s464, s465 …`. The reset copies the base run's order, so the inversion
  was most likely made by the same flush in the base run. The base history was not inspected.

**Fix (server).** Return buffered events in insertion order:
```diff
--- a/common/persistence/sql/sqlplugin/postgres/execution.go
-	getBufferedEventsQuery    = `SELECT data, data_encoding FROM buffered_events WHERE shard_id = $1 AND domain_id = $2 AND workflow_id = $3 AND run_id = $4`
+	getBufferedEventsQuery    = `SELECT data, data_encoding FROM buffered_events WHERE shard_id = $1 AND domain_id = $2 AND workflow_id = $3 AND run_id = $4 ORDER BY id`
--- a/common/persistence/sql/sqlplugin/mysql/execution.go
-shard_id=? AND domain_id=? AND workflow_id=? AND run_id=?`
+shard_id=? AND domain_id=? AND workflow_id=? AND run_id=? ORDER BY id`
```

**Severity:** medium. It silently breaks signal ordering, which workflow code relies on, under ordinary operations
(decision failures, shard moves, cache eviction) with no fault needed. The history stays self-consistent, and only
SQL (Postgres) persistence is affected. Versions: v1.4.5 and master (same code); also present with the three fixes applied.

## Driver / checker notes

No false positive was found. One latent weakness: `checkCounter` stores the *last* position of a signal in
`w.Applied` (model.go:298), so a duplicated signal (known finding retried-signal-duplicated-after-reset) would also
show up as order inversions. To keep the findings apart, record the first position (`if _, seen := w.Applied[id]; !seen { w.Applied[id] = i }`).
None of the inspected examples involved duplicates.

## Sessions

The debugging sessions b7267a789a6d1decd707ea702cf72629-67-6 (expired after 1h) and 7f63ca19c4800f5e60bb662bdabe4008-67-6
(cancelled) were used. The second session was a follow-up adversarial pass on the two outliers (counter-104 with
the "earlier signal late" pattern, and counter-51 with 84 pairs), to check that they were not a different mechanism
(duplicates or reset reapplication). They were not.
