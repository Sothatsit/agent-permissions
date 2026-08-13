package rules

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

type sedParseMode int

const (
	sedGNUDefault sedParseMode = iota
	sedGNUPosix
	sedBSD
)

type sedOptionArg int

const (
	sedNoArg sedOptionArg = iota
	sedRequiredArg
	sedOptionalAttachedArg
)

type sedOptionAction int

const (
	sedOrdinaryOption sedOptionAction = iota
	sedInlineSource
	sedFileSource
	sedSandboxOption
	sedInfoOption
)

type sedLongOption struct {
	name   string
	arg    sedOptionArg
	action sedOptionAction
}

// Keep this to common source and safety options. Unknown options fail closed,
// which avoids copying implementation-specific getopt tables into this rule.
var sedLongOptions = []sedLongOption{
	{name: "--expression", arg: sedRequiredArg, action: sedInlineSource},
	{name: "--file", arg: sedRequiredArg, action: sedFileSource},
	{name: "--help", action: sedInfoOption},
	{name: "--in-place", arg: sedOptionalAttachedArg},
	{name: "--quiet"},
	{name: "--sandbox", action: sedSandboxOption},
	{name: "--silent"},
	{name: "--version", action: sedInfoOption},
}

type sedSource struct {
	word *syntax.Word
	file bool
}

type sedOperand struct {
	word      *syntax.Word
	protected bool
}

type sedInterpretation struct {
	mode      sedParseMode
	sources   []sedSource
	operands  []sedOperand
	sandboxed bool
	infoOnly  bool
}

// breakdownSed separates sed programs from data operands. Programs are
// scanned as sed snippets, while data operands retain only their shell-level
// substitutions. GNU sed's sandbox validates programs itself and is the safe
// way to use shell-expanded program text.
func breakdownSed(
	input model.ParseResult,
	state *model.State,
) (model.BreakdownOutcome, error) {
	work := model.BreakdownWork{ShellWords: input.Raw}
	var interpretations []sedInterpretation
	for _, mode := range []sedParseMode{
		sedGNUDefault, sedGNUPosix, sedBSD,
	} {
		parsed, valid, err := parseSedInterpretation(input.Raw, mode)
		if err != nil {
			return model.BreakdownOutcome{}, &model.RuleError{
				Def:    sedCommandExec,
				Reason: err.Error(),
			}
		}
		if valid {
			interpretations = append(interpretations, parsed)
		}
	}

	// No supported interpretation can identify the program operands. Treat
	// even invalid-looking syntax as unverified because sed may come from PATH.
	if len(interpretations) == 0 {
		return model.BreakdownOutcome{}, &model.RuleError{
			Def:    sedCommandExec,
			Reason: "cannot verify sed option syntax",
		}
	}
	if everySedInterpretation(
		interpretations, func(p sedInterpretation) bool {
			return p.infoOnly
		}) {
		if len(input.Raw) == 0 {
			return model.Safe(), nil
		}
		return model.ReplaceOuter(work), nil
	}

	for _, parsed := range interpretations {
		if parsed.mode != sedGNUDefault || parsed.infoOnly {
			continue
		}
		for _, operand := range parsed.operands {
			if operand.protected ||
				(sedOperandHasSafePrefix(operand.word) &&
					sedWordExpandsAsOneField(operand.word)) {
				continue
			}
			if !word.Static(operand.word) || word.HasUnquotedGlob(operand.word) {
				return model.BreakdownOutcome{}, &model.RuleError{
					Def: sedCommandExec,
					Reason: "sed input before -- can become an option " +
						"at runtime - put -- before dynamic input files",
				}
			}
		}
	}

	if everySedInterpretation(
		interpretations, func(p sedInterpretation) bool {
			return p.sandboxed
		}) {
		if len(input.Raw) == 0 {
			return model.Safe(), nil
		}
		return model.ReplaceOuter(work), nil
	}

	seen := make(map[string]bool)
	var snippets []model.CodeSnippet
	for _, parsed := range interpretations {
		programs, err := sedProgramSnippets(parsed.sources, state.Cwd)
		if err != nil {
			return model.BreakdownOutcome{}, err
		}
		for _, snippet := range programs {
			key := snippet.SourceFile + "\x00" + snippet.Code
			if seen[key] {
				continue
			}
			seen[key] = true
			snippets = append(snippets, snippet)
		}
	}
	work.CodeSnippets = append(work.CodeSnippets, snippets...)
	if len(input.Raw) == 0 {
		return model.Safe(), nil
	}
	return model.ReplaceOuter(work), nil
}

