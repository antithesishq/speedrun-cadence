#!/bin/bash
# Usage: build-server.sh <version-label> <cadence-src-dir>   -> image cadence-server:<label>
set -euo pipefail
LABEL=$1; SRC=$2; HERE=$(cd "$(dirname "$0")/.." && pwd)
docker buildx build --load -f "$HERE/server/Dockerfile" --build-context harness="$HERE/server" \
  -t "cadence-server:$LABEL" "$SRC"
