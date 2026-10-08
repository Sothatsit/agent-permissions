package rules

import (
	"fmt"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// ghApiParser denies on an unknown flag.
var ghApiParser = model.NewFullParser(
	[]model.FlagDef{
		{Name: "--allow-escape-sequences"},
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

var ghApiMethodFlags = map[string]bool{
	"-X": true, "--method": true,
}

// gh api flags that add a request parameter. They imply POST unless the method
// is GET, which sends them in the query string.
var ghApiFieldFlags = map[string]bool{
	"-f": true, "--raw-field": true,
	"-F": true, "--field": true,
}

// gh api flags that read a parameter's value from a file given as @path.
var ghApiFileFieldFlags = map[string]bool{
	"-F": true, "--field": true,
}

func classifyGhApi(
	input model.ParseResult,
) (model.Decision, string) {
	parsed, err := ghApiParser.Parse(input.Raw)
	if err != nil {
		return model.Deny,
			fmt.Sprintf("gh api: %s", err)
	}

	var methodValue *syntax.Word
	var fields []model.ParsedFlag
	var hasInput bool
	var hasHostname bool

	for _, f := range parsed.Flags {
		if ghApiMethodFlags[f.Name] {
			methodValue = f.Value
		}
		if ghApiFieldFlags[f.Name] {
			fields = append(fields, f)
		}
		if f.Name == "--input" {
			hasInput = true
		}
		if f.Name == "--hostname" {
			hasHostname = true
		}
	}

	if hasInput {
		return model.Ask, "gh api: --input implies write"
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

	if methodValue == nil && len(fields) > 0 {
		isGraphQL := len(parsed.Positionals) == 1 &&
			word.DefinitelyEqual(
				parsed.Positionals[0], "graphql")
		if !isGraphQL {
			return model.Ask, fmt.Sprintf(
				"gh api: %s implies write",
				fields[0].Name)
		}

		// A graphql request is always a POST, whether or not it
		// writes. GraphQL keywords are case-sensitive, so every
		// mutation contains the word, and a query that only
		// mentions it asks.
		for _, f := range fields {
			if word.MayContain(f.Value, "mutation") {
				return model.Ask, "gh api: graphql " +
					"query may be a mutation"
			}
			if ghApiFileFieldFlags[f.Name] &&
				word.MayContain(f.Value, "=@") {
				return model.Ask, fmt.Sprintf(
					"gh api: %s reads a graphql "+
						"field from a file", f.Name)
			}
		}
	}

	if hasHostname {
		return model.Ask, "gh api: --hostname " +
			"targets non-default host"
	}

	return model.Allow, "gh api: read-only request"
}
