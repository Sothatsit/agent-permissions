package rules

import (
	"regexp"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
)

var syntaxPerl = &langSyntax{
	Quotes: []quoteDef{
		{Delim: `"`},
		{Delim: `'`},
		// Backticks are NOT quotes — they are
		// shell execution syntax that we want to
		// detect.
	},
	LineComments: []string{"#"},
}

// perlFlags defines recognised Perl interpreter flags.
var perlFlags = []model.FlagDef{
	{Name: "--version"}, {Name: "--help"},
	// Unlike Python's -c, Perl continues parsing its own flags after -e.
	// Leading-only parsing handles script arguments after a file name.
	{Name: "-e", Arg: true},
	{Name: "-E", Arg: true},
	{Name: "-F", Arg: true},
	{Name: "-I", Arg: true},
	{Name: "-M", Arg: true},
	{Name: "-m", Arg: true},
	{Name: "-0", Arg: true},
	{Name: "-C", Arg: true},
	{Name: "-D", Arg: true},
	// No-arg flags.
	{Name: "-a"}, {Name: "-c"}, {Name: "-d"},
	{Name: "-h"}, {Name: "-l"}, {Name: "-n"},
	{Name: "-p"}, {Name: "-s"}, {Name: "-S"},
	{Name: "-t"}, {Name: "-T"}, {Name: "-u"},
	{Name: "-v"}, {Name: "-V"}, {Name: "-w"},
	{Name: "-W"}, {Name: "-x"}, {Name: "-X"},
}

var perlParser = model.NewFullParser(
	perlFlags,
	model.LeadingFlagsOnly,
	"unrecognised Perl flag",
)

var breakdownPerl = breakdownInterpreter(
	interpreterConfig{
		lang:       model.LangPerl,
		name:       "perl",
		unverified: perlUnverified,
		infoFlags: []string{
			"--version", "--help", "-v", "-h",
		},
		codeFlags: []string{"-e", "-E"},
	})

func perlInterpolationContents(code string) []string {
	return interpolatedLiteralContents(
		code, syntaxPerl,
		func(_ string, _ int, quote quoteDef, content string) bool {
			return quote.Delim == `"` &&
				(hasUnescapedPrefix(content, "$") ||
					hasUnescapedPrefix(content, "@"))
		})
}

// --- Snippet matching ---

// perlBareCall matches bare function calls (system,
// exec) while avoiding $-prefixed variables — $ is a
// sigil in Perl, not a word boundary.
func perlBareCall(names ...string) matchBuilder {
	return syntaxPerl.match(
		`(?:^|[^$\w])(?:` +
			reAlternation(names) + `)\b`)
}

// perlUse matches a use/require statement for module.
// Modules ending in "::" are prefix matches (IPC::
// matches IPC::Open2); others require a word boundary.
func perlUse(module string) matchBuilder {
	escaped := regexp.QuoteMeta(module)
	if !strings.HasSuffix(module, "::") {
		escaped += `\b`
	}
	return syntaxPerl.match(
		`\b(?:use|require)\s+` + escaped)
}
