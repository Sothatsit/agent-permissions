package perms

import (
	"strings"
	"testing"
)

// parseEntry is the canonical entry point: extract a raw pattern from
// a Bash(...) wrapped Claude Code entry (or pass through a plain
// agent-permissions entry) and run parsePattern.
func parseEntry(entry string) (Pattern, error) {
	if raw, ok := extractBashPattern(entry); ok {
		return parsePattern(raw)
	}

	return parsePattern(entry)
}

func TestParsePatternValid(t *testing.T) {
	cases := []struct {
		name      string
		entry     string
		wantElems []string
		wantMode  MatchMode
		wantRaw   string
	}{
		{
			name:      "exact command",
			entry:     "Bash(git status)",
			wantElems: []string{"git", "status"},
			wantMode:  MatchExact,
			wantRaw:   "git status",
		},
		{
			name:      "trailing wildcard",
			entry:     "Bash(git log *)",
			wantElems: []string{"git", "log"},
			wantMode:  MatchTrailing,
			wantRaw:   "git log *",
		},
		{
			name:      "prefix colon wildcard",
			entry:     "Bash(git commit:*)",
			wantElems: []string{"git", "commit"},
			wantMode:  MatchPrefix,
			wantRaw:   "git commit:*",
		},
		{
			name:      "bare Bash means match-all",
			entry:     "Bash",
			wantElems: nil,
			wantMode:  MatchTrailing,
			wantRaw:   "*",
		},
		{
			name:      "Bash(*) means match-all",
			entry:     "Bash(*)",
			wantElems: nil,
			wantMode:  MatchTrailing,
			wantRaw:   "*",
		},
		{
			name:      "plain (no Bash wrapper) exact",
			entry:     "git status",
			wantElems: []string{"git", "status"},
			wantMode:  MatchExact,
			wantRaw:   "git status",
		},
		{
			name:      "plain prefix",
			entry:     "git commit:*",
			wantElems: []string{"git", "commit"},
			wantMode:  MatchPrefix,
			wantRaw:   "git commit:*",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := parseEntry(tc.entry)
			if err != nil {
				t.Fatalf(
					"parseEntry %q failed: %v",
					tc.entry, err)
			}

			if p.Mode != tc.wantMode {
				t.Errorf("mode: got %v, want %v",
					p.Mode, tc.wantMode)
			}

			if p.Raw != tc.wantRaw {
				t.Errorf("raw: got %q, want %q",
					p.Raw, tc.wantRaw)
			}

			if len(p.Elements) != len(tc.wantElems) {
				t.Fatalf("elements: got %v, want %v",
					p.Elements, tc.wantElems)
			}

			for i := range p.Elements {
				if p.Elements[i] != tc.wantElems[i] {
					t.Errorf(
						"element %d: got %q, want %q",
						i, p.Elements[i],
						tc.wantElems[i])
				}
			}
		})
	}
}

func TestParsePatternRejectsDegenerate(t *testing.T) {
	cases := []struct {
		name       string
		entry      string
		wantReason string
	}{
		{"empty parens", "Bash()", "empty pattern"},
		{
			"whitespace only", "Bash(   )",
			"empty pattern",
		},
		{
			"bare colon wildcard", "Bash(:*)",
			"empty element",
		},
		{
			"leading empty element", "Bash( :*)",
			"empty element",
		},
		{
			"trailing empty element", "Bash(git :*)",
			"empty element",
		},
		{
			"plain pattern with empty element", ":*",
			"empty element",
		},
		{"plain pattern empty", "", "empty pattern"},
		{
			"plain pattern whitespace", "   ",
			"empty pattern",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseEntry(tc.entry)
			if err == nil {
				t.Errorf(
					"parseEntry %q should have failed",
					tc.entry)
				return
			}

			if !strings.Contains(
				err.Error(), tc.wantReason,
			) {
				t.Errorf(
					"reason: got %q, want substring %q",
					err.Error(), tc.wantReason)
			}
		})
	}
}
