# Paddy's agent-permissions hook

Let's take agent permissions seriously!

Permissions for agents fall in a weird middle ground of not being
super robust like sandboxing, but also still requiring effort to
maintain and setup. I think this combo has led people to abandoning
them, opting instead for things like Claude Code's "auto mode" that
just does permissions based on vibes. I say that's crap! We can do
permissions and we can do them well, we just have to accept some
tradeoffs and accept that permissions can never be 100% robust.

## What this is

A PreToolUse hook for [Claude Code](https://docs.claude.com/en/docs/claude-code)
that makes its bash permission matching actually useful, plus a
library of topic-organised permission presets you can compose.

Claude Code's built-in matcher doesn't parse beyond `&&` and `||` —
a single `for` loop, pipe, or subshell makes it bail out and prompt
for everything. This hook fills the gap: it parses bash with
[`mvdan/sh`](https://github.com/mvdan/sh), walks the AST, and
applies fine-grained rules to compound commands, piped chains, and
inline interpreter code (`bash -c`, `python -c`, `perl -e`, `node
-e`, etc.) so most safe operations stop prompting without giving
the agent free rein.

The guiding principle: **agents are flawed, not malicious.** Agents
make mistakes; they don't actively work to circumvent your
permission model. So if a command is potentially destructive but
common (e.g. `git push`), prompt. If a command is catastrophic and
rare (e.g. forging credentials), block. If it's safe, allow it
silently. Save the prompts for decisions a human actually needs to
make.

## Status

Early. Ported from a larger internal tool and works against Claude
Code today. Only Claude Code is supported — the hook reads
`tool_name == "Bash"` from PreToolUse input. Codex and Copilot
adapters could follow.

## Permission tiers

Four tiers, in deny → allow precedence order:

| Tier | Behaviour |
| ---- | --------- |
| `Deny` | Hook returns `deny`. |
| `Ask` | Hook returns `ask`. Always prompts. |
| `SoftAsk` | Prompts in normal mode. In Claude Code's auto mode, falls through to its classifier for per-invocation judgement. |
| `Allow` | Hook returns `allow`. |

Within one normal source, tier precedence is
`Deny` > `Ask` > `Allow` > `SoftAsk`.

`Allow` sits above `SoftAsk` within a single normal source so an
explicit Allow opts out of a SoftAsk prompt for commands the user
has broadly trusted.

## How the hook decides

Every bash command Claude Code wants to run passes through three
layers. Any layer can deny; all must agree to allow.

### 1. Breakdown: extract what could possibly execute

`internal/breakdown/` parses the command's AST via `mvdan/sh` and
recursively unwraps common forms of nesting: pipes, subshells,
redirects, command substitution, `bash -c`, `sh -c`, `eval`,
`find -exec`, `xargs`, and wrappers like `timeout`, `env`, and
`sudo`. The result is a flat list of every command that could
*possibly* execute. If any AST node isn't recognised, the whole
command is denied. Fail closed.

For interpreter invocations (`python`, `perl`, `ruby`, `node`),
Breakdown also extracts the code itself, either inline
(`python -c '...'`) or by reading the referenced script file
(`python script.py`), and emits a code snippet alongside the
regular extracted commands.

### 2. Rules: per-command logic

`internal/rules/` holds per-command logic for commands where flat
pattern matching is too coarse. Two broad behaviours today.

**Unwrap and re-check.** For commands whose meaning depends
entirely on an argument-supplied script or inner command, the
hook pulls the inner command out and runs the entire pipeline on
it (Breakdown, Rules, Permissions). So `bash -c 'rm -rf /'` is
treated as if the agent had asked to run `rm -rf /` directly. The
original `bash -c` framing is gone by the time pattern matching
runs. Same idea for `sh -c`, `xargs`, `find -exec` / `-execdir`,
and `timeout`. The hook also reads referenced script files and
runs the same scanning over their contents.

**Deny specific dangerous forms.** Many commands have a
normally-safe primary use and a few argument shapes that turn
them into arbitrary execution channels. The rule for each such
command denies just those forms. Examples:

- `tar` with `--to-command`, `--use-compress-program`,
  `--rsh-command`, `--rmt-command`, or `--info-script`. These
  flags pipe archive contents into an arbitrary external
  command, turning a normally-safe extraction into command
  execution.
- `sed` with the `e` flag inside a substitution (`s/.../.../e`)
  or the standalone `e` command. Both execute shell commands as
  part of the substitution.
- `awk` programs containing `system()`, shell pipes inside
  `{ ... | ... }`, or `getline` from a pipe. All execute shell.
- `man --html` and `man --pager`. Both launch the configured
  browser or pager, which the user controls via environment
  variables and which can execute arbitrary code.
- `make --eval` evaluates arbitrary makefile syntax inline,
  which can shell out.
- `find -ok` and `-okdir`. Both prompt the user interactively
  for each match, and the hook can't fulfil that prompt.
- `gh api`. The rule classifies calls as read-only, or denies
  calls that could write.

Code snippets from interpreter invocations get a per-language
regex scan for the obvious shell-out patterns: `subprocess` or
`os.system` in Python, `system`/`exec`/backticks in Perl and
Ruby, `child_process` in Node, and so on. It is not a real AST
parse, more a coarse pattern check, and is expected to improve
over time. The script-vs-inline split applies here too.
Agent-generated inline code (`-c` / `-e`) cannot bypass a deny
via a permission allow. User-authored script files can be opted
out by allowing the file path explicitly.

### 3. Permissions: normal resolution plus enforced policy

`internal/perms/` matches each extracted command and assigned
environment variable against two policy planes. Normal sources use
the source-priority stack described below. Enforced presets form a
minimum policy that user config can strengthen but cannot weaken.
This is where most decisions actually land.

**Source priority decides who wins, not pattern shape.** The hook
walks sources from highest to lowest priority (full list in
Configuration below). The first source that has any matching
pattern decides the outcome, and lower-priority sources are not
consulted once a higher source has matched. So if
`~/.claude/settings.json` has `git push:*` in Allow, that
overrides a `git push *` Deny in any lower-priority source, even
though the pattern shapes are different. The match in the higher
source is final within normal resolution for that command.

**Within a single source, the tier order from above breaks ties.**
When the deciding source has matching patterns in more than one
tier, the hook applies:

```
Deny > Ask > Allow > SoftAsk
```

So a source that has both `git push:*` in Allow and
`git push origin:*` in Deny denies `git push origin main`, even
though both patterns match.

**Per-axis resolution.** Commands and environment variables each
have their own source-priority walk. A Commands match in a higher
source does not lock out EnvVars from being consulted in lower
sources, and vice versa. So a higher-source `Allow.EnvVars: PATH`
overrides a lower-source `SoftAsk.EnvVars: PATH` independently of
whatever command Allow/Deny matched.

**Enforced pattern policy is a floor.** Every matching entry from
`AGENT_PERMISSIONS_ENFORCED_PRESET_DIRS` participates. The strongest
enforced match combines with normal pattern resolution using:

```
Deny > Ask > SoftAsk > Allow > Undecided
```

For example, an enforced Deny beats a user Allow, while a user Deny
beats an enforced Allow.

Soft-ask is the one exception. It means "nudge unless this was
explicitly allowed", so an Allow in normal resolution answers an
enforced SoftAsk rather than losing to it. Enforcing a soft-ask
would otherwise make it stricter than an enforced Ask, which no
config can silence either. The reverse still holds: an enforced
Allow cannot talk a normal SoftAsk down, because the enforced plane
only ever strengthens.

Enforced presets cannot be removed with `enabled-presets` or
`disabled-presets`. Rules they enable cannot be disabled in user
config.

The Rules layer remains authoritative for commands it owns. Those
commands do not belong in preset Command tiers. An enforced preset can
enable a Rules-layer rule and prevent user config from turning it off.

**Absolute paths.** `/usr/bin/git status` matches `git status:*`
in Allow/Ask/SoftAsk only when `/usr/bin` is in the hook
process's PATH (i.e. the shell would have resolved `git` to the
same binary). Out-of-PATH absolute paths (`/tmp/evil/curl`)
require an explicit absolute-path pattern. Deny treats every
absolute path as if it were the basename — a defense-in-depth
safety net that ignores PATH.

The `agent-permissions check '<cmd>'` subcommand shows exactly
which source and pattern decided a given command, which is the
fastest way to understand a surprising prompt or denial. (Note
that `check` uses a placeholder harness, so harness-specific
strings like the Claude Code `/permissions` reference appear as
`<unknown-command-header>` instead — the live hook emits the
real text.)

## Presets

Eight topic-organised bundles, embedded in the binary at build
time so `go install` alone is enough to get a working policy:

| Preset | Covers |
| ------ | ------ |
| `standard-commands` | Shell built-ins, file metadata, text processing, system inspection, binary inspection, archives, shellcheck. The baseline an agent needs to operate a shell. Includes `source`/`.`/`rm` as SoftAsk so project-specific scoping is prompted. |
| `languages` | Python, Perl, Ruby, Node, C/C++, Go, Rust, Java, .NET, Conda. Disabling this is the "no agent-executed code in these languages" toggle. |
| `git` | Git, GitHub CLI, pre-commit. Reads allowed, local writes SoftAsk, push and gh writes Ask, auth/credential commands Deny. |
| `network-fetch` | `curl`, `wget`. SoftAsk. |
| `containers` | Daemonless runtimes Allow; Docker SoftAsk. |
| `process-control` | `kill`, `killall`, `dd`. SoftAsk. |
| `mpi` | MPI launchers. SoftAsk. |
| `escape-hatches` | Commands that can escape or bypass the permission model (sudo, ssh, su, alias, busybox, etc.). Deny. Transparent exec wrappers (`env`, `nohup`, `setsid`, `nice`, `exec`) are handled by the Rules layer instead, which unwraps and re-checks the inner command. |

Each file has the shape:

```json
{
  "description": "Short summary of what this preset covers.",
  "Allow": {
    "Commands": {"git status:*": "read-only"}
  },
  "SoftAsk": {
    "Commands": {"git commit:*": "local commit"}
  },
  "Ask": {
    "Commands": {"git push:*": "publishes commits to a remote"}
  },
  "Deny": {
    "Commands": {"sudo:*": "privilege escalation"},
    "EnvVars": {"BASH_ENV": "sourced automatically on shell startup"}
  }
}
```

Each tier is split by tool axis. `Commands` matches bash commands;
`EnvVars` matches assigned environment variables by name (with an
optional trailing `*` for prefix match, e.g. `LD_*`). Each entry
is a pattern → reason map; the reason appears in hook output as
`<pattern> - <reason>  (from <source>)`. Empty strings (`""`) are
allowed when a reason isn't useful.

A pattern like `git status:*` matches both `git status` (no args)
and `git status <anything>`. Use `git status *` (with a space) if
you need to require at least one argument, and bare `git status`
for the no-args form only.

A preset may also carry a `Rules` axis that configures Rules-layer
rules (layer 2) by ID:

```json
"Rules": {"git.branch-writes": {"Enabled": true}}
```

The built-in rules ship disabled in the binary. Each embedded preset
turns on the rules for its topic. Ordinary external presets and
`.agents` config can override that base, while enforced presets can
lock rules on. Disabling an ordinary preset removes its contribution;
another source may still decide the rule. The object shape leaves room
for future per-rule options. Override rules that are not enforced in
your `.agents/permissions.json` (see Configuration).

All ordinary presets are active by default. To narrow that set, add
`enabled-presets` or `disabled-presets` to your
`~/.agents/permissions.json`. Enforced presets remain active. See
Configuration below.

## Configuration

The hook resolves normal permissions **by source priority, per
axis**. Commands and EnvVars each walk the source stack independently.
For each axis, the first normal source with a matching pattern decides
the normal result. A separate enforced result forms the minimum policy.
The final decision aggregates across policy planes, axes, and extracted
commands using `Deny > Ask > SoftAsk > Allow > Undecided`.

Source priority, highest to lowest:

1. `<project>/.claude/settings.local.json` — Claude Code local
   overrides.
2. `<project>/.claude/settings.json` — Claude Code project settings.
3. `~/.claude/settings.json` — Claude Code user settings.
4. `<project>/.agents/permissions.local.json` — project-local
   personal overrides (typically gitignored).
5. `<project>/.agents/permissions.json` — per-project overrides.
6. `~/.agents/permissions.json` — your global overrides.
7. External presets from `AGENT_PERMISSIONS_PRESET_DIRS` —
   overridable defaults shipped by an organisation.
8. Embedded presets.

The ordinary external and embedded preset layers are filtered by
`enabled-presets` / `disabled-presets` from the most-specific
`.agents` config — local, then project, then global — that specifies
either field.

Enforced presets from
`AGENT_PERMISSIONS_ENFORCED_PRESET_DIRS` sit outside this priority
list. They are always active. Every enforced match participates, and
the strongest enforced result combines with the normal result as a
minimum policy.

`permissions.local.json` mirrors Claude Code's
`settings.local.json`: a project-scoped personal override that sits
above the committed project config and has no global counterpart.
It uses the same shape as `permissions.json` (described below).

### External presets: `AGENT_PERMISSIONS_PRESET_DIRS`

`AGENT_PERMISSIONS_PRESET_DIRS` is a colon-separated list of
directories of preset JSON files, for organisations that want to ship
overridable defaults alongside their own tooling instead of editing
users' config files. Each file uses the preset shape above; the
filename stem is the preset name. External presets outrank the embedded
set but rank below every user config source, and they participate in
`enabled-presets` / `disabled-presets` by name like any other ordinary
preset.

Load failures fail closed: a missing directory, a malformed
file, or a name colliding with another preset is a hard error,
and the hook blocks commands until it is fixed. Site policy
silently vanishing must not weaken the running policy. Give
external presets distinct names (e.g. an org prefix like
`dug-slurm`) — they already win on priority, so a collision with
an embedded name is only ever an accident.

External presets also fail closed on semantic errors. Unknown nested
fields, malformed command or environment-variable patterns, unknown
rule IDs, and Command patterns that overlap a Rules-owned command are
hard errors even when preset selection would disable the affected
preset. The last check rejects policy entries that the Rules layer
would decide before pattern matching. These checks prevent a mistake
from silently removing part of site policy.

### Enforced presets: `AGENT_PERMISSIONS_ENFORCED_PRESET_DIRS`

`AGENT_PERMISSIONS_ENFORCED_PRESET_DIRS` has the same colon-separated
directory format and preset JSON schema as the ordinary external
variable. Use it for organisation policy that users must not weaken.

Enforced presets do not join the normal first-match source stack.
Instead, the resolver finds the strongest pattern match across every
enforced preset and combines it with the normal pattern result. A user
or project can still impose a stronger pattern decision. It cannot
impose a weaker one.

`enabled-presets` and `disabled-presets` do not affect enforced
presets. An enforced `Rules` entry must set `Enabled: true`, and that
rule stays enabled even when user config sets it to false. Load and
validation failures use the same fail-closed behavior as ordinary
external presets.

### Enforcing existing presets: `AGENT_PERMISSIONS_ENFORCED_PRESETS`

A site cannot place the built-in presets in a directory it controls,
so it names them instead. `AGENT_PERMISSIONS_ENFORCED_PRESETS` takes a
comma-separated list of preset names — names rather than paths, hence
the comma — and moves each into the enforced plane:

```sh
AGENT_PERMISSIONS_ENFORCED_PRESETS=escape-hatches,git,standard-commands
```

That makes `escape-hatches`'s denials (sudo, ssh, su, unshare,
crontab, and the dangerous environment variables) a floor no user
config can weaken, and locks on the Rules each named preset owns —
including the interpreter and `bash -c` scanning, which a project
config could otherwise switch off.

