#!/bin/bash
# One-shot (runs once during setup): create and migrate the Cadence + visibility schemas on Postgres.
set -eu
T="cadence-sql-tool --plugin postgres --ep ${PG_HOST:-postgres} -p 5432 -u cadence --pw cadence"
until pg_isready -h "${PG_HOST:-postgres}" -U cadence -q; do echo "waiting for postgres"; sleep 1; done
sleep 1
$T create --db cadence || true
$T --db cadence setup-schema -v 0.0
$T --db cadence update-schema -d /etc/cadence/schema/postgres/cadence/versioned
$T create --db cadence_visibility || true
$T --db cadence_visibility setup-schema -v 0.0
$T --db cadence_visibility update-schema -d /etc/cadence/schema/postgres/visibility/versioned
echo "schema ready"
