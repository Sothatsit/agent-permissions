package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/atomicfile"
	"github.com/sothatsit/agent-permissions/internal/configjson"
	"github.com/sothatsit/agent-permissions/internal/word"
	"mvdan.cc/sh/v3/syntax"
)

// install wires the agent-permissions hook into known harness config files.
// Today it installs Claude Code's Bash PreToolUse hook in settings.json.
//
// This is the only command that modifies Claude Code's settings.json. It must
// preserve settings it does not own.
//
//   - It skips a missing settings file rather than creating one.
//   - It refuses symlinks and permission failures, then prints a stanza the
//     user can paste by hand.
//   - It refuses hook structures that do not match Claude Code's documented
//     shape rather than overwriting data it does not understand.
//   - It parses hook commands to distinguish this hook from wrapper scripts or
//     incidental mentions of the binary name.
//   - It merges into an existing Bash matcher's hooks array when one exists.
func install(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: agent-permissions install")
	}

	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve binary path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(
		binPath,
	); err == nil {
		binPath = resolved
	} else {
		// The unresolved path can become stale if a development symlink
		// moves.
		fmt.Fprintf(os.Stderr,
			"warning: could not resolve symlinks "+
				"on %s: %v\n", binPath, err)
	}

	return installClaudeCode(binPath)
}

func installClaudeCode(binPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home: %w", err)
	}

	path := filepath.Join(home, ".claude", "settings.json")
	// Use mvdan/sh's Bash quoting rules because the executable path may
	// contain spaces or shell metacharacters.
	quotedBinPath, err := syntax.Quote(binPath, syntax.LangBash)
	if err != nil {
		return fmt.Errorf("quote binary path for Bash hook: %w", err)
	}

	hookCmd := quotedBinPath + " claude-hook"
	stanza := buildBashMatcherEntry(hookCmd)

	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println(
				"Skipped Claude Code " +
					"(~/.claude/settings.json " +
					"not found)")
			return nil
		}

		return fmt.Errorf(
			"stat %s: %w", path, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	var root map[string]any
	if err := configjson.Decode(data, &root); err != nil {
		return fmt.Errorf(
			"invalid JSON in %s: %w", path, err)
	}
	if root == nil {
		return buildHandPasteError(
			path, stanza,
			errors.New("settings.json must contain a JSON object"))
	}

	updated, status, err := mergeHookStanza(
		root, hookCmd)
	if err != nil {
		return buildHandPasteError(path, stanza, err)
	}
	if status == hookAlreadyPresent {
		fmt.Println(
			"Claude Code: already installed " +
				"(no changes)")
		return nil
	}

	out, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return err
	}

	out = append(out, '\n')

	if err := atomicfile.Write(
		path, out, 0o644,
	); err != nil {
		if errors.Is(
			err, atomicfile.ErrSymlinkTarget,
		) {
			return buildHandPasteError(
				path, stanza, err)
		}
		// Permission errors and other writable issues also fall back to
		// the hand-paste path so the user has a clear next step.
		if errors.Is(err, os.ErrPermission) {
			return buildHandPasteError(
				path, stanza, err)
		}

		return err
	}

	fmt.Printf(
		"Installed for Claude Code (%s)\n", path)
	return nil
}

// mergeStatus describes whether merging changed settings.json.
type mergeStatus int

const (
	hookMerged         mergeStatus = iota // edited
	hookAlreadyPresent                    // no edit needed
)

