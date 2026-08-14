package model

import (
	"path/filepath"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// SetWorkingDirectory resolves a directory word against the current Cwd. It
// clears Cwd when shell expansion prevents a reliable resolution. Callers
// decide whether the change persists or is scoped to a wrapped command.
func (s *State) SetWorkingDirectory(target *syntax.Word) {
	if !word.Static(target) {
		s.Cwd = ""
		return
	}

	directory := word.Text(target)
	// The shell expands an unquoted tilde before cd or env runs. The Word
	// helpers cannot distinguish that expansion from a quoted literal
	// tilde.
	if strings.HasPrefix(directory, "~") {
		s.Cwd = ""
		return
	}
	if filepath.IsAbs(directory) {
		s.Cwd = filepath.Clean(directory)
		return
	}
	if s.Cwd == "" {
		return
	}

	s.Cwd = filepath.Clean(filepath.Join(s.Cwd, directory))
}
