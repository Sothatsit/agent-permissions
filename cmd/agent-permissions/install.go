package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/sothatsit/agent-permissions/internal/atomicfile"
)

// install wires the agent-permissions hook into known
// harness config files. Today: ~/.claude/settings.json
// (Claude Code's PreToolUse on Bash). Per-harness future
// extension lives here too.
//
// This is the only command that modifies Claude Code's
// settings.json. It is conservative and safety-first:
//
//   - Skips when the file doesn't exist (does not create
//     it).
//   - Refuses to write through a symbolic link and prints
//     the stanza for the user to paste by hand. Same for
//     read-only or otherwise unwritable targets.
//   - Errors if the existing hooks structure isn't the
//     shape Claude Code documents (PreToolUse must be an
//     array of matcher entries). Refusing to write is
//     safer than silently overwriting unrecognised data.
//   - Detects an existing agent-permissions stanza via a
//     word-boundary path match so wrapper scripts or
//     forks that happen to mention the binary's name in
//     a different context don't cause false positives.
//   - Merges into an existing Bash matcher's hooks array
//     when one exists (the canonical Claude Code
//     structure) rather than creating a second top-level
//     matcher entry.
func install(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: agent-permissions install")
	}

	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve binary path: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(
		binPath,
	); err == nil {
		binPath = resolved
	} else {
		// Fall back to the unresolved path but warn —
		// silently baking a dev-time symlink into
		// settings.json is exactly the kind of stale
		// reference we want to avoid.
		fmt.Fprintf(os.Stderr,
			"warning: could not resolve symlinks "+
				"on %s: %v\n", binPath, err)
	}

	return installClaudeCode(binPath)
}

func installClaudeCode(binPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home: %v", err)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	hookCmd := binPath + " claude-hook"
	stanza := bashMatcherEntry(hookCmd)

	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println(
				"Skipped Claude Code " +
					"(~/.claude/settings.json " +
					"not found)")
			return nil
		}
		return fmt.Errorf(
			"stat %s: %v", path, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %v", path, err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf(
			"invalid JSON in %s: %v", path, err)
	}
	if root == nil {
		root = map[string]any{}
	}

	updated, status, err := mergeHookStanza(
		root, hookCmd)
	if err != nil {
		return handPasteError(path, stanza, err)
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
			return handPasteError(
				path, stanza, err)
		}
		// Permission errors and other writable issues
		// also fall back to the hand-paste path so the
		// user has a clear next step.
		if errors.Is(err, os.ErrPermission) {
			return handPasteError(
				path, stanza, err)
		}
		return err
	}

	fmt.Printf(
		"Installed for Claude Code (%s)\n", path)
	return nil
}

// mergeStatus is the result of attempting to merge our
// hook into an existing settings.json structure.
type mergeStatus int

const (
	hookMerged         mergeStatus = iota // edited
	hookAlreadyPresent                    // no edit needed
)

// mergeHookStanza inserts the agent-permissions hook into
// root["hooks"]["PreToolUse"], following Claude Code's
// documented structure. Returns an error if the existing
// structure is the wrong shape (e.g. PreToolUse is not an
// array) so the caller can refuse to overwrite.
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

	hookEntry := map[string]any{
		"type":    "command",
		"command": hookCmd,
	}

	// Look for a Bash matcher entry already in
	// PreToolUse and either confirm we're already
	// installed there or merge our command into its
	// hooks array.
	for i, e := range preToolUse {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		matcher, _ := entry["matcher"].(string)
		if matcher != "Bash" {
			continue
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
			if isAgentPermissionsHook(cmd) {
				return root, hookAlreadyPresent, nil
			}
		}
		entryHooks = append(entryHooks, hookEntry)
		entry["hooks"] = entryHooks
		preToolUse[i] = entry
		hooks["PreToolUse"] = preToolUse
		root["hooks"] = hooks
		return root, hookMerged, nil
	}

	// No Bash matcher entry — append a new one.
	preToolUse = append(preToolUse, bashMatcherEntry(hookCmd))
	hooks["PreToolUse"] = preToolUse
	root["hooks"] = hooks
	return root, hookMerged, nil
}

func bashMatcherEntry(hookCmd string) map[string]any {
	return map[string]any{
		"matcher": "Bash",
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": hookCmd,
			},
		},
	}
}

// hookCmdPattern matches a command string that ends with
// "agent-permissions claude-hook" preceded by a path
// separator or word boundary. This keeps incidental
// substring matches (wrapper scripts, comments, log
// strings, alternate-fork commands) from being treated as
// our own hook.
var hookCmdPattern = regexp.MustCompile(
	`(^|/)agent-permissions claude-hook$`)

func isAgentPermissionsHook(cmd string) bool {
	return hookCmdPattern.MatchString(cmd)
}

// handPasteError wraps a write failure with the JSON
// stanza so the user can install manually. Used for
// symlinks, read-only files, and shape mismatches — any
// case where we can't safely auto-edit settings.json.
func handPasteError(
	path string, stanza map[string]any, cause error,
) error {
	pretty, _ := json.MarshalIndent(
		map[string]any{
			"hooks": map[string]any{
				"PreToolUse": []any{stanza},
			},
		}, "", "  ")
	return fmt.Errorf(
		"cannot write %s: %v\n\n"+
			"To install by hand, merge the "+
			"following into %s:\n\n%s",
		path, cause, path, string(pretty))
}
