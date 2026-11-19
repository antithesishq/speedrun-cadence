# History shard 2 unavailable after faults stop (prefix run, probe timeout)

Run `0dc3db5a45f56d0b81c3e7e474252a51-67-6` (cadence-prefix, c889f8319). Failing moment
`input_hash=1743852966637431354 vtime=811.678`. Property: "a new workflow completes within 2 minutes
once faults stop" (took 126 s). Log: `antithesis/scratchbook/logs/prefix3/probe.log`.

**Classification: false-positive (harness-gap).** No Cadence logic bug. A host was killed in the middle
of a database transaction. Its Postgres backend lived on as `idle in transaction` and kept a row lock on
`shards` row 2. With the harness's default Postgres settings, that orphaned lock would last about 2 h.
Cadence shows a robustness weakness here (no timeout on shard acquisition), but even with a timeout the
shard could not be acquired while the lock is held.

## Sequence (vtime)

| vtime | Event |
|---|---|
| 591.27 | postgres restarts after a kill. cadence2's history service (pid 1 of that incarnation) reconnects. |
| 608.34 | Fault: `network/partition Stopped nodes=ALL` (7.7 s). At the same wall time (20:00:40.49), postgres backend **pid 104** (client 10.20.20.12:60482, the old cadence2) finishes `INSERT INTO timer_tasks …` inside a shard-locked transaction (`SELECT … FROM shards WHERE shard_id=$1 FOR SHARE`, then the INSERT). It then waits in `ClientRead` for the next statement, which never arrives. |
| 616.27 | `network/restore`. |
| 619.27 | `node/kill cadence2` (exit 137). Postgres never sees a FIN/RST for pid 104 or for about 58 other old cadence2 connections, and the same happens for the old cadence3 connections (stopped at 610.5). Postgres's side of each socket is idle, so it never sends anything that would detect the dead peer. |
| 631.95 | `network/restore`, `fault-injector paused=true` (faults stop). |
| 632.52 | cadence1/2/3 containers start. cadence2 gets the same IP, 10.20.20.12. |
| 632.4651–632.4667 | cadence2 `acquireShards` (concurrency 1): shards 0 and 1 start (`Range updated` 16, 12). Shard 2: `Shard engine state changed lifecycle=Starting` and then **nothing more**. No `Range updated`, no `Acquired shards` from cadence2 for the rest of the log. cadence1 and cadence3 log their 60 s `Acquiring/Acquired shards` at 692 and 752; cadence2 does not. |
| 632.47 (20:01:04.617) | postgres **pid 370** (new cadence2, port 32962) runs `SELECT range_id FROM shards WHERE shard_id = $1 FOR UPDATE` (UpdateShard → lockShard) and waits on `Lock/transactionid`; `pg_blocking_pids = {104}`. |
| 634–685 | Every client starts a workflow whose ID hashes to other shards, so these starts succeed. |
| ~690–811 | Probe `probe-1795032117821572188` → `farm.Fingerprint32 % 8 = 2` → shard 2, owned by cadence2 (`Shard belongs to current host? true`). All three frontends log `Frontend request timedout` for StartWorkflowExecution about every 5 s. The driver sees unknown/timeout every ~21 s. |
| 811.68 | Assertion fails. |

## Evidence from commands at the failing moment (`snouty runs exec`)

- postgres, `pg_stat_activity`: pid 104 is `idle in transaction` with an xact age of 3 min 25 s; its last query is the
  `INSERT INTO timer_tasks`. pid 370 (backend_start 20:01:04.587, from the new cadence2) is `active`,
  waiting on `Lock/transactionid`, blocked by `{104}`, on `SELECT range_id FROM shards WHERE shard_id = $1 FOR UPDATE`.
  More orphans hold `task_lists` row locks (pids 143, 220, 226), which block matching on the new hosts as well.
- `SELECT shard_id FROM shards FOR UPDATE SKIP LOCKED` returns 0,1,3,4,5,6,7. Only **shard 2 is locked**.
- postgres settings: `idle_in_transaction_session_timeout=0`, `tcp_keepalives_idle/interval/count=0`
  (so the kernel defaults apply: 7200/75/9), `statement_timeout=0`, `lock_timeout=0`.
- cadence2: `ps` shows a single cadence-server (pid 27, up 179 s) and no supervisor restarts after vtime 326.
  The supervisor played no part.
- **Causal check:** `pg_terminate_backend(104)` at 811.68, then a 20 s sleep. pid 370 becomes idle, shard 2 has
  `range_id` 18 → 19, and cadence2 logs `Range updated for shardID shard-id=2 range-id=19`, `Shard engine
  state changed Started`, `Acquired shards`. Removing the orphaned lock alone brings the shard back.

## Root cause

1. **Environment and harness:** a node crash (the Antithesis kill delivers no FIN/RST to postgres) leaves server-side
   backends `idle in transaction` with their row locks held. With stock Postgres
   (`config-template/docker-compose.yaml:11` sets only `max_connections=400`), nothing ends them until TCP
   keepalive fires. A probe goes out after 7200 s; because the IP is reused, the new host's kernel answers with
   RST and the backend dies. Expected outage of shard 2 is about **2 h** after 20:00:40, far past the 2 min
   window. Any Postgres client would be stuck the same way. Cadence's documented model of Postgres assumes the
   database ends dead sessions.
2. **Cadence (contributing, not the root):** shard acquisition has no deadline and blocks the whole
   acquisition loop:
   - `src/cadence-prefix/service/history/shard/context.go:1806`: `throttleRetry.Do(context.Background(), getShard)`
     and `:1136` `renewRangeLocked` → `UpdateShard(context.Background(), …)`. The UpdateShard call is
     `common/persistence/sql/sql_shard_store.go:180-181` → `lockShard` (`:200`) →
     `postgres/shard.go:42` `SELECT … FOR UPDATE`, which waits forever.
   - `service/history/shard/controller.go:511`: `getOrCreateEngine` holds the shard item's lock for the whole
     acquisition, so every request for shard 2 waits on that lock until the frontend deadline.
   - `controller.go:431-480` `acquireShards`: the default `AcquireShardConcurrency=1`
     (`dynamicproperties/constants.go:4111`) and `wg.Wait()` mean cadence2's periodic and ring-change
     re-acquisition never runs again.
   - v1.4.5 (`context.go:1136,1808`) and master (`context.go:1146,1857`) contain the same `context.Background()`
     calls, so a newer build would not change this result.
   A deadline would turn the hang into retries with errors, but the shard would stay unavailable as long as
   the orphaned lock is held. So this is hardening, not the fix.

## Fix (harness)

Have postgres end orphaned sessions quickly. In `config-template/docker-compose.yaml`:

```yaml
command: ["postgres", "-c", "max_connections=400",
          "-c", "idle_in_transaction_session_timeout=30s",
          "-c", "tcp_keepalives_idle=10", "-c", "tcp_keepalives_interval=5", "-c", "tcp_keepalives_count=3"]
```

Cadence's transactions last milliseconds, so 30 s never cuts off a legitimate one. Keepalives also clean up
dead sessions in other states, such as `active` with an unread result. With this change, recovery takes
≤ ~30 s, well inside the 2 min property. Optional: count orphaned backends from the driver or a sidecar
(`pg_stat_activity` with `state='idle in transaction'` and age > 60 s) and report them as an
environment signal rather than a Cadence failure.

Possible upstream hardening (not needed for this triage): bound `acquireShard`/`renewRangeLocked` with a
context timeout, or run shard acquisition with concurrency greater than 1, so one stuck shard cannot freeze
the host's acquisition loop.
