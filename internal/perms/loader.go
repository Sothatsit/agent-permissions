package perms

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/agentconfig"
	"github.com/sothatsit/agent-permissions/internal/breakdown"
	"github.com/sothatsit/agent-permissions/internal/configjson"
	"github.com/sothatsit/agent-permissions/internal/harness"
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/rules"
	"github.com/sothatsit/agent-permissions/presets"
)

// Resolved is one evaluation-ready policy. Permissions and Breakdown share one
// registry filtered by the resolved rule config.
type Resolved struct {
	Permissions *Permissions

	cwd        string
	registry   map[string]*model.CommandRules
	ruleConfig model.RuleConfigs
}

// Breakdown extracts the commands this policy must evaluate, using the working
// directory and filtered registry from resolution.
func (r *Resolved) Breakdown(
	command string,
) (model.BreakdownResult, error) {
	return breakdown.Breakdown(
		command, r.cwd, r.registry, r.ruleConfig)
}

// Resolve reads every config source and returns an evaluation-ready
// policy. The hook and `check` both use this path, so they resolve
// and evaluate identically.
//
// configDir is CLAUDE_CONFIG_DIR (defaults to ~/.claude) and cwd is
// the project directory. Either may be empty, and missing files are
// skipped.
//
// Ordinary and embedded presets are filtered by
// enabled-/disabled-presets from the most-specific .agents config
// that specifies either field. Enforced external presets are always
// active and resolve separately: matching enforced entries combine
// by strength, then that result combines with normal policy by
// strength.
func Resolve(
	configDir, cwd string,
) (*Resolved, error) {
	snapshot, err := LoadPolicySnapshot(configDir, cwd)
	if err != nil {
		return nil, err
	}

	return snapshot.Resolve(), nil
}

// Resolve builds the effective policy without reading any source again.
func (snapshot *PolicySnapshot) Resolve() *Resolved {
	globalAgent, projectAgent, localAgent :=
		snapshot.resolutionConfigs()
	var selected []*presets.Preset
	for _, policyPreset := range snapshot.presetSelection.Presets {
		if policyPreset.Active() {
			selected = append(selected, policyPreset.Preset)
		}
	}

	ruleConfig := resolveRuleConfig(
		globalAgent, projectAgent, localAgent, selected)
	registry, snippetRules := rules.Registry()
	rules.FilterByConfig(
		registry, snippetRules, ruleConfig)

	// Build sources highest -> lowest priority. ConfigWarnings for
	// malformed entries accumulate into Permissions.Warnings.
	sources := make([]SourcePerms, 0, len(snapshot.claudeSettings))
	var enforcedSources []SourcePerms
	var warnings []ConfigWarning

	for _, loaded := range snapshot.claudeSettings {
		sources = append(sources, loaded.permissions.clone())
		warnings = append(warnings, loaded.warnings...)
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
		cwd:        snapshot.cwd,
		registry:   registry,
		ruleConfig: ruleConfig,
		Permissions: &Permissions{
			Sources:         sources,
			EnforcedSources: enforcedSources,
			Warnings:        warnings,
			rules:           registry,
			snippetRules:    snippetRules,
			PathDirs:        maps.Clone(snapshot.pathDirs),
			// Resolve does not know which harness consumes its
			// output. Harness-bound tools replace this with the
			// concrete harness, and the rest keep the Placeholder
			// so harness-specific strings are visibly marked rather
			// than pretending to be one harness.
			Harness: harness.Placeholder{},
		},
	}
}

// parsePathDirs splits the PATH value into a set of cleaned directory paths.
// Empty PATH or empty entries yield an empty set; the absolute-path basename
// match then falls through, requiring an explicit absolute-path pattern.
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

// validateExternalPresets rejects semantic mistakes that JSON decoding cannot
// catch. External presets carry organisation policy, so dropping a bad entry
// would weaken it. User config keeps warning-only behaviour, so a bad entry can
// be diagnosed without losing the hook.
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

