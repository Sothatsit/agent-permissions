// Package breakdown extracts executable commands from a bash
// command string using mvdan/sh's AST. It walks the entire
// tree, denying any node type it doesn't explicitly handle.
package breakdown

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// maxBreakdownDepth limits recursion when inner commands are
// re-parsed through breakdownAt (e.g. stacked wrappers, file
// scanning, source chains). Prevents stack overflow on
// adversarial input.
const maxBreakdownDepth = 20

// sourcePathSep is the separator used in SourcePath strings
// to delimit the chain of files (e.g. "outer.sh > inner.sh").
const sourcePathSep = " > "

type shellExpansion int

const (
	needsExpansion shellExpansion = iota
	expansionScanned
)

// breaker holds mutable state during a breakdown pass.
type breaker struct {
	model.State
	// registry is the command rules, used for
	// BreakdownFunc dispatch.
	registry map[string]*model.CommandRules
	// parser is reused across breakdownAt calls to avoid
	// re-allocating for each parse.
	parser *syntax.Parser
}

// saveCwd saves Cwd and CwdChanged and returns a restore
// function. Used at subshell boundaries (pipes, command
// subs, process subs) where cd shouldn't propagate out.
func (b *breaker) saveCwd() func() {
	saved, changed := b.Cwd, b.CwdChanged
	return func() { b.Cwd = saved; b.CwdChanged = changed }
}

// isolateForBash saves breaker state that should not leak
// from a bash sub-process (new process = fresh state), and
// returns a restore function. Cwd is inherited (subprocess
// starts in the parent's directory) but changes inside
// don't propagate out.
func (b *breaker) isolateForBash() func() {
	restoreCwd := b.saveCwd()
	savedFuncs := b.Funcs
	savedSawUnsetF := b.SawUnsetF
	savedRootScript := b.RootScript
	savedCondDepth := b.ConditionalDepth
	b.Funcs = make(map[string]bool)
	b.CwdChanged = false
	b.SawUnsetF = false
	b.RootScript = ""
	b.ConditionalDepth = 0
	return func() {
		restoreCwd()
		b.Funcs = savedFuncs
		b.SawUnsetF = savedSawUnsetF
		b.RootScript = savedRootScript
		b.ConditionalDepth = savedCondDepth
	}
}

// Breakdown parses a bash command string and returns all
// commands that could be executed. cwd is the working
// directory used to resolve relative paths in bash
// script.sh and source/. commands. registry provides
// command rules for BreakdownFunc dispatch. ruleConfig is the
// resolved per-rule configuration that breakdown functions
// consult before applying imperative denials; it must be
// populated (RuleConfigs.For panics on a nil map). Returns an
// error with a reason if any AST node is unrecognised.
func Breakdown(
	command string,
	cwd string,
	registry map[string]*model.CommandRules,
	ruleConfig model.RuleConfigs,
) (model.BreakdownResult, error) {
	b := &breaker{
		State: model.State{
			Cwd:        cwd,
			Visited:    make(map[string]bool),
			Funcs:      make(map[string]bool),
			RuleConfig: ruleConfig,
		},
		registry: registry,
		parser: syntax.NewParser(
			syntax.Variant(syntax.LangBash)),
	}
	return b.breakdownAt(command, 0)
}

func (b *breaker) breakdownAt(
	command string, depth int,
) (model.BreakdownResult, error) {
	if depth > maxBreakdownDepth {
		return model.BreakdownResult{}, fmt.Errorf(
			"nesting too deep (limit %d)", maxBreakdownDepth)
	}

	f, err := b.parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return model.BreakdownResult{}, fmt.Errorf(
			"parse error: %v", err)
	}

	result, err := b.processFile(f, depth)
	if err != nil {
		return model.BreakdownResult{}, err
	}

	result.Assigns = append(result.Assigns, collectAssigns(f)...)

	return result, nil
}

// sourcePathStr returns the current file path stack as a
// human-readable string for SourcePath metadata.
func (b *breaker) sourcePathStr() string {
	return strings.Join(b.FilePath, sourcePathSep)
}

// suppressDisabled reports whether a breakdown error should
// be swallowed because the rule that produced it is disabled.
// Rule-attributed denials carry a *model.RuleError with a
// Def; when that rule is off, the denial is dropped and the
// command falls through to the permissions layer. Plain
// errors (parse failures with no governing rule, unsupported
// syntax) carry no Def and are never suppressed — they fail
// closed.
func (b *breaker) suppressDisabled(err error) bool {
	var re *model.RuleError
	if errors.As(err, &re) && re.Def != nil {
		return !b.RuleConfig.For(re.Def).Enabled
	}
	return false
}

