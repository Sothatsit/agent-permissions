package perms

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/presets"
)

// Alias lets the command Name also take Target's permission entries, for a
// wrapper like podman-run that passes its arguments through to podman. Source
// names the preset or config file that declared it.
type Alias struct {
	Name   string
	Target string
	Source string
}

// takesAliasRules reports whether an alias can take a target with these rules.
// Global-option stripping keeps the command itself, so the aliased command
// still reaches its own entries. Anything else, like a rule tree or a wrapper's
// breakdown, would decide or replace the command before its own entries could
// see it, and so could hide a denial written for it.
func takesAliasRules(cr *model.CommandRules) bool {
	if cr == nil {
		return true
	}

	rest := *cr
	rest.GlobalOptions = nil
	rest.Unverified = nil
	rest.PathMode = model.PathDeny
	return reflect.ValueOf(rest).IsZero()
}

// aliasProblem says why an alias cannot be honoured, or returns "".
func aliasProblem(
	name, target string,
	registry map[string]*model.CommandRules,
) string {
	for _, command := range []string{name, target} {
		if command == "" ||
			strings.ContainsAny(command, "/ \t\n") {
			return fmt.Sprintf(
				"%q is not a bare command name", command)
		}
	}

	if name == target {
		return "aliases a command to itself"
	}
	if registry[name] != nil {
		return fmt.Sprintf(
			"%s has rules of its own, which an alias would "+
				"replace", name)
	}
	if !takesAliasRules(registry[target]) {
		return fmt.Sprintf(
			"%s has rules beyond global options, which "+
				"an alias cannot take", target)
	}

	return ""
}

// parseAliases checks one source's aliases, in name order so warnings are
// stable, and leaves out each one it cannot honour with a warning.
func parseAliases(
	warnings []ConfigWarning,
	source string,
	entries map[string]string,
	registry map[string]*model.CommandRules,
) ([]Alias, []ConfigWarning) {
	var aliases []Alias
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		target := entries[name]
		if problem := aliasProblem(
			name, target, registry,
		); problem != "" {
			warnings = append(warnings, ConfigWarning{
				Source: source,
				Entry:  name + " -> " + target,
				Reason: problem,
			})
			continue
		}

		aliases = append(aliases, Alias{
			Name:   name,
			Target: target,
			Source: source,
		})
	}

	return aliases, warnings
}

// resolveAliases merges every source's aliases by name, the way Rules config
// resolves. Ordinary presets form the base, then global, project, and local
// .agents config override it. Enforced presets apply last, so no config can
// redirect an alias that site policy depends on.
//
// Aliases do not chain. A target that is itself an alias contributes only its
// own entries, and the link is reported so the author can name the final
// target directly.
func resolveAliases(
	agentConfigs []AgentConfigSource,
	selected []*presets.Preset,
	registry map[string]*model.CommandRules,
) (map[string]Alias, []ConfigWarning) {
	aliases := map[string]Alias{}
	var warnings []ConfigWarning
	add := func(source string, entries map[string]string) {
		var parsed []Alias
		parsed, warnings = parseAliases(
			warnings, source, entries, registry)
		for _, alias := range parsed {
			aliases[alias.Name] = alias
		}
	}

	for i := len(selected) - 1; i >= 0; i-- {
		if !selected[i].Enforced {
			add(presetSourceName(selected[i]),
				selected[i].Aliases)
		}
	}

	// agentConfigs runs from the most specific file to the least.
	for i := len(agentConfigs) - 1; i >= 0; i-- {
		add(agentConfigs[i].SourceName,
			agentConfigs[i].Config.Aliases)
	}

	for i := len(selected) - 1; i >= 0; i-- {
		if selected[i].Enforced {
			add(presetSourceName(selected[i]),
				selected[i].Aliases)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(aliases)) {
		alias := aliases[name]
		if _, chained := aliases[alias.Target]; !chained {
			continue
		}

		warnings = append(warnings, ConfigWarning{
			Source: alias.Source,
			Entry:  alias.Name + " -> " + alias.Target,
			Reason: fmt.Sprintf(
				"%s is itself an alias, and aliases do "+
					"not chain", alias.Target),
		})
	}

	return aliases, warnings
}
