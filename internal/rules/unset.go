package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// breakdownUnset checks for -f (unset functions) and
// records it in state. It falls through so unset reaches
// through to normal flattening and pattern matching.
func breakdownUnset(
	input model.ParseResult,
	state *model.State,
) (model.BreakdownOutcome, error) {
	for _, w := range input.Raw {
		if word.DefinitelyHasPrefix(w, "-") &&
			word.DefinitelyContains(w, "f") {
			state.SawUnsetF = true
			break
		}
	}
	return model.FallThrough(), nil
}
