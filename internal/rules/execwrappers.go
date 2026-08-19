package rules

import "github.com/sothatsit/agent-permissions/internal/model"

// Simple exec-style wrappers: skip their own flags, then exec the inner command
// through execvp. A leading NAME=val is the program name to them, not an
// assignment, so they need no Assigns handling. An empty inner command (ionice
// -p PID, exec > log) is safe.

var nohupParser, breakdownNohup = wrapperBreakdown(
	wrapperDef{
		flags: []model.FlagDef{
			{Name: "--version"},
			{Name: "--help"},
		},
	})

var setsidParser, breakdownSetsid = wrapperBreakdown(
	wrapperDef{
		flags: []model.FlagDef{
			{Name: "--version"},
			{Name: "--ctty"},
			{Name: "--wait"},
			{Name: "--fork"},
			{Name: "--help"},
			{Name: "-w"},
			{Name: "-c"},
			{Name: "-f"},
		},
	})

// Bare `nice` prints the niceness and runs nothing. The deprecated `nice -NUM
// cmd` form is not parsed and falls to a fail-closed deny.
var niceParser, breakdownNice = wrapperBreakdown(
	wrapperDef{
		flags: []model.FlagDef{
			{Name: "--adjustment", Arg: true},
			{Name: "--version"},
			{Name: "--help"},
			{Name: "-n", Arg: true, Prefix: true},
		},
	})

// With -p/-P/-u, ionice retunes an existing process and runs nothing.
var ioniceParser, breakdownIonice = wrapperBreakdown(
	wrapperDef{
		flags: []model.FlagDef{
			{Name: "--classdata", Arg: true},
			{Name: "--version"},
			{Name: "--ignore"},
			{Name: "--class", Arg: true},
			{Name: "--help"},
			{Name: "--pgid", Arg: true},
			{Name: "--pid", Arg: true},
			{Name: "--uid", Arg: true},
			{Name: "-c", Arg: true, Prefix: true},
			{Name: "-n", Arg: true, Prefix: true},
			{Name: "-p", Arg: true},
			{Name: "-P", Arg: true},
			{Name: "-u", Arg: true},
			{Name: "-t"},
			{Name: "-h"},
		},
	})

// A redirect-only `exec > log` has no command and is safe.
var execParser, breakdownExec = wrapperBreakdown(
	wrapperDef{
		flags: []model.FlagDef{
			{Name: "-a", Arg: true},
			{Name: "-c"},
			{Name: "-l"},
		},
	})
