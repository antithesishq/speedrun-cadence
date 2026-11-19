# RCA: cadence node exits (exit 1 / exit 2) and the post-fault liveness failure

Runs: v1.4.5 `89a272ed819536288c2f7b7835443da6-67-6`, master `0a677a0182adaad0264734a959537e64-67-6`
(both in progress at analysis time, vtime up to ~500).
Raw evidence: `antithesis/scratchbook/logs/node-exits/` (ndjson slices, search outputs, exec outputs).

## Summary

| Signature | Exit | Versions | Classification |
|---|---|---|---|
| `sigterm-before-fx-handler` (Go PID 1 dies from SIGTERM via `exit(2)`) | 2 | both | harness |
| `history-preparetostop-nil-controller` (panic in shutdown during startup) | 2 | both | real (low severity) |
| `frontend-stopratelimiters-nil-collection` (panic in shutdown during startup) | 2 | master observed; same code in v1.4.5 | real (low severity) |
| `startup-dependency-failfast` (`logger.Fatal` / fx start error when DB or ring is unavailable) | 1 | both | expected (made visible by the harness, which does not restart) |
| `postmaster-killed` (postgres `FATAL: terminating connection due to unexpected postmaster exit`) | n/a | both | expected (fault injector `node/kill` on postgres) |
| `probe-start-not-retried` (v1.4.5 liveness) | n/a | v1.4.5 | harness (checker) |
| `worker-not-restarted-after-client-restart` (master liveness) | n/a | master | harness |

**Recommended harness changes** (details in "Recommendations"):
1. Run cadence-server under a supervisor in the entrypoint. It should forward SIGTERM, restart on crash with backoff, and turn a Go panic into an Antithesis assertion.
2. Restart the worker in the client whenever the client process starts.
3. Retry `start` for the driver's liveness probe.

Keep postgres in the fault set.

---

## Q1. Why does the postgres postmaster exit? (`postmaster-killed`)

The fault injector killed it. The earlier summary missed the kill line. From `logs/iter1/exit-cadence3.log`:

```
25.33838 [fault_injector] FAULT node/kill max=5.5s nodes=cadence3
31.13274 [containers_meta] container init cadence3
31.13274 [containers_meta] container start cadence3
31.13274 [containers_meta] container kill postgres           <-- SIGKILL to postgres
31.13325 [postgres] ... FATAL: terminating connection due to unexpected postmaster exit   (x17 backends)
31.15383 [fault_injector] FAULT node/kill max=3.2s nodes=postgres
31.52666 [containers_meta] container died postgres exit=137
31.52666 [containers_meta] container died cadence3 exit=1
```

The `FAULT` event is logged about 20 ms after the `container kill` it caused. The same lag appears for stop faults (see Q2).
The FATAL lines are what postgres backends print when their postmaster is SIGKILLed. There is no shm, OOM or max_connections problem.
Across the runs, postgres died 50 times (v1.4.5) and 58 times (master) with 137 (kill fault), and 99 / 84 times with exit 0 (`node/stop` fault = SIGTERM, clean shutdown). Antithesis restarts it after `max_duration`.

Verdict: legitimate fault behaviour, not an environment artifact and not a config problem.
cadence3 crashed because the injector restarted it at the same instant it killed postgres. Startup then failed fast with exit 1 (Q3), and nothing restarted it.

## Q2. Exit code 2

Every exit-2 `died` event in both runs follows a `node/stop` fault on the same container. A per-timeline fold covered 22 distinct exit-2 deaths in v1.4.5 and 26 in master; the whole-run counts are 37 and 29 (`died-ctx-*.json`, `died-all-*.json`).
- Usually the stop is 0.1 to 0.5 s before the death.
- In 4 cases the `FAULT node/stop` line is logged 1 to 15 ms after the `died` event, the same logging lag as above. For example, v1.4.5 177.2045 died / 177.2075 FAULT stop cadence1; master 110.9264 / 110.9379 and 114.9259 / 114.9259.
- `node/stop` is a graceful stop (SIGTERM). Evidence: clients and postgres exit 0 under it, and `node/kill` gives 137.

