package model

import (
	"fmt"
	"maps"
	"slices"

	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// GlobalOptions are the options a CLI accepts before its subcommand. Breakdown
// strips them so permission patterns written for <cli> <subcommand> match
// however many of them lead the command. Left in place, one hides the
// subcommand from every pattern.
type GlobalOptions struct {
	// Arguments maps each option to how many following words it takes.
	Arguments map[string]int
	// TakesAttachedValue reports whether the CLI also reads the option's
	// value from the same word, as --option=value.
	TakesAttachedValue func(name string, arguments int) bool
}

// PatternPrefixSkips mirrors the options Strip removes, so preset validation
// rejects a pattern that reaches an owned subcommand past them. A command with
// no global options has none to skip.
func (g *GlobalOptions) PatternPrefixSkips() []PatternPrefixSkip {
	if g == nil {
		return nil
	}

	var skips []PatternPrefixSkip
	for _, name := range slices.Sorted(maps.Keys(g.Arguments)) {
		arguments := g.Arguments[name]
		skips = append(skips, PatternPrefixSkip{
			Option:    name,
			Arguments: arguments,
		})
		if g.TakesAttachedValue(name, arguments) {
			skips = append(skips, PatternPrefixSkip{
				Option: name + "=",
				Prefix: true,
			})
		}
	}

	return skips
}

// find names the global option a word holds and reports how many following
// words it takes.
func (g *GlobalOptions) find(w *syntax.Word) (string, int, bool) {
	for name, arguments := range g.Arguments {
		if word.DefinitelyEqual(w, name) {
			return name, arguments, true
		}
		if g.TakesAttachedValue(name, arguments) &&
			word.DefinitelyHasPrefix(w, name+"=") {
			return name, 0, true
		}
	}

	return "", 0, false
}

// Strip is the breakdown for a CLI with global options. It keeps the command's
// own name, so the stripped command still reaches that name's entries. Scanning
// stops at the first non-flag arg (the subcommand), so subcommand flags of the
// same name (e.g. git branch -C) are not affected. unverified governs the
// denial for an option missing its argument.
func (g *GlobalOptions) Strip(
	input ParseResult, unverified *RuleDef,
) (BreakdownOutcome, error) {
	args := input.Raw
	var stripped []*syntax.Word
	found := false

	for i := 0; i < len(args); i++ {
		w := args[i]

		// Non-flag word = subcommand. Stop scanning for global options
		// and copy the rest as-is.
		if !word.DefinitelyHasPrefix(w, "-") {
			stripped = append(stripped, args[i:]...)
			break
		}

		name, arguments, ok := g.find(w)
		if !ok {
			// An unknown option stays in place, where the rules
			// layer may still deny it.
			stripped = append(stripped, w)
			continue
		}
		remaining := len(args) - i - 1
		if arguments > remaining {
			// Without the argument there is no telling where the
			// subcommand starts, so nothing can be stripped.
			return BreakdownOutcome{}, &RuleError{
				Def: unverified,
				Reason: fmt.Sprintf(
					"%s %s requires an argument",
					input.Name, name),
			}
		}

		found = true
		i += arguments
	}

	if !found {
		return FallThrough(), nil
	}

	result := make([]*syntax.Word, 0, len(stripped)+1)
	result = append(result, word.Lit(input.Name))
	result = append(result, stripped...)

	return ReplaceOuter(BreakdownWork{
		Commands: [][]*syntax.Word{result},
	}), nil
}
