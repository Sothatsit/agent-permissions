// Package model defines the shared types for the command permission checking
// system. Types here are used across the breakdown, perms, and rules packages.
package model

import "mvdan.cc/sh/v3/syntax"

// Decision is the result of checking a command against permissions. The
// rules/eval layer uses ordinal comparison
// (Deny > Ask > SoftAsk > Allow > Undecided), so those values must keep their
// relative order.
type Decision int

const (
	Undecided Decision = iota // no opinion - caller decides
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
	// Def is the rule definition that produced this action, or nil for a
	// structural decision with no governing rule (never user-disableable -
	// e.g. a permissive container's Allow). The evaluator stamps it as the
	// action bubbles up, taking the governing Def from the node or an
	// ancestor, so callers can attribute the decision: `check` and the hook
	// surface it as "(from rule:<Def.ID>)".
	Def *RuleDef
}

// RuleDef is the canonical definition of one user-disableable rule: its threat
// ID and the one-line description a user reads to decide whether to disable it.
// Every rule in the registry references a RuleDef rather than a bare ID string,
// so the directory of RuleDefs (in the rules package) is the single source of
// truth and a typo is a compile error. Future cross-cutting metadata (e.g. a
// category) is added here.
type RuleDef struct {
	ID          string
	Description string
}

// RuleConfig is the resolved configuration for one rule, and also the on-disk
// shape under a Rules entry in presets and permissions.json (id -> RuleConfig).
// v1 carries only Enabled; cross-cutting options (e.g. downgrading a rule's
// tier from Deny to Ask) become fields here when built. The zero value is
// disabled, matching the rules-default-OFF posture: a rule fires only when a
// preset (or user config) sets Enabled true.
type RuleConfig struct {
	Enabled bool `json:"Enabled"`
}

// RuleConfigs maps rule ID to resolved config. The loader supplies a resolved
// map: each rule's entry comes from the highest-priority ordinary source that
// mentions it (local .agents > project .agents > global .agents > ordinary
// presets), then enforced presets lock their enabled rules on. An ID mentioned
// nowhere is absent and reads as the zero value, disabled. The map must be
// populated before use. See For.
type RuleConfigs map[string]RuleConfig

// For returns the resolved config for a rule. The map must be populated first:
// a nil map is a wiring bug - a nil-map read returns the zero value, silently
// disabling every denial (fail open) - so we crash rather than run without
// knowing the config. An absent ID is a valid, resolved "disabled".
func (rc RuleConfigs) For(def *RuleDef) RuleConfig {
	if rc == nil {
		panic("RuleConfigs not populated")
	}

	return rc[def.ID]
}

// RuleError is a breakdown-layer denial attributed to a specific rule. The
// imperative wrapper/xargs checks deny by returning an error - the denial
// aborts the whole breakdown before the permissions layer runs - so they return
// a RuleError carrying the governing RuleDef. The hook and check unwrap it with
// errors.As to surface "(from rule:<id>)", the same attribution the permissions
// layer gives its decisions. A plain error (parse failure, unrecognised syntax)
// carries no Def and renders without attribution.
type RuleError struct {
	Def    *RuleDef
	Reason string
}

func (e *RuleError) Error() string { return e.Reason }

// SnippetLang holds the snippet rules for a single language.
type SnippetLang struct {
	// Def is the rule governing this language's snippet rules. All of a
	// language's snippet rules detect the same threat (running shell
	// commands from the script), so they share one def and are enabled or
	// disabled together. The registry filter drops a disabled language
	// before the scan, so the scan never consults config itself.
	Def *RuleDef
	// StripComments removes comments (not strings) before running rules.
	// Nil means no stripping.
	StripComments func(string) string
	// InterpolationContents returns string contents that can evaluate code
	// but the normal matcher skips as one quoted literal. Rules also scan
	// each returned fragment. Nil means the language has no supported
	// extractor.
	InterpolationContents func(string) []string
	Rules                 []SnippetRule
}

// SnippetRule checks code snippets for dangerous patterns and produces an
// action when matched. The rule governing it lives on the enclosing
// SnippetLang.
type SnippetRule struct {
	// Check reports whether the code contains the dangerous pattern. The
	// main source has comments stripped. Interpolated string contents are
	// passed unchanged. String literals are skipped by each matcher's
	// SKIP/FAIL regex.
	Check  func(code string) bool
	Action *Action
}

