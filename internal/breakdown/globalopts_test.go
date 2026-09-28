package breakdown

import (
	"slices"
	"strings"
	"testing"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

func commandTexts(br model.BreakdownResult) []string {
	var out []string
	for _, c := range br.Commands {
		words := make([]string, len(c.Args))
		for i, arg := range c.Args {
			words[i] = word.Text(arg)
		}

		out = append(out, strings.Join(words, " "))
	}

	return out
}

func TestContainerGlobalOptionsStripBeforeSubcommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want []string
	}{
		{"podman --log-level=info login reg.example",
			[]string{"podman login reg.example"}},
		{"podman --log-level info login reg.example",
			[]string{"podman login reg.example"}},
		{"podman --root /x run alpine",
			[]string{"podman run alpine"}},
		{"podman --log-level=info system reset --force",
			[]string{"podman system reset --force"}},
		{"podman -r -c conn --url=ssh://h ps",
			[]string{"podman ps"}},
		// pflag reads a boolean's value only when attached, so the next
		// word stays the subcommand.
		{"podman --remote=false ps", []string{"podman ps"}},
		{"podman --remote true", []string{"podman true"}},
		// A subcommand's own flag of the same name is not global.
		{"podman run --root /x alpine",
			[]string{"podman run --root /x alpine"}},
		{"podman --version", []string{"podman --version"}},
		{"podman --help", []string{"podman --help"}},
		{"podman -v", []string{"podman -v"}},
		{"docker -H tcp://h -D run alpine",
			[]string{"docker run alpine"}},
		{"docker --context=ctx --tlsverify ps", []string{"docker ps"}},
		{"docker --version", []string{"docker --version"}},
		// A path-invoked CLI keeps its own command for path patterns and
		// adds the stripped bare form for subcommand patterns.
		{"/usr/bin/podman --root /x login reg", []string{
			"podman login reg",
			"/usr/bin/podman --root /x login reg",
		}},
	}

	for _, test := range tests {
		t.Run(test.cmd, func(t *testing.T) {
			br, err := breakdownWithAllRules(t, test.cmd)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got := commandTexts(br)
			if !slices.Equal(got, test.want) {
				t.Errorf("commands = %q, want %q", got, test.want)
			}
		})
	}
}

func TestContainerGlobalOptionMissingArgumentDenied(t *testing.T) {
	for _, cmd := range []string{
		"podman --root",
		"podman --log-level",
		"docker -H",
	} {
		t.Run(cmd, func(t *testing.T) {
			_, err := breakdownWithAllRules(t, cmd)
			if err == nil {
				t.Fatal("want deny for an option missing its argument")
			}
			if !strings.Contains(err.Error(), "requires an argument") {
				t.Errorf("error %q does not name the missing argument",
					err)
			}
		})
	}
}
