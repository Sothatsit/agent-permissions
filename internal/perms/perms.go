// Package perms loads Claude Code permission settings and matches commands
// against allow/ask/deny patterns.
package perms

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/harness"
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// MatchMode is how a pattern handles arguments beyond its fixed elements.
type MatchMode int

const (
	// MatchExact requires exactly the fixed elements ("git status").
	MatchExact MatchMode = iota
	// MatchTrailing requires 1+ args after them ("git *").
	MatchTrailing
	// MatchPrefix allows 0+ args after them ("git:*").
	MatchPrefix
)

// Pattern is a parsed permission pattern. Reason is surfaced in hook output,
// and is empty for a Claude Code settings.json, whose array shape has no slot
// for one.
type Pattern struct {
	Elements []string  // fixed parts to match
	Raw      string    // original pattern text
	Mode     MatchMode // how to handle remaining args
	Reason   string
}

func (p Pattern) clone() Pattern {
	p.Elements = slices.Clone(p.Elements)
	return p
}

// EnvVarPattern matches an assigned variable by exact name (BASH_ENV) or by
// prefix with a trailing * (LD_*). The schema concerns itself only with which
// variables can be assigned, not what they are assigned to.
type EnvVarPattern struct {
	Raw    string // original entry, e.g. "LD_*"
	Match  string // entry minus trailing "*"
	Prefix bool   // true when Raw ends with "*"
	Reason string
}

// TierEntries holds one tier's entries split by tool axis. Each axis resolves
// independently.
type TierEntries struct {
	Commands []Pattern
	EnvVars  []EnvVarPattern
}

func (t TierEntries) clone() TierEntries {
	commands := slices.Clone(t.Commands)
	for i := range commands {
		commands[i] = commands[i].clone()
	}

	return TierEntries{
		Commands: commands,
		EnvVars:  slices.Clone(t.EnvVars),
	}
}

// SourcePerms holds one config source's entries. Normal sources resolve in
// priority order with Deny > Ask > Allow > SoftAsk inside one source. Enforced
// sources aggregate every match by decision strength.
type SourcePerms struct {
	// Name is shown in `check` output and reasons ("preset:git").
	Name string

	// AcceptsReasons is false for Claude Code settings.json, whose
	// flat-array shape has no slot for reasons, so `validate` does not
	// flag structurally-empty reasons as missing.
	AcceptsReasons bool

	Allow   TierEntries
	SoftAsk TierEntries
	Ask     TierEntries
	Deny    TierEntries
}

func (s SourcePerms) clone() SourcePerms {
	s.Allow = s.Allow.clone()
	s.SoftAsk = s.SoftAsk.clone()
	s.Ask = s.Ask.clone()
	s.Deny = s.Deny.clone()
	return s
}

// Permissions holds parsed rules across every config source. Sources are normal
// first-match config, highest priority first. EnforcedSources are organisation
// policy: every matching entry participates and the strongest decision combines
// with normal resolution.
type Permissions struct {
	Sources         []SourcePerms
	EnforcedSources []SourcePerms

	// Warnings collects malformed entries rejected at load time, for
	// `check` and `validate` to surface. The hot path ignores them.
	Warnings []ConfigWarning

	// Resolve installs these maps in one filter pass.
	rules        map[string]*model.CommandRules
	snippetRules map[string]*model.SnippetLang

	// PathDirs is the hook process's PATH, which decides whether an
	// absolute-path command can match a bare-name Allow: /usr/bin/git
	// with /usr/bin on PATH is the binary the shell would have found
	// for `git`. An out-of-PATH absolute path needs its own pattern.
	PathDirs map[string]struct{}

	// Harness carries the harness-specific output text, so resolution
	// never branches on which agent harness is running.
	Harness harness.Harness
}

type ConfigWarning struct {
	Source string
	Entry  string
	Reason string
}

type Result struct {
	Decision model.Decision
	Reason   string
}

// DenyResult builds the grouped "Deny:" format for callers that deny outside
// Check, such as breakdown errors.
func DenyResult(reason string) Result {
	return Result{
		Decision: model.Deny,
		Reason: formatResult(
			nil, nil, nil, []string{reason}, ""),
	}
}

// checkSource identifies where a decision came from.
type checkSource int

const (
	sourceRules checkSource = iota
	sourcePattern
	sourceNone
)

