package perms

import (
	"testing"

	"github.com/sothatsit/agent-permissions/internal/agentconfig"
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/rules"
	"github.com/sothatsit/agent-permissions/presets"
)

// presetRuleOwners maps each rule ID mentioned by any preset
// to the presets that mention it. Built from the embedded
// presets so the invariants below test the shipped data.
func presetRuleOwners() map[string][]string {
	owners := map[string][]string{}
	for _, p := range presets.MustEmbedded() {
		for id := range p.Rules {
			owners[id] = append(owners[id], p.Name)
		}
	}
	return owners
}

// Every rule ID a preset enables must be a real catalog rule.
// A typo here would silently enable nothing, leaving the
// command's denial off with no error.
func TestPresetRuleIDsAreValid(t *testing.T) {
	for id, owners := range presetRuleOwners() {
		if !rules.IsRuleID(id) {
			t.Errorf(
				"preset(s) %v enable unknown rule %q",
				owners, id)
		}
	}
}

// Every catalog rule must be owned by exactly one preset.
// Owned by zero → the rule ships permanently off (default
// install stops denying it). Owned by two → ambiguous
// ownership, and disabling one preset wouldn't fully turn it
// off.
func TestEveryRuleOwnedByExactlyOnePreset(t *testing.T) {
	owners := presetRuleOwners()
	for _, def := range rules.AllRules() {
		switch n := len(owners[def.ID]); {
		case n == 0:
			t.Errorf(
				"rule %q is enabled by no preset — "+
					"it would ship permanently off",
				def.ID)
		case n > 1:
			t.Errorf(
				"rule %q is enabled by %d presets %v "+
					"— ownership must be exactly one",
				def.ID, n, owners[def.ID])
		}
	}
}

// A default install (all presets, no user config) must
// resolve every catalog rule to Enabled — matching the
// behaviour before rules were configurable.
func TestDefaultInstallEnablesEveryRule(t *testing.T) {
	rc := resolveRuleConfig(
		nil, nil, presets.MustEmbedded())
	for _, def := range rules.AllRules() {
		if !rc.For(def).Enabled {
			t.Errorf(
				"rule %q not enabled by default install",
				def.ID)
		}
	}
}

// A user .agents override wins over the preset that enabled
// the rule, and project beats global.
func TestRuleConfigOverridePrecedence(t *testing.T) {
	all := presets.MustEmbedded()
	const id = "git.branch-writes"
	def := &model.RuleDef{ID: id}

	global := &agentconfig.Config{
		Rules: map[string]model.RuleConfig{
			id: {Enabled: false},
		},
	}
	if resolveRuleConfig(nil, global, all).For(def).Enabled {
		t.Error("global .agents Enabled:false should " +
			"override the preset enable")
	}

	project := &agentconfig.Config{
		Rules: map[string]model.RuleConfig{
			id: {Enabled: true},
		},
	}
	if !resolveRuleConfig(
		project, global, all).For(def).Enabled {
		t.Error("project .agents should override global")
	}
}