func parseSedInterpretation(
	args []*syntax.Word,
	mode sedParseMode,
) (sedInterpretation, bool, error) {
	parsed := sedInterpretation{mode: mode}
	var positionals []sedOperand
	endOptions := false
	stopAtPositional := mode != sedGNUDefault
	hasSourceOption := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if endOptions {
			positionals = append(positionals, sedOperand{
				word: arg, protected: true,
			})
			continue
		}
		if word.DefinitelyEqual(arg, "--") {
			endOptions = true
			continue
		}
		if word.DefinitelyEqual(arg, "-") ||
			!word.DefinitelyHasPrefix(arg, "-") {
			positionals = append(positionals, sedOperand{word: arg})
			if stopAtPositional {
				for _, rest := range args[i+1:] {
					positionals = append(
						positionals, sedOperand{word: rest})
				}
				break
			}
			continue
		}

		var valid bool
		var err error
		if mode == sedBSD {
			i, valid, err = parseSedBSDOption(
				args, i, &parsed, &hasSourceOption)
		} else {
			i, valid, err = parseSedGNUOption(
				args, i, &parsed, &hasSourceOption)
		}
		if err != nil || !valid {
			return sedInterpretation{}, valid, err
		}
		if parsed.infoOnly {
			return parsed, true, nil
		}
	}

	if !hasSourceOption && len(positionals) > 0 {
		parsed.sources = append(parsed.sources, sedSource{
			word: positionals[0].word,
		})
		positionals = positionals[1:]
	}
	parsed.operands = positionals
	return parsed, true, nil
}

func parseSedGNUOption(
	args []*syntax.Word,
	i int,
	parsed *sedInterpretation,
	hasSourceOption *bool,
) (int, bool, error) {
	arg := args[i]
	if word.DefinitelyHasPrefix(arg, "--") {
		return parseSedGNULongOption(
			args, i, parsed, hasSourceOption)
	}

	prefix := sedStaticPrefix(arg)
	if len(prefix) < 2 {
		return i, false, fmt.Errorf(
			"sed option contains %s; cannot determine which programs to scan",
			word.OpaqueReason(arg))
	}
	for pos := 1; pos < len(prefix); pos++ {
		action := sedOrdinaryOption
		argKind := sedNoArg
		switch prefix[pos] {
		case 'e':
			action = sedInlineSource
			argKind = sedRequiredArg
		case 'f':
			action = sedFileSource
			argKind = sedRequiredArg
		case 'l':
			argKind = sedRequiredArg
		case 'i':
			argKind = sedOptionalAttachedArg
		case 'b', 'c', 'E', 'n', 'r', 's', 'u', 'z':
		default:
			return i, false, nil
		}

		if argKind == sedNoArg {
			continue
		}
		if pos+1 < len(prefix) || !word.Static(arg) {
			value := sedAttachedValue(arg, pos+1)
			if value == nil {
				return i, false, fmt.Errorf(
					"sed option contains opaque syntax; cannot determine its value")
			}
			if err := addSedOptionValue(
				parsed, action, value, hasSourceOption,
			); err != nil {
				return i, false, err
			}
			return i, true, nil
		}
		if argKind == sedOptionalAttachedArg {
			return i, true, nil
		}
		if i+1 >= len(args) {
			return i, false, nil
		}
		i++
		if err := addSedOptionValue(
			parsed, action, args[i], hasSourceOption,
		); err != nil {
			return i, false, err
		}
		return i, true, nil
	}

	if !word.Static(arg) {
		return i, false, fmt.Errorf(
			"sed option contains %s; cannot determine which programs to scan",
			word.OpaqueReason(arg))
	}
	return i, true, nil
}

