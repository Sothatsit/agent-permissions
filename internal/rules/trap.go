package rules

import (
	"fmt"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// breakdownTrap unwraps trap by extracting the code
// string (first positional) for re-parsing. Bare trap,
// -l, -p, and signal resets are safe. Rejects opaque
// code args.
func breakdownTrap(
	input model.ParseResult,
	_ *model.State,
) (*model.UnwrapResult, error) {
	if len(input.Raw) == 0 {
		return &model.UnwrapResult{}, nil
	}

	// Strict: opaque first arg falls through to the
	// static check below, which rejects it.
	if word.DefinitelyEqual(input.Raw[0], "-l") ||
		word.DefinitelyEqual(input.Raw[0], "-p") {
		return &model.UnwrapResult{}, nil
	}

	codeIdx := 0
	if word.DefinitelyEqual(input.Raw[0], "--") {
		codeIdx = 1
		if codeIdx >= len(input.Raw) {
			return &model.UnwrapResult{}, nil
		}
	}

	codeWord := input.Raw[codeIdx]

	// Empty string or "-" means ignore/reset signal.
	if word.DefinitelyEqual(codeWord, "") ||
		word.DefinitelyEqual(codeWord, "-") {
		return &model.UnwrapResult{}, nil
	}

	if !word.Static(codeWord) {
		return nil, fmt.Errorf(
			"trap: cannot verify command — use " +
				"a literal string instead of a " +
				"variable")
	}

	return &model.UnwrapResult{
		CodeStrings: []string{word.Text(codeWord)},
	}, nil
}
