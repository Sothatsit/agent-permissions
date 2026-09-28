package perms

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/sothatsit/agent-permissions/internal/agentconfig"
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/rules"
	"github.com/sothatsit/agent-permissions/internal/word"
	"github.com/sothatsit/agent-permissions/presets"
)

func tierSource(
	t *testing.T, name string, tiers map[model.Decision][]string,
) SourcePerms {
	t.Helper()
	src := SourcePerms{Name: name}
	for decision, raws := range tiers {
		var patterns []Pattern
		for _, raw := range raws {
			patterns = append(patterns, mustCmdPattern(t, raw))
		}

		switch decision {
		case model.Allow:
			src.Allow.Commands = patterns
		case model.SoftAsk:
			src.SoftAsk.Commands = patterns
		case model.Ask:
			src.Ask.Commands = patterns
		case model.Deny:
			src.Deny.Commands = patterns
		default:
			t.Fatalf("unsupported decision %v", decision)
		}
	}

	return src
}

func TestAliasTakesTargetEntriesWhereItsOwnHaveNoOpinion(t *testing.T) {
	podmanRun := map[string]Alias{"podman-run": {
		Name: "podman-run", Target: "podman", Source: "test",
	}}
	tests := []struct {
		name      string
		aliases   map[string]Alias
		normal    []map[model.Decision][]string
		enforced  []map[model.Decision][]string
		args      []string
		want      model.Decision
		wantLabel string
	}{
		{
			name: "target fills in",
			normal: []map[model.Decision][]string{
				{model.Allow: {"podman:*"}},
			},
			args:      []string{"podman-run", "ps"},
			want:      model.Allow,
			wantLabel: "podman:* (via alias podman-run)",
		},
		{
			name:    "no alias leaves the command unknown",
			aliases: map[string]Alias{},
			normal: []map[model.Decision][]string{
				{model.Allow: {"podman:*"}},
			},
			args: []string{"podman-run", "ps"},
			want: model.Undecided,
		},
		{
			name: "own entry decides over the target's",
			normal: []map[model.Decision][]string{{
				model.Allow: {"podman-run push:*"},
				model.Ask:   {"podman push:*"},
			}},
			args:      []string{"podman-run", "push", "x"},
			want:      model.Allow,
			wantLabel: "podman-run push:*",
		},
		{
			name: "target's entry still decides for the target",
			normal: []map[model.Decision][]string{{
				model.Allow: {"podman-run push:*"},
				model.Ask:   {"podman push:*"},
			}},
			args: []string{"podman", "push", "x"},
			want: model.Ask,
		},
		// The target is consulted only once the command's own entries
		// leave the normal plane undecided, so source priority does not
		// let a higher source's target entry override a lower source's
		// own entry.
		{
			name: "own entry in a lower source beats the target's",
			normal: []map[model.Decision][]string{
				{model.Deny: {"podman push:*"}},
				{model.Allow: {"podman-run push:*"}},
			},
			args: []string{"podman-run", "push", "x"},
			want: model.Allow,
		},
		{
			name: "enforced target deny beats own normal allow",
			normal: []map[model.Decision][]string{
				{model.Allow: {"podman-run login:*"}},
			},
			enforced: []map[model.Decision][]string{
				{model.Deny: {"podman login:*"}},
			},
			args:      []string{"podman-run", "login"},
			want:      model.Deny,
			wantLabel: "podman login:* (via alias podman-run)",
		},
		{
			name: "enforced target ask beats own enforced allow",
			enforced: []map[model.Decision][]string{{
				model.Allow: {"podman-run push:*"},
				model.Ask:   {"podman push:*"},
			}},
			args: []string{"podman-run", "push", "x"},
			want: model.Ask,
		},
		{
			name: "untrusted path takes the target's denials",
			normal: []map[model.Decision][]string{{
				model.Allow: {"podman:*"},
				model.Deny:  {"podman login:*"},
			}},
			args: []string{"/opt/x/podman-run", "login"},
			want: model.Deny,
			wantLabel: "podman login:* " +
				"(via alias /opt/x/podman-run)",
		},
		{
			name: "untrusted path does not take the target's allows",
			normal: []map[model.Decision][]string{
				{model.Allow: {"podman:*"}},
			},
			args: []string{"/opt/x/podman-run", "ps"},
			want: model.Undecided,
		},
		{
			name: "path on PATH takes the target's allows",
			normal: []map[model.Decision][]string{
				{model.Allow: {"podman:*"}},
			},
			args: []string{"/usr/bin/podman-run", "ps"},
			want: model.Allow,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			aliases := podmanRun
			if test.aliases != nil {
				aliases = test.aliases
			}

			p := &Permissions{
				Aliases:  aliases,
				PathDirs: map[string]struct{}{"/usr/bin": {}},
			}
			for i, tiers := range test.normal {
				p.Sources = append(p.Sources, tierSource(
					t, fmt.Sprintf("normal%d", i), tiers))
			}

			for i, tiers := range test.enforced {
				p.EnforcedSources = append(p.EnforcedSources,
					tierSource(t, fmt.Sprintf(
						"enforced%d", i), tiers))
			}

			check := p.checkOne(model.Command{
				Args: word.FromStrings(test.args),
			})
			if check.decision != test.want {
				t.Fatalf("decision = %v, want %v",
					check.decision, test.want)
			}

			label := strings.Join(checkLabels(
				check, model.Command{}), "\n")
			if !strings.Contains(label, test.wantLabel) {
				t.Errorf("labels %q do not contain %q",
					label, test.wantLabel)
			}
		})
	}
}

