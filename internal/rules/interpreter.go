package rules

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// interpreterConfig describes an interpreter's flag classification for the
// shared breakdown function.
type interpreterConfig struct {
	// lang is the snippet language constant (e.g. model.LangPython).
	lang string
	// name is the interpreter name for error messages (e.g. "python",
	// "perl").
	name string
	// unverified governs this interpreter's "cannot verify" denials (opaque
	// inline code, opaque or unreadable script path). The breakdown returns
	// them as a RuleError carrying this def, so they attribute to the rule
	// and runBreakdown suppresses them when the rule is disabled.
	unverified *model.RuleDef
	// infoFlags cause an immediate fallthrough to permissions (e.g.
	// --version, --help).
	infoFlags []string
	// codeFlags extract inline code as a snippet (e.g. -c, -e, --eval).
	codeFlags []string
	// unverifiedFlags can add code or change which source the interpreter
	// runs. The shared adapter rejects them until it knows how to scan every
	// executable input.
	unverifiedFlags []string
	// unverifiedPositionals select a source mode instead of naming a script
	// file. The adapter always rejects the shared stdin marker `-` as well.
	unverifiedPositionals []string
	// fallthroughFlags cause a fallthrough without extracting code, such as
	// an interpreter's syntax-check mode.
	fallthroughFlags []string
}

// breakdownInterpreter returns a BreakdownFunc that handles interpreter
// commands generically:
//
//   - Info flags -> fall through to permissions.
//   - Fallthrough flags -> fall through.
//   - One code flag -> extract inline code as a CodeSnippet.
//   - Unverified or repeated code flags -> return a governed error.
//   - Positional -> read script file as a CodeSnippet.
//   - Bare invocation -> fall through.
func breakdownInterpreter(
	cfg interpreterConfig,
) model.BreakdownFunc {
	return func(
		input model.ParseResult,
		state *model.State,
	) (model.BreakdownOutcome, error) {
		// Collect flags in a single pass. Code flags take priority because
		// python3 --version -c "code" must scan the code rather than skip it.
		var codeFlag *model.ParsedFlag
		sawInfo := false
		sawFallthrough := false
		for i := range input.Flags {
			name := input.Flags[i].Name
			if slices.Contains(cfg.unverifiedFlags, name) {
				return model.BreakdownOutcome{}, &model.RuleError{
					Def: cfg.unverified,
					Reason: fmt.Sprintf(
						"%s %s can add code or change which "+
							"program runs — cannot verify the "+
							"invocation",
						cfg.name, name),
				}
			}
			if slices.Contains(
				cfg.infoFlags, name) {
				sawInfo = true
			}
			if slices.Contains(
				cfg.fallthroughFlags, name) {
				sawFallthrough = true
			}
			if slices.Contains(
				cfg.codeFlags, name) {
				if codeFlag != nil {
					return model.BreakdownOutcome{}, &model.RuleError{
						Def: cfg.unverified,
						Reason: fmt.Sprintf(
							"%s: multiple inline code "+
								"arguments cannot be verified",
							cfg.name),
					}
				}

				codeFlag = &input.Flags[i]
			}
		}

		// Fallthrough flags represent modes such as syntax checking that do
		// not execute the supplied source. Info flags (--version) skip only
		// when there are no positionals. If a script is present, scan it in
		// case the interpreter continues after printing the information.
		if codeFlag == nil {
			if sawFallthrough {
				return model.FallThrough(), nil
			}
			if sawInfo &&
				len(input.Positionals) == 0 {
				return model.FallThrough(), nil
			}
		}

		// Inline code extraction.
		if codeFlag != nil {
			if codeFlag.Value == nil {
				return model.BreakdownOutcome{}, &model.RuleError{
					Def: cfg.unverified,
					Reason: fmt.Sprintf(
						"%s requires a code argument",
						codeFlag.Name),
				}
			}
			if !word.Static(codeFlag.Value) {
				return model.BreakdownOutcome{}, &model.RuleError{
					Def: cfg.unverified,
					Reason: fmt.Sprintf(
						"%s %s: code comes from %s"+
							" — cannot read and "+
							"verify it",
						cfg.name, codeFlag.Name,
						word.OpaqueReason(codeFlag.Value)),
				}
			}

			return inlineCode(
				cfg, word.Text(codeFlag.Value)), nil
		}

		// No positionals - bare invocation or flags-only. Anything
		// supplied on stdin is the program to run; with nothing
		// supplied this is an interactive session, so fall through to
		// permissions.
		if len(input.Positionals) == 0 {
			if state.Stdin.Supplied() {
				return stdinProgram(
					cfg, input.Name, state)
			}

			return model.FallThrough(), nil
		}

		// First positional is the script file.
		scriptWord := input.Positionals[0]
		if word.DefinitelyEqual(scriptWord, "-") {
			return stdinProgram(cfg, input.Name, state)
		}
		for _, mode := range cfg.unverifiedPositionals {
			if word.DefinitelyEqual(scriptWord, mode) {
				return model.BreakdownOutcome{}, &model.RuleError{
					Def: cfg.unverified,
					Reason: fmt.Sprintf(
						"%s %s selects an unverified source mode",
						cfg.name, mode),
				}
			}
		}

		if !word.Static(scriptWord) {
			return model.BreakdownOutcome{}, &model.RuleError{
				Def: cfg.unverified,
				Reason: "script path contains " +
					"expansion — cannot determine " +
					"which file to scan",
			}
		}

		path := word.Text(scriptWord)
		if path == "" {
			return model.FallThrough(), nil
		}

		return scriptFile(cfg, path, state)
	}
}

