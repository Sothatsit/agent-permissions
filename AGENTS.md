This repo provides a PreToolUse permission hook for Claude Code, plus a
library of topic-organised JSON permission presets. The hook parses bash
with `mvdan/sh` and applies fine-grained rules to compound commands,
piped chains, and inline interpreter code (`bash -c`, `python -c`, etc.)
so most safe operations stop prompting without giving the agent free
rein.

The guiding principle: **agents are flawed, not malicious.** Agents make
mistakes; they don't actively work to circumvent your permission model.
Tier classifications and rule design should reflect that — strict about
catastrophic mistakes, lenient about routine operations.

## Repo Overview

* `cmd/agent-permissions/` — binary entrypoint and subcommand
  implementations (`claude-hook`, `check`, `setup`, `install`,
  `validate`, and the `presets list` / `rules list` groups).
* `internal/breakdown/` — bash AST parsing and recursive command
  extraction.
* `internal/perms/` — source-priority resolution and pattern matching.
  Builds an ordered list of `SourcePerms` (highest priority first) and
  walks them per axis (Commands, EnvVars) on every check; within an
  axis, the first source with any matching pattern decides.
* `internal/agentconfig/` — reader for `~/.agents/permissions.json`,
  `<project>/.agents/permissions.json`, and the project-local
  `<project>/.agents/permissions.local.json` (the agent-permissions
  config file format).
* `internal/harness/` — `Harness` interface for harness-specific
  output text. `ClaudeCode` is the live implementation;
  `Placeholder` is used by dev tools to mark harness-bound
  strings as `<placeholders>` rather than guess a default.
* `internal/atomicfile/` — `Write` helper that refuses symlinks,
  preserves existing file mode, and replaces atomically via
  tmp+rename. Used wherever the binary modifies a config file
  (`install`, `setup`, `agentconfig.Save`).
* `internal/rules/` — per-command rule registry.
* `internal/word/` — `Word` type for tri-state pattern matching
  (definite/possible/impossible).
* `internal/model/` — types and the decision enum shared across packages.
* `presets/` — topic-organised permission bundles. `*.json` files are
  embedded into the binary at build time via `//go:embed` from
  `presets.go`.
* `build.sh` — static Go build.
* `test/` — orchestrator + Go unit tests + bash integration suite + JSON
  preset invariants.
* `.github/workflows/` — CI (test on push/PR) and release (tag-triggered
  binary builds).

## Development

Requires Go 1.25+.

Build:
`./build.sh`

Run all tests:
`./test/test.sh`

* **Commit messages:** Single line only. No Co-Authored-By or other
  trailers.
* **Keep things in sync.** When command flags, subcommands, or hook
  output semantics change, update in the same change:
  1. `README.md` — anything user-facing (subcommand list, config file
     format, install instructions).
  2. `cmd/agent-permissions/main.go` — `printUsage` output.
  3. `test/test-permission-hook.sh` — assertions covering the new
     behaviour.
  4. `presets/*.json` — when tier semantics or pattern syntax change.
  5. `test/test-presets.sh` — when preset schema changes.
  6. `internal/agentconfig/agentconfig.go` — when `permissions.json`
     schema changes (new tier, new top-level field). The known-keys
     check rejects unrecognised fields, so adding without updating
     this breaks parsing.
* **New features:** Small additions — just build them. Architectural
  changes (new subcommands, preset loader, cross-harness adapters) —
  discuss the approach first. Do not enter plan mode unless the user
  asks for it or approves it.

## Guidelines

* **Agent-facing docs.** AGENTS.md is for guidance, gotchas, and
  workflow instructions — content that changes how an agent approaches
  their work. Documentation of types, APIs, or implementation details
  belongs in source code comments where someone working on the code
  will find it, not here.
* **Line wrapping.** Wrap markdown and code comments at 80 characters.
  Every line in a paragraph except the last should be close to 80
  characters. A line under ~65 characters in the middle of a paragraph
  is a short line and must be reflowed. After every insertion,
  deletion, or rewrite, re-read the surrounding paragraph and reflow
  it so no short lines remain.
* **Distinguish certain from uncertain.** When reviewing code, be clear
  about what you've verified vs what you suspect. "This will produce
  wrong results" is different from "I think this might produce wrong
  results — let me test." Test before stating.
