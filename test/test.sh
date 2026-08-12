#!/usr/bin/env bash
set -uo pipefail

#
# Test orchestrator. Runs Go unit tests, JSON preset invariant
# tests, and the bash integration tests against the built hook
# binary.
#

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
export REPO_DIR
TEST_DIR="$(cd "$(dirname "$0")" && pwd)"

# shellcheck source=test/test-lib.sh
source "$TEST_DIR/test-lib.sh"
export AGENT_PERMISSIONS_TEST_ORCHESTRATED=1

_run_go_tests() {
    local title="Go unit tests"
    local output rc go_passed go_failed
    echo ""
    echo "================================"
    echo "$title"
    echo "================================"
    output=$(go test -C "$REPO_DIR" -v -count=1 ./... 2>&1)
    rc=$?
    go_passed=$(echo "$output" | grep -c '^--- PASS:' || true)
    go_failed=$(echo "$output" | grep -c '^--- FAIL:' || true)
    passed=$((passed + go_passed))
    failed=$((failed + go_failed))
    if [[ $rc -ne 0 ]]; then
        echo "$output"
        if [[ $go_failed -eq 0 ]]; then
            echo "FAIL: $title (build error)"
            failed=$((failed + 1))
        fi
    else
        echo "$output" | grep '^--- ' || true
        echo "PASS"
    fi
}

_run_suite() {
    local title=$1 file=$2
    echo ""
    echo "================================"
    echo "$title"
    echo "================================"
    # shellcheck disable=SC1090
    source "$TEST_DIR/$file"
}

_run_go_tests
_run_suite "Preset Invariants"     test-presets.sh
_run_suite "Bash Integration"      test-permission-hook.sh
_run_suite "Subcommand Integration" test-subcommands.sh

print_test_summary
