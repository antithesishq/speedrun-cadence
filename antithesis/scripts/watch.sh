#!/bin/bash
# Usage: watch.sh <label> <run_id> : appends the stream to scratchbook/logs/stream-<label>.log; prints failed verdicts and completion.
LOG=/home/exedev/cadence/antithesis/scratchbook/logs/stream-$1.log
snouty runs stream "$2" 2>/dev/null < /dev/null | while IFS= read -r line; do
  echo "$line" >> "$LOG"
  case "$line" in verdict*failed*|completed*) echo "$1: $line";; esac
done
