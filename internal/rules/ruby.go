package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"
)

var syntaxRuby = &langSyntax{
	Quotes: []quoteDef{
		{Delim: `"`},
		{Delim: `'`},
		// Backticks are NOT quotes — they are
		// shell execution syntax.
	},
	LineComments: []string{"#"},
}

// rubyFlags defines recognised Ruby interpreter flags.
// Sorted longest-first for cluster splitting.
var rubyFlags = []model.FlagDef{
	{Name: "--disable"}, {Name: "--version"},
	{Name: "--enable"}, {Name: "--help"},
	// Flags that consume the next argument. Unlike
	// Python's -c, Ruby continues parsing its own flags
	// after -e (StopAtPositional handles non-flag args).
	{Name: "-e", Arg: true},
	{Name: "-r", Arg: true},
	{Name: "-I", Arg: true},
	{Name: "-F", Arg: true},
	{Name: "-0", Arg: true},
	{Name: "-C", Arg: true},
	{Name: "-W", Prefix: true},
	// No-arg flags.
	{Name: "-a"}, {Name: "-c"}, {Name: "-d"},
	{Name: "-h"}, {Name: "-l"}, {Name: "-n"},
	{Name: "-p"}, {Name: "-s"}, {Name: "-S"},
	{Name: "-v"}, {Name: "-w"}, {Name: "-x"},
	{Name: "-y"},
}

var rubyParser = func() *model.FullParser {
	p := model.NewFullParser(
		rubyFlags, "unrecognised Ruby flag")
	p.StopAtPositional = true
	return p
}()

var breakdownRuby = breakdownInterpreter(
	interpreterConfig{
		lang:       model.LangRuby,
		name:       "ruby",
		unverified: rubyUnverified,
		// -v is NOT an info flag in Ruby — it prints
		// the version but continues execution (sets
		// $VERBOSE), so ruby -v script.rb runs the
		// script and must be scanned.
		infoFlags: []string{
			"--version", "--help", "-h",
		},
		codeFlags: []string{"-e"},
	})

// --- Snippet matching ---

// rubyBareCall matches bare function calls (system,
// exec, spawn) while avoiding $-prefixed variables —
// $ is a sigil in Ruby, not a word boundary.
func rubyBareCall(names ...string) matchBuilder {
	return syntaxRuby.match(
		`(?:^|[^$\w])(?:` +
			reAlternation(names) + `)\b`)
}

// rubyRequire matches Ruby require statements:
// require 'mod', require('mod').
func rubyRequire(modules ...string) matchBuilder {
	return syntaxRuby.match(
		`\brequire\s*\(?\s*['"](?:` +
			reAlternation(modules) + `)['"]`)
}