Exit 2 therefore means "SIGTERM arrived". There are two distinct ways it becomes exit code 2.

### sigterm-before-fx-handler  (harness)

Most exit-2s (all but the 6 panics below) print nothing after the SIGTERM. Two facts explain this:
- The entrypoint `exec`s cadence-server, so it is **PID 1** (`antithesis/server/entrypoint.sh:8`).
- fx installs its SIGTERM handler only **after** all OnStart hooks succeed: `app.start` calls `app.lifecycle.Start` and then `app.receivers.Start` (go.uber.org/fx app.go, `start()`, checked against fx v1.22. The stacks show v1.23, which has the same order).

Until then Go handles SIGTERM itself. `runtime.dieFromSignal` re-raises the signal. The kernel ignores a default-action signal sent to PID 1, so the Go 1.26 runtime falls through to `exit(2)`. The image is built with `golang:1.26` (`antithesis/server/Dockerfile:3`), and go1.26.0 `src/runtime/signal_unix.go` `dieFromSignal` ends with `exit(2)`. go1.27 changed this to `exit(128+sig)`.

The cadence startup window without a handler is long. The dig provide phase runs `dynamicconfigfx.New`, which reads postgres. The `verifySchema` OnStart hook regularly takes 3 to 26 s under faults. Examples:
- v1.4.5 cadence1 26.53: in `verifySchema`, stop at 26.18.
- v1.4.5 cadence3 121.53: only `Loading configFiles`, stop at 121.43.
- v1.4.5 cadence2 217.28: in `verifySchema`, stop at 216.95.
- master cadence2 44.53: in the provide phase, stop at 44.29.
- master cadence3 126.03: in `verifySchema`, stop at 125.61.

Not a Cadence bug: it is PID 1 semantics plus the Go runtime. Each of these deaths happens inside a stop window, and Antithesis restarts the container when the window ends: 73 / 56 restarts after exit 2, mean gap 4 to 7 s (`restarts-*.json`). They cost availability only briefly.

### history-preparetostop-nil-controller  (real, low severity; v1.4.5 and master)

Seen 3 times in v1.4.5 (cadence3 167.17, cadence1 177.19, cadence1 227.33) and once in master (cadence2 111.22). Each one is a few ms after SIGTERM from a stop fault and right after `[Fx] HOOK OnStop ... (*App).Stop-fm() executing`.

```
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x30 pc=0x34a89fa]
goroutine 3731 [running]:
github.com/uber/cadence/service/history/handler.(*handlerImpl).PrepareToStop(0x1fef0da8c960, 0x0)
	service/history/handler/handler.go:234 +0x7a
github.com/uber/cadence/service/history/wrappers/ratelimited.(*historyHandler).PrepareToStop(...)
	service/history/wrappers/ratelimited/handler_generated.go:129 +0x52      (master: :133)
github.com/uber/cadence/service/history.(*Service).Stop(0x1fef0ce88640)
	service/history/service.go:156 +0x16a
github.com/uber/cadence/cmd/server/cadence.(*server).Stop(...)
	cadence/server.go:116 +0x103                                              (master: :117)
github.com/uber/cadence/cmd/server/cadence.(*App).Stop(...)
	cadence/fx.go:147 +0x4c
go.uber.org/fx/internal/lifecycle.(*Lifecycle).runStopHook(...)  fx@v1.23.0/internal/lifecycle/lifecycle.go:341
go.uber.org/fx/internal/lifecycle.(*Lifecycle).Stop(...)         lifecycle.go:303
go.uber.org/fx.(*App).Stop.func2(...)                             app.go:725
go.uber.org/fx.withTimeout.func1()                                app.go:803
```
(The full stacks for all 5 sampled panics are in `logs/node-exits/panics.txt`.)

