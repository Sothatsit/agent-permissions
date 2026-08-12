package model

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// MaxScriptSizeBytes is the maximum size of a script file that
// will be read for scanning (1 MB). Applies to all
// languages (bash, Python, etc.).
const MaxScriptSizeBytes = 1024 * 1024

// ReadScript reads a script file relative to cwd,
// enforcing the size limit. Returns the file contents.
func ReadScript(
	path string, cwd string,
) ([]byte, error) {
	fullPath := path
	if !filepath.IsAbs(path) && cwd != "" {
		fullPath = filepath.Join(cwd, path)
	}

	file, err := os.OpenFile(
		fullPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if info.Size() > MaxScriptSizeBytes {
		return nil, fmt.Errorf(
			"file too large (%d bytes, limit %d)",
			info.Size(), MaxScriptSizeBytes)
	}

	data, err := io.ReadAll(io.LimitReader(
		file, MaxScriptSizeBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxScriptSizeBytes {
		return nil, fmt.Errorf(
			"file too large (limit %d bytes)",
			MaxScriptSizeBytes)
	}

	return data, nil
}
