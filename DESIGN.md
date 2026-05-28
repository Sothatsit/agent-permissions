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

`~/.agents/permissions.json` and `<project>/.agents/permissions.json`
are the agent-permissions config locations. The hook also reads
Claude Code's `settings.json` so Claude Code-native rules continue
to work.

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

1. `<project>/.claude/settings.json`
2. `~/.claude/settings.json`
3. `<project>/.agents/permissions.json` (explicit entries)
4. `~/.agents/permissions.json` (explicit entries)
5. Embedded presets (selected by the most-specific
   `.agents/permissions.json` that specifies `enabled-presets` or
   `disabled-presets`)

### Within-source tier precedence

When the deciding source has matches in more than one tier, the
order is:

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

- Neither field present → all embedded presets active. Default;
  auto-includes new presets shipped in future binary updates.
- `disabled-presets: ["containers"]` → all except listed. The
  recommended way to opt out.
- `enabled-presets: ["git", "languages"]` → explicit allow-list.
  For users who want full control.
- If both present, `disabled-presets` wins (it filters whatever
  `enabled-presets` resolved to).

Project-level `.agents/permissions.json` overrides global for preset
selection: if the project file specifies either field, that's the
authoritative list for that CWD.

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
| `setup` | Write a populated `~/.agents/permissions.json` with all presets enabled and empty user-custom tier arrays. |
| `install` | Wire the hook into known harness configs. Today: appends the PreToolUse stanza to `~/.claude/settings.json` (merging into an existing Bash matcher when one exists). Refuses to write through symlinks or modify unrecognised structures (e.g. `PreToolUse` that isn't an array), preserves existing file mode, and prints the JSON stanza for hand-paste when it cannot safely auto-edit. Idempotent. |
| `presets list` | Show embedded presets grouped into Enabled/Disabled with the reason next to each entry. The previous `presets enable` and `presets disable` subcommands were removed — with "all enabled by default" the enable side became a confusing no-op, and the disable side too easily produced contradictory `enabled-presets` / `disabled-presets` files. Users edit `~/.agents/permissions.json` directly to opt in or out. |
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
Commands resolution. Each axis walks the source stack
independently, then the final decision aggregates across axes
via the existing tier precedence.

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

Revised: **source-priority resolution**. For each bash command being
checked, walk sources highest → lowest priority. The first source
that has *any* matching pattern decides the outcome via
within-source tier precedence. Lower-priority sources are not
consulted once a higher source has matched.

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
is gone. The remaining question is whether users can disable or
reconfigure individual rules from their permissions.json. For
example: "disable the rule that denies `tar --to-command`", or
"treat `sed -e ...e...` as ask instead of deny". That gives users
real control over what the rules layer does on their behalf,
rather than the current take-it-or-leave-it behaviour.

Big design area. Revisit properly before implementing.

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

### `setup` could port existing Claude Code permissions

> Maybe as part of `setup` in the future we could have it port,
> say, Claude Code permissions into `permissions.json`. That would
> be neat.

Useful onboarding path for existing Claude Code users who have
already curated their `settings.json` and want the same set
managed by agent-permissions going forward.

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

Implementation: `internal/perms/loader.go` builds a list of
`SourcePerms` in priority order (highest first). For each source,
patterns are sorted alphabetically by `Raw` for deterministic
ordering of `reason` text. `Permissions.checkOne` walks the list
top-down; for each source it checks Deny → Ask → Allow → SoftAsk
and returns on the first match. No cross-source deduplication or
rewriting is performed.

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
