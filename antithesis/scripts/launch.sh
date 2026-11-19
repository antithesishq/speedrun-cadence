#!/bin/bash
# Usage: launch.sh <config-label> <compute-hours> <purpose> [tier] [fault-tolerance] [exclusions-regex]
# Validates and launches one run; prints the run ID. Record it in scratchbook/runs.tsv.
set -euo pipefail
HERE=$(cd "$(dirname "$0")/.." && pwd)
L=$1; CH=$2; PURPOSE=$3; TIER=${4:-standard}; FT=${5:-high_availability}; EXCL=${6:-^driver$}
CFG=$HERE/config-$L
snouty validate "$CFG" --timeout 3m > "$HERE/scratchbook/logs/validate-$L.txt" 2>&1 || { tail -20 "$HERE/scratchbook/logs/validate-$L.txt"; exit 1; }
OUT=$(snouty launch --launcher basic_test --config "$CFG" --json \
  --test-name "cadence-$L" --description "$PURPOSE" --source "cadence-$L" \
  --max-compute-hours "$CH" --performance-tier "$TIER" \
  --fault-tolerance "$FT" --fault-exclusions "$EXCL" --setup-timeout 10m \
  --param attrs.plugin_version=0.2.0 --param attrs.sut=cadence --param attrs.version="$L" \
  --param attrs.purpose="$PURPOSE")
echo "$OUT" > "$HERE/scratchbook/logs/launch-last.json"
RID=$(echo "$OUT" | jq -r '.runId // .run_id // .id // empty')
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$(date -u +%FT%TZ)" "$RID" "$L" "$CH" "$TIER" "$FT" "$PURPOSE" >> "$HERE/scratchbook/runs.tsv"
echo "$RID"