// commandCheck is one command's evaluation, carrying enough for the caller to
// format grouped output. That renders as "<subject>[ - <description>] (from
// <sourceName>)", and the two-field split lets every layer share one renderer:
// a pattern puts pat.Raw and the config reason in them, a rule splits its
// "<subject>: <description>" prose on the first ": ", and an env-var match uses
// the variable name and the pattern's reason.
type commandCheck struct {
	decision    model.Decision
	source      checkSource
	subject     string
	description string
	// sourceName attributes a sourcePattern decision to the SourcePerms
	// that produced it ("preset:git").
	sourceName string
	// ruleDef drives the "(from rule:<id>)" attribution, so the displayed
	// ID is the one the user disables. Nil for structural rule decisions
	// with no governing rule.
	ruleDef *model.RuleDef
	// args holds the command's string arguments. Set only for sourceNone
	// (unknown commands) so the caller can compute a suggestion pattern.
	args []string
	// matches holds every reason at the winning tier when enforced policy
	// and normal resolution tie. Empty in the common single-match case, and
	// each child is a complete check.
	matches []commandCheck
}

func (c commandCheck) reasons() []commandCheck {
	if len(c.matches) > 0 {
		return c.matches
	}

	return []commandCheck{c}
}

func (c commandCheck) withSubject(subject string) commandCheck {
	if len(c.matches) == 0 {
		c.subject = subject
		return c
	}

	for i := range c.matches {
		c.matches[i].subject = subject
	}

	return c
}

// labelCollector is a deduplicated ordered list of strings.
type labelCollector struct {
	items []string
	seen  map[string]bool
}

func (c *labelCollector) addAll(items []string) {
	for _, item := range items {
		c.add(item)
	}
}

func (c *labelCollector) add(item string) {
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	if !c.seen[item] {
		c.seen[item] = true
		c.items = append(c.items, item)
	}
}

// decisionAggregate is the decision a whole command lands on, plus the reasons
// behind it. Every axis reports into it and the strongest tier wins, so holding
// the precedence rule here keeps the command, env-var, and snippet axes from
// drifting apart.
//
// The order is Deny > Ask > SoftAsk > Undecided > Allow. Undecided outranks
// Allow because an unknown command must not be silently allowed, which is the
// one place this differs from combineDecision's enforced-versus-normal fold.
type decisionAggregate struct {
	decision               model.Decision
	denies, asks, softAsks labelCollector
}

// add reports one axis's decision with the labels it should display, and
// returns whether it survived tier precedence. A caller does its own
// bookkeeping, such as an unknown command's suggested pattern, only when it
// did.
func (a *decisionAggregate) add(
	decision model.Decision, labels ...string,
) bool {
	switch decision {
	case model.Deny:
		a.decision = model.Deny
		a.denies.addAll(labels)
	case model.Ask:
		if a.decision == model.Deny {
			return false
		}

		a.decision = model.Ask
		a.asks.addAll(labels)
	case model.SoftAsk:
		if a.decision == model.Deny {
			return false
		}
		if a.decision != model.Ask {
			a.decision = model.SoftAsk
		}

		a.softAsks.addAll(labels)
	case model.Undecided:
		if a.decision == model.Deny {
			return false
		}
		if a.decision != model.Ask &&
			a.decision != model.SoftAsk {
			a.decision = model.Undecided
		}
	}

	return true
}

// checkLabels renders every reason behind one check.
func checkLabels(
	check commandCheck, cmd model.Command,
) []string {
	reasons := check.reasons()
	labels := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		labels = append(labels, formatCheck(reason, cmd))
	}

	return labels
}

// parsePattern rejects degenerate inputs so callers can skip them silently.
func parsePattern(raw string) (Pattern, error) {
	elements := strings.Fields(raw)
	// An empty pattern matches nothing. Match-all needs a bare `Bash`
	// or `Bash(*)`, which extractBashPattern normalises to raw="*"
	// and elements=["*"].
	if len(elements) == 0 {
		return Pattern{}, fmt.Errorf("empty pattern")
	}

	mode := MatchExact
	last := elements[len(elements)-1]

	if last == "*" {
		elements = elements[:len(elements)-1]
		mode = MatchTrailing
	} else if strings.HasSuffix(last, ":*") {
		elements[len(elements)-1] =
			strings.TrimSuffix(last, ":*")
		mode = MatchPrefix
	}

	// An empty fixed element, as `:*` collapsing to [""], could never
	// sensibly match and suggests a malformed entry.
	for _, e := range elements {
		if e == "" {
			return Pattern{}, fmt.Errorf(
				"pattern contains empty element")
		}
	}

	return Pattern{
		Elements: elements,
		Raw:      raw,
		Mode:     mode,
	}, nil
}

// envVarPatternRE constrains entries to a POSIX-shape name with an optional
// trailing `*`. Bash rejects anything looser at parse time, so the hook will
// never see one, and rejecting them keeps validate honest about what is
// load-bearing.
var envVarPatternRE = regexp.MustCompile(
	`^[A-Za-z_][A-Za-z0-9_]*\*?$`)