// mergeHookStanza inserts the hook into Claude Code's documented PreToolUse
// structure. It rejects an unknown shape so the caller does not overwrite it.
func mergeHookStanza(
	root map[string]any, hookCmd string,
) (map[string]any, mergeStatus, error) {
	hooksAny, exists := root["hooks"]
	var hooks map[string]any
	if !exists || hooksAny == nil {
		hooks = map[string]any{}
	} else {
		m, ok := hooksAny.(map[string]any)
		if !ok {
			return nil, 0, fmt.Errorf(
				"settings.json `hooks` is not a JSON " +
					"object — refusing to overwrite")
		}

		hooks = m
	}

	preAny, preExists := hooks["PreToolUse"]
	var preToolUse []any
	if !preExists || preAny == nil {
		preToolUse = []any{}
	} else {
		arr, ok := preAny.([]any)
		if !ok {
			return nil, 0, fmt.Errorf(
				"settings.json `hooks.PreToolUse` is " +
					"not an array — refusing to overwrite")
		}

		preToolUse = arr
	}

	// Inspect every Bash matcher before choosing where to merge. Claude
	// Code accepts more than one matcher, and the hook may already be in a
	// later one.
	mergeIndex := -1
	for i, e := range preToolUse {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}

		matcher, _ := entry["matcher"].(string)
		if matcher != "Bash" {
			continue
		}
		if mergeIndex == -1 {
			mergeIndex = i
		}

		entryHooks, ok := entry["hooks"].([]any)
		if !ok {
			return nil, 0, fmt.Errorf(
				"PreToolUse entry %d has a `hooks` "+
					"field that is not an array — "+
					"refusing to overwrite", i)
		}

		for _, h := range entryHooks {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}

			cmd, _ := hm["command"].(string)
			if isAgentPermissionsHook(cmd, hookCmd) {
				return root, hookAlreadyPresent, nil
			}
		}
	}

	if mergeIndex >= 0 {
		entry := preToolUse[mergeIndex].(map[string]any)
		entryHooks := entry["hooks"].([]any)
		entryHooks = append(
			entryHooks, buildCommandHookEntry(hookCmd))
		entry["hooks"] = entryHooks
		preToolUse[mergeIndex] = entry
		hooks["PreToolUse"] = preToolUse
		root["hooks"] = hooks
		return root, hookMerged, nil
	}

	preToolUse = append(preToolUse, buildBashMatcherEntry(hookCmd))
	hooks["PreToolUse"] = preToolUse
	root["hooks"] = hooks
	return root, hookMerged, nil
}

func buildCommandHookEntry(hookCmd string) map[string]any {
	return map[string]any{
		"type":    "command",
		"command": hookCmd,
	}
}

func buildBashMatcherEntry(hookCmd string) map[string]any {
	return map[string]any{
		"matcher": "Bash",
		"hooks": []any{
			buildCommandHookEntry(hookCmd),
		},
	}
}

func isAgentPermissionsHook(cmd, installedCmd string) bool {
	if cmd == installedCmd {
		return true
	}

	file, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	if err != nil {
		return false
	}
	if len(file.Stmts) != 1 {
		return false
	}

	stmt := file.Stmts[0]
	if stmt.Negated || stmt.Background || stmt.Coprocess || stmt.Disown {
		return false
	}
	if stmt.Semicolon.IsValid() || len(stmt.Redirs) != 0 {
		return false
	}

	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok {
		return false
	}
	if len(call.Assigns) != 0 || len(call.Args) != 2 {
		return false
	}
	if !word.Static(call.Args[0]) || !word.Static(call.Args[1]) {
		return false
	}

	return filepath.Base(word.Text(call.Args[0])) == "agent-permissions" &&
		word.Text(call.Args[1]) == "claude-hook"
}

// buildHandPasteError adds the manual stanza to a failure the user can fix.
func buildHandPasteError(
	path string, stanza map[string]any, cause error,
) error {
	pretty, err := json.MarshalIndent(
		map[string]any{
			"hooks": map[string]any{
				"PreToolUse": []any{stanza},
			},
		}, "", "  ")
	if err != nil {
		return fmt.Errorf(
			"format manual hook stanza after %v: %w", cause, err)
	}

	return fmt.Errorf(
		"cannot write %s: %w\n\n"+
			"To install by hand, merge the "+
			"following into %s:\n\n%s",
		path, cause, path, string(pretty))
}