// Command is a single executable command with metadata.
type Command struct {
	// Args holds the command name (first element) and its arguments as AST
	// Words, preserving the original structure including any ParamExp or
	// CmdSubst.
	Args []*syntax.Word
	// CouldBeFuncCall is true when the command name matches a function
	// defined unconditionally in the same scope. Only used as a fallback
	// override in permissions - deny and ask patterns always take
	// precedence.
	CouldBeFuncCall bool
	// SourcePath records the chain of files that led to this command being
	// extracted (e.g. "outer.sh > helpers.sh"). Set on commands extracted
	// from scanned files.
	SourcePath string
	// RootScript is the outermost script name (e.g. "script.sh"), set on
	// commands from scanned files. Used in error messages for "./script.sh"
	// guidance.
	RootScript string
}

// BreakdownResult holds the extracted commands and any environment variable
// assignment names found prefixing commands (e.g. FOO=bar cmd -> Assigns
// contains "FOO").
type BreakdownResult struct {
	Commands     []Command
	CodeSnippets []CodeSnippet
	Assigns      []string // env var names
	// safe distinguishes fully handled input with no executable command or
	// snippet from a result that found nothing and must fall through. Checked
	// environment assignments may remain.
	safe bool
}

// SafeBreakdown returns a verified result with no executable work.
func SafeBreakdown() BreakdownResult {
	return BreakdownResult{safe: true}
}

// IsSafe reports whether the complete result was verified with nothing to
// check. Exported work fields cannot create a contradictory safe result.
func (br BreakdownResult) IsSafe() bool {
	return br.safe &&
		len(br.Commands) == 0 &&
		len(br.CodeSnippets) == 0
}

// Merge combines another BreakdownResult into this one.
func (br *BreakdownResult) Merge(other BreakdownResult) {
	safe := br.IsSafe() || other.IsSafe()
	br.Commands = append(
		br.Commands, other.Commands...)
	br.CodeSnippets = append(
		br.CodeSnippets, other.CodeSnippets...)
	br.Assigns = append(br.Assigns, other.Assigns...)
	br.safe = safe &&
		len(br.Commands) == 0 &&
		len(br.CodeSnippets) == 0
}

// State holds the mutable state during a breakdown pass. This is the
// breakdown's actual internal state - hooks can read and mutate it directly.
type State struct {
	// Cwd is the known working directory for resolving relative file paths.
	// Empty means unknown - either never set or cleared by an uncertain cd.
	// When empty, relative paths cannot be resolved and are denied.
	Cwd string
	// CwdChanged is set by cd/pushd/popd when Cwd is modified. Control flow
	// boundaries check this and clear Cwd when set (because we can't
	// guarantee the cd took effect). The && handler is the exception - it
	// does not clear, because the right side only runs when the left
	// succeeded.
	CwdChanged bool
	// Visited tracks absolute file paths already scanned.
	Visited map[string]bool
	// Funcs tracks function names defined at unconditional scope.
	Funcs map[string]bool
	// SawUnsetF is set when unset -f is seen.
	SawUnsetF bool
	// ConditionalDepth tracks nesting in conditional constructs.
	ConditionalDepth int
	// FilePath is a stack of files being scanned.
	FilePath []string
	// RootScript is the outermost script name.
	RootScript string
	// RuleConfig is the resolved per-rule configuration. Breakdown
	// functions consult it before applying an imperative denial: a disabled
	// deny-flag rule is skipped (the breakdown continues), and a disabled
	// .unverified rule makes the function decline to unwrap so the command
	// falls through to the permissions layer instead of being denied. The
	// declarative layers (rule trees, snippets) are filtered before
	// evaluation and never consult this.
	RuleConfig RuleConfigs
}

// CodeSnippet holds non-bash code extracted from a command (e.g. Python source
// from python3 script.py or python3 -c "code"). The orchestrator scans these
// for dangerous patterns and checks SourceScript against user permissions.
type CodeSnippet struct {
	// Language identifies the code's language (e.g. "python") for matching
	// against snippet rules.
	Language string
	// Code is the source text to scan.
	Code string
	// SourceScript is the original command args (e.g. [python3, script.py])
	// preserved as Words for permission checking without string conversion.
	SourceScript []*syntax.Word
	// SourceFile is the file path the code was read from, or "" for inline
	// code (e.g. -c). Used to choose between deny (inline) and ask (file).
	SourceFile string
}

