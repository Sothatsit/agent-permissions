#!/usr/bin/env bash
#
# Integration tests for agent-permissions subcommands other
# than claude-hook: setup, install, presets list, rules list,
# check, and validate.
# Also asserts that the previously-shipped `presets enable`
# and `presets disable` subcommands now fail with a helpful
# message. Each test runs in an isolated HOME so the
# developer's real config files are untouched.
#

[[ "${AGENT_PERMISSIONS_TEST_ORCHESTRATED:-}" == 1 ]] \
    || { echo "Run via test/test.sh, not directly." >&2; exit 1; }

HOOK="$REPO_DIR/bin/agent-permissions"
if [[ ! -x "$HOOK" ]]; then
    echo "  agent-permissions binary not found at $HOOK"
    failed=$((failed + 1))
    return 0
fi

# Each test gets its own isolated HOME so writes to
# ~/.agents/ and ~/.claude/ don't leak between tests or
# touch the developer's real files.
_sc_tmpdir=$(mktemp -d)
add_exit_hook _sc_cleanup
_sc_cleanup() { rm -rf "$_sc_tmpdir"; }

_fresh_home() {
    local h="$_sc_tmpdir/h-$RANDOM"
    mkdir -p "$h"
    echo "$h"
}

# Strip ANSI colours that some terminals add to subcommand
# output so assertions don't have to match them.
_sc_run() {
    HOME="$1" "$HOOK" "${@:2}" 2>&1
}

# validate reads <cwd>/.agents/permissions.json as well as
# the global config, so run it from a fresh empty project
# dir: a stray <repo>/.agents config must not perturb the
# counts, and the cwd must differ from HOME or the global
# and project agent paths collide and Resolve counts every
# source twice.
_validate_run() {
    local h="$1" proj
    proj=$(_fresh_home)
    ( cd "$proj" && CLAUDE_CONFIG_DIR="$h/empty-claude" \
        HOME="$h" "$HOOK" validate 2>&1 )
}

echo ""
echo "=== subcommands: setup ==="

h=$(_fresh_home)
out=$(_sc_run "$h" setup)
assert_contains "setup writes file" "$out" \
    "$h/.agents/permissions.json"
assert_contains "setup reports preset count" "$out" \
    "presets active"
test -f "$h/.agents/permissions.json"
assert_rc "setup actually creates the file" 0 "$?"

# Second run without --force should refuse.
out=$(_sc_run "$h" setup 2>&1 || true)
assert_contains "setup refuses overwrite without --force" \
    "$out" "already exists"

# --force overwrites.
out=$(_sc_run "$h" setup --force 2>&1)
assert_contains "setup --force overwrites" "$out" "Wrote"


echo ""
echo "=== subcommands: presets list ==="

# Default home — no preset selection; every shipped preset
# should appear under the "Enabled" group.
h=$(_fresh_home)
out=$(_sc_run "$h" presets list)
assert_contains "presets list shows Enabled heading" \
    "$out" "Enabled:"
assert_contains "presets list shows git" "$out" "git"

# With disabled-presets in a config, the corresponding
# preset is grouped under Disabled with its reason.
h=$(_fresh_home)
mkdir -p "$h/.agents"
echo '{"disabled-presets":["git"]}' \
    > "$h/.agents/permissions.json"
out=$(_sc_run "$h" presets list)
assert_contains "presets list shows Disabled heading" \
    "$out" "Disabled:"
assert_contains "presets list shows git as disabled" \
    "$out" "in disabled-presets"

# enable / disable subcommands were removed — verify they
# fail with a helpful message.
out=$(_sc_run "$h" presets enable git 2>&1 || true)
assert_contains "presets enable subcommand removed" \
    "$out" "only \`list\`"
out=$(_sc_run "$h" presets disable git 2>&1 || true)
assert_contains "presets disable subcommand removed" \
    "$out" "only \`list\`"


echo ""
echo "=== subcommands: rules ==="

# rules list prints the static catalog as "<id> - <desc>".
h=$(_fresh_home)
rc=0
out=$(_sc_run "$h" rules list) || rc=$?
assert_rc "rules list: exit 0" 0 "$rc"
assert_contains "rules list: lists a known rule id" "$out" \
    "git.branch-writes - "

# It is a static catalog: config must not change the output.
mkdir -p "$h/.agents"
echo '{"Rules":{"git.branch-writes":{"Enabled":false}}}' \
    > "$h/.agents/permissions.json"
out2=$(_sc_run "$h" rules list)
if [[ "$out" != "$out2" ]]; then
    echo "FAIL: rules list: output changed with config (should be static)"
    failed=$((failed + 1))
else
    echo "PASS: rules list: static regardless of config"
    passed=$((passed + 1))
fi