// runBreakdown calls the BreakdownFunc for a command and
// processes the UnwrapResult. Returns the inner commands
// extracted (if any) and whether the outer command was
// replaced (true = inner commands replace the outer;
// false = hook returned nil or KeepOuter was set, fall
// through to flattening).
func (b *breaker) runBreakdown(
	baseName string,
	cmdArgs []*syntax.Word,
	depth int,
	expansion shellExpansion,
) (model.BreakdownResult, bool, error) {
	rules := b.registry[baseName]

	// Parse args (without command name) using the
	// command's parser, or populate possible flags
	// heuristically. Parsers and breakdown functions
	// receive args without the command name in Raw,
	// but the command name is available via Name.
	args := cmdArgs[1:]
	input := model.ParseResult{
		Name: baseName,
		Raw:  args,
	}
	if rules.Parser != nil {
		parsed, err := rules.Parser.Parse(args)
		if err != nil {
			// A parser failure is this command's "cannot
			// verify" denial. Attribute it to the
			// command's Unverified rule so it both shows
			// the rule ID and suppresses when that rule
			// is disabled.
			var perr error = fmt.Errorf(
				"%s: %w", baseName, err)
			if rules.Unverified != nil {
				perr = &model.RuleError{
					Def: rules.Unverified,
					Reason: fmt.Sprintf(
						"%s: %v", baseName, err),
				}
			}
			if b.suppressDisabled(perr) {
				return model.BreakdownResult{},
					false, nil
			}
			return model.BreakdownResult{}, true, perr
		}
		parsed.Name = baseName
		input = parsed
	} else {
		model.PopulatePossibleFlags(&input)
	}

	// Call the BreakdownFunc.
	unwrapResult, err := rules.Breakdown(
		input, &b.State)
	if err != nil {
		// A breakdown denial attributed to a disabled rule
		// is dropped — the command falls through to the
		// permissions layer instead of being denied.
		if b.suppressDisabled(err) {
			return model.BreakdownResult{}, false, nil
		}
		return model.BreakdownResult{}, true, err
	}
	if unwrapResult == nil {
		// Hook declined to unwrap (e.g. cd just
		// mutated state). No inner commands.
		return model.BreakdownResult{}, false, nil
	}

	// Process the UnwrapResult. If the hook returned an
	// empty result (no commands, no files, no code), the
	// command is safe (e.g. command -v, trap -l, bare
	// xargs).
	var result model.BreakdownResult
	if len(unwrapResult.Commands) == 0 &&
		len(unwrapResult.CodeStrings) == 0 &&
		len(unwrapResult.ScanFiles) == 0 &&
		len(unwrapResult.CodeSnippets) == 0 {
		result.Safe = true
	}

	// Apply env-var assignments the wrapper sets on its inner
	// command (e.g. env NAME=val cmd): record each name on the
	// EnvVars deny axis and extract command substitutions from
	// each value, exactly as a leading assignment on a top-level
	// command would be handled.
	for _, assign := range unwrapResult.Assigns {
		if assign.Name != nil {
			result.Assigns = append(
				result.Assigns, assign.Name.Value)
		}
		if expansion == expansionScanned {
			continue
		}
		inner, assignErr := b.processAssign(
			assign, quotedTextArithmetic)
		if assignErr != nil {
			return model.BreakdownResult{}, true, assignErr
		}

		result.Merge(inner)
	}

	// ShellWords expand before a handled wrapper starts, so they always use
	// the outer working directory rather than a wrapper-scoped one.
	if expansion == needsExpansion {
		for _, shellWord := range unwrapResult.ShellWords {
			inner, shellErr := b.extractSubsFromWord(shellWord)
			if shellErr != nil {
				return model.BreakdownResult{}, true, shellErr
			}
			result.Merge(inner)
		}
	}

	innerExpansion := expansion
	if directory := unwrapResult.WorkingDirectory; directory != nil {
		if expansion == needsExpansion {
			inner, directoryErr := b.extractSubsFromWord(directory)
			if directoryErr != nil {
				return model.BreakdownResult{}, true, directoryErr
			}
			result.Merge(inner)
			for _, words := range unwrapResult.Commands {
				for _, commandWord := range words {
					inner, wordErr := b.extractSubsFromWord(commandWord)
					if wordErr != nil {
						return model.BreakdownResult{}, true, wordErr
					}
					result.Merge(inner)
				}
			}
		}

		restoreCwd := b.saveCwd()
		defer restoreCwd()
		b.CwdChanged = false
		b.SetWorkingDirectory(directory)
		innerExpansion = expansionScanned
	}

	// Process inner commands directly through the AST
	// walker — no print→reparse round trip.
	for _, words := range unwrapResult.Commands {
		call := &syntax.CallExpr{Args: words}
		var inner model.BreakdownResult
		var innerErr error
		if innerExpansion == expansionScanned {
			inner, innerErr =
				b.processCallExprWithScannedExpansion(call, depth)
		} else {
			inner, innerErr = b.processCallExpr(call, depth)
		}
		if innerErr != nil {
			return model.BreakdownResult{},
				true, innerErr
		}
		result.Merge(inner)
	}

	// Re-parse code strings (resolved text from
	// bash -c, eval, trap) through breakdownAt.
	for _, code := range unwrapResult.CodeStrings {
		inner, innerErr := b.breakdownAt(
			code, depth+1)
		if innerErr != nil {
			return model.BreakdownResult{},
				true, innerErr
		}
		result.Merge(inner)
	}

	// Transfer code snippets from unwrap result.
	// Set SourceScript to the full command args if the
	// breakdown function left it unset.
	for i := range unwrapResult.CodeSnippets {
		if unwrapResult.CodeSnippets[i].SourceScript == nil {
			unwrapResult.CodeSnippets[i].SourceScript =
				cmdArgs
		}
	}
	result.CodeSnippets = append(
		result.CodeSnippets,
		unwrapResult.CodeSnippets...)

	// Scan files with automatic isolation.
	for _, path := range unwrapResult.ScanFiles {
		restore := b.isolateForBash()
		b.RootScript = path
		inner, scanErr := b.scanFile(path, depth)
		restore()
		if scanErr != nil {
			return model.BreakdownResult{}, true,
				fmt.Errorf(
					"%s: %v. Fix the issue and "+
						"retry, or run the script "+
						"directly (%s)",
					path, scanErr,
					word.DirectPath(path))
		}
		result.Merge(inner)
	}

	return result, !unwrapResult.KeepOuter, nil
}

// scanFile reads a script file relative to cwd, parses it,
// and returns all extracted commands. Handles the visited set,
// depth limit, file size limit, and symlink resolution.
func (b *breaker) scanFile(
	path string, depth int,
) (model.BreakdownResult, error) {
	fullPath := path
	if !filepath.IsAbs(path) {
		fullPath = filepath.Join(b.Cwd, path)
	}

	// Resolve symlinks for accurate visited-set tracking.
	realPath, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		return model.BreakdownResult{}, err
	}

	// Already processed or in-progress — skip silently.
	// This handles circular source chains and diamond
	// patterns (A sources B and C, both source D).
	if b.Visited[realPath] {
		return model.BreakdownResult{Safe: true}, nil
	}

	data, err := model.ReadScript(realPath, "")
	if err != nil {
		return model.BreakdownResult{}, err
	}

	b.Visited[realPath] = true

	// Push file path for SourcePath tracking.
	b.FilePath = append(b.FilePath, path)
	defer func() {
		b.FilePath = b.FilePath[:len(b.FilePath)-1]
	}()

	result, err := b.breakdownAt(string(data), depth+1)
	if err != nil {
		return model.BreakdownResult{}, err
	}

	// A successfully scanned file with no commands is safe
	// (e.g. comments-only or empty file).
	if len(result.Commands) == 0 {
		result.Safe = true
	}

	return result, nil
}

