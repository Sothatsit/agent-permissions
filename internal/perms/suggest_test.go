package perms

import (
	"strings"
	"testing"

	"github.com/sothatsit/agent-permissions/internal/model"
)

// permsWith builds a Permissions with a single source
// named "test" so suggestion tests can construct policies
// without restating the Sources field every time.
func permsWith(src SourcePerms) *Permissions {
	src.Name = "test"
	return &Permissions{Sources: []SourcePerms{src}}
}

// --- buildPermissionPattern ---

func TestSuggestPatternUnknownTool(t *testing.T) {
	p := &Permissions{}
	got := p.buildPermissionPattern(
		[]string{"some-tool", "arg"})
	if got != "some-tool:*" {
		t.Errorf("got %q, want %q",
			got, "some-tool:*")
	}
}

func TestSuggestPatternBareCommand(t *testing.T) {
	p := &Permissions{}
	got := p.buildPermissionPattern([]string{"some-tool"})
	if got != "some-tool:*" {
		t.Errorf("got %q, want %q",
			got, "some-tool:*")
	}
}

func TestSuggestPatternKnownCommandUnknownSubcmd(
	t *testing.T,
) {
	// git is known (via rules registry), but git apply
	// is not in any pattern — should suggest
	// git apply:* not git:*.
	p := permsWith(SourcePerms{
		Allow: TierEntries{Commands: []Pattern{
			{Elements: []string{"git", "status"},
				Raw: "git status", Mode: MatchExact},
			{Elements: []string{"git", "diff"},
				Raw: "git diff *", Mode: MatchTrailing},
		}},
		Ask: TierEntries{Commands: []Pattern{
			{Elements: []string{"git", "push"},
				Raw: "git push *", Mode: MatchTrailing},
			{Elements: []string{"git", "commit"},
				Raw:  "git commit *",
				Mode: MatchTrailing},
		}},
	})
	p.Rules = map[string]*model.CommandRules{
		"git": {},
	}
	got := p.buildPermissionPattern(
		[]string{"git", "apply", "somefile.patch"})
	if got != "git apply:*" {
		t.Errorf("got %q, want %q",
			got, "git apply:*")
	}
}

func TestSuggestPatternFlagsStopPrefix(t *testing.T) {
	// rm -rf /tmp/junk — flag stops prefix at "rm".
	p := permsWith(SourcePerms{
		SoftAsk: TierEntries{Commands: []Pattern{
			{Elements: []string{"rm"},
				Raw: "rm:*", Mode: MatchPrefix},
		}},
	})
	got := p.buildPermissionPattern(
		[]string{"rm", "-rf", "/tmp/junk"})
	if got != "rm:*" {
		t.Errorf("got %q, want %q", got, "rm:*")
	}
}

func TestSuggestPatternPathStopsPrefix(t *testing.T) {
	// cat file.txt — "file.txt" has a dot, stops prefix.
	p := &Permissions{}
	got := p.buildPermissionPattern(
		[]string{"cat", "file.txt"})
	if got != "cat:*" {
		t.Errorf("got %q, want %q", got, "cat:*")
	}
}

func TestSuggestPatternSlashStopsPrefix(t *testing.T) {
	p := &Permissions{}
	got := p.buildPermissionPattern(
		[]string{"tool", "/tmp/output"})
	if got != "tool:*" {
		t.Errorf("got %q, want %q", got, "tool:*")
	}
}

func TestSuggestPatternCatchAllDoesNotExtend(
	t *testing.T,
) {
	// Bash(*) catch-all should not cause every prefix
	// to be "known".
	p := permsWith(SourcePerms{
		Allow: TierEntries{Commands: []Pattern{
			{Elements: nil, Raw: "*",
				Mode: MatchTrailing},
		}},
	})
	got := p.buildPermissionPattern(
		[]string{"some-tool", "arg"})
	if got != "some-tool:*" {
		t.Errorf("got %q, want %q",
			got, "some-tool:*")
	}
}

func TestSuggestPatternRulesRegistryOnly(t *testing.T) {
	// Command in rules registry but no patterns at
	// all — prefix depth 1 is known from registry.
	p := &Permissions{
		Rules: map[string]*model.CommandRules{
			"git": {},
		},
	}
	got := p.buildPermissionPattern(
		[]string{"git", "apply", "file"})
	if got != "git apply:*" {
		t.Errorf("got %q, want %q",
			got, "git apply:*")
	}
}

func TestSuggestPatternMaxTwoLevels(t *testing.T) {
	// Even if both levels are known, cap at 2.
	p := permsWith(SourcePerms{
		Ask: TierEntries{Commands: []Pattern{
			{Elements: []string{"npm", "run"},
				Raw:  "npm run *",
				Mode: MatchTrailing},
		}},
	})
	p.Rules = map[string]*model.CommandRules{
		"npm": {},
	}
	got := p.buildPermissionPattern(
		[]string{"npm", "run", "build"})
	if got != "npm run:*" {
		t.Errorf("got %q, want %q",
			got, "npm run:*")
	}
}

// --- looksLikeSubcommand ---

