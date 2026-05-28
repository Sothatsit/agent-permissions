package rules

import "github.com/sothatsit/agent-permissions/internal/model"

var timeoutParser, breakdownTimeout = wrapperBreakdown(
	wrapperDef{
		flags: []model.FlagDef{
			{Name: "--preserve-status"},
			{Name: "--foreground"},
			{Name: "--kill-after", Arg: true},
			{Name: "--verbose"},
			{Name: "--signal", Arg: true},
			{Name: "-k", Arg: true, Prefix: true},
			{Name: "-s", Arg: true, Prefix: true},
			{Name: "-v"},
		},
		skipPositional: 1, // duration arg
	})
