package rules

import "github.com/sothatsit/agent-permissions/internal/model"

// Privilege/personality wrappers we cannot model safely. Each
// runs an inner command but with forms that defeat simple
// flag/positional parsing: runuser has a -c shell-string and an
// interactive form, setpriv has a large privilege-flag surface,
// and setarch's leading architecture argument is ambiguous with
// its command. Rather than risk masking the inner command, deny
// — strictly tighter than today (where they are unmodelled and
// merely ask). The RuleError is suppressed when the governing
// rule is off, so the command then falls through to the
// permissions layer.

// denyWrapper returns a BreakdownFunc that denies outright,
// attributed to def so the user can disable it.
func denyWrapper(
	def *model.RuleDef, reason string,
) model.BreakdownFunc {
	return func(
		_ model.ParseResult, _ *model.State,
	) (model.BreakdownOutcome, error) {
		return model.BreakdownOutcome{}, &model.RuleError{
			Def: def, Reason: reason,
		}
	}
}

var breakdownRunuser = denyWrapper(runuserUnverified,
	"runuser runs commands as another user via shell-string or "+
		"interactive forms that can't be verified — invoke the "+
		"command directly")

var breakdownSetpriv = denyWrapper(setprivUnverified,
	"setpriv alters process privileges through many flags that "+
		"can't be verified — invoke the command directly")

var breakdownSetarch = denyWrapper(setarchUnverified,
	"setarch's leading architecture argument is ambiguous to "+
		"parse — invoke the command directly")
