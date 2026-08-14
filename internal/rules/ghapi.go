package rules

import (
	"fmt"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// ghApiParser classifies gh api flags. Unknown flags cause a parse error
// (deny).
var ghApiParser = model.NewFullParser(
	[]model.FlagDef{
		{Name: "--raw-field", Arg: true},
		{Name: "--hostname", Arg: true},
		{Name: "--paginate"},
		{Name: "--template", Arg: true},
		{Name: "--include"},
		{Name: "--preview", Arg: true},
		{Name: "--verbose"},
		{Name: "--method", Arg: true},
		{Name: "--header", Arg: true},
		{Name: "--silent"},
		{Name: "--slurp"},
		{Name: "--field", Arg: true},
		{Name: "--input", Arg: true},
		{Name: "--cache", Arg: true},
		{Name: "--help"},
		{Name: "--jq", Arg: true},
		{Name: "-i"}, {Name: "-q"},
		{Name: "-X", Arg: true},
		{Name: "-f", Arg: true},
		{Name: "-F", Arg: true},
		{Name: "-H", Arg: true},
		{Name: "-p", Arg: true},
		{Name: "-t", Arg: true},
	},
	model.InterspersedFlags,
	"unrecognised flag",
)

// gh api flags that set the HTTP method.
var ghApiMethodFlags = map[string]bool{
	"-X": true, "--method": true,
}

// gh api flags that add request body data (imply POST).
var ghApiBodyFlags = map[string]bool{
	"-f": true, "--raw-field": true,
	"-F": true, "--field": true,
	"--input": true,
}

// classifyGhApi analyzes gh api arguments to determine if the request is
// read-only.
func classifyGhApi(
	input model.ParseResult,
) (model.Decision, string) {
	parsed, err := ghApiParser.Parse(input.Raw)
	if err != nil {
		return model.Deny,
			fmt.Sprintf("gh api: %s", err)
	}

	var methodValue *syntax.Word
	var bodyFlag string
	var hasHostname bool

	for _, f := range parsed.Flags {
		if ghApiMethodFlags[f.Name] {
			methodValue = f.Value
		}
		if ghApiBodyFlags[f.Name] {
			bodyFlag = f.Name
		}
		if f.Name == "--hostname" {
			hasHostname = true
		}
	}

	if bodyFlag != "" {
		return model.Ask, fmt.Sprintf(
			"gh api: %s implies write", bodyFlag)
	}

	if methodValue != nil {
		if !word.Static(methodValue) {
			return model.Ask,
				"gh api: method is not static"
		}

		method := strings.ToUpper(
			word.Text(methodValue))
		if method != "GET" && method != "HEAD" {
			return model.Ask, fmt.Sprintf(
				"gh api: %s request", method)
		}
	}

	if hasHostname {
		return model.Ask, "gh api: --hostname " +
			"targets non-default host"
	}

	return model.Allow, "gh api: read-only request"
}
