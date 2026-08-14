package rules

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

type awkProgramSources struct {
	inline *syntax.Word
	files  []*syntax.Word
}

// breakdownAwk separates program sources from values and input paths. Program
// sources become snippets. Data remains subject to shell substitution scanning
// without being mistaken for awk code.
func breakdownAwk(
	input model.ParseResult,
	state *model.State,
) (model.BreakdownOutcome, error) {
	sources, err := parseAwkSources(input.Raw)
	if err != nil {
		return model.BreakdownOutcome{}, err
	}

	snippets, err := readAwkPrograms(sources, state.Cwd)
	if err != nil {
		return model.BreakdownOutcome{}, err
	}
	if len(snippets) == 0 {
		return model.Safe(), nil
	}

	return model.ReplaceOuter(model.BreakdownWork{
		CodeSnippets: snippets,
	}), nil
}

func parseAwkSources(args []*syntax.Word) (awkProgramSources, error) {
	var sources awkProgramSources
	endOptions := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !endOptions && word.DefinitelyEqual(arg, "--") {
			endOptions = true
			continue
		}

		if endOptions || awkWordDefinitelyNonOption(arg) {
			if len(sources.files) == 0 {
				sources.inline = arg
			}

			return sources, nil
		}

		if !word.DefinitelyHasPrefix(arg, "-") {
			return awkProgramSources{}, &model.RuleError{
				Def: awkCommandExec,
				Reason: "awk program or input before -- can become an " +
					"option at runtime - put -- before it",
			}
		}

		name := ""
		switch {
		case word.DefinitelyHasPrefix(arg, "-F"):
			name = "-F"
		case word.DefinitelyHasPrefix(arg, "-f"):
			name = "-f"
		case word.DefinitelyHasPrefix(arg, "-v"):
			name = "-v"
		default:
			return awkProgramSources{}, &model.RuleError{
				Def: awkCommandExec,
				Reason: fmt.Sprintf(
					"awk option %s is not in the portable -F/-f/-v interface",
					word.Text(arg)),
			}
		}

		var value *syntax.Word
		if word.DefinitelyEqual(arg, name) {
			if i+1 >= len(args) {
				return awkProgramSources{}, &model.RuleError{
					Def: awkCommandExec,
					Reason: fmt.Sprintf(
						"awk %s requires a value", name),
				}
			}

			i++
			value = args[i]
		} else {
			_, value = word.SplitPrefix(arg, len(name))
			if value == nil || !awkWordDefinitelyNonEmpty(value) {
				return awkProgramSources{}, &model.RuleError{
					Def: awkCommandExec,
					Reason: fmt.Sprintf(
						"awk %s attached value may be empty at runtime - "+
							"pass it as the next argument", name),
				}
			}
		}

		if !awkWordIsSingleField(value) {
			return awkProgramSources{}, &model.RuleError{
				Def: awkCommandExec,
				Reason: fmt.Sprintf(
					"awk %s value may not stay one argument at runtime - "+
						"quote single-value expansions and avoid pathname "+
						"or brace expansion",
					name),
			}
		}

		if name == "-f" {
			sources.files = append(sources.files, value)
		}
	}

	return sources, nil
}

func awkWordDefinitelyNonOption(w *syntax.Word) bool {
	if word.DefinitelyEqual(w, "-") {
		return true
	}
	if word.MayHavePrefix(w, "-") {
		return false
	}
	if !word.HasUnquotedGlob(w) {
		return true
	}

	return word.DefinitelyHasPrefix(w, "/") ||
		word.DefinitelyHasPrefix(w, "./") ||
		word.DefinitelyHasPrefix(w, "../")
}

func awkWordDefinitelyNonEmpty(w *syntax.Word) bool {
	for _, part := range w.Parts {
		switch part := part.(type) {
		case *syntax.Lit:
			if word.UnescapeBackslashes(part.Value) != "" {
				return true
			}
		case *syntax.SglQuoted:
			if !part.Dollar && part.Value != "" {
				return true
			}
		case *syntax.DblQuoted:
			if awkWordDefinitelyNonEmpty(&syntax.Word{Parts: part.Parts}) {
				return true
			}
		}
	}

	return false
}

// Option values must stay one argv element. Otherwise an expansion can add a
// later -f and turn what looked like data into another program source.
func awkWordIsSingleField(w *syntax.Word) bool {
	if awkWordHasBraceExpansion(w) {
		return false
	}
	if word.HasUnquotedGlob(w) {
		return false
	}

	for _, part := range w.Parts {
		switch part := part.(type) {
		case *syntax.Lit, *syntax.SglQuoted,
			*syntax.ArithmExp, *syntax.ProcSubst:
			continue
		case *syntax.DblQuoted:
			if awkQuotedWordMaySplit(part) {
				return false
			}
		default:
			return false
		}
	}

	return true
}

