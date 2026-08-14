package perms

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
	"github.com/sothatsit/agent-permissions/presets"
)

func TestLoadPresetCatalogRejectsInvalidExternalPreset(t *testing.T) {
	dir := t.TempDir()
	writeSnapshotTestFile(t, filepath.Join(dir, "invalid.json"),
		`{"Deny":{"Commands":{"bad :*":"invalid"}}}`)
	t.Setenv(presets.PresetDirsEnv, dir)
	t.Setenv(presets.EnforcedPresetDirsEnv, "")
	t.Setenv(presets.EnforcedPresetsEnv, "")

	_, err := LoadPresetCatalog()
	if err == nil {
		t.Fatal("expected semantic validation error")
	}
	if !strings.Contains(err.Error(), "bad :*") {
		t.Fatalf("error does not name invalid pattern: %v", err)
	}
}

func TestResolveReturnsEvaluationReadyPermissions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(presets.PresetDirsEnv, "")
	t.Setenv(presets.EnforcedPresetDirsEnv, "")
	t.Setenv(presets.EnforcedPresetsEnv, "")

	resolved, err := Resolve("", "")
	if err != nil {
		t.Fatalf("resolve permissions: %v", err)
	}

	breakdownResult, err := resolved.Breakdown(
		"tar --to-command=sh -cf a.tar x")
	if err != nil {
		t.Fatalf("break down command: %v", err)
	}

	result := resolved.Permissions.Check(breakdownResult)
	if result.Decision != model.Deny {
		t.Fatalf("got %v, want deny", result.Decision)
	}
	if !strings.Contains(
		result.Reason, "(from rule:tar.command-execution)",
	) {
		t.Fatalf("denial does not name tar rule: %q", result.Reason)
	}
}

func TestPolicySnapshotPresetSelectionStates(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	externalDir := filepath.Join(root, "external")
	enforcedDir := filepath.Join(root, "enforced")
	agentPath := filepath.Join(
		project, ".agents", "permissions.json")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create home: %v", err)
	}

	writeSnapshotTestFile(t, filepath.Join(
		enforcedDir, "shared.json"), presetWithCommand(
		"state-enforced:*"))
	writeSnapshotTestFile(t, filepath.Join(
		externalDir, "disabled.json"), presetWithCommand(
		"state-disabled:*"))
	writeSnapshotTestFile(t, filepath.Join(
		externalDir, "named.json"), presetWithCommand(
		"state-named:*"))
	writeSnapshotTestFile(t, filepath.Join(
		externalDir, "omitted.json"), presetWithCommand(
		"state-omitted:*"))
	writeSnapshotTestFile(t, filepath.Join(
		externalDir, "promoted.json"), presetWithCommand(
		"state-promoted:*"))
	writeSnapshotTestFile(t, filepath.Join(
		externalDir, "shared.json"), presetWithCommand(
		"state-shared:*"))
	writeSnapshotTestFile(t, agentPath, `{
  "enabled-presets": ["disabled", "named", "shared"],
  "disabled-presets": ["disabled", "shared"]
}`)

	t.Setenv("HOME", home)
	t.Setenv(presets.PresetDirsEnv, externalDir)
	t.Setenv(presets.EnforcedPresetDirsEnv, enforcedDir)
	t.Setenv(presets.EnforcedPresetsEnv, "promoted")

	snapshot, err := LoadPolicySnapshot(project)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}

	selection := snapshot.PresetSelection()
	if selection.SelectorPath != agentPath {
		t.Fatalf(
			"selector path = %q, want %q",
			selection.SelectorPath, agentPath)
	}

	want := []struct {
		name   string
		dir    string
		state  PresetState
		active bool
	}{
		{"shared", enforcedDir, PresetEnforced, true},
		{"promoted", externalDir, PresetEnforced, true},
		{"disabled", externalDir, PresetDisabledByName, false},
		{"named", externalDir, PresetEnabledByName, true},
		{"omitted", externalDir, PresetDisabledByOmission, false},
		{"shared", externalDir, PresetDisabledByName, false},
	}
	for i, expected := range want {
		got := selection.Presets[i]
		if got.Preset.Name != expected.name ||
			got.Preset.Dir != expected.dir ||
			got.State != expected.state ||
			got.Active() != expected.active {
			t.Errorf(
				"preset %d = {%q %q %d %t}, want {%q %q %d %t}",
				i, got.Preset.Name, got.Preset.Dir,
				got.State, got.Active(), expected.name,
				expected.dir, expected.state, expected.active)
		}
	}

	resolved, err := snapshot.Resolve("")
	if err != nil {
		t.Fatalf("resolve snapshot: %v", err)
	}

	assertSnapshotDecision(t, resolved, "state-enforced", model.Allow)
	assertSnapshotDecision(t, resolved, "state-promoted", model.Allow)
	assertSnapshotDecision(t, resolved, "state-named", model.Allow)
	assertSnapshotDoesNotAllow(t, resolved, "state-disabled")
	assertSnapshotDoesNotAllow(t, resolved, "state-omitted")
	assertSnapshotDoesNotAllow(t, resolved, "state-shared")
}

