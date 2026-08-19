package model

type RuleBuilder struct {
	def                *RuleDef
	matcher            Matcher
	valueCouldContain  string
	valueMayHavePrefix string
	defaultAction      *Action
}

func (b RuleBuilder) WithRuleDef(def *RuleDef) RuleBuilder {
	b.def = def
	return b
}

func Flag(names ...string) RuleBuilder {
	return RuleBuilder{
		matcher: &FlagMatcher{Names: names},
	}
}

func Subcmd(names ...string) RuleBuilder {
	return RuleBuilder{
		matcher: &SubcmdMatcher{Names: names},
	}
}

func Always() RuleBuilder {
	return RuleBuilder{matcher: &AlwaysMatcher{}}
}

// ValueCouldContain matches conservatively: an opaque value always matches.
func (b RuleBuilder) ValueCouldContain(
	s string,
) RuleBuilder {
	b.valueCouldContain = s
	return b
}

// ValueMayHavePrefix matches conservatively: an opaque value always matches.
func (b RuleBuilder) ValueMayHavePrefix(
	s string,
) RuleBuilder {
	b.valueMayHavePrefix = s
	return b
}

func (b RuleBuilder) finalizeMatcher() Matcher {
	if b.valueCouldContain != "" ||
		b.valueMayHavePrefix != "" {
		if fm, ok := b.matcher.(*FlagMatcher); ok {
			return &FlagMatcher{
				Names:              fm.Names,
				ValueCouldContain:  b.valueCouldContain,
				ValueMayHavePrefix: b.valueMayHavePrefix,
			}
		}
	}

	return b.matcher
}

func (b RuleBuilder) Deny(reason string) Rule {
	return Rule{
		Def:    b.def,
		Match:  b.finalizeMatcher(),
		Action: DenyAction(reason),
	}
}

func (b RuleBuilder) Ask(reason string) Rule {
	return Rule{
		Def:    b.def,
		Match:  b.finalizeMatcher(),
		Action: AskAction(reason),
	}
}

func (b RuleBuilder) SoftAsk(reason string) Rule {
	return Rule{
		Def:    b.def,
		Match:  b.finalizeMatcher(),
		Action: SoftAskAction(reason),
	}
}

func (b RuleBuilder) Allow(reason string) Rule {
	return Rule{
		Def:    b.def,
		Match:  b.finalizeMatcher(),
		Action: AllowAction(reason),
	}
}

// Hook nodes carry no Default, because the evaluator only consults Default
// through children. A hook needing a fallback goes under a parent holding the
// DefaultDeny.
func (b RuleBuilder) Hook(fn HookFunc) Rule {
	return Rule{
		Def:   b.def,
		Match: b.finalizeMatcher(),
		Hook:  fn,
	}
}

func (b RuleBuilder) DefaultDeny(
	reason string,
) RuleBuilder {
	b.defaultAction = DenyAction(reason)
	return b
}

func (b RuleBuilder) DefaultAsk(
	reason string,
) RuleBuilder {
	b.defaultAction = AskAction(reason)
	return b
}

func (b RuleBuilder) DefaultAllow(
	reason string,
) RuleBuilder {
	b.defaultAction = AllowAction(reason)
	return b
}

func (b RuleBuilder) Rules(children ...Rule) Rule {
	return Rule{
		Def:      b.def,
		Match:    b.finalizeMatcher(),
		Default:  b.defaultAction,
		Children: children,
	}
}

func DenyAction(reason string) *Action {
	return &Action{Decision: Deny, Reason: reason}
}

func AskAction(reason string) *Action {
	return &Action{Decision: Ask, Reason: reason}
}

func SoftAskAction(reason string) *Action {
	return &Action{Decision: SoftAsk, Reason: reason}
}

func AllowAction(reason string) *Action {
	return &Action{Decision: Allow, Reason: reason}
}
