package rules

import (
	"path/filepath"
	"strings"

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
) (*model.UnwrapResult, error) {
	// Only resolve when unconditional (depth 0), the
	// command is cd (not pushd/popd), and the target
	// is a single static word.
	state.CwdChanged = true
	if state.ConditionalDepth == 0 &&
		input.Name == "cd" &&
		len(input.Raw) == 1 &&
		word.Static(input.Raw[0]) {
		target := word.Text(input.Raw[0])
		// cd - goes to $OLDPWD, cd ~ expands to
		// $HOME, neither of which we support yet.
		if target == "-" || strings.HasPrefix(target, "~") {
			state.Cwd = ""
			return nil, nil
		}
		if filepath.IsAbs(target) {
			state.Cwd = filepath.Clean(target)
			return nil, nil
		}
		// Relative target: resolve against known cwd.
		if state.Cwd != "" {
			state.Cwd = filepath.Clean(
				filepath.Join(state.Cwd, target))
			return nil, nil
		}
	}
	// Anything else: directory unknown.
	state.Cwd = ""
	return nil, nil
}
