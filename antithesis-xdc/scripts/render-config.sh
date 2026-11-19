#!/bin/bash
# Usage: render-config.sh <server-label> [client-tag] [driver-tag] -> antithesis-xdc/config-xdc-<server-label>/
set -euo pipefail
HERE=$(cd "$(dirname "$0")/.." && pwd); L=$1; C=${2:-latest}; D=${3:-latest}
OUT=$HERE/config-xdc-$L; mkdir -p "$OUT"
sed -e "s/__VERSION__/$L/g" -e "s/__CLIENT_TAG__/$C/g" -e "s/__DRIVER_TAG__/$D/g" \
  "$HERE/config-template-xdc/docker-compose.yaml" > "$OUT/docker-compose.yaml"
cp "$HERE/config-template-xdc/cadence-c0.yaml" "$HERE/config-template-xdc/cadence-c1.yaml" \
   "$HERE/config-template-xdc/dynamicconfig.yaml" "$OUT/"
echo "$OUT"
