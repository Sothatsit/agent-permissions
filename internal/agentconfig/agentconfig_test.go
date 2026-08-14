package agentconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestParseRejectsUnknownNestedKey(t *testing.T) {
	for _, data := range []string{
		`{"Allow": {"Commandz": {}}}`,
		`{"Rules": {"git.branch-writes": {"Enable": true}}}`,
		`{"deny": {"Commands": {}}}`,
		`{"Deny": {"commands": {}}}`,
		`{"Rules": {"git.branch-writes": {"enabled": true}}}`,
	} {
		_, err := Parse("test.json", []byte(data))
		if err == nil || !strings.Contains(
			err.Error(), "unknown key",
		) {
			t.Errorf("expected unknown-field error, got %v", err)
		}
	}
}

func TestParseRejectsNullValues(t *testing.T) {
	tests := []struct {
		name  string
		data  string
		field string
	}{
		{"top level", `null`, "$"},
		{"tier", `{"Allow": null}`, `$["Allow"]`},
		{"axis", `{"Deny": {"Commands": null}}`,
			`$["Deny"]["Commands"]`},
		{"rules", `{"Rules": null}`, `$["Rules"]`},
		{
			"rule config",
			`{"Rules": {"git.branch-writes": null}}`,
			`$["Rules"]["git.branch-writes"]`,
		},
		{
			"rule field",
			`{"Rules": {"git.branch-writes": {"Enabled": null}}}`,
			`$["Rules"]["git.branch-writes"]["Enabled"]`,
		},
		{
			"preset selection",
			`{"enabled-presets": null}`,
			`$["enabled-presets"]`,
		},
		{
			"preset name",
			`{"disabled-presets": [null]}`,
			`$["disabled-presets"][0]`,
		},
		{
			"reason",
			`{"Allow": {"Commands": {"git status:*": null}}}`,
			`$["Allow"]["Commands"]["git status:*"]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("test.json", []byte(tt.data))
			if err == nil || !strings.Contains(
				err.Error(), tt.field,
			) {
				t.Errorf(
					"expected error naming %s, got %v",
					tt.field, err)
			}
		})
	}
}

func TestParseRejectsDuplicateKeys(t *testing.T) {
	tests := []struct {
		name string
		data string
		key  string
	}{
		{
			"top-level tier",
			`{"Deny": {}, "Deny": {"Commands": {}}}`,
			"Deny",
		},
		{
			"rule field",
			`{"Rules": {"git.branch-writes": {` +
				`"Enabled": true, "Enabled": false}}}`,
			"Enabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("test.json", []byte(tt.data))
			if err == nil ||
				!strings.Contains(err.Error(), "duplicate") ||
				!strings.Contains(err.Error(), tt.key) {
				t.Errorf(
					"expected duplicate %s error, got %v",
					tt.key, err)
			}
		})
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
