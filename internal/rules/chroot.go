package rules

import "github.com/sothatsit/agent-permissions/internal/model"

var chrootParser, chrootWrapperBreakdown = wrapperBreakdown(
	wrapperDef{
		flags: []model.FlagDef{
			{Name: "--skip-chdir"},
			{Name: "--userspec", Arg: true},
			{Name: "--version"},
			{Name: "--groups", Arg: true},
			{Name: "--help"},
		},
		skipPositional: 1, // NEWROOT
	})

// breakdownChroot skips NEWROOT and extracts the inner command. With no command
// it runs an interactive $SHELL, which cannot be verified, so deny.
func breakdownChroot(
	input model.ParseResult,
	state *model.State,
) (model.BreakdownOutcome, error) {
	outcome, err := chrootWrapperBreakdown(input, state)
	if err != nil {
		return model.BreakdownOutcome{}, err
	}
	if len(outcome.Work().Commands) == 0 {
		return model.BreakdownOutcome{}, &model.RuleError{
			Def: chrootUnverified,
			Reason: "chroot with no command runs an " +
				"interactive shell — give an explicit command",
		}
	}

	return outcome, nil
}
