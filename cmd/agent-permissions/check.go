package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/breakdown"
	"github.com/sothatsit/agent-permissions/internal/perms"
	"github.com/sothatsit/agent-permissions/internal/rules"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// check simulates the hook on a given bash command and
// prints the decision plus the resolution chain that
// produced it. Useful for "why is this prompting?"
// debugging.
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
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("home: %v", err)
		}
		configDir = filepath.Join(home, ".claude")
	}

	registry, snippetRules := rules.Registry()
	resolved, err := perms.Resolve(configDir, cwd)
	if err != nil {
		return err
	}
	// Prune disabled declarative rules so check evaluates
	// against the same effective registry the hook does.
	rules.FilterByConfig(
		registry, snippetRules, resolved.RuleConfig)
	resolved.Permissions.Rules = registry
	resolved.Permissions.SnippetRules = snippetRules

	// Same rule config the hook resolves (presets + .agents).
	br, brErr := breakdown.Breakdown(
		cmd, cwd, registry, resolved.RuleConfig)

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
		fmt.Println("Reason:")
		fmt.Printf("  breakdown error: %s\n",
			breakdownDenialReason(brErr))
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
	if result.Reason != "" {
		fmt.Println()
		fmt.Println("Reasons:")
		for _, line := range strings.Split(
			strings.TrimRight(result.Reason, "\n"),
			"\n",
		) {
			fmt.Printf("  %s\n", line)
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
