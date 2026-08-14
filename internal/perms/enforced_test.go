package perms

import (
	"testing"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

func commandSource(
	t *testing.T, name string,
	decision model.Decision,
) SourcePerms {
	t.Helper()
	pat := mustCmdPattern(t, "mytool:*")
	pat.Reason = name
	src := SourcePerms{Name: name}
	switch decision {
	case model.Undecided:
		// A source that matches nothing - the way a plane
		// expresses "no opinion".
	case model.Allow:
		src.Allow.Commands = []Pattern{pat}
	case model.SoftAsk:
		src.SoftAsk.Commands = []Pattern{pat}
	case model.Ask:
		src.Ask.Commands = []Pattern{pat}
	case model.Deny:
		src.Deny.Commands = []Pattern{pat}
	default:
		t.Fatalf("unsupported decision %v", decision)
	}

	return src
}

func envSource(
	t *testing.T, name string,
	decision model.Decision,
) SourcePerms {
	t.Helper()
	pat, err := parseEnvVarPattern("POLICY_VAR", name)
	if err != nil {
		t.Fatal(err)
	}

	src := SourcePerms{Name: name}
	switch decision {
	case model.Allow:
		src.Allow.EnvVars = []EnvVarPattern{pat}
	case model.SoftAsk:
		src.SoftAsk.EnvVars = []EnvVarPattern{pat}
	case model.Ask:
		src.Ask.EnvVars = []EnvVarPattern{pat}
	case model.Deny:
		src.Deny.EnvVars = []EnvVarPattern{pat}
	default:
		t.Fatalf("unsupported decision %v", decision)
	}

	return src
}

func TestEnforcedCommandPolicyIsMinimum(t *testing.T) {
	tests := []struct {
		name     string
		normal   model.Decision
		enforced model.Decision
		want     model.Decision
	}{
		{"enforced deny", model.Allow, model.Deny, model.Deny},
		{"normal deny", model.Deny, model.Allow, model.Deny},
		{"enforced ask", model.Allow, model.Ask, model.Ask},
		{"normal ask", model.Ask, model.Allow, model.Ask},
		// An explicit Allow answers a soft-ask, so an enforced soft-ask
		// does not outrank one. Without this, soft-ask would be the one
		// tier no config could ever silence.
		{"enforced soft-ask yields to allow", model.Allow,
			model.SoftAsk, model.Allow},
		// The reverse does not hold: the enforced plane only
		// strengthens, so an enforced Allow cannot talk a
		// normal soft-ask down.
		{"normal soft-ask beats enforced allow",
			model.SoftAsk, model.Allow, model.SoftAsk},
		{"enforced soft-ask decides no opinion",
			model.Undecided, model.SoftAsk, model.SoftAsk},
		{"enforced ask beats normal soft-ask",
			model.SoftAsk, model.Ask, model.Ask},
		{"enforced deny beats normal soft-ask",
			model.SoftAsk, model.Deny, model.Deny},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Permissions{
				Sources: []SourcePerms{
					commandSource(t, "normal", tt.normal),
				},
				EnforcedSources: []SourcePerms{
					commandSource(t, "enforced", tt.enforced),
				},
			}
			got := p.checkOne(model.Command{
				Args: word.FromStrings([]string{
					"mytool", "run",
				}),
			}).decision
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnforcedCommandPolicyIgnoresSourceOrder(
	t *testing.T,
) {
	allow := commandSource(t, "allow", model.Allow)
	deny := commandSource(t, "deny", model.Deny)
	for _, sources := range [][]SourcePerms{
		{allow, deny}, {deny, allow},
	} {
		p := &Permissions{EnforcedSources: sources}
		got := p.checkOne(model.Command{
			Args: word.FromStrings([]string{"mytool"}),
		}).decision
		if got != model.Deny {
			t.Errorf("sources %v produced %v", sources, got)
		}
	}
}

func TestEnforcedAllowDecidesUnknownCommand(t *testing.T) {
	p := &Permissions{EnforcedSources: []SourcePerms{
		commandSource(t, "enforced", model.Allow),
	}}
	got := p.checkOne(model.Command{
		Args: word.FromStrings([]string{"mytool"}),
	}).decision
	if got != model.Allow {
		t.Errorf("got %v, want allow", got)
	}
}

func TestEnforcedEqualTierKeepsEveryReason(t *testing.T) {
	p := &Permissions{
		Sources: []SourcePerms{
			commandSource(t, "normal", model.Ask),
		},
		EnforcedSources: []SourcePerms{
			commandSource(t, "enforced-a", model.Ask),
			commandSource(t, "enforced-b", model.Ask),
		},
	}
	check := p.checkOne(model.Command{
		Args: word.FromStrings([]string{"mytool"}),
	})
	if len(check.reasons()) != 3 {
		t.Fatalf("got %d reasons, want 3", len(check.reasons()))
	}
}

func TestEnforcedSameSourceKeepsEveryCommandReason(
	t *testing.T,
) {
	broad := mustCmdPattern(t, "mytool:*")
	broad.Reason = "broad"
	specific := mustCmdPattern(t, "mytool run:*")
	specific.Reason = "specific"
	p := &Permissions{EnforcedSources: []SourcePerms{{
		Name: "enforced",
		Ask: TierEntries{Commands: []Pattern{
			broad, specific,
		}},
	}}}
	check := p.checkOne(model.Command{
		Args: word.FromStrings([]string{"mytool", "run"}),
	})
	if len(check.reasons()) != 2 {
		t.Fatalf("got %d reasons, want 2", len(check.reasons()))
	}
}

func TestEnforcedEnvVarPolicyIsMinimum(t *testing.T) {
	tests := []struct {
		name     string
		normal   model.Decision
		enforced model.Decision
		want     model.Decision
	}{
		{"enforced deny", model.Allow, model.Deny, model.Deny},
		{"normal deny", model.Deny, model.Allow, model.Deny},
		{"enforced ask", model.Allow, model.Ask, model.Ask},
		{"normal soft-ask", model.SoftAsk,
			model.Allow, model.SoftAsk},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Permissions{
				Sources: []SourcePerms{
					envSource(t, "normal", tt.normal),
				},
				EnforcedSources: []SourcePerms{
					envSource(t, "enforced", tt.enforced),
				},
			}
			if got := p.checkOneEnvVar(
				"POLICY_VAR",
			).decision; got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnforcedSameSourceKeepsEveryEnvVarReason(
	t *testing.T,
) {
	exact, err := parseEnvVarPattern("POLICY_VAR", "exact")
	if err != nil {
		t.Fatal(err)
	}

	prefix, err := parseEnvVarPattern("POLICY_*", "prefix")
	if err != nil {
		t.Fatal(err)
	}

	p := &Permissions{EnforcedSources: []SourcePerms{{
		Name: "enforced",
		Ask: TierEntries{EnvVars: []EnvVarPattern{
			exact, prefix,
		}},
	}}}
	check := p.checkOneEnvVar("POLICY_VAR")
	if len(check.reasons()) != 2 {
		t.Fatalf("got %d reasons, want 2", len(check.reasons()))
	}
}

func TestEnforcedEnvVarDenyCombinesWithCommandAllow(
	t *testing.T,
) {
	p := &Permissions{
		Sources: []SourcePerms{
			commandSource(t, "normal", model.Allow),
		},
		EnforcedSources: []SourcePerms{
			envSource(t, "enforced", model.Deny),
		},
	}
	result := p.Check(model.BreakdownResult{
		Commands: []model.Command{{
			Args: word.FromStrings([]string{
				"mytool", "run",
			}),
		}},
		Assigns: []string{"POLICY_VAR"},
	})
	if result.Decision != model.Deny {
		t.Errorf("got %v, want deny", result.Decision)
	}
}