// collectAssigns walks the AST and returns env var names from
// assignments that prefix a command (e.g. VAR=val cmd) and
// from export/local/declare statements (e.g. export VAR=val).
// The latter matter because they persist in compound commands
// (e.g. export BASH_ENV=/evil && cmd).
func collectAssigns(f *syntax.File) []string {
	var names []string
	syntax.Walk(f, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			for _, a := range n.Assigns {
				names = append(names, a.Name.Value)
			}
		case *syntax.DeclClause:
			for _, a := range n.Args {
				if a.Name != nil {
					names = append(names, a.Name.Value)
				}
			}
		}
		return true
	})
	return names
}

func (b *breaker) processFile(
	f *syntax.File, depth int,
) (model.BreakdownResult, error) {
	return b.processStmts(f.Stmts, depth)
}

func (b *breaker) processStmts(
	stmts []*syntax.Stmt, depth int,
) (model.BreakdownResult, error) {
	var result model.BreakdownResult
	for _, s := range stmts {
		r, err := b.processStmt(s, depth)
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Merge(r)
		// Statement boundary (semicolon / newline):
		// if a cd happened inside this statement, we
		// can't guarantee it succeeded at runtime, so
		// clear cwd for subsequent statements.
		if b.CwdChanged {
			b.Cwd = ""
			b.CwdChanged = false
		}
	}
	return result, nil
}

func (b *breaker) processStmt(
	stmt *syntax.Stmt, depth int,
) (model.BreakdownResult, error) {
	if stmt.Background {
		return model.BreakdownResult{}, unsupported(
			"background operator '&'")
	}
	if stmt.Coprocess {
		return model.BreakdownResult{}, unsupported("coproc")
	}

	// Redirections don't contribute to the command list but
	// can contain CmdSubst that must be extracted and checked.
	var result model.BreakdownResult
	for _, redir := range stmt.Redirs {
		inner, err := b.processRedirect(redir)
		if err != nil {
			return model.BreakdownResult{}, err
		}

		result.Merge(inner)
	}

	main, err := b.processCommand(stmt.Cmd, depth)
	if err != nil {
		return model.BreakdownResult{}, err
	}
	result.Merge(main)

	return result, nil
}

func (b *breaker) processCommand(
	cmd syntax.Command, depth int,
) (model.BreakdownResult, error) {
	switch c := cmd.(type) {
	case *syntax.CallExpr:
		return b.processCallExpr(c, depth)
	case *syntax.BinaryCmd:
		return b.processBinaryCmd(c, depth)
	case *syntax.Subshell:
		// Subshell runs in a child process — cd and
		// function definitions don't propagate out.
		// Not conditional (always executes), so don't
		// increment ConditionalDepth — that would
		// disable cd tracking inside the subshell.
		restoreCwd := b.saveCwd()
		savedFuncs := b.Funcs
		b.Funcs = make(map[string]bool)
		r, err := b.processStmts(c.Stmts, depth)
		b.Funcs = savedFuncs
		restoreCwd()
		return r, err
	case *syntax.Block:
		return b.processStmts(c.Stmts, depth)
	case *syntax.DeclClause:
		return b.processDeclClause(c)
	case *syntax.IfClause:
		return b.processIfClause(c, depth)
	case *syntax.WhileClause:
		return b.processWhileClause(c, depth)
	case *syntax.ForClause:
		return b.processForClause(c, depth)
	case *syntax.CaseClause:
		return b.processCaseClause(c, depth)
	case *syntax.TestClause:
		// [[ ... ]] — operands can contain command subs.
		result, err := b.extractSubsFromNode(c, quotedTextLiteral)
		if err != nil {
			return model.BreakdownResult{}, err
		}

		result.Safe = true
		return result, nil
	case *syntax.ArithmCmd:
		// (( ... )) — operands can contain command subs.
		result, err := b.extractSubsFromNode(
			c, quotedTextArithmetic)
		if err != nil {
			return model.BreakdownResult{}, err
		}

		result.Safe = true
		return result, nil
	case *syntax.TimeClause:
		// Bash's `time` keyword is a separate AST node, not a
		// command — the parser puts the timed statement in
		// TimeClause.Stmt. That statement carries its own
		// redirections and assignments; the keyword's only
		// option, -p, is absorbed into TimeClause.PosixFormat,
		// so the statement holds no time flags. Process the
		// statement directly through processStmt so its
		// redirections (e.g. > /dev/tcp/...) and the command
		// substitutions in their targets are checked — a
		// reconstructed bare CallExpr would silently drop them.
		// External /usr/bin/time is a normal CallExpr handled
		// by the `time` wrapper. Bare `time` is a safe no-op.
		if c.Stmt == nil {
			return model.BreakdownResult{Safe: true}, nil
		}
		return b.processStmt(c.Stmt, depth)
	case *syntax.FuncDecl:
		// Record the function name at unconditional scope
		// so calls to it can be recognised.
		if b.ConditionalDepth == 0 {
			b.Funcs[c.Name.Value] = true
		}
		// Function body is conditional scope — it only
		// runs when the function is called, not at
		// definition time. Nested function definitions
		// inside the body are not added to funcs.
		b.ConditionalDepth++
		r, err := b.processStmt(c.Body, depth)
		b.ConditionalDepth--
		return r, err
	default:
		return model.BreakdownResult{}, unsupported(
			nodeTypeName(cmd))
	}
}

func (b *breaker) processCallExpr(
	ce *syntax.CallExpr, depth int,
) (model.BreakdownResult, error) {
	return b.processCallExprWithExpansion(
		ce, depth, needsExpansion)
}

