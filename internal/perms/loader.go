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
	"github.com/sothatsit/agent-permissions/presets"
)

// Resolved is one harness's full resolution: the
// Permissions to evaluate against (its ordered Sources,
// highest priority first) plus the resolved per-rule config
// the breakdown consults. RuleConfig is harness-agnostic —
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
// Priority, highest → lowest (the order Sources lands
// in):
//  1. <cwd>/.claude/settings.local.json
//  2. <cwd>/.claude/settings.json
//  3. <configDir>/settings.json (Claude Code user)
//  4. <cwd>/.agents/permissions.local.json (explicit)
//  5. <cwd>/.agents/permissions.json (explicit entries)
//  6. ~/.agents/permissions.json (explicit entries)
//  7. Embedded presets, filtered by enabled-/disabled-
//     presets from the most-specific .agents config
//     that specifies either field (local beats project
//     beats global)
//
// permissions.local.json mirrors Claude Code's
// settings.local.json: a project-scoped, typically
// gitignored personal override that sits above the
// committed project config. There is no global local
// variant, again matching Claude Code.
func Resolve(
	configDir, cwd string,
) (*Resolved, error) {
	home, err := homeDir()
	if err != nil {
		// Fail closed: an unresolvable HOME means we
		// can't load ~/.agents/permissions.json or
		// (when configDir is empty) ~/.claude/settings.json.
		// Bubble the error so the hook returns deny
		// rather than silently running a different
		// policy than the user expects.
		return nil, fmt.Errorf(
			"resolve home directory: %v", err)
	}

	homeAgentPath := filepath.Join(
		home, ".agents", "permissions.json")
	globalAgent, err := agentconfig.Load(homeAgentPath)
	if err != nil {
		return nil, fmt.Errorf(
			"global agent config: %v", err)
	}

	var projectAgent *agentconfig.Config
	if cwd != "" {
		projectAgentPath := filepath.Join(
			cwd, ".agents", "permissions.json")
		if projectAgentPath == homeAgentPath {
			// cwd is the home directory, so the project
			// and global agent configs are the same file.
			// Keep it once as the higher-precedence
			// project source: otherwise the file's entries
			// load twice — harmless to decisions (identical
			// patterns) but double-counted by `validate`.
			projectAgent = globalAgent
			globalAgent = nil
		} else {
			projectAgent, err = agentconfig.Load(
				projectAgentPath)
			if err != nil {
				return nil, fmt.Errorf(
					"project agent config: %v", err)
			}
		}
	}

	// The project-local override is project-scoped only, so
	// it loads from cwd just like the committed project
	// config but with no global counterpart.
	var localAgent *agentconfig.Config
	if cwd != "" {
		localAgentPath := filepath.Join(
			cwd, ".agents", "permissions.local.json")
		localAgent, err = agentconfig.Load(localAgentPath)
		if err != nil {
			return nil, fmt.Errorf(
				"local agent config: %v", err)
		}
	}

	selected := SelectPresets(
		globalAgent, projectAgent, localAgent)

	// Build sources highest → lowest priority. Each
	// source-load may return ConfigWarnings for malformed
	// entries; they accumulate into Permissions.Warnings.
	var sources []SourcePerms
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

	if cwd != "" {
		if err := addClaude(filepath.Join(
			cwd, ".claude", "settings.local.json"),
			"local settings"); err != nil {
			return nil, err
		}
		if err := addClaude(filepath.Join(
			cwd, ".claude", "settings.json"),
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

	if localAgent != nil {
		src, w := fromAgentConfig(
			localAgent.Path, localAgent)
		sources = append(sources, src)
		warnings = append(warnings, w...)
	}
	if projectAgent != nil {
		src, w := fromAgentConfig(
			projectAgent.Path, projectAgent)
		sources = append(sources, src)
		warnings = append(warnings, w...)
	}
	if globalAgent != nil {
		src, w := fromAgentConfig(
			"~/.agents/permissions.json", globalAgent)
		sources = append(sources, src)
		warnings = append(warnings, w...)
	}

	for _, p := range selected {
		src, w := fromPreset(p)
		sources = append(sources, src)
		warnings = append(warnings, w...)
	}

	return &Resolved{
		RuleConfig: resolveRuleConfig(
			projectAgent, globalAgent, localAgent,
			selected),
		Permissions: &Permissions{
			Sources:  sources,
			Warnings: warnings,
			PathDirs: parsePathDirs(os.Getenv("PATH")),
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

// SelectPresets returns the embedded presets selected by
// the most-specific agent config that specifies preset
// selection — local, else project, else global — otherwise
// all presets. `enabled-presets` narrows to a whitelist;
// `disabled-presets` then filters out names from whatever
// remains.
func SelectPresets(
	global, project, local *agentconfig.Config,
) []*presets.Preset {
	all := presets.MustEmbedded()

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

	out := all
	if cfg.EnabledPresets != nil {
		out = filterByName(out, *cfg.EnabledPresets, true)
	}
	if cfg.DisabledPresets != nil {
		out = filterByName(
			out, *cfg.DisabledPresets, false)
	}
	return out
}

// resolveRuleConfig resolves per-rule config across the
// rule-config sources. Rules ship default-OFF in code, so a
// rule absent from every source stays disabled; presets are
// the enable base and .agents overrides win. Sources are
// applied lowest priority first so later writes win: presets,
// then global .agents, then project .agents, then the
// project-local .agents override. No two presets own the
// same rule, so the preset layer is an unambiguous union.
// Claude settings.json does not participate — rule config is
// an agent-permissions concept kept in the shared layers,
// which is what makes it identical across harnesses.
func resolveRuleConfig(
	project, global, local *agentconfig.Config,
	selected []*presets.Preset,
) model.RuleConfigs {
	out := model.RuleConfigs{}
	for _, p := range selected {
		for id, cfg := range p.Rules {
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

// homeDir is overridable in tests.
var homeDir = func() (string, error) {
	return os.UserHomeDir()
}