func awkWordHasBraceExpansion(w *syntax.Word) bool {
	parts := make([]syntax.WordPart, len(w.Parts))
	for i, part := range w.Parts {
		lit, ok := part.(*syntax.Lit)
		if !ok {
			parts[i] = part
			continue
		}

		copy := *lit
		var masked strings.Builder
		for j := 0; j < len(lit.Value); j++ {
			if lit.Value[j] != '\\' || j+1 >= len(lit.Value) {
				masked.WriteByte(lit.Value[j])
				continue
			}

			masked.WriteByte('\\')
			j++
			switch lit.Value[j] {
			case '{', '}', ',', '.':
				masked.WriteByte(0)
			default:
				masked.WriteByte(lit.Value[j])
			}
		}

		copy.Value = masked.String()
		parts[i] = &copy
	}

	copy := syntax.Word{Parts: parts}
	return syntax.SplitBraces(&copy)
}

func awkQuotedWordMaySplit(quoted *syntax.DblQuoted) bool {
	maySplit := false
	syntax.Walk(quoted, func(node syntax.Node) bool {
		param, ok := node.(*syntax.ParamExp)
		if !ok {
			return true
		}
		if param.Param != nil && param.Param.Value == "@" {
			maySplit = true
			return false
		}
		if param.Names == syntax.NamesPrefixWords {
			maySplit = true
			return false
		}

		index, ok := param.Index.(*syntax.Word)
		if ok && word.DefinitelyEqual(index, "@") {
			maySplit = true
			return false
		}

		return true
	})

	return maySplit
}

func readAwkPrograms(
	sources awkProgramSources,
	cwd string,
) ([]model.CodeSnippet, error) {
	if sources.inline != nil {
		program := sources.inline
		if !word.Static(program) || word.HasUnquotedGlob(program) {
			reason := word.OpaqueReason(program)
			if reason == "" {
				reason = "pathname expansion"
			}

			return nil, &model.RuleError{
				Def: awkCommandExec,
				Reason: "awk program contains " + reason +
					"; cannot read and verify it",
			}
		}

		return []model.CodeSnippet{{
			Language: model.LangAwk,
			Code:     word.Text(program),
		}}, nil
	}
	if len(sources.files) == 0 {
		return nil, nil
	}

	contents := make([]string, 0, len(sources.files))
	paths := make([]string, 0, len(sources.files))
	for _, sourceFile := range sources.files {
		code, path, err := readAwkProgramFile(sourceFile, cwd)
		if err != nil {
			return nil, err
		}

		contents = append(contents, code)
		paths = append(paths, path)
	}

	sourceFile := strings.Join(paths, " + ")
	exact := strings.Join(contents, "")
	snippets := []model.CodeSnippet{{
		Language:   model.LangAwk,
		Code:       exact,
		SourceFile: sourceFile,
	}}
	// Awk implementations disagree about whether a source-file boundary
	// inserts a newline. Scan both ordered forms so neither can hide a
	// token.
	withNewlines := strings.Join(contents, "\n")
	if withNewlines != exact {
		snippets = append(snippets, model.CodeSnippet{
			Language:   model.LangAwk,
			Code:       withNewlines,
			SourceFile: sourceFile,
		})
	}

	return snippets, nil
}

func readAwkProgramFile(
	programFile *syntax.Word,
	cwd string,
) (string, string, error) {
	if !word.Static(programFile) || word.HasUnquotedGlob(programFile) {
		reason := word.OpaqueReason(programFile)
		if reason == "" {
			reason = "pathname expansion"
		}

		return "", "", &model.RuleError{
			Def: awkCommandExec,
			Reason: "awk -f program path contains " + reason +
				"; cannot determine which file to scan",
		}
	}

	path := word.Text(programFile)
	if path == "-" {
		return "", "", &model.RuleError{
			Def: awkCommandExec,
			Reason: "awk -f - reads a program from standard input and " +
				"cannot be verified",
		}
	}
	if !filepath.IsAbs(path) && !hasPathSeparator(path) {
		return "", "", &model.RuleError{
			Def: awkCommandExec,
			Reason: fmt.Sprintf(
				"awk -f %s may resolve through AWKPATH; use ./%s",
				path, path),
		}
	}
	if cwd == "" && !filepath.IsAbs(path) {
		return "", "", &model.RuleError{
			Def: awkCommandExec,
			Reason: fmt.Sprintf(
				"%s: cannot verify file because the working directory "+
					"may have changed. Use an absolute path",
				path),
		}
	}

	data, err := model.ReadScript(path, cwd)
	if err != nil {
		return "", "", &model.RuleError{
			Def:    awkCommandExec,
			Reason: fmt.Sprintf("%s: %v", path, err),
		}
	}

	return string(data), path, nil
}

func hasPathSeparator(path string) bool {
	return filepath.Base(path) != path
}

// awkCommandExecution matches syntax that can execute or load code. A small
// lexer keeps strings, regular expressions, and comments out of the decision.
func awkCommandExecution() matchBuilder {
	return matchBuilder{check: awkHasCommandExecution}
}