func (b *breaker) processCallExprWithScannedExpansion(
	ce *syntax.CallExpr, depth int,
) (model.BreakdownResult, error) {
	return b.processCallExprWithExpansion(
		ce, depth, expansionScanned)
}

func (b *breaker) processCallExprWithExpansion(
	ce *syntax.CallExpr,
	depth int,
	expansion shellExpansion,
) (model.BreakdownResult, error) {
	var result model.BreakdownResult

	// Collect findings from assignment substitutions
	// (e.g. FOO=$(evil) cmd -> extract "evil" for checking).
	for _, assign := range ce.Assigns {
		if expansion == expansionScanned {
			continue
		}
		inner, err := b.processAssign(
			assign, quotedTextArithmetic)
		if err != nil {
			return model.BreakdownResult{}, err
		}

		result.Merge(inner)
	}

	// No args means assignment-only (e.g. VAR=val).
	if len(ce.Args) == 0 {
		return result, nil
	}

	// Resolve command name from the first word.
	cmdName, err := resolveCommandName(ce.Args[0])
	if err != nil {
		return model.BreakdownResult{}, err
	}

	baseName := filepath.Base(cmdName)
	hasPath := strings.Contains(cmdName, "/")
	if !hasPath {
		inner, err := b.extractBuiltinArithmeticSubs(
			baseName, ce.Args[1:])
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Merge(inner)
	}

	// --- BreakdownFunc dispatch ---
	//
	// If the registry has a BreakdownFunc for this
	// command, call it to extract inner commands or
	// mutate breakdown state. When inner commands are
	// returned, they replace the outer command (the
	// outer is not emitted). When the hook returns nil,
	// the outer command falls through to flattening.
	//
	// Path-invoked commands (./cmd, /usr/bin/cmd) are
	// controlled by PathMode: Deny rejects them (a
	// local binary could ignore its arguments), Skip
	// bypasses the breakdown and falls through to
	// flattening, Allow runs the breakdown normally.
	if b.registry != nil {
		rules := b.registry[baseName]
		if rules != nil && rules.Breakdown != nil {
			skipBreakdown := false
			keepOuter := hasPath
			if hasPath {
				switch rules.PathMode {
				case model.PathDeny:
					return model.BreakdownResult{},
						fmt.Errorf(
							"%s: path-invoked "+
								"wrapper cannot "+
								"be verified "+
								"— use %s "+
								"instead",
							cmdName, baseName)
				case model.PathSkip:
					skipBreakdown = true
				}
			}
			if !skipBreakdown {
				inner, replaced, bdErr :=
					b.runBreakdown(
						baseName, ce.Args,
						depth, expansion)
				if bdErr != nil {
					return model.BreakdownResult{},
						bdErr
				}
				result.Merge(inner)
				if replaced && !keepOuter {
					return result, nil
				}
				// Hook returned nil — fall through
				// to flattening (e.g. cd updated
				// Cwd, bare bash falls to rules
				// deny).
			}
		}
	}

	// Extract command substitutions from args (e.g.
	// $(evil) inside an argument needs its own check).
	// Store the original Words on the Command — text
	// resolution happens lazily in the perms layer.
	for i, word := range ce.Args {
		if expansion == expansionScanned {
			break
		}
		if i == 0 {
			continue
		}
		inner, wordErr := b.extractSubsFromWord(word)
		if wordErr != nil {
			return model.BreakdownResult{}, wordErr
		}

		result.Merge(inner)
	}

	cmd := model.Command{Args: ce.Args}

	// Mark function calls so permissions can override
	// Fallback (but not deny/ask) for known functions.
	if b.Funcs[baseName] && !hasPath {
		if b.SawUnsetF {
			// The function was defined but unset -f was
			// seen — we can't verify which functions
			// still exist at runtime. Deny so the agent
			// can fix the script or use ./script.sh.
			return model.BreakdownResult{}, fmt.Errorf(
				"cannot verify call to %s — unset -f "+
					"was used and function may no "+
					"longer be defined. Remove the "+
					"unset or run the script "+
					"directly (%s)",
				baseName, word.DirectPath(b.RootScript))
		}
		cmd.CouldBeFuncCall = true
	}

	// Attach file context for commands from scanned files.
	if len(b.FilePath) > 0 {
		cmd.SourcePath = b.sourcePathStr()
		cmd.RootScript = b.RootScript
	}

	result.Commands = append(result.Commands, cmd)

	return result, nil
}

func (b *breaker) extractBuiltinArithmeticSubs(
	name string,
	args []*syntax.Word,
) (model.BreakdownResult, error) {
	var targets []*syntax.Word
	switch name {
	case "printf":
		if len(args) == 0 ||
			word.DefinitelyEqual(args[0], "--") {
			break
		}
		option, attached := word.SplitPrefix(args[0], 2)
		if option == "-v" {
			if attached != nil {
				targets = append(targets, attached)
			} else if len(args) > 1 {
				targets = append(targets, args[1])
			}
		}
	case "unset":
		for _, arg := range args {
			if word.DefinitelyEqual(arg, "--") {
				continue
			}
			if word.DefinitelyHasPrefix(arg, "-") {
				continue
			}
			targets = append(targets, arg)
		}
	case "read":
		targets = readBuiltinArithmeticTargets(args)
	default:
		return model.BreakdownResult{}, nil
	}

	var result model.BreakdownResult
	for _, target := range targets {
		text, err := arithmeticWordText(target)
		if err != nil {
			return model.BreakdownResult{}, err
		}
		if !hasArithmeticSubstitution(text) {
			continue
		}
		inner, err := b.extractArithmeticText(text)
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Merge(inner)
	}
	return result, nil
}

func arithmeticWordText(
	w *syntax.Word,
) (string, error) {
	var text strings.Builder
	for _, part := range w.Parts {
		if err := appendArithmeticWordPart(&text, part); err != nil {
			return "", err
		}
	}
	return text.String(), nil
}

