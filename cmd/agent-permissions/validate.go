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

// validate loads every permission source and reports these
// classes of issue:
//
//   - Malformed entries: rejected at load time and not
//     contributing to the policy. A hard error (exit 2 via
//     main) so CI fails on these.
//   - Unknown rule IDs or preset names in a user .agents
//     config: a typo (e.g. "git.branch-writs", or a
//     misspelled preset name) that silently no-ops. Also a
//     hard error, since the rule or preset the user meant to
//     configure is then not actually being configured.
//   - Attempts to put an enforced preset in
//     disabled-presets. Selection ignores the entry, so
//     reporting it avoids a silent failed override.
//   - Empty-reason entries: load fine but carry no
//     description. Surfaced as an informational note; the
//     exit code stays 0 because empty reasons are allowed by
//     design.
//   - Preset names supplied by more than one origin: also a
//     note, for the same reason. Both presets stay active,
//     but attribution can no longer name the directory.
//
// All problems are collected and reported in one pass.
// Validate never bails on the first error, and the exit code
// is decided only at the end.
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

	snapshot, err := perms.LoadPolicySnapshot(cwd)
	if err != nil {
		return err
	}
	resolved, err := snapshot.Resolve(configDir)
	if err != nil {
		return err
	}
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

	// A name supplied by two origins loads fine and both stay
	// active, so this is a note rather than a problem. It is
	// worth reporting because attribution then names only the
	// preset, leaving the output unable to say which directory
	// a decision came from.
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

	// Collect every hard problem before deciding the exit
	// code, so one run reports them all.
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

// unknownRef is a rule ID or preset name in a user .agents
// config that doesn't match anything in the catalog - a typo
// that would otherwise silently no-op.
type unknownRef struct {
	source string
	value  string
}

// printUnknownRefs prints a "Found N unknown <kind>s" block
// naming each typo'd value and pointing at the catalog
// command that lists the valid names. No-op when refs is
// empty.
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

// collectUnknownRules returns rule IDs in the user .agents
// configs that are not known catalog rules - typos that would
// otherwise silently no-op. External presets are checked when
// they load; the embedded-preset invariant covers shipped IDs.
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

// collectUnknownPresets returns preset names referenced by
// enabled-presets / disabled-presets in the user .agents
// configs that don't match any active preset (embedded or
// external). A typo there silently no-ops (filterByName
// just never matches it) - the same failure mode
// collectUnknownRules guards for rule IDs.
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

// collectDisabledEnforcedPresets returns enforced names in
// disabled-presets. Selection deliberately ignores them, so
// validate reports the otherwise silent failed override.
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

// presetSelectionNames returns every preset name the config
// references via enabled-presets or disabled-presets; a typo
// in either silently no-ops, so both are validated.
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
