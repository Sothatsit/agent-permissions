package rules

import (
	"fmt"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// breakdownCommand unwraps the command builtin by extracting the inner command.
// Handles -v/-V (lookups), -p (standard PATH), and -- separators. All flag
// checks are strict - if an arg is opaque, we can't verify its meaning and
// reject it.
func breakdownCommand(
	input model.ParseResult,
	_ *model.State,
) (model.BreakdownOutcome, error) {
	if len(input.Raw) == 0 {
		// Bare "command" - lists functions.
		return model.Safe(), nil
	}

	idx := 0
	w := input.Raw[idx]

	// -v/-V: command existence check - safe.
	if word.DefinitelyEqual(w, "-v") ||
		word.DefinitelyEqual(w, "-V") {
		return model.Safe(), nil
	}

	// -p: use standard PATH, advance past it.
	if word.DefinitelyEqual(w, "-p") {
		idx++
		if idx >= len(input.Raw) {
			return model.Safe(), nil
		}

		w = input.Raw[idx]
		if word.DefinitelyEqual(w, "--") {
			idx++
			if idx >= len(input.Raw) {
				return model.Safe(), nil
			}
		} else if word.DefinitelyEqual(w, "-v") ||
			word.DefinitelyEqual(w, "-V") {
			// command -p -v name - safe lookup.
			return model.Safe(), nil
		} else if word.DefinitelyHasPrefix(w, "-") {
			return model.BreakdownOutcome{}, &model.RuleError{
				Def: commandUnverified,
				Reason: fmt.Sprintf(
					"command: unrecognised flag "+
						"%s — use 'command -v "+
						"<name>' to check "+
						"availability or invoke "+
						"the command directly",
					word.Text(w)),
			}
		}
	} else if word.DefinitelyEqual(w, "--") {
		idx++
		if idx >= len(input.Raw) {
			return model.Safe(), nil
		}
	} else if word.DefinitelyHasPrefix(w, "-") {
		return model.BreakdownOutcome{}, &model.RuleError{
			Def: commandUnverified,
			Reason: fmt.Sprintf(
				"command: unrecognised flag "+
					"%s — use 'command -v "+
					"<name>' to check "+
					"availability or invoke "+
					"the command directly",
				word.Text(w)),
		}
	}

	// Remaining args are the inner command.
	return model.ReplaceOuter(model.BreakdownWork{
		Commands: [][]*syntax.Word{input.Raw[idx:]},
	}), nil
}
