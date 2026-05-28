package rules

import "github.com/sothatsit/agent-permissions/internal/model"

var stdbufParser, breakdownStdbuf = wrapperBreakdown(
	wrapperDef{
		flags: []model.FlagDef{
			{Name: "--output", Arg: true},
			{Name: "--error", Arg: true},
			{Name: "--input", Arg: true},
			{Name: "-i", Arg: true, Prefix: true},
			{Name: "-o", Arg: true, Prefix: true},
			{Name: "-e", Arg: true, Prefix: true},
		},
	})