Mechanism. Line numbers are identical in `src/cadence-v1.4.5` and `src/cadence-master`.
1. `App.Start` creates the server and returns immediately. `server.startService` runs the daemon in a goroutine: `go execute(daemon, s.doneC)` at `cmd/server/cadence/server.go:300` (master `:292`). fx therefore considers the app started and installs the signal handler while `history.Service.Start` is still running.
2. `history.Service.Start` (`service/history/service.go:87`) does three things in order:
   - CASes status to Started.
   - Assigns `s.handler` (`:107`).
   - Calls the blocking `s.Resource.Start()` (`:120`). This waits on membership, the domain cache and so on, which under faults takes seconds.

   Only after that does it call `s.handler.Start()` (`:121`). `handlerImpl.Start` is what assigns `h.controller` (`service/history/handler/handler.go:155`).
3. SIGTERM in that window triggers `Service.Stop` (`service.go:129`). The CAS succeeds, and Stop calls `s.handler.PrepareToStop` (`:156`), which then calls `h.controller.PrepareToStop()` on a nil interface at `handler.go:234`. `addr=0x30` is consistent with a method load from a nil itab.

Evidence that it is startup-time:
- master cadence2 111.22: `Start-fm ... ran successfully` lines appear at 108.88 to 110.21, then OnStop at 111.21, then the panic.
- master cadence3 87.34: `Start-fm` ran successfully at 86.49 and the panic followed 0.85 s later.
- v1.4.5 cadence3 167.17: the node was serving `Internal service error` responses throughout the prior 6 s, so history had not finished starting.

Classification: **real Cadence bug**. It is a nil dereference on a reachable path: graceful shutdown requested while the history service is still starting. Start and Stop also race on `h.controller` with no synchronization, which is a data race as well.
Impact is low: the process was being terminated anyway. What goes wrong:
- It exits 2 instead of draining.
- It skips `EvictSelf` and the shard-ownership hand-off steps, so peers detect it only through ringpop failure.
- It hides any later shutdown work.

This is not a deliberate fail-fast. Worth filing upstream. A fix would guard `h.controller == nil` in `PrepareToStop`/`Stop`, or make Stop wait for Start.

### frontend-stopratelimiters-nil-collection  (real, low severity; observed on master, same code in v1.4.5)

Seen twice in master (cadence3 87.34 and cadence2 289.46), each right after a stop-fault SIGTERM and shortly after `Start-fm` returned.

```
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x88 pc=0x3161fcf]
goroutine 502 [running]:
github.com/uber/cadence/common/quotas/global/collection.(*Collection).OnStop(0x0, {...})
	common/quotas/global/collection/collection.go:255 +0x6f
github.com/uber/cadence/service/frontend.(*Service).stopRatelimiters(0x21718f88db00)
	service/frontend/service.go:370 +0xa5
github.com/uber/cadence/service/frontend.(*Service).Stop(0x21718f88db00)
	service/frontend/service.go:357 +0x1e5
github.com/uber/cadence/cmd/server/cadence.(*server).Stop(...)  cadence/server.go:117
github.com/uber/cadence/cmd/server/cadence.(*App).Stop(...)     cadence/fx.go:147
... fx lifecycle Stop / withTimeout (as above)
```

Mechanism: the same async-start race.
- master `service/frontend/service.go`: `Start` assigns `s.handler` at `:155`, then blocks in `s.Resource.Start()` at `:194`, and only saves `s.ratelimiterCollections = collections` at `:220`.
- `Stop` (`:357` calls `stopRatelimiters`) calls `s.ratelimiterCollections.user.OnStop(ctx)` at `:370` on a nil `*Collection`. That nil receiver is dereferenced at `common/quotas/global/collection/collection.go:255` (`c.ctxCancel()`).
- v1.4.5 has the same code: `service/frontend/service.go:180` Resource.Start, `:206` assignment, `:343` stopRatelimiters, `:356` OnStop. It just has not hit this in the run yet.

