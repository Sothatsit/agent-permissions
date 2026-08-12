# Code preferences sweep

Date: 2026-08-12

This pass reviewed the Go commands and internal packages, the preset loader,
the shell test harness, and the build and release scripts. Behaviour changed
only where a failing case was reproduced or where malformed policy could be
accepted as valid. The remaining items below need design work, carry a wider
compatibility cost, or would add too much unrelated churn to this pass.

## Changes made

### Command analysis

- Nested command and process substitutions now merge the full breakdown
  result. They used to keep extracted commands but drop inline interpreter
  snippets. As a result, Python code inside a substitution could bypass the
  snippet scanner and be allowed.
- Script scanning now opens one file descriptor, rejects non-regular files,
  and reads through a hard byte limit. The old stat-then-read sequence could
  race a growing file, and a FIFO with a reported size of zero could block or
  read without a bound.
- The script size constant and related variables now state their byte units.

### Config and preset loading

- Agent config decoding now rejects unknown fields at every level. Nested
  misspellings such as `Allow.Commandz` used to disappear silently.
- Permission configs and presets now reject `null` at any depth. The standard
  JSON decoder otherwise turns null maps, strings, lists, and booleans into
  zero values. An enforced preset containing `null` could therefore load as
  empty policy.
- The two policy formats share null and exact-key checks. The exact check is
  needed because the standard strict decoder accepts field names that differ
  only in case. The explicit key lists mirror the Go JSON tags for now. Item 5
  records the larger decoder work that would remove that sync point.
- Atomic file errors retain their underlying filesystem cause. The installer
  can now recognise a permission error and show the manual install stanza.

### Installer

- Generated hook commands use the shell library's Bash quoting. Install paths
  containing spaces or metacharacters now produce an executable command.
- Existing hook detection parses the command as Bash. It accepts quoted paths
  while still rejecting wrapper commands and incidental text matches.
- The merge scans every Bash matcher before adding a hook. It no longer adds a
  duplicate to the first matcher when the hook already exists in a later one.
- Settings numbers remain `json.Number` values while the installer edits the
  generic object. Large integers are no longer rounded through `float64`.
- A top-level `null` settings document is rejected and left untouched.
- Hook stanza construction has one definition, and JSON formatting errors are
  no longer discarded.

### Structure and test tooling

- The Claude config directory decision now has one implementation shared by
  `claude-hook`, `check`, and `validate`.
- Hook output accepts the existing decision type until JSON encoding rather
  than passing protocol values around as unchecked strings. Output write
  failures now return to the command boundary instead of being ignored.
- Preset group calls use a named group instead of two opaque booleans.
- Command functions touched by the pass now start with an action. The
  rule-config resolver uses the same global, project, local argument order as
  the adjacent preset resolver.
- The preset command uses `strings.Join`. Unused shell assertion helpers and a
  stale parser-test comment were removed.
- The preset shell suite now owns only cross-file data invariants. JSON schema
  checks go through the production Go parser, so there is no second schema to
  keep in sync.
- Every `jq` capture in the preset checks now handles failure. The duplicate
  check also catches the same pattern repeated in the same tier across files.
- Sourced tests return instead of exiting the orchestrator.
- The benchmark now preserves a failed hook's exit status, returns failure,
  and writes generated JSON only after `jq` succeeds. Its regression wrapper
  also rejects explicit failed cases and a non-zero benchmark exit.

## Issues left for review

1. **String interpolation can hide executable code.** The shared snippet
   scanner skips complete string literals. It also skips expressions embedded
   in Python f-strings, Ruby interpolation, and Node template strings. These
   verified examples are allowed even though their direct forms are denied.

   ```bash
   agent-permissions check \
     "python3 -c 'import os; f\"{os.system(chr(105))}\"'"
   agent-permissions check \
     "ruby -e 'value = \"#{system(%q(id))}\"'"
   ```

   A sound fix needs language-aware interpolation scanning. Treating every
   interpolated string as dangerous would be a broad compatibility change.

