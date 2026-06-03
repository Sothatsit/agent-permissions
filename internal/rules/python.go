package rules

import (
	"regexp"

	"github.com/sothatsit/agent-permissions/internal/model"
)

var syntaxPython = &langSyntax{
	Quotes: []quoteDef{
		{Delim: `"""`, Multiline: true},
		{Delim: `'''`, Multiline: true},
		{Delim: `"`},
		{Delim: `'`},
	},
	LineComments: []string{"#"},
}

// pythonFlags defines all recognised Python interpreter
// flags. Sorted longest-first so cluster splitting
// handles -OO before -O.
var pythonFlags = []model.FlagDef{
	{Name: "--version"}, {Name: "--help"},
	{Name: "-OO"},
	// Terminal flags — remaining args belong to the
	// script/module, not to Python.
	{Name: "-c", Arg: true, Terminal: true},
	{Name: "-m", Arg: true, Terminal: true},
	{Name: "-W", Arg: true},
	{Name: "-X", Arg: true},
	{Name: "-Q", Arg: true}, // Python 2
	// No-arg flags.
	{Name: "-b"}, {Name: "-B"}, {Name: "-d"},
	{Name: "-E"}, {Name: "-h"}, {Name: "-i"},
	{Name: "-I"}, {Name: "-O"}, {Name: "-q"},
	{Name: "-R"}, {Name: "-s"}, {Name: "-S"},
	{Name: "-t"}, {Name: "-u"}, {Name: "-v"},
	{Name: "-V"}, {Name: "-x"},
}

var pythonParser = func() *model.FullParser {
	p := model.NewFullParser(
		pythonFlags, "unrecognised Python flag")
	p.StopAtPositional = true
	return p
}()

var breakdownPython = breakdownInterpreter(
	interpreterConfig{
		lang:       model.LangPython,
		name:       "python",
		unverified: pythonUnverified,
		infoFlags: []string{
			"--version", "--help", "-V", "-h",
		},
		codeFlags:        []string{"-c"},
		fallthroughFlags: []string{"-m", "-i"},
	})

// --- Snippet matching ---

// pythonDangerousOSFuncs lists os module functions that
// execute shell commands.
var pythonDangerousOSFuncs = []string{
	"system", "popen", "exec",
}

// pythonImport matches "import module" at statement
// start and "from module import ..." (including
// wildcard and parenthesized multi-line). When names
// is nil, any import of the module matches. When set,
// only from-imports of those names match (prefix).
func pythonImport(
	module string, names []string,
) matchBuilder {
	return syntaxPython.match(
		rePythonImport(module, names))
}

// pythonCall matches qualified calls like os.system(),
// os.popen(), etc.
func pythonCall(
	module string, names []string,
) matchBuilder {
	return syntaxPython.match(
		`\b` + regexp.QuoteMeta(module) +
			`\.(?:` + reAlternation(names) + `)`)
}

func rePythonImport(
	module string, names []string,
) string {
	m := regexp.QuoteMeta(module)
	if names == nil {
		return `(?:` +
			`\bimport\b[^\n]*\b` + m + `\b` +
			`|\bfrom\s+` + m + `\s+import\b)`
	}
	np := reAlternation(names)
	return `\bfrom\s+` + m + `\s+import\s+` +
		`(?:\*` +
		`|[^\n(]*\b(?:` + np + `)` +
		`|\([^)]*\b(?:` + np + `))`
}
