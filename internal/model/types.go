// Package model defines the shared types for the command permission checking
// system. Types here are used across the breakdown, perms, and rules packages.
package model

import "mvdan.cc/sh/v3/syntax"

// Decision is the result of checking a command against permissions. The
// rules/eval layer uses ordinal comparison (Deny > Ask > SoftAsk > Allow >
// Undecided), so those values must keep their relative order.
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

const (
	LangPython = "python"
	LangPerl   = "perl"
	LangRuby   = "ruby"
	LangNode   = "node"
	LangSed    = "sed"
	LangAwk    = "awk"
)

type Action struct {
	Decision Decision
	Reason   string
	// Def is nil for a structural decision with no governing rule, which is
	// never user-disableable. The evaluator stamps it from the node or an
	// ancestor as the action bubbles up, so callers can report
	// "(from rule:<Def.ID>)".
	Def *RuleDef
}

// RuleDef is the canonical definition of one user-disableable rule. Rules
// reference a RuleDef rather than a bare ID string, so the directory of
// RuleDefs in the rules package is the single source of truth and a typo is a
// compile error.
type RuleDef struct {
	ID          string
	Description string
}

// RuleConfig is the resolved configuration for one rule, and also its on-disk
// shape under a Rules entry in presets and permissions.json. The zero value is
// disabled, matching the rules-default-OFF posture.
type RuleConfig struct {
	Enabled bool `json:"Enabled"`
}

// RuleConfigs maps rule ID to resolved config. An ID no source mentions is
// absent and reads as the zero value, disabled.
type RuleConfigs map[string]RuleConfig

// For returns the resolved config for a rule. A nil map is a wiring bug: its
// reads would return the zero value and silently disable every denial, so we
// crash rather than run without knowing the config.
func (rc RuleConfigs) For(def *RuleDef) RuleConfig {
	if rc == nil {
		panic("RuleConfigs not populated")
	}

	return rc[def.ID]
}

// RuleError is a breakdown-layer denial attributed to a specific rule. A
// breakdown denial aborts the whole pass before the permissions layer runs, so
// it carries its RuleDef to give the same "(from rule:<id>)" attribution the
// permissions layer gives its decisions.
type RuleError struct {
	Def    *RuleDef
	Reason string
}

func (e *RuleError) Error() string { return e.Reason }

type SnippetLang struct {
	// Def governs every one of a language's snippet rules, because they all
	// detect the same threat: running shell commands from the script. The
	// registry filter drops a disabled language before the scan, so the
	// scan never consults config itself.
	Def           *RuleDef
	StripComments func(string) string
	// InterpolationContents returns string contents that can evaluate code
	// but the normal matcher skips as one quoted literal. Nil means the
	// language has no supported extractor.
	InterpolationContents func(string) []string
	Rules                 []SnippetRule
}

type SnippetRule struct {
	// Check receives the main source with comments stripped, or one
	// interpolated string's contents unchanged. String literals are skipped
	// by each matcher's SKIP/FAIL regex.
	Check  func(code string) bool
	Action *Action
}

type Command struct {
	// Args holds the command name and its arguments as AST Words, keeping
	// any ParamExp or CmdSubst intact.
	Args []*syntax.Word
	// CouldBeFuncCall is only a fallback override in permissions: deny and
	// ask patterns always take precedence.
	CouldBeFuncCall bool
	// SourcePath is the chain of files that led to this command being
	// extracted (e.g. "outer.sh > helpers.sh").
	SourcePath string
	// RootScript names the outermost script, for "./script.sh" guidance in
	// error messages.
	RootScript string
}

// BreakdownResult holds the extracted commands and the names of environment
// variables assigned to prefix a command (FOO=bar cmd -> Assigns has "FOO").
type BreakdownResult struct {
	Commands     []Command
	CodeSnippets []CodeSnippet
	Assigns      []string
	// safe distinguishes fully handled input with no executable command or
	// snippet from a result that found nothing and must fall through.
	safe bool
}

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

type StdinKind uint8

const (
	// StdinInherited means nothing was attached to stdin, so an interpreter
	// reading it waits for a terminal rather than running a script.
	StdinInherited StdinKind = iota
	// StdinUnreadable means something supplies stdin but its content cannot
	// be read from the command alone.
	StdinUnreadable
	StdinCode
	StdinFile
)

