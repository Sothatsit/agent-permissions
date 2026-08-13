package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"

	"mvdan.cc/sh/v3/syntax"
)

// rawParser preserves argument order and shell syntax for wrappers whose
// command-specific breakdown must consider more than one option grammar.
type rawParser struct{}

func (rawParser) Parse(
	args []*syntax.Word,
) (model.ParseResult, error) {
	return model.ParseResult{Raw: args}, nil
}
