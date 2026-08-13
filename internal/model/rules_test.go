package model

import (
	"testing"

	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// --- helpers ---

func ws(ss ...string) []*syntax.Word {
	return word.FromStrings(ss)
}

func flagNames(pf []ParsedFlag) []string {
	names := make([]string, len(pf))
	for i, f := range pf {
		names[i] = f.Name
	}
	return names
}

func flagValue(pf []ParsedFlag, name string) string {
	for _, f := range pf {
		if f.Name == name && f.Value != nil {
			return word.Text(f.Value)
		}
	}
	return ""
}

func TestWorkOutcomesRejectEmptyWork(t *testing.T) {
	constructors := []struct {
		name      string
		construct func()
	}{
		{"replace", func() { ReplaceOuter(BreakdownWork{}) }},
		{"keep", func() { KeepOuter(BreakdownWork{}) }},
	}

	for _, constructor := range constructors {
		t.Run(constructor.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("expected panic for empty work")
				}
			}()
			constructor.construct()
		})
	}
}

// --- PopulatePossibleFlags ---

func TestPopulatePossibleFlags(t *testing.T) {
	tests := []struct {
		name      string
		raw       []*syntax.Word
		wantNames []string
	}{
		{"no flags", ws("foo", "bar"),
			nil},
		{"short flag", ws("-v"),
			[]string{"-v"}},
		{"long flag", ws("--verbose"),
			[]string{"--verbose"}},
		{"mixed", ws("-v", "pos", "--output", "file"),
			[]string{"-v", "--output"}},
		{"stops at separator",
			ws("--flag", "--", "--not-a-flag"),
			[]string{"--flag"}},
		{"long with equals",
			ws("--method=POST"),
			[]string{"--method"}},
		{"bare dash not a flag", ws("-"),
			nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &ParseResult{Raw: tt.raw}
			PopulatePossibleFlags(input)
			got := flagNames(input.PossibleFlags)
			if len(got) != len(tt.wantNames) {
				t.Fatalf("got %v, want %v",
					got, tt.wantNames)
			}
			for i := range got {
				if got[i] != tt.wantNames[i] {
					t.Errorf(
						"flag[%d] = %q, want %q",
						i, got[i],
						tt.wantNames[i])
				}
			}
		})
	}
}

func TestPopulatePossibleFlagsValues(t *testing.T) {
	tests := []struct {
		name      string
		raw       []*syntax.Word
		flagName  string
		wantValue string
	}{
		// Long flag with = splits value.
		{"long equals",
			ws("--method=POST"),
			"--method", "POST"},
		// Long flag without = uses next arg.
		{"long next arg",
			ws("--output", "file.txt"),
			"--output", "file.txt"},
		// Short flag uses next arg as value.
		{"short next arg",
			ws("-o", "file.txt"),
			"-o", "file.txt"},
		// Last flag with no next arg — value is
		// nil, flagValue returns "".
		{"short no next arg",
			ws("-o"),
			"-o", ""},
		{"long no next arg",
			ws("--output"),
			"--output", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &ParseResult{Raw: tt.raw}
			PopulatePossibleFlags(input)
			got := flagValue(
				input.PossibleFlags, tt.flagName)
			if got != tt.wantValue {
				t.Errorf(
					"value of %s = %q, want %q",
					tt.flagName, got,
					tt.wantValue)
			}
		})
	}
}

func TestPopulatePossibleFlagsSkipsOpaque(t *testing.T) {
	// Opaque words (variable expansion) are skipped —
	// they can't be classified as flags.
	raw := []*syntax.Word{
		word.Lit("-v"),
		{Parts: []syntax.WordPart{
			&syntax.ParamExp{
				Param: &syntax.Lit{Value: "X"},
			},
		}},
		word.Lit("--output"),
	}
	input := &ParseResult{Raw: raw}
	PopulatePossibleFlags(input)
	names := flagNames(input.PossibleFlags)
	if len(names) != 2 ||
		names[0] != "-v" ||
		names[1] != "--output" {
		t.Errorf("got %v, want [-v --output]",
			names)
	}
}

func TestPopulatePossibleFlagsOpaqueEquals(
	t *testing.T,
) {
	// --flag=$VAR: the name is in the static prefix
	// before =, the value is opaque. SplitEq extracts
	// the name from the first Lit part.
	raw := []*syntax.Word{
		{Parts: []syntax.WordPart{
			&syntax.Lit{Value: "--method="},
			&syntax.ParamExp{
				Param: &syntax.Lit{Value: "M"},
			},
		}},
	}
	input := &ParseResult{Raw: raw}
	PopulatePossibleFlags(input)
	if len(input.PossibleFlags) != 1 {
		t.Fatalf("got %d flags, want 1",
			len(input.PossibleFlags))
	}
	f := input.PossibleFlags[0]
	if f.Name != "--method" {
		t.Errorf("name = %q, want --method",
			f.Name)
	}
	if f.Value == nil {
		t.Fatal("value is nil")
	}
	// Value should be opaque (contains ParamExp).
	if word.Static(f.Value) {
		t.Error("value should be opaque")
	}
}

func TestPopulatePossibleFlagsOpaqueNoEquals(
	t *testing.T,
) {
	// Fully opaque words like $FLAG are skipped — no
	// static prefix to extract a name from.
	raw := []*syntax.Word{
		{Parts: []syntax.WordPart{
			&syntax.ParamExp{
				Param: &syntax.Lit{Value: "FLAG"},
			},
		}},
	}
	input := &ParseResult{Raw: raw}
	PopulatePossibleFlags(input)
	if len(input.PossibleFlags) != 0 {
		t.Errorf("got %d flags, want 0",
			len(input.PossibleFlags))
	}
}
