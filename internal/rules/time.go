package rules

import "github.com/sothatsit/agent-permissions/internal/model"

// Bash's `time` keyword is already transparent in the AST, so this handles the
// external /usr/bin/time.
var timeParser, breakdownTime = wrapperBreakdown(
	wrapperDef{
		flags: []model.FlagDef{
			{Name: "--portability"},
			{Name: "--verbose"},
			{Name: "--version"},
			{Name: "--format", Arg: true},
			{Name: "--append"},
			{Name: "--output", Arg: true},
			{Name: "--quiet"},
			{Name: "--help"},
			{Name: "-f", Arg: true},
			{Name: "-o", Arg: true},
			{Name: "-a"},
			{Name: "-p"},
			{Name: "-v"},
			{Name: "-V"},
		},
	})
