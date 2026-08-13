package rules

import (
	"fmt"
	"path/filepath"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// breakdownBash handles bash/sh inner command extraction.
//
// For --version/--help: returns Safe (read-only, nothing
// to check).
// For -n (syntax check): returns Safe (doesn't execute).
// For -c "string": extracts and returns the code string
// as a command to re-parse.
// For script.sh positional: returns the file path for
// scanning.
// For bare bash or unrecognised flags: falls through to
// the rules layer, which denies bare bash.
func breakdownBash(
	input model.ParseResult,
	state *model.State,
) (model.BreakdownOutcome, error) {
	// --version and --help print info and exit without
	// executing anything. Allow when they're the sole
	// argument.
	if len(input.Raw) == 1 &&
		(word.DefinitelyEqual(input.Raw[0], "--version") ||
			word.DefinitelyEqual(input.Raw[0], "--help")) {
		return model.Safe(), nil
	}

	// -n only checks syntax without executing anything.
	// Allow when it's the sole flag.
	if len(input.PossibleFlags) == 1 &&
		input.PossibleFlags[0].Name == "-n" {
		return model.Safe(), nil
	}

	// Check PossibleFlags for -c. The caller
	// (runBreakdown) already populated these via
	// PopulatePossibleFlags since bash has no Parser.
	for _, pf := range input.PossibleFlags {
		if pf.Name != "-c" {
			continue
		}
		// Found -c. Only extract when -c is the sole
		// flag — other flags (--rcfile, --init-file,
		// -i, etc.) can source arbitrary code before
		// the -c body runs.
		for _, other := range input.PossibleFlags {
			if other.Name != "-c" {
				return model.FallThrough(), nil
			}
		}
		if pf.Value == nil {
			return model.FallThrough(), nil
		}
		if reason := word.ExpansionReason(
			pf.Value); reason != "" {
			return model.BreakdownOutcome{}, &model.RuleError{
				Def: bashUnverified,
				Reason: fmt.Sprintf(
					"bash -c: code comes from %s "+
						"— cannot read and verify "+
						"it. Use the command "+
						"directly instead of "+
						"bash -c",
					reason),
			}
		}
		code := word.Text(pf.Value)
		if code == "" {
			return model.Safe(), nil
		}
		return model.ReplaceOuter(model.BreakdownWork{
			CodeStrings: []string{code},
		}), nil
	}

	// No -c flag. Check for script file: the first arg
	// must be a non-flag positional (e.g.
	// "bash script.sh"). If there are any flags before
	// the positional (e.g. "bash -x script.sh"), we
	// can't verify the invocation — fall through to the
	// rules layer deny.
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

// hookFormatBashDenial is a HookFunc for bash/sh. Always
// denies with a formatted reason.
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