// parseEnvVarPattern rejects an empty pattern, and any character that can never
// appear in an env var name.
func parseEnvVarPattern(
	raw, reason string,
) (EnvVarPattern, error) {
	if raw == "" {
		return EnvVarPattern{},
			fmt.Errorf("empty env var pattern")
	}
	if !envVarPatternRE.MatchString(raw) {
		return EnvVarPattern{}, fmt.Errorf(
			"invalid env var pattern %q "+
				"(expected NAME or NAME*)",
			raw)
	}

	match := raw
	prefix := false
	if strings.HasSuffix(raw, "*") {
		match = raw[:len(raw)-1]
		prefix = true
	}

	return EnvVarPattern{
		Raw:    raw,
		Match:  match,
		Prefix: prefix,
		Reason: reason,
	}, nil
}

func (p EnvVarPattern) MatchEnvVar(name string) bool {
	if p.Prefix {
		return strings.HasPrefix(name, p.Match)
	}

	return name == p.Match
}

func extractBashPattern(entry string) (string, bool) {
	if entry == "Bash" {
		return "*", true
	}
	if !strings.HasPrefix(entry, "Bash(") ||
		!strings.HasSuffix(entry, ")") {
		return "", false
	}

	return entry[5 : len(entry)-1], true
}

// Check evaluates every extracted command, env var, and snippet.
func (p *Permissions) Check(
	result model.BreakdownResult,
) Result {
	aggregate := decisionAggregate{decision: model.Allow}
	var undecidedReason string
	var unknowns, scripts labelCollector

	// Each assigned name resolves through the source stack exactly like a
	// command. The Allow path leaves the matched name out of the
	// commandCheck, because Allow.EnvVars overrides do not surface, so plug
	// it in for the tiers that do.
	for _, name := range result.Assigns {
		check := p.checkOneEnvVar(name).withSubject(name)
		// A variable no pattern mentions is not an opinion, unlike an
		// unknown command. Treating it as one would make every
		// FOO=bar cmd undecided.
		if check.decision == model.Undecided {
			continue
		}

		aggregate.add(check.decision,
			checkLabels(check, model.Command{})...)
	}

	cmds := result.Commands
	snippets := result.CodeSnippets
	// Env vars that already produced a decision must reach the labels, so
	// they skip the no-commands early return. Bare safe input still allows.
	if len(cmds) == 0 && len(snippets) == 0 &&
		aggregate.decision == model.Allow {
		if result.IsSafe() {
			return Result{Decision: model.Allow}
		}

		return Result{Decision: model.Undecided}
	}

	for _, cmd := range cmds {
		check := p.checkOne(cmd)
		if !aggregate.add(check.decision,
			checkLabels(check, cmd)...) {
			continue
		}

		switch check.decision {
		case model.Deny:
			if cmd.SourcePath != "" {
				scripts.add(word.DirectPath(
					cmd.RootScript))
			}
		case model.Undecided:
			if len(check.args) > 0 {
				unknowns.add(p.buildPermissionSuggestion(
					check.args,
					cmd.SourcePath))
			} else {
				undecidedReason = check.subject
			}
		}
	}

	// A snippet's reason is pre-composed, so it is its own label. Snippets
	// never land on Undecided.
	for i := range snippets {
		snippetResult := p.checkSnippet(&snippets[i])
		aggregate.add(snippetResult.decision,
			snippetResult.subject)
	}

	// Unknowns with no other opinion become SoftAsk, so the hook can
	// ask in normal mode and fall through in auto. Pure undecided is
	// a true "no opinion" and always falls through.
	if aggregate.decision == model.Undecided {
		if len(unknowns.items) > 0 {
			aggregate.decision = model.SoftAsk
		} else {
			return Result{
				Decision: model.Undecided,
				Reason:   undecidedReason,
			}
		}
	}

	h := p.Harness
	if h == nil {
		h = harness.Placeholder{}
	}

	unknownHeader := h.UnknownCommandHeader()

	if aggregate.decision == model.Deny {
		reason := formatResult(
			nil, nil, nil, aggregate.denies.items, unknownHeader)
		if len(scripts.items) > 0 {
			reason += "\n\n" +
				sourceGuidance(scripts.items)
		}

		return Result{
			Decision: model.Deny,
			Reason:   reason,
		}
	}

	return Result{
		Decision: aggregate.decision,
		Reason: formatResult(
			aggregate.asks.items, aggregate.softAsks.items,
			unknowns.items, nil, unknownHeader),
	}
}

