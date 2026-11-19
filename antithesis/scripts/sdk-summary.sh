#!/bin/bash
# Usage: sdk-summary.sh <sdk.jsonl...> : per assertion: type, hits, passes, message; flags would-fail.
jq -rs '[.[].antithesis_assert // empty] | group_by(.message)[]
  | (map(select(.hit))|length) as $h | (map(select(.hit and .condition))|length) as $p
  | (if .[0].assert_type == "sometimes" then ($p == 0) else ((map(select(.hit and (.condition|not)))|length) > 0 or (.[0].must_hit and $h == 0)) end) as $fail
  | "\(if $fail then "FAIL" else "ok  " end)\t\(.[0].display_type)\thits=\($h)\tpass=\($p)\t\(.[0].message)"' "$@"
