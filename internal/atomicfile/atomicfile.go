// Package atomicfile provides a safe write-replace primitive
// for config files (~/.claude/settings.json and
// ~/.agents/permissions.json).
//
// The semantics are intentionally narrow: refuse to write
// through a symlink (so dotfile setups aren't silently
// disconnected from their real target), preserve the
// original file's mode when overwriting, and rename atomically
// so a crash mid-write can't leave a half-written file.
package atomicfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrSymlinkTarget is returned by Write when the target
// path is a symlink. Callers should print a hand-paste
// message rather than silently follow or replace it.
var ErrSymlinkTarget = errors.New(
	"target is a symbolic link")

// Write replaces path with data atomically. If path already
// exists, the new file inherits its mode. If path is a
// symlink, returns ErrSymlinkTarget without writing. If
// path doesn't exist, defaultMode is used.
//
// The temp file lives in the same directory as path so the
// rename stays within one filesystem (where rename is
// atomic on POSIX).
func Write(
	path string, data []byte, defaultMode fs.FileMode,
) error {
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf(
			"stat %s: %v", path, err)
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
			"create parent dir: %v", err)
	}

	tmp, err := os.CreateTemp(
		dir, ".atomic-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %v", err)
	}
	tmpPath := tmp.Name()

	// Best-effort cleanup; ignored if rename succeeds.
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %v", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp: %v", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %v", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf(
			"rename %s -> %s: %v", tmpPath, path, err)
	}
	return nil
}
