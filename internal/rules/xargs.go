package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

var xargsParser, _xargsBaseBreakdown = wrapperBreakdown(
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
			// --replace is -I's long form but takes
			// an optional =value (bare --replace
			// defaults to {}). Declaring it without
			// Arg means bare --replace won't eat the
			// next arg; --replace=STR is handled by
			// the =value split.
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
	})

// breakdownXargs wraps the generic wrapper breakdown
// with a check for -I replacement strings in the command
// name. xargs -I{} {} runs whatever stdin provides as
// a command - that's arbitrary execution.
func breakdownXargs(
	input model.ParseResult,
	state *model.State,
) (model.BreakdownOutcome, error) {
	outcome, err := _xargsBaseBreakdown(input, state)
	if err != nil {
		return model.BreakdownOutcome{}, err
	}
	work := outcome.Work()
	if len(work.Commands) == 0 {
		return outcome, nil
	}

	// The remaining checks (ambiguous/empty -I, command
	// from stdin) are the xargs.unverified rule. When it's
	// disabled, skip them and return the already-extracted
	// inner command for normal checking.
	if !state.RuleConfig.For(xargsUnverified).Enabled {
		return outcome, nil
	}

	// Find the -I/--replace replacement string.
	// Deny if specified more than once - ambiguous.
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
		// Explicit empty replacement string (e.g. -I "")
		// is nonsensical and could mask intent. No -I flag
		// at all also lands here (replCount == 0).
		if replCount > 0 {
			return model.BreakdownOutcome{}, &model.RuleError{
				Def: xargsUnverified,
				Reason: "-I/--replace with empty " +
					"replacement string",
			}
		}
		return outcome, nil
	}

	// If the replacement string appears in the
	// command name, the command to execute comes
	// from stdin.
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
