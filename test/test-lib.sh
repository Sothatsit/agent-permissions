#!/usr/bin/env bash
#
# Shared test assertions, counters, and exit hooks.
# Source this from test scripts — do not execute directly.
#

passed=0
failed=0

# --- Assertions ---

assert_contains() {
    local label="$1"
    local haystack="$2"
    local needle="$3"

    if [[ "$haystack" == *"$needle"* ]]; then
        echo "PASS: $label"
        passed=$((passed + 1))
    else
        echo "FAIL: $label — expected to find: $needle"
        echo "actual output:"
        printf '  %s\n' "$haystack"
        failed=$((failed + 1))
    fi
}

assert_not_contains() {
    local label="$1"
    local haystack="$2"
    local needle="$3"

    if [[ "$haystack" == *"$needle"* ]]; then
        echo "FAIL: $label — did not expect to find: $needle"
        failed=$((failed + 1))
    else
        echo "PASS: $label"
        passed=$((passed + 1))
    fi
}

assert_not_empty() {
    local label="$1"
    local value="$2"

    if [[ -n "$value" ]]; then
        echo "PASS: $label"
        passed=$((passed + 1))
    else
        echo "FAIL: $label — output was empty"
        failed=$((failed + 1))
    fi
}

assert_rc() {
    local label="$1"
    local expected="$2"
    local actual="$3"

    if [[ "$actual" -eq "$expected" ]]; then
        echo "PASS: $label"
        passed=$((passed + 1))
    else
        echo "FAIL: $label — expected exit code $expected, got $actual"
        failed=$((failed + 1))
    fi
}

# --- Exit Hooks ---

_exit_hooks=()

add_exit_hook() {
    _exit_hooks+=("$1")
}

_run_exit_hooks() {
    for hook in "${_exit_hooks[@]+"${_exit_hooks[@]}"}"; do
        "$hook"
    done
}

trap _run_exit_hooks EXIT

# --- Summary ---

print_test_summary() {
    echo ""
    echo "================================"
    local total=$((passed + failed))
    echo "Results: $passed/$total passed"
    if [[ $failed -gt 0 ]]; then
        echo "FAILED: $failed test(s)"
        return 1
    else
        echo "All tests passed."
    fi
}
