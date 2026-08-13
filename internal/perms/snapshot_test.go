package perms

import (
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
