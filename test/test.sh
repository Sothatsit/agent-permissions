#!/usr/bin/env bash
set -uo pipefail

#
# Test orchestrator. Runs Go unit tests, JSON preset invariant tests, and the
# bash integration tests against the built hook binary. With no arguments
# every suite runs; naming suites runs just those, in the order given.
#
#   test/test.sh [go|presets|hook|subcommands]...
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
    # Match build.sh, which builds without cgo. Leaving it on makes the
    # tests unbuildable wherever the platform's cgo is broken, as on
    # CentOS 7, where every package fails on an undefined uid_t.
    output=$(CGO_ENABLED=0 go test -C "$REPO_DIR" -v -count=1 ./... 2>&1)
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

_run_named_suite() {
    case $1 in
        go)          _run_go_tests ;;
        presets)     _run_suite "Preset Invariants"      test-presets.sh ;;
        hook)        _run_suite "Bash Integration"       test-permission-hook.sh ;;
        subcommands) _run_suite "Subcommand Integration" test-subcommands.sh ;;
    esac
}

suites=("$@")
if [[ ${#suites[@]} -eq 0 ]]; then
    suites=(go presets hook subcommands)
fi

# Reject a bad name before any suite runs, so a typo cannot pass as a
# shorter run.
for suite in "${suites[@]}"; do
    case $suite in
        go|presets|hook|subcommands) ;;
        *)
            echo "unknown suite: $suite" >&2
            echo "usage: test/test.sh [go|presets|hook|subcommands]..." >&2
            exit 2
            ;;
    esac
done

for suite in "${suites[@]}"; do
    _run_named_suite "$suite"
done

print_test_summary
