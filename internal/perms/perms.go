// Package perms loads Claude Code permission settings and
// matches commands against allow/ask/deny patterns.
package perms

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/harness"
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// MatchMode describes how a pattern handles arguments
// beyond its fixed elements.
type MatchMode int

const (
	// MatchExact requires exactly the fixed elements, no
	// extra args. Pattern "git status".
	MatchExact MatchMode = iota
	// MatchTrailing requires 1+ args after the fixed
	// elements. Pattern "git *".
	MatchTrailing
	// MatchPrefix allows 0+ args after the fixed
	// elements. Pattern "git:*" (Claude Code syntax).
	MatchPrefix
)

// Pattern is a parsed permission pattern from a command
// entry. Reason is the optional explanation surfaced in
// hook output ("<pattern> - <reason>  (from <source>)").
// Empty when loaded from a Claude Code settings.json
// (its array shape has no slot for a reason) or when the
// preset/config entry omitted one.
type Pattern struct {
	Elements []string  // fixed parts to match
	Raw      string    // original pattern text
	Mode     MatchMode // how to handle remaining args
	Reason   string
}

// EnvVarPattern matches an assigned environment variable
// by name. Syntax: exact name (`BASH_ENV`) or a name with
// a trailing `*` for prefix match (`LD_*` covers
// LD_PRELOAD, LD_LIBRARY_PATH, etc.). No value matching —
// the schema concerns itself only with which variables
// can be assigned, not what they're assigned to.
type EnvVarPattern struct {
	Raw    string // original entry, e.g. "LD_*"
	Match  string // entry minus trailing "*"
	Prefix bool   // true when Raw ends with "*"
	Reason string
}

// TierEntries holds one tier's entries split by tool axis.
// Each axis resolves independently (a Commands match in a
// higher source does not lock out EnvVars from being
// consulted in lower sources, and vice versa).
type TierEntries struct {
	Commands []Pattern
	EnvVars  []EnvVarPattern
}

// SourcePerms holds one config source's entries, classified
// by tier and tool axis. The hook checks sources in
// priority order per axis; within a single source, the
// strongest tier with a match wins
// (Deny > Ask > Allow > SoftAsk).
type SourcePerms struct {
	// Name is shown in `check` output and reasons —
	// "preset:git", "~/.agents/permissions.json", etc.
	Name string

	// AcceptsReasons is true when the source's schema can
	// carry per-pattern reasons. False for Claude Code
	// settings.json, whose flat-array shape has no slot
	// for reasons. The `validate` subcommand uses this to
	// avoid flagging structurally-empty reasons as
	// "missing" — only sources that *could* have reasons
	// get the empty-reason warning.
	AcceptsReasons bool

	Allow   TierEntries
	SoftAsk TierEntries
	Ask     TierEntries
	Deny    TierEntries
}

// Permissions holds parsed permission rules across every
// config source consulted by the hook. Sources are
// ordered highest priority first; checkOne walks them in
// order and the first source with a matching pattern
// decides.
type Permissions struct {
	Sources []SourcePerms

	// Warnings collects malformed entries rejected at
	// load time. Surfaced by the `check` and `validate`
	// subcommands so users can find typos that silently
	// degrade their policy. The hot path ignores these.
	Warnings []ConfigWarning

	Rules        map[string]*model.CommandRules
	SnippetRules map[string]*model.SnippetLang

	// PathDirs is the set of directories on the hook
	// process's PATH, used to decide whether an absolute-
	// path command can be matched by its basename in
	// Allow/Ask/SoftAsk. `/usr/bin/git` with `/usr/bin`
	// in PATH means the shell would have resolved `git`
	// to the same binary, so the basename match is safe.
	// An out-of-PATH absolute path requires an explicit
	// absolute-path pattern. Populated by the loader from
	// os.Getenv("PATH"); injectable in tests.
	PathDirs map[string]struct{}

	// Harness provides the harness-specific surfaces (text
	// strings today, more later) so output can vary by
	// agent harness without the resolution code branching.
	// Loader sets a default; tests can inject.
	Harness harness.Harness
}

// ConfigWarning is a single rejected entry from a
// permission source. Source identifies where it came from
// ("preset:git", "~/.agents/permissions.json", etc.),
// Entry is the rejected raw text, and Reason explains why
// parsePattern refused it.
type ConfigWarning struct {
	Source string
	Entry  string
	Reason string
}

// Result is the outcome of checking all commands.
type Result struct {
	Decision model.Decision
	Reason   string
}

