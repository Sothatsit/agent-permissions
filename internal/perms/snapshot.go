package perms

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sothatsit/agent-permissions/internal/agentconfig"
	"github.com/sothatsit/agent-permissions/presets"
)

// PresetCatalog is one validated view of every available preset. It keeps the
// loader's priority order and includes presets that user selection disables.
type PresetCatalog struct {
	available []*presets.Preset
}

// LoadPresetCatalog reads every preset and rejects structural or semantic
// errors before returning any policy data to a caller.
func LoadPresetCatalog() (*PresetCatalog, error) {
	loaded, err := presets.All()
	if err != nil {
		return nil, fmt.Errorf("presets: %v", err)
	}

	all := clonePresets(loaded)
	if err := validateExternalPresets(all); err != nil {
		return nil, err
	}

	return &PresetCatalog{available: all}, nil
}

// Presets returns independent copies of every available preset in policy
// priority order.
func (c *PresetCatalog) Presets() []*presets.Preset {
	return clonePresets(c.available)
}

func clonePresets(all []*presets.Preset) []*presets.Preset {
	out := make([]*presets.Preset, len(all))
	for i, preset := range all {
		out[i] = preset.Clone()
	}

	return out
}

// PresetState identifies why one available preset is active or inactive.
type PresetState int

const (
	PresetEnabledByDefault PresetState = iota
	PresetEnabledByName
	PresetDisabledByName
	PresetDisabledByOmission
	PresetEnforced
)

// PolicyPreset is one available preset and its effective selection state.
type PolicyPreset struct {
	Preset *presets.Preset
	State  PresetState
}

// Active reports whether this preset contributes to resolved policy.
func (p PolicyPreset) Active() bool {
	switch p.State {
	case PresetEnabledByDefault,
		PresetEnabledByName,
		PresetEnforced:
		return true
	case PresetDisabledByName,
		PresetDisabledByOmission:
		return false
	default:
		panic(fmt.Sprintf("unknown preset state %d", p.State))
	}
}

// PresetSelection is the effective state of every available preset. With an
// explicit selector, enforced presets precede ordinary presets while each
// group keeps catalog order. SelectorPath is empty when no config specifies
// preset selection.
type PresetSelection struct {
	SelectorPath string
	Presets      []PolicyPreset
}

// AgentConfigScope identifies one .agents config slot.
type AgentConfigScope int

const (
	AgentConfigGlobal AgentConfigScope = iota
	AgentConfigProject
	AgentConfigLocal
)

// AgentConfigSource is one captured .agents config file. Config is nil when the
// path did not exist. SourceName is the name used in decision output.
type AgentConfigSource struct {
	Path       string
	SourceName string
	Config     *agentconfig.Config
}

// PolicySnapshot captures the harness-neutral policy inputs for one command:
// validated presets and each .agents config. Harness-native settings are read
// only by Resolve, so unrelated settings cannot break preset-only commands.
type PolicySnapshot struct {
	cwd             string
	presets         *PresetCatalog
	configs         [3]AgentConfigSource
	presetSelection PresetSelection
}

// LoadPolicySnapshot reads the presets and .agents configs once. Missing config
// files remain represented by a source with a nil Config.
func LoadPolicySnapshot(cwd string) (*PolicySnapshot, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %v", err)
	}

	globalPath := filepath.Join(
		home, ".agents", "permissions.json")
	projectPath := ""
	localPath := ""
	if cwd != "" {
		projectPath = filepath.Join(
			cwd, ".agents", "permissions.json")
		localPath = filepath.Join(
			cwd, ".agents", "permissions.local.json")
	}

	loaded := map[string]*agentconfig.Config{}
	load := func(
		path, sourceName, errorName string,
	) (AgentConfigSource, error) {
		source := AgentConfigSource{
			Path:       path,
			SourceName: sourceName,
		}
		if path == "" {
			return source, nil
		}

		config, ok := loaded[path]
		if !ok {
			config, err = agentconfig.Load(path)
			if err != nil {
				return AgentConfigSource{}, fmt.Errorf(
					"%s: %v", errorName, err)
			}

			loaded[path] = config
		}

		source.Config = config
		return source, nil
	}

	global, err := load(
		globalPath,
		"~/.agents/permissions.json", "global agent config")
	if err != nil {
		return nil, err
	}

	project, err := load(
		projectPath,
		projectPath, "project agent config")
	if err != nil {
		return nil, err
	}

	local, err := load(
		localPath,
		localPath, "local agent config")
	if err != nil {
		return nil, err
	}

	catalog, err := LoadPresetCatalog()
	if err != nil {
		return nil, err
	}

	configs := [3]AgentConfigSource{
		AgentConfigGlobal:  global,
		AgentConfigProject: project,
		AgentConfigLocal:   local,
	}
	return &PolicySnapshot{
		cwd:             cwd,
		presets:         catalog,
		configs:         configs,
		presetSelection: makePresetSelection(catalog.available, configs),
	}, nil
}

