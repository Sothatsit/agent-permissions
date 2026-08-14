package perms

import (
	"testing"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// These tests pin the PATH-aware absolute-path matching asymmetry described in
// DESIGN.md: Allow/Ask/SoftAsk match an absolute-path command by its bare name
// only when the path's directory is on PATH (the shell would have resolved the
// bare name to that same binary), while Deny strips the basename
// unconditionally so an absolute path can never slip past a bare-name deny.
// They inject PathDirs directly, so unlike the bash integration tests they
// don't depend on the runner's real PATH.

func mustCmdPattern(t *testing.T, raw string) Pattern {
	t.Helper()
	p, err := parsePattern(raw)
	if err != nil {
		t.Fatalf("parsePattern(%q): %v", raw, err)
	}

	return p
}

// pathPerms builds a Permissions with one source holding the given Allow and
// Deny command patterns and the given PATH directories.
func pathPerms(
	t *testing.T, pathDirs, allow, deny []string,
) *Permissions {
	t.Helper()
	src := SourcePerms{Name: "test"}
	for _, raw := range allow {
		src.Allow.Commands = append(
			src.Allow.Commands, mustCmdPattern(t, raw))
	}

	for _, raw := range deny {
		src.Deny.Commands = append(
			src.Deny.Commands, mustCmdPattern(t, raw))
	}

	dirs := map[string]struct{}{}
	for _, d := range pathDirs {
		dirs[d] = struct{}{}
	}

	return &Permissions{
		Sources:  []SourcePerms{src},
		PathDirs: dirs,
	}
}

func pathDecide(
	p *Permissions, args ...string,
) model.Decision {
	cmd := model.Command{Args: word.FromStrings(args)}
	return p.checkOne(cmd).decision
}

func TestAllowMatchesAbsolutePathInPath(t *testing.T) {
	p := pathPerms(t,
		[]string{"/usr/bin"}, []string{"git:*"}, nil)
	if got := pathDecide(
		p, "/usr/bin/git", "status"); got != model.Allow {
		t.Errorf(
			"in-PATH abs path should Allow; got %v", got)
	}
}

func TestAllowDoesNotMatchOutOfPathAbsolute(t *testing.T) {
	p := pathPerms(t,
		[]string{"/usr/bin"}, []string{"git:*"}, nil)
	// /tmp/evil is not on PATH, so the bare-name Allow must not fire - the
	// agent typed a binary the shell wouldn't have resolved to the trusted
	// git.
	if got := pathDecide(
		p, "/tmp/evil/git", "status",
	); got != model.Undecided {
		t.Errorf(
			"out-of-PATH abs path should not Allow; got %v",
			got)
	}
}

func TestAllowMatchesBareName(t *testing.T) {
	p := pathPerms(t, nil, []string{"git:*"}, nil)
	if got := pathDecide(
		p, "git", "status"); got != model.Allow {
		t.Errorf("bare name should Allow; got %v", got)
	}
}

func TestDenyStripsBasenameRegardlessOfPath(t *testing.T) {
	p := pathPerms(t,
		[]string{"/usr/bin"}, nil, []string{"curl:*"})
	// Even though /tmp/evil is off PATH, Deny strips the basename so the
	// absolute path still hits curl:*.
	if got := pathDecide(
		p, "/tmp/evil/curl", "http://x",
	); got != model.Deny {
		t.Errorf(
			"out-of-PATH abs path should still Deny; got %v",
			got)
	}
}

func TestDenyBeatsAllowForAbsolutePath(t *testing.T) {
	// Same source, same command in both Allow and Deny: within-source
	// precedence makes Deny win, and it does so for an absolute path too.
	p := pathPerms(t,
		[]string{"/usr/bin"},
		[]string{"curl:*"}, []string{"curl:*"})
	if got := pathDecide(
		p, "/usr/bin/curl", "http://x"); got != model.Deny {
		t.Errorf("Deny should beat Allow; got %v", got)
	}
}
