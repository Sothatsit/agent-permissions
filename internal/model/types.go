// Package model defines the shared types for the command
// permission checking system. Types here are used across
// the breakdown, perms, and rules packages.
package model

import "mvdan.cc/sh/v3/syntax"

// Decision is the result of checking a command against
// permissions. The rules/eval layer uses ordinal
// comparison (Deny > Ask > SoftAsk > Allow > Undecided),
// so those values must keep their relative order.
type Decision int

const (
	Undecided Decision = iota // no opinion — caller decides
	Allow                     // explicitly permitted
	SoftAsk                   // ask in normal, classifier in auto
	Ask                       // prompt required (even in auto)
	Deny                      // blocked
)

func (d Decision) String() string {
	switch d {
	case Undecided:
		return "undecided"
	case Allow:
		return "allow"
	case SoftAsk:
		return "soft_ask"
	case Ask:
		return "ask"
	case Deny:
		return "deny"
	}
	return "unknown"
}

// Language constants for code snippets.
const (
	LangPython = "python"
	LangPerl   = "perl"
	LangRuby   = "ruby"
	LangNode   = "node"
	LangSed    = "sed"
	LangAwk    = "awk"
)

// Action is a decision with a human-readable reason.
type Action struct {
	Decision Decision
	Reason   string
	// Def is the rule definition that produced this action,
	// or nil for a structural decision with no governing
	// rule (never user-disableable — e.g. a permissive
	// container's Allow). The evaluator stamps it as the
	// action bubbles up, taking the governing Def from the
	// node or an ancestor, so callers can attribute the
	// decision: `check` and the hook surface it as "(from
	// rule:<Def.ID>)".
	Def *RuleDef
}

// RuleDef is the canonical definition of one user-disableable
// rule: its threat ID and the one-line description a user reads
// to decide whether to disable it. Every rule in the registry
// references a RuleDef rather than a bare ID string, so the
// directory of RuleDefs (in the rules package) is the single
// source of truth and a typo is a compile error. Future
// cross-cutting metadata (e.g. a category) is added here.
type RuleDef struct {
	ID          string
	Description string
}

// RuleConfig is the resolved configuration for one rule, and
// also the on-disk shape under a Rules entry in presets and
// permissions.json (id -> RuleConfig). v1 carries only
// Enabled; cross-cutting options (e.g. downgrading a rule's
// tier from Deny to Ask) become fields here when built. The
// zero value is disabled, matching the rules-default-OFF
// posture: a rule fires only when a preset (or user config)
// sets Enabled true.
type RuleConfig struct {
	Enabled bool `json:"Enabled"`
}

// RuleConfigs maps rule ID to resolved config. The loader
// supplies a resolved map: each rule's entry comes from the
// highest-priority ordinary source that mentions it (local
// .agents > project .agents > global .agents > ordinary
// presets), then enforced presets lock their enabled rules
// on. An ID mentioned nowhere is absent and reads as the
// zero value, disabled. The map must be populated before use
// — see For.
type RuleConfigs map[string]RuleConfig

// For returns the resolved config for a rule. The map must be
// populated first: a nil map is a wiring bug — a nil-map read
// returns the zero value, silently disabling every denial
// (fail open) — so we crash rather than run without knowing
// the config. An absent ID is a valid, resolved "disabled".
func (rc RuleConfigs) For(def *RuleDef) RuleConfig {
	if rc == nil {
		panic("RuleConfigs not populated")
	}
	return rc[def.ID]
}

// RuleError is a breakdown-layer denial attributed to a
// specific rule. The imperative wrapper/xargs checks deny by
// returning an error — the denial aborts the whole breakdown
// before the permissions layer runs — so they return a
// RuleError carrying the governing RuleDef. The hook and
// check unwrap it with errors.As to surface "(from
// rule:<id>)", the same attribution the permissions layer
// gives its decisions. A plain error (parse failure,
// unrecognised syntax) carries no Def and renders without
// attribution.
type RuleError struct {
	Def    *RuleDef
	Reason string
}

func (e *RuleError) Error() string { return e.Reason }

