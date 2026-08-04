package presets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePreset writes a preset JSON file into dir.
func writePreset(
	t *testing.T, dir, name, body string,
) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(
		path, []byte(body), 0o644,
	); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

const minimalPreset = `{
  "description": "test preset",
  "Allow": {"Commands": {"mytool:*": "site tool"}}
}`

func TestLoadDirsEmptyValue(t *testing.T) {
	got, err := LoadDirs("")
	if err != nil {
		t.Fatalf("LoadDirs(\"\"): %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestLoadDirsSingleDir(t *testing.T) {
	dir := t.TempDir()
	writePreset(t, dir, "dug-test.json", minimalPreset)
	got, err := LoadDirs(dir)
	if err != nil {
		t.Fatalf("LoadDirs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 preset, got %d", len(got))
	}
	p := got[0]
	if p.Name != "dug-test" {
		t.Errorf("name: got %q, want dug-test", p.Name)
	}
	if p.Dir != dir {
		t.Errorf("dir: got %q, want %q", p.Dir, dir)
	}
	if p.Allow.Commands["mytool:*"] != "site tool" {
		t.Errorf("allow entry missing: %v",
			p.Allow.Commands)
	}
}

func TestLoadDirsOrderAndTrailingColon(t *testing.T) {
	// Dirs load in list order; files sort by name within
	// each dir; empty list entries (trailing colon) skip.
	dirA := t.TempDir()
	dirB := t.TempDir()
	writePreset(t, dirA, "b.json", minimalPreset)
	writePreset(t, dirA, "a.json", minimalPreset)
	writePreset(t, dirB, "c.json", minimalPreset)
	got, err := LoadDirs(dirB + ":" + dirA + ":")
	if err != nil {
		t.Fatalf("LoadDirs: %v", err)
	}
	names := make([]string, len(got))
	for i, p := range got {
		names[i] = p.Name
	}
	want := "c a b"
	if strings.Join(names, " ") != want {
		t.Errorf("order: got %v, want %s", names, want)
	}
}

func TestLoadDirsSkipsNonJSON(t *testing.T) {
	dir := t.TempDir()
	writePreset(t, dir, "README.md", "not a preset")
	writePreset(t, dir, "dug-test.json", minimalPreset)
	got, err := LoadDirs(dir)
	if err != nil {
		t.Fatalf("LoadDirs: %v", err)
	}
	if len(got) != 1 || got[0].Name != "dug-test" {
		t.Errorf("expected only dug-test, got %v", got)
	}
}

func TestLoadDirsMissingDirErrors(t *testing.T) {
	_, err := LoadDirs("/does/not/exist")
	if err == nil {
		t.Fatal("expected error for missing dir")
	}
}

func TestLoadDirsMalformedJSONErrors(t *testing.T) {
	dir := t.TempDir()
	writePreset(t, dir, "bad.json", "{not json")
	if _, err := LoadDirs(dir); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestLoadDirsUnknownKeyErrors(t *testing.T) {
	dir := t.TempDir()
	writePreset(t, dir, "bad.json",
		`{"Alow": {"Commands": {}}}`)
	if _, err := LoadDirs(dir); err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestAllWithoutEnvIsEmbedded(t *testing.T) {
	t.Setenv(PresetDirsEnv, "")
	got, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != len(MustEmbedded()) {
		t.Errorf("expected embedded set, got %d presets",
			len(got))
	}
}

func TestAllExternalFirst(t *testing.T) {
	dir := t.TempDir()
	writePreset(t, dir, "dug-test.json", minimalPreset)
	t.Setenv(PresetDirsEnv, dir)
	got, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != len(MustEmbedded())+1 {
		t.Fatalf("expected embedded+1, got %d", len(got))
	}
	if got[0].Name != "dug-test" || got[0].Dir != dir {
		t.Errorf(
			"external preset should come first, got %q",
			got[0].Name)
	}
}

func TestAllDuplicateOfEmbeddedErrors(t *testing.T) {
	dir := t.TempDir()
	writePreset(t, dir, "git.json", minimalPreset)
	t.Setenv(PresetDirsEnv, dir)
	_, err := All()
	if err == nil ||
		!strings.Contains(err.Error(), "git") {
		t.Fatalf(
			"expected duplicate-name error naming git, "+
				"got %v", err)
	}
}

func TestAllDuplicateAcrossDirsErrors(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	writePreset(t, dirA, "dug-test.json", minimalPreset)
	writePreset(t, dirB, "dug-test.json", minimalPreset)
	t.Setenv(PresetDirsEnv, dirA+":"+dirB)
	if _, err := All(); err == nil {
		t.Fatal("expected duplicate-name error")
	}
}
