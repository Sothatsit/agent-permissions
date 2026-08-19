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

// Presets returns independent copies, in policy priority order.
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

type PresetState int

const (
	PresetEnabledByDefault PresetState = iota
	PresetEnabledByName
	PresetDisabledByName
	PresetDisabledByOmission
	PresetEnforced
)

type PolicyPreset struct {
	Preset *presets.Preset
	State  PresetState
}

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
// explicit selector, enforced presets precede ordinary ones and each group
// keeps catalog order. SelectorPath is empty when no config specifies
// selection.
type PresetSelection struct {
	SelectorPath string
	Presets      []PolicyPreset
}

type AgentConfigScope int

const (
	AgentConfigGlobal AgentConfigScope = iota
	AgentConfigProject
	AgentConfigLocal
)

type policyFileID string

func identifyPolicyFile(path string) (policyFileID, error) {
	identity, err := filepath.EvalSymlinks(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}

		identity = path
	}

	identity, err = filepath.Abs(identity)
	if err != nil {
		return "", err
	}

	return policyFileID(filepath.Clean(identity)), nil
}

// AgentConfigSource is one captured .agents config file. Config is nil when the
// path did not exist. SourceName is the name used in decision output.
type AgentConfigSource struct {
	Path       string
	SourceName string
	Config     *agentconfig.Config
	fileID     policyFileID
}

type claudeSettingsSource struct {
	permissions SourcePerms
	warnings    []ConfigWarning
}

// PolicySnapshot captures every policy input for one command, so resolution
// reads no file or environment state again.
type PolicySnapshot struct {
	cwd             string
	presets         *PresetCatalog
	configs         [3]AgentConfigSource
	claudeSettings  []claudeSettingsSource
	presetSelection PresetSelection
	pathDirs        map[string]struct{}
}

// LoadPolicySnapshot reads and validates every policy source once. Missing
// .agents files remain represented by a source with a nil Config.
func LoadPolicySnapshot(configDir, cwd string) (*PolicySnapshot, error) {
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

	parsedAgents := map[policyFileID]*agentconfig.Config{}
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

		fileID, err := identifyPolicyFile(path)
		if err != nil {
			return AgentConfigSource{}, fmt.Errorf(
				"%s: %v", errorName, err)
		}

		config, parsed := parsedAgents[fileID]
		if !parsed {
			config, err = agentconfig.Load(path)
			if err != nil {
				return AgentConfigSource{}, fmt.Errorf(
					"%s: %v", errorName, err)
			}

			parsedAgents[fileID] = config
		} else if config != nil {
			config = config.Clone()
			config.Path = path
		}

		source.Config = config
		if config != nil {
			source.fileID = fileID
		}
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

	var claudeSettings []claudeSettingsSource
	seenClaude := map[policyFileID]bool{}
	loadClaude := func(path, label string) error {
		fileID, err := identifyPolicyFile(path)
		if err != nil {
			return fmt.Errorf("%s: %v", label, err)
		}
		if seenClaude[fileID] {
			return nil
		}

		seenClaude[fileID] = true
		source, warnings, err := loadClaudeSettings(path)
		if err != nil {
			return fmt.Errorf("%s: %v", label, err)
		}
		if source == nil {
			return nil
		}

		claudeSettings = append(claudeSettings, claudeSettingsSource{
			permissions: *source,
			warnings:    warnings,
		})
		return nil
	}

	if cwd != "" {
		if err := loadClaude(filepath.Join(
			cwd, ".claude", "settings.local.json"),
			"local settings"); err != nil {
			return nil, err
		}
		if err := loadClaude(filepath.Join(
			cwd, ".claude", "settings.json"),
			"project settings"); err != nil {
			return nil, err
		}
	}

	if configDir != "" {
		if err := loadClaude(filepath.Join(
			configDir, "settings.json"),
			"user settings"); err != nil {
			return nil, err
		}
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
		claudeSettings:  claudeSettings,
		presetSelection: makePresetSelection(catalog.available, configs),
		pathDirs:        parsePathDirs(os.Getenv("PATH")),
	}, nil
}

// Presets includes the disabled presets that validation needs.
func (s *PolicySnapshot) Presets() []*presets.Preset {
	return s.presets.Presets()
}

// PresetSelection returns an independent copy.
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

// AgentConfig returns an independent copy of one logical config slot. Two
// slots may refer to the same captured file through different paths.
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
	seen := map[policyFileID]bool{}
	for _, scope := range []AgentConfigScope{
		AgentConfigLocal,
		AgentConfigProject,
		AgentConfigGlobal,
	} {
		source := s.configs[scope]
		if source.Config == nil || seen[source.fileID] {
			continue
		}

		seen[source.fileID] = true
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
	selected := [3]*agentconfig.Config{}
	seen := map[policyFileID]bool{}
	for _, scope := range []AgentConfigScope{
		AgentConfigLocal,
		AgentConfigProject,
		AgentConfigGlobal,
	} {
		source := s.configs[scope]
		if source.Config == nil || seen[source.fileID] {
			continue
		}

		seen[source.fileID] = true
		selected[scope] = source.Config
	}

	return selected[AgentConfigGlobal],
		selected[AgentConfigProject],
		selected[AgentConfigLocal]
}
