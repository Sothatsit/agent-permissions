package model

import (
	"strings"

	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// ParsedFlag represents a parsed flag from a command.
// Name is the flag name as a string (always static).
// Value is the flag's value as a Word, preserving the
// original AST structure for opacity checking.
type ParsedFlag struct {
	// Name is the flag name (e.g. "-c", "--signal").
	// Always a resolved string — flag names are static.
	Name string
	// Value is the flag's value as a Word. Nil for bool
	// flags. For --flag=value, this is a synthetic Word
	// containing the value portion. For -c value, this
	// is the original next-arg Word.
	Value *syntax.Word
}

// ParseResult carries command arguments through breakdown and rule matching.
// Parsers populate Flags and Positionals for Breakdown. Permission evaluation
// populates PossibleFlags conservatively from Raw.
type ParseResult struct {
	// Name is the resolved command name (e.g.
	// "python3", "bash"). Set by the breakdown
	// framework so breakdown functions can inspect
	// which command they're handling.
	Name string
	Raw  []*syntax.Word

	// Populated by a Parser for Breakdown:
	Flags       []ParsedFlag
	Positionals []*syntax.Word

	// Populated by permission evaluation:
	PossibleFlags []ParsedFlag
}

// HookFunc inspects parsed arguments and returns a
// decision. Returns Undecided when the hook has no opinion.
type HookFunc func(input ParseResult) (Decision, string)

// BreakdownFunc examines a command's parsed arguments and
// optionally unwraps it into inner commands for further
// breakdown. Also used for pure state mutations (e.g.
// cd updates Cwd). Returns nil when the command should not
// be unwrapped (fall through to normal flattening).
// Returns an error for "cannot verify" (becomes a deny).
type BreakdownFunc func(
	input ParseResult,
	state *State,
) (*UnwrapResult, error)

// Rule matches a command's arguments and produces a
// decision via a static action, a hook, children, or a
// combination.
type Rule struct {
	// Def is the rule governing this node. When the rule is
	// disabled, the registry filter prunes this node and its
	// whole subtree before evaluation, so the command falls
	// through as if the rule did not exist. A nil Def marks a
	// node governed by an ancestor's Def (or a structural
	// node, e.g. a permissive container) — it is not
	// independently disableable. ValidateRegistry asserts
	// every restrictive node has a Def on its path.
	Def      *RuleDef
	Match    Matcher
	Action   *Action
	Hook     HookFunc
	Default  *Action
	Children []Rule
}

// CommandRules defines how a command is evaluated across
// both the breakdown (unwrapping) and permissions
// (allow/ask/deny) phases.
// PathMode controls how path-invoked commands (./cmd,
// /usr/bin/cmd) interact with the Breakdown function.
type PathMode int

const (
	// PathDeny denies path-invoked commands that have
	// a Breakdown. A local binary could ignore its
	// arguments, so breakdown's argument-based
	// extraction can't be trusted. Default for
	// wrappers (timeout, xargs, bash, etc.).
	PathDeny PathMode = iota
	// PathSkip skips the breakdown for path-invoked
	// commands, falling through to normal flattening.
	// The original command reaches rules and
	// permissions unchanged. Use for breakdowns that
	// strip flags rather than extract inner commands
	// — the stripping is a convenience, not a
	// security gate.
	PathSkip
	// PathAllow runs the breakdown for path-invoked
	// commands but keeps the outer command so it
	// reaches permissions. Snippets are still
	// extracted and scanned; the user controls
	// whether the interpreter path is trusted.
	PathAllow
)

type CommandRules struct {
	// OwnsAllPatterns marks commands whose intended breakdown
	// or enabled Rules policy consumes every bare-name
	// invocation before command patterns can apply. A
	// path-invoked PathAllow command remains addressable.
	// OwnedPatternPrefixes lists argument prefixes with that
	// property when other invocations still fall through.
	// Preset validation rejects overlaps rather than accept
	// policy that runtime evaluation would ignore.
	// PatternPrefixSkips lets preset validation mirror leading
	// options that Breakdown removes before an owned
	// subcommand reaches rule matching.
	OwnsAllPatterns      bool
	OwnedPatternPrefixes [][]string
	PatternPrefixSkips   []PatternPrefixSkip
	// Parser structures raw arguments for Breakdown. The permissions phase
	// always matches possible flags conservatively, so a Parser requires a
	// Breakdown.
	Parser Parser
	// Breakdown handles the unwrapping phase: extracting
	// inner commands, scanning files, or mutating
	// breakdown state. Nil means no unwrapping.
	Breakdown BreakdownFunc
	// BreakdownDef governs the whole Breakdown. The registry filter removes
	// that function when the rule is disabled. Leave nil when useful work in the
	// breakdown belongs to more than one rule.
	BreakdownDef *RuleDef
	// PathMode controls what happens when a command with
	// a Breakdown is invoked via a path (./cmd,
	// /usr/bin/cmd). Zero value is PathDeny.
	PathMode PathMode
	// Default is the fallback decision when no rule
	// matched. Nil means fall through to the permissions
	// pattern layer.
	Default *Action
	// Unverified gates this command's fail-closed denials: the Default above
	// (nil'd by the registry filter when disabled) and the "cannot verify"
	// errors produced while parsing or running Breakdown. A disabled rule
	// suppresses those errors in breakdown. A nil Unverified leaves the
	// denials always on.
	Unverified *RuleDef
	// Rules are evaluated during the permissions phase.
	Rules []Rule
}

// PatternPrefixSkip describes a repeatable leading option
// removed by Breakdown and how many following words it
// consumes.
type PatternPrefixSkip struct {
	Option    string
	Arguments int
}

// Matcher tests whether a rule applies to the given input.
// On match it returns scoped input for children and a
// context string used in error messages.
type Matcher interface {
	Match(input ParseResult) (
		matched bool,
		childInput ParseResult,
		context string,
	)
}

// Parser converts raw Word arguments into a structured ParseResult for a
// BreakdownFunc.
type Parser interface {
	Parse(args []*syntax.Word) (ParseResult, error)
}

// PopulatePossibleFlags extracts flag-like tokens from
// raw Word args into PossibleFlags. Stops at "--".
func PopulatePossibleFlags(input *ParseResult) {
	for i, w := range input.Raw {
		if word.DefinitelyEqual(w, "--") {
			break
		}
		// Try --flag=value split. Works for both
		// static and mixed words (e.g. --flag=$VAR)
		// — SplitEq extracts the name from the
		// static prefix before =.
		name, vw := word.SplitEq(w)
		if vw != nil &&
			strings.HasPrefix(name, "--") {
			input.PossibleFlags = append(
				input.PossibleFlags,
				ParsedFlag{
					Name:  name,
					Value: vw,
				})
			continue
		}
		// Non-equals flags need static text for
		// the Name field. Skip opaque words —
		// can't determine their flag name.
		if !word.Static(w) {
			continue
		}
		text := word.Text(w)
		if len(text) > 1 && text[0] == '-' {
			var nextW *syntax.Word
			if i+1 < len(input.Raw) {
				nextW = input.Raw[i+1]
			}
			input.PossibleFlags = append(
				input.PossibleFlags,
				ParsedFlag{
					Name:  text,
					Value: nextW,
				})
		}
	}
}
