package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// breakdownCd tracks the working directory change.
// When the target is a static literal absolute path at
// unconditional scope, updates Cwd so downstream
// commands can resolve relative file paths. Otherwise
// marks the directory as unknown.
//
// Only cd gets the safe treatment. pushd/popd also
// route here but always mark unknown — popd depends
// on stack state we can't track, and treating pushd
// differently from popd would leave the pair
// inconsistent.
func breakdownCd(
	input model.ParseResult,
	state *model.State,
) (model.BreakdownOutcome, error) {
	// Only resolve when unconditional (depth 0), the
	// command is cd (not pushd/popd), and the target
	// is a single static word.
	state.CwdChanged = true
	if state.ConditionalDepth == 0 &&
		input.Name == "cd" &&
		len(input.Raw) == 1 &&
		word.Static(input.Raw[0]) {
		// cd - goes to $OLDPWD, which we do not track.
		if word.DefinitelyEqual(input.Raw[0], "-") {
			state.Cwd = ""
			return model.FallThrough(), nil
		}
		state.SetWorkingDirectory(input.Raw[0])
		return model.FallThrough(), nil
	}
	// Anything else: directory unknown.
	state.Cwd = ""
	return model.FallThrough(), nil
}
