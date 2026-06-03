package breakdown

import (
	"testing"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/rules"
	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// stubBreakdown is a BreakdownFunc that strips the first
// arg and returns the rest as an inner command (mimicking
// a simple wrapper). Records whether it was called.
func stubBreakdown(called *bool) model.BreakdownFunc {
	return func(
		input model.ParseResult,
		_ *model.State,
	) (*model.UnwrapResult, error) {
		*called = true
		if len(input.Raw) == 0 {
			return &model.UnwrapResult{}, nil
		}
		return &model.UnwrapResult{
			Commands: [][]*syntax.Word{
				input.Raw,
			},
		}, nil
	}
}

func makeRegistry(
	name string, pm model.PathMode, called *bool,
) map[string]*model.CommandRules {
	return map[string]*model.CommandRules{
		name: {
			Breakdown: stubBreakdown(called),
			PathMode:  pm,
		},
	}
}

func TestPathDenyRejectsPathInvoked(t *testing.T) {
	var called bool
	reg := makeRegistry("mycmd", model.PathDeny, &called)
	_, err := Breakdown(
		"/usr/bin/mycmd arg", "/tmp", reg, rules.AllEnabled())
	if err == nil {
		t.Fatal("expected error for path-invoked " +
			"command with PathDeny")
	}
	if called {
		t.Error("breakdown should not have been " +
			"called")
	}
}

func TestPathDenyAllowsBareCommand(t *testing.T) {
	var called bool
	reg := makeRegistry("mycmd", model.PathDeny, &called)
	_, err := Breakdown("mycmd arg", "/tmp", reg, rules.AllEnabled())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("breakdown should have been called")
	}
}

func TestPathSkipSkipsPathInvoked(t *testing.T) {
	var called bool
	reg := makeRegistry("mycmd", model.PathSkip, &called)
	result, err := Breakdown(
		"/usr/bin/mycmd arg", "/tmp", reg, rules.AllEnabled())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("breakdown should not have been " +
			"called for path-invoked PathSkip")
	}
	// Command should fall through to flattening
	// with original args intact.
	if len(result.Commands) != 1 {
		t.Fatalf("got %d commands, want 1",
			len(result.Commands))
	}
	got := word.Texts(result.Commands[0].Args)
	if got[0] != "/usr/bin/mycmd" {
		t.Errorf("arg[0] = %q, want path preserved",
			got[0])
	}
}

func TestPathSkipAllowsBareCommand(t *testing.T) {
	var called bool
	reg := makeRegistry("mycmd", model.PathSkip, &called)
	_, err := Breakdown("mycmd arg", "/tmp", reg, rules.AllEnabled())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("breakdown should have been called " +
			"for bare command with PathSkip")
	}
}

func TestPathAllowRunsBreakdownForPath(t *testing.T) {
	var called bool
	reg := makeRegistry(
		"mycmd", model.PathAllow, &called)
	result, err := Breakdown(
		"/usr/bin/mycmd arg", "/tmp", reg, rules.AllEnabled())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("breakdown should have been called " +
			"for path-invoked PathAllow")
	}
	// The outer command must be kept so it reaches
	// the permissions layer. The stub breakdown also
	// produces an inner command from the args, so we
	// expect both: the inner "arg" command and the
	// outer "/usr/bin/mycmd arg".
	found := false
	for _, cmd := range result.Commands {
		args := word.Texts(cmd.Args)
		if len(args) > 0 &&
			args[0] == "/usr/bin/mycmd" {
			found = true
		}
	}
	if !found {
		t.Error("outer command should be kept " +
			"for path-invoked PathAllow")
	}
}

func TestPathAllowBareCommandReplaces(t *testing.T) {
	var called bool
	reg := makeRegistry(
		"mycmd", model.PathAllow, &called)
	result, err := Breakdown(
		"mycmd arg", "/tmp", reg, rules.AllEnabled())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("breakdown should have been called")
	}
	// Bare (no path) should still replace — only
	// path-invoked keeps the outer command.
	for _, cmd := range result.Commands {
		args := word.Texts(cmd.Args)
		if len(args) > 0 && args[0] == "mycmd" {
			t.Error("outer command should be " +
				"replaced for bare PathAllow")
		}
	}
}

func TestPathDenyRelativePath(t *testing.T) {
	var called bool
	reg := makeRegistry("mycmd", model.PathDeny, &called)
	_, err := Breakdown("./mycmd arg", "/tmp", reg, rules.AllEnabled())
	if err == nil {
		t.Fatal("expected error for relative " +
			"path-invoked command with PathDeny")
	}
	if called {
		t.Error("breakdown should not have been " +
			"called")
	}
}

func TestPathSkipRelativePath(t *testing.T) {
	var called bool
	reg := makeRegistry("mycmd", model.PathSkip, &called)
	result, err := Breakdown(
		"./mycmd arg", "/tmp", reg, rules.AllEnabled())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("breakdown should not have been " +
			"called for relative path PathSkip")
	}
	if len(result.Commands) != 1 {
		t.Fatalf("got %d commands, want 1",
			len(result.Commands))
	}
	got := word.Texts(result.Commands[0].Args)
	if got[0] != "./mycmd" {
		t.Errorf("arg[0] = %q, want path preserved",
			got[0])
	}
}
