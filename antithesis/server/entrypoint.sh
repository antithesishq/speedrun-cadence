#!/bin/bash
# Supervisor for cadence-server (like Kubernetes restartPolicy: Always):
# - cadence-server runs as a child, so it is not PID 1;
# - SIGTERM/SIGINT are forwarded and the container then exits cleanly;
# - any other exit restarts it after a short backoff;
# - a Go panic or fatal error in its output is reported as an Antithesis assertion.
HOST_IP=$(hostname -i | awk '{print $1}')
export HOST_IP
sed "s/__HOST_IP__/${HOST_IP}/g" /config/cadence.yaml > /etc/cadence/config/docker.yaml
SDK_OUT="${ANTITHESIS_OUTPUT_DIR:-/tmp}/sdk.jsonl"
stopping=0; child=0
on_term() { stopping=1; [ "$child" -gt 0 ] && kill -TERM "$child" 2>/dev/null; }
trap on_term TERM INT
report_crash() { # $1 exit code, $2 first panic/fatal line
  local msg; msg=$(printf '%s' "$2" | tr -d '"\\' | cut -c1-300)
  printf '{"antithesis_assert":{"assert_type":"reachability","display_type":"Unreachable","condition":false,"hit":true,"must_hit":false,"id":"a cadence server process crashes","message":"a cadence server process crashes","location":{"class":"entrypoint","function":"supervise","file":"entrypoint.sh","begin_line":1,"begin_column":0},"details":{"node":"%s","exit":%s,"line":"%s","stopping":%s}}}\n' \
    "$(hostname)" "$1" "$msg" "$stopping" >> "$SDK_OUT" 2>/dev/null
}
n=0
while :; do
  n=$((n+1))
  echo "cadence node $(hostname) ip=${HOST_IP} services=${SERVICES} start #$n"
  cadence-server --root /etc/cadence --env docker --config config start --services="${SERVICES}" > >(tee /tmp/server.out) 2>&1 &
  child=$!
  wait "$child"; rc=$?
  while kill -0 "$child" 2>/dev/null; do wait "$child"; rc=$?; done
  sleep 0.2
  line=$(grep -m1 -E '^(panic:|fatal error:)' /tmp/server.out)
  if [ -n "$line" ]; then report_crash "$rc" "$line"; fi
  [ "$stopping" = 1 ] && exit 0
  echo "cadence-server exited rc=$rc; restarting in 2s"
  sleep 2
done
