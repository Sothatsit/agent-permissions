#!/usr/bin/env bash
#
# Invariant tests for presets/*.json.
#
# Validates structural rules across all preset files:
#   1. Every preset is valid JSON with only known
#      top-level and tier keys.
#   2. Every Commands entry appears in exactly one tier
#      across all preset files (no cross-file duplicates;
#      no same command in both Allow and Ask).
#   3. Every Deny.Commands entry uses the `:*` form.
#   4. No single tier holds both `cmd` and `cmd *` for
#      the same command — collapse to `cmd:*`.
#   5. Every Commands/EnvVars entry carries a non-empty
#      reason — presets must document each entry (a user's
#      own config may leave reasons empty; presets may not).
#
# Can be sourced by test/test.sh or run standalone.

# Resolve REPO_DIR and PRESETS_DIR. When sourced, REPO_DIR
# is already exported; when run standalone, derive it from
# this script's path.
: "${REPO_DIR:=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
PRESETS_DIR="$REPO_DIR/presets"

if ! declare -F assert_contains >/dev/null 2>&1; then
    # shellcheck source=test-lib.sh
    source "$(dirname "${BASH_SOURCE[0]}")/test-lib.sh"
fi

# Known top-level keys: description, the four tiers, and the
# Rules axis (rule ID -> {Enabled}). Catalog-vs-preset rule
# invariants (every rule owned by exactly one preset; IDs
# valid) live in the Go test, which can see the rule catalog.
KNOWN_TOP_KEYS='["description","Allow","SoftAsk","Ask","Deny","Rules"]'
# Known per-tier keys: tool axes only.
KNOWN_TIER_KEYS='["Commands","EnvVars"]'
TIER_NAMES='["Allow","SoftAsk","Ask","Deny"]'

# =========================================================
# 1. Valid JSON, known keys only
# =========================================================
echo ""
echo "=== Presets: valid JSON with known keys ==="
section_ok=0
section_fail=0
for f in "$PRESETS_DIR"/*.json; do
    if ! jq empty "$f" 2>/dev/null; then
        echo "  invalid JSON: $(basename "$f")"
        section_fail=$((section_fail + 1))
        continue
    fi
    unknown_top=$(jq -r --argjson known "$KNOWN_TOP_KEYS" \
        'keys[] | select(. as $k | $known | index($k) | not)' \
        "$f")
    if [[ -n "$unknown_top" ]]; then
        echo "  $(basename "$f"): unknown top-level key(s): $unknown_top"
        section_fail=$((section_fail + 1))
        continue
    fi
    # Each tier object must contain only Commands/EnvVars.
    unknown_axes=$(jq -r \
        --argjson tiers "$TIER_NAMES" \
        --argjson axes "$KNOWN_TIER_KEYS" '
        to_entries[] |
        select(.key as $k | $tiers | index($k)) |
        .value | keys[] |
        select(. as $a | $axes | index($a) | not)
    ' "$f")
    if [[ -n "$unknown_axes" ]]; then
        echo "  $(basename "$f"): unknown tier-axis key(s): $unknown_axes"
        section_fail=$((section_fail + 1))
    else
        section_ok=$((section_ok + 1))
    fi
done
if [[ $section_fail -eq 0 ]]; then
    echo "PASS: $section_ok presets valid"
    passed=$((passed + 1))
else
    echo "FAIL: $section_fail invalid (of $((section_ok + section_fail)) checked)"
    failed=$((failed + 1))
fi

# =========================================================
# 2. Every Commands entry appears in exactly one tier
# =========================================================
echo ""
echo "=== Presets: each Commands entry appears in exactly one tier ==="
dupes=$(jq -s --argjson tiers "$TIER_NAMES" '
    # For each file, emit [pattern, tier] for each Commands key.
    [ .[] | to_entries[] |
      select(.key as $k | $tiers | index($k)) as $e |
      ($e.value.Commands // {}) |
      to_entries[] | [.key, $e.key]
    ] |
    group_by(.[0]) |
    map(select((map(.[1]) | unique | length) > 1)) |
    map({entry: .[0][0], tiers: (map(.[1]) | unique)})
' "$PRESETS_DIR"/*.json)
dupe_count=$(echo "$dupes" | jq 'length')
if [[ "$dupe_count" -eq 0 ]]; then
    echo "PASS: no cross-tier Commands duplicates"
    passed=$((passed + 1))
else
    echo "$dupes" | jq -r '.[] | "  \(.entry): \(.tiers | join(", "))"'
    echo "FAIL: $dupe_count Commands entries appear in multiple tiers"
    failed=$((failed + 1))
fi

# =========================================================
# 3. Every Deny.Commands entry uses the `:*` form
# =========================================================
echo ""
echo '=== Deny.Commands: every entry uses `:*` form ==='
deny_entries=$(jq -r -s '
    [ .[] | (.Deny.Commands // {}) | keys[] ] | .[]
' "$PRESETS_DIR"/*.json)
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
    echo "FAIL: $section_fail deny entries leave bypasses (of $((section_ok + section_fail)) checked)"
    failed=$((failed + 1))
fi

# =========================================================
# 4. No tier holds both `cmd` and `cmd *` for same command
# =========================================================
echo ""
echo '=== Presets: no Commands tier holds both `cmd` and `cmd *` ==='
uncollapsed=$(jq -r -s --argjson tiers "$TIER_NAMES" '
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
' "$PRESETS_DIR"/*.json)
if [[ -z "$uncollapsed" ]]; then
    echo "PASS: no uncollapsed bare+starred pairs"
    passed=$((passed + 1))
else
    while IFS= read -r line; do
        echo "  uncollapsed: $line"
    done <<<"$uncollapsed"
    fail_count=$(echo "$uncollapsed" | wc -l)
    echo "FAIL: $fail_count tier(s) with uncollapsed bare+starred pairs"
    failed=$((failed + 1))
fi

# =========================================================
# 5. Every entry carries a non-empty reason
# =========================================================
echo ""
echo "=== Presets: every entry has a non-empty reason ==="
section_ok=0
section_fail=0
for f in "$PRESETS_DIR"/*.json; do
    empties=$(jq -r --argjson tiers "$TIER_NAMES" '
        to_entries[]
        | select(.key as $k | $tiers | index($k)) as $t
        | (["Commands","EnvVars"][]) as $axis
        | ($t.value[$axis] // {}) | to_entries[]
        | select(.value == "")
        | "\($t.key).\($axis): \(.key)"
    ' "$f")
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
    test_summary
fi
