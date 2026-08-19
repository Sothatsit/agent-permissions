package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// breakdownCd updates Cwd when the target is a static absolute path
// at unconditional scope, so downstream commands can resolve relative
// paths, and marks the directory unknown otherwise.
//
// pushd/popd route here too but always mark it unknown: popd depends
// on stack state we cannot track, and treating pushd differently
// would leave the pair inconsistent.
func breakdownCd(
	input model.ParseResult,
	state *model.State,
) (model.BreakdownOutcome, error) {
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

	state.Cwd = ""
	return model.FallThrough(), nil
}
