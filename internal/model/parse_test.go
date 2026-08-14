package model

import (
	"strings"
	"testing"

	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// --- FullParser ---

func TestFullParser(t *testing.T) {
	p := NewFullParser([]FlagDef{
		{Name: "--verbose"},
		{Name: "--output", Arg: true},
		{Name: "-v"},
		{Name: "-o", Arg: true},
	}, InterspersedFlags, "")

	result, err := p.Parse(word.FromStrings(
		[]string{
			"-v", "--output", "file.txt", "pos1",
			"--", "--not-a-flag",
		}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Flags) != 2 {
		t.Fatalf("got %d flags, want 2",
			len(result.Flags))
	}

	if result.Flags[0].Name != "-v" {
		t.Errorf("flag 0: %+v", result.Flags[0])
	}

	if result.Flags[1].Name != "--output" ||
		word.Text(result.Flags[1].Value) !=
			"file.txt" {
		t.Errorf("flag 1: name=%s value=%v",
			result.Flags[1].Name,
			result.Flags[1].Value)
	}

	if len(result.Positionals) != 2 ||
		word.Text(result.Positionals[0]) !=
			"pos1" ||
		word.Text(result.Positionals[1]) !=
			"--not-a-flag" {
		t.Errorf("positionals: %v",
			result.Positionals)
	}
}

func TestFullParserUnknownFlag(t *testing.T) {
	p := NewFullParser([]FlagDef{
		{Name: "-v"},
	}, InterspersedFlags, "custom reason")

	_, err := p.Parse(word.FromStrings(
		[]string{"-v", "--unknown"}))
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}

	want := "custom reason: --unknown"
	if got := err.Error(); got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

func TestFullParserLongEquals(t *testing.T) {
	p := NewFullParser([]FlagDef{
		{Name: "--method", Arg: true},
	}, InterspersedFlags, "")

	result, err := p.Parse(word.FromStrings(
		[]string{"--method=POST"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Flags) != 1 {
		t.Fatalf("got %d flags", len(result.Flags))
	}

	if result.Flags[0].Name != "--method" ||
		word.Text(result.Flags[0].Value) !=
			"POST" {
		t.Errorf("flag: name=%s value=%v",
			result.Flags[0].Name,
			result.Flags[0].Value)
	}
}

func TestFullParserPrefixWithValue(t *testing.T) {
	p := NewFullParser([]FlagDef{
		{Name: "-n", Prefix: true},
	}, InterspersedFlags, "")

	result, err := p.Parse(word.FromStrings(
		[]string{"-n5"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Flags) != 1 {
		t.Fatalf("got %d flags", len(result.Flags))
	}

	if result.Flags[0].Name != "-n" ||
		word.Text(result.Flags[0].Value) != "5" {
		t.Errorf("flag: name=%s value=%v",
			result.Flags[0].Name,
			result.Flags[0].Value)
	}
}

func TestFullParserPrefixBare(t *testing.T) {
	p := NewFullParser([]FlagDef{
		{Name: "-n", Prefix: true},
	}, InterspersedFlags, "")

	result, err := p.Parse(word.FromStrings(
		[]string{"-n"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Flags) != 1 {
		t.Fatalf("got %d flags", len(result.Flags))
	}

	if result.Flags[0].Name != "-n" ||
		result.Flags[0].Value != nil {
		t.Errorf("flag: name=%s value=%v",
			result.Flags[0].Name,
			result.Flags[0].Value)
	}
}

func TestFullParserPrefixUnknown(t *testing.T) {
	p := NewFullParser([]FlagDef{
		{Name: "-n", Prefix: true},
	}, InterspersedFlags, "")

	_, err := p.Parse(word.FromStrings(
		[]string{"-x5"}))
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

// --- FullParser cluster splitting ---

func TestFullParserClusterBooleans(t *testing.T) {
	p := NewFullParser([]FlagDef{
		{Name: "-a"}, {Name: "-b"}, {Name: "-c"},
	}, InterspersedFlags, "")
	result, err := p.Parse(word.FromStrings(
		[]string{"-abc"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Flags) != 3 {
		t.Fatalf("got %d flags, want 3",
			len(result.Flags))
	}

	want := []string{"-a", "-b", "-c"}
	for i, w := range want {
		if result.Flags[i].Name != w {
			t.Errorf("flag %d = %s, want %s",
				i, result.Flags[i].Name, w)
		}
	}
}

func TestFullParserClusterArgMid(t *testing.T) {
	p := NewFullParser([]FlagDef{
		{Name: "-a"},
		{Name: "-c", Arg: true},
	}, InterspersedFlags, "")
	result, err := p.Parse(word.FromStrings(
		[]string{"-acvalue"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Flags) != 2 {
		t.Fatalf("got %d flags, want 2",
			len(result.Flags))
	}

	if result.Flags[0].Name != "-a" {
		t.Errorf("flag 0 = %s, want -a",
			result.Flags[0].Name)
	}

	if result.Flags[1].Name != "-c" ||
		word.Text(result.Flags[1].Value) !=
			"value" {
		t.Errorf("flag 1: name=%s value=%s",
			result.Flags[1].Name,
			word.Text(result.Flags[1].Value))
	}
}

func TestFullParserClusterArgEnd(t *testing.T) {
	p := NewFullParser([]FlagDef{
		{Name: "-a"},
		{Name: "-c", Arg: true},
	}, InterspersedFlags, "")
	result, err := p.Parse(word.FromStrings(
		[]string{"-ac", "value"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Flags) != 2 {
		t.Fatalf("got %d flags, want 2",
			len(result.Flags))
	}

	if result.Flags[1].Name != "-c" ||
		word.Text(result.Flags[1].Value) !=
			"value" {
		t.Errorf("flag 1: name=%s value=%s",
			result.Flags[1].Name,
			word.Text(result.Flags[1].Value))
	}
}

func TestFullParserClusterArgEndMissing(
	t *testing.T,
) {
	p := NewFullParser([]FlagDef{
		{Name: "-a"},
		{Name: "-c", Arg: true},
	}, InterspersedFlags, "")
	_, err := p.Parse(word.FromStrings(
		[]string{"-ac"}))
	if err == nil {
		t.Fatal("expected error for missing value")
	}
}

func TestFullParserSortsForGreedyClusterMatching(t *testing.T) {
	p := NewFullParser([]FlagDef{
		{Name: "-O"},
		{Name: "-u"},
		{Name: "-OO"},
	}, InterspersedFlags, "")
	result, err := p.Parse(word.FromStrings(
		[]string{"-OOu"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Flags) != 2 {
		t.Fatalf("got %d flags, want 2",
			len(result.Flags))
	}

	if result.Flags[0].Name != "-OO" {
		t.Errorf("flag 0 = %s, want -OO",
			result.Flags[0].Name)
	}

	if result.Flags[1].Name != "-u" {
		t.Errorf("flag 1 = %s, want -u",
			result.Flags[1].Name)
	}
}

func TestFullParserClusterPrefix(t *testing.T) {
	p := NewFullParser([]FlagDef{
		{Name: "-n", Prefix: true},
		{Name: "-v"},
	}, InterspersedFlags, "")
	result, err := p.Parse(word.FromStrings(
		[]string{"-vn5"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Flags) != 2 {
		t.Fatalf("got %d flags, want 2",
			len(result.Flags))
	}

	if result.Flags[0].Name != "-v" {
		t.Errorf("flag 0 = %s, want -v",
			result.Flags[0].Name)
	}

	if result.Flags[1].Name != "-n" ||
		word.Text(result.Flags[1].Value) != "5" {
		t.Errorf("flag 1: name=%s value=%s",
			result.Flags[1].Name,
			word.Text(result.Flags[1].Value))
	}
}

func TestFullParserClusterUnknown(t *testing.T) {
	p := NewFullParser([]FlagDef{
		{Name: "-a"},
	}, InterspersedFlags, "test reason")
	_, err := p.Parse(word.FromStrings(
		[]string{"-ax"}))
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}

	if !strings.Contains(err.Error(),
		"test reason") {
		t.Errorf("error = %q, want test reason",
			err.Error())
	}
}

func TestFullParserClusterNonStatic(t *testing.T) {
	p := NewFullParser([]FlagDef{
		{Name: "-a"}, {Name: "-b"},
	}, InterspersedFlags, "test reason")
	w := &syntax.Word{Parts: []syntax.WordPart{
		&syntax.Lit{Value: "-a"},
		&syntax.ParamExp{
			Param: &syntax.Lit{Value: "VAR"},
		},
	}}
	_, err := p.Parse([]*syntax.Word{w})
	if err == nil {
		t.Fatal(
			"expected error for non-static cluster")
	}
}

func TestFullParserFlagPlacement(t *testing.T) {
	for _, tt := range []struct {
		name            string
		placement       FlagPlacement
		wantFlagCount   int
		wantPositionals []string
	}{
		{
			name:            "interspersed flags",
			placement:       InterspersedFlags,
			wantFlagCount:   1,
			wantPositionals: []string{"script.py"},
		},
		{
			name:            "leading flags only",
			placement:       LeadingFlagsOnly,
			wantPositionals: []string{"script.py", "-v"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := NewFullParser(
				[]FlagDef{{Name: "-v"}},
				tt.placement,
				"",
			)

			result, err := p.Parse(word.FromStrings(
				[]string{"script.py", "-v"}))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(result.Flags) != tt.wantFlagCount {
				t.Errorf("got %d flags, want %d",
					len(result.Flags), tt.wantFlagCount)
			}

			if len(result.Positionals) !=
				len(tt.wantPositionals) {
				t.Fatalf("got %d positionals, want %d",
					len(result.Positionals),
					len(tt.wantPositionals))
			}

			for i, want := range tt.wantPositionals {
				if got := word.Text(
					result.Positionals[i]); got != want {
					t.Errorf(
						"positional %d = %q, want %q",
						i, got, want)
				}
			}
		})
	}
}

func TestFullParserLeadingFlagsOnly(t *testing.T) {
	p := NewFullParser([]FlagDef{
		{Name: "--verbose"},
		{Name: "-v"},
		{Name: "-u"},
	}, LeadingFlagsOnly, "")

	// After the first positional, flags belong to the positional (e.g.
	// script args after a file).
	result, err := p.Parse(word.FromStrings(
		[]string{
			"-v", "script.py", "run",
			"--test-name", "foo",
		}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Flags) != 1 ||
		result.Flags[0].Name != "-v" {
		t.Errorf("flags: %+v", result.Flags)
	}

	if len(result.Positionals) != 4 {
		t.Fatalf("got %d positionals, want 4",
			len(result.Positionals))
	}

	wantPos := []string{
		"script.py", "run", "--test-name", "foo",
	}
	for i, w := range wantPos {
		if word.Text(result.Positionals[i]) != w {
			t.Errorf("positional %d = %s, want %s",
				i,
				word.Text(result.Positionals[i]),
				w)
		}
	}
}

// --- FullParser Terminal flag ---

func TestFullParserTerminalStandalone(t *testing.T) {
	p := NewFullParser([]FlagDef{
		{Name: "-c", Arg: true, Terminal: true},
		{Name: "-v"},
	}, LeadingFlagsOnly, "")

	// After -c "code", remaining args should be treated as positionals, not
	// flags.
	result, err := p.Parse(word.FromStrings(
		[]string{
			"-v", "-c", "code", "--flag", "arg1",
		}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Flags) != 2 {
		t.Fatalf("got %d flags, want 2",
			len(result.Flags))
	}

	if result.Flags[0].Name != "-v" {
		t.Errorf("flag 0 = %s, want -v",
			result.Flags[0].Name)
	}

	if result.Flags[1].Name != "-c" ||
		word.Text(result.Flags[1].Value) !=
			"code" {
		t.Errorf("flag 1: name=%s value=%v",
			result.Flags[1].Name,
			result.Flags[1].Value)
	}

	if len(result.Positionals) != 2 {
		t.Fatalf("got %d positionals, want 2",
			len(result.Positionals))
	}

	wantPos := []string{"--flag", "arg1"}
	for i, w := range wantPos {
		if word.Text(result.Positionals[i]) != w {
			t.Errorf("positional %d = %s, want %s",
				i,
				word.Text(result.Positionals[i]),
				w)
		}
	}
}

func TestFullParserTerminalCluster(t *testing.T) {
	p := NewFullParser([]FlagDef{
		{Name: "-c", Arg: true, Terminal: true},
		{Name: "-B"},
	}, LeadingFlagsOnly, "")

	// -Bc is a cluster where -c is terminal. After its value, remaining
	// args are positionals.
	result, err := p.Parse(word.FromStrings(
		[]string{"-Bc", "code", "--flag"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Flags) != 2 {
		t.Fatalf("got %d flags, want 2",
			len(result.Flags))
	}

	if result.Flags[0].Name != "-B" {
		t.Errorf("flag 0 = %s, want -B",
			result.Flags[0].Name)
	}

	if result.Flags[1].Name != "-c" ||
		word.Text(result.Flags[1].Value) !=
			"code" {
		t.Errorf("flag 1: name=%s value=%v",
			result.Flags[1].Name,
			result.Flags[1].Value)
	}

	if len(result.Positionals) != 1 ||
		word.Text(result.Positionals[0]) !=
			"--flag" {
		t.Errorf("positionals: %v",
			result.Positionals)
	}
}

func TestFullParserOwnsFlagDefinitions(t *testing.T) {
	flags := []FlagDef{
		{Name: "-ab"},
		{Name: "-c"},
	}
	p := NewFullParser(flags, InterspersedFlags, "")
	flags[0] = FlagDef{Name: "-xy"}

	result, err := p.Parse(word.FromStrings(
		[]string{"-abc"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Flags) != 2 {
		t.Fatalf("got %d flags, want 2",
			len(result.Flags))
	}

	if result.Flags[0].Name != "-ab" ||
		result.Flags[1].Name != "-c" {
		t.Errorf("flags = %+v, want -ab, -c",
			result.Flags)
	}
}

func TestNewFullParserRejectsDuplicateFlags(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error(
				"expected panic for duplicate flags")
		}
	}()

	NewFullParser([]FlagDef{
		{Name: "-a"},
		{Name: "-a", Arg: true},
	}, InterspersedFlags, "")
}