2. **Execution-directory wrappers lose their context.** `env -C/--chdir`
   strips the wrapper and checks the inner command in the original directory.
   This verified command scans the repository's `README.md`, although runtime
   resolves the file under `/var/empty`.

   ```bash
   agent-permissions check \
     'env -C /var/empty python3 README.md'
   ```

   `chroot` has the same host-versus-target namespace problem by inspection.
   The breakdown state needs an execution context, or these forms need a
   deliberate conservative denial until that context exists.

3. **Preset loading has more than one validity boundary.** `presets.All`
   performs structural decoding, while semantic checks require a separate
   `perms.ValidateExternalPresets` call that each caller must remember. The
   `validate` command also reloads presets and configs during one invocation,
   so it can combine facts from different filesystem snapshots. A single
   validated snapshot should feed resolution, listing, setup, and validation.

4. **Resolved permissions are not ready to evaluate.** `perms.Resolve` returns
   permissions with rule maps unset. Both `claude-hook` and `check` must build
   a registry, filter it, and mutate the returned permissions in the same
   order. The resolved result should own one evaluation-ready registry so a
   new caller cannot omit part of that protocol.

5. **Duplicate JSON keys are still accepted.** The standard decoder keeps one
   value when a config, preset, or settings file repeats a key. A repeated
   `Deny` or hook field can therefore be lost before validation or rewriting.
   Detecting duplicates needs token-level decoding shared by all three JSON
   boundaries. That decoder should also derive exact keys from the typed
   schema so the current allowlists do not mirror struct tags.

6. **Embedded presets expose mutable cached data.** `presets.Embedded` returns
   pointers from a process-wide cache. `All` copies each preset struct before
   changing its enforced flag, but the nested maps remain shared. The API
   should return immutable data or a complete copy.

7. **Parser failure policy depends on a currently dead path.** `perms.Evaluate`
   denies a parser error through the command's unverified rule without knowing
   whether that rule is enabled. Every current parser command is parsed during
   breakdown first, which makes the branch unreachable today. A future
   parser-only command would make it reachable and could create a denial that
   cannot be disabled. Parser failure and rule filtering need one explicit
   contract before such a command is added.

8. **Several model types permit contradictory states.** `ParseResult` is a
   boolean-tagged union. `UnwrapResult` uses nil, an empty pointer,
   `KeepOuter`, and several optional lists as an outcome protocol. `FullParser`
   also relies on caller-sorted flags and a field changed after construction.
   These should become named outcomes and construction-time choices, but that
   refactor touches every command wrapper.

9. **Some tests bind to private helpers.** `internal/perms/rules_test.go` and
   much of `suggest_test.go` assert helper output directly even though
   `Permissions.Check` and the shipped `check` command are cheap boundaries.
   The important precedence, suggestion, and reason-layout cases should move
   to those boundaries before the helper tests are removed.

10. **Timing and release logic need their own repository tools.** The benchmark
    measures with wall-clock `date`, which can move during a run. A monotonic
    measurement needs one process to own both timestamps without adding tool
    startup time to every sample. The release workflow also contains its own
    cross-build, archive, and checksum logic instead of calling a repository
    script. Both changes affect developer and CI workflows and deserve a
    separate pass.

11. **Plain ASCII and layout deviations are widespread.** Comments, test
    labels, and some user output use arrows, em dashes, and check marks. There
    are also many pre-existing short comment lines and missing blank lines
    after blocks. A mechanical replacement would produce a large review diff.
    Changing user output would also be observable, so this pass records the
    cleanup rather than mixing it with the bug fixes.

12. **Two command views mirror resolver decisions.** The preset-list command
    independently mirrors preset selection, and validation independently
    collects parts of resolved state. These can drift from hook behaviour.
    They should consume the validated snapshot from item 3 rather than expose
    another shared helper shaped around the current implementation.

## Verification

- `./build.sh` built the Linux AMD64 binary from the final tree.
- `./test/test.sh` passed all 1466 tests against that binary.
- `go vet ./...` passed.
- `bash -n` and ShellCheck passed for the build and test scripts.
- `gofmt` reported no diff in the Go tree.
- The benchmark passed all 11 cases under 50 ms. Its mean was 6.17 ms.
