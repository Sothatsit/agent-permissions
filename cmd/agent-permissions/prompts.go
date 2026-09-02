package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// promptsOffMarker is the file whose presence turns permission prompts off
// for one Claude Code session. It lives under the temp directory because a
// session runs on one host, and the marker should die with the host's temp
// space rather than outlive the session in a config directory.
func promptsOffMarker(sessionID string) (string, error) {
	if sessionID == "" ||
		sessionID == "." || sessionID == ".." ||
		strings.ContainsAny(sessionID, `/\`) {
		return "", fmt.Errorf(
			"invalid session id %q", sessionID)
	}

	dir := filepath.Join(os.TempDir(),
		fmt.Sprintf("agent-permissions-%d", os.Getuid()),
		"prompts-off")
	return filepath.Join(dir, sessionID), nil
}

// promptsOff reports whether prompts are switched off for the session. A
// session the hook cannot identify keeps its prompts.
func promptsOff(sessionID string) bool {
	marker, err := promptsOffMarker(sessionID)
	if err != nil {
		return false
	}

	_, err = os.Stat(marker)
	return err == nil
}

// prompts turns permission prompts off or on for the current Claude Code
// session, or reports which it is. With prompts off the hook denies where it
// would have asked, so a session never stalls on a prompt.
func prompts(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf(
			"usage: agent-permissions prompts off|on|status")
	}

	sessionID := os.Getenv("CLAUDE_CODE_SESSION_ID")
	if sessionID == "" {
		return fmt.Errorf("CLAUDE_CODE_SESSION_ID is not set. " +
			"Run this from a Bash command inside the Claude " +
			"Code session whose prompts you want to change")
	}

	marker, err := promptsOffMarker(sessionID)
	if err != nil {
		return err
	}

	switch args[0] {
	case "off":
		if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
			return fmt.Errorf("create marker directory: %w", err)
		}

		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			return fmt.Errorf("write marker: %w", err)
		}

		fmt.Println("Permission prompts are off for this session. " +
			"Commands that would have asked are denied instead. " +
			"Run `agent-permissions prompts on` to restore them.")
	case "on":
		err := os.Remove(marker)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove marker: %w", err)
		}

		fmt.Println("Permission prompts are on for this session.")
	case "status":
		if promptsOff(sessionID) {
			fmt.Println("off")
		} else {
			fmt.Println("on")
		}
	default:
		return fmt.Errorf(
			"usage: agent-permissions prompts off|on|status")
	}

	return nil
}
