Verdict: stands

# Adversarial review: sql-admin-conn-close-panic

Finding: during startup, cadence-server panics with `panic: cannot close connection`
(`tools/sql/conn.go:121`). The call path is `verifySchema` (`cmd/server/cadence/fx.go:163`), then
`sql.VerifyCompatibleVersion`, then `doCheckCompatibleVersion` (`tools/sql/handler.go:125`, `defer connection.Close()`).
Source: [rca/fixverify-residuals.md (the crash section; verdicts in "Patch verdicts")](../rca/fixverify-residuals.md#patch-verdicts). Proposed fix: [sql-conn-close-no-panic.diff](../../fixes/sql-conn-close-no-panic.diff).

The bug stands, with low severity. It is a deliberate `panic` on an I/O error path. It sits in unmodified
upstream code, in v1.4.5 and master alike, and runs with default configuration. The best defense
("the server would have exited anyway") holds only in part. The panic's real cost is one unneeded
process restart, and the loss of the error that would have explained a failed check.

## Check 1: harness intervention

- `antithesis/server/entrypoint.sh` supervises the server like a Kubernetes `restartPolicy: Always`. It
  restarts the server after a 2 s backoff. It turns a `panic:` or `fatal error:` line in the server's
  output into the reachability assertion "a cadence server process crashes"
  (`entrypoint.sh` `report_crash`, `grep -m1 -E '^(panic:|fatal error:)'`).
- The details are `stopping:0` and `exit:2` (`logs/fixverify-residuals/crash.json`, all 119 lines). No
  SIGTERM from the harness was in flight. Exit code 2 is the Go runtime's code for an unrecovered panic.
  The crash was not induced by the supervisor.
- The harness does not touch the schema-check path. The `schema` container (`docker-compose.yaml:15-20`)
  is a one-shot setup. The server's own fx OnStart hook runs the check on every start.
- The harness only makes restarts frequent, and restarts are what expose this path. Restarts are
  normal in production too: deploys, OOM kills, node loss.

Result: not harness-caused.

## Check 2: configuration and tuning

- `cadence.yaml` (lines 10-34) is a plain postgres SQL store: maxConns 20/10, maxIdleConns, and
  maxConnLifetime 1h. There are no unusual DSN parameters and no socket timeouts.
- `dynamicconfig.yaml` contains no schema or verification keys. The panicking call is the legacy SQL
  version check (`fx.go:162-164`). It runs unconditionally for any SQL persistence and is not gated by
  `EnforceSchemaVerificationV2`.
- The `panic("cannot close connection")` is identical in v1.4.5 (`src/cadence-v1.4.5/tools/sql/conn.go:117-123`)
  and master (`src/cadence-master/tools/sql/conn.go:117-124`).

Result: it would happen with default configuration.

## Check 3: faults beyond the guarantee

- Faults active at the panic (`logs/fixverify-residuals/crash-slice.ndjson`): a "Slowed" partition from
  vtime 400.48, with `drop_rate 0`, latency mean 838 ms and sd 296. postgres and cadence3 are on the
  **same** side: `[client3, client2, postgres, cadence3, client1] | [cadence1, cadence2, schema]`. There was
  no kill of postgres in the slice. The fault is ordinary network latency, well within what a
  production database link sees.
- `lib/pq` v1.10.9 `conn.Close` (`~/go/pkg/mod/github.com/lib/pq@v1.10.9/conn.go:868-884`) writes a
  Terminate message and closes the socket. It returns an error on EPIPE, ECONNRESET or a socket close
  error. The DB layer passes that error through unchanged: `sqldriver/singleton.go:108-120` and
  `sqlplugin/postgres/db.go:132-133`. A connection the peer has reset is a normal event under network trouble.
- `guarantees.md` has no "never crash" guarantee. "a cadence server process crashes" is our own
  robustness property. It is still a reasonable bar: a server process should not panic on a transient
  database I/O error, and Cadence's persistence layer otherwise returns such errors.

Result: the fault is within a normal operating envelope. The property is ours, not a documented Cadence guarantee.

## Check 4: checker and workflow code

- No workflow or driver code is involved. The crash happens in the server process at startup, before
  any RPC is served.
- The checker (`entrypoint.sh`) fires only on a literal `panic:` or `fatal error:` line. It cannot fire on
  a clean error exit. When `verifySchema` returns an error, fx prints it and the process exits with
  rc 1 and no `panic:` line. So the assertion fires only because of the `panic()` call.

Result: no checker or workflow-code artifact.

## Check 5: reproduction across runs and versions

- Fix-verify run (v1.4.5 plus 3 patches): all 119 examples are **one** incident on cadence3, vtime 404.70
  (`crash.json`, a uniq count gives 119 identical `details`).
- Baseline v1.4.5 f04f3907: the RCA says 8 of a 999-example sample are this panic, on all three nodes
  (`rca/fixverify-residuals.md:341`). **No raw evidence for this is on disk.** A grep for
  `close connection` under `logs/` matches only `fixverify-residuals/`. The baseline count rests on the
  RCA's statement alone.
- Master was not observed. The code is identical there (`src/cadence-master/tools/sql/conn.go:121`).
- The fix was not verified by a run. The diff is trivial and obviously removes the panic.

Result: seen in one incident on disk. Recurrence on the baseline is claimed but not archived. The
code path is deterministic given a close error.

## Strongest defense, and why it holds only in part

**Defense:** the panic replaces the error that the version check returned, so the evidence cannot
tell whether the check itself failed. In `doCheckCompatibleVersion`, `Close` is deferred and runs even
when `schema.VerifyCompatibleVersion` returned an error. Under a degraded link, the check query can
fail on the same broken connection that then fails to close. In that case:
1. `verifySchema` returns an error,
2. fx Start fails,
3. `runServices` (`cmd/server/cadence/cadence.go:255-300`) cancels the other services, and
4. the process exits with rc 1.

The supervisor restarts it 2 s later either way. On this reading the panic changes only the exit
code and log text, so the "crash" is cosmetic.

**Why it does not fully hold:**
- (a) The close can fail when the check succeeded. lib/pq Close performs its own write and socket
  close, so it can fail independently of the query. In that case the panic turns a healthy startup
  into a process death. The process runs four services, each with its own fx app and its own
  `verifySchema` (`cadence.go:177-187`, `261-283`). The log shows one service's `verifySchema` reporting
  "ran successfully" 2 ms before the panic (`crash-slice.ndjson`, vtime 404.693 then 404.6955), so
  other services on the node were healthy, and one panicking goroutine kills all of them.
- (b) In the failure case, the panic discards the real error. Operators see "cannot close connection"
  instead of the reason the schema check failed, which is a diagnosability regression.
- (c) Panicking on a recoverable I/O error contradicts how the rest of the persistence layer handles
  errors, which is to return them. The `panic` adds nothing over returning or logging the error.

## Severity for Uber engineers

**Low.** There is no data loss and no correctness violation. The failure is confined to startup, and a
supervisor recovers it with one extra restart. The cost is:
- needless restarts, which can become a crash loop while the database link stays flaky during a rollout,
- co-located services taken down with the crashing one,
- a misleading crash message that hides the real schema-check error.

The fix is a few lines: ignore or log the close error in `tools/sql/conn.go` `Close()` (see
`antithesis/fixes/sql-conn-close-no-panic.diff`). Worth reporting as a small robustness and hygiene issue.
