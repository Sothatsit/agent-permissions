package perms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sothatsit/agent-permissions/internal/agentconfig"
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/presets"
)

func presetNames(ps []*presets.Preset) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

func writeClaudeSettings(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestLoadClaudeSettingsRejectsDuplicatePermission(t *testing.T) {
	path := writeClaudeSettings(t, `{
  "permissions": {
    "deny": ["Bash(ssh:*)"],
    "deny": []
  }
}`)

	_, _, err := loadClaudeSettings(path)
	if err == nil {
		t.Fatal("expected duplicate deny field to be rejected")
	}
	if !strings.Contains(err.Error(), "duplicate") ||
		!strings.Contains(err.Error(), "deny") {
		t.Fatalf("expected duplicate deny error, got %v", err)
	}
}

func TestLoadClaudeSettingsAllowsUnknownFieldsAndNull(t *testing.T) {
	path := writeClaudeSettings(t, `{
  "future": {"items": [null, {"large": 9007199254740993}]},
	"permissions": {
		"allow": [null],
		"ask": null,
		"deny": ["Bash(ssh:*)"],
		"future": null
	}
}`)

	src, warnings, err := loadClaudeSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(src.Allow.Commands) != 0 {
		t.Fatalf("null allow entry decoded as %v", src.Allow.Commands)
	}
	if len(src.Deny.Commands) != 1 ||
		src.Deny.Commands[0].Raw != "ssh:*" {
		t.Fatalf("deny commands = %v, want ssh:*", src.Deny.Commands)
	}
}

func TestSelectPresetsAllByDefault(t *testing.T) {
	got := selectPresets(presets.MustEmbedded(), nil, nil, nil)
	all := presets.MustEmbedded()
	if len(got) != len(all) {
		t.Errorf(
			"got %d presets, want %d (all)",
			len(got), len(all))
	}
}

func TestSelectPresetsEnabledWhitelist(t *testing.T) {
	enabled := []string{"git", "languages"}
	c := &agentconfig.Config{EnabledPresets: &enabled}
	got := presetNames(selectPresets(presets.MustEmbedded(), c, nil, nil))
	if len(got) != 2 ||
		!contains(got, "git") ||
		!contains(got, "languages") {
		t.Errorf(
			"got %v, want exactly [git languages]", got)
	}
}

func TestSelectPresetsDisabledBlacklist(t *testing.T) {
	disabled := []string{"git"}
	c := &agentconfig.Config{DisabledPresets: &disabled}
	got := presetNames(selectPresets(presets.MustEmbedded(), c, nil, nil))
	if contains(got, "git") {
		t.Errorf("expected git to be excluded; got %v", got)
	}
	// Should still include other presets.
	if !contains(got, "languages") {
		t.Errorf("expected languages preset; got %v", got)
	}
}

func TestSelectPresetsBothFields(t *testing.T) {
	// disabled-presets filters whatever enabled-presets
	// resolved to.
	enabled := []string{"git", "languages", "containers"}
	disabled := []string{"containers"}
	c := &agentconfig.Config{
		EnabledPresets:  &enabled,
		DisabledPresets: &disabled,
	}
	got := presetNames(selectPresets(presets.MustEmbedded(), c, nil, nil))
	if len(got) != 2 ||
		!contains(got, "git") ||
		!contains(got, "languages") {
		t.Errorf(
			"got %v, want exactly [git languages]", got)
	}
}

func TestSelectPresetsProjectOverridesGlobal(t *testing.T) {
	globalEnabled := []string{"git"}
	projectEnabled := []string{"languages"}
	global := &agentconfig.Config{
		EnabledPresets: &globalEnabled,
	}
	project := &agentconfig.Config{
		EnabledPresets: &projectEnabled,
	}
	got := presetNames(selectPresets(presets.MustEmbedded(), global, project, nil))
	if contains(got, "git") {
		t.Errorf(
			"project should have overridden global; "+
				"got %v", got)
	}
	if !contains(got, "languages") {
		t.Errorf(
			"project python should be selected; "+
				"got %v", got)
	}
}

func TestSelectPresetsLocalOverridesProject(t *testing.T) {
	// permissions.local.json is the most-specific source, so
	// its selection wins over both project and global.
	globalEnabled := []string{"git"}
	projectEnabled := []string{"languages"}
	localEnabled := []string{"containers"}
	global := &agentconfig.Config{
		EnabledPresets: &globalEnabled,
	}
	project := &agentconfig.Config{
		EnabledPresets: &projectEnabled,
	}
	local := &agentconfig.Config{
		EnabledPresets: &localEnabled,
	}
	got := presetNames(selectPresets(
		presets.MustEmbedded(), global, project, local))
	if len(got) != 1 || !contains(got, "containers") {
		t.Errorf(
			"local should have won; got %v", got)
	}
}

func TestSelectPresetsLocalFallthroughWhenSilent(t *testing.T) {
	// Local present but with no preset selection — defer to
	// project.
	projectEnabled := []string{"languages"}
	project := &agentconfig.Config{
		EnabledPresets: &projectEnabled,
	}
	local := &agentconfig.Config{
		Allow: agentconfig.TierEntries{
			Commands: map[string]string{"some-cmd:*": ""},
		},
	}
	got := presetNames(selectPresets(presets.MustEmbedded(), nil, project, local))
	if len(got) != 1 || !contains(got, "languages") {
		t.Errorf(
			"expected project selection (languages), got %v",
			got)
	}
}

func TestSelectPresetsProjectFallthroughWhenSilent(t *testing.T) {
	// Project doesn't specify either field — fall back to
	// global.
	globalEnabled := []string{"git"}
	global := &agentconfig.Config{
		EnabledPresets: &globalEnabled,
	}
	project := &agentconfig.Config{
		Allow: agentconfig.TierEntries{
			Commands: map[string]string{
				"some-cmd:*": "",
			},
		},
	}
	got := presetNames(selectPresets(presets.MustEmbedded(), global, project, nil))
	if len(got) != 1 || !contains(got, "git") {
		t.Errorf(
			"expected global selection (git), got %v",
			got)
	}
}

func TestSelectPresetsIncludesExternal(t *testing.T) {
	// External presets are part of the pool and are
	// selected (and disabled) by name exactly like
	// embedded ones.
	pool := append(
		[]*presets.Preset{{Name: "dug-test"}},
		presets.MustEmbedded()...)

	got := presetNames(selectPresets(pool, nil, nil, nil))
	if !contains(got, "dug-test") {
		t.Errorf(
			"external preset missing from default "+
				"selection: %v", got)
	}

	disabled := []string{"dug-test"}
	c := &agentconfig.Config{
		DisabledPresets: &disabled,
	}
	got = presetNames(selectPresets(pool, c, nil, nil))
	if contains(got, "dug-test") {
		t.Errorf(
			"disabled-presets should drop the external "+
				"preset: %v", got)
	}
}

func TestSelectPresetsCannotDisableEnforced(t *testing.T) {
	pool := append(
		[]*presets.Preset{{
			Name: "dug-enforced", Enforced: true,
		}}, presets.MustEmbedded()...)
	disabled := []string{"dug-enforced"}
	enabled := []string{"git"}
	c := &agentconfig.Config{
		EnabledPresets:  &enabled,
		DisabledPresets: &disabled,
	}
	got := presetNames(selectPresets(pool, c, nil, nil))
	if !contains(got, "dug-enforced") {
		t.Errorf("enforced preset was disabled: %v", got)
	}
	if !contains(got, "git") {
		t.Errorf("ordinary selection was not retained: %v", got)
	}
}

func TestValidateExternalPresetsRejectsMalformedPatterns(
	t *testing.T,
) {
	p := &presets.Preset{
		Name: "dug-bad",
		Dir:  "/site/presets",
		Deny: presets.TierEntries{
			Commands: map[string]string{"scancel :*": "x"},
			EnvVars:  map[string]string{"BAD-NAME": "x"},
		},
	}
	err := validateExternalPresets([]*presets.Preset{p})
	if err == nil {
		t.Fatal("expected malformed external preset error")
	}
	for _, want := range []string{
		"scancel :*", "BAD-NAME", "/site/presets",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestValidateExternalPresetsRejectsUnknownRule(
	t *testing.T,
) {
	p := &presets.Preset{
		Name: "dug-bad",
		Dir:  "/site/presets",
		Rules: map[string]model.RuleConfig{
			"git.branch-writs": {Enabled: true},
		},
	}
	err := validateExternalPresets([]*presets.Preset{p})
	if err == nil || !strings.Contains(
		err.Error(), "git.branch-writs",
	) {
		t.Fatalf("expected unknown rule error, got %v", err)
	}
}

func TestValidateExternalPresetsRejectsEnforcedRuleOff(
	t *testing.T,
) {
	p := &presets.Preset{
		Name:     "dug-bad",
		Dir:      "/site/presets",
		Enforced: true,
		Rules: map[string]model.RuleConfig{
			"git.branch-writes": {Enabled: false},
		},
	}
	err := validateExternalPresets([]*presets.Preset{p})
	if err == nil || !strings.Contains(
		err.Error(), "must have Enabled true",
	) {
		t.Fatalf("expected enforced rule error, got %v", err)
	}
}

func TestValidateExternalPresetsRejectsRuleOwnedPatterns(
	t *testing.T,
) {
	tests := []struct {
		name    string
		pattern string
		owner   string
	}{
		{"whole root", "timeout:*", "timeout"},
		{"whole root path", "/usr/bin/timeout:*", "timeout"},
		{"owned exact", "git remote", "git remote"},
		{"owned prefix", "git remote:*", "git remote"},
		{"owned descendant", "git remote add:*", "git remote"},
		{"normalised prefix", "git -C /repo remote remove:*", "git remote"},
		{"repeated normalised prefix", "git -C /a -C /b branch:*", "git branch"},
		{"globbed normalised option", "git -* /repo tag:*", "git tag"},
		{"trailing normalised prefix", "git -C *", "git branch"},
		{"root prefix", "git:*", "git branch"},
		{"trailing root", "git *", "git branch"},
		{"argument glob", "git rem*:*", "git remote"},
		{"command glob", "*git rem*:*", "git remote"},
		{"catch all", "*", ""},
		{"wildcard path", "/opt/*/git remote:*", "git remote"},
		{"other command", "gh api:*", "gh api"},
		{"path-allow bare root", "env:*", "env"},
		{"sed language wrapper", "sed:*", "sed"},
		{"awk language wrapper", "awk:*", "awk"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &presets.Preset{
				Name: "dug-bad",
				Dir:  "/site/presets",
				Allow: presets.TierEntries{
					Commands: map[string]string{
						tt.pattern: "x",
					},
				},
			}
			err := validateExternalPresets(
				[]*presets.Preset{p})
			if err == nil {
				t.Fatal("expected rule-owned pattern error")
			}
			wants := []string{tt.pattern}
			if tt.owner != "" {
				wants = append(wants, tt.owner)
			}
			for _, want := range wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err, want)
				}
			}
		})
	}
}