// Presets returns every preset captured by the snapshot, including disabled
// presets needed by validation.
func (s *PolicySnapshot) Presets() []*presets.Preset {
	return s.presets.Presets()
}

// PresetSelection returns an independent copy of the captured preset states.
func (s *PolicySnapshot) PresetSelection() PresetSelection {
	selection := PresetSelection{
		SelectorPath: s.presetSelection.SelectorPath,
		Presets: make(
			[]PolicyPreset, len(s.presetSelection.Presets)),
	}
	for i, policyPreset := range s.presetSelection.Presets {
		selection.Presets[i] = PolicyPreset{
			Preset: policyPreset.Preset.Clone(),
			State:  policyPreset.State,
		}
	}

	return selection
}

// AgentConfig returns an independent copy of one logical config slot. Project
// and global may refer to the same captured file when cwd is the user's home.
func (s *PolicySnapshot) AgentConfig(
	scope AgentConfigScope,
) AgentConfigSource {
	if scope < AgentConfigGlobal || scope > AgentConfigLocal {
		panic(fmt.Sprintf("unknown agent config scope %d", scope))
	}

	return cloneAgentConfigSource(s.configs[scope])
}

// AgentConfigs returns each existing config once in resolution precedence:
// local, project, then global.
func (s *PolicySnapshot) AgentConfigs() []AgentConfigSource {
	var out []AgentConfigSource
	seen := map[string]bool{}
	for _, scope := range []AgentConfigScope{
		AgentConfigLocal,
		AgentConfigProject,
		AgentConfigGlobal,
	} {
		source := s.configs[scope]
		if source.Config == nil || seen[source.Path] {
			continue
		}

		seen[source.Path] = true
		out = append(out, cloneAgentConfigSource(source))
	}

	return out
}

func cloneAgentConfigSource(source AgentConfigSource) AgentConfigSource {
	source.Config = source.Config.Clone()
	return source
}

func makePresetSelection(
	all []*presets.Preset,
	configs [3]AgentConfigSource,
) PresetSelection {
	var selector AgentConfigSource
	for _, scope := range []AgentConfigScope{
		AgentConfigLocal,
		AgentConfigProject,
		AgentConfigGlobal,
	} {
		if configs[scope].Config.HasPresetSelection() {
			selector = configs[scope]
			break
		}
	}

	var enabled, disabled map[string]bool
	if selector.Config != nil {
		if selector.Config.EnabledPresets != nil {
			enabled = map[string]bool{}
			for _, name := range *selector.Config.EnabledPresets {
				enabled[name] = true
			}
		}

		if selector.Config.DisabledPresets != nil {
			disabled = map[string]bool{}
			for _, name := range *selector.Config.DisabledPresets {
				disabled[name] = true
			}
		}
	}

	selection := PresetSelection{
		SelectorPath: selector.Path,
		Presets:      make([]PolicyPreset, len(all)),
	}
	ordered := all
	if selector.Config != nil {
		ordered = make([]*presets.Preset, 0, len(all))
		for _, preset := range all {
			if preset.Enforced {
				ordered = append(ordered, preset)
			}
		}

		for _, preset := range all {
			if !preset.Enforced {
				ordered = append(ordered, preset)
			}
		}
	}

	for i, preset := range ordered {
		state := PresetEnabledByDefault
		switch {
		case preset.Enforced:
			state = PresetEnforced
		case disabled[preset.Name]:
			state = PresetDisabledByName
		case enabled == nil:
			state = PresetEnabledByDefault
		case enabled[preset.Name]:
			state = PresetEnabledByName
		default:
			state = PresetDisabledByOmission
		}

		selection.Presets[i] = PolicyPreset{
			Preset: preset,
			State:  state,
		}
	}

	return selection
}

func (s *PolicySnapshot) resolutionConfigs() (
	global, project, local *agentconfig.Config,
) {
	global = s.configs[AgentConfigGlobal].Config
	project = s.configs[AgentConfigProject].Config
	local = s.configs[AgentConfigLocal].Config
	if global != nil && project != nil &&
		s.configs[AgentConfigGlobal].Path ==
			s.configs[AgentConfigProject].Path {
		global = nil
	}

	return global, project, local
}
