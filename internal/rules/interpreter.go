package rules

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// interpreterConfig describes an interpreter's flag
// classification for the shared breakdown function.
type interpreterConfig struct {
	// lang is the snippet language constant
	// (e.g. model.LangPython).
	lang string
	// name is the interpreter name for error messages
	// (e.g. "python", "perl").
	name string
	// infoFlags cause an immediate fallthrough to
	// permissions (e.g. --version, --help).
	infoFlags []string
	// codeFlags extract inline code as a snippet
	// (e.g. -c, -e, --eval).
	codeFlags []string
	// fallthroughFlags cause a fallthrough without
	// extracting code (e.g. -m, -i).
	fallthroughFlags []string
}

// breakdownInterpreter returns a BreakdownFunc that
// handles interpreter commands generically:
//
//   - Info flags → nil (fall through to permissions).
//   - Fallthrough flags → nil (fall through).
//   - Code flags → extract inline code as a CodeSnippet.
//   - Positional → read script file as a CodeSnippet.
//   - Bare invocation → nil (fall through).
func breakdownInterpreter(
	cfg interpreterConfig,
) model.BreakdownFunc {
	return func(
		input model.ParseResult,
		state *model.State,
	) (*model.UnwrapResult, error) {
		// Collect flags in a single pass. Code flags
		// take priority — python3 --version -c "code"
		// must scan the code, not skip because of
		// --version.
		var codeFlag *model.ParsedFlag
		sawInfo := false
		sawFallthrough := false
		for i := range input.Flags {
			name := input.Flags[i].Name
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
				codeFlag = &input.Flags[i]
			}
		}
		// Fallthrough flags (-m, -i) always skip —
		// we explicitly can't verify these.
		// Info flags (--version) only skip when
		// there are no positionals — if a script
		// is present, scan it defensively in case
		// our flag classification is wrong and the
		// interpreter actually runs it.
		if codeFlag == nil {
			if sawFallthrough {
				return nil, nil
			}
			if sawInfo &&
				len(input.Positionals) == 0 {
				return nil, nil
			}
		}

		// Inline code extraction.
		if codeFlag != nil {
			if codeFlag.Value == nil {
				return nil, fmt.Errorf(
					"%s requires a code argument",
					codeFlag.Name)
			}
			if reason := word.ExpansionReason(
				codeFlag.Value); reason != "" {
				return nil, fmt.Errorf(
					"%s %s: code comes from %s"+
						" — cannot read and "+
						"verify it",
					cfg.name, codeFlag.Name,
					reason)
			}
			code := word.Text(codeFlag.Value)
			if code == "" {
				return &model.UnwrapResult{}, nil
			}
			return &model.UnwrapResult{
				CodeSnippets: []model.CodeSnippet{{
					Language: cfg.lang,
					Code:     code,
				}},
			}, nil
		}

		// No positionals — bare invocation or
		// flags-only. Fall through to permissions.
		if len(input.Positionals) == 0 {
			return nil, nil
		}

		// First positional is the script file.
		scriptWord := input.Positionals[0]
		if !word.Static(scriptWord) {
			return nil, fmt.Errorf(
				"script path contains expansion" +
					" — cannot determine which" +
					" file to scan")
		}

		path := word.Text(scriptWord)
		if path == "" {
			return nil, nil
		}

		if state.Cwd == "" && !filepath.IsAbs(path) {
			return nil, fmt.Errorf(
				"%s: cannot verify file — "+
					"working directory may have"+
					" changed. Use an absolute"+
					" path", path)
		}

		data, err := model.ReadScript(
			path, state.Cwd)
		if err != nil {
			return nil, fmt.Errorf(
				"%s: %v", path, err)
		}

		return &model.UnwrapResult{
			CodeSnippets: []model.CodeSnippet{{
				Language:   cfg.lang,
				Code:       string(data),
				SourceFile: path,
			}},
		}, nil
	}
}
