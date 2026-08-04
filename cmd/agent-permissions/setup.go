package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sothatsit/agent-permissions/internal/atomicfile"
	"github.com/sothatsit/agent-permissions/presets"
)

// setup writes a populated ~/.agents/permissions.json so
// the user has a starting point to customise. The file
// includes empty tier arrays as placeholders and leaves
// preset selection unspecified (which means "all presets
// enabled" — new presets in future binary updates are
// picked up automatically).
//
// Refuses to overwrite an existing file unless --force is
// passed. Any non-NotExist stat error is treated as a
// hard failure rather than silently writing — a transient
// filesystem error on the parent directory could otherwise
// clobber a real, customised file.
func setup(args []string) error {
	force := false
	for _, a := range args {
		switch a {
		case "--force", "-f":
			force = true
		default:
			return fmt.Errorf(
				"usage: agent-permissions setup [--force]")
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home: %v", err)
	}
	path := filepath.Join(
		home, ".agents", "permissions.json")

	if _, err := os.Lstat(path); err == nil {
		if !force {
			return fmt.Errorf(
				"%s already exists (use --force to "+
					"overwrite)", path)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		// Any error that isn't "file not found" means we
		// can't safely decide whether overwriting is OK.
		// Refuse rather than risk clobbering a real file
		// behind a transient stat failure.
		return fmt.Errorf(
			"stat %s: %v", path, err)
	}

	body := setupTemplate()
	if err := atomicfile.Write(
		path, body, 0o644,
	); err != nil {
		return fmt.Errorf("write: %v", err)
	}

	fmt.Printf("Wrote %s\n", path)
	fmt.Printf(
		"Embedded presets active: %d\n",
		len(presets.MustEmbedded()))
	ext, err := presets.External()
	if err != nil {
		return fmt.Errorf("external presets: %v", err)
	}
	if len(ext) > 0 {
		fmt.Printf(
			"External presets active: %d (from %s)\n",
			len(ext), presets.PresetDirsEnv)
	}
	fmt.Println(
		"To narrow the active set, add `enabled-presets` " +
			"or `disabled-presets` arrays.")
	return nil
}

// setupTemplate returns the initial JSON body with empty
// tier objects (each holding empty Commands and EnvVars
// maps) as placeholders for hand-editing. Key order is
// alphabetised by encoding/json — fine for a starter file
// users will hand-edit anyway.
func setupTemplate() []byte {
	body := map[string]any{
		"Allow":   emptyTier(),
		"SoftAsk": emptyTier(),
		"Ask":     emptyTier(),
		"Deny":    emptyTier(),
	}
	out, _ := json.MarshalIndent(body, "", "  ")
	out = append(out, '\n')
	return out
}

// emptyTier returns a new empty tier object matching the
// loader's expected schema. Returned fresh each call so
// JSON marshalling doesn't share state across tier keys.
func emptyTier() map[string]any {
	return map[string]any{
		"Commands": map[string]string{},
		"EnvVars":  map[string]string{},
	}
}
