package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// breakdownUnset checks for -f (unset functions) and
// records it in state. Returns nil so unset falls
// through to normal flattening and pattern matching.
func breakdownUnset(
	input model.ParseResult,
	state *model.State,
) (*model.UnwrapResult, error) {
	for _, w := range input.Raw {
		if word.DefinitelyHasPrefix(w, "-") &&
			word.DefinitelyContains(w, "f") {
			state.SawUnsetF = true
			break
		}
	}
	return nil, nil
}
