package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"

	"mvdan.cc/sh/v3/syntax"
)

// wrapperDef defines a transparent wrapper: the parser extracts flags and
// positionals, and the breakdown returns the rest as the inner command.
type wrapperDef struct {
	flags     []model.FlagDef
	denyFlags map[string]string
	// denyRule gates denyFlags, and a wrapper with deny-flags must
	// set it, because the breakdown calls For(denyRule) on a match.
	// Disabled, the denial is skipped and the inner command is still
	// extracted and checked.
	denyRule       *model.RuleDef
	skipPositional int // positional args to skip
	// consumesStdin marks a wrapper that reads stdin for itself instead of
	// handing it to the command it runs.
	consumesStdin bool
}

// wrapperBreakdown returns the parser separately so it can be set on
// CommandRules.Parser for the framework to pre-parse with.
func wrapperBreakdown(
	def wrapperDef,
) (*model.FullParser, model.BreakdownFunc) {
	p := model.NewFullParser(
		def.flags,
		model.LeadingFlagsOnly,
		"unrecognised flag",
	)

	breakdown := func(
		input model.ParseResult,
		state *model.State,
	) (model.BreakdownOutcome, error) {
		// Deny is policy, not parsing, so deny flags are checked after.
		// A match implies a denyRule; honour its config so a disabled
		// rule still extracts and checks the inner command.
		for _, f := range input.Flags {
			reason, ok := def.denyFlags[f.Name]
			if !ok {
				continue
			}
			if state.RuleConfig.For(def.denyRule).Enabled {
				return model.BreakdownOutcome{}, &model.RuleError{
					Def:    def.denyRule,
					Reason: reason,
				}
			}
		}

		rest := input.Positionals
		skip := def.skipPositional
		if skip > len(rest) {
			skip = len(rest)
		}

		rest = rest[skip:]

		if len(rest) == 0 {
			return model.Safe(), nil
		}

		// These wrappers exec the inner command, so it runs on the
		// wrapper's own descriptors.
		return model.ReplaceOuter(model.BreakdownWork{
			Commands:     [][]*syntax.Word{rest},
			ForwardStdin: !def.consumesStdin,
		}), nil
	}

	return p, breakdown
}