// SnippetLang holds the snippet rules for a single
// language.
type SnippetLang struct {
	// Def is the rule governing this language's snippet
	// rules. All of a language's snippet rules detect the
	// same threat (running shell commands from the script),
	// so they share one def and are enabled or disabled
	// together. The registry filter drops a disabled
	// language before the scan, so the scan never consults
	// config itself.
	Def *RuleDef
	// StripComments removes comments (not strings)
	// before running rules. Nil means no stripping.
	StripComments func(string) string
	// InterpolationContents returns string contents that can evaluate code but
	// the normal matcher skips as one quoted literal. Rules also scan each
	// returned fragment. Nil means the language has no supported extractor.
	InterpolationContents func(string) []string
	Rules                 []SnippetRule
}

// SnippetRule checks code snippets for dangerous patterns
// and produces an action when matched. The rule governing
// it lives on the enclosing SnippetLang.
type SnippetRule struct {
	// Check reports whether the code contains the dangerous pattern. The main
	// source has comments stripped. Interpolated string contents are passed
	// unchanged. String literals are skipped by each matcher's SKIP/FAIL regex.
	Check  func(code string) bool
	Action *Action
}

// Command is a single executable command with metadata.
type Command struct {
	// Args holds the command name (first element) and its
	// arguments as AST Words, preserving the original
	// structure including any ParamExp or CmdSubst.
	Args []*syntax.Word
	// CouldBeFuncCall is true when the command name
	// matches a function defined unconditionally in the
	// same scope. Only used as a fallback override in
	// permissions — deny and ask patterns always take
	// precedence.
	CouldBeFuncCall bool
	// SourcePath records the chain of files that led to
	// this command being extracted (e.g. "outer.sh >
	// helpers.sh"). Set on commands extracted from
	// scanned files.
	SourcePath string
	// RootScript is the outermost script name (e.g.
	// "script.sh"), set on commands from scanned files.
	// Used in error messages for "./script.sh" guidance.
	RootScript string
}

// BreakdownResult holds the extracted commands and any
// environment variable assignment names found prefixing
// commands (e.g. FOO=bar cmd → Assigns contains "FOO").
type BreakdownResult struct {
	Commands     []Command
	CodeSnippets []CodeSnippet
	Assigns      []string // env var names
	// Safe is true when the input was fully analyzed and
	// contains no executable commands (e.g. [[ test ]]
	// or (( arithmetic ))). Distinguishes "safe, nothing
	// to check" from "no commands, fall through".
	Safe bool
}

// Merge combines another BreakdownResult into this one.
func (br *BreakdownResult) Merge(other BreakdownResult) {
	br.Commands = append(
		br.Commands, other.Commands...)
	br.CodeSnippets = append(
		br.CodeSnippets, other.CodeSnippets...)
	br.Assigns = append(br.Assigns, other.Assigns...)
	br.Safe = br.Safe || other.Safe
}

// State holds the mutable state during a breakdown pass.
// This is the breakdown's actual internal state — hooks
// can read and mutate it directly.
type State struct {
	// Cwd is the known working directory for resolving
	// relative file paths. Empty means unknown — either
	// never set or cleared by an uncertain cd. When
	// empty, relative paths cannot be resolved and are
	// denied.
	Cwd string
	// CwdChanged is set by cd/pushd/popd when Cwd is
	// modified. Control flow boundaries check this and
	// clear Cwd when set (because we can't guarantee
	// the cd took effect). The && handler is the
	// exception — it does not clear, because the right
	// side only runs when the left succeeded.
	CwdChanged bool
	// Visited tracks absolute file paths already scanned.
	Visited map[string]bool
	// Funcs tracks function names defined at
	// unconditional scope.
	Funcs map[string]bool
	// SawUnsetF is set when unset -f is seen.
	SawUnsetF bool
	// ConditionalDepth tracks nesting in conditional
	// constructs.
	ConditionalDepth int
	// FilePath is a stack of files being scanned.
	FilePath []string
	// RootScript is the outermost script name.
	RootScript string
	// RuleConfig is the resolved per-rule configuration.
	// Breakdown functions consult it before applying an
	// imperative denial: a disabled deny-flag rule is
	// skipped (the breakdown continues), and a disabled
	// .unverified rule makes the function decline to unwrap
	// so the command falls through to the permissions layer
	// instead of being denied. The declarative layers (rule
	// trees, snippets) are filtered before evaluation and
	// never consult this.
	RuleConfig RuleConfigs
}

