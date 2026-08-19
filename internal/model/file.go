package model

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const MaxScriptSizeBytes = 1024 * 1024

// ReadScript reads a script file relative to cwd, up to the size limit.
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
