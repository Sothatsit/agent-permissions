#!/usr/bin/env bash
set -euo pipefail

#
# Performance regression test for the agent-permissions hook.
# Runs the benchmark and fails if any individual case
# exceeds the threshold.
#
# Usage: test/test-benchmark.sh
#

THRESHOLD_MS=50
DIR="$(cd "$(dirname "$0")" && pwd)"

output=$("$DIR/benchmark.sh")
echo "$output"
echo ""

failed=0
total=0

while IFS= read -r line; do
    [[ "$line" == *" ms" ]] || continue
    [[ "$line" == *"overhead"* ]] && continue
    [[ "$line" == *"mean"* ]] && continue

    stripped="${line% ms}"
    whole="${stripped##* }"
    whole="${whole%.*}"

    total=$((total + 1))
    if [[ "$whole" -ge "$THRESHOLD_MS" ]]; then
        failed=$((failed + 1))
    fi
done <<< "$output"

if [[ "$total" -eq 0 ]]; then
    echo "FAIL: no benchmark results found"
    exit 1
elif [[ "$failed" -gt 0 ]]; then
    echo "FAIL: $failed/$total case(s) exceeded ${THRESHOLD_MS}ms"
    exit 1
else
    echo "PASS: All $total cases under ${THRESHOLD_MS}ms"
fi
