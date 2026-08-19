package rules

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
)

// FilterByConfig makes the declarative layers honour per-rule
// config by removing everything a disabled rule governs before
// evaluation. Registry() returns fresh structures on every call, so
// these edits touch no shared state:
//
//   - Prune a rule-tree node whose Def is disabled, taking its whole subtree.
//   - Nil a command's fail-closed Default when Unverified is disabled.
//   - Remove a Breakdown and its Parser when BreakdownDef is disabled.
//   - Drop a disabled SnippetLang so that language is no longer scanned.
//
// After this filter the permissions layer needs no rule config of
// its own. Imperative breakdown funcs still gate themselves on
// State.RuleConfig at runtime.
func FilterByConfig(
	registry map[string]*model.CommandRules,
	snippets map[string]*model.SnippetLang,
	rc model.RuleConfigs,
) {
	for _, cr := range registry {
		if cr.Unverified != nil &&
			!rc.For(cr.Unverified).Enabled {
			cr.Default = nil
		}
		if cr.BreakdownDef != nil &&
			!rc.For(cr.BreakdownDef).Enabled {
			cr.Parser = nil
			cr.Breakdown = nil
		}

		cr.Rules = filterRules(cr.Rules, rc)
	}

	for lang, sl := range snippets {
		if sl.Def != nil && !rc.For(sl.Def).Enabled {
			delete(snippets, lang)
		}
	}
}

// filterRules drops a node carrying a disabled Def with its whole subtree, and
// keeps recursing through kept nodes so a disabled rule nested under an enabled
// parent is still pruned.
func filterRules(
	in []model.Rule, rc model.RuleConfigs,
) []model.Rule {
	var out []model.Rule
	for i := range in {
		r := in[i]
		if r.Def != nil && !rc.For(r.Def).Enabled {
			continue
		}

		r.Children = filterRules(r.Children, rc)
		out = append(out, r)
	}

	return out
}

// ValidateRegistry asserts the registry's structural and attribution
// invariants: a Parser must belong to a Breakdown, and every node that can
// deny, ask, or soft-ask must have a governing RuleDef on its path so the
// decision can be named and disabled. The registry is static, so a violation is
// a coding mistake, and this check stays off the hook path.
func ValidateRegistry(
	registry map[string]*model.CommandRules,
	snippets map[string]*model.SnippetLang,
) error {
	var problems []string

	cmds := make([]string, 0, len(registry))
	for name := range registry {
		cmds = append(cmds, name)
	}

	sort.Strings(cmds)
	for _, name := range cmds {
		cr := registry[name]
		if cr.Parser != nil && cr.Breakdown == nil {
			problems = append(problems, fmt.Sprintf(
				"%s: Parser has no Breakdown", name))
		}
		// A command's Default is governed by its Unverified rule, so a
		// restrictive Default without one could never be disabled.
		if cr.Default != nil &&
			cr.Default.Decision >= model.SoftAsk &&
			cr.Unverified == nil {
			problems = append(problems, fmt.Sprintf(
				"%s: restrictive Default has no "+
					"Unverified rule", name))
		}

		problems = append(problems,
			validateRules(name, cr.Rules, false)...)
	}

	langs := make([]string, 0, len(snippets))
	for lang := range snippets {
		langs = append(langs, lang)
	}

	sort.Strings(langs)
	for _, lang := range langs {
		sl := snippets[lang]
		if len(sl.Rules) > 0 && sl.Def == nil {
			problems = append(problems, fmt.Sprintf(
				"snippet lang %q has rules but no Def",
				lang))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf(
			"registry violations:\n  %s",
			strings.Join(problems, "\n  "))
	}

	return nil
}

// validateRules walks a subtree. hasDef covers this node or any ancestor,
// because the evaluator inherits the nearest ancestor's def.
func validateRules(
	path string, rules []model.Rule, hasDef bool,
) []string {
	var problems []string
	for i := range rules {
		r := &rules[i]
		nodeHasDef := hasDef || r.Def != nil
		if !nodeHasDef {
			if r.Action != nil &&
				r.Action.Decision >= model.SoftAsk {
				problems = append(problems, fmt.Sprintf(
					"%s: restrictive Action has no "+
						"governing rule Def", path))
			}
			if r.Hook != nil {
				problems = append(problems, fmt.Sprintf(
					"%s: Hook has no governing rule "+
						"Def", path))
			}
			if r.Default != nil &&
				r.Default.Decision >= model.SoftAsk {
				problems = append(problems, fmt.Sprintf(
					"%s: restrictive Default has no "+
						"governing rule Def", path))
			}
		}

		problems = append(problems,
			validateRules(path, r.Children, nodeHasDef)...)
	}

	return problems
}
