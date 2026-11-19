#!/bin/bash
# Usage: local-run.sh <server-label> [workload-seconds] [eventually-after-s]
# Runs config-xdc-<label> locally (eventually rule off; with eventually-after-s, the eventually is
# called directly after that many seconds of workload). Evidence goes to scratchbook/logs/local-<label>-<ts>/.
set -uo pipefail
HERE=$(cd "$(dirname "$0")/.." && pwd); L=$1; SECS=${2:-180}; EV=${3:-}
CFG=$HERE/config-xdc-$L
OUT=$HERE/scratchbook/logs/local-$L-$(date -u +%Y%m%dT%H%M%S); mkdir -p "$OUT"
cat > "$OUT/override.yaml" <<YAML
services:
  driver:
    environment:
      ANTITHESIS_SDK_LOCAL_OUTPUT: /tmp/sdk.jsonl
      EVENTUALLY_OFF: "1"
      EVENTUALLY_AFTER_S: "${EV}"
YAML
DC="docker compose -f $CFG/docker-compose.yaml -f $OUT/override.yaml"
$DC down -v >/dev/null 2>&1
$DC up -d || exit 1
$DC exec -T driver /opt/antithesis/test/v1/main/first_start_workload
echo "waiting for setup complete"
timeout 600 sh -c "until $DC exec -T driver grep -qs antithesis_setup /tmp/sdk.jsonl 2>/dev/null; do sleep 5; done" || echo "setup did not complete"
echo "setup complete; running $SECS s"
sleep "$SECS"
$DC cp driver:/tmp/sdk.jsonl "$OUT/sdk.jsonl"
for s in c0-cadence1 c0-cadence2 c1-cadence1 c1-cadence2; do
  $DC cp "$s:/tmp/sdk.jsonl" "$OUT/sdk-$s.jsonl" 2>/dev/null || true
done
$DC logs --no-color > "$OUT/compose.log" 2>&1
$DC ps -a > "$OUT/ps.txt"
$DC down -v >/dev/null 2>&1   # leave nothing behind: snouty validate refuses a project with leftover containers
"$HERE/scripts/sdk-summary.sh" "$OUT"/sdk*.jsonl > "$OUT/summary.txt"
grep -c . "$OUT/summary.txt"; grep FAIL "$OUT/summary.txt"
echo "$OUT"
