// Package atomicfile provides a safe write-replace primitive for config files
// (~/.claude/settings.json, ~/.agents/permissions.json).
package atomicfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrSymlinkTarget is returned by Write when the target path is a symlink.
// Callers print a hand-paste message rather than follow or replace it.
var ErrSymlinkTarget = errors.New(
	"target is a symbolic link")

// Write replaces path with data atomically, inheriting the mode of an existing
// file and using defaultMode otherwise. A symlink target returns
// ErrSymlinkTarget without writing. The temp file shares path's directory, so
// the rename stays inside one filesystem, where POSIX makes it atomic.
func Write(
	path string, data []byte, defaultMode fs.FileMode,
) error {
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf(
			"stat %s: %w", path, err)
	}

	mode := defaultMode
	if info != nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrSymlinkTarget
		}

		mode = info.Mode().Perm()
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf(
			"create parent dir: %w", err)
	}

	tmp, err := os.CreateTemp(
		dir, ".atomic-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}

	tmpPath := tmp.Name()

	// Best-effort cleanup; ignored if rename succeeds.
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf(
			"rename %s -> %s: %w", tmpPath, path, err)
	}

	return nil
}