// BreakdownWork is the work extracted from one command. Its fields are
// additive: a wrapper can produce several kinds of work.
type BreakdownWork struct {
	// WorkingDirectory scopes the inner work to this directory. The
	// framework scans its source word under the outer state, resolves this
	// directory with the same rules as cd, then restores the outer state.
	WorkingDirectory *syntax.Word
	// Commands to recurse into. Each slice of Words is processed directly
	// through the AST walker (no print-and-reparse round trip). Use this
	// for inner commands that are already structured as separate arg Words
	// (e.g. timeout 5 [ls -la], find -exec [git status] ;). Preserve the
	// original Word pointers so the framework can tell which outer args
	// were forwarded.
	Commands [][]*syntax.Word
	// CodeStrings to recurse into. Each string is re-parsed through
	// breakdownAt. Use this for code extracted from a single Word's
	// resolved content (e.g. bash -c "code", eval args, trap code)
	// where the Word's text representation includes quotes that shouldn't
	// be in the code. The owner must reject opaque source before adding it
	// here.
	CodeStrings []string
	// Assigns are environment-variable assignments the wrapper applies to
	// its inner command (e.g. env NAME=val cmd). The framework records each
	// name on the EnvVars deny axis. Their source words are absent from
	// Commands, so the framework scans them as consumed arguments.
	// Exec-style wrappers (timeout, nohup, ...) leave this nil: they exec
	// the inner command directly via execvp, so a leading NAME=val is the
	// program name, not an assignment.
	Assigns []*syntax.Assign
	// ScanFiles lists file paths to scan directly (e.g. bash script.sh).
	// The framework handles isolation (new process state) and rootScript
	// automatically.
	ScanFiles []string
	// CodeSnippets holds non-bash code extracted from the command (e.g.
	// Python source). The framework transfers these to BreakdownResult for
	// the orchestrator to scan.
	CodeSnippets []CodeSnippet
}

func (w BreakdownWork) empty() bool {
	return w.WorkingDirectory == nil &&
		len(w.Commands) == 0 &&
		len(w.CodeStrings) == 0 &&
		len(w.Assigns) == 0 &&
		len(w.ScanFiles) == 0 &&
		len(w.CodeSnippets) == 0
}

// OuterDisposition is the framework action selected by a BreakdownOutcome.
// Callers choose it through an outcome constructor rather than setting it.
type OuterDisposition uint8

const (
	invalidBreakdownOutcome OuterDisposition = iota
	OuterFallThrough
	OuterSafe
	OuterReplace
	OuterKeep
)

// BreakdownOutcome says what happens to the outer command and carries any work
// extracted from it. Its zero value is invalid; construct outcomes with
// FallThrough, Safe, ReplaceOuter, or KeepOuter.
type BreakdownOutcome struct {
	disposition OuterDisposition
	work        BreakdownWork
}

// FallThrough keeps the outer command for normal flattening. Use it when a hook
// declines to unwrap an invocation or only mutates breakdown state.
func FallThrough() BreakdownOutcome {
	return BreakdownOutcome{disposition: OuterFallThrough}
}

// Safe removes an outer command that the hook has fully checked and found to
// contain no work. The framework still scans its original arguments for shell
// expansions.
func Safe() BreakdownOutcome {
	return BreakdownOutcome{disposition: OuterSafe}
}

// ReplaceOuter removes the outer command after processing the extracted work.
// It panics when work is empty; use Safe for that outcome.
func ReplaceOuter(work BreakdownWork) BreakdownOutcome {
	if work.empty() {
		panic("ReplaceOuter requires non-empty BreakdownWork")
	}

	return BreakdownOutcome{
		disposition: OuterReplace,
		work:        work,
	}
}

// KeepOuter processes the extracted work, then also flattens the outer command
// through normal rules and permissions. It panics when work is empty; use
// FallThrough when there is nothing to extract.
func KeepOuter(work BreakdownWork) BreakdownOutcome {
	if work.empty() {
		panic("KeepOuter requires non-empty BreakdownWork")
	}

	return BreakdownOutcome{
		disposition: OuterKeep,
		work:        work,
	}
}

// Work returns the work extracted from the outer command.
func (o BreakdownOutcome) Work() BreakdownWork {
	return o.work
}

// Disposition returns the selected outer-command action. The bool is false for
// an outcome that did not come from a constructor.
func (o BreakdownOutcome) Disposition() (OuterDisposition, bool) {
	valid := o.disposition >= OuterFallThrough &&
		o.disposition <= OuterKeep
	return o.disposition, valid
}
