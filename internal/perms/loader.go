package perms

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/agentconfig"
	"github.com/sothatsit/agent-permissions/internal/harness"
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/rules"
	"github.com/sothatsit/agent-permissions/presets"
)

// Resolved is one harness's full resolution: the normal
// source chain and enforced policy to evaluate, plus the
// resolved per-rule config the breakdown consults.
// RuleConfig is harness-agnostic —
// it comes only from presets and .agents, never from a
// harness's native settings — so when multi-harness support
// lands each harness's Resolved shares the same RuleConfig
// and differs only in its native Sources.
type Resolved struct {
	Permissions *Permissions
	RuleConfig  model.RuleConfigs
}

// Resolve reads every config source and returns the full
// resolved view: the Permissions to evaluate against (with
// per-source attribution) and the resolved per-rule config.
// Used by both the hook and the `check` subcommand so they
// resolve identically.
//
// configDir is CLAUDE_CONFIG_DIR (defaults to ~/.claude).
// cwd is the project directory. Either may be empty;
// missing files are silently skipped.
//
// Normal-source priority, highest → lowest (the order
// Permissions.Sources lands in):
//  1. <cwd>/.claude/settings.local.json
//  2. <cwd>/.claude/settings.json
//  3. <configDir>/settings.json (Claude Code user)
//  4. <cwd>/.agents/permissions.local.json (explicit)
//  5. <cwd>/.agents/permissions.json (explicit entries)
//  6. ~/.agents/permissions.json (explicit entries)
//  7. Ordinary external presets
//  8. Embedded presets
//
// Ordinary and embedded presets are filtered by enabled-/
// disabled-presets from the most-specific .agents config
// that specifies either field (local beats project beats
// global). Enforced external presets are always active and
// resolve separately: all matching enforced entries combine
// by strength, then that result combines with normal policy
// by strength.
//
// permissions.local.json mirrors Claude Code's
// settings.local.json: a project-scoped, typically
// gitignored personal override that sits above the
// committed project config. There is no global local
// variant, again matching Claude Code.
func Resolve(
	configDir, cwd string,
) (*Resolved, error) {
	snapshot, err := LoadPolicySnapshot(cwd)
	if err != nil {
		return nil, err
	}
	return snapshot.Resolve(configDir)
}

// Resolve builds the effective policy from this captured shared policy and
// the harness-native Claude settings selected by configDir.
func (snapshot *PolicySnapshot) Resolve(
	configDir string,
) (*Resolved, error) {
	globalAgent, projectAgent, localAgent :=
		snapshot.resolutionConfigs()
	selected := selectPresets(
		snapshot.presets.available,
		globalAgent, projectAgent, localAgent)

	// Build sources highest → lowest priority. Each
	// source-load may return ConfigWarnings for malformed
	// entries; they accumulate into Permissions.Warnings.
	var sources []SourcePerms
	var enforcedSources []SourcePerms
	var warnings []ConfigWarning

	addClaude := func(path, label string) error {
		src, w, err := loadClaudeSettings(path)
		if err != nil {
			return fmt.Errorf("%s: %v", label, err)
		}
		if src != nil {
			sources = append(sources, *src)
			warnings = append(warnings, w...)
		}
		return nil
	}

	if snapshot.cwd != "" {
		if err := addClaude(filepath.Join(
			snapshot.cwd, ".claude", "settings.local.json"),
			"local settings"); err != nil {
			return nil, err
		}
		if err := addClaude(filepath.Join(
			snapshot.cwd, ".claude", "settings.json"),
			"project settings"); err != nil {
			return nil, err
		}
	}

	if configDir != "" {
		if err := addClaude(filepath.Join(
			configDir, "settings.json"),
			"user settings"); err != nil {
			return nil, err
		}
	}

	for _, loaded := range snapshot.AgentConfigs() {
		src, w := fromAgentConfig(
			loaded.SourceName, loaded.Config)
		sources = append(sources, src)
		warnings = append(warnings, w...)
	}

	for _, p := range selected {
		src, w := fromPreset(p)
		if p.Enforced {
			enforcedSources = append(
				enforcedSources, src)
		} else {
			sources = append(sources, src)
		}
		warnings = append(warnings, w...)
	}

	return &Resolved{
		RuleConfig: resolveRuleConfig(
			globalAgent, projectAgent, localAgent,
			selected),
		Permissions: &Permissions{
			Sources:         sources,
			EnforcedSources: enforcedSources,
			Warnings:        warnings,
			PathDirs:        parsePathDirs(os.Getenv("PATH")),
			// Resolve doesn't know which harness is the
			// consumer. Tools that produce harness-bound
			// output (claude-hook) replace this with the
			// concrete harness; tools that don't (check,
			// validate) keep the Placeholder so
			// harness-specific strings appear as visibly
			// marked placeholders rather than silently
			// pretending to be one harness or another.
			Harness: harness.Placeholder{},
		},
	}, nil
}

