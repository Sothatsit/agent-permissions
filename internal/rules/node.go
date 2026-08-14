package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"
)

var syntaxNode = &langSyntax{
	Quotes: []quoteDef{
		{Delim: `"`},
		{Delim: `'`},
		// Backticks ARE quotes in JS (template literals), not shell
		// execution.
		{Delim: "`", Multiline: true},
	},
	LineComments:  []string{"//"},
	BlockComments: []blockComment{{"/*", "*/"}},
}

// nodeFlags defines recognised Node.js interpreter flags.
var nodeFlags = []model.FlagDef{
	// Inspector addresses are optional and attach with `=`. A following word
	// is the script rather than the option value.
	{Name: "--inspect-brk"},
	{Name: "--version"}, {Name: "--require", Arg: true},
	{Name: "--inspect"},
	{Name: "--check"},
	{Name: "--print", Arg: true},
	{Name: "--help"},
	{Name: "--eval", Arg: true},
	// Unlike Python's -c, Node continues parsing its own flags after -e.
	// Leading-only parsing handles script arguments after a file name.
	{Name: "-e", Arg: true},
	{Name: "-p", Arg: true},
	{Name: "-r", Arg: true},
	{Name: "-C", Arg: true},
	// No-arg flags.
	{Name: "-c"},
	{Name: "-h"},
	{Name: "-i"},
	{Name: "-v"},
}

var nodeParser = model.NewFullParser(
	nodeFlags,
	model.LeadingFlagsOnly,
	"unrecognised Node flag",
)

var breakdownNode = breakdownInterpreter(
	interpreterConfig{
		lang:       model.LangNode,
		name:       "node",
		unverified: nodeUnverified,
		infoFlags: []string{
			"--version", "--help", "-v", "-h",
		},
		codeFlags: []string{
			"-e", "--eval", "-p", "--print",
		},
		unverifiedFlags:       []string{"-i", "-r", "--require"},
		unverifiedPositionals: []string{"inspect"},
		// -c/--check performs a syntax check without running the source, so it
		// falls through to permissions.
		fallthroughFlags: []string{
			"-c", "--check",
		},
	})

func nodeInterpolationContents(code string) []string {
	return interpolatedLiteralContents(
		code, syntaxNode,
		func(_ string, _ int, quote quoteDef, content string) bool {
			return quote.Delim == "`" &&
				hasUnescapedPrefix(content, "${")
		})
}

// --- Snippet matching ---

// nodeRequire matches Node.js require() calls and ESM from-imports:
// require('mod'), from 'mod'.
func nodeRequire(modules ...string) matchBuilder {
	return syntaxNode.match(
		`\b(?:require\s*\(?\s*|from\s+)['"](?:` +
			reAlternation(modules) + `)['"]`)
}
