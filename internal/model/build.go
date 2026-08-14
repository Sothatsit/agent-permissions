package model

// RuleBuilder constructs rules with a fluent API.
type RuleBuilder struct {
	def                *RuleDef
	matcher            Matcher
	valueCouldContain  string
	valueMayHavePrefix string
	dflt               *Action
}

// WithRuleDef sets the rule governing this node. A node with a Def is pruned by
// the registry filter (FilterByConfig) when the rule is disabled, taking its
// whole subtree with it. Nodes without a Def are governed by an ancestor's Def
// (or are permissive containers); ValidateRegistry asserts every restrictive
// node has a Def on its path.
func (b RuleBuilder) WithRuleDef(def *RuleDef) RuleBuilder {
	b.def = def
	return b
}

// Flag creates a RuleBuilder that matches flags by name.
func Flag(names ...string) RuleBuilder {
	return RuleBuilder{
		matcher: &FlagMatcher{Names: names},
	}
}

// Subcmd creates a RuleBuilder that matches subcommands.
func Subcmd(names ...string) RuleBuilder {
	return RuleBuilder{
		matcher: &SubcmdMatcher{Names: names},
	}
}

// Always creates a RuleBuilder that always matches.
func Always() RuleBuilder {
	return RuleBuilder{matcher: &AlwaysMatcher{}}
}

// ValueCouldContain adds a value condition that conservatively matches if the
// value could contain the given substring. Opaque values always match.
func (b RuleBuilder) ValueCouldContain(
	s string,
) RuleBuilder {
	b.valueCouldContain = s
	return b
}

// ValueMayHavePrefix adds a value condition that conservatively matches if the
// value could have the given prefix. Opaque values always match.
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

// Hook returns a Rule that decides via a hook function. The hook's threat is
// governed by the builder's Def (set with WithRuleDef) or an ancestor's. A hook
// node carries no Default - the evaluator only consults Default through
// children, so a hook that needs a fallback is placed under a parent that holds
// the DefaultDeny.
func (b RuleBuilder) Hook(fn HookFunc) Rule {
	return Rule{
		Def:   b.def,
		Match: b.finalizeMatcher(),
		Hook:  fn,
	}
}

// DefaultDeny sets the default action to deny.
func (b RuleBuilder) DefaultDeny(
	reason string,
) RuleBuilder {
	b.dflt = DenyAction(reason)
	return b
}

// DefaultAsk sets the default action to ask.
func (b RuleBuilder) DefaultAsk(
	reason string,
) RuleBuilder {
	b.dflt = AskAction(reason)
	return b
}

// DefaultAllow sets the default action to allow.
func (b RuleBuilder) DefaultAllow(
	reason string,
) RuleBuilder {
	b.dflt = AllowAction(reason)
	return b
}

func (b RuleBuilder) Rules(children ...Rule) Rule {
	return Rule{
		Def:      b.def,
		Match:    b.finalizeMatcher(),
		Default:  b.dflt,
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
