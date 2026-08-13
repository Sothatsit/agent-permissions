package presets

import (
	"os"
	"path/filepath"
	"slices"
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
	if p.Enforced {
		t.Error("ordinary external preset marked enforced")
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

func TestLoadDirsUnknownTierAxisErrors(t *testing.T) {
	dir := t.TempDir()
	writePreset(t, dir, "bad.json",
		`{"Allow": {"Command": {"mytool:*": "x"}}}`)
	if _, err := LoadDirs(dir); err == nil {
		t.Fatal("expected error for unknown tier axis")
	}
}

func TestLoadDirsUnknownRuleConfigKeyErrors(t *testing.T) {
	dir := t.TempDir()
	writePreset(t, dir, "bad.json",
		`{"Rules": {"git.branch-writes": {"Enable": true}}}`)
	if _, err := LoadDirs(dir); err == nil {
		t.Fatal("expected error for unknown rule config key")
	}
}

func TestLoadDirsRejectsWrongKeyCase(t *testing.T) {
	for _, body := range []string{
		`{"deny": {"Commands": {}}}`,
		`{"Deny": {"commands": {}}}`,
		`{"Rules": {"git.branch-writes": {"enabled": true}}}`,
	} {
		dir := t.TempDir()
		writePreset(t, dir, "bad.json", body)
		if _, err := LoadDirs(dir); err == nil {
			t.Errorf("expected wrong-case key error for %s", body)
		}
	}
}

func TestLoadDirsRejectsNullValues(t *testing.T) {
	tests := []struct {
		name  string
		body  string
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
			"description",
			`{"description": null}`,
			`$["description"]`,
		},
		{
			"reason",
			`{"Deny": {"Commands": {"mytool:*": null}}}`,
			`$["Deny"]["Commands"]["mytool:*"]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writePreset(t, dir, "bad.json", tt.body)
			_, err := LoadDirs(dir)
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

func TestLoadDirsRejectsDuplicateKeys(t *testing.T) {
	tests := []struct {
		name string
		body string
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
			dir := t.TempDir()
			writePreset(t, dir, "bad.json", tt.body)
			_, err := LoadDirs(dir)
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

func TestAllWithoutEnvIsEmbedded(t *testing.T) {
	t.Setenv(PresetDirsEnv, "")
	t.Setenv(EnforcedPresetDirsEnv, "")
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
	t.Setenv(EnforcedPresetDirsEnv, "")
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

// findPreset returns the first preset with the name, or nil.
func findPreset(ps []*Preset, name string) *Preset {
	for _, p := range ps {
		if p.Name == name {
			return p
		}
	}
	return nil
}

func TestEnforcedPresetsEnvMarksEmbedded(t *testing.T) {
	t.Setenv(PresetDirsEnv, "")
	t.Setenv(EnforcedPresetDirsEnv, "")
	t.Setenv(EnforcedPresetsEnv, "escape-hatches, git")
	got, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	for _, name := range []string{"escape-hatches", "git"} {
		p := findPreset(got, name)
		if p == nil {
			t.Fatalf("preset %q missing", name)
		}
		if !p.Enforced {
			t.Errorf("preset %q not enforced", name)
		}
	}
	if p := findPreset(got, "mpi"); p == nil || p.Enforced {
		t.Error("unnamed preset should stay unenforced")
	}
}

// Embedded() caches one slice per process, so marking must not
// outlive the call that asked for it.
func TestEnforcedPresetsEnvDoesNotLeak(t *testing.T) {
	t.Setenv(PresetDirsEnv, "")
	t.Setenv(EnforcedPresetDirsEnv, "")
	t.Setenv(EnforcedPresetsEnv, "escape-hatches")
	if _, err := All(); err != nil {
		t.Fatalf("All: %v", err)
	}

	t.Setenv(EnforcedPresetsEnv, "")
	got, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if p := findPreset(got, "escape-hatches"); p.Enforced {
		t.Error("marking leaked into a later load")
	}
	if p := findPreset(MustEmbedded(), "escape-hatches"); p.Enforced {
		t.Error("marking leaked into the embedded cache")
	}
}

// A misspelled name must fail closed: silently ignoring it
// would leave a site believing policy is enforced when it is
// not.
func TestEnforcedPresetsEnvUnknownNameErrors(t *testing.T) {
	t.Setenv(PresetDirsEnv, "")
	t.Setenv(EnforcedPresetDirsEnv, "")
	t.Setenv(EnforcedPresetsEnv, "escape-hatchs")
	_, err := All()
	if err == nil {
		t.Fatal("expected an error for an unknown name")
	}
	if !strings.Contains(err.Error(), "escape-hatchs") ||
		!strings.Contains(err.Error(), EnforcedPresetsEnv) {
		t.Errorf(
			"error should name the preset and the variable, "+
				"got %v", err)
	}
}

func TestEnforcedPresetsEnvMarksEveryOrigin(t *testing.T) {
	dir := t.TempDir()
	writePreset(t, dir, "git.json", minimalPreset)
	t.Setenv(PresetDirsEnv, dir)
	t.Setenv(EnforcedPresetDirsEnv, "")
	t.Setenv(EnforcedPresetsEnv, "git")
	got, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	var seen int
	for _, p := range got {
		if p.Name != "git" {
			continue
		}
		seen++
		if !p.Enforced {
			t.Errorf("git from %q not enforced", p.Dir)
		}
	}
	if seen != 2 {
		t.Fatalf("want 2 presets named git, got %d", seen)
	}
}

func TestEnforcedLoadsMarked(t *testing.T) {
	dir := t.TempDir()
	writePreset(t, dir, "dug-test.json", minimalPreset)
	t.Setenv(EnforcedPresetDirsEnv, dir)
	got, err := Enforced()
	if err != nil {
		t.Fatalf("Enforced: %v", err)
	}
	if len(got) != 1 || !got[0].Enforced {
		t.Fatalf("expected one enforced preset, got %#v", got)
	}
}

func TestAllEnforcedBeforeOrdinaryExternal(t *testing.T) {
	enforcedDir := t.TempDir()
	externalDir := t.TempDir()
	writePreset(t, enforcedDir, "enforced.json", minimalPreset)
	writePreset(t, externalDir, "external.json", minimalPreset)
	t.Setenv(EnforcedPresetDirsEnv, enforcedDir)
	t.Setenv(PresetDirsEnv, externalDir)
	got, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) < 2 || got[0].Name != "enforced" ||
		got[1].Name != "external" {
		t.Fatalf("unexpected preset order: %v", got)
	}
}

func TestEnforcedMissingDirNamesEnv(t *testing.T) {
	t.Setenv(EnforcedPresetDirsEnv, "/does/not/exist")
	_, err := Enforced()
	if err == nil || !strings.Contains(
		err.Error(), EnforcedPresetDirsEnv,
	) {
		t.Fatalf("expected enforced env attribution, got %v", err)
	}
}

// countName reports how many of the presets carry the name.
func countName(ps []*Preset, name string) int {
	var n int
	for _, p := range ps {
		if p.Name == name {
			n++
		}
	}
	return n
}

// A name collision must never stop the policy loading — that
// would block every Bash call. Both presets stay active and
// DuplicateNames reports the collision for validate.
func TestAllKeepsDuplicateOfEmbedded(t *testing.T) {
	dir := t.TempDir()
	writePreset(t, dir, "git.json", minimalPreset)
	t.Setenv(PresetDirsEnv, dir)
	t.Setenv(EnforcedPresetDirsEnv, "")
	got, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if n := countName(got, "git"); n != 2 {
		t.Fatalf("want 2 presets named git, got %d", n)
	}
	dupes := DuplicateNames(got)
	if len(dupes) != 1 || dupes[0].Name != "git" {
		t.Fatalf("want git reported duplicate, got %v", dupes)
	}
	if len(dupes[0].Origins) != 2 {
		t.Fatalf("want 2 origins, got %v", dupes[0].Origins)
	}
}

func TestAllKeepsDuplicateAcrossDirs(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	writePreset(t, dirA, "dug-test.json", minimalPreset)
	writePreset(t, dirB, "dug-test.json", minimalPreset)
	t.Setenv(PresetDirsEnv, dirA+":"+dirB)
	t.Setenv(EnforcedPresetDirsEnv, "")
	got, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if n := countName(got, "dug-test"); n != 2 {
		t.Fatalf("want 2 dug-test presets, got %d", n)
	}
	dupes := DuplicateNames(got)
	if len(dupes) != 1 ||
		!slices.Contains(dupes[0].Origins, dirA) ||
		!slices.Contains(dupes[0].Origins, dirB) {
		t.Fatalf("want both dirs reported, got %v", dupes)
	}
}

func TestAllKeepsDuplicateAcrossChannels(t *testing.T) {
	enforcedDir := t.TempDir()
	externalDir := t.TempDir()
	writePreset(t, enforcedDir, "dug-test.json", minimalPreset)
	writePreset(t, externalDir, "dug-test.json", minimalPreset)
	t.Setenv(EnforcedPresetDirsEnv, enforcedDir)
	t.Setenv(PresetDirsEnv, externalDir)
	got, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if n := countName(got, "dug-test"); n != 2 {
		t.Fatalf("want 2 dug-test presets, got %d", n)
	}
	// The enforced copy must still be the enforced one, so a
	// collision cannot demote org policy to a normal source.
	var enforcedSeen bool
	for _, p := range got {
		if p.Name == "dug-test" && p.Enforced {
			enforcedSeen = true
		}
	}
	if !enforcedSeen {
		t.Fatal("enforced copy lost its enforced flag")
	}
}

// Repeating a directory is a no-op, as it is on PATH. The
// same dir named twice must not double its presets.
func TestLoadDirsSkipsRepeatedDir(t *testing.T) {
	dir := t.TempDir()
	writePreset(t, dir, "dug-test.json", minimalPreset)
	t.Setenv(EnforcedPresetDirsEnv, "")
	got, err := LoadDirs(dir + ":" + dir)
	if err != nil {
		t.Fatalf("LoadDirs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 preset, got %d", len(got))
	}
}

// A repeat spelled differently is still the same directory.
func TestLoadDirsSkipsRepeatedDirViaSymlink(t *testing.T) {
	dir := t.TempDir()
	writePreset(t, dir, "dug-test.json", minimalPreset)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	t.Setenv(EnforcedPresetDirsEnv, "")
	got, err := LoadDirs(dir + ":" + link + ":" + dir + "/")
	if err != nil {
		t.Fatalf("LoadDirs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 preset, got %d", len(got))
	}
}