Classification: **real Cadence bug**, same family and severity as above. File them together upstream as "SIGTERM during service startup panics in Stop".

## Q3. Exit code 1

Exit 1 is always a fail-fast at startup. There are two paths:
- `logger.Fatal` (zap calls os.Exit(1)).
- An fx start error, returned by `runServices` (`cmd/server/cadence/cadence.go:251`) and turned into exit 1 by `commoncli.ExitHandler` (`tools/common/commoncli/cli.go:59`).

Each one is caused by a dependency that is unreachable during startup (`exit1-ctx-*.json`, last messages before each exit-1 death):

| Message | Site (same in both trees) | Cause |
|---|---|---|
| `creating operational dynamic config store: EOF / timeout error on fetch / no such host` (fx start error) | `common/dynamicconfig/dynamicconfigfx/fx.go:87` | postgres down / partitioned / DNS for killed container |
| `sql schema version compatibility check failed` (fx start error) | `cmd/server/cadence/fx.go` verifySchema | postgres down |
| `Unable to initialize domain cache` (GetMetadata / ListDomains timed out) | `common/cache/domainCache.go:299` | DB slow/unreachable |
| `could not start config store: timeout error on fetch` | `common/dynamicconfig/configstore/config_store_client.go:397` | DB slow/unreachable |
| `Fail to start <svc> service` (connection reset, EOF, no such host) | `cmd/server/cadence/server.go:297` | DB reset mid-start |
| `failed to verify if system domain exists: sql: database is closed` | `service/worker/service.go:538` | DB / shutdown in progress |
| `failed to start peer provider: bootstrap ringpop: join duration exceeded max 30s` | `common/membership/resolver.go:133` | network partition from peers (ring, not DB) |
| `failed to start peer provider: bootstrap ringpop: node destroyed while attempting to join` | `common/membership/resolver.go:133` | ringpop destroyed during join (shutdown/cancel during startup) |

So exit 1 is mostly DB unavailability. The exceptions are ringpop bootstrap failures from partitions, and shutdown racing startup.
All of these are deliberate fail-fast behaviour, which Cadence expects a supervisor to handle (Kubernetes restarts it).
What turns them into permanent outages here: exits that happen outside a stop/kill window are not restarted by Antithesis. An example is cadence3 in `exit-cadence3.log`, and in master cadence1 dies at 42.86, right as faults stop, and stays down.
The whole runs have 103 (v1.4.5) and 109 (master) exit-1 deaths but only 19 and 9 restarts that follow an exit-1 (`died-all-*.json`, `restarts-*.json`); those are exits that fell inside a stop window.

## Q5. "a new workflow completes within 2 minutes once faults stop"

Neither failing example is explained by dead nodes.

**v1.4.5** (`8070392237796148924` @210.96):
- When faults stopped at 90.41, all three cadence containers and postgres were up. The last cadence restart was cadence1 at 54.0, and there were no deaths after that.
- The driver sends one `start` with a 30 s timeout (`antithesis/driver/main.go:491`). Its StartWorkflowExecution attempts failed from 95.5 to 111.8 with:
  - frontend `grpc: the client connection is closing` / `Frontend request timedout`
  - history `AppendHistoryNodes ... Failed to start transaction. Error: context canceled / deadline exceeded`

  Stale DB connections after the partitions (`read tcp ... connection reset`, `driver: bad connection`) were still clearing until about 120 to 140.
- The driver never retries `start`. It polls `result` for 120 s on a workflow that does not exist.
- Exec at the failing moment confirms it: `cadence workflow describe -w probe-1795029443601449249` returns `GetCurrentExecution failed. Error: sql: no rows in result set` (`exec-v145-probe.txt`).

Classification: harness (checker). The "every client is served again" gate passed in 66 ms because `describe_domain` is served from the domain cache, so it does not show that the write path has recovered.

