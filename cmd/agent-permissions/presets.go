package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/sothatsit/agent-permissions/internal/agentconfig"
	"github.com/sothatsit/agent-permissions/presets"
)

// presetsCmd dispatches the `presets` subcommand group.
// `list` is the only subcommand today; users opt presets
// in or out by hand-editing `enabled-presets` /
// `disabled-presets` in ~/.agents/permissions.json. The
// previous `enable` / `disable` subcommands were removed
// because "all enabled by default" made `enable` a
// no-op-with-a-misleading-success-message in the common
// case.
func presetsCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(
			"usage: agent-permissions presets list")
	}
	switch args[0] {
	case "list":
		return presetsList(args[1:])
	default:
		return fmt.Errorf(
			"unknown presets subcommand: %s "+
				"(only `list` is supported)",
			args[0])
	}
}

// presetState classifies whether each preset is enabled
// or disabled, plus the reason (a short note shown
// inline next to the preset name).
type presetState struct {
	enabled bool
	reason  string
}

// presetsList shows every embedded preset and the reason
// it is enabled or disabled, considering both the global
// and project-level permissions.json files (project beats
// global, same as the hook).
func presetsList(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: presets list")
	}

	global, globalPath, err := loadAgentConfig(
		globalConfigPath)
	if err != nil {
		return err
	}
	project, projectPath, err := loadAgentConfig(
		projectConfigPath)
	if err != nil {
		return err
	}
	local, localPath, err := loadAgentConfig(
		localConfigPath)
	if err != nil {
		return err
	}

	selecting := pickPresetSelector(global, project, local)

	// Header — show which config files were consulted
	// and which one is authoritative for preset
	// selection. Matches what the hook actually does, so
	// `presets list` doesn't lie about the effective
	// state.
	fmt.Println("Configs:")
	fmt.Printf(
		"  ~ %s\n",
		describeConfig(globalPath, global))
	fmt.Printf(
		"  cwd: %s\n",
		describeConfig(projectPath, project))
	fmt.Printf(
		"  cwd-local: %s\n",
		describeConfig(localPath, local))
	if selecting == nil {
		fmt.Println(
			"  Preset selection: (none — all " +
				"presets enabled by default)")
	} else {
		fmt.Printf(
			"  Preset selection: %s\n",
			selecting.Path)
	}
	fmt.Println()

	// Classify every embedded preset.
	all := presets.MustEmbedded()
	rows := make([]classifiedPreset, 0, len(all))
	for _, p := range all {
		rows = append(rows, classifiedPreset{
			name:        p.Name,
			description: p.Description,
			state:       classify(p.Name, selecting),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].name < rows[j].name
	})

	// Two top-level groups (enabled / disabled), reason
	// printed inline.
	printPresetGroup("Enabled", rows, true)
	printPresetGroup("Disabled", rows, false)
	return nil
}

type classifiedPreset struct {
	name        string
	description string
	state       presetState
}

func printPresetGroup(
	heading string,
	rows []classifiedPreset,
	enabled bool,
) {
	var any bool
	for _, r := range rows {
		if r.state.enabled == enabled {
			any = true
			break
		}
	}
	if !any {
		return
	}
	fmt.Printf("%s:\n", heading)
	for _, r := range rows {
		if r.state.enabled != enabled {
			continue
		}
		reason := ""
		if r.state.reason != "" {
			reason = "  (" + r.state.reason + ")"
		}
		fmt.Printf(
			"  %-22s%s\n", r.name, reason)
		fmt.Printf("    %s\n", r.description)
	}
	fmt.Println()
}

// pickPresetSelector returns the agentconfig that owns
// preset selection: the most-specific config that specifies
// either field — local, else project, else global —
// otherwise nil (= default, all enabled). Mirrors
// SelectPresets in the resolver so `presets list` reports
// the same effective state the hook applies.
func pickPresetSelector(
	global, project, local *agentconfig.Config,
) *agentconfig.Config {
	if local.HasPresetSelection() {
		return local
	}
	if project.HasPresetSelection() {
		return project
	}
	if global.HasPresetSelection() {
		return global
	}
	return nil
}

func classify(
	name string, sel *agentconfig.Config,
) presetState {
	if sel == nil {
		return presetState{enabled: true}
	}
	if sel.DisabledPresets != nil {
		for _, n := range *sel.DisabledPresets {
			if n == name {
				return presetState{
					enabled: false,
					reason:  "in disabled-presets",
				}
			}
		}
	}
	if sel.EnabledPresets != nil {
		for _, n := range *sel.EnabledPresets {
			if n == name {
				return presetState{
					enabled: true,
					reason:  "in enabled-presets",
				}
			}
		}
		return presetState{
			enabled: false,
			reason:  "not in enabled-presets",
		}
	}
	return presetState{enabled: true}
}

// loadAgentConfig reads the file at path and returns
// (config, displayPath, err). A missing file produces a
// nil config — callers treat that as "no opinion from this
// source", matching the hook's behaviour.
func loadAgentConfig(
	pathFn func() (string, error),
) (*agentconfig.Config, string, error) {
	path, err := pathFn()
	if err != nil {
		return nil, "", err
	}
	c, err := agentconfig.Load(path)
	if err != nil {
		return nil, "", err
	}
	return c, path, nil
}

func globalConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home: %v", err)
	}
	return filepath.Join(
		home, ".agents", "permissions.json"), nil
}

func projectConfigPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cwd: %v", err)
	}
	return filepath.Join(
		cwd, ".agents", "permissions.json"), nil
}

// localConfigPath is the project-scoped override that sits
// above the committed project config; project-only, with no
// global counterpart (mirroring Claude's settings.local.json).
func localConfigPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cwd: %v", err)
	}
	return filepath.Join(
		cwd, ".agents", "permissions.local.json"), nil
}

func describeConfig(
	path string, c *agentconfig.Config,
) string {
	if c == nil {
		return path + " (not present)"
	}
	bits := []string{}
	if c.EnabledPresets != nil {
		bits = append(bits,
			"enabled-presets")
	}
	if c.DisabledPresets != nil {
		bits = append(bits,
			"disabled-presets")
	}
	if len(bits) == 0 {
		return path + " (no preset selection)"
	}
	return fmt.Sprintf(
		"%s (%s)", path, joinComma(bits))
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
