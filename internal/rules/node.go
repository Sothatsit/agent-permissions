package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"
)

var syntaxNode = &langSyntax{
	Quotes: []quoteDef{
		{Delim: `"`},
		{Delim: `'`},
		// Backticks ARE quotes in JS (template
		// literals), not shell execution.
		{Delim: "`", Multiline: true},
	},
	LineComments:  []string{"//"},
	BlockComments: []blockComment{{"/*", "*/"}},
}

// nodeFlags defines recognised Node.js interpreter flags.
// Sorted longest-first for cluster splitting.
var nodeFlags = []model.FlagDef{
	{Name: "--inspect-brk", Arg: true},
	{Name: "--version"}, {Name: "--require", Arg: true},
	{Name: "--inspect", Arg: true},
	{Name: "--check"},
	{Name: "--print", Arg: true},
	{Name: "--help"},
	{Name: "--eval", Arg: true},
	// Flags that consume the next argument. Unlike
	// Python's -c, Node continues parsing its own flags
	// after -e (StopAtPositional handles non-flag args).
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

var nodeParser = func() *model.FullParser {
	p := model.NewFullParser(
		nodeFlags, "unrecognised Node flag")
	p.StopAtPositional = true
	return p
}()

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
		// -i/interactive, -c/--check (syntax
		// check) — fall through to permissions.
		fallthroughFlags: []string{
			"-i", "-c", "--check",
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

// nodeRequire matches Node.js require() calls and
// ESM from-imports: require('mod'), from 'mod'.
func nodeRequire(modules ...string) matchBuilder {
	return syntaxNode.match(
		`\b(?:require\s*\(?\s*|from\s+)['"](?:` +
			reAlternation(modules) + `)['"]`)
}
