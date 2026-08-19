package rules

import (
	"fmt"
	"path/filepath"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

func verifyBashSource(
	owner string,
	source *syntax.Word,
) (string, error) {
	if word.Static(source) {
		return word.Text(source), nil
	}

	return "", &model.RuleError{
		Def: bashUnverified,
		Reason: fmt.Sprintf(
			"%s: code comes from %s and cannot be "+
				"verified. Quote the code so the outer "+
				"shell passes it literally",
			owner, word.OpaqueReason(source)),
	}
}

func breakdownBash(
	input model.ParseResult,
	state *model.State,
) (model.BreakdownOutcome, error) {
	// --version and --help print and exit without executing.
	if len(input.Raw) == 1 &&
		(word.DefinitelyEqual(input.Raw[0], "--version") ||
			word.DefinitelyEqual(input.Raw[0], "--help")) {
		return model.Safe(), nil
	}

	// -n only checks syntax.
	if len(input.PossibleFlags) == 1 &&
		input.PossibleFlags[0].Name == "-n" {
		return model.Safe(), nil
	}

	// Check PossibleFlags for -c. The caller (runBreakdown) already
	// populated these via PopulatePossibleFlags since bash has no Parser.
	for _, pf := range input.PossibleFlags {
		if pf.Name != "-c" {
			continue
		}

		// Other flags (--rcfile, --init-file, -i) can source arbitrary
		// code before the -c body runs.
		for _, other := range input.PossibleFlags {
			if other.Name != "-c" {
				return model.FallThrough(), nil
			}
		}

		if pf.Value == nil {
			return model.FallThrough(), nil
		}

		code, err := verifyBashSource("bash -c", pf.Value)
		if err != nil {
			return model.BreakdownOutcome{}, err
		}
		if code == "" {
			return model.Safe(), nil
		}

		// bash -c leaves stdin alone, so the code it runs reads
		// whatever bash was given.
		return model.ReplaceOuter(model.BreakdownWork{
			CodeStrings:  []string{code},
			ForwardStdin: true,
		}), nil
	}

	// Bare bash and bash -s take their script from stdin. A readable
	// one runs through the normal bash pipeline like -c code. Bash
	// consumes that stdin, so the code inside inherits nothing.
	if readsScriptFromStdin(input) &&
		state.Stdin.Kind == model.StdinCode {
		if state.Stdin.Code == "" {
			return model.Safe(), nil
		}

		return model.ReplaceOuter(model.BreakdownWork{
			CodeStrings: []string{state.Stdin.Code},
		}), nil
	}

	// Without -c, the first arg must be a non-flag positional. A flag
	// before it (bash -x script.sh) leaves the invocation unverifiable, so
	// fall through to the rules-layer deny.
	if len(input.Raw) == 0 {
		return model.FallThrough(), nil
	}
	if word.MayHavePrefix(input.Raw[0], "-") {
		return model.FallThrough(), nil
	}

	scriptWord := input.Raw[0]
	if !word.Static(scriptWord) {
		return model.FallThrough(), nil
	}

	path := word.Text(scriptWord)
	if state.Cwd == "" && !filepath.IsAbs(path) {
		return model.BreakdownOutcome{}, &model.RuleError{
			Def: bashUnverified,
			Reason: fmt.Sprintf(
				"%s: cannot verify file — working "+
					"directory may have changed. "+
					"Use an absolute path or run "+
					"the script directly (%s)",
				path, word.DirectPath(path)),
		}
	}

	return model.ReplaceOuter(model.BreakdownWork{
		ScanFiles: []string{path},
	}), nil
}

// readsScriptFromStdin covers bare bash and bash -s. Any other flag can run
// code of its own, which stdin would not account for.
func readsScriptFromStdin(
	input model.ParseResult,
) bool {
	for _, arg := range input.Raw {
		if !word.DefinitelyEqual(arg, "-s") {
			return false
		}
	}

	return true
}

// hookFormatBashDenial is the bash/sh denial message.
func hookFormatBashDenial(
	input model.ParseResult,
) (model.Decision, string) {
	if len(input.Raw) > 0 &&
		!word.MayHavePrefix(input.Raw[0], "-") {
		text := word.Text(input.Raw[0])
		return model.Deny, fmt.Sprintf(
			"bare invocation — invoke the "+
				"script directly (e.g. %s) "+
				"instead",
			word.DirectPath(text))
	}

	return model.Deny, "bare invocation"
}
