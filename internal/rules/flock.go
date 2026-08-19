package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// flockParser parses flock's leading options. -c/--command is deliberately
// omitted because it follows the lock file in flock's grammar. Leading-only
// parsing leaves it as a positional for breakdownFlock to inspect. A -c before
// the lock file is invalid and fails closed as an unrecognised flag.
var flockParser = model.NewFullParser(
	[]model.FlagDef{
		{Name: "--conflict-exit-code", Arg: true},
		{Name: "--exclusive"},
		{Name: "--nonblock"},
		{Name: "--no-fork"},
		{Name: "--timeout", Arg: true},
		{Name: "--verbose"},
		{Name: "--version"},
		{Name: "--shared"},
		{Name: "--unlock"},
		{Name: "--close"},
		{Name: "--help"},
		{Name: "--wait", Arg: true},
		{Name: "--nb"},
		{Name: "-w", Arg: true, Prefix: true},
		{Name: "-E", Arg: true, Prefix: true},
		{Name: "-s"},
		{Name: "-x"},
		{Name: "-u"},
		{Name: "-n"},
		{Name: "-o"},
		{Name: "-F"},
		{Name: "-h"},
		{Name: "-V"},
	},
	model.LeadingFlagsOnly,
	"unrecognised flag",
)

// breakdownFlock unwraps flock. Its first positional is the lock file/dir/fd
// (skip it). What follows is either an inner command (flock FILE CMD args) or
// `-c STR`, where STR is run via a shell and must be re-parsed as code. A lock
// file with nothing after it (or an fd-only form) runs no command.
func breakdownFlock(
	input model.ParseResult,
	_ *model.State,
) (model.BreakdownOutcome, error) {
	pos := input.Positionals
	if len(pos) == 0 {
		// No lock file - flock errors at runtime; nothing runs.
		return model.Safe(), nil
	}

	rest := pos[1:] // pos[0] is the lock file/dir/fd
	if len(rest) == 0 {
		return model.Safe(), nil
	}
	if word.DefinitelyEqual(rest[0], "-c") ||
		word.DefinitelyEqual(rest[0], "--command") {
		if len(rest) < 2 {
			return model.Safe(), nil
		}

		code, err := verifyBashSource("flock -c", rest[1])
		if err != nil {
			return model.BreakdownOutcome{}, err
		}

		return model.ReplaceOuter(model.BreakdownWork{
			CodeStrings:  []string{code},
			ForwardStdin: true,
		}), nil
	}

	return model.ReplaceOuter(model.BreakdownWork{
		Commands:     [][]*syntax.Word{rest},
		ForwardStdin: true,
	}), nil
}
