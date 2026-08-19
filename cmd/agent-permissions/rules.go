package main

import (
	"fmt"

	"github.com/sothatsit/agent-permissions/internal/rules"
)

// runRulesCommand dispatches the `rules` subcommand group, where `list` is the
// only subcommand.
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

// listRules prints every built-in rule as "<id> - <description>" in declaration
// order. It resolves no config, so the output does not depend on cwd.
func listRules(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: rules list")
	}

	for _, r := range rules.AllRules() {
		fmt.Printf("%s - %s\n", r.ID, r.Description)
	}

	return nil
}
