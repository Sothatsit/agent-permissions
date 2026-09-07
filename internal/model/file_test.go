package model

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadScriptSizeLimit(t *testing.T) {
	tests := []struct {
		name      string
		sizeBytes int
		wantError bool
	}{
		{name: "at limit", sizeBytes: MaxScriptSizeBytes},
		{
			name:      "over limit",
			sizeBytes: MaxScriptSizeBytes + 1,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "script")
			data := bytes.Repeat([]byte{'x'}, tt.sizeBytes)
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}

			got, err := ReadScript(path, "")
			if tt.wantError {
				if err == nil {
					t.Fatal("expected size-limit error")
				}

				return
			}
			if err != nil {
				t.Fatalf("ReadScript: %v", err)
			}

			if !bytes.Equal(got, data) {
				t.Error("ReadScript returned different contents")
			}
		})
	}
}

func TestReadScriptRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := ReadScript(path, "")
		done <- err
	}()

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(
			err.Error(), "not a regular file") {
			t.Fatalf("got error %v, want non-regular-file error", err)
		}
	case <-deadline.C:
		// Release a reader blocked in open so a failing test does
		// not leave a goroutine behind.
		writer, err := os.OpenFile(
			path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			writer.Close()
			<-done
		}

		t.Fatal("ReadScript blocked while opening a FIFO")
	}
}

// A missing script gets the ordering explained, because the write that
// would have created it has not run yet. Anything else keeps the plain
// wording, which the callers prefix with the path as spelled.
func TestScriptReadReasonExplainsAMissingScript(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "later.py")

	_, err := ReadScript(missing, "")
	if err == nil {
		t.Fatal("expected a read error")
	}

	got := ScriptReadReason("later.py", err)
	if !strings.Contains(got, "in an earlier command") {
		t.Errorf("missing script not explained: %q", got)
	}
	if !strings.Contains(got, missing) {
		t.Errorf("reason lost the resolved path: %q", got)
	}

	// A directory exists, so the reason stays the plain one and keeps the
	// path the caller spelled.
	_, err = ReadScript(dir, "")
	if err == nil {
		t.Fatal("expected a read error")
	}

	got = ScriptReadReason("thedir", err)
	if strings.Contains(got, "in an earlier command") {
		t.Errorf("explained a file that exists: %q", got)
	}
	if !strings.HasPrefix(got, "thedir: ") {
		t.Errorf("reason dropped the spelled path: %q", got)
	}
}
