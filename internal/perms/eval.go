package perms

import (
	"fmt"

	"github.com/sothatsit/agent-permissions/internal/model"

	"mvdan.cc/sh/v3/syntax"
)

// Evaluate checks a command against a rules map. Returns
// nil when no rules exist or no rule matched and there is
// no default (fall through to permissions layer).
func Evaluate(
	rules map[string]*model.CommandRules,
	name string,
	args []*syntax.Word,
) *model.Action {
	cr := rules[name]
	if cr == nil {
		return nil
	}

	input := model.ParseResult{Name: name, Raw: args}
	if cr.Parser != nil {
		parsed, err := cr.Parser.Parse(args)
		if err != nil {
			return &model.Action{
				Decision: model.Deny,
				Reason:   err.Error(),
			}
		}
		parsed.Name = name
		input = parsed
	} else {
		model.PopulatePossibleFlags(&input)
	}

	result := evaluateRules(cr.Rules, input, name)
	if result != nil {
		return result
	}
	if cr.Default != nil {
		return formatAction(cr.Default, name)
	}
	return nil
}

func evaluateRules(
	ruleList []model.Rule,
	input model.ParseResult,
	path string,
) *model.Action {
	var strongest *model.Action

	for i := range ruleList {
		rule := &ruleList[i]
		matched, childInput, context :=
			rule.Match.Match(input)
		if !matched {
			continue
		}

		// Extend path with match context (e.g.
		// "git" → "git --upload-pack", or
		// "git" → "git remote" → "git remote add").
		childPath := path
		if context != "" {
			childPath = path + " " + context
		}

		if rule.Action != nil {
			a := formatAction(rule.Action, childPath)
			strongest = stronger(strongest, a)
		}

		if rule.Hook != nil {
			decision, reason := rule.Hook(childInput)
			if decision > model.Undecided {
				strongest = stronger(
					strongest,
					&model.Action{
						Decision: decision,
						Reason:   reason,
					},
				)
			}
		}

		if len(rule.Children) > 0 {
			child := evaluateRules(
				rule.Children, childInput,
				childPath)
			if child != nil {
				strongest = stronger(strongest, child)
			} else if rule.Default != nil {
				d := formatAction(
					rule.Default, childPath)
				strongest = stronger(strongest, d)
			}
		}

		// Deny is highest priority — no subsequent rule
		// can upgrade past it.
		if strongest != nil &&
			strongest.Decision == model.Deny {
			return strongest
		}
	}

	return strongest
}

// stronger returns the action with the higher-priority
// decision. Deny > Ask > SoftAsk > Allow.
func stronger(a, b *model.Action) *model.Action {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if b.Decision > a.Decision {
		return b
	}
	return a
}

// formatAction builds a new Action with a formatted reason.
// Never mutates the input action.
func formatAction(
	action *model.Action, path string,
) *model.Action {
	return &model.Action{
		Decision: action.Decision,
		Reason: fmt.Sprintf(
			"%s: %s", path, action.Reason),
	}
}
