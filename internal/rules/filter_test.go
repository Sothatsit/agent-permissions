package rules

import (
	"testing"

	"github.com/sothatsit/agent-permissions/internal/model"
)

// A node whose Def is disabled is pruned with its subtree; an
// enabled one is kept.
func TestFilterByConfigPrunesDisabledNode(t *testing.T) {
	def := &model.RuleDef{ID: "x.test"}
	build := func() map[string]*model.CommandRules {
		return map[string]*model.CommandRules{
			"foo": {Rules: []model.Rule{
				model.Flag("--danger").
					WithRuleDef(def).Deny("bad"),
			}},
		}
	}

	kept := build()
	FilterByConfig(kept, nil,
		model.RuleConfigs{"x.test": {Enabled: true}})
	if len(kept["foo"].Rules) != 1 {
		t.Fatal("enabled rule should be kept")
	}

	pruned := build()
	FilterByConfig(pruned, nil, model.RuleConfigs{})
	if len(pruned["foo"].Rules) != 0 {
		t.Fatal("disabled rule should be pruned")
	}
}

// A disabled rule nested under an enabled parent is still
// pruned, while the parent and its other children survive.
func TestFilterByConfigPrunesNestedDisabledNode(t *testing.T) {
	parent := &model.RuleDef{ID: "p"}
	child := &model.RuleDef{ID: "c"}
	reg := map[string]*model.CommandRules{
		"foo": {Rules: []model.Rule{
			model.Subcmd("sub").WithRuleDef(parent).Rules(
				model.Flag("-x").
					WithRuleDef(child).Deny("bad"),
				model.Flag("-y").Deny("kept"),
			),
		}},
	}
	FilterByConfig(reg, nil,
		model.RuleConfigs{"p": {Enabled: true}})
	sub := reg["foo"].Rules
	if len(sub) != 1 {
		t.Fatalf("parent should survive, got %d", len(sub))
	}
	if len(sub[0].Children) != 1 {
		t.Fatalf("disabled child pruned, sibling kept; "+
			"got %d children", len(sub[0].Children))
	}
}

// A command's Default is nil'd when its Unverified rule is
// disabled, and kept when enabled.
func TestFilterByConfigNilsDefault(t *testing.T) {
	unv := &model.RuleDef{ID: "foo.unverified"}
	build := func() map[string]*model.CommandRules {
		return map[string]*model.CommandRules{
			"foo": {
				Default:    model.DenyAction("cannot verify"),
				Unverified: unv,
			},
		}
	}

	off := build()
	FilterByConfig(off, nil, model.RuleConfigs{})
	if off["foo"].Default != nil {
		t.Fatal("disabled Unverified should nil the Default")
	}

	on := build()
	FilterByConfig(on, nil,
		model.RuleConfigs{"foo.unverified": {Enabled: true}})
	if on["foo"].Default == nil {
		t.Fatal("enabled Unverified should keep the Default")
	}
}

// Removing a disabled Breakdown removes its parser too, preserving the
// parser-ownership invariant in the filtered registry.
func TestFilterByConfigNilsBreakdownAndParser(t *testing.T) {
	def := &model.RuleDef{ID: "foo.breakdown"}
	reg := map[string]*model.CommandRules{
		"foo": {
			Parser: rawParser{},
			Breakdown: func(
				model.ParseResult, *model.State,
			) (*model.UnwrapResult, error) {
				return nil, nil
			},
			BreakdownDef: def,
		},
	}

	FilterByConfig(reg, nil, model.RuleConfigs{})
	if reg["foo"].Breakdown != nil {
		t.Fatal("disabled Breakdown should be removed")
	}
	if reg["foo"].Parser != nil {
		t.Fatal("disabled Breakdown's Parser should be removed")
	}
}

// A disabled language's snippet rules are dropped entirely so
// the permissions layer scans nothing for it.
func TestFilterByConfigDropsDisabledSnippetLang(t *testing.T) {
	def := &model.RuleDef{ID: "lang.test"}
	snips := map[string]*model.SnippetLang{
		"python": {Def: def},
	}
	FilterByConfig(
		map[string]*model.CommandRules{}, snips,
		model.RuleConfigs{})
	if _, ok := snips["python"]; ok {
		t.Fatal("disabled snippet lang should be dropped")
	}
}

// The shipped registry must satisfy the attribution
// invariant: every restrictive decision can be named and
// disabled.
func TestValidateRegistryPassesOnRealRegistry(t *testing.T) {
	reg, snips := Registry()
	if err := ValidateRegistry(reg, snips); err != nil {
		t.Fatalf("real registry should pass: %v", err)
	}
}

// Parsing belongs to breakdown so parser failures follow the same configurable
// error path.
func TestValidateRegistryCatchesParserWithoutBreakdown(t *testing.T) {
	reg := map[string]*model.CommandRules{
		"foo": {Parser: rawParser{}},
	}
	if err := ValidateRegistry(reg, nil); err == nil {
		t.Fatal("Parser without Breakdown should fail")
	}
}

// A restrictive node with no governing Def is the mistake the
// invariant exists to catch.
func TestValidateRegistryCatchesMissingNodeDef(t *testing.T) {
	reg := map[string]*model.CommandRules{
		"foo": {Rules: []model.Rule{
			model.Flag("--danger").Deny("bad"),
		}},
	}
	if err := ValidateRegistry(reg, nil); err == nil {
		t.Fatal("restrictive node without Def should fail")
	}
}

// A restrictive command Default with no Unverified rule could
// never be disabled.
func TestValidateRegistryCatchesDefaultWithoutUnverified(t *testing.T) {
	reg := map[string]*model.CommandRules{
		"foo": {Default: model.DenyAction("cannot verify")},
	}
	if err := ValidateRegistry(reg, nil); err == nil {
		t.Fatal("restrictive Default without Unverified " +
			"should fail")
	}
}

// A snippet language with rules but no Def is unattributable.
func TestValidateRegistryCatchesSnippetLangWithoutDef(t *testing.T) {
	snips := map[string]*model.SnippetLang{
		"python": {Rules: []model.SnippetRule{
			{Check: func(string) bool { return true },
				Action: model.DenyAction("x")},
		}},
	}
	err := ValidateRegistry(
		map[string]*model.CommandRules{}, snips)
	if err == nil {
		t.Fatal("snippet lang with rules but no Def " +
			"should fail")
	}
}
