#!/usr/bin/env bash
# shellcheck disable=SC2016  # Backticks in headings are literal.
set -uo pipefail

#
# Invariant tests for presets/*.json.
#
# Validates shipped-data rules that span preset files. The Go parser tests the
# JSON schema through the production loader.
#   1. Every Commands entry appears once across all preset files.
#   2. Every Deny.Commands entry uses the `:*` form.
#   3. No single tier holds both `cmd` and `cmd *` for
#      the same command. Collapse those pairs to `cmd:*`.
#   4. Every Commands/EnvVars entry carries a non-empty
#      reason. Presets must document each entry (a user's
#      own config may leave reasons empty; presets may not).
#
# Can be sourced by test/test.sh or run standalone.

# Resolve REPO_DIR and PRESETS_DIR. When sourced, REPO_DIR
# is already exported; when run standalone, derive it from
# this script's path.
: "${REPO_DIR:=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
PRESETS_DIR="$REPO_DIR/presets"

if ! declare -F assert_contains >/dev/null 2>&1; then
    # shellcheck source=test/test-lib.sh
    source "$(dirname "${BASH_SOURCE[0]}")/test-lib.sh"
fi

TIER_NAMES='["Allow","SoftAsk","Ask","Deny"]'

# =========================================================
# 1. Every Commands entry appears once across all files
# =========================================================
echo ""
echo "=== Presets: each Commands entry appears once ==="
if ! dupes=$(jq -s --argjson tiers "$TIER_NAMES" '
    # For each file, emit [pattern, tier] for each Commands key.
    [ .[] | to_entries[] |
      select(.key as $k | $tiers | index($k)) as $e |
      ($e.value.Commands // {}) |
      to_entries[] | [.key, $e.key]
    ] |
    group_by(.[0]) |
    map(select(length > 1)) |
    map({
        entry: .[0][0],
        tiers: (map(.[1]) | unique),
        count: length
    })
' "$PRESETS_DIR"/*.json); then
    echo "FAIL: could not inspect Commands duplicates"
    failed=$((failed + 1))
else
    if ! dupe_count=$(printf '%s\n' "$dupes" | jq 'length'); then
        echo "FAIL: could not count Commands duplicates"
        failed=$((failed + 1))
    elif [[ "$dupe_count" -eq 0 ]]; then
        echo "PASS: no Commands duplicates"
        passed=$((passed + 1))
    else
        printf '%s\n' "$dupes" | jq -r '
            .[] |
            "  \(.entry): \(.count) in \(.tiers | join(", "))"
        ' || printf '  %s\n' "$dupes"
        echo "FAIL: $dupe_count Commands entries appear more than once"
        failed=$((failed + 1))
    fi
fi

# =========================================================
# 2. Every Deny.Commands entry uses the `:*` form
# =========================================================
echo ""
echo '=== Deny.Commands: every entry uses `:*` form ==='
if ! deny_entries=$(jq -r -s '
    [ .[] | (.Deny.Commands // {}) | keys[] ] | .[]
' "$PRESETS_DIR"/*.json); then
    echo "FAIL: could not inspect Deny.Commands entries"
    failed=$((failed + 1))
else
    section_ok=0
    section_fail=0
    while IFS= read -r entry; do
        [[ -n "$entry" ]] || continue
        if [[ "$entry" == *":*" ]]; then
            section_ok=$((section_ok + 1))
        else
            echo "  deny entry not in :* form: $entry"
            section_fail=$((section_fail + 1))
        fi
    done <<<"$deny_entries"
    if [[ $section_fail -eq 0 ]]; then
        echo "PASS: $section_ok deny entries checked, all cover all forms"
        passed=$((passed + 1))
    else
        echo "FAIL: $section_fail deny entries leave bypasses " \
            "(of $((section_ok + section_fail)) checked)"
        failed=$((failed + 1))
    fi
fi

# =========================================================
# 3. No tier holds both `cmd` and `cmd *` for same command
# =========================================================
echo ""
echo '=== Presets: no Commands tier holds both `cmd` and `cmd *` ==='
if ! uncollapsed=$(jq -r -s --argjson tiers "$TIER_NAMES" '
    [ .[] | to_entries[] |
      select(.key as $k | $tiers | index($k)) as $t |
      ($t.value.Commands // {}) |
      keys[] | {tier: $t.key, entry: .}
    ] |
    group_by(.tier) |
    map(
        . as $tier_entries |
        ($tier_entries | map(.entry)) as $entries |
        $tier_entries[] |
        select(
            (.entry | endswith(" *")) and
            (.entry | rtrimstr(" *")) as $bare |
            ($entries | index($bare))
        ) |
        "\(.tier): \(.entry | rtrimstr(" *")) + \(.entry)"
    ) |
    .[]
' "$PRESETS_DIR"/*.json); then
    echo "FAIL: could not inspect bare+starred pairs"
    failed=$((failed + 1))
else
    if [[ -z "$uncollapsed" ]]; then
        echo "PASS: no uncollapsed bare+starred pairs"
        passed=$((passed + 1))
    else
        while IFS= read -r line; do
            echo "  uncollapsed: $line"
        done <<<"$uncollapsed"
        fail_count=$(printf '%s\n' "$uncollapsed" | wc -l)
        echo "FAIL: $fail_count tier(s) with " \
            "uncollapsed bare+starred pairs"
        failed=$((failed + 1))
    fi
fi

# =========================================================
# 4. Every entry carries a non-empty reason
# =========================================================
echo ""
echo "=== Presets: every entry has a non-empty reason ==="
section_ok=0
section_fail=0
for f in "$PRESETS_DIR"/*.json; do
    if ! empties=$(jq -r --argjson tiers "$TIER_NAMES" '
        to_entries[]
        | select(.key as $k | $tiers | index($k)) as $t
        | (["Commands","EnvVars"][]) as $axis
        | ($t.value[$axis] // {}) | to_entries[]
        | select(.value == "")
        | "\($t.key).\($axis): \(.key)"
    ' "$f"); then
        echo "  $(basename "$f"): could not inspect reasons"
        section_fail=$((section_fail + 1))
        continue
    fi
    if [[ -z "$empties" ]]; then
        section_ok=$((section_ok + 1))
    else
        while IFS= read -r line; do
            echo "  $(basename "$f") $line"
        done <<<"$empties"
        section_fail=$((section_fail + 1))
    fi
done
if [[ $section_fail -eq 0 ]]; then
    echo "PASS: $section_ok preset files, no empty reasons"
    passed=$((passed + 1))
else
    echo "FAIL: $section_fail preset file(s) with empty reasons"
    failed=$((failed + 1))
fi

if ! [[ "${AGENT_PERMISSIONS_TEST_ORCHESTRATED:-}" == 1 ]]; then
    print_test_summary
fi