# No subcommand and unknown subcommand both error.
rc=0
_sc_run "$h" rules >/dev/null 2>&1 || rc=$?
assert_rc "rules: no subcommand exits 2" 2 "$rc"
rc=0
_sc_run "$h" rules bogus >/dev/null 2>&1 || rc=$?
assert_rc "rules: unknown subcommand exits 2" 2 "$rc"
# Extra args after `list` are rejected (parity with validate).
rc=0
_sc_run "$h" rules list extra-arg >/dev/null 2>&1 || rc=$?
assert_rc "rules list: extra-arg exits 2" 2 "$rc"


echo ""
echo "=== subcommands: install ==="

# No settings.json → skipped.
h=$(_fresh_home)
out=$(_sc_run "$h" install)
assert_contains "install: skips when settings.json absent" \
    "$out" "Skipped Claude Code"

# settings.json present → installed.
mkdir -p "$h/.claude"
echo '{}' > "$h/.claude/settings.json"
out=$(_sc_run "$h" install)
assert_contains "install: writes stanza when settings present" \
    "$out" "Installed for Claude Code"
assert_contains "install: settings.json now has PreToolUse" \
    "$(cat "$h/.claude/settings.json")" "PreToolUse"

# Second run is idempotent.
out=$(_sc_run "$h" install)
assert_contains "install: second run is idempotent" \
    "$out" "already installed"

# Preserves other settings keys.
h=$(_fresh_home)
mkdir -p "$h/.claude"
cat > "$h/.claude/settings.json" <<'EOF'
{"model": "sonnet", "permissions": {"allow": ["Bash(ls)"]}}
EOF
_sc_run "$h" install >/dev/null
contents=$(cat "$h/.claude/settings.json")
assert_contains "install: preserves model key" "$contents" \
    "sonnet"
assert_contains "install: preserves permissions key" \
    "$contents" "Bash(ls)"

# Merges into an existing Bash matcher rather than adding
# a duplicate top-level entry.
h=$(_fresh_home)
mkdir -p "$h/.claude"
cat > "$h/.claude/settings.json" <<'EOF'
{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"my-existing-logger"}]}]}}
EOF
_sc_run "$h" install >/dev/null
contents=$(cat "$h/.claude/settings.json")
assert_contains "install: merges into existing Bash matcher (preserves existing hook)" \
    "$contents" "my-existing-logger"
# There should be only one Bash matcher entry, not two.
count=$(echo "$contents" | grep -c '"matcher": "Bash"' || true)
if [[ "$count" -ne 1 ]]; then
    echo "FAIL: install: expected 1 Bash matcher entry, got $count"
    failed=$((failed + 1))
else
    echo "PASS: install: single Bash matcher entry after merge"
    passed=$((passed + 1))
fi

# Preserves file mode when overwriting.
h=$(_fresh_home)
mkdir -p "$h/.claude"
echo '{}' > "$h/.claude/settings.json"
chmod 0600 "$h/.claude/settings.json"
_sc_run "$h" install >/dev/null
mode=$(stat -c '%a' "$h/.claude/settings.json" 2>/dev/null \
        || stat -f '%Lp' "$h/.claude/settings.json")
if [[ "$mode" != "600" ]]; then
    echo "FAIL: install: mode preservation (got $mode, want 600)"
    failed=$((failed + 1))
else
    echo "PASS: install: mode preservation (600 retained)"
    passed=$((passed + 1))
fi

# Refuses to write through a symlink and leaves the real
# file untouched.
h=$(_fresh_home)
mkdir -p "$h/.claude" "$h/real"
echo '{"model":"sonnet"}' > "$h/real/settings.json"
ln -s "$h/real/settings.json" "$h/.claude/settings.json"
out=$(_sc_run "$h" install 2>&1 || true)
assert_contains "install: refuses symlink with hand-paste message" \
    "$out" "symbolic link"
realcontents=$(cat "$h/real/settings.json")
if [[ "$realcontents" != '{"model":"sonnet"}' ]]; then
    echo "FAIL: install: symlinked real file was modified"
    failed=$((failed + 1))
else
    echo "PASS: install: symlink target preserved"
    passed=$((passed + 1))
fi

# Refuses when PreToolUse exists in an unexpected shape
# rather than destroying it.
h=$(_fresh_home)
mkdir -p "$h/.claude"
cat > "$h/.claude/settings.json" <<'EOF'
{"hooks":{"PreToolUse":"not-an-array"}}
EOF
before=$(cat "$h/.claude/settings.json")
out=$(_sc_run "$h" install 2>&1 || true)
assert_contains "install: refuses non-array PreToolUse" \
    "$out" "not an array"
after=$(cat "$h/.claude/settings.json")
if [[ "$before" != "$after" ]]; then
    echo "FAIL: install: non-array PreToolUse was modified"
    failed=$((failed + 1))
else
    echo "PASS: install: non-array PreToolUse left untouched"
    passed=$((passed + 1))
fi


echo ""
echo "=== subcommands: check ==="

h=$(_fresh_home)
# check inherits CLAUDE_CONFIG_DIR / cwd, so pin both.
unset CLAUDE_CONFIG_DIR
out=$(CLAUDE_CONFIG_DIR="$h/empty-claude" \
        HOME="$h" "$HOOK" check 'git status')
