package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sothatsit/agent-permissions/internal/atomicfile"
	"github.com/sothatsit/agent-permissions/internal/perms"
	"github.com/sothatsit/agent-permissions/presets"
)

// setup writes a populated ~/.agents/permissions.json so
// the user has a starting point to customise. The file
// includes empty tier arrays as placeholders and leaves
// preset selection unspecified (which means "all ordinary
// presets enabled" - new presets in future binary updates
// are picked up automatically). Enforced presets sit outside
// user selection and remain active.
//
// Refuses to overwrite an existing file unless --force is
// passed. Any non-NotExist stat error is treated as a
// hard failure rather than silently writing - a transient
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

	catalog, err := perms.LoadPresetCatalog()
	if err != nil {
		return err
	}
	all := catalog.Presets()

	body, err := buildSetupTemplate()
	if err != nil {
		return fmt.Errorf("build starter config: %w", err)
	}
	if err := atomicfile.Write(
		path, body, 0o644,
	); err != nil {
		return fmt.Errorf("write: %v", err)
	}

	fmt.Printf("Wrote %s\n", path)
	embeddedCount := 0
	externalCount := 0
	enforcedCount := 0
	for _, p := range all {
		switch {
		case p.Enforced:
			enforcedCount++
		case p.Dir != "":
			externalCount++
		default:
			embeddedCount++
		}
	}
	fmt.Printf(
		"Embedded presets active: %d\n",
		embeddedCount)
	if externalCount > 0 {
		fmt.Printf(
			"External presets active: %d (from %s)\n",
			externalCount, presets.PresetDirsEnv)
	}
	if enforcedCount > 0 {
		fmt.Printf(
			"Enforced presets active: %d (from %s)\n",
			enforcedCount, presets.EnforcedPresetDirsEnv)
	}
	fmt.Println(
		"To narrow the ordinary preset set, add " +
			"`enabled-presets` " +
			"or `disabled-presets` arrays.")
	if enforcedCount > 0 {
		fmt.Println(
			"Enforced presets stay active regardless of " +
				"preset selection.")
	}
	return nil
}

// buildSetupTemplate returns the initial JSON body with empty
// tier objects (each holding empty Commands and EnvVars
// maps) as placeholders for hand-editing. encoding/json sorts
// the keys, which gives the starter file stable output.
func buildSetupTemplate() ([]byte, error) {
	body := map[string]any{
		"Allow":   buildEmptyTier(),
		"SoftAsk": buildEmptyTier(),
		"Ask":     buildEmptyTier(),
		"Deny":    buildEmptyTier(),
	}
	out, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return nil, err
	}
	out = append(out, '\n')
	return out, nil
}

// buildEmptyTier returns an empty tier in the loader's schema.
func buildEmptyTier() map[string]any {
	return map[string]any{
		"Commands": map[string]string{},
		"EnvVars":  map[string]string{},
	}
}
