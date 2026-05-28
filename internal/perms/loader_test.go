package perms

import (
	"testing"

	"github.com/sothatsit/agent-permissions/internal/agentconfig"
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
	got := SelectPresets(nil, nil)
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
	got := presetNames(SelectPresets(c, nil))
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
	got := presetNames(SelectPresets(c, nil))
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
	got := presetNames(SelectPresets(c, nil))
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
	got := presetNames(SelectPresets(global, project))
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
	got := presetNames(SelectPresets(global, project))
	if len(got) != 1 || !contains(got, "git") {
		t.Errorf(
			"expected global selection (git), got %v",
			got)
	}
}