// sourceGuidance is the footer for denies from sourced files.
func sourceGuidance(scriptPaths []string) string {
	if len(scriptPaths) == 1 {
		return fmt.Sprintf(
			"Remove restricted commands and "+
				"retry, or if required, inform "+
				"the user and suggest running "+
				"%s directly.",
			scriptPaths[0])
	}

	return fmt.Sprintf(
		"Remove restricted commands and retry, "+
			"or if required, inform the user "+
			"and suggest running the scripts "+
			"directly (%s).",
		strings.Join(scriptPaths, ", "))
}

// formatCheck builds a matched check's display label. A pattern shows as-is,
// keeping its `:*` form so it can be pasted into an Allow list. A rule shows
// subject and description split by a dash, attributed to its RuleDef ID so the
// user has the exact ID to disable. An empty description drops the dash, which
// is common for settings.json and for presets without a reason.
func formatCheck(
	check commandCheck, cmd model.Command,
) string {
	body := check.subject
	if check.description != "" {
		body += " - " + check.description
	}

	source := check.sourceName
	if check.source == sourceRules {
		source = "rule"
		if check.ruleDef != nil {
			source = "rule:" + check.ruleDef.ID
		}
	}

	label := body
	if source != "" {
		label += "  (from " + source + ")"
	}
	if cmd.SourcePath != "" {
		label += " (in " + cmd.SourcePath + ")"
	}

	return label
}

// splitRuleReason splits a rule reason on the first ": ". Rule reasons follow
// "<subject>: <description>", so the subject can drive attribution and the
// description follow a dash. Without a separator the whole string is the
// subject.
func splitRuleReason(
	reason string,
) (subject, description string) {
	i := strings.Index(reason, ": ")
	if i < 0 {
		return reason, ""
	}

	return reason[:i], reason[i+2:]
}

// checkSnippet prefers an explicit permission on SourceScript, and otherwise
// returns the strongest finding from the language's snippet rules.
func (p *Permissions) checkSnippet(
	snippet *model.CodeSnippet,
) commandCheck {
	// For file-based code the pattern decision wins outright. For inline
	// -c/-e code only Deny and Ask win: an Allow does not suppress
	// scanning, because agent-generated code is never user-reviewed, so
	// pattern matching is a hard floor even under broad allows.
	if len(snippet.SourceScript) > 0 {
		cmd := model.Command{
			Args: snippet.SourceScript,
		}
		check := p.checkOne(cmd)
		hasPattern := check.source == sourcePattern ||
			check.source == sourceRules
		inlineAllow := snippet.SourceFile == "" &&
			check.decision == model.Allow
		if hasPattern && !inlineAllow {
			return check
		}
	}

	lang := p.snippetRules[snippet.Language]
	if lang == nil || len(lang.Rules) == 0 {
		return commandCheck{decision: model.Allow}
	}

	code := snippet.Code
	var interpolationContents []string
	if lang.InterpolationContents != nil {
		interpolationContents = lang.InterpolationContents(code)
	}
	if lang.StripComments != nil {
		code = lang.StripComments(code)
	}

	scanInputs := append([]string{code}, interpolationContents...)

	var reasons []string
	strongest := model.Allow
	for i := range lang.Rules {
		matched := false
		for _, input := range scanInputs {
			if lang.Rules[i].Check(input) {
				matched = true
				break
			}
		}

		if matched {
			action := lang.Rules[i].Action
			if action.Decision > strongest {
				strongest = action.Decision
			}

			reasons = append(
				reasons, action.Reason)
		}
	}

	if len(reasons) == 0 {
		return commandCheck{decision: model.Allow}
	}

	// All of a language's snippet rules share one RuleDef, so the
	// finding is attributed to it and the displayed ID is the one the
	// user disables.
	source := "inline code"
	if snippet.SourceFile != "" {
		source = snippet.SourceFile
	}

	reason := fmt.Sprintf(
		"%s: %s",
		source, strings.Join(reasons, ", "))
	if lang.Def != nil {
		reason += "  (from rule:" + lang.Def.ID + ")"
	}

	// Inline code (-c) keeps the original decision (deny). File-based code
	// is downgraded to ask.
	decision := strongest
	if snippet.SourceFile != "" &&
		decision == model.Deny {
		decision = model.Ask
		reason += ". To always allow: add " +
			p.buildPermissionSuggestion(
				word.Texts(
					snippet.SourceScript), "")
	}

	return commandCheck{
		decision: decision,
		source:   sourceRules,
		ruleDef:  lang.Def,
		// Snippet reasons are pre-composed and bypass formatCheck, so
		// the attribution is already baked in. Park the composed string
		// in subject and leave description empty.
		subject: reason,
	}
}