func TestParseAliasesDropsWhatItCannotHonour(t *testing.T) {
	registry, _ := rules.Registry()
	aliases, warnings := parseAliases(nil, "test", map[string]string{
		"podman-run": "podman",
		"my-tool":    "some-unregistered-tool",
		"loop":       "loop",
		"git":        "podman",
		"mygit":      "git",
		"mytimeout":  "timeout",
		"bad/name":   "podman",
		"blank":      "",
	}, registry)

	var names []string
	for _, alias := range aliases {
		names = append(names, alias.Name)
	}

	wantNames := []string{"my-tool", "podman-run"}
	if !slices.Equal(names, wantNames) {
		t.Errorf("kept %q, want %q", names, wantNames)
	}

	wantReasons := map[string]string{
		"loop -> loop":         "to itself",
		"git -> podman":        "rules of its own",
		"mygit -> git":         "beyond global options",
		"mytimeout -> timeout": "beyond global options",
		"bad/name -> podman":   "bare command name",
		"blank -> ":            "bare command name",
	}
	if len(warnings) != len(wantReasons) {
		t.Errorf("got %d warnings, want %d: %v",
			len(warnings), len(wantReasons), warnings)
	}

	for _, w := range warnings {
		want, ok := wantReasons[w.Entry]
		if !ok || !strings.Contains(w.Reason, want) {
			t.Errorf("warning %q: %q, want reason with %q",
				w.Entry, w.Reason, want)
		}
	}
}

func TestResolveAliasesPrecedence(t *testing.T) {
	registry, _ := rules.Registry()
	selected := []*presets.Preset{
		{Name: "site", Enforced: true, Aliases: map[string]string{
			"locked": "podman",
		}},
		{Name: "external", Aliases: map[string]string{
			"global-wins": "docker",
			"locked":      "docker",
		}},
		{Name: "embedded", Aliases: map[string]string{
			"global-wins": "docker",
			"local-wins":  "podman",
			"external":    "podman",
		}},
	}
	configs := []AgentConfigSource{
		{SourceName: "local", Config: &agentconfig.Config{
			Aliases: map[string]string{
				"local-wins": "docker",
				"locked":     "docker",
			},
		}},
		{SourceName: "project", Config: &agentconfig.Config{}},
		{SourceName: "global", Config: &agentconfig.Config{
			Aliases: map[string]string{
				"global-wins": "podman",
				"local-wins":  "podman",
			},
		}},
	}

	aliases, warnings := resolveAliases(configs, selected, registry)
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}

	want := map[string]Alias{
		"locked": {"locked", "podman", "enforced-preset:site"},
		"global-wins": {
			"global-wins", "podman", "global"},
		"local-wins": {"local-wins", "docker", "local"},
		"external": {
			"external", "podman", "preset:embedded"},
	}
	for name, alias := range want {
		if aliases[name] != alias {
			t.Errorf("%s = %+v, want %+v",
				name, aliases[name], alias)
		}
	}
}

func TestResolveAliasesReportsChains(t *testing.T) {
	registry, _ := rules.Registry()
	_, warnings := resolveAliases(nil, []*presets.Preset{
		{Name: "chained", Aliases: map[string]string{
			"outer":  "middle",
			"middle": "podman",
		}},
	}, registry)

	if len(warnings) != 1 ||
		warnings[0].Entry != "outer -> middle" ||
		!strings.Contains(warnings[0].Reason, "do not chain") {
		t.Errorf("warnings = %v, want one chain warning on outer",
			warnings)
	}
}
