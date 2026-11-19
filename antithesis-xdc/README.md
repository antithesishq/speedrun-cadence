# Cadence XDC (cross-cluster replication) under Antithesis

Extension of the single-cluster harness in [../antithesis](../antithesis) to two Cadence clusters
replicating one **global domain**, with failovers. Black-box: unmodified Cadence (the same
instrumented images `cadence-server:v1.4.5` / `cadence-server:master`), configuration and dynamic
config only. Nothing in `../antithesis` or `../src` is changed.

## How Cadence replicates here

- **No Kafka.** In v1.4.5 (and master) replication is pull-based over RPC: each history shard's
  replication task fetcher pulls tasks from the *other* cluster's frontend (admin API), and the
  worker service's domain replication processor pulls domain changes (registration, failover) from
  the other cluster's domain replication queue (a DB queue). Kafka is only used for ES visibility,
  which we don't run (DB visibility).
- **Cluster redirection:** `clusterRedirectionPolicy: all-domain-apis-forwarding`. A request for the
  global domain that reaches the passive cluster is forwarded to the active one, including worker
  polls, so `xclient3` (cluster1 only) runs workflows and activities wherever the domain is active.
  `RegisterDomain`, `UpdateDomain`, `DescribeDomain` and the admin API are never forwarded.

## Deployment (`config-template-xdc/`)

Compose project `cadence-xdc`, subnet `10.30.30.0/24`, every container name distinct from the
single-cluster harness (both can run on one host).

| Container | IP | What |
|---|---|---|
| `c0-postgres`, `c1-postgres` | .5, .6 | Postgres 17.4, one per cluster |
| `c0-schema`, `c1-schema` | .7, .8 | one-shot schema setup (`setup-schema.sh` with `PG_HOST`) |
| `c0-cadence1..2` | .11, .12 | cluster0: all four services each, ringpop `cadence_cluster0`, 8 shards; network alias `cluster0-frontend` |
| `c1-cadence1..2` | .21, .22 | cluster1: same, ringpop `cadence_cluster1`; alias `cluster1-frontend` |
| `xclient1`, `xclient2` | .31, .32 | client program, `CADENCE_FRONTENDS` = cluster0's frontends |
| `xclient3` | .33 | client program, `CADENCE_FRONTENDS` = cluster1's frontends |
| `xdriver` (service `driver`) | .40 | hegel driver; fault-excluded (`--fault-exclusions '^xdriver$'`) |

Config files: `cadence-c0.yaml` / `cadence-c1.yaml` (identical except DB host, ringpop name and
hosts, `currentClusterName`, and which `rpcAddress` is local), `dynamicconfig.yaml` (the
single-cluster file plus a short XDC section).

- `clusterGroupMetadata`: `primaryClusterName: cluster0`, `failoverVersionIncrement: 10`,
  `initialFailoverVersion` 0 (cluster0) and 2 (cluster1). Each cluster's own `rpcAddress` is
  `__HOST_IP__:7833`; the other cluster's is `clusterN-frontend:7833`, a Docker network alias that
  resolves to both of that cluster's frontends. Cadence's DNS peer chooser round-robins over the
  A records and refreshes them, so replication survives losing either remote frontend.
- **Postgres: one container per cluster.** The image's `setup-schema.sh` hard-codes the database
  names (`cadence`, `cadence_visibility`) and only takes `PG_HOST`, so separate databases in one
  server would need a new script; and a real XDC deployment has an independent store per region,
  so a DB fault (kill, pause, partition) should only hit one cluster. Cost: one more container.
- Dynamic config additions: `history.ReplicationTaskFetcherServiceBusyWait` 5s (default 1m),
  `history.ReplicationTaskProcessorShardQPS` 50 (default 5), `frontend.failoverCoolDown` 10s
  (default 1m; the driver paces failovers itself), `frontend.domainFailoverRefreshInterval` 5s.

## Client (`client/`)

All methods and workflows of the single-cluster client, plus:

- `register_global_domain`: `IsGlobalDomain`, clusters `[cluster0, cluster1]`, active `cluster0`.
- `failover {cluster, graceful_s}`: `UpdateDomain` with `ActiveClusterName = cluster`;
  `graceful_s > 0` sets `FailoverTimeoutInSeconds` (graceful failover), 0 is a force failover.