// stdinProgram extracts the program an interpreter reads from stdin. Code
// arrives written into the command itself, so it is scanned like -c code; a
// file keeps file semantics so users can still allow their own scripts.
func stdinProgram(
	cfg interpreterConfig,
	name string,
	state *model.State,
) (model.BreakdownOutcome, error) {
	switch state.Stdin.Kind {
	case model.StdinCode:
		return inlineCode(
			cfg, state.Stdin.Code), nil
	case model.StdinFile:
		return scriptFile(
			cfg, state.Stdin.File, state)
	}

	return model.BreakdownOutcome{}, &model.RuleError{
		Def: cfg.unverified,
		Reason: fmt.Sprintf(
			"%s reads its program from stdin, which this hook "+
				"cannot read. Write the code in a quoted "+
				"heredoc (%s - <<'EOF' ... EOF) so it can "+
				"be scanned",
			cfg.name, name),
	}
}

// inlineCode wraps code written into the command itself. An empty program
// runs nothing.
func inlineCode(
	cfg interpreterConfig, code string,
) model.BreakdownOutcome {
	if code == "" {
		return model.Safe()
	}

	return model.ReplaceOuter(model.BreakdownWork{
		CodeSnippets: []model.CodeSnippet{{
			Language: cfg.lang,
			Code:     code,
		}},
	})
}

// scriptFile reads a script the interpreter runs, recording the path so the
// snippet layer can ask rather than deny on a match.
func scriptFile(
	cfg interpreterConfig,
	path string,
	state *model.State,
) (model.BreakdownOutcome, error) {
	if state.Cwd == "" && !filepath.IsAbs(path) {
		return model.BreakdownOutcome{}, &model.RuleError{
			Def: cfg.unverified,
			Reason: fmt.Sprintf(
				"%s: cannot verify file — "+
					"working directory may have"+
					" changed. Use an absolute"+
					" path", path),
		}
	}

	data, err := model.ReadScript(path, state.Cwd)
	if err != nil {
		return model.BreakdownOutcome{}, &model.RuleError{
			Def: cfg.unverified,
			Reason: fmt.Sprintf(
				"%s: %v", path, err),
		}
	}

	return model.ReplaceOuter(model.BreakdownWork{
		CodeSnippets: []model.CodeSnippet{{
			Language:   cfg.lang,
			Code:       string(data),
			SourceFile: path,
		}},
	}), nil
}
