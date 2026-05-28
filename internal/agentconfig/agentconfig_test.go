package agentconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadMissingReturnsNil(t *testing.T) {
	c, err := Load("/no/such/file")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if c != nil {
		t.Errorf("expected nil config for missing file")
	}
}

func TestParseBasicTiers(t *testing.T) {
	data := []byte(`{
        "Allow": {
            "Commands": {"a:*": "", "b:*": "why-b"}
        },
        "Deny": {
            "Commands": {"bad:*": ""},
            "EnvVars": {"BASH_ENV": "shell startup"}
        }
    }`)
	c, err := Parse("test.json", data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(
		c.Allow.Commands,
		map[string]string{"a:*": "", "b:*": "why-b"},
	) {
		t.Errorf("Allow.Commands: got %v",
			c.Allow.Commands)
	}
	if !reflect.DeepEqual(
		c.Deny.Commands,
		map[string]string{"bad:*": ""},
	) {
		t.Errorf("Deny.Commands: got %v",
			c.Deny.Commands)
	}
	if !reflect.DeepEqual(
		c.Deny.EnvVars,
		map[string]string{"BASH_ENV": "shell startup"},
	) {
		t.Errorf("Deny.EnvVars: got %v",
			c.Deny.EnvVars)
	}
	if c.HasPresetSelection() {
		t.Errorf("expected no preset selection")
	}
}

func TestParseRejectsLegacyArrayShape(t *testing.T) {
	data := []byte(`{"Allow": ["a:*"]}`)
	_, err := Parse("test.json", data)
	if err == nil {
		t.Fatal("expected error for array shape")
	}
}

func TestParseEnabledPresets(t *testing.T) {
	data := []byte(`{
        "enabled-presets": ["git", "python"]
    }`)
	c, err := Parse("test.json", data)
	if err != nil {
		t.Fatal(err)
	}
	if c.EnabledPresets == nil {
		t.Fatal("EnabledPresets is nil")
	}
	if !reflect.DeepEqual(
		*c.EnabledPresets, []string{"git", "python"},
	) {
		t.Errorf("got %v", *c.EnabledPresets)
	}
	if !c.HasPresetSelection() {
		t.Errorf("expected HasPresetSelection true")
	}
}

func TestParseDisabledPresetsEmpty(t *testing.T) {
	// Empty explicit list is meaningful (no disables).
	data := []byte(`{"disabled-presets": []}`)
	c, err := Parse("test.json", data)
	if err != nil {
		t.Fatal(err)
	}
	if c.DisabledPresets == nil {
		t.Fatal("DisabledPresets is nil; expected []")
	}
	if !c.HasPresetSelection() {
		t.Errorf(
			"expected HasPresetSelection true even " +
				"for empty list")
	}
}

func TestParseRejectsUnknownKey(t *testing.T) {
	data := []byte(`{"bogus": []}`)
	_, err := Parse("test.json", data)
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "perms.json")
	enabled := []string{"git", "languages"}
	c := &Config{
		Path: path,
		Allow: TierEntries{
			Commands: map[string]string{
				"a:*": "test",
			},
		},
		EnabledPresets: &enabled,
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(
		loaded.Allow.Commands, c.Allow.Commands,
	) {
		t.Errorf("Allow.Commands: got %v, want %v",
			loaded.Allow.Commands, c.Allow.Commands)
	}
	if !reflect.DeepEqual(
		*loaded.EnabledPresets, *c.EnabledPresets,
	) {
		t.Errorf("EnabledPresets roundtrip failed")
	}
}
