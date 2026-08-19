package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/agentconfig"
	"github.com/sothatsit/agent-permissions/internal/perms"
	"github.com/sothatsit/agent-permissions/internal/rules"
	"github.com/sothatsit/agent-permissions/presets"
)

// validate loads every permission source and reports what would
// silently degrade the policy: malformed entries, unknown rule IDs
// or preset names, and enforced presets that a config tries to
// disable. Those are hard errors, so CI fails on them. Empty
// reasons and a preset name supplied by two origins are notes,
// because both load fine by design.
//
// Every problem is collected in one pass, and the exit code is
// decided only at the end.
func validate(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf(
			"usage: agent-permissions validate")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cwd: %v", err)
	}

	configDir, err := resolveClaudeConfigDir()
	if err != nil {
		return err
	}

	snapshot, err := perms.LoadPolicySnapshot(configDir, cwd)
	if err != nil {
		return err
	}

	resolved := snapshot.Resolve()

	all := snapshot.Presets()
	agentConfigs := snapshot.AgentConfigs()

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

	// Both presets stay active, so this is only a note. It is worth
	// reporting because attribution can then name the preset but not
	// the directory a decision came from.
	if dupes := presets.DuplicateNames(all); len(dupes) > 0 {
		fmt.Printf(
			"Note: %d preset %s supplied by more than one "+
				"origin:\n",
			len(dupes), plural(len(dupes), "name", "names"))
		for _, d := range dupes {
			fmt.Printf("  %s: %s\n",
				d.Name, strings.Join(d.Origins, ", "))
		}

		fmt.Println()
	}

	unknownRules := collectUnknownRules(agentConfigs)
	unknownPresets := collectUnknownPresets(agentConfigs, all)
	disabledEnforced := collectDisabledEnforcedPresets(
		agentConfigs, all)
	warnings := resolved.Permissions.Warnings

	total := len(unknownRules) +
		len(unknownPresets) + len(disabledEnforced) +
		len(warnings)
	if total == 0 {
		fmt.Println("OK. No problems found.")
		return nil
	}

	printUnknownRefs(unknownRules,
		"rule ID", "rule IDs", "rule", "rules list")
	printUnknownRefs(unknownPresets,
		"preset name", "preset names",
		"preset", "presets list")
	printDisabledEnforcedPresets(disabledEnforced)

	if len(warnings) > 0 {
		fmt.Printf(
			"Found %d malformed %s:\n",
			len(warnings),
			plural(len(warnings), "entry", "entries"))
		for _, w := range warnings {
			fmt.Printf(
				"  %s: %q (%s)\n",
				w.Source, w.Entry, w.Reason)
		}
	}

	return fmt.Errorf(
		"%d %s", total,
		plural(total, "problem", "problems"))
}

func printDisabledEnforcedPresets(refs []unknownRef) {
	if len(refs) == 0 {
		return
	}

	fmt.Printf(
		"Found %d enforced %s in disabled-presets:\n",
		len(refs), plural(len(refs), "preset", "presets"))
	for _, ref := range refs {
		fmt.Printf("  %s: %q\n", ref.source, ref.value)
	}

	fmt.Println(
		"Enforced presets are always active and cannot be disabled.")
}

// unknownRef is a rule ID or preset name in a user .agents config that doesn't
// match anything in the catalog - a typo that would otherwise silently no-op.
type unknownRef struct {
	source string
	value  string
}

// printUnknownRefs names each typo'd value and points at the command that lists
// the valid names.
func printUnknownRefs(
	refs []unknownRef, singular, plur, kind, listCmd string,
) {
	if len(refs) == 0 {
		return
	}

	fmt.Printf(
		"Found %d unknown %s:\n",
		len(refs), plural(len(refs), singular, plur))
	for _, r := range refs {
		fmt.Printf(
			"  %s: %q (not a known %s — see `%s`)\n",
			r.source, r.value, kind, listCmd)
	}

	fmt.Println()
}

// collectUnknownRules returns rule IDs in the user .agents configs that no
// catalog rule matches. External presets are checked as they load, and the
// embedded-preset invariant covers shipped IDs.
func collectUnknownRules(
	configs []perms.AgentConfigSource,
) []unknownRef {
	var out []unknownRef
	for _, loaded := range configs {
		ids := make([]string, 0, len(loaded.Config.Rules))
		for id := range loaded.Config.Rules {
			ids = append(ids, id)
		}

		sort.Strings(ids)
		for _, id := range ids {
			if !rules.IsRuleID(id) {
				out = append(out, unknownRef{
					source: loaded.SourceName,
					value:  id,
				})
			}
		}
	}

	return out
}

// collectUnknownPresets returns names in enabled-presets or disabled-presets
// that match no available preset, embedded or external.
func collectUnknownPresets(
	configs []perms.AgentConfigSource,
	all []*presets.Preset,
) []unknownRef {
	known := map[string]bool{}
	for _, p := range all {
		known[p.Name] = true
	}

	var out []unknownRef
	for _, loaded := range configs {
		names := presetSelectionNames(loaded.Config)
		sort.Strings(names)
		for _, name := range names {
			if !known[name] {
				out = append(out, unknownRef{
					source: loaded.SourceName,
					value:  name,
				})
			}
		}
	}

	return out
}

// collectDisabledEnforcedPresets returns enforced names in disabled-presets,
// whose failed override would otherwise be silent.
func collectDisabledEnforcedPresets(
	configs []perms.AgentConfigSource,
	all []*presets.Preset,
) []unknownRef {
	enforced := map[string]bool{}
	for _, p := range all {
		if p.Enforced {
			enforced[p.Name] = true
		}
	}

	var out []unknownRef
	for _, loaded := range configs {
		if loaded.Config.DisabledPresets == nil {
			continue
		}

		names := append(
			[]string{}, *loaded.Config.DisabledPresets...)
		sort.Strings(names)
		for _, name := range names {
			if enforced[name] {
				out = append(out, unknownRef{
					source: loaded.SourceName,
					value:  name,
				})
			}
		}
	}

	return out
}

// presetSelectionNames returns every name the config references, from both
// lists, because a typo in either silently no-ops.
func presetSelectionNames(cfg *agentconfig.Config) []string {
	var names []string
	if cfg.EnabledPresets != nil {
		names = append(names, *cfg.EnabledPresets...)
	}
	if cfg.DisabledPresets != nil {
		names = append(names, *cfg.DisabledPresets...)
	}

	return names
}

type emptyReason struct {
	source string
	entry  string
}

func collectEmptyReasons(
	p *perms.Permissions,
) []emptyReason {
	var out []emptyReason
	for _, sources := range [][]perms.SourcePerms{
		p.EnforcedSources, p.Sources,
	} {
		for _, src := range sources {
			if !src.AcceptsReasons {
				continue
			}

			for _, tier := range []perms.TierEntries{
				src.Allow, src.SoftAsk,
				src.Ask, src.Deny,
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
	}

	return out
}

func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}

	return pluralForm
}