type commandIdentityKind int

const (
	trustedCommandName commandIdentityKind = iota
	trustedCommandPath
	untrustedCommandPath
)

// commandIdentity is how patterns address one command. A path-invoked command
// also carries basenameArgs, its argv with the bare name in place, so deny and
// ask patterns reach it either way. Only a path found in the captured PATH may
// use that bare name to gain an Allow.
type commandIdentity struct {
	kind         commandIdentityKind
	name         string
	args         []string
	basenameArgs []string
}

func (p *Permissions) identifyCommand(
	cmd model.Command,
) commandIdentity {
	args := word.Texts(cmd.Args)
	executable := args[0]
	identity := commandIdentity{
		kind: trustedCommandName,
		name: executable,
		args: args,
	}
	if !strings.Contains(executable, "/") {
		return identity
	}

	identity.kind = untrustedCommandPath
	identity.name = filepath.Base(executable)
	identity.basenameArgs = make([]string, len(args))
	copy(identity.basenameArgs, args)
	identity.basenameArgs[0] = identity.name

	if filepath.IsAbs(executable) {
		if _, ok := p.PathDirs[filepath.Dir(executable)]; ok {
			identity.kind = trustedCommandPath
		}
	}

	return identity
}

func (p *Permissions) checkOne(
	cmd model.Command,
) commandCheck {
	if len(cmd.Args) == 0 {
		return commandCheck{
			decision: model.Allow,
			source:   sourceNone,
		}
	}

	identity := p.identifyCommand(cmd)

	// 1. Rules layer, working on Words with no string conversion.
	if p.rules != nil {
		if action := evaluateCommandRules(
			p.rules, identity, cmd.Args[1:],
		); action != nil {
			subject, desc := splitRuleReason(
				action.Reason)
			return commandCheck{
				decision:    action.Decision,
				source:      sourceRules,
				subject:     subject,
				description: desc,
				ruleDef:     action.Def,
			}
		}
	}

	// 2. Pattern matching. Normal sources keep first-source semantics.
	// Enforced sources form a minimum policy: every match participates, and
	// their strongest decision combines with the normal result.
	var pathResolved []string
	if identity.kind == trustedCommandPath {
		pathResolved = identity.basenameArgs
	}

	normal := matchCommandSources(
		p.Sources, identity.args,
		identity.basenameArgs, pathResolved)
	enforced := matchEnforcedCommandSources(
		p.EnforcedSources, identity.args,
		identity.basenameArgs, pathResolved)
	check := combinePolicyChecks(normal, enforced)
	if check.decision != model.Undecided {
		return check
	}

	// 3. Function-call override, after all sources, because an explicit
	// deny should outlive the function-call escape hatch.
	if cmd.CouldBeFuncCall {
		return commandCheck{
			decision: model.Allow,
			source:   sourceNone,
		}
	}

	// 4. Unknown command.
	return commandCheck{
		decision: model.Undecided,
		source:   sourceNone,
		args:     identity.args,
	}
}

// matchCommandSources applies normal first-source resolution. Within a source,
// an explicit Allow opts out of SoftAsk.
func matchCommandSources(
	sources []SourcePerms,
	argTexts, stripped, pathResolved []string,
) commandCheck {
	for _, src := range sources {
		if check, ok := matchTier(
			src, src.Deny.Commands, model.Deny,
			argTexts, stripped,
		); ok {
			return check
		}
		if check, ok := matchTier(
			src, src.Ask.Commands, model.Ask,
			argTexts, pathResolved,
		); ok {
			return check
		}
		if check, ok := matchTier(
			src, src.Allow.Commands, model.Allow,
			argTexts, pathResolved,
		); ok {
			return check
		}
		if check, ok := matchTier(
			src, src.SoftAsk.Commands, model.SoftAsk,
			argTexts, pathResolved,
		); ok {
			return check
		}
	}

	return commandCheck{
		decision: model.Undecided,
		source:   sourceNone,
	}
}

// matchEnforcedCommandSources treats every enforced entry as an independent
// minimum, so no ordering can hide a stronger match behind a weaker one.
func matchEnforcedCommandSources(
	sources []SourcePerms,
	argTexts, stripped, pathResolved []string,
) commandCheck {
	var matches []commandCheck
	strongest := model.Undecided
	for _, src := range sources {
		tiers := []struct {
			patterns []Pattern
			decision model.Decision
			stripped []string
		}{
			{src.Deny.Commands, model.Deny, stripped},
			{src.Ask.Commands, model.Ask, pathResolved},
			{src.SoftAsk.Commands, model.SoftAsk,
				pathResolved},
			{src.Allow.Commands, model.Allow,
				pathResolved},
		}
		for _, tier := range tiers {
			tierMatches := matchTierAll(
				src, tier.patterns, tier.decision,
				argTexts, tier.stripped)
			if len(tierMatches) == 0 ||
				tier.decision < strongest {
				continue
			}
			if tier.decision > strongest {
				strongest = tier.decision
				matches = matches[:0]
			}

			matches = append(matches, tierMatches...)
		}
	}

	return aggregateChecks(matches)
}