* **Check the repo before asserting what's in it.** Don't assume what
  presets, commands, or features exist based on memory. `ls` the
  directory. If you're unsure whether something is part of this
  project, look.
* **Use what you build.** If you create a convenience script, use it
  end-to-end on a real task. Passing tests and producing the expected
  output is necessary but not sufficient; real usage reveals problems
  that synthetic tests miss.
* **Think about process.** After finishing a piece of work, consider
  whether the experience revealed friction or gaps in the dev workflow
  or AGENTS.md. Suggest improvements rather than waiting to be asked.
* **Fail fast.** Prefer explicit errors over silent failures. Stack
  traces are fine — agents are the primary users.
* **Comments explain why, not what.** The Go permissions code
  (`internal/`, `cmd/`) has many rules whose purpose isn't obvious from
  the code alone. Comments should explain *why* a rule exists — the
  threat model, the edge case it handles, or the design decision
  behind it — not restate what the code does. Keep them terse.
* **No aliases or re-exports.** Never create module-level aliases to
  preserve old names after a rename or consolidation. They hide what
  is actually being called. Rename every call site to use the canonical
  name directly.
* **Sourced scripts use `return`, not `exit`.** Test files are sourced
  by `test/test.sh`. Using `exit` terminates the orchestrator. Use
  `return` for early exits in sourced scripts.
* **Bash variable scoping.** Always use `local` for variables inside
  functions. Private helper functions (not intended to be called
  directly) use a `_` prefix.
* **`set -e` and command substitution.** With `set -euo pipefail`,
  `var=$(cmd)` exits the script if `cmd` returns non-zero. When `cmd`
  can legitimately return non-zero (e.g. `grep` with no match), add
  `|| true`: `var=$(cmd || true)`.
* **`jq` and output redirection.** `jq` writes errors to stderr and
  nothing to stdout on failure. Never pipe `jq` output directly into a
  `>` redirect — the shell truncates the file before `jq` runs, so a
  failure silently destroys it. Capture first, then write:
  `result=$(jq ... input) || return 1; printf '%s\n' "$result" > out.json`.
* **Script naming.** Prefer dashes over underscores in script and
  command names (`agent-permissions`, not `agent_permissions`).

## Testing

* **Test before changing.** When a review identifies a suspected bug,
  verify the actual behavior before modifying code. Run the command
  and check the output.
* **After any code change,** run `./test/test.sh` without asking.
  Don't commit without it green.
* **bash vs Go tests.** Go is for unit tests, bash is for integration
  tests. Go unit tests (`go test ./...`) cover package internals
  (rules engine, parser, word helpers). Bash integration tests
  exercise the full pipeline (parsing, breakdown, permissions) against
  the real registry, invoking the built binary end-to-end.
* **Smoke check.** `test/test-permission-hook.sh` starts with a smoke
  assertion that fires before any other test. If the binary is broken
  (subcommand dispatch regressed, JSON output malformed, etc.), the
  suite fails loudly there instead of producing a misleading "all
  passed" report from negative assertions vacuously matching empty
  output.

## Permission Hook Architecture

Every bash command passes through three layers. Any layer can deny;
all must agree to allow.

1. **Breakdown** (`internal/breakdown/`) — Parses the command's AST,
   recursively unwraps nesting (pipes, subshells, `bash -c`, `eval`,
   `find -exec`, etc.), and extracts every command that could execute.
   For interpreter commands (`python`, `perl`, `ruby`, `node`), reads
   the script file or inline code (`-c`/`-e`) and produces code
   snippets that are scanned for language-specific dangerous patterns
   (`subprocess`, `system()`, `child_process`, etc.). Unrecognised
   syntax is denied.
2. **Rules** (`internal/rules/`) — Per-command logic for commands where
   allow/ask/deny is too coarse. Two broad shapes today: rules that
   inspect arguments and deny specific dangerous forms (e.g.
   `tar --to-command`) then fall through to the pattern layer, and
   rules that extract a wrapped inner command and re-run the whole
   pipeline on it (e.g. `bash -c`, `xargs`, `find -exec`, `timeout`).
   Rule-owned commands do not appear in any preset tier; they are
   identified by registration in `internal/rules/registry.go`.
