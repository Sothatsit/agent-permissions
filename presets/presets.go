// Package presets bundles the topic-organised permission
// presets that ship with agent-permissions. The JSON files
// in this directory are embedded into the binary at build
// time and parsed once at startup, so the binary is
// self-contained — `go install` alone is enough to use it.
//
// On parse the package validates that every key is known;
// bad data here is a build-time error rather than a
// runtime surprise.
package presets

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/sothatsit/agent-permissions/internal/model"
)

//go:embed *.json
var fsys embed.FS

// TierEntries is one tier of a preset, holding entries by
// tool axis. The shape mirrors agentconfig.TierEntries —
// presets and user permission files use the same JSON.
type TierEntries struct {
	Commands map[string]string `json:"Commands,omitempty"`
	EnvVars  map[string]string `json:"EnvVars,omitempty"`
}

// Preset is one topic-organised bundle of permissions.
// Name is the filename stem; everything else is read from
// the JSON. A preset may populate any subset of the four
// tier objects, and within each, any subset of the tool
// axes. Dir is the directory an external preset was loaded
// from; empty for embedded presets.
//
// Rules enables Rules-layer rules by ID (rule -> {Enabled}).
// Rules ship default-OFF in code, so a preset listing a rule
// with Enabled true is what turns it on. A preset owns the
// rules for its topic; disabling the preset disables them.
type Preset struct {
	Name        string
	Dir         string
	Description string                      `json:"description"`
	Allow       TierEntries                 `json:"Allow"`
	SoftAsk     TierEntries                 `json:"SoftAsk"`
	Ask         TierEntries                 `json:"Ask"`
	Deny        TierEntries                 `json:"Deny"`
	Rules       map[string]model.RuleConfig `json:"Rules,omitempty"`
}

var (
	loadOnce sync.Once
	loaded   []*Preset
	loadErr  error
)

// Embedded returns the parsed list of presets bundled in
// the binary, sorted alphabetically by name. The first
// call parses every file; later calls return the cached
// slice.
func Embedded() ([]*Preset, error) {
	loadOnce.Do(func() {
		loaded, loadErr = parseAll()
	})
	return loaded, loadErr
}

// MustEmbedded is Embedded but panics on parse error.
// Used by the hook on the hot path — a parse failure here
// means the binary was built with broken JSON, not a
// recoverable runtime condition.
func MustEmbedded() []*Preset {
	ps, err := Embedded()
	if err != nil {
		panic(fmt.Sprintf("embedded preset parse error: %v", err))
	}
	return ps
}

// ByName returns the embedded preset with the given name,
// or nil if no such preset exists.
func ByName(name string) *Preset {
	for _, p := range MustEmbedded() {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// Names returns the names of all embedded presets in
// alphabetical order.
func Names() []string {
	all := MustEmbedded()
	out := make([]string, len(all))
	for i, p := range all {
		out[i] = p.Name
	}
	return out
}

// PresetDirsEnv names extra directories of preset JSON
// files, colon-separated like PATH. It lets a site ship
// organisation-wide policy alongside its own tooling
// (rather than editing users' config files) — the
// installer or launch wrapper sets the variable and the
// presets load like any other preset.
const PresetDirsEnv = "AGENT_PERMISSIONS_PRESET_DIRS"

// External loads the presets named by PresetDirsEnv. An
// unset or empty variable yields nil. Errors are load
// failures a user must fix, not conditions to skip past:
// a missing directory or malformed file means site policy
// has silently vanished, so the hook must fail closed
// rather than run with weaker policy.
func External() ([]*Preset, error) {
	return LoadDirs(os.Getenv(PresetDirsEnv))
}

// LoadDirs loads presets from a colon-separated list of
// directories. Presets are returned in list order, and in
// filename order within each directory. Empty list entries
// are skipped (PATH convention); anything else that fails
// — unreadable directory, malformed or misnamed JSON — is
// an error.
func LoadDirs(list string) ([]*Preset, error) {
	var out []*Preset
	for _, dir := range strings.Split(list, ":") {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf(
				"preset dir (from %s): %v",
				PresetDirsEnv, err)
		}
		for _, e := range entries {
			if e.IsDir() ||
				!strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf(
					"read %s: %v", path, err)
			}
			p, err := parseOne(e.Name(), data)
			if err != nil {
				return nil, fmt.Errorf(
					"%s: %v", dir, err)
			}
			p.Dir = dir
			out = append(out, p)
		}
	}
	return out, nil
}

// All returns every active preset in priority order:
// external presets (from PresetDirsEnv) first, then the
// embedded set. External presets outrank embedded ones so
// site policy can override shipped defaults, while user
// config sources still outrank both. Duplicate names are
// an error — external presets already win on priority, so
// a name collision is only ever an accident.
func All() ([]*Preset, error) {
	ext, err := External()
	if err != nil {
		return nil, err
	}
	all := make([]*Preset, 0,
		len(ext)+len(MustEmbedded()))
	all = append(all, ext...)
	all = append(all, MustEmbedded()...)

	origin := func(p *Preset) string {
		if p.Dir == "" {
			return "embedded"
		}
		return p.Dir
	}
	seen := map[string]*Preset{}
	for _, p := range all {
		if prev, ok := seen[p.Name]; ok {
			return nil, fmt.Errorf(
				"duplicate preset name %q (%s and %s)",
				p.Name, origin(prev), origin(p))
		}
		seen[p.Name] = p
	}
	return all, nil
}

func parseAll() ([]*Preset, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var presets []*Preset
	for _, e := range entries {
		if e.IsDir() ||
			!strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := fsys.ReadFile(e.Name())
		if err != nil {
			return nil, fmt.Errorf(
				"read %s: %v", e.Name(), err)
		}
		p, err := parseOne(e.Name(), data)
		if err != nil {
			return nil, err
		}
		presets = append(presets, p)
	}
	sort.Slice(presets, func(i, j int) bool {
		return presets[i].Name < presets[j].Name
	})
	return presets, nil
}

// parseOne parses a single preset file. Validates that
// every top-level key is known and that tier fields are
// objects (the legacy flat-array shape errors with a
// migration message). Per-axis structural checks belong
// in test/test-presets.sh — those are invariants about the
// shipped data, not the schema.
func parseOne(
	filename string, data []byte,
) (*Preset, error) {
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(data, &generic); err != nil {
		return nil, fmt.Errorf(
			"parse %s: %v", filename, err)
	}
	known := map[string]bool{
		"description": true,
		"Allow":       true,
		"SoftAsk":     true,
		"Ask":         true,
		"Deny":        true,
		"Rules":       true,
	}
	for k, v := range generic {
		if !known[k] {
			return nil, fmt.Errorf(
				"%s: unknown key %q", filename, k)
		}
		// description is a string and Rules is an object
		// keyed by rule ID; neither is a tier, so skip the
		// legacy flat-array check below (which guards the
		// Commands/EnvVars tier keys).
		if k == "description" || k == "Rules" {
			continue
		}
		if len(v) > 0 && v[0] == '[' {
			return nil, fmt.Errorf(
				"%s: %q must be an object with "+
					"Commands/EnvVars keys, got an array",
				filename, k)
		}
	}
	var p Preset
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf(
			"decode %s: %v", filename, err)
	}
	p.Name = strings.TrimSuffix(filename, ".json")
	return &p, nil
}