// combineDecision folds the enforced verdict into the normal one,
// keeping the stronger tier so enforced policy is a minimum the
// user cannot weaken.
//
// SoftAsk is the exception. It means "nudge unless the user has
// already allowed this", so an explicit Allow answers it rather
// than losing to it. The reverse does not hold: an enforced Allow
// cannot talk a normal SoftAsk down, because the enforced plane
// only strengthens.
func combineDecision(
	normal, enforced model.Decision,
) model.Decision {
	if enforced == model.SoftAsk &&
		normal == model.Allow {
		return model.Allow
	}
	if normal > enforced {
		return normal
	}

	return enforced
}

func combinePolicyChecks(
	normal, enforced commandCheck,
) commandCheck {
	combined := combineDecision(
		normal.decision, enforced.decision)
	if combined != enforced.decision {
		return normal
	}
	if combined != normal.decision {
		return enforced
	}
	if normal.decision == model.Undecided ||
		normal.decision == model.Allow {
		if normal.source != sourceNone {
			return normal
		}

		return enforced
	}

	matches := append(
		append([]commandCheck{}, normal.reasons()...),
		enforced.reasons()...)
	return aggregateChecks(matches)
}

func aggregateChecks(matches []commandCheck) commandCheck {
	if len(matches) == 0 {
		return commandCheck{
			decision: model.Undecided,
			source:   sourceNone,
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}

	return commandCheck{
		decision: matches[0].decision,
		source:   sourcePattern,
		matches:  matches,
	}
}

func matchFirst(
	patterns []Pattern, args []string,
) (Pattern, bool) {
	for _, pat := range patterns {
		if matchPattern(pat, args) {
			return pat, true
		}
	}

	return Pattern{}, false
}

// matchTier tries the raw args, then the basename-stripped form. The Allow tier
// gets an empty reason because Allow never surfaces, but the source is still
// recorded for attribution.
func matchTier(
	src SourcePerms,
	patterns []Pattern,
	tier model.Decision,
	argTexts, stripped []string,
) (commandCheck, bool) {
	if pat, ok := matchFirst(patterns, argTexts); ok {
		return commandCheckFromPattern(
			src, pat, tier, ""), true
	}
	if stripped != nil {
		if pat, ok := matchFirst(patterns, stripped); ok {
			via := fmt.Sprintf(
				" (via %s)", argTexts[0])
			return commandCheckFromPattern(
				src, pat, tier, via), true
		}
	}

	return commandCheck{}, false
}

// matchTierAll returns every match in one enforced tier. A pattern matching
// both forms contributes one reason, preferring the raw form as matchTier does.
func matchTierAll(
	src SourcePerms,
	patterns []Pattern,
	tier model.Decision,
	argTexts, stripped []string,
) []commandCheck {
	var matches []commandCheck
	for _, pat := range patterns {
		if matchPattern(pat, argTexts) {
			matches = append(matches,
				commandCheckFromPattern(
					src, pat, tier, ""))
			continue
		}
		if stripped != nil && matchPattern(pat, stripped) {
			via := fmt.Sprintf(
				" (via %s)", argTexts[0])
			matches = append(matches,
				commandCheckFromPattern(
					src, pat, tier, via))
		}
	}

	return matches
}

// commandCheckFromPattern builds a pattern-layer match. The via suffix joins
// the subject only for a basename-stripped retry, telling the user a
// path-prefixed invocation hit a bare-name pattern.
func commandCheckFromPattern(
	src SourcePerms,
	pat Pattern,
	tier model.Decision,
	via string,
) commandCheck {
	return commandCheck{
		decision:    tier,
		source:      sourcePattern,
		subject:     pat.Raw + via,
		description: pat.Reason,
		sourceName:  src.Name,
	}
}

// checkOneEnvVar resolves normal and enforced EnvVars policy independently,
// then keeps the stronger result.
func (p *Permissions) checkOneEnvVar(
	name string,
) commandCheck {
	normal := matchEnvVarSources(p.Sources, name)
	enforced := matchEnforcedEnvVarSources(
		p.EnforcedSources, name)
	return combinePolicyChecks(normal, enforced)
}

func matchEnvVarSources(
	sources []SourcePerms, name string,
) commandCheck {
	for _, src := range sources {
		if pat, ok := matchEnvVarFirst(
			src.Deny.EnvVars, name,
		); ok {
			return envVarCheck(src, pat, model.Deny)
		}
		if pat, ok := matchEnvVarFirst(
			src.Ask.EnvVars, name,
		); ok {
			return envVarCheck(src, pat, model.Ask)
		}
		if pat, ok := matchEnvVarFirst(
			src.Allow.EnvVars, name,
		); ok {
			return envVarCheck(src, pat, model.Allow)
		}
		if pat, ok := matchEnvVarFirst(
			src.SoftAsk.EnvVars, name,
		); ok {
			return envVarCheck(src, pat, model.SoftAsk)
		}
	}

	return commandCheck{
		decision: model.Undecided,
		source:   sourceNone,
	}
}

func matchEnforcedEnvVarSources(
	sources []SourcePerms, name string,
) commandCheck {
	var matches []commandCheck
	strongest := model.Undecided
	for _, src := range sources {
		tiers := []struct {
			patterns []EnvVarPattern
			decision model.Decision
		}{
			{src.Deny.EnvVars, model.Deny},
			{src.Ask.EnvVars, model.Ask},
			{src.SoftAsk.EnvVars, model.SoftAsk},
			{src.Allow.EnvVars, model.Allow},
		}
		for _, tier := range tiers {
			tierMatches := matchEnvVarAll(
				tier.patterns, name)
			if len(tierMatches) == 0 ||
				tier.decision < strongest {
				continue
			}
			if tier.decision > strongest {
				strongest = tier.decision
				matches = matches[:0]
			}

			for _, pat := range tierMatches {
				matches = append(matches,
					envVarCheck(
						src, pat, tier.decision))
			}
		}
	}

	return aggregateChecks(matches)
}

// envVarCheck builds an env-var match. Subject is the matched pattern's Raw
// ("BASH_ENV", "LD_*").
func envVarCheck(
	src SourcePerms,
	pat EnvVarPattern,
	tier model.Decision,
) commandCheck {
	return commandCheck{
		decision:    tier,
		source:      sourcePattern,
		subject:     pat.Raw,
		description: pat.Reason,
		sourceName:  src.Name,
	}
}

func matchEnvVarFirst(
	patterns []EnvVarPattern, name string,
) (EnvVarPattern, bool) {
	for _, pat := range patterns {
		if pat.MatchEnvVar(name) {
			return pat, true
		}
	}

	return EnvVarPattern{}, false
}

func matchEnvVarAll(
	patterns []EnvVarPattern, name string,
) []EnvVarPattern {
	var matches []EnvVarPattern
	for _, pat := range patterns {
		if pat.MatchEnvVar(name) {
			matches = append(matches, pat)
		}
	}

	return matches
}

func matchPattern(pat Pattern, args []string) bool {
	if len(args) == 0 {
		return false
	}

	if len(pat.Elements) == 0 &&
		pat.Mode == MatchTrailing {
		return true
	}

	for i, elem := range pat.Elements {
		if i >= len(args) {
			return false
		}
		if !globMatch(elem, args[i]) {
			return false
		}
	}

	remaining := len(args) - len(pat.Elements)

	switch pat.Mode {
	case MatchExact:
		return remaining == 0
	case MatchTrailing:
		return remaining >= 1
	case MatchPrefix:
		return remaining >= 0
	}

	return false
}

func globMatch(pattern, text string) bool {
	if pattern == "*" {
		return true
	}
	if pattern == text {
		return true
	}

	if !strings.Contains(pattern, "*") {
		return false
	}

	parts := strings.Split(pattern, "*")
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}

		idx := strings.Index(text[pos:], part)
		if idx < 0 {
			return false
		}
		if i == 0 && idx != 0 {
			return false
		}

		pos += idx + len(part)
	}

	if parts[len(parts)-1] != "" && pos != len(text) {
		return false
	}

	return true
}