func appendArithmeticWordPart(
	text *strings.Builder,
	part syntax.WordPart,
) error {
	switch part := part.(type) {
	case *syntax.Lit:
		text.WriteString(word.UnescapeBackslashes(part.Value))
	case *syntax.SglQuoted:
		value := part.Value
		if part.Dollar {
			var err error
			value, _, err = expand.Format(nil, value, nil)
			if err != nil {
				return err
			}
			value = strings.SplitN(value, "\x00", 2)[0]
		}
		text.WriteString(value)
	case *syntax.DblQuoted:
		for _, inner := range part.Parts {
			if err := appendArithmeticWordPart(text, inner); err != nil {
				return err
			}
		}
	default:
		// Runtime values can affect arithmetic, but tracking their contents
		// needs shell data flow. A neutral operand preserves explicit syntax
		// in the surrounding source without guessing the runtime value.
		text.WriteByte('0')
	}
	return nil
}

func readBuiltinArithmeticTargets(args []*syntax.Word) []*syntax.Word {
	var targets []*syntax.Word
	options := true
	for i := 0; i < len(args); i++ {
		if options && word.DefinitelyEqual(args[i], "--") {
			options = false
			continue
		}
		if options && readBuiltinOptionTakesValue(args[i]) {
			i++
			continue
		}
		if options && word.DefinitelyHasPrefix(args[i], "-") {
			continue
		}
		options = false
		targets = append(targets, args[i:]...)
		break
	}
	return targets
}

func readBuiltinOptionTakesValue(arg *syntax.Word) bool {
	for _, option := range []string{
		"-a", "-d", "-i", "-n", "-N", "-p", "-t", "-u",
	} {
		if word.DefinitelyEqual(arg, option) {
			return true
		}
	}
	return false
}

func (b *breaker) processBinaryCmd(
	bc *syntax.BinaryCmd, depth int,
) (model.BreakdownResult, error) {
	switch bc.Op {
	case syntax.Pipe, syntax.PipeAll:
		// Both sides always execute in subshells — cd
		// and function definitions don't propagate out.
		// Not conditional (both sides always run), so
		// don't increment ConditionalDepth.
		restoreCwd := b.saveCwd()
		savedFuncs := b.Funcs
		b.Funcs = make(map[string]bool)
		left, err := b.processStmt(bc.X, depth)
		b.Funcs = savedFuncs
		restoreCwd()
		if err != nil {
			return model.BreakdownResult{}, err
		}

		restoreCwd = b.saveCwd()
		savedFuncs = b.Funcs
		b.Funcs = make(map[string]bool)
		right, err := b.processStmt(bc.Y, depth)
		b.Funcs = savedFuncs
		restoreCwd()
		if err != nil {
			return model.BreakdownResult{}, err
		}

		left.Merge(right)
		return left, nil

	case syntax.AndStmt:
		// Left side runs in the parent shell. cd
		// effects propagate to the right side because
		// && guarantees the right only runs when the
		// left succeeded (so cd took effect).
		left, err := b.processStmt(bc.X, depth)
		if err != nil {
			return model.BreakdownResult{}, err
		}

		b.ConditionalDepth++
		right, err := b.processStmt(bc.Y, depth)
		b.ConditionalDepth--
		if err != nil {
			return model.BreakdownResult{}, err
		}

		left.Merge(right)
		return left, nil

	case syntax.OrStmt:
		// Right side only runs when left failed — if
		// left changed cwd, we can't trust it for the
		// right side.
		left, err := b.processStmt(bc.X, depth)
		if err != nil {
			return model.BreakdownResult{}, err
		}
		if b.CwdChanged {
			b.Cwd = ""
			b.CwdChanged = false
		}

		b.ConditionalDepth++
		right, err := b.processStmt(bc.Y, depth)
		b.ConditionalDepth--
		if err != nil {
			return model.BreakdownResult{}, err
		}

		left.Merge(right)
		return left, nil

	default:
		return model.BreakdownResult{}, unsupported(
			fmt.Sprintf("binary operator %v", bc.Op))
	}
}

func (b *breaker) processIfClause(
	ic *syntax.IfClause, depth int,
) (model.BreakdownResult, error) {
	// Walk all branches: condition, then, elif chains, else.
	// IfClause.Else is another IfClause (elif) or has empty
	// Cond (else). Recurse until nil.
	var result model.BreakdownResult
	for node := ic; node != nil; node = node.Else {
		// Conditions are unconditional (always evaluated).
		r, err := b.processStmts(node.Cond, depth)
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Merge(r)

		// Bodies are conditional.
		b.ConditionalDepth++
		r, err = b.processStmts(node.Then, depth)
		b.ConditionalDepth--
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Merge(r)
	}
	return result, nil
}

func (b *breaker) processWhileClause(
	wc *syntax.WhileClause, depth int,
) (model.BreakdownResult, error) {
	// Covers both while and until (WhileClause.Until = true).
	// Condition is unconditional, body is conditional.
	result, err := b.processStmts(wc.Cond, depth)
	if err != nil {
		return model.BreakdownResult{}, err
	}

	b.ConditionalDepth++
	body, err := b.processStmts(wc.Do, depth)
	b.ConditionalDepth--
	if err != nil {
		return model.BreakdownResult{}, err
	}
	result.Merge(body)
	return result, nil
}

func (b *breaker) processForClause(
	fc *syntax.ForClause, depth int,
) (model.BreakdownResult, error) {
	if fc.Select {
		return model.BreakdownResult{}, unsupported(
			"select (interactive)")
	}
	var result model.BreakdownResult
	// Check iteration words for command substitutions.
	if wi, ok := fc.Loop.(*syntax.WordIter); ok {
		for _, w := range wi.Items {
			inner, err := b.extractSubsFromWord(w)
			if err != nil {
				return model.BreakdownResult{}, err
			}

			result.Merge(inner)
		}
	}
	if loop, ok := fc.Loop.(*syntax.CStyleLoop); ok {
		inner, err := b.extractSubsFromNode(
			loop, quotedTextArithmetic)
		if err != nil {
			return model.BreakdownResult{}, err
		}

		result.Merge(inner)
	}

	// The body is conditional because the loop may not run.
	b.ConditionalDepth++
	body, err := b.processStmts(fc.Do, depth)
	b.ConditionalDepth--
	if err != nil {
		return model.BreakdownResult{}, err
	}
	result.Merge(body)
	return result, nil
}

