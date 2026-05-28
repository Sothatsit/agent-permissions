package presets

import (
	"strings"
	"testing"
)

// TestEmbeddedParses ensures every shipped JSON file in
// presets/*.json embeds and parses cleanly. A parse error
// would mean the binary ships with broken data, so this
// is build-time-grade in spirit even though it runs as a
// unit test.
func TestEmbeddedParses(t *testing.T) {
	all, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded(): %v", err)
	}
	if len(all) == 0 {
		t.Fatal("Embedded() returned no presets")
	}
	for _, p := range all {
		if p.Name == "" {
			t.Errorf("preset missing name")
		}
		if strings.HasSuffix(p.Name, ".json") {
			t.Errorf(
				"preset name %q still has .json suffix",
				p.Name)
		}
	}
}

// TestByNameKnownPreset spot-checks lookup against a
// preset that's been shipped since day one.
func TestByNameKnownPreset(t *testing.T) {
	p := ByName("git")
	if p == nil {
		t.Fatal("ByName(\"git\") returned nil")
	}
	if len(p.Allow.Commands) == 0 {
		t.Error("git preset has no Allow.Commands entries")
	}
}

// TestByNameUnknownPreset returns nil for missing names.
func TestByNameUnknownPreset(t *testing.T) {
	if ByName("does-not-exist") != nil {
		t.Error("expected nil for unknown preset")
	}
}

func TestNamesAlphabetical(t *testing.T) {
	names := Names()
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf(
				"Names not sorted: %q before %q",
				names[i-1], names[i])
		}
	}
}