func parseSedGNULongOption(
	args []*syntax.Word,
	i int,
	parsed *sedInterpretation,
	hasSourceOption *bool,
) (int, bool, error) {
	arg := args[i]
	name, attached := word.SplitEq(arg)
	if attached == nil {
		if word.Static(arg) {
			text := word.Text(arg)
			if equals := strings.IndexByte(text, '='); equals >= 0 {
				name = text[:equals]
				attached = word.Lit(text[equals+1:])
			} else {
				name = text
			}
		} else {
			return i, false, fmt.Errorf(
				"sed option contains %s; cannot determine which programs to scan",
				word.OpaqueReason(arg))
		}
	}

	option, ok := resolveSedLongOption(name)
	if !ok {
		return i, false, fmt.Errorf(
			"unknown sed long option %s; cannot determine "+
				"which programs to scan",
			name)
	}
	if option.arg == sedNoArg && attached != nil {
		return i, false, nil
	}
	if option.arg == sedRequiredArg && attached == nil {
		if i+1 >= len(args) {
			return i, false, nil
		}
		i++
		attached = args[i]
	}
	if err := addSedOptionValue(
		parsed, option.action, attached, hasSourceOption,
	); err != nil {
		return i, false, err
	}
	return i, true, nil
}

func resolveSedLongOption(name string) (sedLongOption, bool) {
	for _, option := range sedLongOptions {
		if option.name == name {
			return option, true
		}
	}
	return sedLongOption{}, false
}

func parseSedBSDOption(
	args []*syntax.Word,
	i int,
	parsed *sedInterpretation,
	hasSourceOption *bool,
) (int, bool, error) {
	arg := args[i]
	if word.DefinitelyHasPrefix(arg, "--") {
		return i, false, nil
	}

	prefix := sedStaticPrefix(arg)
	if len(prefix) < 2 {
		return i, false, fmt.Errorf(
			"sed option contains %s; cannot determine which programs to scan",
			word.OpaqueReason(arg))
	}
	for pos := 1; pos < len(prefix); pos++ {
		action := sedOrdinaryOption
		requiresArg := false
		switch prefix[pos] {
		case 'e':
			action = sedInlineSource
			requiresArg = true
		case 'f':
			action = sedFileSource
			requiresArg = true
		case 'i', 'I':
			requiresArg = true
		case 'E', 'H', 'a', 'l', 'n', 'r', 'u':
		default:
			return i, false, nil
		}

		if !requiresArg {
			continue
		}
		if pos+1 < len(prefix) || !word.Static(arg) {
			value := sedAttachedValue(arg, pos+1)
			if value == nil {
				return i, false, fmt.Errorf(
					"sed option contains opaque syntax; cannot determine its value")
			}
			if err := addSedOptionValue(
				parsed, action, value, hasSourceOption,
			); err != nil {
				return i, false, err
			}
			return i, true, nil
		}
		if i+1 >= len(args) {
			return i, false, nil
		}
		i++
		if err := addSedOptionValue(
			parsed, action, args[i], hasSourceOption,
		); err != nil {
			return i, false, err
		}
		return i, true, nil
	}

	if !word.Static(arg) {
		return i, false, fmt.Errorf(
			"sed option contains %s; cannot determine which programs to scan",
			word.OpaqueReason(arg))
	}
	return i, true, nil
}

func sedAttachedValue(arg *syntax.Word, prefixLen int) *syntax.Word {
	if word.Static(arg) {
		text := word.Text(arg)
		if prefixLen > len(text) {
			return nil
		}
		return word.Lit(text[prefixLen:])
	}
	_, value := word.SplitPrefix(arg, prefixLen)
	return value
}

func addSedOptionValue(
	parsed *sedInterpretation,
	action sedOptionAction,
	value *syntax.Word,
	hasSourceOption *bool,
) error {
	if action == sedOrdinaryOption && value != nil &&
		(!sedWordExpandsAsOneField(value) ||
			word.HasUnquotedGlob(value)) {
		return fmt.Errorf(
			"sed option value can expand to multiple arguments; " +
				"quote it or use a static value")
	}

	switch action {
	case sedInlineSource:
		*hasSourceOption = true
		parsed.sources = append(parsed.sources, sedSource{word: value})
	case sedFileSource:
		*hasSourceOption = true
		parsed.sources = append(parsed.sources, sedSource{
			word: value,
			file: true,
		})
	case sedSandboxOption:
		parsed.sandboxed = true
	case sedInfoOption:
		parsed.infoOnly = true
	}
	return nil
}