// CodeSnippet holds non-bash code extracted from a command
// (e.g. Python source from python3 script.py or python3 -c
// "code"). The orchestrator scans these for dangerous
// patterns and checks SourceScript against user permissions.
type CodeSnippet struct {
	// Language identifies the code's language (e.g.
	// "python") for matching against snippet rules.
	Language string
	// Code is the source text to scan.
	Code string
	// SourceScript is the original command args (e.g.
	// [python3, script.py]) preserved as Words for
	// permission checking without string conversion.
	SourceScript []*syntax.Word
	// SourceFile is the file path the code was read
	// from, or "" for inline code (e.g. -c). Used to
	// choose between deny (inline) and ask (file).
	SourceFile string
}

// UnwrapResult tells the breakdown framework what a
// command unwraps to. Returned by BreakdownFunc hooks.
//
// Return semantics:
//   - nil: the hook did not unwrap the command. The outer
//     command falls through to normal flattening. Use
//     this for state-only mutations (cd updates Cwd) or
//     when the hook can't handle the invocation (bare
//     bash falls to the rules deny).
//   - &UnwrapResult{} (empty): the hook handled the
//     command and it is safe — no inner commands to
//     check. The outer command is replaced (not emitted).
//     Use this for safe operations (command -v, trap -l,
//     bare xargs).
//   - &UnwrapResult{Commands: ...}: inner commands
//     replace the outer command. The framework processes
//     them and the outer command is not emitted.
//   - &UnwrapResult{KeepOuter: true, Commands: ...}:
//     inner commands are extracted AND the outer command
//     continues through normal flattening (cmd-sub
//     extraction, rules, permissions). Use this when the
//     breakdown extracts embedded commands but the outer
//     command itself still needs independent checking
//     (e.g. find -exec extracts inner commands, but the
//     outer find may have flags or args that need
//     rules/permissions evaluation).
type UnwrapResult struct {
	// WorkingDirectory scopes the inner work to this directory. The framework
	// scans its shell substitutions under the outer state, resolves it with the
	// same rules as cd, then restores the outer state.
	WorkingDirectory *syntax.Word
	// Commands to recurse into. Each slice of Words is
	// processed directly through the AST walker (no
	// print→reparse round trip). Use this for inner
	// commands that are already structured as separate
	// arg Words (e.g. timeout 5 [ls -la], find -exec
	// [git status] ;).
	Commands [][]*syntax.Word
	// CodeStrings to recurse into. Each string is
	// re-parsed through breakdownAt. Use this for code
	// extracted from a single Word's resolved content
	// (e.g. bash -c "code", eval args, trap code)
	// where the Word's text representation includes
	// quotes that shouldn't be in the code.
	CodeStrings []string
	// Assigns are environment-variable assignments the
	// wrapper applies to its inner command (e.g.
	// env NAME=val cmd). The framework records each name on
	// the EnvVars deny axis and extracts command
	// substitutions from each value — so a wrapper that sets
	// env vars carries that execution-relevant part forward
	// rather than dropping it. Exec-style wrappers (timeout,
	// nohup, ...) leave this nil: they exec the inner command
	// directly via execvp, so a leading NAME=val is the
	// program name, not an assignment, and is not honoured.
	Assigns []*syntax.Assign
	// ScanFiles lists file paths to scan directly (e.g.
	// bash script.sh). The framework handles isolation
	// (new process state) and rootScript automatically.
	ScanFiles []string
	// CodeSnippets holds non-bash code extracted from
	// the command (e.g. Python source). The framework
	// transfers these to BreakdownResult for the
	// orchestrator to scan.
	CodeSnippets []CodeSnippet
	// ShellWords contains words that the outer shell still expands before a
	// handled wrapper runs. The framework extracts command and process
	// substitutions even though the outer command itself is replaced. Language
	// wrappers use this for program sources and data. Either can run shell code
	// before the language runtime sees it.
	ShellWords []*syntax.Word
	// KeepOuter preserves the outer command alongside
	// the extracted inner commands. When false (default),
	// the inner commands replace the outer command. When
	// true, the outer command continues through normal
	// flattening — cmd-sub extraction from args, rules
	// evaluation, and permissions checking all apply.
	KeepOuter bool
}
