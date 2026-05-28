package model

import (
	"fmt"
	"os"
	"path/filepath"
)

// MaxScriptSize is the maximum size of a script file that
// will be read for scanning (1 MB). Applies to all
// languages (bash, Python, etc.).
const MaxScriptSize = 1024 * 1024

// ReadScript reads a script file relative to cwd,
// enforcing the size limit. Returns the file contents.
// Stats the file before reading to reject oversized files
// without allocating memory.
func ReadScript(
	path string, cwd string,
) ([]byte, error) {
	fullPath := path
	if !filepath.IsAbs(path) && cwd != "" {
		fullPath = filepath.Join(cwd, path)
	}
	fi, err := os.Stat(fullPath)
	if err != nil {
		return nil, err
	}
	if fi.Size() > MaxScriptSize {
		return nil, fmt.Errorf(
			"file too large (%d bytes, limit %d)",
			fi.Size(), MaxScriptSize)
	}
	return os.ReadFile(fullPath)
}