`SoftAsk` entries in a named preset stay silenceable by an Allow, so
enforcing a topic does not freeze its nudges. `Allow` entries change
nothing, since the enforced plane only ever strengthens.

An unknown name is a load failure, not a no-op: a site that misspells
one would otherwise believe policy is enforced when it is not.

**Within one normal source**, tier precedence applies in this order:
`Deny > Ask > Allow > SoftAsk`.
So if a single source has both an Allow and a Deny matching the
same command, the Deny wins.

**Across normal sources**, the first match wins entirely. If
`~/.claude/settings.json` has `Bash(git push:*)` in Allow, that
overrides a `git push *` Deny in any lower-priority source —
even though the pattern shapes differ — because the higher source
already had a match. This is what gives `enabled-presets` /
`disabled-presets` and the agent-permissions config their
override power within normal resolution. Enforced policy then applies
its minimum decision.

### `~/.agents/permissions.json`

Same shape as the preset files (no `Bash(...)` wrapping — that's
Claude Code's settings.json syntax, not ours), plus optional
`enabled-presets` and `disabled-presets` arrays:

```json
{
  "Allow": {
    "Commands": {"my-tool:*": "internal CLI"}
  },
  "Deny": {
    "Commands": {"some-binary:*": ""},
    "EnvVars": {"DANGEROUS_VAR": "leaks credentials"}
  },
  "Rules": {
    "git.branch-writes": {"Enabled": false}
  },
  "disabled-presets": ["containers"]
}
```

`agent-permissions setup` writes a starter file with empty tier
objects you can fill in.

- No `enabled-presets` or `disabled-presets` → all ordinary external
  and embedded presets active (the default).
- `disabled-presets: ["containers"]` → all ordinary presets except
  those listed.
- `enabled-presets: ["git", "languages"]` → only those ordinary
  presets listed.
- Both → `enabled-presets` is applied first, then
  `disabled-presets` filters what remains.

Enforced presets remain active in every case.

If `disabled-presets` names an enforced preset,
`agent-permissions validate` reports the failed override. The preset
remains active.

A misspelled preset name silently no-ops (it just never matches), so
`agent-permissions validate` reports a name that is not an available
preset, the same way it flags a typo'd rule ID. Run
`agent-permissions presets list` to see the valid names.

`Rules` overrides Rules-layer rule config by ID. Presets enable
the rules for their topic; set `Enabled: false` here to turn one
off, or `Enabled: true` to turn on a rule no active ordinary preset
enables. Local `.agents` beats project `.agents` beats global
`.agents` beats ordinary presets. Enforced presets apply last and lock
their enabled rules on. A rule mentioned nowhere stays off. Run
`agent-permissions rules list` to see every rule ID and what it guards;
`agent-permissions validate` flags a rule ID you've typo'd.

The most-specific `.agents` config with a preset selection wins:
`<cwd>/.agents/permissions.local.json`, then
`<cwd>/.agents/permissions.json`, then the global file. If a file
specifies either field, that's the authoritative list for that
project.

## Install

Requires Go 1.25+.

```
go install github.com/sothatsit/agent-permissions/cmd/agent-permissions@latest
agent-permissions install
```

`agent-permissions install` is the only command that writes to
`~/.claude/settings.json` — it appends a PreToolUse stanza for
the Bash matcher and is idempotent. If `~/.claude/settings.json`
doesn't exist, it reports `Skipped Claude Code` rather than
creating the file.

Alternatively, grab a prebuilt binary from the GitHub Releases
page.

## Commands

```
agent-permissions claude-hook     # PreToolUse handler (used in settings.json)
agent-permissions install         # Wire into ~/.claude/settings.json
agent-permissions setup           # Write a starter ~/.agents/permissions.json
agent-permissions check '<cmd>'   # Simulate the hook and explain the decision
agent-permissions validate        # Report malformed entries and bad rule/preset references
agent-permissions presets list    # Show enforced, enabled, and disabled presets
agent-permissions rules list      # List built-in rules as 'id - description'
```

To enable or disable an ordinary preset, edit
`~/.agents/permissions.json` (or
`<project>/.agents/permissions.json`) and add to the `enabled-presets`
or `disabled-presets` array. Enforced presets cannot be disabled.

`install` refuses to write through a symlink, refuses to
overwrite settings.json structures it doesn't recognise (e.g.
`PreToolUse` that isn't an array), and preserves the existing
file's permission bits. When it can't safely auto-edit it prints
the JSON stanza for you to paste manually.

`agent-permissions check` is useful for figuring out why something
is (or isn't) prompting:

```
$ agent-permissions check 'git rm -rf .'
Command:
  git rm -rf .

Enforced policy (strongest match wins):
  (none)

Normal resolution chain (highest → lowest priority):
  preset:git
  ...

Extracted commands:
  git rm -rf .

Decision: soft_ask

Reasons:
  Soft-ask. To allow, add to your Allow permissions:
  * git rm:*  (from preset:git)
```

## Build from source

Requires Go 1.25+.

```
./build.sh
```

Outputs `bin/agent-permissions` (pure-Go static binary, no libc
dependency).

## Run tests

```
./test/test.sh
```

Runs Go unit tests, JSON preset invariants, and the bash
integration suite against the built binary.

## License

MIT. See [LICENSE](LICENSE).
