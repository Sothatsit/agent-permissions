package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCreatesFileWithDefaultMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.json")
	if err := Write(
		path, []byte(`{"k":1}`), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf(
			"expected mode 0o600, got %o",
			info.Mode().Perm())
	}
}

func TestWritePreservesExistingMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.json")
	if err := os.WriteFile(
		path, []byte(`{}`), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	// The existing mode must win over the default mode.
	if err := Write(
		path, []byte(`{"k":2}`), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf(
			"expected preserved 0o600, got %o",
			info.Mode().Perm())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"k":2}` {
		t.Errorf("contents not updated")
	}
}

func TestWriteRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	link := filepath.Join(dir, "link.json")
	if err := os.WriteFile(
		real, []byte(`{}`), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	err := Write(link, []byte(`new`), 0o644)
	if !errors.Is(err, ErrSymlinkTarget) {
		t.Errorf(
			"expected ErrSymlinkTarget, got %v", err)
	}
	contents, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != `{}` {
		t.Errorf(
			"real file was modified: %s", contents)
	}
}

func TestWriteCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "deeper", "file.json")
	if err := Write(
		path, []byte(`{}`), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestWriteDoesNotLeaveTempOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	if err := Write(
		path, []byte(`{}`), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "f.json" {
			t.Errorf(
				"unexpected leftover file: %s", e.Name())
		}
	}
}

// Sanity: errors are returned, not panics, when we can't
// write to the parent.
func TestWriteUnwritableParent(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses chmod restrictions")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "ro")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(sub, 0o755)
	err := Write(
		filepath.Join(sub, "f.json"),
		[]byte(`{}`), 0o644)
	if err == nil {
		t.Fatal("expected error for read-only parent")
	}

	var pErr *fs.PathError
	if !errors.As(err, &pErr) {
		t.Errorf("expected filesystem cause, got %v", err)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("expected permission error, got %v", err)
	}
}