func TestPolicySnapshotPresetSelectorPrecedence(t *testing.T) {
	tests := []struct {
		name        string
		global      string
		project     string
		local       string
		selector    AgentConfigScope
		hasSelector bool
		preset      string
		state       PresetState
		omitted     string
	}{
		{
			name:   "all by default",
			preset: "git",
			state:  PresetEnabledByDefault,
		},
		{
			name:        "empty disabled list owns selection",
			global:      `{"disabled-presets": []}`,
			selector:    AgentConfigGlobal,
			hasSelector: true,
			preset:      "git",
			state:       PresetEnabledByDefault,
		},
		{
			name:        "empty enabled list disables by omission",
			global:      `{"enabled-presets": []}`,
			selector:    AgentConfigGlobal,
			hasSelector: true,
			preset:      "git",
			state:       PresetDisabledByOmission,
		},
		{
			name:        "silent local defers to project",
			global:      `{"enabled-presets": ["git"]}`,
			project:     `{"enabled-presets": ["languages"]}`,
			local:       `{}`,
			selector:    AgentConfigProject,
			hasSelector: true,
			preset:      "languages",
			state:       PresetEnabledByName,
			omitted:     "git",
		},
		{
			name:        "local overrides project and global",
			global:      `{"enabled-presets": ["git"]}`,
			project:     `{"enabled-presets": ["languages"]}`,
			local:       `{"enabled-presets": ["containers"]}`,
			selector:    AgentConfigLocal,
			hasSelector: true,
			preset:      "containers",
			state:       PresetEnabledByName,
			omitted:     "languages",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			project := filepath.Join(root, "project")
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatalf("create home: %v", err)
			}

			paths := map[AgentConfigScope]string{
				AgentConfigGlobal: filepath.Join(
					home, ".agents", "permissions.json"),
				AgentConfigProject: filepath.Join(
					project, ".agents", "permissions.json"),
				AgentConfigLocal: filepath.Join(
					project, ".agents", "permissions.local.json"),
			}
			for scope, body := range map[AgentConfigScope]string{
				AgentConfigGlobal:  test.global,
				AgentConfigProject: test.project,
				AgentConfigLocal:   test.local,
			} {
				if body != "" {
					writeSnapshotTestFile(t, paths[scope], body)
				}
			}

			t.Setenv("HOME", home)
			t.Setenv(presets.PresetDirsEnv, "")
			t.Setenv(presets.EnforcedPresetDirsEnv, "")
			t.Setenv(presets.EnforcedPresetsEnv, "")

			snapshot, err := LoadPolicySnapshot(project)
			if err != nil {
				t.Fatalf("load snapshot: %v", err)
			}

			selection := snapshot.PresetSelection()
			wantPath := ""
			if test.hasSelector {
				wantPath = paths[test.selector]
			}

			if selection.SelectorPath != wantPath {
				t.Errorf(
					"selector path = %q, want %q",
					selection.SelectorPath, wantPath)
			}
			if got := policyPresetState(
				t, selection, test.preset,
			); got != test.state {
				t.Errorf(
					"%s state = %d, want %d",
					test.preset, got, test.state)
			}

			if test.omitted != "" {
				got := policyPresetState(t, selection, test.omitted)
				if got != PresetDisabledByOmission {
					t.Errorf("%s state = %d, want disabled by omission",
						test.omitted, got)
				}
			}
		})
	}
}

func TestPolicySnapshotRemainsStableAcrossFileChanges(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	presetDir := filepath.Join(root, "presets")
	presetPath := filepath.Join(presetDir, "snapshot.json")
	agentPath := filepath.Join(
		project, ".agents", "permissions.json")

	writeSnapshotTestFile(t, presetPath,
		`{"Allow":{"Commands":{"snapshot-preset-old:*":"old"}}}`)
	writeSnapshotTestFile(t, agentPath,
		`{"Allow":{"Commands":{"snapshot-agent-old:*":"old"}}}`)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create home: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv(presets.PresetDirsEnv, presetDir)
	t.Setenv(presets.EnforcedPresetDirsEnv, "")
	t.Setenv(presets.EnforcedPresetsEnv, "")

	captured, err := LoadPolicySnapshot(project)
	if err != nil {
		t.Fatalf("load captured snapshot: %v", err)
	}

	writeSnapshotTestFile(t, presetPath,
		`{"Allow":{"Commands":{"snapshot-preset-new:*":"new"}}}`)
	writeSnapshotTestFile(t, agentPath,
		`{"Allow":{"Commands":{"snapshot-agent-new:*":"new"}}}`)

	capturedPolicy, err := captured.Resolve("")
	if err != nil {
		t.Fatalf("resolve captured snapshot: %v", err)
	}

	fresh, err := LoadPolicySnapshot(project)
	if err != nil {
		t.Fatalf("load fresh snapshot: %v", err)
	}

	freshPolicy, err := fresh.Resolve("")
	if err != nil {
		t.Fatalf("resolve fresh snapshot: %v", err)
	}

	assertSnapshotDecision(t, capturedPolicy,
		"snapshot-preset-old", model.Allow)
	assertSnapshotDecision(t, capturedPolicy,
		"snapshot-agent-old", model.Allow)
	assertSnapshotDoesNotAllow(t, capturedPolicy,
		"snapshot-preset-new")
	assertSnapshotDoesNotAllow(t, capturedPolicy,
		"snapshot-agent-new")
	assertSnapshotDoesNotAllow(t, freshPolicy,
		"snapshot-preset-old")
	assertSnapshotDoesNotAllow(t, freshPolicy,
		"snapshot-agent-old")
	assertSnapshotDecision(t, freshPolicy,
		"snapshot-preset-new", model.Allow)
	assertSnapshotDecision(t, freshPolicy,
		"snapshot-agent-new", model.Allow)
}

