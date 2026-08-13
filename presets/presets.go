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
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/sothatsit/agent-permissions/internal/configjson"
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
// from; empty for embedded presets. Enforced marks external
// policy that users may strengthen but cannot weaken or
// disable through their config.
//
// Rules enables Rules-layer rules by ID (rule -> {Enabled}).
// Rules ship default-OFF in code, so a preset listing a rule
// with Enabled true turns it on. Embedded presets own the
// rules for their topic. Ordinary presets follow selection;
// enforced presets lock their enabled rules on.
type Preset struct {
	Name        string                      `json:"-"`
	Dir         string                      `json:"-"`
	Enforced    bool                        `json:"-"`
	Description string                      `json:"description"`
	Allow       TierEntries                 `json:"Allow"`
	SoftAsk     TierEntries                 `json:"SoftAsk"`
	Ask         TierEntries                 `json:"Ask"`
	Deny        TierEntries                 `json:"Deny"`
	Rules       map[string]model.RuleConfig `json:"Rules,omitempty"`
}

// Clone returns an independent copy of the preset.
func (p *Preset) Clone() *Preset {
	if p == nil {
		return nil
	}

	cloned := *p
	cloned.Allow = cloneTier(p.Allow)
	cloned.SoftAsk = cloneTier(p.SoftAsk)
	cloned.Ask = cloneTier(p.Ask)
	cloned.Deny = cloneTier(p.Deny)
	cloned.Rules = maps.Clone(p.Rules)
	return &cloned
}

func cloneTier(tier TierEntries) TierEntries {
	return TierEntries{
		Commands: maps.Clone(tier.Commands),
		EnvVars:  maps.Clone(tier.EnvVars),
	}
}

func clonePresets(all []*Preset) []*Preset {
	out := make([]*Preset, len(all))
	for i, preset := range all {
		out[i] = preset.Clone()
	}
	return out
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

// EnforcedPresetDirsEnv names extra preset directories whose
// decisions form a minimum policy. Their decisions combine
// with normal resolution by strength, they cannot be disabled
// through preset selection, and rules they enable stay on.
const EnforcedPresetDirsEnv = "AGENT_PERMISSIONS_ENFORCED_PRESET_DIRS"

// EnforcedPresetsEnv names already-available presets to move
// into the enforced plane, comma-separated. It exists for the
// presets a site does not ship and so cannot place in an
// enforced directory — the embedded topics above all. Naming
// `escape-hatches` makes its denials a floor no user config can
// weaken; naming a topic with Rules locks those rules on.
//
// Names, not paths, so the separator is a comma rather than the
// colon its sibling directory variables use.
const EnforcedPresetsEnv = "AGENT_PERMISSIONS_ENFORCED_PRESETS"

// External loads the presets named by PresetDirsEnv. An
// unset or empty variable yields nil. Errors are load
// failures a user must fix, not conditions to skip past:
// a missing directory or malformed file means site policy
// has silently vanished, so the hook must fail closed
// rather than run with weaker policy.
func External() ([]*Preset, error) {
	return LoadDirs(os.Getenv(PresetDirsEnv))
}

// Enforced loads organisation policy named by
// EnforcedPresetDirsEnv. The resolver treats these presets as
// a minimum policy rather than another first-match source.
func Enforced() ([]*Preset, error) {
	return loadDirs(
		os.Getenv(EnforcedPresetDirsEnv), true,
		EnforcedPresetDirsEnv)
}

// LoadDirs loads presets from a colon-separated list of
// directories. Presets are returned in list order, and in
// filename order within each directory. Empty list entries
// and repeats of a directory already loaded are skipped
// (PATH convention); anything else that fails — unreadable
// directory, malformed or misnamed JSON — is an error.
func LoadDirs(list string) ([]*Preset, error) {
	return loadDirs(list, false, PresetDirsEnv)
}

func loadDirs(
	list string, enforced bool, envName string,
) ([]*Preset, error) {
	var out []*Preset
	// Keyed by resolved path so a repeat spelled differently
	// — a symlink, a trailing slash, "." segments — is still
	// recognised as the same directory.
	seen := map[string]bool{}
	for _, dir := range strings.Split(list, ":") {
		if dir == "" {
			continue
		}
		key, err := filepath.EvalSymlinks(dir)
		if err != nil {
			key = filepath.Clean(dir)
		}
		if seen[key] {
			continue
		}
		seen[key] = true

		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf(
				"preset dir (from %s): %v",
				envName, err)
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
			p.Enforced = enforced
			out = append(out, p)
		}
	}
	return out, nil
}

