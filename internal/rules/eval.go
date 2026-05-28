package rules

import (
	"fmt"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// breakdownEval unwraps eval by joining its static args
// into a single code string for re-parsing. Rejects
// opaque args since eval could execute anything.
func breakdownEval(
	input model.ParseResult,
	_ *model.State,
) (*model.UnwrapResult, error) {
	if len(input.Raw) == 0 {
		return &model.UnwrapResult{}, nil
	}
	// Every arg must be static — eval with a variable
	// could execute anything.
	for _, w := range input.Raw {
		if !word.Static(w) {
			return nil, fmt.Errorf(
				"eval contains %s — cannot verify "+
					"what will execute. Use the "+
					"command directly instead "+
					"of eval",
				word.OpaqueReason(w))
		}
	}
	// All args are static — join and re-parse as a
	// single code string.
	var b strings.Builder
	for i, w := range input.Raw {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(word.Text(w))
	}
	code := b.String()
	return &model.UnwrapResult{
		CodeStrings: []string{code},
	}, nil
}
