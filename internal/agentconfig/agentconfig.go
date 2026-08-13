// Package agentconfig reads agent-permissions config files
// (~/.agents/permissions.json, <project>/.agents/permissions.json,
// and the project-local <project>/.agents/permissions.local.json).
//
// The schema is four tier objects, each holding entries by
// tool axis (Commands, EnvVars). Within each axis, entries
// are a map from pattern → reason; the reason is shown in
// hook output and may be empty. A top-level Rules object
// (rule ID -> {Enabled}) overrides Rules-layer config. The
// top-level shape also carries optional enabled-presets /
// disabled-presets fields for selecting which embedded
// presets contribute.
package agentconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/sothatsit/agent-permissions/internal/atomicfile"
	"github.com/sothatsit/agent-permissions/internal/configjson"
	"github.com/sothatsit/agent-permissions/internal/model"
)

// TierEntries is one tier's entries split by tool axis.
// Each axis maps pattern → reason; the reason is surfaced
// as "<pattern> - <reason>  (from <source>)" in hook output
// and may be empty (no dash shown). Loading code at the
// perms layer converts these into typed patterns.
type TierEntries struct {
	Commands map[string]string `json:"Commands,omitempty"`
	EnvVars  map[string]string `json:"EnvVars,omitempty"`
}

// Config is the parsed form of a permissions.json file.
// Path is retained so that `check` and error messages can
// attribute decisions back to the source file.
type Config struct {
	Path string

	Allow   TierEntries
	SoftAsk TierEntries
	Ask     TierEntries
	Deny    TierEntries

	// Rules overrides Rules-layer rule config by ID
	// (rule -> {Enabled}). Presets enable rules by default;
	// a user sets Enabled false here to turn one off, or
	// Enabled true to turn on one no active preset enables.
	// Nil when absent.
	Rules map[string]model.RuleConfig

	// EnabledPresets and DisabledPresets are nil when the
	// field is absent from the JSON. An empty slice means
	// the user explicitly wrote `[]`. The distinction
	// matters: absent = "no opinion, defer to defaults or
	// another file"; empty = "explicitly nothing".
	EnabledPresets  *[]string
	DisabledPresets *[]string
}

// Clone returns an independent copy of the config.
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}

	cloned := *c
	cloned.Allow = cloneTier(c.Allow)
	cloned.SoftAsk = cloneTier(c.SoftAsk)
	cloned.Ask = cloneTier(c.Ask)
	cloned.Deny = cloneTier(c.Deny)
	cloned.Rules = maps.Clone(c.Rules)
	if c.EnabledPresets != nil {
		enabled := slices.Clone(*c.EnabledPresets)
		cloned.EnabledPresets = &enabled
	}
	if c.DisabledPresets != nil {
		disabled := slices.Clone(*c.DisabledPresets)
		cloned.DisabledPresets = &disabled
	}
	return &cloned
}

func cloneTier(tier TierEntries) TierEntries {
	return TierEntries{
		Commands: maps.Clone(tier.Commands),
		EnvVars:  maps.Clone(tier.EnvVars),
	}
}

// HasPresetSelection reports whether the config specifies
// either field. Used by the resolution chain to decide
// whether the project-level config overrides global for
// preset selection.
func (c *Config) HasPresetSelection() bool {
	if c == nil {
		return false
	}
	return c.EnabledPresets != nil ||
		c.DisabledPresets != nil
}

type rawConfig struct {
	Allow           TierEntries                 `json:"Allow,omitempty"`
	SoftAsk         TierEntries                 `json:"SoftAsk,omitempty"`
	Ask             TierEntries                 `json:"Ask,omitempty"`
	Deny            TierEntries                 `json:"Deny,omitempty"`
	Rules           map[string]model.RuleConfig `json:"Rules,omitempty"`
	EnabledPresets  *[]string                   `json:"enabled-presets,omitempty"`
	DisabledPresets *[]string                   `json:"disabled-presets,omitempty"`
}

// Load reads the JSON file at path and returns the parsed
// config. Returns nil, nil when the file does not exist —
// callers treat that as "no contribution from this source".
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return Parse(path, data)
}

// Parse parses the given JSON data. Exposed so callers
// (e.g. the `check` subcommand printing a breakdown) can
// hand in already-read bytes.
//
// Tier fields that arrive as a JSON array (legacy flat
// shape) produce a clear migration error rather than a
// silent reinterpretation — pre-release, no compatibility
// shim warranted.
func Parse(path string, data []byte) (*Config, error) {
	generic, err := configjson.DecodeObject(data)
	if err != nil {
		return nil, fmt.Errorf(
			"invalid JSON in %s: %v", path, err)
	}
	if err := checkSchemaKeys(generic); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	for _, tierName := range []string{
		"Allow", "SoftAsk", "Ask", "Deny",
	} {
		value, exists := generic[tierName]
		if !exists {
			continue
		}
		if _, isArray := value.([]any); isArray {
			return nil, fmt.Errorf(
				"%s: %q must be an object with "+
					"Commands/EnvVars keys, got an array",
				path, tierName)
		}
	}

	var raw rawConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf(
			"invalid JSON in %s: %v", path, err)
	}

	return &Config{
		Path:            path,
		Allow:           raw.Allow,
		SoftAsk:         raw.SoftAsk,
		Ask:             raw.Ask,
		Deny:            raw.Deny,
		Rules:           raw.Rules,
		EnabledPresets:  raw.EnabledPresets,
		DisabledPresets: raw.DisabledPresets,
	}, nil
}

func checkSchemaKeys(root map[string]any) error {
	if err := configjson.CheckKeys(
		root,
		"Allow", "SoftAsk", "Ask", "Deny", "Rules",
		"enabled-presets", "disabled-presets",
	); err != nil {
		return err
	}

	for _, tierName := range []string{
		"Allow", "SoftAsk", "Ask", "Deny",
	} {
		tier, ok := configjson.ObjectAt(root, tierName)
		if !ok {
			continue
		}
		if err := configjson.CheckKeys(
			tier, "Commands", "EnvVars",
		); err != nil {
			return fmt.Errorf("%s: %w", tierName, err)
		}
	}

	rules, ok := configjson.ObjectAt(root, "Rules")
	if !ok {
		return nil
	}
	if err := configjson.CheckObjectValueKeys(
		rules, "Enabled",
	); err != nil {
		return fmt.Errorf("Rules.%w", err)
	}

	return nil
}

// Save writes the config back to its Path. The write goes
// through atomicfile.Write so a crash mid-write can't leave
// a half-written file, and the existing file's mode is
// preserved when overwriting.
func (c *Config) Save() error {
	if c.Path == "" {
		return fmt.Errorf("agentconfig: empty path")
	}
	raw := rawConfig{
		Allow:           c.Allow,
		SoftAsk:         c.SoftAsk,
		Ask:             c.Ask,
		Deny:            c.Deny,
		Rules:           c.Rules,
		EnabledPresets:  c.EnabledPresets,
		DisabledPresets: c.DisabledPresets,
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicfile.Write(c.Path, data, 0o644)
}