// buildPermissionSuggestion builds the "Bash(cmd:*)" entry shown for an unknown
// command.
func (p *Permissions) buildPermissionSuggestion(
	args []string, sourcePath string,
) string {
	s := "Bash(" + p.buildPermissionPattern(args) + ")"
	if sourcePath != "" {
		s += " (from " + sourcePath + ")"
	}

	return s
}

// buildPermissionPattern finds the shortest :* pattern that is not already
// known to the patterns or the rules registry, so git apply suggests git
// apply:* rather than git:* when git has rules but apply does not.
func (p *Permissions) buildPermissionPattern(
	args []string,
) string {
	prefix := commandPrefix(args)
	for depth := 1; depth < len(prefix); depth++ {
		if !p.prefixKnown(prefix[:depth]) {
			return strings.Join(
				prefix[:depth], " ") + ":*"
		}
	}

	return strings.Join(prefix, " ") + ":*"
}

// compoundFlags are flags whose next arg is part of the command identity, so
// python3 -m module suggests python3 -m module:*.
var compoundFlags = map[string]map[string]bool{
	"python":  {"-m": true},
	"python3": {"-m": true},
	"java":    {"-jar": true},
}

// commandPrefix takes at most 2 subcommand-like elements from the start of
// args, stopping at a flag or a path. A compound flag brings its argument
// along.
func commandPrefix(args []string) []string {
	var prefix []string
	for i, arg := range args {
		if i > 0 && !looksLikeSubcommand(arg) {
			if len(prefix) == 1 && i+1 < len(args) {
				if flags, ok :=
					compoundFlags[prefix[0]]; ok {
					if flags[arg] {
						prefix = append(
							prefix, arg,
							args[i+1])
						return prefix
					}
				}
			}

			break
		}

		prefix = append(prefix, arg)
		if len(prefix) >= 2 {
			break
		}
	}

	return prefix
}