// DenyResult builds a deny Result with the grouped
// "Deny:" format for a single reason. Used by callers
// that produce denials outside of Check (e.g. breakdown
// errors).
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
	// sourceRules means the rules layer decided.
	sourceRules checkSource = iota
	// sourcePattern means a permission pattern matched.
	sourcePattern
	// sourceNone means no rule or pattern matched.
	sourceNone
)

// commandCheck is the raw result of evaluating a single
// command. It carries enough information for the caller to
// format the grouped output without checkOne needing to
// know about presentation.
//
// Output is rendered as "<subject>[ - <description>]
// (from <sourceName>)". The two-field split lets pattern
// layer and rules layer share a renderer:
//
//   - Pattern layer: subject is the pattern (pat.Raw),
//     description is the user-supplied reason from the
//     preset/config map (may be empty).
//   - Rules layer: subject and description come from
//     splitting the rule's "<subject>: <description>"
//     prose on the first ": ".
//   - Env-var match: subject is the var name (or the
//     pattern's Raw if a prefix match), description is
//     the pattern's reason.
type commandCheck struct {
	decision    model.Decision
	source      checkSource
	subject     string
	description string
	// sourceName identifies which SourcePerms produced
	// a sourcePattern decision. Used to attribute matches
	// to their source (e.g. "preset:git") in the reason
	// text shown to users.
	sourceName string
	// args holds the command's string arguments. Set
	// only for sourceNone (unknown commands) so the
	// caller can compute a suggestion pattern.
	args []string
}

