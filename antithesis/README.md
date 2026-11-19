# Cadence under Antithesis

Black-box workload for [Cadence](https://github.com/cadence-workflow/cadence): an unmodified
Cadence server (built with Antithesis coverage instrumentation only) on Postgres, driven through
the official Go client. Versions: `v1.4.5` (release) and `master` @ `572e7e2c`.

## Deployment (`config-template/docker-compose.yaml`)

| Container | What |
|---|---|
| `postgres` | Postgres 17.4: default store and DB visibility |
| `schema` | one-shot: `cadence-sql-tool` creates and migrates both schemas |
| `cadence1..3` | `cadence-server` with all four services (frontend, history, matching, worker); ringpop membership; 8 history shards |
| `client1..3` | the client program ([client/](client/)): one Cadence Go client + worker each, serving the driver over `/clients/<name>.sock` (stateful-clients pattern) |
| `driver` | the hegel driver ([driver/](driver/)); fault-excluded |

Config: [cadence.yaml](config-template/cadence.yaml), [dynamicconfig.yaml](config-template/dynamicconfig.yaml).

## Workflows (client/workflows.go)

`counter` (signals, queries, continue-as-new, cancellation, reset), `chain` (activities with
retries, heartbeats, parallel and local activities), `timer`, `parent` (child workflows), `race`
(timers racing long local activities, which forces decision heartbeats).

## Checks (driver/model.go, driver/main.go)

Every result, history and description is checked against the driver's model when it arrives:
acknowledged signals applied exactly once and in ack order; results equal what the inputs
determine; timers never early; histories well formed (contiguous IDs, one close event, each
timer/activity/child resolved at most once, no duplicate signal events); duplicate start of an open
ID rejected; closed runs stay closed. The eventually stops faults and checks that clients are served,
a new workflow completes within 2 minutes, every started workflow closes, and visibility lists every
closed run.

## Scripts

- `scripts/build-server.sh <label> <src>`: instrumented server image `cadence-server:<label>`
- `docker build -t cadence-client:latest client`, `docker build -t cadence-driver:latest driver`
- `scripts/render-config.sh <label>` → `config-<label>/`
- `scripts/launch.sh <label> <compute-hours> <purpose> [tier]`
- `scripts/sdk-summary.sh sdk.jsonl`: local assertion summary