// looksLikeSubcommand is true for words like "apply" or "run", and false for
// flags, paths, filenames, and key=value pairs.
func looksLikeSubcommand(s string) bool {
	if s == "" || s[0] == '-' {
		return false
	}

	for _, c := range s {
		if c == '/' || c == '.' || c == '=' {
			return false
		}
	}

	return true
}

// prefixKnown checks whether any existing pattern or rules registry entry
// shares the given command prefix. Walks every source's tiers since suggestions
// should reflect the full active policy.
func (p *Permissions) prefixKnown(
	prefix []string,
) bool {
	// Rules registry keys cover depth 1 (command name).
	if len(prefix) == 1 && p.rules != nil {
		if _, ok := p.rules[prefix[0]]; ok {
			return true
		}
	}

	for _, sources := range [][]SourcePerms{
		p.EnforcedSources, p.Sources,
	} {
		for _, src := range sources {
			for _, patterns := range [][]Pattern{
				src.Allow.Commands,
				src.SoftAsk.Commands,
				src.Ask.Commands,
				src.Deny.Commands,
			} {
				for _, pat := range patterns {
					if patternSharesPrefix(
						pat, prefix,
					) {
						return true
					}
				}
			}
		}
	}

	return false
}

// patternSharesPrefix checks whether a pattern's elements share a prefix with
// the given command prefix. Catch-all patterns (empty elements) are skipped.
func patternSharesPrefix(
	pat Pattern, prefix []string,
) bool {
	if len(pat.Elements) == 0 {
		return false
	}

	n := len(pat.Elements)
	if len(prefix) < n {
		n = len(prefix)
	}

	for i := 0; i < n; i++ {
		if pat.Elements[i] != prefix[i] {
			return false
		}
	}

	return true
}

// --- Grouped result formatting ---

// formatResult builds the grouped reason string from collected labels. Ask,
// soft-ask, and unknown-suggestion sections render under their own headings.
// Soft-ask is formatted in the "to allow, add..." style with source
// attribution; unknown commands get the same "add-to-permissions" framing,
// parameterised by the harness's preferred phrasing (Claude Code references
// /permissions; future harnesses substitute their own).
func formatResult(
	askLabels []string,
	softAskLabels []string,
	unknownSuggestions []string,
	denyLabels []string,
	unknownHeader string,
) string {
	var b strings.Builder

	if len(denyLabels) > 0 {
		b.WriteString("Deny:\n")
		for _, l := range denyLabels {
			fmt.Fprintf(&b, "* %s\n", l)
		}

		return strings.TrimRight(b.String(), "\n")
	}

	if len(askLabels) > 0 {
		b.WriteString("Ask:\n")
		for _, l := range askLabels {
			fmt.Fprintf(&b, "* %s\n", l)
		}
	}

	if len(softAskLabels) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}

		b.WriteString(
			"Soft-ask. To allow, add to your " +
				"Allow permissions:\n")
		for _, l := range softAskLabels {
			fmt.Fprintf(&b, "* %s\n", l)
		}
	}

	if len(unknownSuggestions) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}

		noun := "command"
		if len(unknownSuggestions) > 1 {
			noun = "commands"
		}

		fmt.Fprintf(&b,
			"Unknown %s. %s:\n",
			noun, unknownHeader)
		for _, s := range unknownSuggestions {
			fmt.Fprintf(&b, "* %s\n", s)
		}
	}

	return strings.TrimRight(b.String(), "\n")
}