func awkHasCommandExecution(code string) bool {
	expectOperand := true
	for i := 0; i < len(code); {
		if code[i] == '\\' && i+1 < len(code) && code[i+1] == '\n' {
			i += 2
			continue
		}
		if isAwkSpace(code[i]) {
			if code[i] == '\n' {
				expectOperand = true
			}

			i++
			continue
		}
		if code[i] == '#' {
			i = skipAwkComment(code, i)
			continue
		}
		if code[i] == '"' {
			i = skipAwkString(code, i)
			expectOperand = false
			continue
		}
		if code[i] == '/' && expectOperand {
			i = skipAwkRegex(code, i)
			expectOperand = false
			continue
		}
		if isAwkIdentifierStart(code[i]) {
			name, end := scanAwkIdentifier(code, i)
			i = end
			if (name == "system" || name == "extension") &&
				awkCallFollows(code, i) {
				return true
			}

			expectOperand = awkKeywordNeedsOperand(name)
			continue
		}
		if code[i] == '@' {
			name, end := scanAwkIdentifier(code, i+1)
			if name == "include" || name == "load" {
				return true
			}
			if awkCallFollows(code, end) {
				return true
			}

			i = end
			expectOperand = true
			continue
		}
		if code[i] == '|' {
			if i+1 < len(code) && code[i+1] == '|' {
				i += 2
				expectOperand = true
				continue
			}

			return true
		}
		if isAwkDigit(code[i]) ||
			(code[i] == '.' && i+1 < len(code) &&
				isAwkDigit(code[i+1])) {
			i++
			for i < len(code) &&
				(isAwkDigit(code[i]) || code[i] == '.') {
				i++
			}

			expectOperand = false
			continue
		}

		switch code[i] {
		case ')', ']':
			expectOperand = false
		case '+', '-':
			if i+1 < len(code) && code[i+1] == code[i] {
				i++
			} else {
				expectOperand = true
			}
		case '/', '=', '!', '~', '<', '>', '*', '%', '^', '&',
			'?', ':', ',', ';', '(', '[', '{', '}':
			expectOperand = true
		case '$':
			expectOperand = true
		default:
			expectOperand = true
		}

		i++
	}

	return false
}

func scanAwkIdentifier(code string, start int) (string, int) {
	var name strings.Builder
	i := start
	for i < len(code) {
		if isAwkIdentifierPart(code[i]) {
			name.WriteByte(code[i])
			i++
			continue
		}
		if code[i] == '\\' && i+1 < len(code) && code[i+1] == '\n' {
			i += 2
			continue
		}

		break
	}

	return name.String(), i
}

func awkCallFollows(code string, i int) bool {
	for i < len(code) {
		if code[i] == '\\' && i+1 < len(code) && code[i+1] == '\n' {
			i += 2
			continue
		}
		if isAwkSpace(code[i]) {
			i++
			continue
		}
		if code[i] == '#' {
			i = skipAwkComment(code, i)
			continue
		}

		return code[i] == '('
	}

	return false
}

func skipAwkString(code string, start int) int {
	for i := start + 1; i < len(code); i++ {
		if code[i] == '\\' && i+1 < len(code) {
			i++
			continue
		}
		if code[i] == '"' {
			return i + 1
		}
	}

	return len(code)
}

func skipAwkRegex(code string, start int) int {
	inClass := false
	classCanClose := false
	for i := start + 1; i < len(code); {
		if code[i] == '\\' && i+1 < len(code) {
			i += 2
			if inClass {
				classCanClose = true
			}

			continue
		}
		if code[i] == '/' && !inClass {
			return i + 1
		}
		if code[i] == '\n' {
			return i
		}

		switch code[i] {
		case '[':
			if !inClass {
				inClass = true
				classCanClose = false
				i++
				continue
			}
			if next, ok := skipAwkBracketConstruct(code, i); ok {
				i = next
				classCanClose = true
				continue
			}

			classCanClose = true
		case ']':
			if inClass && classCanClose {
				inClass = false
			}

			classCanClose = true
		default:
			if inClass && code[i] != '^' {
				classCanClose = true
			}
		}

		i++
	}

	return len(code)
}

func skipAwkBracketConstruct(code string, i int) (int, bool) {
	if i+1 >= len(code) ||
		(code[i+1] != ':' && code[i+1] != '.' && code[i+1] != '=') {
		return i, false
	}

	marker := code[i+1]
	for i += 2; i+1 < len(code); i++ {
		if code[i] == marker && code[i+1] == ']' {
			return i + 2, true
		}
		if code[i] == '\\' && i+1 < len(code) {
			i++
		}
		if code[i] == '\n' {
			return i, false
		}
	}

	return i, false
}

func skipAwkComment(code string, start int) int {
	i := start
	for i < len(code) && code[i] != '\n' {
		i++
	}

	return i
}

func awkKeywordNeedsOperand(name string) bool {
	switch name {
	case "print", "printf", "return", "exit":
		return true
	default:
		return false
	}
}

func isAwkSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f'
}

func isAwkIdentifierStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isAwkIdentifierPart(c byte) bool {
	return isAwkIdentifierStart(c) || isAwkDigit(c)
}

func isAwkDigit(c byte) bool {
	return c >= '0' && c <= '9'
}