func TestPolicySnapshotViewsCannotMutateCapturedPolicy(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	presetDir := filepath.Join(root, "presets")
	writeSnapshotTestFile(t, filepath.Join(presetDir, "snapshot.json"),
		`{"Allow":{"Commands":{"snapshot-preset:*":"old"}}}`)
	writeSnapshotTestFile(t, filepath.Join(
		project, ".agents", "permissions.json"),
		`{"Allow":{"Commands":{"snapshot-agent:*":"old"}}}`)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create home: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv(presets.PresetDirsEnv, presetDir)
	t.Setenv(presets.EnforcedPresetDirsEnv, "")
	t.Setenv(presets.EnforcedPresetsEnv, "")

	snapshot, err := LoadPolicySnapshot(project)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}

	for _, preset := range snapshot.Presets() {
		if preset.Name == "snapshot" {
			delete(preset.Allow.Commands, "snapshot-preset:*")
		}
	}

	selection := snapshot.PresetSelection()
	for i, policyPreset := range selection.Presets {
		if policyPreset.Preset.Name == "snapshot" {
			delete(policyPreset.Preset.Allow.Commands,
				"snapshot-preset:*")
			selection.Presets[i].State = PresetDisabledByName
		}
	}

	projectConfig := snapshot.AgentConfig(AgentConfigProject)
	delete(projectConfig.Config.Allow.Commands, "snapshot-agent:*")

	resolved, err := snapshot.Resolve("")
	if err != nil {
		t.Fatalf("resolve snapshot: %v", err)
	}

	assertSnapshotDecision(t, resolved,
		"snapshot-preset", model.Allow)
	assertSnapshotDecision(t, resolved,
		"snapshot-agent", model.Allow)
}

func TestPresetCatalogDoesNotShareEmbeddedCache(t *testing.T) {
	t.Setenv(presets.PresetDirsEnv, "")
	t.Setenv(presets.EnforcedPresetDirsEnv, "")
	t.Setenv(presets.EnforcedPresetsEnv, "")
	catalog, err := LoadPresetCatalog()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	embedded := presets.MustEmbedded()[0]
	const pattern = "snapshot-cache-mutation:*"
	embedded.Allow.Commands[pattern] = "mutation"
	t.Cleanup(func() {
		delete(embedded.Allow.Commands, pattern)
	})

	for _, preset := range catalog.Presets() {
		if _, ok := preset.Allow.Commands[pattern]; ok {
			t.Fatalf("catalog changed through embedded cache")
		}
	}
}

func writeSnapshotTestFile(
	t *testing.T, path, body string,
) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func presetWithCommand(pattern string) string {
	return fmt.Sprintf(
		`{"Allow":{"Commands":{%q:"test"}}}`, pattern)
}

func policyPresetState(
	t *testing.T,
	selection PresetSelection,
	name string,
) PresetState {
	t.Helper()
	for _, policyPreset := range selection.Presets {
		if policyPreset.Preset.Name == name {
			return policyPreset.State
		}
	}

	t.Fatalf("preset %q not found", name)
	return PresetEnabledByDefault
}

func assertSnapshotDecision(
	t *testing.T,
	resolved *Resolved,
	command string,
	want model.Decision,
) {
	t.Helper()
	got := snapshotDecision(resolved, command)
	if got != want {
		t.Errorf("%s: got %v, want %v", command, got, want)
	}
}

func assertSnapshotDoesNotAllow(
	t *testing.T,
	resolved *Resolved,
	command string,
) {
	t.Helper()
	decision := snapshotDecision(resolved, command)
	if decision == model.Allow {
		t.Errorf("%s: unexpectedly allowed", command)
	}
}

func snapshotDecision(
	resolved *Resolved,
	command string,
) model.Decision {
	return resolved.Permissions.Check(model.BreakdownResult{
		Commands: []model.Command{{
			Args: word.FromStrings([]string{command, "run"}),
		}},
	}).Decision
}
