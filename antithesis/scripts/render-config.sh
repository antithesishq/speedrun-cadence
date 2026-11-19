#!/bin/bash
# Usage: render-config.sh <server-label> [client-tag] [driver-tag] -> antithesis/config-<server-label>/
set -euo pipefail
HERE=$(cd "$(dirname "$0")/.." && pwd); L=$1; C=${2:-latest}; D=${3:-latest}
OUT=$HERE/config-$L; mkdir -p "$OUT"
sed -e "s/__VERSION__/$L/g" -e "s/__CLIENT_TAG__/$C/g" -e "s/__DRIVER_TAG__/$D/g" "$HERE/config-template/docker-compose.yaml" > "$OUT/docker-compose.yaml"
cp "$HERE/config-template/cadence.yaml" "$HERE/config-template/dynamicconfig.yaml" "$OUT/"
echo "$OUT"