func TestValidateExternalPresetsAllowsPatternLayerCommands(
	t *testing.T,
) {
	patterns := []string{
		"git",
		"git status:*",
		"/usr/bin/git -C /repo remote remove:*",
		"gh pr:*",
		"tar:*",
		"find:*",
		"python3 --version",
		"/d/sw/dug/ai-tools/*/.venv/bin/python3:*",
		"/usr/bin/env:*",
		"*/env:*",
	}
	for _, pattern := range patterns {
		t.Run(pattern, func(t *testing.T) {
			p := &presets.Preset{
				Name: "dug-valid",
				Dir:  "/site/presets",
				Allow: presets.TierEntries{
					Commands: map[string]string{pattern: "x"},
				},
			}
			if err := validateExternalPresets(
				[]*presets.Preset{p},
			); err != nil {
				t.Fatalf("valid pattern rejected: %v", err)
			}
		})
	}
}

func TestValidateExternalPresetsAllowsUserWarnings(
	t *testing.T,
) {
	c := &agentconfig.Config{
		Allow: agentconfig.TierEntries{
			Commands: map[string]string{":*": ""},
		},
	}
	_, warnings := fromAgentConfig("user", c)
	if len(warnings) != 1 {
		t.Fatalf("expected one user warning, got %v", warnings)
	}
}
