package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// breakdownFind extracts inner commands from -exec and
// -execdir clauses. Uses KeepOuter so the outer find
// command is also emitted — the rules layer handles
// dangerous flags (-ok, -okdir) and normal flattening
// catches cmd subs in other args.
func breakdownFind(
	input model.ParseResult,
	_ *model.State,
) (*model.UnwrapResult, error) {
	var commands [][]*syntax.Word

	for i := 0; i < len(input.Raw); i++ {
		// Strict: if the arg is opaque, we can't
		// know it's -exec — skip it. The outer find
		// is still emitted (KeepOuter) so opaque
		// args get caught by cmd-sub extraction.
		if !word.DefinitelyEqual(input.Raw[i], "-exec") &&
			!word.DefinitelyEqual(input.Raw[i], "-execdir") {
			continue
		}

		// Collect inner command words until ; or +
		// terminator.
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
		return nil, nil
	}

	return &model.UnwrapResult{
		Commands:  commands,
		KeepOuter: true,
	}, nil
}