func (b *breaker) processCaseClause(
	cc *syntax.CaseClause, depth int,
) (model.BreakdownResult, error) {
	var result model.BreakdownResult
	// Check the case word for command substitutions.
	if cc.Word != nil {
		inner, err := b.extractSubsFromWord(cc.Word)
		if err != nil {
			return model.BreakdownResult{}, err
		}

		result.Merge(inner)
	}
	// Check all arms — both patterns and bodies. Each arm
	// is conditional (only the matching arm executes).
	for _, item := range cc.Items {
		for _, pattern := range item.Patterns {
			inner, err := b.extractSubsFromWord(pattern)
			if err != nil {
				return model.BreakdownResult{}, err
			}

			result.Merge(inner)
		}
		b.ConditionalDepth++
		r, err := b.processStmts(item.Stmts, depth)
		b.ConditionalDepth--
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Merge(r)
	}
	return result, nil
}

// Some arithmetic contexts reparse text that the shell parser represented as a
// literal quote.
type quotedTextMode int

const (
	quotedTextLiteral quotedTextMode = iota
	quotedTextArithmetic
)

// extractSubsFromNode walks an AST node for command and process substitutions.
// Callers decide whether the surrounding construct is safe when none exist.
func (b *breaker) extractSubsFromNode(
	node syntax.Node,
	quoteMode quotedTextMode,
) (model.BreakdownResult, error) {
	var result model.BreakdownResult
	var walkErr error
	syntax.Walk(node, func(n syntax.Node) bool {
		if walkErr != nil {
			return false
		}
		switch c := n.(type) {
		case *syntax.ArithmExp:
			if quoteMode == quotedTextArithmetic {
				return true
			}
			inner, err := b.extractSubsFromNode(
				c, quotedTextArithmetic)
			if err != nil {
				walkErr = err
				return false
			}
			result.Merge(inner)
			return false
		case *syntax.ParamExp:
			inner, err := b.extractSubsFromParamExp(c)
			if err != nil {
				walkErr = err
				return false
			}
			result.Merge(inner)
			return false
		case *syntax.Word:
			if quoteMode != quotedTextArithmetic {
				return true
			}
			text, err := arithmeticWordText(c)
			if err != nil {
				walkErr = err
				return false
			}
			if hasArithmeticSubstitution(text) {
				inner, err := b.extractArithmeticText(text)
				if err != nil {
					walkErr = err
					return false
				}
				result.Merge(inner)
			}
			return true
		case *syntax.SglQuoted:
			// Arithmetic Words are scanned as one runtime string so adjacent
			// quotes cannot split a substitution token across AST parts.
			return false
		case *syntax.CmdSubst:
			// Command substitutions run in a subshell —
			// cd doesn't propagate out.
			restoreCwd := b.saveCwd()
			inner, err := b.processStmts(c.Stmts, 0)
			restoreCwd()
			if err != nil {
				walkErr = err
				return false
			}
			result.Merge(inner)
			return false
		case *syntax.ProcSubst:
			// Process substitutions run in a subshell
			// — cd doesn't propagate out.
			restoreCwd := b.saveCwd()
			inner, err := b.processStmts(c.Stmts, 0)
			restoreCwd()
			if err != nil {
				walkErr = err
				return false
			}
			result.Merge(inner)
			return false
		}
		return true
	})
	if walkErr != nil {
		return model.BreakdownResult{}, walkErr
	}
	return result, nil
}

func hasArithmeticSubstitution(text string) bool {
	return strings.Contains(text, "$(") ||
		strings.Contains(text, "`") ||
		strings.Contains(text, "<(") ||
		strings.Contains(text, ">(")
}

func (b *breaker) extractArithmeticText(
	text string,
) (model.BreakdownResult, error) {
	file, err := b.parser.Parse(strings.NewReader(
		"(( "+text+" ))"), "")
	if err != nil {
		return model.BreakdownResult{}, fmt.Errorf(
			"cannot verify quoted arithmetic: %v", err)
	}
	return b.extractSubsFromNode(file, quotedTextArithmetic)
}

func (b *breaker) processDeclClause(
	dc *syntax.DeclClause,
) (model.BreakdownResult, error) {
	// Bash reparses values assigned under the integer attribute as arithmetic,
	// even when shell quotes hid executable syntax from the first parse.
	var result model.BreakdownResult
	integerValues := false
	associativeValues := false
	variant := ""
	if dc.Variant != nil {
		variant = dc.Variant.Value
	}
	reparsesAssignments := variant == "declare" ||
		variant == "local" || variant == "typeset"
	assignmentMode := reparsesAssignments
	options := true
	for _, assign := range dc.Args {
		if options && assign.Naked && assign.Value != nil &&
			word.Static(assign.Value) {
			option := word.Text(assign.Value)
			if option == "--" {
				options = false
				continue
			}
			if len(option) > 1 &&
				(option[0] == '-' || option[0] == '+') {
				if strings.ContainsAny(option[1:], "fF") {
					assignmentMode = false
				}
				if variant != "local" &&
					strings.Contains(option[1:], "p") {
					assignmentMode = false
				}
				if reparsesAssignments &&
					strings.Contains(option[1:], "i") {
					integerValues = option[0] == '-'
				}
				if strings.Contains(option[1:], "A") {
					associativeValues = option[0] == '-'
				} else if strings.Contains(option[1:], "a") {
					associativeValues = false
				}
				continue
			}
		}
		options = false
		indexMode := quotedTextLiteral
		if assignmentMode && !associativeValues {
			indexMode = quotedTextArithmetic
		}
		inner, err := b.processAssign(assign, indexMode)
		if err != nil {
			return model.BreakdownResult{}, err
		}

		result.Merge(inner)
		if assignmentMode && associativeValues &&
			assign.Index != nil {
			inner, err = b.extractSubsFromNode(
				assign.Index, quotedTextArithmetic)
			if err != nil {
				return model.BreakdownResult{}, err
			}
			result.Merge(inner)
		}
		if assignmentMode && assign.Naked &&
			assign.Value != nil {
			inner, err = b.extractRuntimeDeclAssignmentSubs(
				assign.Value, integerValues)
			if err != nil {
				return model.BreakdownResult{}, err
			}
			result.Merge(inner)
		}
		if assignmentMode && integerValues && !assign.Naked &&
			assign.Value != nil {
			text, textErr := arithmeticWordText(assign.Value)
			if textErr != nil {
				return model.BreakdownResult{}, textErr
			}
			inner, err = b.extractArithmeticText(text)
			if err != nil {
				return model.BreakdownResult{}, err
			}
			result.Merge(inner)
		}
	}
	return result, nil
}

