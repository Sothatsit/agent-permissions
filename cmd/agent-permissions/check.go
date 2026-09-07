package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/perms"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// check simulates the hook on a bash command and prints the decision with the
// resolution chain that produced it, for "why is this prompting?" debugging.
func check(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf(
			"usage: agent-permissions check '<command>'")
	}

	cmd := args[0]

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cwd: %v", err)
	}

	configDir, err := resolveClaudeConfigDir()
	if err != nil {
		return err
	}

	resolved, err := perms.Resolve(configDir, cwd)
	if err != nil {
		return err
	}

	br, brErr := resolved.Breakdown(cmd)

	fmt.Println("Command:")
	fmt.Printf("  %s\n", cmd)
	fmt.Println()

	fmt.Println("Enforced policy (strongest match wins):")
	if len(resolved.Permissions.EnforcedSources) == 0 {
		fmt.Println("  (none)")
	}

	for _, s := range resolved.Permissions.EnforcedSources {
		fmt.Printf("  %s\n", s.Name)
	}

	fmt.Println()

	fmt.Println("Normal resolution chain " +
		"(highest → lowest priority):")
	for _, s := range resolved.Permissions.Sources {
		fmt.Printf("  %s\n", s.Name)
	}

	fmt.Println()

	if brErr != nil {
		fmt.Println("Decision: deny")
		fmt.Println()
		// The hook wraps a breakdown error like any other deny, so
		// wrap it the same way here. check exists to predict the
		// hook, and a prefix of its own misreports what the agent
		// will be told.
		fmt.Println(strings.TrimRight(perms.DenyResult(
			breakdownDenialReason(brErr)).Reason, "\n"))
		return nil
	}

	fmt.Println("Extracted commands:")
	if len(br.Commands) == 0 && len(br.CodeSnippets) == 0 {
		fmt.Println("  (none)")
	}

	for _, c := range br.Commands {
		parts := make([]string, len(c.Args))
		for i, a := range c.Args {
			parts[i] = word.Text(a)
		}

		fmt.Printf("  %s\n", strings.Join(parts, " "))
	}

	for _, s := range br.CodeSnippets {
		fmt.Printf(
			"  [%s code snippet]\n", s.Language)
	}

	fmt.Println()

	result := resolved.Permissions.Check(br)
	fmt.Printf("Decision: %s\n", result.Decision)
	// The reason carries its own "Deny:"/"Ask:" headers and bullets, so it
	// goes out as the agent receives it. A header and indent of the
	// report's own would label the same block twice.
	if result.Reason != "" {
		fmt.Println()
		fmt.Println(strings.TrimRight(result.Reason, "\n"))
	}

	// An allow sends the agent nothing, so this block is check's own,
	// in the reason block's bullet form.
	if len(result.Allows) > 0 {
		fmt.Println()
		fmt.Println("Allowed by:")
		for _, l := range result.Allows {
			fmt.Printf("* %s\n", l)
		}
	}

	if len(resolved.Permissions.Warnings) > 0 {
		fmt.Println()
		fmt.Println("Warnings:")
		for _, w := range resolved.Permissions.Warnings {
			fmt.Printf("  %s: %q (%s)\n",
				w.Source, w.Entry, w.Reason)
		}
	}

	return nil
}
