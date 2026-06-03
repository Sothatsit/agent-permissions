package rules

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
)

// FilterByConfig is the registry filter: it makes the
// declarative layers honour per-rule config by removing
// everything a disabled rule governs, before any evaluation
// runs. Three edits, all in place — Registry() returns fresh
// structures on every call, so there is nothing shared to
// corrupt:
//
//   - Prune any rule-tree node whose Def is disabled, taking
//     its whole subtree (the matcher, action, hook, and
//     children all vanish).
//   - Nil a command's Default when its Unverified rule is
//     disabled — that Default is the command's fail-closed
//     denial.
//   - Drop a SnippetLang whose Def is disabled, so the
//     permissions layer scans nothing for that language.
//
// The imperative breakdown funcs are not touched here: they
// receive State.RuleConfig and gate themselves at runtime
// (wrappers, xargs), or return a RuleError that runBreakdown
// suppresses when the rule is off. So after this filter the
// permissions layer needs no rule config of its own — a
// pruned node never matches, a nil'd Default never fires, and
// a dropped language scans clean.
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
		cr.Rules = filterRules(cr.Rules, rc)
	}
	for lang, sl := range snippets {
		if sl.Def != nil && !rc.For(sl.Def).Enabled {
			delete(snippets, lang)
		}
	}
}

// filterRules returns the subset of rules whose governing rule
// is enabled. A node carrying a disabled Def is dropped with
// its whole subtree; a kept node keeps recursing so a disabled
// rule nested under an enabled parent is still pruned.
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

// ValidateRegistry asserts the attribution invariant: every
// node that can produce a restrictive decision (deny, ask, or
// soft-ask) has a governing RuleDef reachable on its path, so
// the decision can be named and disabled. Without it, a
// restrictive node would render "(from rule:)" with no ID, the
// user could not disable it, and the registry filter could not
// prune it — a silent, permanently-on rule. It depends only on
// the static registry, so a violation is a coding mistake; a
// test calls this and the (cheap) check could also be wired
// into `validate`. It is deliberately NOT on the hook path.
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
		// A command's Default is governed by its Unverified
		// rule; a restrictive Default with no Unverified
		// could never be disabled.
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
			"registry attribution violations:\n  %s",
			strings.Join(problems, "\n  "))
	}
	return nil
}

// validateRules walks a rule subtree. hasDef is true when an
// ancestor (or this node) carries a Def, since the evaluator
// inherits the nearest ancestor's def. A restrictive action,
// a hook (which can deny), or a restrictive Default on a node
// with no def on its path is a violation.
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
