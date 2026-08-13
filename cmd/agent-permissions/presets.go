package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/agentconfig"
	"github.com/sothatsit/agent-permissions/internal/perms"
	"github.com/sothatsit/agent-permissions/presets"
)

// runPresetsCommand dispatches the `presets` subcommand group.
// `list` is the only subcommand today; users opt presets
// in or out by hand-editing `enabled-presets` /
// `disabled-presets` in ~/.agents/permissions.json. The
// previous `enable` / `disable` subcommands were removed
// because "all enabled by default" made `enable` a
// no-op-with-a-misleading-success-message in the common
// case.
func runPresetsCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(
			"usage: agent-permissions presets list")
	}
	switch args[0] {
	case "list":
		return listPresets(args[1:])
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

// listPresets shows every available preset and its state,
// considering global, project, and local permissions.json
// files with the same priority as the hook.
func listPresets(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: presets list")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cwd: %v", err)
	}
	snapshot, err := perms.LoadPolicySnapshot(cwd)
	if err != nil {
		return err
	}

	global := snapshot.AgentConfig(perms.AgentConfigGlobal)
	project := snapshot.AgentConfig(perms.AgentConfigProject)
	local := snapshot.AgentConfig(perms.AgentConfigLocal)

	selecting := pickPresetSelector(
		global.Config, project.Config, local.Config)

	// Show which file owns preset selection so this view matches the hook.
	fmt.Println("Configs:")
	fmt.Printf(
		"  ~ %s\n",
		describeConfig(global.Path, global.Config))
	fmt.Printf(
		"  cwd: %s\n",
		describeConfig(project.Path, project.Config))
	fmt.Printf(
		"  cwd-local: %s\n",
		describeConfig(local.Path, local.Config))
	if dirs := os.Getenv(
		presets.PresetDirsEnv); dirs != "" {
		fmt.Printf(
			"  %s: %s\n", presets.PresetDirsEnv, dirs)
	}
	if dirs := os.Getenv(
		presets.EnforcedPresetDirsEnv); dirs != "" {
		fmt.Printf("  %s: %s\n",
			presets.EnforcedPresetDirsEnv, dirs)
	}
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

	// Classify every available preset. Enforced presets are
	// always active; ordinary external and embedded presets
	// follow the user's selection.
	all := snapshot.Presets()
	rows := make([]classifiedPreset, 0, len(all))
	for _, p := range all {
		rows = append(rows, classifiedPreset{
			name:        p.Name,
			dir:         p.Dir,
			enforced:    p.Enforced,
			description: p.Description,
			state:       classifyPreset(p, selecting),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].name < rows[j].name
	})

	printPresetGroup(enforcedPresetGroup, rows)
	printPresetGroup(enabledPresetGroup, rows)
	printPresetGroup(disabledPresetGroup, rows)
	return nil
}

type classifiedPreset struct {
	name        string
	dir         string
	enforced    bool
	description string
	state       presetState
}

type presetGroup int

const (
	enforcedPresetGroup presetGroup = iota
	enabledPresetGroup
	disabledPresetGroup
)

func printPresetGroup(
	group presetGroup, rows []classifiedPreset,
) {
	var hasRows bool
	for _, r := range rows {
		if r.group() == group {
			hasRows = true
			break
		}
	}

	if !hasRows {
		return
	}

	fmt.Printf("%s:\n", group)
	for _, r := range rows {
		if r.group() != group {
			continue
		}

		reason := ""
		if r.state.reason != "" {
			reason = "  (" + r.state.reason + ")"
		}

		fmt.Printf(
			"  %-22s%s\n", r.name, reason)
		fmt.Printf("    %s\n", r.description)
		if r.dir != "" {
			fmt.Printf("    from: %s\n", r.dir)
		}
	}

	fmt.Println()
}

func (p classifiedPreset) group() presetGroup {
	if p.enforced {
		return enforcedPresetGroup
	}

	if p.state.enabled {
		return enabledPresetGroup
	}

	return disabledPresetGroup
}

func (g presetGroup) String() string {
	switch g {
	case enforcedPresetGroup:
		return "Enforced"
	case enabledPresetGroup:
		return "Enabled"
	case disabledPresetGroup:
		return "Disabled"
	default:
		panic(fmt.Sprintf("unknown preset group %d", g))
	}
}

func classifyPreset(
	p *presets.Preset, sel *agentconfig.Config,
) presetState {
	if p.Enforced {
		return presetState{
			enabled: true,
			reason:  "always active",
		}
	}

	return classify(p.Name, sel)
}

// pickPresetSelector returns the most-specific config that specifies preset
// selection. It checks local, then project, then global. It returns nil when
// none has an opinion. This mirrors the resolver's selection so the preset
// list reports the same effective state the hook applies.
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
		"%s (%s)", path, strings.Join(bits, ", "))
}