- `describe_domain` now returns `{name, is_global, active_cluster, failover_version, cluster}`.
- `raw_history {wid, run_id}`: the run's history **from this client's cluster's own store**, via the
  admin API `GetWorkflowExecutionRawHistoryV2` (the workflow API's `GetWorkflowExecutionHistory`
  is forwarded to the active cluster, so it can't see the passive copy). Blobs are decoded
  (thriftrw `History`); each event is returned as id, type, version, timestamp and a digest of the
  whole event with `TaskId` cleared (a per-cluster sequence number).
- `CADENCE_CLUSTER` env (reported by `health`/`describe_domain`); which cluster a client talks to is
  set by its `CADENCE_FRONTENDS`.

## Driver (`driver/`)

Same rules, model and checks as the single-cluster driver, plus (`driver/xdc.go`):

- `CLIENT_CLUSTERS` (`xclient1=cluster0,...`) maps clients to clusters.
- **Startup:** registers the global domain on a cluster0 client, waits until `describe_domain` on
  every client (each answered by its own cluster) shows it global and active in cluster0, starts
  workers, then completes one probe workflow through cluster0 and one through cluster1 (forwarding).
- **`RuleFailover`:** behind a 2% draw and a 60 s cooldown (first one possible 30 s into the
  workload), flips the active cluster. Sent to a client of the cluster becoming active (Cadence
  requires that for graceful failover). 70% graceful (20 s timeout), 30% force.
- **`RuleDescribeDomain`:** tracks the active cluster; asserts the domain stays global.
- **Eventually** (after the existing checks): (a) both clusters report the same active cluster and
  failover version (`both clusters agree on the domain's active cluster once faults stop`), (b) for
  every closed run the driver knows, `raw_history` from a cluster0 client equals `raw_history` from
  the cluster1 client, retried for up to 3 minutes (`a closed workflow's history is the same in both
  clusters once faults stop`).
- New exploration assertions: `a global domain fails over to cluster0/cluster1`, `a graceful
  failover is accepted`, `a force failover is accepted`, `a workflow open during a failover closes`,
  `the passive cluster becomes active`, `closed workflow histories are compared across clusters`.

## Scripts

- `scripts/build-images.sh [tag]` → `cadence-client-xdc:<tag>`, `cadence-driver-xdc:<tag>`.
  Server images come from `../antithesis/scripts/build-server.sh`.
- `scripts/render-config.sh <server-label> [client-tag] [driver-tag]` → `config-xdc-<label>/`
  (rendered: `config-xdc-v1.4.5/`, `config-xdc-master/`).
- `scripts/local-run.sh <label> [seconds] [eventually-after-s]`: local run without faults
  (eventually rule off; optionally the eventually called directly), evidence and
  `summary.txt` in `scratchbook/logs/local-<label>-<ts>/`.
- `scripts/sdk-summary.sh`: assertion summary (copied from the main harness).
- `snouty validate config-xdc-<label> --timeout 8m`.

To launch (not done here): push the images, then use `../antithesis/scripts/launch.sh`-style flags
with `--config config-xdc-<label>` and `--fault-exclusions '^xdriver$'`.

## Validation (local, 2026-11-18)

- `snouty validate config-xdc-v1.4.5 --timeout 8m`: setup validation successful (global domain
  visible in cluster1 about 1 s after registration; probe workflows complete through both clusters).
  Output: `scratchbook/validate-v1.4.5.txt`.
- `snouty validate config-xdc-master --timeout 8m`: successful. `scratchbook/validate-master.txt`.
- v1.4.5, `local-run.sh v1.4.5 360 180` (`scratchbook/logs/local-v1.4.5-20261118T203855/`): two
  failovers (graceful cluster0→cluster1, force cluster1→cluster0), eventually after 180 s: 64 closed
  runs compared across clusters, 0 diverged; all 22 Always/Unreachable assertions pass; the only
  unmet assertions need faults (`a client restarts`, `a start returns an unknown outcome`,
  `a signal with an unknown outcome is applied`). Driver exited 0.
- v1.4.5, `local-run.sh v1.4.5 240 120` (`.../local-v1.4.5-20261118T204603/`): one graceful
  failover, 64 runs compared, 0 diverged, no Always failures.
- master, `local-run.sh master 240 120` (`.../local-master-20261118T205029/`): one force failover,
  59 runs compared, 0 diverged; one Always failure, `an acknowledged signal is applied exactly once`
  (counter-15, signal s13 acknowledged 17 ms before a reset, missing from the result; before any
  failover). That is the single-cluster harness's known finding `acked-signal-lost-on-reset`
  (`../antithesis/scratchbook/violations.tsv`), not XDC-related.

## Known limitations / decisions

- **Force failovers may legitimately break single-cluster checks.** A force failover can lose
  un-replicated progress on the old active cluster: Cadence's conflict resolution keeps the newer
  branch and re-applies only signals from the losing branch. A cancel or terminate acknowledged
  just before a force failover can then be lost, a closed run can effectively continue on the
  winning branch, and a re-applied signal plus a client retry could show up twice. The checks are
  unchanged (as asked); a violation must be triaged against the failover timing in the driver log
  (`xdc: failover ...`). Graceful failovers have no such window. Setting the force fraction to 0
  is a one-line change in `RuleFailover` if this turns out noisy.
- **Raw history comparison** compares the current branch on each side. Events are compared field
  by field except `TaskId`. If some other field turns out to be legitimately per-cluster, it shows
  up as a divergence in the details (`diff`).
- **DNS alias for the remote cluster** relies on Docker's embedded DNS returning every container on
  a network alias. If the Antithesis environment didn't honour aliases, setup would hang (domain
  never reaches cluster1) and `snouty validate`-style checks would catch it early in a run.
- Only cluster0 can register global domains (`primaryClusterName`), as in Cadence.
- Active-active domains, more than two clusters, and graceful failover cancellation are out of scope.
- `xclient3` is the only cluster1 client; the model treats it like the others.
