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

assert_matches() {
    local label="$1"
    local haystack="$2"
    local pattern="$3"

    if grep -qE -- "$pattern" <<< "$haystack"; then
        echo "PASS: $label"
        passed=$((passed + 1))
    else
        echo "FAIL: $label — expected to match pattern: $pattern"
        echo "actual output:"
        printf '  %s\n' "$haystack"
        failed=$((failed + 1))
    fi
}

assert_line_count_ge() {
    local label="$1"
    local value="$2"
    local min="$3"

    local count
    if [[ -z "$value" ]]; then
        count=0
    else
        count=$(echo "$value" | wc -l)
    fi
    if [[ $count -ge $min ]]; then
        echo "PASS: $label ($count lines)"
        passed=$((passed + 1))
    else
        echo "FAIL: $label — expected >= $min lines, got $count"
        failed=$((failed + 1))
    fi
}

assert_starts_with() {
    local label="$1"
    local value="$2"
    local prefix="$3"

    if [[ "$value" == "$prefix"* ]]; then
        echo "PASS: $label"
        passed=$((passed + 1))
    else
        echo "FAIL: $label — expected prefix: $prefix (got: $value)"
        failed=$((failed + 1))
    fi
}

assert_command_fails() {
    local label="$1"
    local expected="$2"
    shift 2

    local output
    if output=$("$@" 2>&1); then
        echo "FAIL: $label — command succeeded unexpectedly"
        failed=$((failed + 1))
        return
    fi

    if [[ -n "$expected" ]]; then
        if [[ "$output" == *"$expected"* ]]; then
            echo "PASS: $label"
            passed=$((passed + 1))
        else
            echo "FAIL: $label — expected error containing: $expected"
            echo "actual output:"
            printf '  %s\n' "$output"
            failed=$((failed + 1))
        fi
    else
        echo "PASS: $label"
        passed=$((passed + 1))
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

assert_symlink() {
    local label="$1"
    local link="$2"
    local expected_target="$3"

    if [[ -L "$link" ]]; then
        local actual_target
        actual_target=$(readlink "$link")
        if [[ "$actual_target" == "$expected_target" ]]; then
            echo "PASS: $label"
            passed=$((passed + 1))
        else
            echo "FAIL: $label — target: $actual_target (expected: $expected_target)"
            failed=$((failed + 1))
        fi
    else
        echo "FAIL: $label — not a symlink: $link"
        failed=$((failed + 1))
    fi
}

assert_exists() {
    local label="$1"
    local path="$2"

    if [[ -e "$path" || -L "$path" ]]; then
        echo "PASS: $label"
        passed=$((passed + 1))
    else
        echo "FAIL: $label — expected to exist: $path"
        failed=$((failed + 1))
    fi
}

assert_not_exists() {
    local label="$1"
    local path="$2"

    if [[ ! -e "$path" && ! -L "$path" ]]; then
        echo "PASS: $label"
        passed=$((passed + 1))
    else
        echo "FAIL: $label — expected not to exist: $path"
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

test_summary() {
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