// All returns every available preset: enforced external
// presets first, then ordinary external presets, then the
// embedded set. The resolver keeps enforced presets in a
// separate policy plane; the ordering here supplies normal
// priority and stable enforced attribution.
//
// Two directories may supply the same preset name — a dev
// checkout beside a deployed tree is the ordinary case — and
// both stay active. Refusing to load would block every Bash
// call over a name collision, which is a far worse failure
// than the ambiguity it prevents: matching entries combine by
// strength in the enforced plane and by list order in the
// normal one, so keeping both can only preserve or strengthen
// policy. The cost is that attribution names the preset, not
// the directory it came from; `presets list` reports the
// directories and `validate` reports the collision.
func All() ([]*Preset, error) {
	enforced, err := Enforced()
	if err != nil {
		return nil, err
	}
	ext, err := External()
	if err != nil {
		return nil, err
	}
	embedded := MustEmbedded()
	all := make([]*Preset, 0,
		len(enforced)+len(ext)+len(embedded))
	all = append(all, enforced...)
	all = append(all, ext...)
	all = append(all, clonePresets(embedded)...)

	if err := markEnforced(
		all, os.Getenv(EnforcedPresetsEnv),
	); err != nil {
		return nil, err
	}
	return all, nil
}

// markEnforced moves the named presets into the enforced plane.
// An unknown name is an error rather than a no-op: a site that
// misspells one would otherwise believe policy is enforced
// while it silently is not, which is the failure this whole
// plane exists to prevent.
func markEnforced(all []*Preset, list string) error {
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		var found bool
		// Every preset with the name, since two origins may
		// supply one name and enforcing only the first would
		// be arbitrary.
		for _, p := range all {
			if p.Name == name {
				p.Enforced = true
				found = true
			}
		}
		if !found {
			return fmt.Errorf(
				"%s names unknown preset %q",
				EnforcedPresetsEnv, name)
		}
	}
	return nil
}

// DuplicateName describes one preset name supplied by more
// than one origin. Reported by validate; not an error.
type DuplicateName struct {
	Name    string
	Origins []string
}

// DuplicateNames lists the names that more than one origin
// supplies, in the order the names first appear.
func DuplicateNames(all []*Preset) []DuplicateName {
	origin := func(p *Preset) string {
		if p.Dir == "" {
			return "embedded"
		}
		return p.Dir
	}
	var order []string
	byName := map[string][]string{}
	for _, p := range all {
		if _, ok := byName[p.Name]; !ok {
			order = append(order, p.Name)
		}
		byName[p.Name] = append(byName[p.Name], origin(p))
	}
	var out []DuplicateName
	for _, name := range order {
		if len(byName[name]) > 1 {
			out = append(out, DuplicateName{
				Name:    name,
				Origins: byName[name],
			})
		}
	}
	return out
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

// parseOne parses a single preset file and rejects unknown fields at every
// level. Shipped-data invariants beyond the schema live in test/test-presets.sh.
func parseOne(
	filename string, data []byte,
) (*Preset, error) {
	var p Preset
	if err := configjson.Decode(data, &p); err != nil {
		return nil, fmt.Errorf(
			"decode %s: %v", filename, err)
	}

	p.Name = strings.TrimSuffix(filename, ".json")
	return &p, nil
}
