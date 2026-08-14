package main

import (
	"fmt"

	"github.com/sothatsit/agent-permissions/internal/rules"
)

// runRulesCommand dispatches the `rules` subcommand group. `list` is the only
// subcommand. It prints the static rule catalog so users can discover the IDs
// to put in a `Rules` config.
func runRulesCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(
			"usage: agent-permissions rules list")
	}

	switch args[0] {
	case "list":
		return listRules(args[1:])
	default:
		return fmt.Errorf(
			"unknown rules subcommand: %s "+
				"(only `list` is supported)",
			args[0])
	}
}

// listRules prints every built-in rule as "<id> - <description>", one per line,
// in declaration order. It is a static catalog with no config resolution or
// enabled/disabled state, so the output is the same regardless of cwd or
// config. Use the ID in a `Rules` entry in .agents/permissions.json to enable
// or disable a rule.
func listRules(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: rules list")
	}

	for _, r := range rules.AllRules() {
		fmt.Printf("%s - %s\n", r.ID, r.Description)
	}

	return nil
}