3. **Permissions** (`internal/perms/`) — Pattern-based
   allow/ask/deny entries assembled from multiple sources. Each
   tool axis (`Commands`, `EnvVars`) walks the source stack
   independently: `checkOne` resolves Commands per source,
   `checkOneEnvVar` resolves EnvVars per source, and the
   aggregated decision picks the strongest tier across axes
   via `Deny > Ask > Allow > SoftAsk`. Within a single source,
   the same tier precedence breaks ties when more than one tier
   matches on the same axis. Lower-priority sources are not
   consulted for an axis once a higher source has matched on
   that axis. Sources (highest to lowest):
   `<project>/.claude/settings.local.json` →
   `<project>/.claude/settings.json` → `~/.claude/settings.json` →
   `<project>/.agents/permissions.local.json` →
   `<project>/.agents/permissions.json` →
   `~/.agents/permissions.json` → embedded presets.
   `permissions.local.json` is a project-scoped, typically
   gitignored personal override mirroring Claude's
   `settings.local.json`; it is project-only.

   Output text that varies per agent harness (e.g. Claude
   Code's `/permissions` reference) goes through
   `internal/harness/`. `claude-hook` swaps the loader's default
   `Placeholder` harness for `ClaudeCode`; dev tools like
   `check` and `validate` keep the placeholder so harness-
   specific strings surface visibly rather than masquerading as
   one harness's wording.

### Guidelines

* **File scripts ask, inline code denies.** When `python3 script.py`
  trips a dangerous-pattern check, the decision is ask — the script
  may be the user's own code, so they need the chance to see what was
  flagged and add a permission override. When `python3 -c '...'` trips
  the same check, the decision is deny — inline code is
  agent-generated, so the agent can fix it. `bash script.sh` doesn't
  need this distinction because users can always run their scripts
  directly. When adding handling for new commands, ensure users always
  have a way to allow their files to be run without ask prompts,
  regardless of what the security filtering flags.
* **Fail closed.** If you can't verify what a command will do, deny —
  not ask. We can't verify safety, so we don't let it through.
* **Breakdown errors = denials.** When the breakdown can't handle a
  rule-owned command (e.g. an unsupported `bash` flag combination),
  return an error (not `handled=false` with nil error) so the denial
  includes a useful message. A "cannot verify" breakdown error
  should be a `model.RuleError` carrying the command's `Unverified`
  def — that both attributes the denial (`(from rule:<id>)`) and
  lets `runBreakdown` suppress it when the user disables the rule.
* **Every restrictive node needs a governing `RuleDef`.** Any rule
  that can deny/ask/soft-ask must be attributable to a catalog rule
  so the user can disable it: set it via `WithRuleDef` (rule-tree
  nodes), `CommandRules.Unverified` (a command's fail-closed
  `Default` and "cannot verify" errors), `SnippetLang.Def`, or a
  wrapper's `denyRule`. `rules.ValidateRegistry` (run by a Go test)
  fails the build if a restrictive node has no def on its path. New
  catalog rules must also be enabled by exactly one preset — see
  Presets.
* **Comment every registry entry.** Each command entry in `Registry()`
  must have a comment explaining what the breakdown and/or rules do
  for that command — the threat model, what gets extracted, and what
  gets denied. Short inline comments are fine for simple entries; use
  longer comments for commands with non-obvious behaviour.
* **Output messages show all reasons.** Users approve or deny based on
  the reason string. Show every reason for the decision — not just the
  first — so users can make an informed choice. A compound command
  that triggers three asks should surface all three, not hide two
  behind the first.
* **Test code paths, not config.** Permission hook tests exercise the
  parsing, breakdown, and rules engine — not individual entries in the
  env-var or permission lists. A new env var added to
  `dangerousEnvVars` doesn't need its own test; the existing tests
  already cover the dangerous-var code path. Add tests when a change
  introduces a new decision path, edge case, or behavioural branch.
* **Use `word` primitives, not `word.Text`.** Compare and inspect
  Words using `word.DefinitelyEqual`, `word.MayHavePrefix`,
  `word.MayContain`, etc. — never convert to a string with `word.Text`
  and then compare. `word.Text` is only for boundaries where a string
  is required: log messages, error messages, pattern matching against
  string-based permission lists, and constructing output for the user.
  In rule logic, always use the tri-state matching primitives
  directly.

## Presets

`presets/*.json` are topic-organised permission bundles. Each file may
span multiple tiers, and each tier may carry entries on any tool axis
(`Commands`, `EnvVars`). A top-level `Rules` object enables Rules-layer
rules by ID — rules ship default-OFF in the binary, so the preset
turns on the rules for its topic. Shape:

```json
{
  "description": "...",
  "Allow": {
    "Commands": {"cmd:*": "why-it's-allowed", "...": "..."}
  },
  "SoftAsk": {
    "Commands": {"...": "..."},
    "EnvVars":  {"PATH": "command lookup hijack risk"}
  },
  "Ask": {
    "Commands": {"...": "..."}
  },
  "Deny": {
    "Commands": {"...": "..."},
    "EnvVars":  {"BASH_ENV": "sourced automatically on shell startup"}
  },
  "Rules": {
    "git.branch-writes": {"Enabled": true}
  }
}
```

Entries are pattern → reason maps. Reason text surfaces in hook
output as `<pattern> - <reason>  (from <source>)`. Preset entries
carry a terse reason naming the blast radius that justifies the
tier — for Allow that is one of `Read-only`, `Local changes`,
`Shell built-in`, or `Build toolchain`; the riskier tiers name
the specific hazard (`deletes files`, `rewrites history`). A
user's own config may leave a reason empty, but presets must
carry one, enforced by `test/test-presets.sh`.

### Command pattern syntax

* `cmd:*` — matches `cmd` with 0+ args. Prefer this form. Covers both
  the bare command and any args.
* `cmd *` (space-star) — matches `cmd` with 1+ args. Use only when you
  intentionally want to exclude the bare form (rare).
* `cmd` (bare) — matches `cmd` with no args. Use only when you
  intentionally want a no-args-only allow, e.g. `git pull` in Allow
  paired with `git pull *` in a different tier.

### Env-var pattern syntax

* `NAME` — exact match on the assigned variable name.
* `NAME*` — prefix match. Covers `LD_PRELOAD`, `LD_LIBRARY_PATH`,
  etc. via `LD_*`.

No value matching: the schema concerns itself only with which
variables can be assigned, not what they're assigned to.

### Guidelines

* **Deny.Commands must use `:*`.** A bare `cmd` or `cmd *` in Deny
  leaves a bypass via the missing form. The invariant tests in
  `test/test-presets.sh` enforce this.
* **Collapse same-tier bare+starred pairs.** When both `cmd` and
  `cmd *` would live in the same tier, write `cmd:*` instead. The
  invariant tests enforce this too.
* **Rule-owned commands stay out of preset Command tiers.**
  Commands the Rules layer owns (`bash`, `sh`, `xargs`, `timeout`,
  `git remote`/`branch`/`tag`, `gh api`, `eval`, etc.) must not
  appear in `Allow`, `Ask`, or `Deny` Commands in any preset.
  Claude Code ask/deny rules override hook allow decisions, so a
  duplicated entry breaks the hook's ability to auto-allow safe
  invocations decided by the rule. This is about the Command
  tiers only — a rule-owned command's *rules* are enabled via the
  preset's `Rules` axis (e.g. `bash.unverified` lives in
  `standard-commands`'s Rules without `bash` appearing in its
  Commands).
* **Every rule is owned by exactly one preset.** Rules ship
  default-OFF, so a rule no preset enables ships permanently off,
  and a rule two presets enable can't be cleanly disabled. The
  Go invariant test (`internal/perms/ruleconfig_test.go`) enforces
  one-preset ownership and that every preset rule ID is a real
  catalog rule. When you add a rule to the catalog, add it to its
  topic preset's `Rules` in the same change.
* **Env-var policy lives in `escape-hatches.json`.** All preset-
  shipped EnvVar entries are bundled there: dangerous vars
  (BASH_ENV, LD_PRELOAD, etc.) under Deny.EnvVars, suspicious-
  but-useful vars (PATH, EDITOR, etc.) under SoftAsk.EnvVars.
  Don't sprinkle env-var entries across other presets — keeping
  them together makes the policy reviewable in one place.
