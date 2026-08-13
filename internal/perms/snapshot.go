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

// AgentConfigScope identifies one .agents config slot.
type AgentConfigScope int

const (
	AgentConfigGlobal AgentConfigScope = iota
	AgentConfigProject
	AgentConfigLocal
)

// AgentConfigSource is one captured .agents config file. Config is nil when
// the path did not exist. SourceName is the name used in decision output.
type AgentConfigSource struct {
	Path       string
	SourceName string
	Config     *agentconfig.Config
}

// PolicySnapshot captures the harness-neutral policy inputs for one command:
// validated presets and each .agents config. Harness-native settings are read
// only by Resolve, so unrelated settings cannot break preset-only commands.
type PolicySnapshot struct {
	cwd     string
	presets *PresetCatalog
	configs [3]AgentConfigSource
}

// LoadPolicySnapshot reads the presets and .agents configs once. Missing
// config files remain represented by a source with a nil Config.
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

	return &PolicySnapshot{
		cwd:     cwd,
		presets: catalog,
		configs: [3]AgentConfigSource{
			AgentConfigGlobal:  global,
			AgentConfigProject: project,
			AgentConfigLocal:   local,
		},
	}, nil
}

// Presets returns every preset captured by the snapshot, including disabled
// presets needed by listing and validation.
func (s *PolicySnapshot) Presets() []*presets.Preset {
	return s.presets.Presets()
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