func TestLooksLikeSubcommand(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"apply", true},
		{"status", true},
		{"--flag", false},
		{"-rf", false},
		{"/tmp/path", false},
		{"file.txt", false},
		{"key=value", false},
		{"", false},
		{"sub-cmd", true},
		{"run", true},
	}
	for _, tc := range tests {
		got := looksLikeSubcommand(tc.input)
		if got != tc.want {
			t.Errorf(
				"looksLikeSubcommand(%q) = %v, "+
					"want %v",
				tc.input, got, tc.want)
		}
	}
}

// --- patternSharesPrefix ---

func TestPatternSharesPrefix(t *testing.T) {
	tests := []struct {
		name   string
		pat    Pattern
		prefix []string
		want   bool
	}{
		{
			"exact match depth 1",
			Pattern{Elements: []string{"git"}},
			[]string{"git"},
			true,
		},
		{
			"shares depth 1 of 2",
			Pattern{
				Elements: []string{"git", "push"}},
			[]string{"git"},
			true,
		},
		{
			"no match depth 1",
			Pattern{
				Elements: []string{"npm", "run"}},
			[]string{"git"},
			false,
		},
		{
			"shares depth 1 not depth 2",
			Pattern{
				Elements: []string{"git", "push"}},
			[]string{"git", "apply"},
			false,
		},
		{
			"catch-all skipped",
			Pattern{Elements: nil},
			[]string{"git"},
			false,
		},
	}
	for _, tc := range tests {
		got := patternSharesPrefix(
			tc.pat, tc.prefix)
		if got != tc.want {
			t.Errorf("%s: got %v, want %v",
				tc.name, got, tc.want)
		}
	}
}

// --- formatResult ---

// claudeHeader is the production Claude Code harness's
// unknown-command header. formatResult tests pass it
// directly to keep the unit tests focused on layout, not
// harness dispatch.
const claudeHeader = "Add to /permissions to auto-allow"

func TestFormatResultSingleAsk(t *testing.T) {
	got := formatResult(
		[]string{"git push *"}, nil, nil, nil,
		claudeHeader)
	if got == "" {
		t.Fatal("empty result")
	}
	assertContains(t, "Ask header", got, "Ask:")
	assertContains(t, "pattern", got, "git push *")
}

func TestFormatResultMultiAsk(t *testing.T) {
	got := formatResult(
		[]string{"git push *", "curl *"},
		nil, nil, nil, claudeHeader)
	assertContains(t, "git push", got, "git push *")
	assertContains(t, "curl", got, "curl *")
}

func TestFormatResultSingleUnknown(t *testing.T) {
	got := formatResult(
		nil, nil, []string{"Bash(tool:*)"}, nil,
		claudeHeader)
	assertContains(t, "singular",
		got, "Unknown command.")
	assertContains(t, "suggestion",
		got, "Bash(tool:*)")
	assertContains(t, "permissions",
		got, "/permissions")
}

func TestFormatResultMultiUnknown(t *testing.T) {
	got := formatResult(
		nil, nil,
		[]string{"Bash(a:*)", "Bash(b:*)"},
		nil, claudeHeader)
	assertContains(t, "plural",
		got, "Unknown commands.")
}

func TestFormatResultMixed(t *testing.T) {
	got := formatResult(
		[]string{"curl *"}, nil,
		[]string{"Bash(tool:*)"},
		nil, claudeHeader)
	assertContains(t, "Ask header", got, "Ask:")
	assertContains(t, "curl", got, "curl *")
	assertContains(t, "Unknown", got, "Unknown command.")
	assertContains(t, "suggestion", got, "Bash(tool:*)")
}

func TestFormatResultSoftAsk(t *testing.T) {
	got := formatResult(
		nil,
		[]string{"curl *  (from preset:network-fetch)"},
		nil, nil, claudeHeader)
	assertContains(t, "soft-ask header",
		got, "Soft-ask. To allow")
	assertContains(t, "curl", got, "curl *")
	assertContains(t, "source attribution",
		got, "preset:network-fetch")
}

func TestFormatResultMixedAskAndSoftAsk(
	t *testing.T,
) {
	got := formatResult(
		[]string{"git push *"},
		[]string{"curl *  (from preset:network-fetch)"},
		nil, nil, claudeHeader)
	assertContains(t, "Ask header", got, "Ask:")
	assertContains(t, "git push", got, "git push *")
	assertContains(t, "soft-ask header",
		got, "Soft-ask. To allow")
	assertContains(t, "curl", got, "curl *")
}

func TestFormatResultSingleDeny(t *testing.T) {
	got := formatResult(
		nil, nil, nil, []string{"ssh *"}, claudeHeader)
	assertContains(t, "Deny header", got, "Deny:")
	assertContains(t, "pattern", got, "ssh *")
}

func TestFormatResultMultiDeny(t *testing.T) {
	got := formatResult(
		nil, nil, nil,
		[]string{"ssh *", "sudo *"}, claudeHeader)
	assertContains(t, "ssh", got, "ssh *")
	assertContains(t, "sudo", got, "sudo *")
}

func assertContains(
	t *testing.T, label, haystack, needle string,
) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("%s: %q not found in %q",
			label, needle, haystack)
	}
}
