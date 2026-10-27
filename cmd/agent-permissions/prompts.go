package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/agentconfig"
	"github.com/sothatsit/agent-permissions/internal/atomicfile"
)

// promptsMode is whether a Claude Code session's permission prompts are on.
type promptsMode string

const (
	promptsOn  promptsMode = "on"
	promptsOff promptsMode = "off"
	// promptsOnce lets the next ask through, then turns prompts off.
	promptsOnce promptsMode = "once"
)

// promptsModeFile is the file holding the mode one Claude Code session was
// switched to. It lives under ~/.agents, not the temp directory, because
// Claude Code sets TMPDIR for its Bash commands but not for its statusline,
// so the two would look in different places. The files outlive their
// sessions, which is harmless since session ids never repeat.
func promptsModeFile(sessionID string) (string, error) {
	if sessionID == "" ||
		sessionID == "." || sessionID == ".." ||
		strings.ContainsAny(sessionID, `/\`) {
		return "", fmt.Errorf(
			"invalid session id %q", sessionID)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf(
			"cannot determine home directory: %w", err)
	}

	dir := filepath.Join(home, ".agents", "state", "prompts")
	return filepath.Join(dir, sessionID), nil
}

// defaultPromptsMode is the mode a session starts in, which the user sets
// with prompts in ~/.agents/permissions.json.
func defaultPromptsMode() (promptsMode, error) {
	path, err := agentconfig.GlobalPath()
	if err != nil {
		return "", err
	}

	config, err := agentconfig.Load(path)
	if err != nil {
		return "", err
	}

	if config != nil && config.Prompts == "off" {
		return promptsOff, nil
	}
	return promptsOn, nil
}

// sessionPromptsMode is the mode the session was switched to, or the
// default when it was never switched. A session the hook cannot identify
// takes the default.
func sessionPromptsMode(sessionID string) (promptsMode, error) {
	path, err := promptsModeFile(sessionID)
	if err != nil {
		return defaultPromptsMode()
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return defaultPromptsMode()
	}
	if err != nil {
		return "", fmt.Errorf("read prompts mode: %w", err)
	}

	mode := promptsMode(strings.TrimSpace(string(data)))
	switch mode {
	case promptsOn, promptsOff, promptsOnce:
		return mode, nil
	}
	return "", fmt.Errorf(
		"%s holds %q, not on, off or once", path, data)
}

// switchPrompts switches the session's prompts to mode.
func switchPrompts(sessionID string, mode promptsMode) error {
	path, err := promptsModeFile(sessionID)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create prompts mode directory: %w", err)
	}

	err = atomicfile.Write(path, []byte(string(mode)+"\n"), 0o600)
	if err != nil {
		return fmt.Errorf("write prompts mode: %w", err)
	}
	return nil
}

// prompts switches permission prompts off, on, or on for one ask, for the
// current Claude Code session, or reports which it is. With prompts off the
// hook denies where it would have asked, so a session never stalls on a
// prompt.
func prompts(args []string) error {
	const usage = "usage: agent-permissions prompts off|on|once|status"
	if len(args) != 1 {
		return errors.New(usage)
	}

	sessionID := os.Getenv("CLAUDE_CODE_SESSION_ID")
	if sessionID == "" {
		return fmt.Errorf("CLAUDE_CODE_SESSION_ID is not set. " +
			"Run this from a Bash command inside the Claude " +
			"Code session whose prompts you want to change")
	}

	switch args[0] {
	case "off":
		if err := switchPrompts(sessionID, promptsOff); err != nil {
			return err
		}

		fmt.Println("Permission prompts are off for this session. " +
			"Commands that would have asked are denied instead. " +
			"Run `agent-permissions prompts on` to restore them.")
	case "on":
		if err := switchPrompts(sessionID, promptsOn); err != nil {
			return err
		}

		fmt.Println("Permission prompts are on for this session.")
	case "once":
		if err := switchPrompts(sessionID, promptsOnce); err != nil {
			return err
		}

		fmt.Println("The next command that would ask the user asks, " +
			"then permission prompts turn off again.")
	case "status":
		mode, err := sessionPromptsMode(sessionID)
		if err != nil {
			return err
		}

		fmt.Println(mode)
	default:
		return errors.New(usage)
	}

	return nil
}
