package breakdown

import (
	"testing"

	"github.com/sothatsit/agent-permissions/internal/model"
)

// The text an interpreter reads from stdin has to reach the scanner exactly as
// the shell would deliver it. These tests cover the delivery rules that a
// permission decision cannot show, because two different bodies can score the
// same against the pattern scanner while only one matches what runs.

// stdinCode returns the code of the only snippet a command extracted.
func stdinCode(
	t *testing.T, cmd string,
) string {
	t.Helper()
	br, err := wbd(t, cmd)
	if err != nil {
		t.Fatalf("breakdown %q: %v", cmd, err)
	}
	if len(br.CodeSnippets) != 1 {
		t.Fatalf("breakdown %q: got %d snippets, want 1",
			cmd, len(br.CodeSnippets))
	}

	return br.CodeSnippets[0].Code
}

func TestQuotedHeredocKeepsBackslashes(t *testing.T) {
	code := stdinCode(t, "python3 - <<'PY'\n"+
		`print(re.sub(r"\n", "", text))`+"\nPY\n")
	want := `print(re.sub(r"\n", "", text))` + "\n"
	if code != want {
		t.Errorf("heredoc code = %q, want %q", code, want)
	}
}

func TestDashHeredocStripsLeadingTabs(t *testing.T) {
	code := stdinCode(t,
		"python3 - <<-'PY'\n\t\tprint(1)\n\tPY\n")
	if code != "print(1)\n" {
		t.Errorf("<<- code = %q, want %q",
			code, "print(1)\n")
	}
}

// An interpreter reading stdin is not a REPL, so the body is the program even
// without the `-` argument.
func TestBareInterpreterScansHeredoc(t *testing.T) {
	code := stdinCode(t,
		"python3 <<'PY'\nprint(1)\nPY\n")
	if code != "print(1)\n" {
		t.Errorf("bare python3 code = %q, want %q",
			code, "print(1)\n")
	}
}

// Inline stdin code carries no source file, which is what makes a dangerous
// pattern in it a deny rather than an ask.
func TestHeredocCodeHasNoSourceFile(t *testing.T) {
	br, err := wbd(t,
		"python3 - <<'PY'\nprint(1)\nPY\n")
	if err != nil {
		t.Fatalf("breakdown: %v", err)
	}
	if br.CodeSnippets[0].SourceFile != "" {
		t.Errorf("SourceFile = %q, want empty",
			br.CodeSnippets[0].SourceFile)
	}
}

func TestUnreadableStdinDenies(t *testing.T) {
	cases := map[string]string{
		"unquoted heredoc expands its body": "python3 - <<PY\np\nPY\n",
		"pipe content is unknowable":        "cat prog.py | python3 -",
		"bare interpreter reading a pipe":   "cat prog.py | python3",
		"descriptor copy is unknowable":     "python3 - <&3",
		"path built by expansion":           "python3 - < $PROG",
		"heredoc on another descriptor":     "python3 - 3<<'P'\np\nP\n",
	}
	for name, cmd := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := wbd(t, cmd); err == nil {
				t.Errorf("%q: no error, want a denial",
					cmd)
			}
		})
	}
}

// A redirect on a compound command reaches the commands inside it, but the
// right side of a pipe reads the left side's output instead. Letting the
// heredoc stand in there would scan a program the interpreter never runs.
func TestPipeInsideRedirectedBlockDenies(t *testing.T) {
	cmd := "{ cat prog.py | python3 -; } <<'PY'\nprint(1)\nPY\n"
	if _, err := wbd(t, cmd); err == nil {
		t.Errorf("%q: no error, want a denial", cmd)
	}
}

func TestRedirectedBlockFeedsInterpreter(t *testing.T) {
	code := stdinCode(t,
		"{ python3 -; } <<'PY'\nprint(1)\nPY\n")
	if code != "print(1)\n" {
		t.Errorf("block code = %q, want %q",
			code, "print(1)\n")
	}
}

// A statement's stdin belongs to that statement alone.
func TestHeredocDoesNotReachNextStatement(t *testing.T) {
	cmd := "python3 - <<'PY'\nprint(1)\nPY\npython3 -"
	if _, err := wbd(t, cmd); err == nil {
		t.Errorf("%q: no error, want a denial", cmd)
	}
}

// Exec-style wrappers hand their stdin to the command they run. Commands that
// consume it themselves do not.
func TestStdinForwarding(t *testing.T) {
	heredoc := " <<'PY'\nprint(1)\nPY\n"
	forwarded := []string{
		"timeout 5 python3 -",
		"nohup python3 -",
		"env FOO=1 python3 -",
		"command python3 -",
		"bash -c 'python3 -'",
	}
	for _, prefix := range forwarded {
		t.Run(prefix, func(t *testing.T) {
			if code := stdinCode(
				t, prefix+heredoc,
			); code != "print(1)\n" {
				t.Errorf("code = %q, want %q",
					code, "print(1)\n")
			}
		})
	}

	// xargs reads stdin for its argument list and gives the child
	// /dev/null.
	if _, err := wbd(
		t, "xargs python3 -"+heredoc,
	); err == nil {
		t.Error("xargs: no error, want a denial")
	}
}

// bash reading its script from stdin runs shell code, so it goes through the
// bash pipeline rather than being scanned as a snippet.
func TestBashStdinScriptExtractsCommands(t *testing.T) {
	br, err := wbd(t,
		"bash <<'EOF'\ngit status\nEOF\n")
	if err != nil {
		t.Fatalf("breakdown: %v", err)
	}
	if !hasCmd(br, "git") {
		t.Error("git not extracted from bash stdin script")
	}
}

// bash consumes the stdin it reads its script from, so an interpreter inside
// that script has none.
func TestBashStdinScriptConsumesStdin(t *testing.T) {
	cmd := "bash <<'EOF'\npython3 -\nEOF\n"
	if _, err := wbd(t, cmd); err == nil {
		t.Errorf("%q: no error, want a denial", cmd)
	}
}

func TestSuppliedStdinKinds(t *testing.T) {
	if (model.Stdin{}).Supplied() {
		t.Error("inherited stdin reported as supplied")
	}
	if !(model.Stdin{
		Kind: model.StdinUnreadable}).Supplied() {
		t.Error("unreadable stdin reported as unsupplied")
	}
}
