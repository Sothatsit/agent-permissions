package model

import (
	"strings"

	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

type ParsedFlag struct {
	// Name is always resolved, because flag names are static.
	Name string
	// Value is nil for bool flags, the original next-arg Word for -c value,
	// and a synthetic Word holding just the value for --flag=value.
	Value *syntax.Word
}

// ParseResult carries command arguments through breakdown and rule matching.
type ParseResult struct {
	// Name lets a breakdown function inspect which command it is handling.
	Name string
	Raw  []*syntax.Word

	// Populated by a Parser for Breakdown:
	Flags       []ParsedFlag
	Positionals []*syntax.Word

	// Populated by permission evaluation:
	PossibleFlags []ParsedFlag
}

// HookFunc returns Undecided when it has no opinion.
type HookFunc func(input ParseResult) (Decision, string)

// BreakdownFunc chooses how the framework handles a command, and may mutate
// breakdown state as cd does. A successful function must return an outcome from
// a BreakdownOutcome constructor. An error becomes a denial.
type BreakdownFunc func(
	input ParseResult,
	state *State,
) (BreakdownOutcome, error)

type Rule struct {
	// Def governs this node. The registry filter prunes a disabled rule's
	// whole subtree before evaluation, so the command falls through as if
	// the rule did not exist. Nil marks a node governed by an ancestor's
	// Def, or a structural node such as a permissive container, which is
	// not independently disableable. ValidateRegistry asserts every
	// restrictive node has a Def on its path.
	Def      *RuleDef
	Match    Matcher
	Action   *Action
	Hook     HookFunc
	Default  *Action
	Children []Rule
}

// PathMode controls how a path-invoked command (./cmd, /usr/bin/cmd) interacts
// with its Breakdown function.
type PathMode int

const (
	// PathDeny denies path-invoked commands that have a Breakdown, because
	// a local binary could ignore its arguments and make argument-based
	// extraction meaningless. Default for wrappers.
	PathDeny PathMode = iota
	// PathSkip falls through to normal flattening, for breakdowns that
	// strip flags rather than extract inner commands. The stripping is a
	// convenience, not a security gate.
	PathSkip
	// PathAllow runs the breakdown but keeps the outer command so it
	// reaches permissions, leaving the user to decide whether the
	// interpreter path is trusted. Snippets are still extracted and
	// scanned.
	PathAllow
)

// CommandRules defines how a command is evaluated across both the breakdown
// (unwrapping) and permissions (allow/ask/deny) phases.
type CommandRules struct {
	// OwnsAllPatterns marks commands whose breakdown or enabled Rules
	// consume every bare-name invocation before command patterns can apply.
	// OwnedPatternPrefixes lists argument prefixes with that property when
	// other invocations still fall through, and PatternPrefixSkips mirrors
	// leading options Breakdown removes before an owned subcommand reaches
	// rule matching. Preset validation rejects overlaps rather than accept
	// policy that runtime evaluation would ignore. A path-invoked PathAllow
	// command remains addressable.
	OwnsAllPatterns      bool
	OwnedPatternPrefixes [][]string
	PatternPrefixSkips   []PatternPrefixSkip
	// Parser requires a Breakdown, because the permissions phase always
	// matches possible flags conservatively instead.
	Parser Parser
	// Breakdown extracts inner commands, scans files, or mutates breakdown
	// state. Nil means no unwrapping.
	Breakdown BreakdownFunc
	// BreakdownDef governs the whole Breakdown, which the registry filter
	// removes when the rule is disabled. Leave nil when useful work in the
	// breakdown belongs to more than one rule.
	BreakdownDef *RuleDef
	PathMode     PathMode
	// Default is the decision when no rule matched. Nil falls through to
	// the permissions pattern layer.
	Default *Action
	// Unverified gates this command's fail-closed denials: the Default
	// above and the "cannot verify" errors from parsing or running
	// Breakdown. Nil leaves those denials always on.
	Unverified *RuleDef
	Rules      []Rule
}

// PatternPrefixSkip is a repeatable leading option removed by Breakdown, and
// how many following words it consumes.
type PatternPrefixSkip struct {
	Option    string
	Arguments int
}

// Matcher tests whether a rule applies. On match it returns scoped input for
// children and a context string for error messages.
type Matcher interface {
	Match(input ParseResult) (
		matched bool,
		childInput ParseResult,
		context string,
	)
}

type Parser interface {
	Parse(args []*syntax.Word) (ParseResult, error)
}

// PopulatePossibleFlags extracts flag-like tokens from raw args. Stops at "--".
func PopulatePossibleFlags(input *ParseResult) {
	for i, arg := range input.Raw {
		if word.DefinitelyEqual(arg, "--") {
			break
		}

		// SplitEq reads the name from the static prefix before =, so
		// this also covers mixed words like --flag=$VAR.
		name, valueWord := word.SplitEq(arg)
		if valueWord != nil &&
			strings.HasPrefix(name, "--") {
			input.PossibleFlags = append(
				input.PossibleFlags,
				ParsedFlag{
					Name:  name,
					Value: valueWord,
				})
			continue
		}
		// Without an =, the name has to come from static text.
		if !word.Static(arg) {
			continue
		}

		text := word.Text(arg)
		if len(text) > 1 && text[0] == '-' {
			var nextArg *syntax.Word
			if i+1 < len(input.Raw) {
				nextArg = input.Raw[i+1]
			}

			input.PossibleFlags = append(
				input.PossibleFlags,
				ParsedFlag{
					Name:  text,
					Value: nextArg,
				})
		}
	}
}
