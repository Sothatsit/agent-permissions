package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sothatsit/agent-permissions/internal/perms"
)

// validate loads every permission source and reports two
// classes of issue:
//
//   - Malformed entries: rejected at load time and not
//     contributing to the policy. Returns an error
//     (exit 2 via main) so CI fails on these.
//   - Empty-reason entries: load fine but carry no
//     description. Surfaced as informational warnings;
//     the exit code stays at 0 because empty reasons
//     are allowed by design — useful for users who
//     don't want to write a reason in their own config.
func validate(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf(
			"usage: agent-permissions validate")
	}

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

	resolved, err := perms.Resolve(configDir, cwd)
	if err != nil {
		return err
	}

	emptyReasons := collectEmptyReasons(
		resolved.Permissions)
	if len(emptyReasons) > 0 {
		fmt.Printf(
			"Note: %d %s without a reason:\n",
			len(emptyReasons),
			plural(len(emptyReasons),
				"entry", "entries"))
		for _, e := range emptyReasons {
			fmt.Printf("  %s: %q\n", e.source, e.entry)
		}
		fmt.Println()
	}

	warnings := resolved.Permissions.Warnings
	if len(warnings) == 0 {
		fmt.Println("OK. No malformed entries.")
		return nil
	}

	fmt.Printf(
		"Found %d malformed %s:\n",
		len(warnings),
		plural(len(warnings), "entry", "entries"))
	for _, w := range warnings {
		fmt.Printf(
			"  %s: %q (%s)\n",
			w.Source, w.Entry, w.Reason)
	}
	return fmt.Errorf(
		"%d malformed %s",
		len(warnings),
		plural(len(warnings), "entry", "entries"))
}

type emptyReason struct {
	source string
	entry  string
}

func collectEmptyReasons(
	p *perms.Permissions,
) []emptyReason {
	var out []emptyReason
	for _, src := range p.Sources {
		if !src.AcceptsReasons {
			continue
		}
		for _, tier := range []perms.TierEntries{
			src.Allow, src.SoftAsk, src.Ask, src.Deny,
		} {
			for _, pat := range tier.Commands {
				if pat.Reason == "" {
					out = append(out, emptyReason{
						source: src.Name,
						entry:  pat.Raw,
					})
				}
			}
			for _, pat := range tier.EnvVars {
				if pat.Reason == "" {
					out = append(out, emptyReason{
						source: src.Name,
						entry:  pat.Raw,
					})
				}
			}
		}
	}
	return out
}

func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}
