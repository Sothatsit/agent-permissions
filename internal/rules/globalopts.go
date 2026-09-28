package rules

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// globalOptions are the options a CLI accepts before its subcommand. Breakdown
// strips them so permission patterns written for <cli> <subcommand> match
// however many of them lead the command. Left in place, one hides the
// subcommand from every pattern.
type globalOptions struct {
	// arguments maps each option to how many following words it takes.
	arguments map[string]int
	// takesAttachedValue reports whether the CLI also reads the option's
	// value from the same word, as --option=value.
	takesAttachedValue func(name string, arguments int) bool
	// unverified governs the denial for an option missing its argument.
	unverified *model.RuleDef
}

// patternPrefixSkips mirrors the options breakdown strips, so preset validation
// rejects a pattern that reaches an owned subcommand past them.
func (g globalOptions) patternPrefixSkips() []model.PatternPrefixSkip {
	var skips []model.PatternPrefixSkip
	for _, name := range slices.Sorted(maps.Keys(g.arguments)) {
		arguments := g.arguments[name]
		skips = append(skips, model.PatternPrefixSkip{
			Option:    name,
			Arguments: arguments,
		})
		if g.takesAttachedValue(name, arguments) {
			skips = append(skips, model.PatternPrefixSkip{
				Option: name + "=",
				Prefix: true,
			})
		}
	}

	return skips
}

// find names the global option a word holds and reports how many following
// words it takes.
func (g globalOptions) find(w *syntax.Word) (string, int, bool) {
	for name, arguments := range g.arguments {
		if word.DefinitelyEqual(w, name) {
			return name, arguments, true
		}
		if g.takesAttachedValue(name, arguments) &&
			word.DefinitelyHasPrefix(w, name+"=") {
			return name, 0, true
		}
	}

	return "", 0, false
}

// strip is the breakdown for a CLI with global options. Scanning stops at the
// first non-flag arg (the subcommand), so subcommand flags of the same name
// (e.g. git branch -C) are not affected.
func (g globalOptions) strip(
	input model.ParseResult,
	_ *model.State,
) (model.BreakdownOutcome, error) {
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
			return model.BreakdownOutcome{}, &model.RuleError{
				Def: g.unverified,
				Reason: fmt.Sprintf(
					"%s %s requires an argument",
					input.Name, name),
			}
		}

		found = true
		i += arguments
	}

	if !found {
		return model.FallThrough(), nil
	}

	result := make([]*syntax.Word, 0, len(stripped)+1)
	result = append(result, word.Lit(input.Name))
	result = append(result, stripped...)

	return model.ReplaceOuter(model.BreakdownWork{
		Commands: [][]*syntax.Word{result},
	}), nil
}

// pflagTakesAttachedValue matches the spf13/pflag parser that podman and docker
// use, which reads --option=value for every long option, booleans included.
func pflagTakesAttachedValue(name string, _ int) bool {
	return strings.HasPrefix(name, "--")
}
