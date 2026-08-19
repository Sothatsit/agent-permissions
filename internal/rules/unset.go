package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// breakdownUnset records -f in state, then falls through so unset still reaches
// pattern matching.
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
