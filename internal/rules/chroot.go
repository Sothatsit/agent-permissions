package rules

import "github.com/sothatsit/agent-permissions/internal/model"

var chrootParser, _chrootBaseBreakdown = wrapperBreakdown(
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

// breakdownChroot unwraps chroot: skip NEWROOT, extract the
// inner command. chroot with only a directory and no command
// runs an interactive $SHELL, which cannot be verified - deny.
// The RuleError is suppressed when chroot.unverified is off, so
// the command then falls through to the permissions layer.
func breakdownChroot(
	input model.ParseResult,
	state *model.State,
) (model.BreakdownOutcome, error) {
	outcome, err := _chrootBaseBreakdown(input, state)
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
