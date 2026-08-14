package breakdown

import (
	"errors"
	"strings"
	"testing"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

func TestBreakdownRejectsMissingOutcome(t *testing.T) {
	registry := map[string]*model.CommandRules{
		"wrapper": {
			Breakdown: func(
				model.ParseResult,
				*model.State,
			) (model.BreakdownOutcome, error) {
				return model.BreakdownOutcome{}, nil
			},
		},
	}

	_, err := Breakdown(
		"wrapper", "/work", registry, model.RuleConfigs{})
	if err == nil || !strings.Contains(err.Error(), "returned no outcome") {
		t.Fatalf("missing outcome error = %v", err)
	}
}

func TestBreakdownAcceptsMissingOutcomeWithError(t *testing.T) {
	want := errors.New("cannot verify")
	registry := map[string]*model.CommandRules{
		"wrapper": {
			Breakdown: func(
				model.ParseResult,
				*model.State,
			) (model.BreakdownOutcome, error) {
				return model.BreakdownOutcome{}, want
			},
		},
	}

	_, err := Breakdown(
		"wrapper", "/work", registry, model.RuleConfigs{})
	if !errors.Is(err, want) {
		t.Fatalf("breakdown error = %v, want %v", err, want)
	}
}

func TestAssignmentOnlyInputIsHandled(t *testing.T) {
	for _, command := range []string{
		"FOO=bar",
		"export FOO=bar",
		"[[ true ]]; FOO=bar",
	} {
		t.Run(command, func(t *testing.T) {
			result, err := wbd(t, command)
			if err != nil {
				t.Fatalf("breakdown error: %v", err)
			}
			if !result.IsSafe() {
				t.Fatal("assignment-only input is not handled")
			}
			if !hasAssign(result, "FOO") {
				t.Fatalf("assignments = %v, want FOO", result.Assigns)
			}
		})
	}
}

func TestNonExecutableInputIsHandled(t *testing.T) {
	for _, command := range []string{
		"# comment",
		"case x in esac",
		"[[ true ]]; case x in esac",
		"bash -c '# comment'",
		"eval '# comment'",
		"flock lock -c '# comment'",
	} {
		t.Run(command, func(t *testing.T) {
			result, err := wbd(t, command)
			if err != nil {
				t.Fatalf("breakdown error: %v", err)
			}
			if !result.IsSafe() {
				t.Fatal("non-executable input is not handled")
			}
		})
	}
}

func TestOutcomesScanArgumentsOnce(t *testing.T) {
	safe := func(
		model.ParseResult,
		*model.State,
	) (model.BreakdownOutcome, error) {
		return model.Safe(), nil
	}
	replace := func(
		input model.ParseResult,
		_ *model.State,
	) (model.BreakdownOutcome, error) {
		return model.ReplaceOuter(model.BreakdownWork{
			Commands: [][]*syntax.Word{input.Raw[1:]},
		}), nil
	}
	keep := func(
		input model.ParseResult,
		_ *model.State,
	) (model.BreakdownOutcome, error) {
		return model.KeepOuter(model.BreakdownWork{
			Commands: [][]*syntax.Word{input.Raw},
		}), nil
	}
	codeAndCommand := func(
		input model.ParseResult,
		_ *model.State,
	) (model.BreakdownOutcome, error) {
		return model.ReplaceOuter(model.BreakdownWork{
			CodeStrings: []string{word.Text(input.Raw[0])},
			Commands:    [][]*syntax.Word{input.Raw[1:]},
		}), nil
	}
	innerCode := func(
		input model.ParseResult,
		_ *model.State,
	) (model.BreakdownOutcome, error) {
		return model.ReplaceOuter(model.BreakdownWork{
			CodeStrings: []string{word.Text(input.Raw[0])},
		}), nil
	}
	outerCommand := func(
		input model.ParseResult,
		_ *model.State,
	) (model.BreakdownOutcome, error) {
		return model.ReplaceOuter(model.BreakdownWork{
			Commands: [][]*syntax.Word{input.Raw[1:]},
		}), nil
	}
	outerKeepCommand := func(
		input model.ParseResult,
		_ *model.State,
	) (model.BreakdownOutcome, error) {
		return model.KeepOuter(model.BreakdownWork{
			Commands: [][]*syntax.Word{input.Raw[1:]},
		}), nil
	}

	tests := []struct {
		name      string
		command   string
		breakdown model.BreakdownFunc
		pathMode  model.PathMode
		want      []string
	}{
		{"safe", "wrapper $(danger)", safe, 0, []string{"danger"}},
		{"replace", "wrapper $(danger) echo", replace, 0, []string{"danger"}},
		{"keep", "wrapper echo $(danger)", keep, 0, []string{"danger"}},
		{
			"path allow", "/usr/bin/wrapper $(danger) echo",
			replace, model.PathAllow, []string{"danger"},
		},
		{
			"code and command",
			`wrapper 'echo $(danger)' echo $(otherdanger)`,
			codeAndCommand, 0, []string{"danger", "otherdanger"},
		},
		{
			"nested code wrapper",
			`wrapper consumed inner 'echo $(danger)'`,
			outerCommand, 0, []string{"danger"},
		},
		{
			"kept outer with nested code wrapper",
			`wrapper consumed inner 'echo $(danger)'`,
			outerKeepCommand, 0, []string{"danger"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := map[string]*model.CommandRules{
				"wrapper": {
					Breakdown: test.breakdown,
					PathMode:  test.pathMode,
				},
				"inner": {Breakdown: innerCode},
			}
			result, err := Breakdown(
				test.command, "/work", registry, model.RuleConfigs{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, name := range test.want {
				if countCommands(result, name) != 1 {
					t.Fatalf(
						"commands = %v, want %s once",
						cmdNames(result), name)
				}
			}
		})
	}
}

func countCommands(result model.BreakdownResult, name string) int {
	count := 0
	for _, command := range result.Commands {
		if len(command.Args) > 0 &&
			word.DefinitelyEqual(command.Args[0], name) {
			count++
		}
	}

	return count
}
