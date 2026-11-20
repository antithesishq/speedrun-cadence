# Patches

All patches apply to Cadence v1.4.5 (`ecd2b70c`); the code on each finding's path is identical on master `572e7e2c`.

| File | Finding(s) | Status |
|---|---|---|
| `combined-final.patch` | everything in `combined-fixverify4.patch` + the last Stop() guards (frontend/service.go:335, history/service.go PrepareToStop/Stop) | **Use this one.** Unit tests pass; the added guards are the same pattern as the verified ones but were not run end to end |
| `combined-fixverify4.patch` | all server findings below (except stale history and signal ordering) + Stop() guards + signal dedup across continue-as-new | Exactly what fix check `68cc2e9d` ran: reset/signal patches verified, Stop guards verified except frontend/service.go:335 |
| `combined-fixverify3.patch` | earlier combined set | **Do not use: breaks every reset** (base-run read without shard ID); superseded by v4 |
| `retried-signal-duplicated-after-reset.patch` | retried-signal-duplicated-after-reset (v1) | superseded by `.v2` |
| `retried-signal-duplicated-after-reset.v2.patch` | + retried-reset-applied-twice | included in v4 |
| `acked-signal-lost-on-reset.patch` | acked-signal-lost-on-reset | included in v4 |
| `sigterm-during-startup-panics.patch` | sigterm-during-startup-panics (history, frontend rate limiters) | included in v4 (v4 adds 3 more sites) |
| `reset-copies-base-dedup-set.diff` | hole in v1 dedup | **buggy as written** (missing shard ID); corrected in v4 |
| `reset-dedup-across-can.diff` | reset retry after the reset run continued as new | included in v4 |
| `signal-dedup-across-can.diff` | retried-signal-duplicated-after-continue-as-new | included in v4 (semantics change; no size cap as written, add one before adopting) |
| `sql-conn-close-no-panic.diff` | sql-admin-conn-close-panic | included in v4 |

The Go-client findings (retried-decision-heartbeat-stalls-workflow, child-cancel-ack-after-close) have proposed fixes in their root-cause files, not patches. stale-history-after-shard-steal and buffered-signals-reordered-on-reload have proposed fixes in their root-cause files.
