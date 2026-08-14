package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/perms"
	"github.com/sothatsit/agent-permissions/presets"
)

// runPresetsCommand dispatches the `presets` subcommand group. `list` is the
// only subcommand today; users opt presets in or out by hand-editing
// `enabled-presets` / `disabled-presets` in ~/.agents/permissions.json. The
// previous `enable` / `disable` subcommands were removed because
// "all enabled by default" made `enable` a
// no-op-with-a-misleading-success-message in the common case.
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

// listPresets shows every available preset and its state, considering global,
// project, and local permissions.json files with the same priority as the hook.
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
	selection := snapshot.PresetSelection()

	// Show which file owns preset selection so this view matches the hook.
	fmt.Println("Configs:")
	fmt.Printf(
		"  ~ %s\n",
		describeConfig(global))
	fmt.Printf(
		"  cwd: %s\n",
		describeConfig(project))
	fmt.Printf(
		"  cwd-local: %s\n",
		describeConfig(local))
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

	if selection.SelectorPath == "" {
		fmt.Println(
			"  Preset selection: (none — all " +
				"presets enabled by default)")
	} else {
		fmt.Printf(
			"  Preset selection: %s\n",
			selection.SelectorPath)
	}

	fmt.Println()

	rows := selection.Presets
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Preset.Name < rows[j].Preset.Name
	})

	printPresetGroup(enforcedPresetGroup, rows)
	printPresetGroup(enabledPresetGroup, rows)
	printPresetGroup(disabledPresetGroup, rows)
	return nil
}

type presetGroup int

const (
	enforcedPresetGroup presetGroup = iota
	enabledPresetGroup
	disabledPresetGroup
)

func printPresetGroup(
	group presetGroup, rows []perms.PolicyPreset,
) {
	var hasRows bool
	for _, r := range rows {
		if presetGroupFor(r) == group {
			hasRows = true
			break
		}
	}

	if !hasRows {
		return
	}

	fmt.Printf("%s:\n", group)
	for _, r := range rows {
		if presetGroupFor(r) != group {
			continue
		}

		reason := ""
		if stateReason := presetStateReason(r.State); stateReason != "" {
			reason = "  (" + stateReason + ")"
		}

		fmt.Printf(
			"  %-22s%s\n", r.Preset.Name, reason)
		fmt.Printf("    %s\n", r.Preset.Description)
		if r.Preset.Dir != "" {
			fmt.Printf("    from: %s\n", r.Preset.Dir)
		}
	}

	fmt.Println()
}

func presetGroupFor(p perms.PolicyPreset) presetGroup {
	if p.State == perms.PresetEnforced {
		return enforcedPresetGroup
	}

	if p.Active() {
		return enabledPresetGroup
	}

	return disabledPresetGroup
}

func presetStateReason(state perms.PresetState) string {
	switch state {
	case perms.PresetEnforced:
		return "always active"
	case perms.PresetEnabledByDefault:
		return ""
	case perms.PresetEnabledByName:
		return "in enabled-presets"
	case perms.PresetDisabledByName:
		return "in disabled-presets"
	case perms.PresetDisabledByOmission:
		return "not in enabled-presets"
	default:
		panic(fmt.Sprintf("unknown preset state %d", state))
	}
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

func describeConfig(source perms.AgentConfigSource) string {
	path := source.Path
	c := source.Config
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
