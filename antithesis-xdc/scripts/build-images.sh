#!/bin/bash
# Usage: build-images.sh [tag]   -> cadence-client-xdc:<tag>, cadence-driver-xdc:<tag> (default latest)
# Server images are shared with the single-cluster harness: ../antithesis/scripts/build-server.sh.
set -euo pipefail
HERE=$(cd "$(dirname "$0")/.." && pwd); T=${1:-latest}
docker build -t "cadence-client-xdc:$T" "$HERE/client"
docker build -t "cadence-driver-xdc:$T" "$HERE/driver"
