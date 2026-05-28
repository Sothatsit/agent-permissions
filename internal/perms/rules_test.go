package perms

import (
	"testing"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// --- stronger ---

func TestStronger(t *testing.T) {
	allow := &model.Action{
		Decision: model.Allow, Reason: "a",
	}
	ask := &model.Action{
		Decision: model.Ask, Reason: "b",
	}
	deny := &model.Action{
		Decision: model.Deny, Reason: "c",
	}

	if stronger(nil, ask) != ask {
		t.Error("nil,ask should be ask")
	}
	if stronger(deny, nil) != deny {
		t.Error("deny,nil should be deny")
	}
	if stronger(allow, ask) != ask {
		t.Error("allow,ask should be ask")
	}
	if stronger(deny, ask) != deny {
		t.Error("deny,ask should be deny")
	}
	if stronger(ask, deny) != deny {
		t.Error("ask,deny should be deny")
	}
}

// --- formatAction ---

func TestFormatAction(t *testing.T) {
	a := &model.Action{
		Decision: model.Deny,
		Reason:   "executes a program",
	}
	result := formatAction(a, "git --upload-pack")
	want := "git --upload-pack: executes a program"
	if result.Reason != want {
		t.Errorf("got %q", result.Reason)
	}
	if result.Decision != model.Deny {
		t.Error("decision should be preserved")
	}
	// Must not mutate original.
	if a.Reason != "executes a program" {
		t.Error("original mutated")
	}
}

func TestFormatActionBare(t *testing.T) {
	a := &model.Action{
		Decision: model.Deny,
		Reason: "invocation could not " +
			"be verified",
	}
	result := formatAction(a, "bash")
	want := "bash: invocation could not be verified"
	if result.Reason != want {
		t.Errorf("got %q", result.Reason)
	}
}

// --- Evaluate ---

func TestEvaluateFlagDeny(t *testing.T) {
	r := map[string]*model.CommandRules{
		"_test": {
			Rules: []model.Rule{
				model.Flag("--dangerous").Deny(
					"bad flag"),
			},
		},
	}

	result := Evaluate(r, "_test",
		word.FromStrings([]string{"--dangerous"}))
	if result == nil {
		t.Fatal("expected deny")
	}
	if result.Decision != model.Deny {
		t.Errorf("got %v, want Deny",
			result.Decision)
	}
}

func TestEvaluateNoMatch(t *testing.T) {
	r := map[string]*model.CommandRules{
		"_test": {
			Rules: []model.Rule{
				model.Flag("--dangerous").Deny(
					"bad flag"),
			},
		},
	}

	result := Evaluate(r, "_test",
		word.FromStrings([]string{"--safe"}))
	if result != nil {
		t.Errorf(
			"expected nil (fall through), got %+v",
			result)
	}
}

func TestEvaluateCommandDefault(t *testing.T) {
	r := map[string]*model.CommandRules{
		"_test": {
			Default: model.DenyAction(
				"invocation could not be verified"),
			Rules: []model.Rule{
				model.Hook("test", func(
					input model.ParseResult,
				) (model.Decision, string) {
					return model.Undecided, ""
				}),
			},
		},
	}

	result := Evaluate(r, "_test",
		word.FromStrings([]string{"something"}))
	if result == nil {
		t.Fatal("expected default deny")
	}
	if result.Decision != model.Deny {
		t.Errorf("got %v, want Deny",
			result.Decision)
	}
}

func TestEvaluateSubcmdWithHook(t *testing.T) {
	r := map[string]*model.CommandRules{
		"_test": {
			Rules: []model.Rule{
				model.Subcmd("api").DefaultDeny(
					"unrecognised flag").Rules(
					model.Hook("classify", func(
						input model.ParseResult,
					) (model.Decision, string) {
						return model.Allow,
							"read-only request"
					}),
				),
			},
		},
	}

	result := Evaluate(r, "_test",
		word.FromStrings(
			[]string{"api", "/repos"}))
	if result == nil {
		t.Fatal("expected result from hook")
	}
	if result.Decision != model.Allow {
		t.Errorf("got %v, want Allow",
			result.Decision)
	}
}

func TestEvaluateSubcmdDefault(t *testing.T) {
	r := map[string]*model.CommandRules{
		"_test": {
			Rules: []model.Rule{
				model.Subcmd("api").DefaultDeny(
					"unrecognised flag").Rules(
					model.Hook("classify", func(
						input model.ParseResult,
					) (model.Decision, string) {
						return model.Undecided, ""
					}),
				),
			},
		},
	}

	result := Evaluate(r, "_test",
		word.FromStrings(
			[]string{"api", "/repos"}))
	if result == nil {
		t.Fatal("expected default deny")
	}
	if result.Decision != model.Deny {
		t.Errorf("got %v, want Deny",
			result.Decision)
	}
}

func TestEvaluatePrecedenceDenyBeatsAllow(t *testing.T) {
	r := map[string]*model.CommandRules{
		"_test": {
			Rules: []model.Rule{
				model.Flag("--safe").Allow("safe flag"),
				model.Flag("--dangerous").Deny(
					"bad flag"),
			},
		},
	}

	result := Evaluate(r, "_test",
		word.FromStrings(
			[]string{"--safe", "--dangerous"}))
	if result == nil {
		t.Fatal("expected deny")
	}
	if result.Decision != model.Deny {
		t.Errorf("got %v, want Deny",
			result.Decision)
	}
}

func TestEvaluateUnknownCommand(t *testing.T) {
	r := map[string]*model.CommandRules{}
	result := Evaluate(r, "_nonexistent",
		word.FromStrings([]string{"foo"}))
	if result != nil {
		t.Errorf(
			"expected nil for unknown command, "+
				"got %+v", result)
	}
}

func TestEvaluateNoRulesNilDefault(t *testing.T) {
	r := map[string]*model.CommandRules{
		"_test": {
			Rules: []model.Rule{
				model.Flag("--x").Deny("bad"),
			},
		},
	}

	result := Evaluate(r, "_test",
		word.FromStrings([]string{"--y"}))
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}
}