// patternOverlapsNormalizedOwnedPrefix reports whether the pattern could reach
// an owned subcommand past the leading options Breakdown strips. Searching over
// runs of those options costs a branch per skip per element, which a pattern of
// option globs turns into a hang, so walk the pattern's own elements instead.
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

	// Everything after the command name, which both the stripped options
	// and the owned prefix align against.
	args := pat.Elements[1:]

	// An option running past the last element leaves the rest of the
	// command unwritten, which only a trailing wildcard can cover.
	overrunOverlaps := pat.Mode != MatchExact

	reached := make([]bool, len(args)+1)
	reached[0] = true

	for at := 0; at <= len(args); at++ {
		if !reached[at] {
			continue
		}

		// Position zero is the pattern as written, which the caller
		// already compared against the owned prefix.
		if at > 0 && ownedPrefixAlignsAt(pat, prefix, at) {
			return true
		}
		if at == len(args) {
			if overrunOverlaps {
				return true
			}

			continue
		}

		for _, skip := range skips {
			if !skipOptionMatches(skip, args[at]) {
				continue
			}

			// The option names this element, and its arguments take
			// the elements after it whatever they hold.
			next := at + 1 + skip.Arguments
			if next > len(args) {
				if overrunOverlaps {
					return true
				}

				continue
			}

			reached[next] = true
		}
	}

	return false
}

// skipOptionMatches reports whether a pattern element could name this stripped
// option. An option whose argument arrives attached is a glob comparison,
// because the pattern may spell that value however it likes.
func skipOptionMatches(
	skip model.PatternPrefixSkip, element string,
) bool {
	if skip.Prefix {
		return globLanguagesOverlap(
			element, skip.Option+"*")
	}

	return globMatch(element, skip.Option)
}

// ownedPrefixAlignsAt reports whether the owned subcommands sit in the pattern
// from the given element onwards.
func ownedPrefixAlignsAt(
	pat Pattern, prefix []string, at int,
) bool {
	args := pat.Elements[1:]
	owner := prefix[1:]
	remaining := len(args) - at

	shared := min(remaining, len(owner))
	for i := 0; i < shared; i++ {
		if !globMatch(args[at+i], owner[i]) {
			return false
		}
	}

	return remaining >= len(owner) ||
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

// globLanguagesOverlap reports whether two patterns using this package's single
// `*` wildcard can match the same string. Command ownership needs language
// intersection, not filepath.Base(pattern), because `*` can cross `/`.
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
			// Consuming a character leaves both stars in the same
			// state. Their epsilon moves above provide every route
			// that can make progress.
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

// resolveRuleConfig resolves per-rule config across the rule-config sources.
// Rules ship default-OFF in code, so a rule absent from every source stays
// disabled. Ordinary presets form the base, then global/project/local .agents
// overrides apply. Enforced Enabled:true entries apply last, locking those
// rules on. External validation rejects Enabled:false in enforced presets.
// Claude settings.json does not participate - rule config is an
// agent-permissions concept kept in the shared layers, which is what makes it
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

// loadClaudeSettings reads one Claude settings file for a policy snapshot.
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

	source, warnings, err := parseClaudeSettings(path, data)
	if err != nil {
		return nil, nil, err
	}

	return &source, warnings, nil
}

