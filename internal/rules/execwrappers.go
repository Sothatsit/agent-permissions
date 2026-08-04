package rules

import "github.com/sothatsit/agent-permissions/internal/model"

// Simple exec-style wrappers: skip their own flags, then exec
// the inner command directly via execvp. They honour no env
// assignments — a leading NAME=val is the program name to them,
// not an assignment — so they need no Assigns handling and the
// generic wrapper breakdown suffices. An empty inner (e.g.
// `ionice -p PID`, `exec > log`) is safe (nothing to run).

// nohup COMMAND [ARG]... — runs COMMAND immune to hangups.
var nohupParser, breakdownNohup = wrapperBreakdown(
	wrapperDef{
		flags: []model.FlagDef{
			{Name: "--version"},
			{Name: "--help"},
		},
	})

// setsid [-wcf] COMMAND [ARG]... — runs COMMAND in a new
// session.
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

// nice [-n N] COMMAND [ARG]... — runs COMMAND with adjusted
// scheduling priority. Bare `nice` prints the niceness (no
// command). The deprecated `nice -NUM cmd` form is not parsed
// and falls to a fail-closed deny.
var niceParser, breakdownNice = wrapperBreakdown(
	wrapperDef{
		flags: []model.FlagDef{
			{Name: "--adjustment", Arg: true},
			{Name: "--version"},
			{Name: "--help"},
			{Name: "-n", Arg: true, Prefix: true},
		},
	})

// ionice [options] [COMMAND [ARG]...] — runs COMMAND (or, with
// -p/-P/-u, retunes an existing process and runs nothing).
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

// exec [-cl] [-a name] [COMMAND [ARG]...] — replaces the shell
// with COMMAND. A redirect-only `exec > log` has no command and
// is safe.
var execParser, breakdownExec = wrapperBreakdown(
	wrapperDef{
		flags: []model.FlagDef{
			{Name: "-a", Arg: true},
			{Name: "-c"},
			{Name: "-l"},
		},
	})
