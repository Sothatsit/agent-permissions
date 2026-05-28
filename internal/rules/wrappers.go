package rules

import (
	"fmt"

	"github.com/sothatsit/agent-permissions/internal/model"

	"mvdan.cc/sh/v3/syntax"
)

// wrapperDef defines a transparent wrapper command using
// FullParser for flag parsing. The parser extracts
// flags and positionals; the breakdown checks deny
// flags, skips leading positionals (e.g. timeout's
// duration), and returns the rest as the inner command.
type wrapperDef struct {
	flags          []model.FlagDef
	denyFlags      map[string]string
	skipPositional int // positional args to skip
}

// wrapperBreakdown builds a FullParser and BreakdownFunc
// from a wrapperDef. The parser is returned separately
// so it can be set on CommandRules.Parser for pre-parsing
// by the breakdown framework.
func wrapperBreakdown(
	def wrapperDef,
) (*model.FullParser, model.BreakdownFunc) {
	p := model.NewFullParser(
		def.flags, "unrecognised flag")
	p.StopAtPositional = true

	breakdown := func(
		input model.ParseResult,
		_ *model.State,
	) (*model.UnwrapResult, error) {
		// Deny flags checked post-parse — deny is
		// policy, not parsing.
		for _, f := range input.Flags {
			if reason, ok :=
				def.denyFlags[f.Name]; ok {
				return nil, fmt.Errorf(
					"%s", reason)
			}
		}

		// Skip leading positionals (e.g. timeout's
		// duration arg) and return the rest as the
		// inner command.
		rest := input.Positionals
		skip := def.skipPositional
		if skip > len(rest) {
			skip = len(rest)
		}
		rest = rest[skip:]

		if len(rest) == 0 {
			return &model.UnwrapResult{}, nil
		}
		return &model.UnwrapResult{
			Commands: [][]*syntax.Word{rest},
		}, nil
	}

	return p, breakdown
}