// labelCollector is a deduplicated ordered list of
// strings. Used in Check to collect deny labels, ask
// labels, unknown suggestions, and script paths without
// duplicates.
type labelCollector struct {
	items []string
	seen  map[string]bool
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

// parsePattern converts a raw pattern string ("git status",
// "git log *", "git commit:*", "*") into a Pattern.
// Returns false for degenerate inputs (empty, empty fixed
// elements, etc.) so callers can silently skip them.
func parsePattern(raw string) (Pattern, error) {
	elements := strings.Fields(raw)
	// Empty raw — the legacy behaviour of treating these
	// as match-all was a footgun. Match-all is still
	// available via bare `Bash` or `Bash(*)`, both of
	// which normalise to raw="*" in extractBashPattern
	// and yield elements=["*"].
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

	// Reject patterns with an empty fixed element. E.g.
	// `:*` collapses to [""] with MatchPrefix, which
	// would never sensibly match anything and suggests
	// a malformed entry.
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

// envVarPatternRE constrains entries to a POSIX-shape env
// var name with an optional trailing `*`. The hook will
// never see an assignment whose name doesn't match this
// shape (bash itself rejects them at parse time), so
// rejecting anything looser keeps validate honest about
// what's load-bearing.
var envVarPatternRE = regexp.MustCompile(
	`^[A-Za-z_][A-Za-z0-9_]*\*?$`)

// parseEnvVarPattern converts a raw entry into an
// EnvVarPattern. `LD_*` becomes a prefix match against
// `LD_`; `BASH_ENV` matches that exact name. Empty
// patterns and patterns with characters that can never
// appear in an env var name are rejected.
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

// MatchEnvVar reports whether the assigned variable name
// matches this pattern.
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

// Env-var policy used to live in two hard-coded maps here
// (dangerousEnvVars, suspiciousEnvVars). Both were moved
// into the escape-hatches preset's Deny.EnvVars and
// SoftAsk.EnvVars maps so the same source-priority and
// override semantics that govern commands now govern
// environment variables.

// Check evaluates all extracted commands against the
// permissions and dangerous pattern checks.
func (p *Permissions) Check(
	result model.BreakdownResult,
) Result {
	aggregate := model.Allow
	var undecidedReason string

	// Collectors for grouped formatting (deduplicated).
	// Each axis (commands, env vars, snippets) feeds the
	// same four label lists; the resolution is independent
	// per axis but the final decision aggregates across
	// them via tier precedence.
	var denies, asks, softAsks labelCollector
	var unknowns, scripts labelCollector

	// Env-var axis. Each assigned variable name is resolved
	// independently against EnvVars patterns in the source
	// stack, exactly like commands. Decisions feed the same
	// aggregate so deny/ask/soft-ask from env vars
	// participate in tier precedence with command and
	// snippet decisions.
	for _, name := range result.Assigns {
		check := p.checkOneEnvVar(name)
		// The Allow path doesn't bake the matched name
		// into the commandCheck (Allow.EnvVars overrides
		// don't surface in output), so plug the assigned
		// name in for Deny/Ask/SoftAsk rendering.
		check.subject = name
		switch check.decision {
		case model.Deny:
			aggregate = model.Deny
			denies.add(formatCheck(check, model.Command{}))
		case model.Ask:
			if aggregate == model.Deny {
				continue
			}
			aggregate = model.Ask
			asks.add(formatCheck(check, model.Command{}))
		case model.SoftAsk:
			if aggregate == model.Deny {
				continue
			}
			if aggregate != model.Ask {
				aggregate = model.SoftAsk
			}
			softAsks.add(
				formatCheck(check, model.Command{}))
		}
	}

	cmds := result.Commands
	snippets := result.CodeSnippets
	// Skip the no-commands early return when env vars
	// already produced any decision — fall through to emit
	// the labels. Without env vars to evaluate (or with all
	// of them allowed), bare safe input still allows.
	if len(cmds) == 0 && len(snippets) == 0 &&
		aggregate == model.Allow {
		if result.Safe {
			return Result{Decision: model.Allow}
		}
		return Result{Decision: model.Undecided}
	}

	for _, cmd := range cmds {
		check := p.checkOne(cmd)

		switch check.decision {
		case model.Deny:
			aggregate = model.Deny
			denies.add(formatCheck(check, cmd))
			if cmd.SourcePath != "" {
				scripts.add(word.DirectPath(
					cmd.RootScript))
			}
		case model.Ask:
			// Deny takes priority — skip
			// collection when already denied.
			if aggregate == model.Deny {
				continue
			}
			aggregate = model.Ask
			asks.add(formatCheck(check, cmd))
		case model.SoftAsk:
			if aggregate == model.Deny {
				continue
			}
			if aggregate != model.Ask {
				aggregate = model.SoftAsk
			}
			softAsks.add(formatCheck(check, cmd))
		case model.Undecided:
			if aggregate == model.Deny {
				continue
			}
			if aggregate != model.Ask &&
				aggregate != model.SoftAsk {
				aggregate = model.Undecided
			}
			if len(check.args) > 0 {
				unknowns.add(p.buildPermissionSuggestion(
					check.args,
					cmd.SourcePath))
			} else {
				undecidedReason = check.subject
			}
		case model.Allow:
			// Lowest priority, keep going.
		}
	}

	// Process code snippets (e.g. Python source).
	// Snippets only produce Deny, Ask, SoftAsk, or
	// Allow.
	for i := range snippets {
		snippetResult := p.checkSnippet(&snippets[i])
		switch snippetResult.decision {
		case model.Deny:
			aggregate = model.Deny
			denies.add(snippetResult.subject)
		case model.Ask:
			if aggregate == model.Deny {
				continue
			}
			aggregate = model.Ask
			asks.add(snippetResult.subject)
		case model.SoftAsk:
			if aggregate == model.Deny {
				continue
			}
			if aggregate != model.Ask {
				aggregate = model.SoftAsk
			}
			softAsks.add(
				snippetResult.subject)
		case model.Allow:
			// Clean snippet or user override.
		}
	}

	// Undecided with collected unknowns means unknown
	// commands were seen. Promote to SoftAsk so the
	// hook can apply mode-dependent behavior (ask in
	// normal, fall through in auto). Pure undecided
	// (no unknowns) is a true "no opinion" — the hook
	// always falls through.
	if aggregate == model.Undecided {
		if len(unknowns.items) > 0 {
			aggregate = model.SoftAsk
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

	// When deny is present, only show denies.
	if aggregate == model.Deny {
		reason := formatResult(
			nil, nil, nil, denies.items, unknownHeader)
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
		Decision: aggregate,
		Reason: formatResult(
			asks.items, softAsks.items,
			unknowns.items, nil, unknownHeader),
	}
}

// sourceGuidance builds the footer guidance for denies
// that came from sourced files.
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

// formatCheck produces the display label for a matched
// check (Deny/Ask/SoftAsk). Pattern-layer matches show the
// pattern as-is with `:*` form preserved, so the label can
// be pasted into an Allow list when applicable. Rule-layer
// matches surface the rule's subject and description with
// a dash separator, distinguishing them visually from
// patterns; the subject becomes a kebab-case source label
// (e.g. "rule:git-branch") so a future configurability
// path can address the rule by name. Empty descriptions
// drop the dash and surface only the subject — common for
// Claude Code settings.json (no reason slot) and presets
// where a reason wasn't worth writing.
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
		if check.subject != "" {
			source = "rule:" + strings.ReplaceAll(
				check.subject, " ", "-")
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

// splitRuleReason splits a rule-layer reason on the first
// ": " separator. Rule reasons follow the convention
// "<subject>: <description>" (e.g. "git branch: write flag
// -d") so the subject can drive the source attribution and
// the description appears after a dash. Returns (reason,
// "") when there's no separator — the whole string is a
// complete thought and renders as the subject alone.
func splitRuleReason(
	reason string,
) (subject, description string) {
	i := strings.Index(reason, ": ")
	if i < 0 {
		return reason, ""
	}
	return reason[:i], reason[i+2:]
}

// checkSnippet evaluates a code snippet against snippet
// rules. First checks the SourceScript against user
// permissions — an explicit allow/ask/deny overrides
// scanning. Otherwise runs snippet rules for the
// language and returns the strongest finding.
func (p *Permissions) checkSnippet(
	snippet *model.CodeSnippet,
) commandCheck {
	// Check SourceScript against permissions. For
	// file-based code, the pattern decision wins. For
	// inline -c/-e code, only pattern Deny/Ask win:
	// pattern Allow does NOT suppress snippet scanning,
	// because agent-generated inline code is never
	// user-reviewed, so dangerous-pattern matching is
	// a hard floor even under broad allows.
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

	// Run snippet rules for this language.
	lang := p.SnippetRules[snippet.Language]
	if lang == nil || len(lang.Rules) == 0 {
		return commandCheck{decision: model.Allow}
	}

	code := snippet.Code
	if lang.StripComments != nil {
		code = lang.StripComments(code)
	}

	var reasons []string
	strongest := model.Allow
	for i := range lang.Rules {
		if lang.Rules[i].Check(code) {
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

	// Build the reason with source context.
	source := "inline code"
	if snippet.SourceFile != "" {
		source = snippet.SourceFile
	}
	reason := fmt.Sprintf(
		"%s: %s",
		source, strings.Join(reasons, ", "))

	// Inline code (-c) keeps the original decision
	// (deny). File-based code is downgraded to ask.
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
		// Snippet reasons are pre-composed
		// "<source>: <findings>" strings; they bypass
		// formatCheck and get dumped to the user verbatim
		// so source attribution and `<thing> - <reason>`
		// formatting wouldn't add anything here. Park the
		// composed string in subject; description stays
		// empty.
		subject: reason,
	}
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

	name := filepath.Base(word.Text(cmd.Args[0]))

	// 1. Rules layer — operates on Words directly,
	// no string conversion needed.
	if p.Rules != nil {
		if action := Evaluate(
			p.Rules, name, cmd.Args[1:],
		); action != nil {
			subject, desc := splitRuleReason(
				action.Reason)
			return commandCheck{
				decision:    action.Decision,
				source:      sourceRules,
				subject:     subject,
				description: desc,
			}
		}
	}

	// 2. Pattern matching across sources, highest
	// priority first. The first source with any matching
	// pattern decides via within-source tier precedence
	// (Deny > Ask > Allow > SoftAsk). Lower-priority
	// sources are never consulted for this command once
	// a higher source has matched.
	argTexts := word.Texts(cmd.Args)
	var stripped []string
	if strings.Contains(argTexts[0], "/") {
		stripped = make([]string, len(argTexts))
		copy(stripped, argTexts)
		stripped[0] = filepath.Base(argTexts[0])
	}

	// pathResolved is the basename-stripped form that
	// non-Deny tiers may use when the absolute path's
	// directory is in PATH — i.e. when the shell would
	// have resolved the bare name to this same binary. An
	// out-of-PATH absolute path falls through to require
	// an explicit absolute-path pattern. Deny never uses
	// this; it strips unconditionally for safety.
	var pathResolved []string
	if stripped != nil && filepath.IsAbs(argTexts[0]) {
		if _, ok := p.PathDirs[filepath.Dir(argTexts[0])]; ok {
			pathResolved = stripped
		}
	}

	for _, src := range p.Sources {
		// Walk this source's tiers in deny > ask > allow >
		// soft-ask precedence. Deny uniquely retries with
		// the unconditional basename-stripped form so that
		// an absolute-path invocation can't slip past a
		// bare-name deny pattern — a safety net regardless
		// of where the path points. Allow/Ask/SoftAsk use
		// the PATH-gated form so an out-of-PATH binary at
		// `/tmp/evil/curl` doesn't auto-match `curl:*`.
		// Allow before SoftAsk: an explicit Allow opts out
		// of a SoftAsk prompt for commands the user has
		// trusted broadly.
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

	// 3. Function call override — checked after all
	// sources because an explicit deny anywhere should
	// still win over the function-call escape hatch.
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
		args:     argTexts,
	}
}

// matchFirst returns the first pattern in patterns that
// matches args, or (Pattern{}, false) if none match.
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

// matchTier tries patterns against the raw args, then
// against the basename-stripped form. On a match it builds
// the commandCheck for the given tier. For Allow tier the
// reason is left empty — Allow doesn't surface in output —
// but the source is still recorded so attribution works
// downstream if a caller introspects.
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

// commandCheckFromPattern builds the commandCheck for a
// pattern-layer match. Subject is the matched pattern;
// description is the preset/config-supplied reason (may be
// empty). The via suffix is appended to the subject only
// when the match came from the basename-stripped retry,
// telling the user the path-prefixed invocation hit a
// bare-name pattern.
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

// checkOneEnvVar walks sources looking for an EnvVars match
// for the given assigned name. Returns the strongest
// match's commandCheck, or a commandCheck with decision ==
// Undecided when no source has any opinion. Tier precedence
// is Deny > Ask > Allow > SoftAsk, same as commands.
func (p *Permissions) checkOneEnvVar(
	name string,
) commandCheck {
	for _, src := range p.Sources {
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
		if _, ok := matchEnvVarFirst(
			src.Allow.EnvVars, name,
		); ok {
			return commandCheck{
				decision: model.Allow,
				source:   sourcePattern,
			}
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

// envVarCheck builds the commandCheck for an env-var
// pattern match. Subject is the matched pattern's Raw
// (e.g. "BASH_ENV" or "LD_*"); description is the preset/
// config-supplied reason.
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

// matchEnvVarFirst returns the first pattern that matches
// the given name, or (zero, false) if none match.
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

// --- Smart pattern suggestion ---

// buildPermissionSuggestion builds the "Bash(cmd:*)"
// entry shown for unknown commands, with optional source
// path annotation.
func (p *Permissions) buildPermissionSuggestion(
	args []string, sourcePath string,
) string {
	s := "Bash(" + p.buildPermissionPattern(args) + ")"
	if sourcePath != "" {
		s += " (from " + sourcePath + ")"
	}
	return s
}

// buildPermissionPattern produces the shortest useful
// :* pattern for a command that has no matching rule or
// pattern. It checks existing patterns and the rules
// registry to find the shortest prefix that isn't
// already "known", so git apply → git apply:* (not
// git:*) when git has existing rules but apply does not.
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

// compoundFlags maps commands to flags that consume
// the next arg as part of the command identity. For
// suggestion purposes, "python3 -m module" should
// suggest "python3 -m module:*", not "python3:*".
var compoundFlags = map[string]map[string]bool{
	"python":  {"-m": true},
	"python3": {"-m": true},
	"java":    {"-jar": true},
}

// commandPrefix extracts at most 2 non-flag,
// subcommand-like elements from the start of args.
// Flags (-...) and path-like args (/, .) stop the
// prefix. For known compound flag-arg patterns (e.g.
// python3 -m module), the flag and its argument are
// included in the prefix.
func commandPrefix(args []string) []string {
	var prefix []string
	for i, arg := range args {
		if i > 0 && !looksLikeSubcommand(arg) {
			// Check for compound flag-arg
			// patterns before giving up.
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

// looksLikeSubcommand returns true for words that look
// like CLI subcommands (e.g. "apply", "run") and false
// for flags, paths, filenames, and key=value pairs.
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

// prefixKnown checks whether any existing pattern or
// rules registry entry shares the given command prefix.
// Walks every source's tiers since suggestions should
// reflect the full active policy.
func (p *Permissions) prefixKnown(
	prefix []string,
) bool {
	// Rules registry keys cover depth 1 (command name).
	if len(prefix) == 1 && p.Rules != nil {
		if _, ok := p.Rules[prefix[0]]; ok {
			return true
		}
	}

	for _, src := range p.Sources {
		for _, patterns := range [][]Pattern{
			src.Allow.Commands, src.SoftAsk.Commands,
			src.Ask.Commands, src.Deny.Commands,
		} {
			for _, pat := range patterns {
				if patternSharesPrefix(pat, prefix) {
					return true
				}
			}
		}
	}
	return false
}

// patternSharesPrefix checks whether a pattern's
// elements share a prefix with the given command prefix.
// Catch-all patterns (empty elements) are skipped.
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

// formatResult builds the grouped reason string from
// collected labels. Ask, soft-ask, and unknown-suggestion
// sections render under their own headings. Soft-ask is
// formatted in the "to allow, add..." style with source
// attribution; unknown commands get the same
// "add-to-permissions" framing, parameterised by the
// harness's preferred phrasing (Claude Code references
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
