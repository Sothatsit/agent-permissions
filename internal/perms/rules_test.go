package perms

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/rules"
	"github.com/sothatsit/agent-permissions/internal/word"
)

func TestCheckRulePrecedence(t *testing.T) {
	def := &model.RuleDef{ID: "test.precedence"}
	allow := model.Always().WithRuleDef(def).Allow("allow")
	softAsk := model.Always().WithRuleDef(def).SoftAsk("soft ask")
	ask := model.Always().WithRuleDef(def).Ask("ask")
	deny := model.Always().WithRuleDef(def).Deny("deny")

	tests := []struct {
		name  string
		rules []model.Rule
		want  model.Decision
	}{
		{"soft ask after allow", []model.Rule{allow, softAsk}, model.SoftAsk},
		{"soft ask before allow", []model.Rule{softAsk, allow}, model.SoftAsk},
		{"ask after soft ask", []model.Rule{softAsk, ask}, model.Ask},
		{"ask before soft ask", []model.Rule{ask, softAsk}, model.Ask},
		{"deny after ask", []model.Rule{ask, deny}, model.Deny},
		{"deny before ask", []model.Rule{deny, ask}, model.Deny},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			permissions := &Permissions{rules: map[string]*model.CommandRules{
				"tool": {Rules: test.rules},
			}}
			result := permissions.Check(model.BreakdownResult{
				Commands: []model.Command{{
					Args: word.FromStrings([]string{"tool"}),
				}},
			})
			if result.Decision != test.want {
				t.Errorf("decision = %v, want %v", result.Decision, test.want)
			}
		})
	}
}

func TestCheckRuleDefaultsAreStableAndAttributed(t *testing.T) {
	commandDef := &model.RuleDef{ID: "test.command-default"}
	nestedDef := &model.RuleDef{ID: "test.nested-default"}
	tests := []struct {
		name       string
		args       []string
		rules      *model.CommandRules
		wantReason string
	}{
		{
			name: "command default",
			args: []string{"tool"},
			wantReason: "Deny:\n* tool - cannot verify  " +
				"(from rule:test.command-default)",
			rules: &model.CommandRules{
				Default:    model.DenyAction("cannot verify"),
				Unverified: commandDef,
			},
		},
		{
			name: "nested default",
			args: []string{"tool", "api", "endpoint"},
			wantReason: "Deny:\n* tool api - unrecognised  " +
				"(from rule:test.nested-default)",
			rules: &model.CommandRules{Rules: []model.Rule{
				model.Subcmd("api").
					WithRuleDef(nestedDef).
					DefaultDeny("unrecognised").Rules(
					model.Flag("--known").Allow("known"),
				),
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			permissions := &Permissions{rules: map[string]*model.CommandRules{
				"tool": test.rules,
			}}
			breakdown := model.BreakdownResult{
				Commands: []model.Command{{
					Args: word.FromStrings(test.args),
				}},
			}

			first := permissions.Check(breakdown)
			second := permissions.Check(breakdown)
			if !reflect.DeepEqual(first, second) {
				t.Fatalf(
					"repeated checks differ:\nfirst:  %+v\nsecond: %+v",
					first, second,
				)
			}

			if first.Decision != model.Deny {
				t.Errorf("decision = %v, want deny", first.Decision)
			}

			if first.Reason != test.wantReason {
				t.Errorf("reason = %q, want %q", first.Reason, test.wantReason)
			}
		})
	}
}

func TestRgCommandExecutionDeniesFlagsThatRunPrograms(t *testing.T) {
	registry, _ := rules.Registry()
	permissions := &Permissions{rules: registry}
	tests := []struct {
		args     []string
		wantDeny bool
	}{
		{[]string{"rg", "--pre", "./x.sh", "pattern"}, true},
		{[]string{"rg", "--pre=./x.sh", "pattern"}, true},
		{[]string{"rg", "--hostname-bin", "./x.sh", "pattern"}, true},
		{[]string{"rg", "--hostname-bin=./x.sh", "pattern"}, true},
		{[]string{"/d/sw/ripgrep/latest/rg", "--pre", "./x.sh", "p"}, true},
		{[]string{"/opt/untrusted/rg", "--pre=./x.sh", "p"}, true},
		{[]string{"rg", "--pre-glob", "*.pdf", "pattern"}, false},
		{[]string{"rg", "-z", "pattern"}, false},
		{[]string{"rg", "--", "--pre", "file"}, false},
	}

	for _, test := range tests {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			got := permissions.Check(model.BreakdownResult{
				Commands: []model.Command{{
					Args: word.FromStrings(test.args),
				}},
			})
			denied := got.Decision == model.Deny
			if denied != test.wantDeny {
				t.Errorf("decision = %v, want deny %v", got.Decision,
					test.wantDeny)
			}
		})
	}
}