// Stdin is what the breakdown could learn about a command's standard input.
// Code and File carry the content for their matching Kind.
type Stdin struct {
	Kind StdinKind
	Code string
	File string
}

func (s Stdin) Supplied() bool {
	return s.Kind != StdinInherited
}

// State holds the mutable state during a breakdown pass. Hooks can read and
// mutate it directly.
type State struct {
	// Cwd is empty when unknown, either never set or cleared by an
	// uncertain cd. Relative paths cannot be resolved then, and are denied.
	Cwd string
	// CwdChanged makes control-flow boundaries clear Cwd, because a cd
	// inside them may not have run. The && handler is the exception: its
	// right side runs only when the left succeeded.
	CwdChanged bool
	// Visited holds absolute paths already scanned.
	Visited          map[string]bool
	Funcs            map[string]bool
	SawUnsetF        bool
	ConditionalDepth int
	FilePath         []string
	RootScript       string
	// RuleConfig gates imperative denials only. A breakdown function skips
	// a disabled deny-flag rule, and declines to unwrap when a disabled
	// .unverified rule would have denied, so the command falls through to
	// the permissions layer. The declarative layers are filtered before
	// evaluation and never consult this.
	RuleConfig RuleConfigs
	// Stdin is ambient like Cwd: a statement's own redirect overrides it, a
	// pipe hides it, and it reaches an inner command only when the outer
	// command forwards its stdin.
	Stdin Stdin
}

// CodeSnippet holds non-bash code extracted from a command. The orchestrator
// scans these for dangerous patterns and checks SourceScript against user
// permissions.
type CodeSnippet struct {
	Language string
	Code     string
	// SourceScript is the original command args, kept as Words so
	// permission checking needs no string conversion.
	SourceScript []*syntax.Word
	// SourceFile is the file the code was read from, or "" for inline code.
	// It chooses between deny (inline) and ask (file).
	SourceFile string
}

// BreakdownWork is the work extracted from one command. Its fields are
// additive: a wrapper can produce several kinds of work.
type BreakdownWork struct {
	// WorkingDirectory scopes the inner work to this directory. The
	// framework scans its source word under the outer state, resolves this
	// directory with the same rules as cd, then restores the outer state.
	WorkingDirectory *syntax.Word
	// Commands go straight through the AST walker with no print-and-reparse
	// round trip, so use them for inner commands already structured as
	// separate arg Words (timeout 5 [ls -la]). Preserve the original Word
	// pointers so the framework can tell which outer args were forwarded.
	Commands [][]*syntax.Word
	// CodeStrings are re-parsed through breakdownAt, for code extracted
	// from a single Word's resolved content (bash -c "code", eval args)
	// whose text representation would carry quotes the code should not
	// have. The owner must reject opaque source before adding it here.
	CodeStrings []string
	// Assigns are environment-variable assignments the wrapper applies to
	// its inner command (env NAME=val cmd), recorded on the EnvVars deny
	// axis. Exec-style wrappers leave this nil: they exec through execvp,
	// where a leading NAME=val is the program name, not an assignment.
	Assigns []*syntax.Assign
	// ScanFiles are scanned with isolation and rootScript handled by the
	// framework.
	ScanFiles    []string
	CodeSnippets []CodeSnippet
	// ForwardStdin lets the inner work inherit this command's stdin. Set it
	// where the command hands its own stdin to a command the user chose
	// (exec wrappers, eval, bash -c), and leave it off where the command
	// consumes stdin itself (xargs), replaces it (bash reading its script
	// from stdin), or runs the work later (trap).
	ForwardStdin bool
}

func (w BreakdownWork) empty() bool {
	return w.WorkingDirectory == nil &&
		len(w.Commands) == 0 &&
		len(w.CodeStrings) == 0 &&
		len(w.Assigns) == 0 &&
		len(w.ScanFiles) == 0 &&
		len(w.CodeSnippets) == 0
}

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

// Safe removes an outer command the hook has fully checked and found to
// contain no work. The framework still scans its original arguments for shell
// expansions.
func Safe() BreakdownOutcome {
	return BreakdownOutcome{disposition: OuterSafe}
}

// ReplaceOuter removes the outer command after processing the extracted work.
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
// through normal rules and permissions.
func KeepOuter(work BreakdownWork) BreakdownOutcome {
	if work.empty() {
		panic("KeepOuter requires non-empty BreakdownWork")
	}

	return BreakdownOutcome{
		disposition: OuterKeep,
		work:        work,
	}
}

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