**master** (`4692074680276153158` @165.79):
- cadence1 died with exit 1 at 42.86 and was never restarted. cadence2 and cadence3 were up, and membership correctly shows 2 members per ring (`exec-m-tl.txt`).
- The probe *was* created (`probe-1795029431808821126`, 19:17:12). Its history stays at `WorkflowExecutionStarted, DecisionTaskScheduled` for the full 120 s (`exec-m-probe.txt`).
- `cadence tasklist describe --tl tl-main` returns **`No poller for tasklist: tl-main`**.
- All three client containers were killed or restarted at 42.2 to 43.5 (`client1/2/3 init/start`). Workers are started only once, by `driver.startup()` sending `start_worker` (`antithesis/driver/main.go:106`). `client/main.go` does not start a worker on boot (`startWorker` at `client/main.go:86` runs only on RPC `start_worker`, `:291`). The reconnect path in `driver/rpc.go:126-150` does not re-send it.
- After the restarts no client polls `tl-main`, so no workflow can make progress.
- The dead cadence1 is not the cause: 2/3 nodes own all shards and task-list partitions (`tl-main` partitions on .12 and .13).

Classification: harness. The same gap affects v1.4.5: client1 restarted at 50.78 there and lost its worker. Two workers were left, so it was not decisive.

## Recommendations

1. **Supervisor in `antithesis/server/entrypoint.sh`** (the most important change). Replace the `exec` with a bash loop that keeps PID 1 for itself:
   - Run `cadence-server ... &` and `wait` on it.
   - `trap` TERM/INT: set `stopping=1`, `kill -TERM $child`, wait, then exit 0. Because the child is no longer PID 1, an early SIGTERM kills it normally. That removes `sigterm-before-fx-handler`.
   - On child exit without `stopping`, log `cadence-server exited rc=$rc, restarting`, sleep 1 to 2 s (with backoff), and loop. This makes `startup-dependency-failfast` self-heal, as `restartPolicy: Always` does in Kubernetes. Retrying startup inside cadence is not needed.
   - Keep panics visible as findings. When rc=2 and stdout/stderr contain `panic:`, emit an SDK assertion line to `$ANTITHESIS_OUTPUT_DIR/sdk.jsonl` (an `unreachable` "cadence-server does not panic", with the first frames as details). Alternatively, tee output and let the driver or a sidecar assert on it. Check the fallback-SDK JSON format before relying on it.
   - Expected effect: the "No unexpected container exits" failures for cadence disappear, apart from real panics. The two shutdown-race panics will still fire after SIGTERM and remain a real finding.
   - Alternative or extra: add `restart: on-failure` to cadence1..3 in `config-template/docker-compose.yaml` (Antithesis honors restart policies). But the container exit would still trip "No unexpected container exits", so the in-container loop is better.
2. **Client worker restart**:
   - Start the worker in `client/main.go` at process boot, in a goroutine that retries `startWorker()` until it succeeds.
   - Or have the driver re-send `start_worker` whenever a client connection is (re)established, i.e. on a new `gen`.
   - Without this, every client restart silently removes a poller, and liveness checks fail for harness reasons.
3. **Liveness probe**:
   - Retry `start` (with `reuse`, treating `already_started` as success) until it succeeds or the 120 s budget ends. Then poll `result`.
   - Gate the "served again" step on a write (for example a start of a throwaway workflow), not `describe_domain`.
   - Optionally wait until all three cadence containers answer health before starting the 120 s clock. With item 1 in place they will come back.
4. **Postgres**: keep it in the fault set (kill/stop/pause/partition are legitimate faults for the single DB). It needs no config change: shm, max_connections and OOM are not involved. Once item 1 is in place, DB outages become recoverable. If DB kills are too frequent to leave time for work, lower the kill/stop weight for postgres rather than excluding it.
5. **Report upstream**: the two `*-nil-*` panics. One report: "SIGTERM while a service is in Start() panics in Stop(): history handler.go:234 / frontend service.go:370 → collection.go:255". It reproduces on v1.4.5 and master @572e7e2c.
