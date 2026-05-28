# agent-permissions vs nah

A close read of [manuelschipper/nah](https://github.com/manuelschipper/nah)
alongside this project. nah is the closest competitor in terms of stated
goals — a deterministic permission guard for coding agents that does more
than substring-matching on command names. The two projects share a lot
of the same insights about why pattern lists are insufficient, but
they've made very different scoping decisions.

This document is descriptive, not promotional. The goal is to capture
what each project actually does so we can decide what to learn from,
what to ignore, and where the genuine differentiation lives.

## TL;DR

Both are PreToolUse permission guards that go deeper than command-name
allow lists. They overlap meaningfully on Bash classification and the
threat classes that flow from it (wrapper evasion, git history damage,
unknown code execution, shell obfuscation). They diverge on almost
everything else.

- **agent-permissions** — Go, Claude Code Bash only, ~11k LOC, 6
  subcommands, 8 topic presets, purely deterministic, per-axis
  source-priority pattern matching with four tiers
  (Deny/Ask/Allow/SoftAsk), one `check` trace explains every
  decision.
- **nah** — Python, Claude Code (every tool) + Codex + bash/zsh
  terminal guards, ~27k LOC source + ~30k LOC tests, ~25 subcommands,
  31 action-type classify files (1,695 entries), optional LLM
  tiebreaker, action-type taxonomy with deferred `context` policies,
  sensitive-path/content scanning, taint/provenance modules.

Roughly: nah is ~5× the code and a much wider product. They cover
different problem surfaces; only the inner ring (Bash safety on
Claude Code) is a true head-to-head.

## Scale and surface

|                          | agent-permissions          | nah                                                                       |
| ------------------------ | -------------------------- | ------------------------------------------------------------------------- |
| Language                 | Go                         | Python (stdlib-only core, optional yaml/keyring)                          |
| Source LOC               | ~11k                       | ~27k                                                                      |
| Test LOC                 | ~3k Go + ~1k bash          | 30,765 pytest                                                             |
| Built-in entries         | 389 across 28 presets      | 1,695 across 31 action-type classify files                                |
| CLI verbs                | 5                          | ~25                                                                       |
| Tools guarded            | Bash only                  | Bash, Read, Write, Edit, MultiEdit, NotebookEdit, Glob, Grep, mcp__\*, Codex `apply_patch` |
| Runtimes                 | Claude Code                | Claude Code, Codex (interactive + headless), bash/zsh terminal guards    |
| LLM in the loop          | No                         | Optional (Anthropic / OpenAI / OpenRouter / Cortex / Azure cascade)       |
| Real-trace benchmark     | None                       | 101k Novita Bash calls, 4.2% ask rate, reproducible                       |

## Architecture, side by side

### agent-permissions

Three layers, fail-closed at every boundary:

1. **Breakdown** (`internal/breakdown/breakdown.go`, ~1,100 lines).
   Parses with `mvdan/sh` and walks the AST. Recursively unwraps pipes,
   subshells, `&&`/`||`/`;`, `if`/`while`/`for`/`case`, function
   defs (recorded for later call recognition), command and process
   substitution, redirects, and dynamic env-var assignments. Any AST
   node it does not explicitly handle is denied. Tracks `cwd` through
   `cd`, clears it on uncertain boundaries, denies network redirections
   to `/dev/tcp` and `/dev/udp`.
2. **Rules** (`internal/rules/`). Per-command logic in two flavours.
   *Guarded* rules deny specific dangerous flags and fall through
   otherwise (`tar --to-command`, `make --eval`, `sort
   --compress-program`, `man --html`, `nm --plugin`, `patch -e`,
   `zip -TT`, `sed e`, `awk system()`/pipe/`getline`). *Managed*
   rules own the whole decision and have a default
   (`bash`/`sh`/`xargs`/`eval`/`trap`/`command`/`timeout` etc.).
   Wrappers extract the inner command and recurse through the
   pipeline. Interpreter handlers read the code (inline `-c/-e` or
   referenced script file) and emit a `CodeSnippet` for scanning.
3. **Permissions** (`internal/perms/perms.go`). Four tiers
   (`Deny`/`Ask`/`Allow`/`SoftAsk`), each split by tool axis
   (`Commands`/`EnvVars`), resolved per-axis across sources in
   priority order:

   ```
   <cwd>/.claude/settings.local.json
   <cwd>/.claude/settings.json
   ~/.claude/settings.json
   <cwd>/.agents/permissions.json
   ~/.agents/permissions.json
   embedded presets (filtered by enabled-/disabled-presets)
   ```

   First source with any matching pattern wins; within that source the
   tier order breaks ties. Lower-priority sources are not consulted
   once a higher source has matched.

Tri-state `Word` matching (`internal/word/word.go`) distinguishes
*definitely* equal, *maybe* equal, and *definitely not* equal so that
opaque content (`$VAR`, `$(cmd)`) can be reasoned about without losing
soundness. Rules use the tri-state primitives directly rather than
flattening to strings.

### nah

Five-ish layers, with deferred and stateful pieces:

1. **Bash parsing** (`src/nah/bash.py`, ~5,400 lines). Custom Python
   parser around `shlex.split` plus quote-aware operator splitting,
   heredoc skipping, and substitution extraction. Substitutions are
   pulled out first via placeholders so that operator splitting never
   trips on a pipe inside `$(...)`. Expands `for`/`while`/`if` bodies
   only when iteration values are static; blocks dynamic loops.
2. **Taxonomy** (`src/nah/taxonomy.py`, ~3,600 lines). 40 action
   types: `filesystem_{read,write,delete}`,
   `git_{safe,write,remote_write,discard,history_rewrite}`,
   `network_{outbound,write,diagnostic}`,
   `package_{install,run,uninstall}`, `lang_exec`, `process_signal`,
   `container_{read,write,exec,destructive}`,
   `service_{read,write,destructive}`,
   `browser_{read,interact,state,navigate,exec,file}`,
   `db_{read,write}`,
   `agent_{read,write,exec_read,exec_write,exec_remote,server,exec_bypass}`,
   `obfuscated`, `unknown`. Three-phase classification: global
   trusted-user table, ~30 flag-aware classifiers (`find`, `sed`,
   `awk`, `tar`, `git`, `kubectl`, `sqlite3`, `psql`, `curl`, `wget`,
   `httpie`, `gh api`, `glab api`, `mise`, `codex`, `bazel`, `make`,
   `npx`, Windows shells, package-exec wrappers, script-exec
   detector), then project + builtin classify tables.
3. **Per-tool handlers** (`src/nah/hook.py`). Separate handlers for
   `Bash`, `Read`, `Write`, `Edit`, `MultiEdit`, `NotebookEdit`,
   `Glob`, `Grep`, and MCP tools (`mcp__*`). Writes get a content
   scan plus project-boundary check plus optional LLM review gate.
   Grep flags credential-search regexes outside the project root.
4. **Sensitive paths** (`src/nah/paths.py`). ~50 hardcoded paths with
   policies — `~/.ssh`, `~/.gnupg`, `~/.netrc` → block;
   `~/.aws`, `~/.kube`, `~/.docker`, `/var/run/docker.sock`,
   `~/.config/gcloud`, `~/.terraform.d/credentials.tfrc.json`,
   `~/.bashrc`, `~/.zshrc`, `/etc/shadow` → ask. Project boundary
   detected via `git rev-parse --show-toplevel`.
5. **LLM tiebreaker** (`src/nah/llm.py`, ~1,900 lines). Optional.
   Shared time budget across providers, prompt caching, write-review
   gate (can relax project-boundary ask → allow, or escalate
   structural allow → ask), script-veto gate for `lang_exec`, Codex
   permission-request gate. Per-session auto-state file tracks
   consecutive denies and auto-disables the LLM after a threshold.

Plus stateful side modules: `taint.py` (~850 lines) for output-taint
propagation across PreToolUse events, `provenance.py` (~1,170) for
command provenance tracking, `codex_preflight.py` (~1,040) for Codex
MCP-config auditing, and `apply_patch.py` (~360) for parsing Codex's
patch format with credential scanning on added lines.

## Decision model

The shapes are genuinely different.

**agent-permissions** decides by command-prefix pattern matching with
source priority and tier precedence. Patterns are glob-style — `cmd:*`
matches `cmd` plus 0+ args, `cmd *` requires 1+ args, bare `cmd` is
the no-args form only. A typical preset entry looks like
`"git push:*"`. The `check` subcommand prints the full resolution
chain, every extracted command, the final decision, and exactly which
pattern in which source matched. One trace, fully explainable.

**nah** decides by classifying into an action type and then looking up
that type's policy. `policies.json` maps each of 40 types to one of
four states: `allow`, `ask`, `block`, or `context`. The interesting
state is `context` — used for `filesystem_write`, `filesystem_delete`,
`lang_exec`, `network_outbound`, `db_write`, several `browser_*`, etc.
A `context` policy means "the decision depends on which path or
content is involved, resolve at runtime." That is why `cat foo.txt`
allows and `cat ~/.aws/credentials` asks without anyone writing an
explicit pattern: `paths.py` does the path check at decision time.

This gives nah more out-of-the-box smarts on sensitive-path
operations, at the cost of a decision flow that requires understanding
the action-type taxonomy *plus* the context-resolver rules to predict
the outcome. agent-permissions is easier to debug ("which pattern won")
but doesn't carry path/content context into the decision at all.

Within-source precedence in agent-permissions:

```
Deny > Ask > Allow > SoftAsk
```

Strictness order for nah's policy merging:

```
allow < context < ask < block
```

## Bash decomposition

Both unwrap deeply. Different routes, similar coverage on the obvious
constructs:

| Construct                  | agent-permissions                                                                                                                  | nah                                                                                                              |
| -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `cmd1 \| cmd2`             | `mvdan/sh` AST pipe; subshell isolation; cwd does not propagate                                                                    | Quote-/heredoc-aware operator split; substitutions lifted out via placeholders first                             |
| `&&` / `\|\|` / `;`        | AST `BinaryCmd`; `&&` propagates cwd, `\|\|` clears, both increment `ConditionalDepth`                                             | Operator split with `python_prior_env_risk` and `shell_cwd` tracking across stages                               |
| `$(...)` / backticks       | `extractSubsFromWord` → recurse on inner statements                                                                                | `_extract_substitutions` → placeholder substitution → classify inner separately                                  |
| `<(...)` / `>(...)`        | AST `ProcSubst` → recurse                                                                                                          | Extracted with kind `process_in` / `process_out`                                                                 |
| Heredocs `<<EOF`           | `mvdan/sh` handles it natively; `extractSubsFromWord` runs on `Hdoc`                                                               | Hand-rolled `_skip_heredoc` / `_strip_heredoc_bodies` because `shlex.split` chokes on apostrophes in heredoc bodies |
| `for`/`while`/`if`/`case`  | Walks AST; marks `ConditionalDepth++` so cd-tracking knows the inside is conditional                                               | `_expand_control_flow` expands the body up to 128 iterations when static; blocks on dynamic values or pipes inside |
| `bash -c "..."`            | Extracts code string only when `-c` is the sole flag; recurses on contents; other flags (`--rcfile`, `-i`) deny                    | Inline code becomes a `lang_exec` stage; context resolver scans it                                               |
| `bash script.sh`           | Reads script file, parses through the full pipeline, visited-set + symlink resolution to prevent loops                             | Script-exec detector classifies as `lang_exec`; context resolver reads the file content                          |
| `find -exec … ;`/`+`       | `breakdownFind` extracts inner words → recurses; `KeepOuter` so outer `find` still pattern-checked; `-ok`/`-okdir` deny             | `_classify_find` recurses on inner tokens; `-delete` → `filesystem_delete`                                        |
| `xargs cmd`                | `wrapperBreakdown` skips wrapper flags; `-I {} {}` denied (stdin becomes command name); `-p` / `-o` denied                          | Wrapper unwrap classified by inner action type                                                                    |
| `eval "..."`               | Joined and reparsed; denied if any arg is opaque                                                                                   | Classified as `lang_exec`                                                                                        |
| `env A=B cmd`              | Caught at `ce.Assigns`; assigning `BASH_ENV`/`LD_PRELOAD`/`PYTHONSTARTUP`/`GIT_SSH_COMMAND`/`GIT_CONFIG_KEY_*`/`TAR_OPTIONS` → deny; `PATH`/`EDITOR`/`GIT_PAGER` → suspicious | Tracks env in `Stage.env_assignments`; `PGOPTIONS` parsed for read-only enable; Python env-risk vars carried across stages |
| `> /dev/tcp/host/port`     | Denied at redirect check (`checkNetworkRedirect`)                                                                                  | Routed through normal path / composition rules                                                                    |
| `/usr/bin/sudo cmd`        | Path-invoked commands controlled by `PathMode` (Deny / Skip / Allow); basename-stripped check so `/usr/bin/sudo` still hits `sudo:*` deny | `_normalize_command_name` strips `/path/`, Windows `.exe`, version suffixes (`python3.12` → `python3`)            |

**Composition rule difference.** nah has an explicit "read | exec"
allow-list — `cat foo.txt | python3 -c '...'` (with visible inline
code) is allowed despite `lang_exec` normally being `context`.
Forbidden actions (`filesystem_delete`, `filesystem_write`,
`network_outbound`, `git_*_write`, decode/obfuscation patterns)
anywhere in the pipeline disqualify the rule. agent-permissions has
no equivalent; each pipe stage is checked independently and any one
asking causes the whole chain to ask. This is a real UX gap on one
side or a real safety gap on the other, depending on how much trust
you place in nah's visible-inline-code heuristic.

## Per-command rule coverage

Both explicitly handle: `git`, `tar`, `sed`, `awk`, `find`, `xargs`,
`bash`/`sh`, `python`/`python3`, `perl`, `node`, `ruby`, `gh api`,
interpreter file scripts.

**Only agent-permissions** explicitly handles:

- `sort --compress-program`, `man --pager` / `--html`, `make --eval`,
  `zip -TT`, `patch -e`, `nm --plugin`.
- `command -v` / `-V`, `time` / `timeout` / `stdbuf` / `strace` as
  transparent wrappers, `trap` code-string extraction.
- `cd` / `pushd` / `popd` cwd tracking, `unset -f` function-call
  invalidation.
- `sed` `e` flag and `e` command detection with semicolon-split for
  multi-command scripts.
- `awk` `system()` / `getline`-from-pipe / shell-pipe / cmd-sub
  detection.
- Custom `langSyntax` comment-stripping that preserves strings and
  distinguishes Node template-literal backticks (quotes) from
  Perl/Ruby backticks (shell-exec).

**Only nah** explicitly handles:

- `kubectl` subcommands and resource kinds (~120 lines enumerating
  safe vs. unsafe resources).
- `sqlite3` read-only shape: safe dot-commands, safe pragmas, EXPLAIN
  parsing, function blocklist for `writefile`, `load_extension`,
  `edit`, `fts3_tokenizer`.
- `psql` read-only via `PGOPTIONS=-c
  default_transaction_read_only=on` plus single-statement validation
  and safe EXPLAIN option filtering.
- GraphQL / JSON-RPC / gRPC / WebSocket / REST API detection by
  request shape; `httpie`, `glab api`.
- `mise exec` / `mise x`, `npx`, `uv tool run`, `pnpm exec` wrapper
  transparency.
- `bazel test`, Windows `cmd` / `powershell` / `pwsh`.
- `nah run claude` / `nah run codex` recursion (mutual recursion
  blocked), `codex` / `codex exec`.
- `docker exec` with `trusted_containers` allow-list.
- `sed -i` / `--in-place` write detection (combined-letter flag
  clusters too), tar mode detection from bare-mode strings
  (`tf`, `czf`) and `--list` / `--create` / `--extract` / `--append`.

## Inline interpreter code

Both go beyond just gating the interpreter command itself.

**agent-permissions** builds a per-language `langSyntax` that knows
the language's quote delimiters, line comments, block comments, and
multiline strings (Python `"""` / `'''`, Node template literals as
quotes, Perl/Ruby backticks treated as shell-exec). Comments are
stripped first; dangerous patterns are matched with a regex that
skips string literals via a SKIP/FAIL construction. Per-language deny
rules:

- **Python** — `import subprocess`, `import ctypes` / `cffi`,
  `import os` with dangerous funcs, `os.system` / `os.popen` /
  `os.exec*` calls.
- **Perl** — `use IPC::*` / `Inline::*` / `FFI::*` / `DynaLoader` /
  `XSLoader`, bare `system` / `exec`, backtick, `qx`.
- **Ruby** — `require 'open3'` / `'open4'` / `'fiddle'` / `'ffi'`,
  bare `system` / `exec` / `spawn`, `IO.popen`,
  `Open3.popen` / `capture` / `pipeline`, backtick, `%x`.
- **Node** — `require('child_process')` / `'ffi'` / `'ref'`,
  `process.binding` / `dlopen`.

Outcome asymmetry: inline `-c/-e` snippets that trip a rule **deny**
(agent-generated code can't be permission-allowed); file-script
snippets that trip the same rule **ask** so the user can
pattern-allow the file path.

**nah** classifies all interpreter code as `lang_exec` and defers to
the context resolver, which scans content using `content.py` patterns
(credential regexes, destructive patterns,
subprocess-of-dangerous-tokens, base64-pipe-bash) plus an optional
LLM script-veto gate. It also has visible-inline-exec composition
detection so `cat foo | python3 -c "filter code"` allows when the
inline code is plainly visible.

Convergent for `python3 -c 'import os; os.system("rm -rf /")'`;
divergent for native-code paths like `import ctypes` — agent-permissions
catches them by static rule, nah relies on LLM script veto if enabled.

## Threat-class coverage

nah's README enumerates 13 audited danger classes with pytest
coverage hits. Mapping to agent-permissions:

| nah class                              | Hits | agent-permissions                                                                                                                                                                                                            |
| -------------------------------------- | ----:| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Sensitive file access                  |  258 | **Not first-class.** Only via bash patterns (`cat:*` allow); no path discrimination, no `~/.ssh` blocking.                                                                                                                       |
| Wrapper evasion                        |  236 | First-class. `env`/`busybox`/`sudo`/`nohup`/`setsid`/`unshare`/`nsenter`/`exec`/`builtin`/`alias` all denied in `presets/escape-hatches.json`. Transparent wrappers (`time`/`timeout`/`stdbuf`/`strace`/`xargs`/`command`) unwrap and recurse. |
| Unknown code execution                 |  236 | First-class. `curl`/`wget` SoftAsk; `bash`/`sh`/`eval`/`trap` HookDecided with deny on bare/`-i`/`--rcfile`; heredocs walked through breakdown; file scripts scanned recursively.                                                  |
| Git history damage                     |  216 | Covered. `git push:*` Ask, `git branch`/`tag`/`remote` HookDecided with split read/write flag classification, `git checkout`/`switch`/`restore`/`reset`/`rebase`/`cherry-pick`/`revert` SoftAsk.                                  |
| Shell redirection abuse                |  187 | Partial. `/dev/tcp` / `/dev/udp` denied; no general content-on-redirect scan.                                                                                                                                                  |
| Package escalation                     |  149 | Partial. `pip install *` SoftAsk; no global-install flag detection (nah catches `-g` / `--global`).                                                                                                                            |
| Secret leaks                           |   92 | **Not covered.** No content scanning.                                                                                                                                                                                          |
| Destructive container actions          |   89 | Partial. `docker:*` SoftAsk catches everything in one bucket; nah classifies `docker rm` / `prune` / `system prune` as `container_destructive` distinctly.                                                                       |
| Secret exfiltration                    |   88 | **Not covered.** No taint tracking.                                                                                                                                                                                            |
| MCP and agent tool permissions         |   83 | **Not applicable.** agent-permissions doesn't see non-Bash tool calls.                                                                                                                                                         |
| Guard tampering                        |   67 | Partial. `install` refuses symlink writes and unknown structures; no Write-tool protection of `~/.claude/hooks` or its own config dir (nah blocks both).                                                                          |
| Project boundary escapes               |   24 | **Not covered.** No project root awareness.                                                                                                                                                                                    |
| Shell obfuscation                      |   30 | Covered. Opaque words denied in `awk` / `sed` / `eval`; cmd-subst extracted and re-checked. No base64-decode-pipe-bash detection though.                                                                                          |

Five of the 13 classes (sensitive file access, secret leaks, secret
exfiltration, MCP, project boundary) are essentially **out of scope**
for agent-permissions because it only sees Bash. They're not gaps so
much as evidence that the two projects solve different problem
shapes.

## Configuration

**agent-permissions** — JSON, hand-edited:

```json
{
  "Allow": ["my-tool:*"],
  "Deny": ["some-binary:*"],
  "disabled-presets": ["containers"]
}
```

Six tier arrays plus optional `enabled-presets` / `disabled-presets`.
Strict known-keys check rejects unrecognised fields. Files are
written via `internal/atomicfile/atomicfile.go`, which refuses
symlink writes and preserves mode bits. No project-trust gate: the
project file is consulted at its native priority slot regardless.

**nah** — YAML, with a project-trust gate:

```yaml
actions:
  filesystem_delete: ask
  git_history_rewrite: block
  lang_exec: ask
sensitive_paths:
  ~/.kube: ask
trusted_containers:
  - hermes-creatbot
```

`NahConfig` has 40+ fields: classify (global/project), actions,
sensitive paths, allow paths, trusted paths, trusted containers,
db_targets, content patterns (add / suppress / policies), credential
patterns, llm config, log config, taint config, provenance config,
active_allow, ui color, ask_fallback, terminal config. Project
`.nah.yaml` is **tighten-only** unless its exact root is registered
via `nah trust-project`. agent-permissions doesn't need an equivalent
trust gate because its project file can only express tier policy,
not redirect classification.

## CLI and observability

**agent-permissions** — five verbs: `claude-hook`, `check`, `setup`,
`install`, `presets list`. The `check` command is the headline
debugging tool — it prints the resolution chain, every extracted
command, the decision, and which pattern in which source matched. One
trace, fully explainable. No persistent logging; the hook is stateless
per invocation.

**nah** — ~25 verbs. Highlights:

- `nah test "..."` — dry-run classification (with `--tool`,
  `--pattern`, `--content` variants).
- `nah log` / `--blocks` / `--asks` / `--llm` — inspect persistent
  JSONL decision log.
- `nah types` — list 40 action types with default policies.
- `nah config show` / `path` / `presets` — inspect merged config.
- `nah audit-threat-model --format summary` — coverage report
  against the threat-model audit categories.
- `nah codex doctor` / `setup` — Codex-specific diagnostics.
- `nah run claude` / `nah run codex` — spawn target with hooks
  active for this session only.
- `nah allow` / `deny` / `classify` / `trust` / `forget` — rule
  CRUD without hand-editing YAML.

Plus an auto-state dir per session and LLM cascade attempt logs.

## Testing approach

**agent-permissions** — `test/test.sh` orchestrates Go unit tests,
JSON preset invariants (validates that no entry appears in two tiers
across files, every Deny uses the `:*` form, and no tier has both
`cmd` and `cmd *` for the same command), and a bash integration suite
against the real binary (mocks `HOME` and `CLAUDE_CONFIG_DIR` per
test). A smoke check at the top catches the "negative assertions
vacuously pass against empty output" failure mode. ~7k test LOC.

**nah** — pytest suite, ~30k LOC. Notable files:
`test_taxonomy.py` (~4,200), `test_bash.py` (~4,400), `test_llm.py`
(~1,600), `test_codex_hooks.py` (~1,900), `test_paths.py` (~950),
`test_terminal_guard.py` (~700). Also `test_audit_threat_model.py`
for the audit tooling itself. Plus a real-trace benchmark
(`benchmarks/novita_bash_friction.py`) that replays 101,194 Bash
tool calls from a public HuggingFace dataset and reports a 4.2% ask
rate, with a markdown report at
`docs/benchmarks/novita-bash-friction.md`.

agent-permissions has nothing comparable to the real-trace benchmark.
Coverage of "what fraction of real-world commands prompt" is not
reported.

## Things worth borrowing

Not a roadmap, just observations:

- **Real-trace benchmark.** nah's Novita replay is the single
  highest-leverage thing they ship that we don't. A reproducible
  ask-rate number on a public corpus is a much more honest
  evaluation signal than handcrafted test cases. Even a simpler
  version — replay any captured session log and emit per-command
  decisions — would let us measure friction objectively.
- **Action-type taxonomy as an interpretive layer.** We don't need
  to adopt their full model, but the distinction between "what does
  this command *do*" and "what should we do about it" is sometimes
  useful for grouping presets (e.g. all destructive-container ops
  in one bucket rather than `docker:*` SoftAsk).
- **Tighten-only project config.** A meaningful concern if we ever
  extend `.agents/permissions.json` beyond policy expression.

## Things to deliberately not borrow

- **LLM tiebreaker.** Adds non-determinism, a runtime dependency, a
  cost surface, and a whole prompt-engineering attack surface.
  Direct contradiction of our deterministic stance.
- **Action-type taxonomy as the primary decision model.** We chose
  pattern matching deliberately for debuggability and source-priority
  override semantics. Switching would lose the
  "one `check` trace explains everything" property.
- **Multi-runtime scope.** Codex and shell terminal guards are
  meaningful features but each multiplies the surface area we have to
  maintain. The Claude Code focus is a feature.

## Bottom line

These projects look like competitors but solve different problems.
On the inner ring — Claude Code Bash classification with deep
parsing and per-command rules — the overlap is real and the threat
coverage is broadly similar. Outside that overlap they diverge
sharply.

If a user is on Claude Code, only cares about Bash safety, and
dislikes opaque decision-making, agent-permissions covers the
high-value cases at maybe 20% of nah's surface area. If they want
defence-in-depth across every Claude tool, sensitive-file blocking,
secret detection, and Codex coverage, nah is in a different weight
class. They are not direct substitutes — they are answers to
different versions of the question *how do I make agent permissions
actually work?*
