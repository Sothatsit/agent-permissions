package breakdown

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/rules"
	"github.com/sothatsit/agent-permissions/internal/word"
)

func breakdownTrapTest(
	t *testing.T, command string, cwd string,
) (model.BreakdownResult, error) {
	t.Helper()
	registry, snippets := rules.Registry()
	config := rules.AllEnabled()
	rules.FilterByConfig(registry, snippets, config)
	return Breakdown(command, cwd, registry, config)
}

func writeTrapTestScript(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "safe.sh")
	if err := os.WriteFile(path, []byte("echo ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return directory
}

func commandFunctionFlags(
	result model.BreakdownResult, name string,
) []bool {
	var flags []bool
	for _, command := range result.Commands {
		if len(command.Args) == 0 ||
			!word.DefinitelyEqual(command.Args[0], name) {
			continue
		}

		flags = append(flags, command.CouldBeFuncCall)
	}

	return flags
}

func TestTrapHandlerStartsWithUnknownWorkingDirectory(t *testing.T) {
	directory := writeTrapTestScript(t)
	_, err := breakdownTrapTest(
		t, `trap 'bash safe.sh' EXIT`, directory)
	if err == nil ||
		!strings.Contains(err.Error(), "working directory may have changed") {
		t.Fatalf("breakdown error = %v, want unknown directory", err)
	}
}

func TestTrapHandlerTracksWorkingDirectoryWithinHandler(t *testing.T) {
	directory := writeTrapTestScript(t)
	command := fmt.Sprintf(
		`trap 'cd %s && bash safe.sh' EXIT`, directory)
	result, err := breakdownTrapTest(t, command, directory)
	if err != nil {
		t.Fatalf("unexpected breakdown error: %v", err)
	}
	if !hasCmd(result, "echo") {
		t.Fatalf("commands = %v, want scanned script", cmdNames(result))
	}
}

func TestTrapHandlerLeavesWorkingDirectoryUnknown(t *testing.T) {
	directory := writeTrapTestScript(t)
	_, err := breakdownTrapTest(
		t, `trap 'echo cleanup' EXIT; bash safe.sh`, directory)
	if err == nil ||
		!strings.Contains(err.Error(), "working directory may have changed") {
		t.Fatalf("breakdown error = %v, want unknown directory", err)
	}
}

func TestTrapHandlerUsesOnlyLocalFunctionKnowledge(t *testing.T) {
	result, err := breakdownTrapTest(t,
		`known(){ :; }; trap 'known; inner(){ :; }; inner' EXIT`, "/work")
	if err != nil {
		t.Fatalf("unexpected breakdown error: %v", err)
	}

	if got := commandFunctionFlags(result, "known"); len(got) != 1 || got[0] {
		t.Errorf("known function flags = %v, want [false]", got)
	}

	if got := commandFunctionFlags(result, "inner"); len(got) != 1 || !got[0] {
		t.Errorf("inner function flags = %v, want [true]", got)
	}
}

func TestConditionalTrapHandlerHasFreshTopLevel(t *testing.T) {
	result, err := breakdownTrapTest(t,
		`if true; then trap 'inner(){ :; }; inner' EXIT; fi`, "/work")
	if err != nil {
		t.Fatalf("unexpected breakdown error: %v", err)
	}

	if got := commandFunctionFlags(result, "inner"); len(got) != 1 || !got[0] {
		t.Errorf("inner function flags = %v, want [true]", got)
	}
}

func TestTrapHandlerInvalidatesLaterFunctionKnowledge(t *testing.T) {
	_, err := breakdownTrapTest(t,
		`trap 'echo cleanup' EXIT; later(){ :; }; later`, "/work")
	if err == nil || !strings.Contains(err.Error(), "unset -f") {
		t.Fatalf("breakdown error = %v, want unknown function state", err)
	}
}

func TestTrapHandlerForgetsKnownAndDeferredFunctions(t *testing.T) {
	result, err := breakdownTrapTest(t,
		`known(){ :; }; trap 'inner(){ :; }' EXIT; known; inner`, "/work")
	if err != nil {
		t.Fatalf("unexpected breakdown error: %v", err)
	}

	for _, name := range []string{"known", "inner"} {
		flags := commandFunctionFlags(result, name)
		if len(flags) != 1 || flags[0] {
			t.Errorf("%s function flags = %v, want [false]", name, flags)
		}
	}
}

func TestTrapNonHandlersPreserveState(t *testing.T) {
	directory := writeTrapTestScript(t)
	forms := []string{
		"trap",
		"trap -l",
		"trap -p",
		"trap -lp EXIT",
		"trap -pl EXIT",
		"trap 'echo cleanup'",
		"trap -- 'echo cleanup'",
		"trap - EXIT",
		"trap '' EXIT",
	}
	for _, form := range forms {
		t.Run(form, func(t *testing.T) {
			command := "known(){ :; }; " + form +
				"; known; bash safe.sh"
			result, err := breakdownTrapTest(t, command, directory)
			if err != nil {
				t.Fatalf("unexpected breakdown error: %v", err)
			}

			flags := commandFunctionFlags(result, "known")
			if len(flags) != 1 || !flags[0] {
				t.Errorf(
					"known function flags = %v, want [true]", flags)
			}

			if !hasCmd(result, "echo") {
				t.Errorf("commands = %v, want scanned script", cmdNames(result))
			}
		})
	}
}

func TestTrapHandlerInvalidationStaysInChildShell(t *testing.T) {
	commands := []string{
		`known(){ :; }; (trap 'echo cleanup' EXIT); known`,
		`known(){ :; }; trap 'echo cleanup' EXIT | cat; known`,
		`known(){ :; }; echo "$(trap 'echo cleanup' EXIT)"; known`,
		`known(){ :; }; cat <(trap 'echo cleanup' EXIT); known`,
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			result, err := breakdownTrapTest(t, command, "/work")
			if err != nil {
				t.Fatalf("unexpected breakdown error: %v", err)
			}

			flags := commandFunctionFlags(result, "known")
			if len(flags) != 1 || !flags[0] {
				t.Errorf("known function flags = %v, want [true]", flags)
			}
		})
	}
}

func TestEmptyTrapHandlerIsSafe(t *testing.T) {
	for _, code := range []string{"# cleanup", "   "} {
		t.Run(code, func(t *testing.T) {
			result, err := breakdownTrapTest(
				t, "trap '"+code+"' EXIT", "/work")
			if err != nil {
				t.Fatalf("unexpected breakdown error: %v", err)
			}
			if !result.Safe {
				t.Fatal("empty handler is not safe")
			}
		})
	}
}