// parseClaudeSettings validates captured Claude Code settings and converts its
// Bash permission entries. Other tool wrappers are valid but do not contribute
// to command policy. Claude settings cannot express reasons or environment
// variable permissions.
func parseClaudeSettings(
	path string, data []byte,
) (SourcePerms, []ConfigWarning, error) {
	var settings map[string]any
	if err := configjson.Decode(data, &settings); err != nil {
		return SourcePerms{}, nil, fmt.Errorf(
			"invalid JSON in %s: %v", path, err)
	}
	if settings == nil {
		return SourcePerms{}, nil, fmt.Errorf(
			"invalid JSON in %s: root must be an object", path)
	}

	permissions, err := readClaudePermissions(settings)
	if err != nil {
		return SourcePerms{}, nil, fmt.Errorf(
			"invalid JSON in %s: %v", path, err)
	}

	allow, err := readClaudePermissionList(permissions, "allow")
	if err != nil {
		return SourcePerms{}, nil, fmt.Errorf(
			"invalid JSON in %s: %v", path, err)
	}

	softAsk, err := readClaudePermissionList(
		permissions, "softAsk")
	if err != nil {
		return SourcePerms{}, nil, fmt.Errorf(
			"invalid JSON in %s: %v", path, err)
	}

	ask, err := readClaudePermissionList(permissions, "ask")
	if err != nil {
		return SourcePerms{}, nil, fmt.Errorf(
			"invalid JSON in %s: %v", path, err)
	}

	deny, err := readClaudePermissionList(permissions, "deny")
	if err != nil {
		return SourcePerms{}, nil, fmt.Errorf(
			"invalid JSON in %s: %v", path, err)
	}

	src := SourcePerms{Name: path}
	var warnings []ConfigWarning
	src.Allow.Commands, warnings = appendParsedClaude(
		warnings, path,
		allow, src.Allow.Commands)
	src.SoftAsk.Commands, warnings = appendParsedClaude(
		warnings, path,
		softAsk, src.SoftAsk.Commands)
	src.Ask.Commands, warnings = appendParsedClaude(
		warnings, path,
		ask, src.Ask.Commands)
	src.Deny.Commands, warnings = appendParsedClaude(
		warnings, path,
		deny, src.Deny.Commands)
	return src, warnings, nil
}

func readClaudePermissions(
	settings map[string]any,
) (map[string]any, error) {
	value, exists := settings["permissions"]
	if !exists || value == nil {
		return nil, nil
	}

	permissions, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("permissions must be an object")
	}

	return permissions, nil
}

func readClaudePermissionList(
	permissions map[string]any, key string,
) ([]string, error) {
	value, exists := permissions[key]
	if !exists || value == nil {
		return nil, nil
	}

	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("permissions.%s must be an array", key)
	}

	entries := make([]string, len(items))
	for i, item := range items {
		if item == nil {
			continue
		}

		entry, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf(
				"permissions.%s[%d] must be a string", key, i)
		}

		entries[i] = entry
	}

	return entries, nil
}

// parseTier parses one tier's entries (commands and env vars) into typed
// patterns and records ConfigWarnings for rejected entries. Both maps may be
// nil; a nil map yields an empty axis. Commands and EnvVars are stored on
// TierEntries; the resolution layer walks each axis independently.
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

// appendCommandMap parses a pattern->reason map into Patterns with the reason
// attached. Order is unstable across runs (Go map iteration), so the result is
// sorted by Raw for deterministic output.
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

// appendEnvVarMap parses an env var pattern->reason map. Same ConfigWarning
// treatment as commands. Sorted by Raw.
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

// appendParsedClaude parses Claude Code Bash(...) entries. Malformed Bash()
// bodies (e.g. missing close paren) emit a warning; non-Bash wrappers (Read,
// Edit, WebFetch) are silently skipped - they target other tools.
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

// sortPatterns sorts in place by Raw for deterministic output. The reason text
// shown to the user (and asserted on by tests) is the first matching pattern's
// Raw - map iteration in the loader makes the order non-deterministic without
// this.
func sortPatterns(patterns []Pattern) {
	sort.Slice(patterns, func(i, j int) bool {
		return patterns[i].Raw < patterns[j].Raw
	})
}

// sortEnvVarPatterns sorts env-var patterns by Raw for the same reason
// sortPatterns does - map iteration in the loader makes order
// non-deterministic.
func sortEnvVarPatterns(patterns []EnvVarPattern) {
	sort.Slice(patterns, func(i, j int) bool {
		return patterns[i].Raw < patterns[j].Raw
	})
}
