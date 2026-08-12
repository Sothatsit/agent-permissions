package perms

import (
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

func TestSelectPresetsAllByDefault(t *testing.T) {
	got := SelectPresets(presets.MustEmbedded(), nil, nil, nil)
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
	got := presetNames(SelectPresets(presets.MustEmbedded(), c, nil, nil))
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
	got := presetNames(SelectPresets(presets.MustEmbedded(), c, nil, nil))
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
	got := presetNames(SelectPresets(presets.MustEmbedded(), c, nil, nil))
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
	got := presetNames(SelectPresets(presets.MustEmbedded(), global, project, nil))
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
	got := presetNames(SelectPresets(
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
	got := presetNames(SelectPresets(presets.MustEmbedded(), nil, project, local))
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
	got := presetNames(SelectPresets(presets.MustEmbedded(), global, project, nil))
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

	got := presetNames(SelectPresets(pool, nil, nil, nil))
	if !contains(got, "dug-test") {
		t.Errorf(
			"external preset missing from default "+
				"selection: %v", got)
	}

	disabled := []string{"dug-test"}
	c := &agentconfig.Config{
		DisabledPresets: &disabled,
	}
	got = presetNames(SelectPresets(pool, c, nil, nil))
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
	got := presetNames(SelectPresets(pool, c, nil, nil))
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
	err := ValidateExternalPresets([]*presets.Preset{p})
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
	err := ValidateExternalPresets([]*presets.Preset{p})
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
	err := ValidateExternalPresets([]*presets.Preset{p})
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
		{"catch all", "*", "bash"},
		{"wildcard path", "/opt/*/git remote:*", "git remote"},
		{"other command", "gh api:*", "gh api"},
		{"path-allow bare root", "env:*", "env"},
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
			err := ValidateExternalPresets(
				[]*presets.Preset{p})
			if err == nil {
				t.Fatal("expected rule-owned pattern error")
			}
			for _, want := range []string{tt.pattern, tt.owner} {
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
			if err := ValidateExternalPresets(
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
