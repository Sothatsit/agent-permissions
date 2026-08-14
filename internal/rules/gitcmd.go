package rules

import (
	"fmt"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// breakdownGit strips -C <path> from git's global options so the command
// matches permission patterns written for plain git <subcommand>. -C only
// changes git's working directory - no security implication. Scanning stops at
// the first non-flag arg (the subcommand), so subcommand-level -C flags (e.g.
// git branch -C) are not affected.
func breakdownGit(
	input model.ParseResult,
	_ *model.State,
) (model.BreakdownOutcome, error) {
	args := input.Raw
	var stripped []*syntax.Word
	found := false

	for i := 0; i < len(args); i++ {
		w := args[i]

		if word.DefinitelyEqual(w, "-C") {
			if i+1 >= len(args) {
				return model.BreakdownOutcome{}, fmt.Errorf(
					"git -C requires a path argument")
			}

			found = true
			i++ // skip path
			continue
		}

		// Non-flag word = subcommand. Stop scanning for global options
		// and copy the rest as-is.
		if !word.DefinitelyHasPrefix(w, "-") {
			stripped = append(stripped, args[i:]...)
			break
		}

		// Other global flag - keep it.
		stripped = append(stripped, w)
	}

	if !found {
		return model.FallThrough(), nil
	}

	result := make([]*syntax.Word, 0, len(stripped)+1)
	result = append(result, word.Lit(input.Name))
	result = append(result, stripped...)

	return model.ReplaceOuter(model.BreakdownWork{
		Commands: [][]*syntax.Word{result},
	}), nil
}

// gitBranchParser classifies git branch flags. Unknown flags cause a parse
// error (deny).
var gitBranchParser = model.NewFullParser(
	[]model.FlagDef{
		{Name: "--edit-description"},
		{Name: "--set-upstream-to", Arg: true},
		{Name: "--unset-upstream"},
		{Name: "--show-current"},
		{Name: "--ignore-case"},
		{Name: "--no-contains", Arg: true},
		{Name: "--omit-empty"},
		{Name: "--no-column"},
		{Name: "--no-abbrev"},
		{Name: "--no-merged", Arg: true},
		{Name: "--points-at", Arg: true},
		{Name: "--no-color"},
		{Name: "--contains", Arg: true},
		{Name: "--verbose"},
		{Name: "--remotes"},
		{Name: "--column"},
		{Name: "--delete", Arg: true},
		{Name: "--format", Arg: true},
		{Name: "--merged", Arg: true},
		{Name: "--abbrev", Arg: true},
		{Name: "--color"},
		{Name: "--move", Arg: true},
		{Name: "--copy", Arg: true},
		{Name: "--sort", Arg: true},
		{Name: "--list"},
		{Name: "--all"},
		{Name: "-vv"},
		{Name: "-a"}, {Name: "-r"}, {Name: "-v"},
		{Name: "-i"}, {Name: "-l"},
		{Name: "-D"}, {Name: "-M"}, {Name: "-C"},
		{Name: "-d", Arg: true},
		{Name: "-m", Arg: true},
		{Name: "-c", Arg: true},
		{Name: "-u", Arg: true},
	},
	model.InterspersedFlags,
	"unrecognised flag",
)

var gitBranchListFlags = map[string]bool{
	"-a": true, "--all": true,
	"-r": true, "--remotes": true,
	"--list": true, "-l": true,
	"--show-current": true,
	"--contains":     true, "--no-contains": true,
	"--merged": true, "--no-merged": true,
	"--points-at": true,
	"--sort":      true,
	"--format":    true,
	"--abbrev":    true, "--no-abbrev": true,
	"--column": true, "--no-column": true,
	"--color": true, "--no-color": true,
	"-i": true, "--ignore-case": true,
	"--omit-empty": true,
}

var gitBranchWriteFlags = map[string]bool{
	"-d": true, "--delete": true,
	"-D": true,
	"-m": true, "--move": true,
	"-M": true,
	"-c": true, "--copy": true,
	"-C":                 true,
	"--edit-description": true,
	"-u":                 true,
	"--set-upstream-to":  true,
	"--unset-upstream":   true,
}

func classifyGitBranch(
	input model.ParseResult,
) (model.Decision, string) {
	parsed, err := gitBranchParser.Parse(input.Raw)
	if err != nil {
		return model.Deny,
			fmt.Sprintf("git branch: %s", err)
	}

	for _, f := range parsed.Flags {
		if gitBranchWriteFlags[f.Name] {
			return model.SoftAsk, fmt.Sprintf(
				"git branch: write flag %s", f.Name)
		}
	}

	listMode := false
	for _, f := range parsed.Flags {
		if gitBranchListFlags[f.Name] {
			listMode = true
			break
		}
	}

	if len(parsed.Positionals) > 0 && !listMode {
		return model.SoftAsk,
			"git branch: possible write operation"
	}

	return model.Allow, "git branch: read-only"
}

// gitTagParser classifies git tag flags.
var gitTagParser = model.NewFullParser(
	[]model.FlagDef{
		{Name: "--create-reflog"},
		{Name: "--ignore-case"},
		{Name: "--no-contains", Arg: true},
		{Name: "--omit-empty"},
		{Name: "--local-user", Arg: true},
		{Name: "--no-column"},
		{Name: "--points-at", Arg: true},
		{Name: "--no-merged", Arg: true},
		{Name: "--no-color"},
		{Name: "--annotate"},
		{Name: "--contains", Arg: true},
		{Name: "--message", Arg: true},
		{Name: "--cleanup", Arg: true},
		{Name: "--verify"},
		{Name: "--format", Arg: true},
		{Name: "--column"},
		{Name: "--delete", Arg: true},
		{Name: "--merged", Arg: true},
		{Name: "--color"},
		{Name: "--force"},
		{Name: "--sign"},
		{Name: "--sort", Arg: true},
		{Name: "--file", Arg: true},
		{Name: "--list"},
		{Name: "--edit"},
		{Name: "-n", Prefix: true},
		{Name: "-l"}, {Name: "-i"}, {Name: "-v"},
		{Name: "-a"}, {Name: "-s"}, {Name: "-f"},
		{Name: "-d", Arg: true},
		{Name: "-m", Arg: true},
		{Name: "-F", Arg: true},
		{Name: "-u", Arg: true},
	},
	model.InterspersedFlags,
	"unrecognised flag",
)

var gitTagWriteFlags = map[string]bool{
	"-d": true, "--delete": true,
	"-a": true, "--annotate": true,
	"-s": true, "--sign": true,
	"-f": true, "--force": true,
	"-m": true, "--message": true,
	"-F": true, "--file": true,
	"--create-reflog": true,
	"--cleanup":       true,
	"-u":              true, "--local-user": true,
	"--edit": true,
}

func classifyGitTag(
	input model.ParseResult,
) (model.Decision, string) {
	parsed, err := gitTagParser.Parse(input.Raw)
	if err != nil {
		return model.Deny,
			fmt.Sprintf("git tag: %s", err)
	}

	for _, f := range parsed.Flags {
		if gitTagWriteFlags[f.Name] {
			return model.SoftAsk, fmt.Sprintf(
				"git tag: write flag %s", f.Name)
		}
	}

	listMode := len(parsed.Flags) > 0

	if len(parsed.Positionals) > 0 && !listMode {
		return model.SoftAsk,
			"git tag: possible write operation"
	}

	return model.Allow, "git tag: read-only"
}
