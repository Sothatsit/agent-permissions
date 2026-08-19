package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// breakdownFind extracts inner commands from -exec and -execdir.
// KeepOuter leaves the outer find for the rules layer, which handles
// -ok and -okdir.
func breakdownFind(
	input model.ParseResult,
	_ *model.State,
) (model.BreakdownOutcome, error) {
	var commands [][]*syntax.Word

	for i := 0; i < len(input.Raw); i++ {
		// Strict: if the arg is opaque, we cannot know whether it is
		// -exec, so leave it with the outer command.
		if !word.DefinitelyEqual(input.Raw[i], "-exec") &&
			!word.DefinitelyEqual(input.Raw[i], "-execdir") {
			continue
		}

		// Collect inner command words until ; or + terminator.
		i++
		start := i
		for i < len(input.Raw) {
			if word.DefinitelyEqual(input.Raw[i], ";") ||
				word.DefinitelyEqual(input.Raw[i], "+") {
				break
			}

			i++
		}

		if start < i {
			commands = append(commands,
				input.Raw[start:i])
		}
	}

	if len(commands) == 0 {
		return model.FallThrough(), nil
	}

	return model.KeepOuter(model.BreakdownWork{
		Commands: commands,
	}), nil
}
