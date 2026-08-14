package perms

import (
	"strings"
	"testing"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

func TestCheckSuggestsUnknownCommandPatterns(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		registryCommand bool
		knownPattern    []string
		wantPattern     string
	}{
		{
			name:        "bare",
			args:        []string{"some-tool"},
			wantPattern: "some-tool:*",
		},
		{
			name:        "flag",
			args:        []string{"some-tool", "--flag"},
			wantPattern: "some-tool:*",
		},
		{
			name:        "filename",
			args:        []string{"some-tool", "file.txt"},
			wantPattern: "some-tool:*",
		},
		{
			name:        "slash",
			args:        []string{"some-tool", "/tmp/output"},
			wantPattern: "some-tool:*",
		},
		{
			name:        "key-value",
			args:        []string{"some-tool", "key=value"},
			wantPattern: "some-tool:*",
		},
		{
			name:            "known command unknown subcommand",
			args:            []string{"git", "apply", "change.patch"},
			registryCommand: true,
			wantPattern:     "git apply:*",
		},
		{
			name:         "suggestion stops at two words",
			args:         []string{"npm", "run", "build"},
			knownPattern: []string{"npm", "run"},
			wantPattern:  "npm run:*",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			permissions := &Permissions{}
			if test.registryCommand {
				permissions.rules = map[string]*model.CommandRules{
					test.args[0]: {},
				}
			}

			if test.knownPattern != nil {
				permissions.Sources = []SourcePerms{{
					Allow: TierEntries{Commands: []Pattern{{
						Elements: test.knownPattern,
						Raw:      strings.Join(test.knownPattern, " "),
						Mode:     MatchExact,
					}}},
				}}
			}

			got := permissions.Check(model.BreakdownResult{
				Commands: []model.Command{{
					Args: word.FromStrings(test.args),
				}},
			})
			if got.Decision != model.SoftAsk {
				t.Errorf("decision = %v, want soft ask", got.Decision)
			}

			want := "* Bash(" + test.wantPattern + ")"
			if !strings.Contains(got.Reason, want) {
				t.Errorf("reason %q does not contain %q", got.Reason, want)
			}
		})
	}
}

func TestCheckFormatsAskSoftAskAndUnknown(t *testing.T) {
	permissions := &Permissions{Sources: []SourcePerms{{
		Name: "test",
		Ask: TierEntries{
			Commands: []Pattern{{
				Elements: []string{"publish"},
				Raw:      "publish:*",
				Mode:     MatchPrefix,
				Reason:   "publishes a release",
			}},
		},
		SoftAsk: TierEntries{
			Commands: []Pattern{{
				Elements: []string{"fetch"},
				Raw:      "fetch:*",
				Mode:     MatchPrefix,
				Reason:   "uses the network",
			}},
		},
	}}}

	got := permissions.Check(model.BreakdownResult{
		Commands: []model.Command{
			{Args: word.FromStrings([]string{"publish", "release"})},
			{Args: word.FromStrings([]string{"fetch", "archive"})},
			{Args: word.FromStrings([]string{"mystery", "argument"})},
		},
	})
	want := Result{
		Decision: model.Ask,
		Reason: "Ask:\n" +
			"* publish:* - publishes a release  (from test)\n\n" +
			"Soft-ask. To allow, add to your Allow permissions:\n" +
			"* fetch:* - uses the network  (from test)\n\n" +
			"Unknown command. <unknown-command-header>:\n" +
			"* Bash(mystery:*)",
	}
	if got != want {
		t.Errorf("Check() = %#v, want %#v", got, want)
	}
}
