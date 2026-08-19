package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

var xargsParser, xargsWrapperBreakdown = wrapperBreakdown(
	wrapperDef{
		flags: []model.FlagDef{
			{Name: "--process-slot-var", Arg: true},
			{Name: "--no-run-if-empty"},
			{Name: "--interactive"},
			{Name: "--show-limits"},
			{Name: "--max-procs", Arg: true},
			{Name: "--max-lines", Arg: true},
			{Name: "--max-chars", Arg: true},
			{Name: "--delimiter", Arg: true},
			{Name: "--max-args", Arg: true},
			{Name: "--arg-file", Arg: true},
			{Name: "--open-tty"},
			{Name: "--version"},
			{Name: "--verbose"},
			// --replace is -I's long form but takes an optional =value, so
			// declaring it without Arg stops bare --replace eating the next
			// arg. --replace=STR comes through the =value split.
			{Name: "--replace"},
			{Name: "--null"},
			{Name: "--help"},
			{Name: "--exit"},
			{Name: "-E", Arg: true},
			{Name: "-I", Arg: true, Prefix: true},
			{Name: "-L", Arg: true, Prefix: true},
			{Name: "-P", Arg: true, Prefix: true},
			{Name: "-a", Arg: true},
			{Name: "-d", Arg: true},
			{Name: "-n", Arg: true, Prefix: true},
			{Name: "-s", Arg: true, Prefix: true},
			{Name: "-p"},
			{Name: "-o"},
			{Name: "-r"},
			{Name: "-t"},
			{Name: "-x"},
			{Name: "-0"},
		},
		denyFlags: map[string]string{
			"-p": "-p/--interactive denied " +
				"— hangs in non-interactive context",
			"--interactive": "-p/--interactive denied " +
				"— hangs in non-interactive context",
			"-o": "-o/--open-tty denied " +
				"— opens /dev/tty",
			"--open-tty": "-o/--open-tty denied " +
				"— opens /dev/tty",
		},
		denyRule: xargsInteractive,
		// xargs reads its argument list from stdin and gives each child
		// /dev/null instead, so a heredoc on xargs never reaches the
		// inner command. (-o hands the child a tty, and is denied.)
		consumesStdin: true,
	})

// breakdownXargs wraps the generic wrapper breakdown with a check for -I
// replacement strings in the command name. xargs -I{} {} runs whatever stdin
// provides as a command - that's arbitrary execution.
func breakdownXargs(
	input model.ParseResult,
	state *model.State,
) (model.BreakdownOutcome, error) {
	outcome, err := xargsWrapperBreakdown(input, state)
	if err != nil {
		return model.BreakdownOutcome{}, err
	}

	work := outcome.Work()
	if len(work.Commands) == 0 {
		return outcome, nil
	}

	// The checks below are the xargs.unverified rule. Disabled, they are
	// skipped and the extracted inner command is checked normally.
	if !state.RuleConfig.For(xargsUnverified).Enabled {
		return outcome, nil
	}

	// Specified more than once, the replacement string is ambiguous.
	var replStr string
	replCount := 0
	for _, f := range input.Flags {
		if f.Name == "-I" || f.Name == "--replace" {
			replCount++
			if f.Value != nil {
				replStr = word.Text(f.Value)
			} else {
				replStr = "{}"
			}
		}
	}

	if replCount > 1 {
		return model.BreakdownOutcome{}, &model.RuleError{
			Def: xargsUnverified,
			Reason: "multiple -I/--replace flags " +
				"— ambiguous",
		}
	}
	if replStr == "" {
		// An explicit empty replacement string could mask intent. No
		// -I flag at all also lands here.
		if replCount > 0 {
			return model.BreakdownOutcome{}, &model.RuleError{
				Def: xargsUnverified,
				Reason: "-I/--replace with empty " +
					"replacement string",
			}
		}

		return outcome, nil
	}

	// A replacement string in the command name means stdin supplies the
	// command.
	cmd := work.Commands[0]
	if len(cmd) > 0 &&
		word.MayContain(cmd[0], replStr) {
		return model.BreakdownOutcome{}, &model.RuleError{
			Def: xargsUnverified,
			Reason: "-I replacement string in command " +
				"name — command to execute comes " +
				"from stdin",
		}
	}

	return outcome, nil
}
