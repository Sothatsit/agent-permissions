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
)

// Action is a decision with a human-readable reason.
type Action struct {
	Decision Decision
	Reason   string
}

// SnippetLang holds the snippet rules for a single
// language.
type SnippetLang struct {
	// StripComments removes comments (not strings)
	// before running rules. Nil means no stripping.
	StripComments func(string) string
	Rules         []SnippetRule
}

// SnippetRule checks code snippets for dangerous
// patterns and produces an action when matched.
type SnippetRule struct {
	// Check reports whether the code contains the
	// dangerous pattern. Comments are already stripped
	// by the caller; string literals are skipped by
	// the SKIP/FAIL regex built into each matcher.
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
	// ScanFiles lists file paths to scan directly (e.g.
	// bash script.sh). The framework handles isolation
	// (new process state) and rootScript automatically.
	ScanFiles []string
	// CodeSnippets holds non-bash code extracted from
	// the command (e.g. Python source). The framework
	// transfers these to BreakdownResult for the
	// orchestrator to scan.
	CodeSnippets []CodeSnippet
	// KeepOuter preserves the outer command alongside
	// the extracted inner commands. When false (default),
	// the inner commands replace the outer command. When
	// true, the outer command continues through normal
	// flattening — cmd-sub extraction from args, rules
	// evaluation, and permissions checking all apply.
	KeepOuter bool
}