func everySedInterpretation(
	interpretations []sedInterpretation,
	check func(sedInterpretation) bool,
) bool {
	for _, parsed := range interpretations {
		if !check(parsed) {
			return false
		}
	}
	return true
}

func sedOperandHasSafePrefix(operand *syntax.Word) bool {
	return word.DefinitelyHasPrefix(operand, "/") ||
		word.DefinitelyHasPrefix(operand, "./") ||
		word.DefinitelyHasPrefix(operand, "../")
}

func sedWordExpandsAsOneField(w *syntax.Word) bool {
	for _, part := range w.Parts {
		switch part := part.(type) {
		case *syntax.Lit, *syntax.SglQuoted:
		case *syntax.ArithmExp, *syntax.ProcSubst:
		case *syntax.DblQuoted:
			if sedDoubleQuoteExpandsAsManyFields(part) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func sedDoubleQuoteExpandsAsManyFields(quoted *syntax.DblQuoted) bool {
	manyFields := false
	syntax.Walk(quoted, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.ParamExp:
			if sedQuotedParamExpandsAsManyFields(node) {
				manyFields = true
				return false
			}
		case *syntax.ArithmExp, *syntax.CmdSubst, *syntax.ProcSubst:
			// Each boundary contributes one value to the surrounding quote. Its own
			// parameter expansions cannot split that outer value.
			return false
		}
		return !manyFields
	})
	return manyFields
}

func sedQuotedParamExpandsAsManyFields(param *syntax.ParamExp) bool {
	if param.Param != nil && param.Param.Value == "@" {
		return true
	}
	if param.Names == syntax.NamesPrefixWords {
		return true
	}
	index, ok := param.Index.(*syntax.Word)
	return ok && word.DefinitelyEqual(index, "@")
}

func sedStaticPrefix(w *syntax.Word) string {
	var prefix strings.Builder
	for _, part := range w.Parts {
		switch part := part.(type) {
		case *syntax.Lit:
			prefix.WriteString(word.UnescapeBackslashes(part.Value))
		case *syntax.SglQuoted:
			if part.Dollar {
				return prefix.String()
			}
			prefix.WriteString(part.Value)
		case *syntax.DblQuoted:
			for _, inner := range part.Parts {
				lit, ok := inner.(*syntax.Lit)
				if !ok {
					return prefix.String()
				}
				prefix.WriteString(word.UnescapeBackslashes(lit.Value))
			}
		default:
			return prefix.String()
		}
	}
	return prefix.String()
}

func sedProgramSnippets(
	sources []sedSource,
	cwd string,
) ([]model.CodeSnippet, error) {
	if len(sources) == 0 {
		return nil, nil
	}

	contents := make([]string, 0, len(sources))
	var sourceFiles []string
	hasInline := false
	for _, source := range sources {
		if !source.file {
			if !word.Static(source.word) ||
				word.HasUnquotedGlob(source.word) {
				reason := word.OpaqueReason(source.word)
				if reason == "" {
					reason = "pathname expansion"
				}
				return nil, &model.RuleError{
					Def: sedCommandExec,
					Reason: "sed program contains " + reason +
						" - use GNU sed --sandbox for " +
						"shell-expanded program text",
				}
			}
			hasInline = true
			contents = append(contents, word.Text(source.word))
			continue
		}

		code, path, err := readSedProgramFile(source.word, cwd)
		if err != nil {
			return nil, err
		}
		contents = append(contents, code)
		sourceFiles = append(sourceFiles, path)
	}

	var exact strings.Builder
	for i, content := range contents {
		if i > 0 && !sources[i-1].file {
			exact.WriteByte('\n')
		}
		exact.WriteString(content)
	}
	joined := strings.Join(contents, "")

	sourceFile := ""
	if !hasInline {
		sourceFile = strings.Join(sourceFiles, " + ")
	}
	exactCode := exact.String()
	snippets := []model.CodeSnippet{{
		Language:   model.LangSed,
		Code:       exactCode,
		SourceFile: sourceFile,
	}}
	if joined != exactCode {
		snippets = append(snippets, model.CodeSnippet{
			Language:   model.LangSed,
			Code:       joined,
			SourceFile: sourceFile,
		})
	}
	return snippets, nil
}

func readSedProgramFile(
	programFile *syntax.Word,
	cwd string,
) (string, string, error) {
	if !word.Static(programFile) || word.HasUnquotedGlob(programFile) {
		reason := word.OpaqueReason(programFile)
		if reason == "" {
			reason = "pathname expansion"
		}
		return "", "", &model.RuleError{
			Def: sedCommandExec,
			Reason: "sed script path contains " + reason +
				" - cannot determine which file to scan",
		}
	}

	path := word.Text(programFile)
	if path == "-" {
		return "", "", &model.RuleError{
			Def: sedCommandExec,
			Reason: "sed -f - reads a program from standard input " +
				"and cannot be verified",
		}
	}
	if cwd == "" && !filepath.IsAbs(path) {
		return "", "", &model.RuleError{
			Def: sedCommandExec,
			Reason: fmt.Sprintf(
				"%s: cannot verify file because the working "+
					"directory may have changed. Use an absolute path",
				path),
		}
	}

	data, err := model.ReadScript(path, cwd)
	if err != nil {
		return "", "", &model.RuleError{
			Def:    sedCommandExec,
			Reason: fmt.Sprintf("%s: %v", path, err),
		}
	}
	return string(data), path, nil
}

// sedCommandExecution matches the e command and e substitution flag. Both pass
// text to a shell.
func sedCommandExecution() matchBuilder {
	return matchBuilder{check: sedExecutesShell}
}

func sedExecutesShell(program string) bool {
	for i := 0; i < len(program); {
		i = skipSedCommandSpace(program, i)
		if i >= len(program) {
			return false
		}

		if program[i] == '#' {
			i = skipSedPhysicalLine(program, i+1)
			continue
		}

		i = skipSedAddresses(program, i)
		i = skipSedHorizontalSpace(program, i)
		if i < len(program) && program[i] == '!' {
			i++
			i = skipSedHorizontalSpace(program, i)
		}
		if i >= len(program) {
			return false
		}

		switch program[i] {
		case 'e':
			return true
		case 's':
			var dangerous bool
			i, dangerous = scanSedSubstitution(program, i+1)
			if dangerous {
				return true
			}
		case 'y':
			i = skipSedTransliteration(program, i+1)
		case 'a', 'c', 'i':
			i = skipSedLine(program, i+1)
		case 'r', 'R', 'w', 'W':
			i = skipSedPhysicalLine(program, i+1)
		case '{', '}':
			i++
		default:
			i = skipSedSimpleCommand(program, i+1)
		}
	}

	return false
}

func skipSedCommandSpace(program string, i int) int {
	for i < len(program) {
		switch program[i] {
		case ' ', '\t', '\r', '\n', ';', '{', '}':
			i++
		default:
			return i
		}
	}
	return i
}

func skipSedHorizontalSpace(program string, i int) int {
	for i < len(program) {
		switch program[i] {
		case ' ', '\t', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

func skipSedAddresses(program string, i int) int {
	if next, ok := skipSedAddress(program, i, false); ok {
		i = skipSedAddressModifiers(program, next)
		i = skipSedHorizontalSpace(program, i)
		if i < len(program) && program[i] == ',' {
			i = skipSedHorizontalSpace(program, i+1)
			if next, ok = skipSedAddress(program, i, true); ok {
				i = skipSedAddressModifiers(program, next)
			}
		}
	}
	return i
}

func skipSedAddressModifiers(program string, i int) int {
	for i < len(program) &&
		(program[i] == 'I' || program[i] == 'M') {
		i++
	}
	return i
}

func skipSedAddress(
	program string,
	i int,
	second bool,
) (int, bool) {
	if i >= len(program) {
		return i, false
	}

	switch {
	case program[i] >= '0' && program[i] <= '9':
		i = skipSedDigits(program, i)
		if i < len(program) && program[i] == '~' {
			i = skipSedDigits(program, i+1)
		}
		return i, true
	case program[i] == '$':
		return i + 1, true
	case program[i] == '/':
		return skipSedRegex(program, i+1, '/')
	case program[i] == '\\' && i+1 < len(program):
		return skipSedRegex(program, i+2, program[i+1])
	case second && (program[i] == '+' || program[i] == '~'):
		next := skipSedDigits(program, i+1)
		return next, next > i+1
	default:
		return i, false
	}
}

func skipSedDigits(program string, i int) int {
	for i < len(program) &&
		program[i] >= '0' && program[i] <= '9' {
		i++
	}
	return i
}

func skipSedRegex(
	program string,
	i int,
	delimiter byte,
) (int, bool) {
	inClass := false
	classCanClose := false
	for i < len(program) {
		if program[i] == delimiter && !inClass {
			return i + 1, true
		}

		switch program[i] {
		case '\\':
			i += 2
			if inClass {
				classCanClose = true
			}
		case '[':
			if !inClass {
				inClass = true
				classCanClose = false
				i++
				continue
			}
			if next, ok := skipSedBracketConstruct(program, i); ok {
				i = next
				classCanClose = true
				continue
			}
			classCanClose = true
			i++
		case ']':
			if inClass && classCanClose {
				inClass = false
			}
			classCanClose = true
			i++
		case '\n':
			return len(program), false
		default:
			if inClass && program[i] != '^' {
				classCanClose = true
			}
			i++
		}
	}
	return i, false
}

// skipSedBracketConstruct keeps POSIX character classes, collating symbols,
// and equivalence classes nested inside the surrounding bracket expression.
func skipSedBracketConstruct(
	program string,
	i int,
) (int, bool) {
	if i+1 >= len(program) ||
		(program[i+1] != ':' && program[i+1] != '.' &&
			program[i+1] != '=') {
		return i, false
	}

	marker := program[i+1]
	for i += 2; i+1 < len(program); i++ {
		if program[i] == marker && program[i+1] == ']' {
			return i + 2, true
		}
		if program[i] == '\\' && i+1 < len(program) {
			i++
		}
		if program[i] == '\n' {
			return i, false
		}
	}
	return i, false
}

func scanSedSubstitution(
	program string,
	i int,
) (int, bool) {
	if i >= len(program) || program[i] == '\n' {
		return len(program), false
	}
	delimiter := program[i]

	i, ok := skipSedRegex(program, i+1, delimiter)
	if !ok {
		return i, false
	}
	i, ok = skipSedDelimited(program, i, delimiter)
	if !ok {
		return i, false
	}

	for i < len(program) {
		switch program[i] {
		case 'e':
			return i + 1, true
		case 'w':
			return skipSedPhysicalLine(program, i+1), false
		case ';', '\n', '}':
			return i, false
		case '\\':
			i += 2
		default:
			i++
		}
	}
	return i, false
}

func skipSedDelimited(
	program string,
	i int,
	delimiter byte,
) (int, bool) {
	for i < len(program) {
		switch program[i] {
		case '\\':
			i += 2
		case delimiter:
			return i + 1, true
		case '\n':
			return len(program), false
		default:
			i++
		}
	}
	return i, false
}

func skipSedTransliteration(program string, i int) int {
	if i >= len(program) || program[i] == '\n' {
		return len(program)
	}
	delimiter := program[i]

	i, ok := skipSedDelimited(program, i+1, delimiter)
	if !ok {
		return i
	}
	i, _ = skipSedDelimited(program, i, delimiter)
	return i
}

func skipSedSimpleCommand(program string, i int) int {
	for i < len(program) {
		switch program[i] {
		case '\\':
			if i+1 < len(program) &&
				(program[i+1] == ';' || program[i+1] == '\n') {
				return i + 1
			}
			i += 2
		case ';', '\n', '}':
			return i
		default:
			i++
		}
	}
	return i
}

func skipSedLine(program string, i int) int {
	for i < len(program) {
		if program[i] == '\\' && i+1 < len(program) {
			i += 2
			continue
		}
		if program[i] == '\n' {
			return i + 1
		}
		i++
	}
	return i
}

func skipSedPhysicalLine(program string, i int) int {
	for i < len(program) {
		if program[i] == '\n' {
			return i + 1
		}
		i++
	}
	return i
}
