#!/bin/bash
# Usage: details.sh <run_id> "<assertion message>" [limit] : unique failing details
snouty runs search "$1" "filter(ev => ev.antithesis_assert && ev.antithesis_assert.message == \"$2\" && ev.antithesis_assert.condition == false).narrow([\"antithesis_assert\"])" --limit ${3:-50} 2>&1 | grep -o '"details":{[^}]*}' | sort | uniq -c | sort -rn | head -10 | cut -c1-500
