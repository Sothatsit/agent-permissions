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
