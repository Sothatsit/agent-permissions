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

// ParseResult holds parsed command arguments. When
// FullyParsed is true, Flags and Positionals are
// authoritative. Otherwise, PossibleFlags is populated
// from raw args heuristically.
type ParseResult struct {
	// Name is the resolved command name (e.g.
	// "python3", "bash"). Set by the breakdown
	// framework so breakdown functions can inspect
	// which command they're handling.
	Name        string
	Raw         []*syntax.Word
	FullyParsed bool

	// Populated when FullyParsed:
	Flags       []ParsedFlag
	Positionals []*syntax.Word

	// Populated when NOT FullyParsed:
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
	// Parser converts raw Word args into structured
	// ParseResult. If nil, PossibleFlags are populated
	// heuristically.
	Parser Parser
	// Breakdown handles the unwrapping phase: extracting
	// inner commands, scanning files, or mutating
	// breakdown state. Nil means no unwrapping.
	Breakdown BreakdownFunc
	// PathMode controls what happens when a command with
	// a Breakdown is invoked via a path (./cmd,
	// /usr/bin/cmd). Zero value is PathDeny.
	PathMode PathMode
	// Default is the fallback decision when no rule
	// matched. Nil means fall through to the permissions
	// pattern layer.
	Default *Action
	// Rules are evaluated during the permissions phase.
	Rules []Rule
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

// Parser converts raw Word args into a structured
// ParseResult.
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