// parsePathDirs splits the PATH value into a set of
// cleaned directory paths. Empty PATH or empty entries
// yield an empty set; the absolute-path basename match
// then falls through, requiring an explicit absolute-path
// pattern.
func parsePathDirs(path string) map[string]struct{} {
	dirs := map[string]struct{}{}
	for _, d := range filepath.SplitList(path) {
		if d == "" {
			continue
		}
		dirs[filepath.Clean(d)] = struct{}{}
	}
	return dirs
}

// validateExternalPresets rejects semantic mistakes that
// structural JSON decoding cannot catch. External presets
// carry organisation policy, so dropping a bad entry or
// silently ignoring a rule typo would weaken that policy.
// User config keeps the warning-only behavior so users can
// diagnose and repair a bad entry without losing the hook.
func validateExternalPresets(
	all []*presets.Preset,
) error {
	registry, _ := rules.Registry()
	var problems []string
	for _, p := range all {
		if p.Dir == "" {
			continue
		}

		src, warnings := fromPreset(p)
		for _, w := range warnings {
			problems = append(problems, fmt.Sprintf(
				"%s (%s): %q (%s)",
				w.Source, p.Dir, w.Entry, w.Reason))
		}
		for _, pat := range sourceCommandPatterns(src) {
			owner, ok := ruleOwnedPattern(pat, registry)
			if !ok {
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"%s (%s): command pattern %q overlaps "+
					"rule-owned %q",
				src.Name, p.Dir, pat.Raw, owner))
		}

		ids := make([]string, 0, len(p.Rules))
		for id := range p.Rules {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if !rules.IsRuleID(id) {
				problems = append(problems, fmt.Sprintf(
					"preset:%s (%s): unknown rule ID %q",
					p.Name, p.Dir, id))
				continue
			}
			if p.Enforced && !p.Rules[id].Enabled {
				problems = append(problems, fmt.Sprintf(
					"preset:%s (%s): enforced rule %q "+
						"must have Enabled true",
					p.Name, p.Dir, id))
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf(
		"external preset validation failed: %s",
		strings.Join(problems, "; "))
}

func sourceCommandPatterns(src SourcePerms) []Pattern {
	var out []Pattern
	for _, tier := range []TierEntries{
		src.Allow, src.SoftAsk, src.Ask, src.Deny,
	} {
		out = append(out, tier.Commands...)
	}
	return out
}

func ruleOwnedPattern(
	pat Pattern,
	registry map[string]*model.CommandRules,
) (string, bool) {
	commands := make([]string, 0, len(registry))
	for command := range registry {
		commands = append(commands, command)
	}
	sort.Strings(commands)

	for _, command := range commands {
		commandRules := registry[command]
		if commandRules.OwnsAllPatterns {
			bareOnly := commandRules.PathMode == model.PathAllow
			prefix := []string{command}
			if patternOverlapsOwnedPrefix(
				pat, prefix, bareOnly,
			) {
				return command, true
			}
		}
		for _, relative := range commandRules.OwnedPatternPrefixes {
			prefix := append(
				[]string{command}, relative...)
			if patternOverlapsOwnedPrefix(
				pat, prefix, false,
			) || patternOverlapsNormalizedOwnedPrefix(
				pat, prefix,
				commandRules.PatternPrefixSkips,
				commandRules.PathMode == model.PathSkip,
			) {
				return strings.Join(prefix, " "), true
			}
		}
	}
	return "", false
}

type patternTokenConstraint struct {
	literal string
	any     bool
}

func patternOverlapsNormalizedOwnedPrefix(
	pat Pattern,
	prefix []string,
	skips []model.PatternPrefixSkip,
	bareOnly bool,
) bool {
	if len(skips) == 0 || len(pat.Elements) == 0 {
		return false
	}
	if !commandElementOverlaps(
		pat.Elements[0], prefix[0], bareOnly,
	) {
		return false
	}

	owner := make([]patternTokenConstraint, 0, len(prefix)-1)
	for _, element := range prefix[1:] {
		owner = append(owner, patternTokenConstraint{
			literal: element,
		})
	}

	var leading []patternTokenConstraint
	var search func(int) bool
	search = func(depth int) bool {
		if depth > len(pat.Elements) {
			return false
		}

		for _, skip := range skips {
			before := len(leading)
			leading = append(leading, patternTokenConstraint{
				literal: skip.Option,
			})
			for range skip.Arguments {
				leading = append(leading,
					patternTokenConstraint{any: true})
			}
			candidate := append(
				append([]patternTokenConstraint{}, leading...),
				owner...)
			if patternOverlapsTokenConstraints(pat, candidate) {
				return true
			}
			if search(depth + 1) {
				return true
			}
			leading = leading[:before]
		}
		return false
	}
	return search(1)
}

func patternOverlapsTokenConstraints(
	pat Pattern,
	args []patternTokenConstraint,
) bool {
	shared := min(len(pat.Elements)-1, len(args))
	for i := 0; i < shared; i++ {
		constraint := args[i]
		if !constraint.any &&
			!globMatch(pat.Elements[i+1], constraint.literal) {
			return false
		}
	}
	candidateLength := len(args) + 1
	return len(pat.Elements) >= candidateLength ||
		pat.Mode != MatchExact
}

func patternOverlapsOwnedPrefix(
	pat Pattern, prefix []string, bareOnly bool,
) bool {
	if len(pat.Elements) == 0 {
		return pat.Mode != MatchExact
	}
	if !commandElementOverlaps(
		pat.Elements[0], prefix[0], bareOnly,
	) {
		return false
	}

	shared := min(len(pat.Elements), len(prefix))
	for i := 1; i < shared; i++ {
		if !globMatch(pat.Elements[i], prefix[i]) {
			return false
		}
	}
	return len(pat.Elements) >= len(prefix) ||
		pat.Mode != MatchExact
}

func commandElementOverlaps(
	pattern, command string, bareOnly bool,
) bool {
	if globMatch(pattern, command) {
		return true
	}
	if bareOnly {
		return false
	}
	return globLanguagesOverlap(
		pattern, "*/"+command)
}

// globLanguagesOverlap reports whether two patterns using
// this package's single `*` wildcard can match the same
// string. Command ownership needs language intersection,
// not filepath.Base(pattern), because `*` can cross `/`.
func globLanguagesOverlap(a, b string) bool {
	type state struct{ a, b int }
	queue := []state{{}}
	seen := map[state]bool{}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if seen[current] {
			continue
		}
		seen[current] = true
		if current.a == len(a) && current.b == len(b) {
			return true
		}

		aStar := current.a < len(a) && a[current.a] == '*'
		bStar := current.b < len(b) && b[current.b] == '*'
		if aStar {
			queue = append(queue, state{current.a + 1, current.b})
		}
		if bStar {
			queue = append(queue, state{current.a, current.b + 1})
		}
		if current.a == len(a) || current.b == len(b) {
			continue
		}

		switch {
		case aStar && bStar:
			// Consuming a character leaves both stars in the
			// same state. Their epsilon moves above provide
			// every route that can make progress.
		case aStar:
			queue = append(queue, state{current.a, current.b + 1})
		case bStar:
			queue = append(queue, state{current.a + 1, current.b})
		case a[current.a] == b[current.b]:
			queue = append(queue, state{current.a + 1, current.b + 1})
		}
	}
	return false
}

// selectPresets returns the presets from all (kept in the
// given order) selected by the most-specific agent config
// that specifies preset selection — local, else project,
// else global — otherwise every preset. `enabled-presets`
// narrows ordinary external and embedded presets to a
// whitelist; `disabled-presets` then filters that result.
// Enforced presets are always retained.
func selectPresets(
	all []*presets.Preset,
	global, project, local *agentconfig.Config,
) []*presets.Preset {
	// Checked least- to most-specific so the most-specific
	// config that has an opinion is the one left in cfg.
	cfg := global
	if project.HasPresetSelection() {
		cfg = project
	}
	if local.HasPresetSelection() {
		cfg = local
	}

	if cfg == nil || !cfg.HasPresetSelection() {
		return all
	}

	var enforced, selectable []*presets.Preset
	for _, p := range all {
		if p.Enforced {
			enforced = append(enforced, p)
		} else {
			selectable = append(selectable, p)
		}
	}

	out := selectable
	if cfg.EnabledPresets != nil {
		out = filterByName(out, *cfg.EnabledPresets, true)
	}
	if cfg.DisabledPresets != nil {
		out = filterByName(
			out, *cfg.DisabledPresets, false)
	}
	return append(enforced, out...)
}

// resolveRuleConfig resolves per-rule config across the
// rule-config sources. Rules ship default-OFF in code, so a
// rule absent from every source stays disabled. Ordinary
// presets form the base, then global/project/local .agents
// overrides apply. Enforced Enabled:true entries apply last,
// locking those rules on. External validation rejects
// Enabled:false in enforced presets. Claude settings.json
// does not participate — rule config is an agent-permissions
// concept kept in the shared layers, which is what makes it
// identical across harnesses.
func resolveRuleConfig(
	global, project, local *agentconfig.Config,
	selected []*presets.Preset,
) model.RuleConfigs {
	out := model.RuleConfigs{}
	for i := len(selected) - 1; i >= 0; i-- {
		if selected[i].Enforced {
			continue
		}
		for id, cfg := range selected[i].Rules {
			out[id] = cfg
		}
	}
	if global != nil {
		for id, cfg := range global.Rules {
			out[id] = cfg
		}
	}
	if project != nil {
		for id, cfg := range project.Rules {
			out[id] = cfg
		}
	}
	if local != nil {
		for id, cfg := range local.Rules {
			out[id] = cfg
		}
	}
	for i := len(selected) - 1; i >= 0; i-- {
		if !selected[i].Enforced {
			continue
		}
		for id, cfg := range selected[i].Rules {
			if cfg.Enabled {
				out[id] = cfg
			}
		}
	}
	return out
}

func filterByName(
	in []*presets.Preset,
	names []string,
	include bool,
) []*presets.Preset {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	var out []*presets.Preset
	for _, p := range in {
		if set[p.Name] == include {
			out = append(out, p)
		}
	}
	return out
}

func fromPreset(p *presets.Preset) (SourcePerms, []ConfigWarning) {
	name := "preset:" + p.Name
	if p.Enforced {
		name = "enforced-preset:" + p.Name
	}
	src := SourcePerms{Name: name, AcceptsReasons: true}
	var warnings []ConfigWarning

	src.Allow, warnings = parseTier(
		warnings, name,
		p.Allow.Commands, p.Allow.EnvVars)
	src.SoftAsk, warnings = parseTier(
		warnings, name,
		p.SoftAsk.Commands, p.SoftAsk.EnvVars)
	src.Ask, warnings = parseTier(
		warnings, name,
		p.Ask.Commands, p.Ask.EnvVars)
	src.Deny, warnings = parseTier(
		warnings, name,
		p.Deny.Commands, p.Deny.EnvVars)
	return src, warnings
}

func fromAgentConfig(
	name string, c *agentconfig.Config,
) (SourcePerms, []ConfigWarning) {
	src := SourcePerms{Name: name, AcceptsReasons: true}
	var warnings []ConfigWarning

	src.Allow, warnings = parseTier(
		warnings, name,
		c.Allow.Commands, c.Allow.EnvVars)
	src.SoftAsk, warnings = parseTier(
		warnings, name,
		c.SoftAsk.Commands, c.SoftAsk.EnvVars)
	src.Ask, warnings = parseTier(
		warnings, name,
		c.Ask.Commands, c.Ask.EnvVars)
	src.Deny, warnings = parseTier(
		warnings, name,
		c.Deny.Commands, c.Deny.EnvVars)
	return src, warnings
}

// loadClaudeSettings reads a Claude Code settings.json
// and returns a SourcePerms (or nil if the file doesn't
// exist) plus any malformed-pattern warnings. Entries are
// wrapped as `Bash(...)`; non-Bash wrappers (Edit, Read,
// WebFetch, etc.) are skipped via extractBashPattern
// without warning since they're valid Claude Code entries
// for other tools. Reasons are always empty here — the
// Claude Code schema has no slot for them. EnvVars cannot
// be expressed in Claude Code settings.
func loadClaudeSettings(
	path string,
) (*SourcePerms, []ConfigWarning, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var settings struct {
		Permissions struct {
			Allow   []string `json:"allow"`
			SoftAsk []string `json:"softAsk"`
			Ask     []string `json:"ask"`
			Deny    []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, nil, fmt.Errorf(
			"invalid JSON in %s: %v", path, err)
	}

	src := SourcePerms{Name: path}
	var warnings []ConfigWarning
	src.Allow.Commands, warnings = appendParsedClaude(
		warnings, path,
		settings.Permissions.Allow, src.Allow.Commands)
	src.SoftAsk.Commands, warnings = appendParsedClaude(
		warnings, path,
		settings.Permissions.SoftAsk, src.SoftAsk.Commands)
	src.Ask.Commands, warnings = appendParsedClaude(
		warnings, path,
		settings.Permissions.Ask, src.Ask.Commands)
	src.Deny.Commands, warnings = appendParsedClaude(
		warnings, path,
		settings.Permissions.Deny, src.Deny.Commands)
	return &src, warnings, nil
}

// parseTier parses one tier's entries (commands and env
// vars) into typed patterns and records ConfigWarnings for
// rejected entries. Both maps may be nil; a nil map yields
// an empty axis. Commands and EnvVars are stored on
// TierEntries; the resolution layer walks each axis
// independently.
func parseTier(
	warnings []ConfigWarning,
	source string,
	commands, envVars map[string]string,
) (TierEntries, []ConfigWarning) {
	var t TierEntries
	t.Commands, warnings = appendCommandMap(
		warnings, source, commands)
	t.EnvVars, warnings = appendEnvVarMap(
		warnings, source, envVars)
	return t, warnings
}

// appendCommandMap parses a pattern→reason map into
// Patterns with the reason attached. Order is unstable
// across runs (Go map iteration), so the result is sorted
// by Raw for deterministic output.
func appendCommandMap(
	warnings []ConfigWarning,
	source string,
	entries map[string]string,
) ([]Pattern, []ConfigWarning) {
	var into []Pattern
	for raw, reason := range entries {
		pat, err := parsePattern(raw)
		if err != nil {
			warnings = append(warnings, ConfigWarning{
				Source: source,
				Entry:  raw,
				Reason: err.Error(),
			})
			continue
		}
		pat.Reason = reason
		into = append(into, pat)
	}
	sortPatterns(into)
	return into, warnings
}

// appendEnvVarMap parses an env var pattern→reason map.
// Same ConfigWarning treatment as commands. Sorted by Raw.
func appendEnvVarMap(
	warnings []ConfigWarning,
	source string,
	entries map[string]string,
) ([]EnvVarPattern, []ConfigWarning) {
	var into []EnvVarPattern
	for raw, reason := range entries {
		pat, err := parseEnvVarPattern(raw, reason)
		if err != nil {
			warnings = append(warnings, ConfigWarning{
				Source: source,
				Entry:  raw,
				Reason: err.Error(),
			})
			continue
		}
		into = append(into, pat)
	}
	sortEnvVarPatterns(into)
	return into, warnings
}

// appendParsedClaude parses Claude Code Bash(...) entries.
// Malformed Bash() bodies (e.g. missing close paren) emit
// a warning; non-Bash wrappers (Read, Edit, WebFetch) are
// silently skipped — they target other tools.
func appendParsedClaude(
	warnings []ConfigWarning,
	source string,
	entries []string,
	into []Pattern,
) ([]Pattern, []ConfigWarning) {
	for _, raw := range entries {
		body, ok := extractBashPattern(raw)
		if !ok {
			if strings.HasPrefix(raw, "Bash(") {
				warnings = append(warnings, ConfigWarning{
					Source: source,
					Entry:  raw,
					Reason: "missing closing paren",
				})
			}
			continue
		}
		pat, err := parsePattern(body)
		if err != nil {
			warnings = append(warnings, ConfigWarning{
				Source: source,
				Entry:  raw,
				Reason: err.Error(),
			})
			continue
		}
		into = append(into, pat)
	}
	sortPatterns(into)
	return into, warnings
}

// sortPatterns sorts in place by Raw for deterministic
// output. The reason text shown to the user (and asserted
// on by tests) is the first matching pattern's Raw — map
// iteration in the loader makes the order non-
// deterministic without this.
func sortPatterns(patterns []Pattern) {
	sort.Slice(patterns, func(i, j int) bool {
		return patterns[i].Raw < patterns[j].Raw
	})
}

// sortEnvVarPatterns sorts env-var patterns by Raw for
// the same reason sortPatterns does — map iteration in
// the loader makes order non-deterministic.
func sortEnvVarPatterns(patterns []EnvVarPattern) {
	sort.Slice(patterns, func(i, j int) bool {
		return patterns[i].Raw < patterns[j].Raw
	})
}
