package rules_test

import (
	"errors"
	"testing"

	"github.com/sothatsit/agent-permissions/internal/breakdown"
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/rules"
	"github.com/sothatsit/agent-permissions/internal/word"
)

func breakShellSource(
	t *testing.T,
	command string,
) (model.BreakdownResult, error) {
	t.Helper()

	registry, snippets := rules.Registry()
	config := rules.AllEnabled()
	rules.FilterByConfig(registry, snippets, config)
	return breakdown.Breakdown(command, "/work", registry, config)
}

func hasShellSourceCommand(
	result model.BreakdownResult,
	name string,
) bool {
	for _, command := range result.Commands {
		if len(command.Args) > 0 &&
			word.DefinitelyEqual(command.Args[0], name) {
			return true
		}
	}

	return false
}

func TestLiteralShellSourceReparsesInnerSubstitution(t *testing.T) {
	commands := []string{
		`bash -c 'echo $(ssh evil)'`,
		`flock /tmp/lock -c 'echo $(ssh evil)'`,
	}

	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			result, err := breakShellSource(t, command)
			if err != nil {
				t.Fatalf("breakdown error = %v", err)
			}
			if !hasShellSourceCommand(result, "ssh") {
				t.Fatalf("commands = %v, want ssh", result.Commands)
			}
		})
	}
}

func TestOuterExpansionMakesShellSourceUnverified(t *testing.T) {
	commands := []string{
		`bash -c "$COMMAND"`,
		`bash -c "true $(printf '; ssh evil')"`,
		`bash -c "$((1 + VALUE))"`,
		`bash -c <(printf 'ssh evil')`,
		`bash -c $'ssh\x20evil'`,
		`flock /tmp/lock -c "$COMMAND"`,
		`flock /tmp/lock -c "true $(printf '; ssh evil')"`,
	}

	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			_, err := breakShellSource(t, command)
			if err == nil {
				t.Fatal("breakdown succeeded, want unverified denial")
			}

			var ruleErr *model.RuleError
			if !errors.As(err, &ruleErr) {
				t.Fatalf("error = %v, want RuleError", err)
			}
			if ruleErr.Def == nil ||
				ruleErr.Def.ID != "bash.unverified" {
				t.Fatalf("rule = %v, want bash.unverified", ruleErr.Def)
			}
		})
	}
}