assert_contains "check: shows the command" "$out" \
    "git status"
assert_contains "check: shows decision line" "$out" \
    "Decision:"
assert_contains "check: lists at least one preset source" \
    "$out" "preset:"

# Invalid usage exits non-zero.
rc=0
HOME="$h" "$HOOK" check >/dev/null 2>&1 || rc=$?
assert_rc "check: missing arg exits 2" 2 "$rc"

# Malformed entries in permissions.json should surface as
# a Warnings: section without changing the decision.
h=$(_fresh_home)
mkdir -p "$h/.agents"
cat > "$h/.agents/permissions.json" <<'EOF'
{"Allow": {"Commands": {"git status:*": "", ":*": "", "  ": ""}}}
EOF
out=$(CLAUDE_CONFIG_DIR="$h/empty-claude" \
        HOME="$h" "$HOOK" check 'git status')
assert_contains "check: lists Warnings section" \
    "$out" "Warnings:"
assert_contains "check: warning quotes the bad entry" \
    "$out" '":*"'
assert_contains "check: warning attributes the source" \
    "$out" ".agents/permissions.json"


echo ""
echo "=== subcommands: validate ==="

# No malformed entries → exit 0, "OK." message.
h=$(_fresh_home)
out=$(_validate_run "$h")
rc=$?
assert_contains "validate: clean exit message" "$out" \
    "OK."
assert_rc "validate: clean exit code 0" 0 "$rc"

# Malformed entries → exit 2 (validate now returns an
# error rather than calling os.Exit(1) directly, so main's
# normal error path applies). Lists them and quotes the
# bad entry.
h=$(_fresh_home)
mkdir -p "$h/.agents"
cat > "$h/.agents/permissions.json" <<'EOF'
{"Allow": {"Commands": {"git status:*": "", ":*": ""}}, "Deny": {"Commands": {"  ": ""}}}
EOF
rc=0
out=$(_validate_run "$h") || rc=$?
assert_rc "validate: malformed exit code 2" 2 "$rc"
assert_contains "validate: lists the count" "$out" \
    "Found 2 malformed"
assert_contains "validate: quotes the bad entry" "$out" \
    '":*"'

# Unknown rule ID in a user .agents config → exit 2. The
# config also has a malformed pattern, so this doubles as a
# check that validate reports every problem in one pass
# rather than bailing on the first.
h=$(_fresh_home)
mkdir -p "$h/.agents"
cat > "$h/.agents/permissions.json" <<'EOF'
{"Rules": {"git.branch-writs": {"Enabled": false}}, "Allow": {"Commands": {":*": ""}}}
EOF
rc=0
out=$(_validate_run "$h") || rc=$?
assert_rc "validate: unknown rule ID exits 2" 2 "$rc"
assert_contains "validate: names the unknown rule" "$out" \
    '"git.branch-writs"'
assert_contains "validate: labels it an unknown rule" "$out" \
    "unknown rule"
assert_contains "validate: also reports the malformed entry" \
    "$out" "malformed"

# Unknown preset name in disabled-presets → exit 2, the same
# silent-no-op class as an unknown rule ID.
h=$(_fresh_home)
mkdir -p "$h/.agents"
cat > "$h/.agents/permissions.json" <<'EOF'
{"disabled-presets": ["containerz"]}
EOF
rc=0
out=$(_validate_run "$h") || rc=$?
assert_rc "validate: unknown preset exits 2" 2 "$rc"
assert_contains "validate: names the unknown preset" "$out" \
    '"containerz"'
assert_contains "validate: labels it an unknown preset" \
    "$out" "unknown preset"

# A real preset name is not flagged.
h=$(_fresh_home)
mkdir -p "$h/.agents"
cat > "$h/.agents/permissions.json" <<'EOF'
{"disabled-presets": ["containers"]}
EOF
out=$(_validate_run "$h")
rc=$?
assert_rc "validate: real preset name is clean" 0 "$rc"
assert_contains "validate: real preset → OK" "$out" "OK."

# cwd == $HOME: the project and global agent paths are the
# same file. Resolve must dedup it (keeping the higher-
# precedence project entry), so one malformed entry is
# reported once, not twice. Run from $HOME itself, not the
# isolated project dir _validate_run uses.
h=$(_fresh_home)
mkdir -p "$h/.agents"
cat > "$h/.agents/permissions.json" <<'EOF'
{"Allow": {"Commands": {":*": ""}}}
EOF
rc=0
out=$(cd "$h" && CLAUDE_CONFIG_DIR="$h/empty-claude" \
        HOME="$h" "$HOOK" validate 2>&1) || rc=$?
assert_rc "validate: cwd==HOME exits 2" 2 "$rc"
assert_contains "validate: cwd==HOME counts the file once" \
    "$out" "Found 1 malformed"

# Invalid usage exits non-zero.
rc=0
HOME="$h" "$HOOK" validate extra-arg >/dev/null 2>&1 || rc=$?
assert_rc "validate: extra-arg exits 2" 2 "$rc"
