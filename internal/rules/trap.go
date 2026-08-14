package rules

import (
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

func trapDisplaysState(arg *syntax.Word) bool {
	if !word.Static(arg) {
		return false
	}

	text := word.Text(arg)
	if len(text) < 2 || text[0] != '-' {
		return false
	}

	return strings.Trim(text[1:], "lp") == ""
}

// breakdownTrap unwraps trap by extracting the code
// string (first positional) for re-parsing. Bare trap,
// -l, -p, and signal resets are safe. Rejects opaque
// code args.
func breakdownTrap(
	input model.ParseResult,
	_ *model.State,
) (model.BreakdownOutcome, error) {
	if len(input.Raw) == 0 {
		return model.Safe(), nil
	}

	// Strict: opaque first arg falls through to the
	// static check below, which rejects it.
	if trapDisplaysState(input.Raw[0]) {
		return model.Safe(), nil
	}

	codeIdx := 0
	if word.DefinitelyEqual(input.Raw[0], "--") {
		codeIdx = 1
		if codeIdx >= len(input.Raw) {
			return model.Safe(), nil
		}
	}

	codeWord := input.Raw[codeIdx]
	if codeIdx+1 >= len(input.Raw) {
		// Without a signal, Bash reports usage and does not
		// register or execute the action.
		return model.Safe(), nil
	}

	// Empty string or "-" means ignore/reset signal.
	if word.DefinitelyEqual(codeWord, "") ||
		word.DefinitelyEqual(codeWord, "-") {
		return model.Safe(), nil
	}

	if !word.Static(codeWord) {
		return model.BreakdownOutcome{}, &model.RuleError{
			Def: trapUnverified,
			Reason: "trap: cannot verify command — use " +
				"a literal string instead of a " +
				"variable",
		}
	}

	return model.ReplaceOuter(model.BreakdownWork{
		CodeStrings: []string{word.Text(codeWord)},
	}), nil
}