func (b *breaker) extractRuntimeDeclAssignmentSubs(
	value *syntax.Word,
	integerValue bool,
) (model.BreakdownResult, error) {
	text, err := arithmeticWordText(value)
	if err != nil {
		return model.BreakdownResult{}, err
	}
	assign, lhs, rhs, assigned := parseRuntimeDeclAssignment(
		b.parser, text)
	if !assigned {
		return model.BreakdownResult{}, nil
	}

	var result model.BreakdownResult
	if assign != nil && assign.Index != nil {
		inner, err := b.extractSubsFromNode(
			assign.Index, quotedTextArithmetic)
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Merge(inner)
	} else if assign == nil && hasArithmeticSubstitution(lhs) {
		return model.BreakdownResult{}, fmt.Errorf(
			"cannot verify quoted declaration assignment index")
	}
	if integerValue && hasArithmeticSubstitution(rhs) {
		inner, err := b.extractArithmeticText(rhs)
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Merge(inner)
	}
	return result, nil
}

func parseRuntimeDeclAssignment(
	parser *syntax.Parser,
	text string,
) (*syntax.Assign, string, string, bool) {
	for offset := 0; offset < len(text); {
		relative := strings.IndexByte(text[offset:], '=')
		if relative < 0 {
			break
		}
		assignAt := offset + relative
		lhs := text[:assignAt]
		if strings.HasSuffix(lhs, "+") {
			lhs = strings.TrimSuffix(lhs, "+")
		}
		file, err := parser.Parse(strings.NewReader(
			"x"+lhs+"=0"), "")
		if err == nil && len(file.Stmts) == 1 {
			call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
			if ok && len(call.Assigns) == 1 &&
				len(call.Args) == 0 {
				return call.Assigns[0], lhs,
					text[assignAt+1:], true
			}
		}
		offset = assignAt + 1
	}
	assignAt := runtimeDeclAssignmentSeparator(text)
	if assignAt < 0 {
		return nil, "", "", false
	}
	lhs := text[:assignAt]
	if strings.HasSuffix(lhs, "+") {
		lhs = strings.TrimSuffix(lhs, "+")
	}
	return nil, lhs, text[assignAt+1:], true
}

func runtimeDeclAssignmentSeparator(text string) int {
	brackets := 0
	parentheses := 0
	var quote byte
	escaped := false
	for i := 0; i < len(text); i++ {
		char := text[i]
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			continue
		}
		if char == '\'' || char == '"' || char == '`' {
			quote = char
			continue
		}
		switch char {
		case '(':
			parentheses++
		case ')':
			if parentheses > 0 {
				parentheses--
			}
		case '[':
			if parentheses == 0 {
				brackets++
			}
		case ']':
			if parentheses == 0 && brackets > 0 {
				brackets--
			}
		case '=':
			if parentheses == 0 && brackets == 0 {
				return i
			}
		}
	}
	return -1
}

func (b *breaker) processAssign(
	assign *syntax.Assign,
	indexMode quotedTextMode,
) (model.BreakdownResult, error) {
	var result model.BreakdownResult
	if assign.Index != nil {
		inner, err := b.extractSubsFromNode(
			assign.Index, indexMode)
		if err != nil {
			return model.BreakdownResult{}, err
		}

		result.Merge(inner)
	}

	if assign.Value != nil {
		inner, err := b.extractSubsFromWord(
			assign.Value)
		if err != nil {
			return model.BreakdownResult{}, err
		}

		result.Merge(inner)
	}

	if assign.Array != nil {
		for _, elem := range assign.Array.Elems {
			if elem.Index != nil {
				inner, err := b.extractSubsFromNode(
					elem.Index, indexMode)
				if err != nil {
					return model.BreakdownResult{}, err
				}

				result.Merge(inner)
			}

			if elem.Value != nil {
				inner, err :=
					b.extractSubsFromWord(
						elem.Value)
				if err != nil {
					return model.BreakdownResult{}, err
				}

				result.Merge(inner)
			}
		}
	}

	return result, nil
}

func (b *breaker) processRedirect(
	redir *syntax.Redirect,
) (model.BreakdownResult, error) {
	var result model.BreakdownResult

	if redir.Word != nil {
		if err := checkNetworkRedirect(
			redir.Word); err != nil {
			return model.BreakdownResult{}, err
		}
		inner, err := b.extractSubsFromWord(
			redir.Word)
		if err != nil {
			return model.BreakdownResult{}, err
		}

		result.Merge(inner)
	}

	if redir.Hdoc != nil {
		inner, err := b.extractSubsFromWord(
			redir.Hdoc)
		if err != nil {
			return model.BreakdownResult{}, err
		}

		result.Merge(inner)
	}

	return result, nil
}

