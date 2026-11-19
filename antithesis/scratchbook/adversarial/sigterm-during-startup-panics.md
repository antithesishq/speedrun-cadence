Verdict: stands

Finding: SIGTERM delivered while a Cadence service is still inside `Start()` panics in `Stop()` with a nil
dereference (history `handler.go:234` nil `h.controller`; frontend `service.go:370` nil
`ratelimiterCollections.*` -> `collection.go:255`). Process exits 2 instead of draining.

## Check 1: harness intervention between fault and symptom
- Original observations (rca/node-exits.md) were taken when `entrypoint.sh` `exec`ed cadence-server as PID 1.
  PID 1 changes only what happens to SIGTERM *before* fx installs its handler (Go `dieFromSignal` -> `exit(2)`,
  the separate `sigterm-before-fx-handler` signature). These panics happen *after* fx's handler is installed:
  the stack is fx `App.Stop` -> `lifecycle.Stop` -> `App.Stop` (cadence `cmd/server/cadence/fx.go:147`),
  i.e. fx received the signal normally. PID-1 semantics play no part.
- Re-observed with the new supervisor (`antithesis/server/entrypoint.sh`: cadence-server is a child, the trap
  `on_term` just forwards `kill -TERM`): `logs/iter3m/crash.log:12035-12064`, master cadence2 @334.54, same
  frontend stack, assertion details `"stopping":1` (the SIGTERM came from a `node/stop` fault, forwarded unchanged).
  The supervisor sends a single SIGTERM; nothing else touches the process between signal and panic.
- The fault itself is `node/stop` = plain SIGTERM (clients/postgres exit 0 under it, rca Q2). That is exactly what
  Kubernetes/systemd send.
- Defense fails.

## Check 2: configuration / tuning
- The window is `Resource.Start()` blocking between the field that Stop needs being nil and it being assigned:
  history `service/history/service.go:107` (handler assigned) / `:120` `s.Resource.Start()` / `:121` `handler.Start()`
  which assigns `h.controller` at `service/history/handler/handler.go:155`; frontend `service/frontend/service.go`
  master `:194` Resource.Start / `:220` `s.ratelimiterCollections = collections` (v1.4.5 `:180`/`:206`).
- Stop is reachable during that window because `server.startService` runs the daemon in a goroutine
  (`cmd/server/cadence/server.go:300` v1.4.5, `:292` master, `go execute(daemon, s.doneC)`), so fx's `App.Start`
  (`fx.go:140-142`) returns immediately and the signal handler is installed while Start still runs.
- Nothing in `config-template/dynamicconfig.yaml` (small history cache, 3 task-list partitions) controls this
  ordering. Tuning only changes how long `Resource.Start()` blocks (membership join, domain cache over a faulted DB);
  with defaults the window still exists and is seconds long whenever the DB or peers are slow (ringpop join can take
  up to 30 s by default). Would happen with Cadence defaults. Defense fails.

## Check 3: faults beyond the guarantee
- No guarantee in guarantees.md promises graceful shutdown; this is a crash-safety finding, not a guarantee check.
  The trigger is a single SIGTERM during startup, which is an ordinary operational event (rolling deploy that is
  cancelled, pod evicted while starting, crash-loop + scale-down). Slow startup is caused by DB/partition faults,
  which are in scope for a single-DB deployment. No exotic fault (clock, memory corruption) is involved.
- Defense fails.

## Check 4: can the checker fire on documented behaviour?
- Checker = supervisor greps `^(panic:|fatal error:)` in server output (`entrypoint.sh` `report_crash`), and the
  original detection was Antithesis "No unexpected container exits". Both fire on a genuine Go runtime panic
  (SIGSEGV nil dereference), confirmed by full stacks in `logs/node-exits/panics.txt` and `iter3m/crash.log`.
- Is panicking on SIGTERM-during-start documented/intended? No: `history.Service.Stop` deliberately does an
  orderly drain (`PrepareToStop`, `EvictSelf`, shard hand-off, comments at `service.go:~139-156`); a nil deref is
  not a fail-fast (`logger.Fatal` is used for that elsewhere). Note the checker does *not* fire on the many
  legitimate exit-1 fail-fasts (no `panic:` line), so it is not over-broad here.
- Weak spot: the same assertion id also covers other crashes; for this finding, only samples whose stack contains
  `PrepareToStop`/`stopRatelimiters` count. Defense fails for those samples.

## Check 5: reproduction
- v1.4.5 (old entrypoint): history panic 3x (cadence3 167.17, cadence1 177.19, cadence1 227.33).
- master @572e7e2c (old entrypoint): history panic 1x (cadence2 111.22), frontend panic 2x (cadence3 87.34,
  cadence2 289.46).
- master (supervisor entrypoint): frontend panic, cadence2 334.54, `logs/iter3m/crash.log`.
- prefix build: frontend `stopRatelimiters` stack in `logs/prefix1/unsent.log:6611` (cadence1 85.76).
- Multiple timelines, both versions, both entrypoints. The frontend v1.4.5 instance was not observed, but the code
  is identical (`service.go:180/206/343/356`), so it is the same bug. Source inspection confirms the race deterministically.

## Strongest defense
"The process was being terminated anyway; the panic is a cosmetic difference in exit path, and it only shows up
because Antithesis stops nodes during their startup far more often than production does." This holds for *impact*
(no data loss: the node was going away, durable state is in the DB and shards are re-acquired by peers after the
ringpop failure detector fires) but not for *validity*: it is a reachable nil dereference plus an unsynchronized
Start/Stop data race on `h.controller` / `s.ratelimiterCollections`, independent of the harness, config or fault
model.

## Severity to state to Uber
Low. Real bug, not a correctness or durability issue. Effects: exit 2 instead of a graceful drain; skipped
`EvictSelf`/shard-ownership hand-off (peers notice only via ringpop failure detection, briefly longer unavailability
of the shards it held); a Go data race between Start and Stop; noisy panics in crash-loop/rollback scenarios.
Suggested fix: nil-guard in `handlerImpl.PrepareToStop`/`Stop` and `frontend.Service.stopRatelimiters` (or make
`Collection.OnStop` nil-receiver safe), or have `Stop` wait for `Start` to finish.
