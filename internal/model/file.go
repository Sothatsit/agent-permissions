package model

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
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

// ScriptReadReason explains why the hook could not read a script. A missing
// file earns its own wording because the hook reads a script before the
// command runs, so a script the same command writes is not there yet. The
// bare "no such file or directory" reads as though the write failed, and an
// agent that believes that re-checks the file instead of splitting the
// command in two. The error carries the path it resolved, which is what
// tells the reader a relative path was taken from somewhere unexpected.
func ScriptReadReason(path string, err error) string {
	var pathErr *fs.PathError
	if errors.Is(err, fs.ErrNotExist) &&
		errors.As(err, &pathErr) {
		return fmt.Sprintf(
			"%s does not exist. The hook scans a script before "+
				"the command runs, so a script the same "+
				"command creates is not there yet. Write it "+
				"in an earlier command, or check the path",
			pathErr.Path)
	}

	return fmt.Sprintf("%s: %v", path, err)
}