// checkNetworkRedirect denies bash network redirections
// via /dev/tcp/host/port and /dev/udp/host/port. These
// are bash built-ins that open network sockets, bypassing
// curl/wget/ssh deny rules. Strict: opaque targets (e.g.
// > "$outfile") are allowed — these paths are always
// written literally.
func checkNetworkRedirect(w *syntax.Word) error {
	if word.DefinitelyHasPrefix(w, "/dev/tcp/") ||
		word.DefinitelyHasPrefix(w, "/dev/udp/") {
		return fmt.Errorf(
			"network redirection via %s — "+
				"use curl/wget instead",
			word.Text(w))
	}
	return nil
}

// resolveCommandName extracts the command name from the first
// word of a CallExpr. Returns an error if the command name
// contains substitutions or parameter expansions (unknowable).
func resolveCommandName(w *syntax.Word) (string, error) {
	var name strings.Builder

	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			name.WriteString(p.Value)
		case *syntax.SglQuoted:
			if p.Dollar {
				return "", fmt.Errorf(
					"command name contains ANSI-C " +
						"quoting ($'...') — cannot " +
						"determine what will run")
			}
			name.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				switch ip := inner.(type) {
				case *syntax.Lit:
					name.WriteString(ip.Value)
				case *syntax.CmdSubst:
					return "", fmt.Errorf(
						"command name contains " +
							"substitution — cannot " +
							"determine what will run")
				case *syntax.ParamExp:
					return "", fmt.Errorf(
						"command name contains " +
							"variable expansion — " +
							"cannot determine what " +
							"will run")
				default:
					return "", unsupported(
						fmt.Sprintf(
							"%s in command name",
							nodeTypeName(inner)))
				}
			}
		case *syntax.CmdSubst:
			return "", fmt.Errorf(
				"command name is a substitution — " +
					"cannot determine what will run")
		case *syntax.ParamExp:
			return "", fmt.Errorf(
				"command name contains variable " +
					"expansion — cannot determine " +
					"what will run")
		default:
			return "", unsupported(
				fmt.Sprintf("%s in command name",
					nodeTypeName(part)))
		}
	}

	result := word.UnescapeBackslashes(name.String())

	// Reject glob characters in command names — they expand
	// unpredictably. Exception: bare "[" is the test builtin.
	if result != "[" && strings.ContainsAny(result, "*?[") {
		return "", fmt.Errorf(
			"command name contains glob characters — " +
				"cannot determine what will run")
	}

	return result, nil
}

// extractSubsFromWord walks a Word for command substitutions
// and returns every finding from their nested breakdowns.
func (b *breaker) extractSubsFromWord(
	w *syntax.Word,
) (model.BreakdownResult, error) {
	var result model.BreakdownResult
	for _, part := range w.Parts {
		inner, err := b.extractSubsFromPart(part)
		if err != nil {
			return model.BreakdownResult{}, err
		}

		result.Merge(inner)
	}

	return result, nil
}

func (b *breaker) extractSubsFromPart(
	part syntax.WordPart,
) (model.BreakdownResult, error) {
	switch p := part.(type) {
	case *syntax.Lit, *syntax.SglQuoted:
		return model.BreakdownResult{}, nil
	case *syntax.DblQuoted:
		var result model.BreakdownResult
		for _, inner := range p.Parts {
			innerResult, err := b.extractSubsFromPart(inner)
			if err != nil {
				return model.BreakdownResult{}, err
			}

			result.Merge(innerResult)
		}

		return result, nil
	case *syntax.CmdSubst:
		restoreCwd := b.saveCwd()
		innerResult, err := b.processStmts(
			p.Stmts, 0)
		restoreCwd()
		if err != nil {
			return model.BreakdownResult{}, err
		}

		return innerResult, nil
	case *syntax.ParamExp:
		return b.extractSubsFromParamExp(p)
	case *syntax.ArithmExp:
		return b.extractSubsFromNode(p, quotedTextArithmetic)
	case *syntax.ProcSubst:
		restoreCwd := b.saveCwd()
		innerResult, err := b.processStmts(
			p.Stmts, 0)
		restoreCwd()
		if err != nil {
			return model.BreakdownResult{}, err
		}

		return innerResult, nil
	case *syntax.ExtGlob:
		return model.BreakdownResult{}, unsupported(
			"extended glob")
	case *syntax.BraceExp:
		return model.BreakdownResult{}, unsupported(
			"brace expansion")
	default:
		return model.BreakdownResult{}, unsupported(
			nodeTypeName(part))
	}
}

func (b *breaker) extractSubsFromParamExp(
	param *syntax.ParamExp,
) (model.BreakdownResult, error) {
	var result model.BreakdownResult
	if param.NestedParam != nil {
		inner, err := b.extractSubsFromPart(param.NestedParam)
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Merge(inner)
	}
	if param.Index != nil {
		inner, err := b.extractSubsFromNode(
			param.Index, quotedTextArithmetic)
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Merge(inner)
	}
	if param.Slice != nil {
		if param.Slice.Offset != nil {
			inner, err := b.extractSubsFromNode(
				param.Slice.Offset, quotedTextArithmetic)
			if err != nil {
				return model.BreakdownResult{}, err
			}
			result.Merge(inner)
		}
		if param.Slice.Length != nil {
			inner, err := b.extractSubsFromNode(
				param.Slice.Length, quotedTextArithmetic)
			if err != nil {
				return model.BreakdownResult{}, err
			}
			result.Merge(inner)
		}
	}
	if param.Repl != nil {
		inner, err := b.extractSubsFromWords(
			param.Repl.Orig, param.Repl.With)
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Merge(inner)
	}
	if param.Exp != nil && param.Exp.Word != nil {
		inner, err := b.extractSubsFromWord(param.Exp.Word)
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Merge(inner)
	}
	return result, nil
}

func (b *breaker) extractSubsFromWords(
	words ...*syntax.Word,
) (model.BreakdownResult, error) {
	var result model.BreakdownResult
	for _, current := range words {
		if current == nil {
			continue
		}
		inner, err := b.extractSubsFromWord(current)
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Merge(inner)
	}
	return result, nil
}

func unsupported(what string) error {
	return fmt.Errorf("unsupported: %s", what)
}

func nodeTypeName(node syntax.Node) string {
	return fmt.Sprintf("%T", node)
}
