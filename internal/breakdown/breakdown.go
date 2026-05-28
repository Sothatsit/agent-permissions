// Package breakdown extracts executable commands from a bash
// command string using mvdan/sh's AST. It walks the entire
// tree, denying any node type it doesn't explicitly handle.
package breakdown

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"

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
// command rules for BreakdownFunc dispatch. Returns an
// error with a reason if any AST node is unrecognised.
func Breakdown(
	command string,
	cwd string,
	registry map[string]*model.CommandRules,
) (model.BreakdownResult, error) {
	b := &breaker{
		State: model.State{
			Cwd:     cwd,
			Visited: make(map[string]bool),
			Funcs:   make(map[string]bool),
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
			return model.BreakdownResult{}, true,
				fmt.Errorf("%s: %w", baseName, err)
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

	// Process inner commands directly through the AST
	// walker — no print→reparse round trip.
	for _, words := range unwrapResult.Commands {
		inner, innerErr := b.processCallExpr(
			&syntax.CallExpr{Args: words}, depth)
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
		c, err := b.processRedirect(redir)
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Commands = append(result.Commands, c...)
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
		return b.extractSubsFromNode(c)
	case *syntax.ArithmCmd:
		// (( ... )) — operands can contain command subs.
		return b.extractSubsFromNode(c)
	case *syntax.TimeClause:
		// Bash's `time` keyword is a separate AST node,
		// not a command — the parser puts the timed
		// statement in TimeClause.Stmt. External
		// /usr/bin/time appears as a normal CallExpr
		// and is handled by the `time` wrapper in the
		// registry.
		//
		// We transform the keyword form into a
		// synthetic CallExpr with "time" prepended so
		// it routes through processCallExpr and the
		// registry's `time` BreakdownFunc. This lets
		// both forms share the same flag parsing and
		// inner-command extraction logic — without it
		// we'd need to duplicate the flag table here.
		//
		// Bare `time` or `time` with no inner CallExpr
		// is a safe no-op.
		if c.Stmt == nil {
			return model.BreakdownResult{Safe: true}, nil
		}
		inner, ok := c.Stmt.Cmd.(*syntax.CallExpr)
		if !ok || len(inner.Args) == 0 {
			return b.processStmt(c.Stmt, depth)
		}
		timeLit := &syntax.Word{Parts: []syntax.WordPart{
			&syntax.Lit{Value: "time"},
		}}
		synthetic := *inner
		synthetic.Args = append(
			[]*syntax.Word{timeLit}, inner.Args...)
		return b.processCallExpr(&synthetic, depth)
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
	var result model.BreakdownResult

	// Collect commands from assignment substitutions
	// (e.g. FOO=$(evil) cmd → extract "evil" for checking).
	for _, assign := range ce.Assigns {
		c, err := b.processAssign(assign)
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Commands = append(result.Commands, c...)
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
						depth)
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
		if i == 0 {
			continue
		}
		subs, wordErr := b.extractSubsFromWord(word)
		if wordErr != nil {
			return model.BreakdownResult{}, wordErr
		}
		result.Commands = append(
			result.Commands, subs...)
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
			subs, err := b.extractSubsFromWord(w)
			if err != nil {
				return model.BreakdownResult{}, err
			}
			result.Commands = append(
				result.Commands, subs...)
		}
	}
	// CStyleLoop is pure arithmetic — no commands to
	// extract. Body is conditional (may not execute if
	// list is empty).
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
		subs, err := b.extractSubsFromWord(cc.Word)
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Commands = append(result.Commands, subs...)
	}
	// Check all arms — both patterns and bodies. Each arm
	// is conditional (only the matching arm executes).
	for _, item := range cc.Items {
		for _, pattern := range item.Patterns {
			subs, err := b.extractSubsFromWord(pattern)
			if err != nil {
				return model.BreakdownResult{}, err
			}
			result.Commands = append(
				result.Commands, subs...)
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

// extractSubsFromNode walks an AST node looking for command
// substitutions. Used for nodes that don't contain commands
// themselves but whose operands can (TestClause, ArithmCmd).
// Returns Safe=true when no commands are found.
func (b *breaker) extractSubsFromNode(
	node syntax.Node,
) (model.BreakdownResult, error) {
	var result model.BreakdownResult
	result.Safe = true
	var walkErr error
	syntax.Walk(node, func(n syntax.Node) bool {
		if walkErr != nil {
			return false
		}
		switch c := n.(type) {
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
			result.Commands = append(
				result.Commands, inner.Commands...)
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
			result.Commands = append(
				result.Commands, inner.Commands...)
			return false
		}
		return true
	})
	if walkErr != nil {
		return model.BreakdownResult{}, walkErr
	}
	return result, nil
}

func (b *breaker) processDeclClause(
	dc *syntax.DeclClause,
) (model.BreakdownResult, error) {
	// export, local, declare, readonly — treat like
	// assignments.
	var result model.BreakdownResult
	for _, assign := range dc.Args {
		c, err := b.processAssign(assign)
		if err != nil {
			return model.BreakdownResult{}, err
		}
		result.Commands = append(result.Commands, c...)
	}
	return result, nil
}

func (b *breaker) processAssign(
	assign *syntax.Assign,
) ([]model.Command, error) {
	var cmds []model.Command
	if assign.Value != nil {
		subs, err := b.extractSubsFromWord(
			assign.Value)
		if err != nil {
			return nil, err
		}
		cmds = append(cmds, subs...)
	}
	if assign.Array != nil {
		for _, elem := range assign.Array.Elems {
			if elem.Value != nil {
				subs, err :=
					b.extractSubsFromWord(
						elem.Value)
				if err != nil {
					return nil, err
				}
				cmds = append(cmds, subs...)
			}
		}
	}
	return cmds, nil
}

func (b *breaker) processRedirect(
	redir *syntax.Redirect,
) ([]model.Command, error) {
	var cmds []model.Command

	if redir.Word != nil {
		if err := checkNetworkRedirect(
			redir.Word); err != nil {
			return nil, err
		}
		subs, err := b.extractSubsFromWord(
			redir.Word)
		if err != nil {
			return nil, err
		}
		cmds = append(cmds, subs...)
	}

	if redir.Hdoc != nil {
		subs, err := b.extractSubsFromWord(
			redir.Hdoc)
		if err != nil {
			return nil, err
		}
		cmds = append(cmds, subs...)
	}

	return cmds, nil
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

// extractSubsFromWord walks a Word for command
// substitutions and returns the commands found. Unlike
// resolveWord, it does not build the resolved text.
func (b *breaker) extractSubsFromWord(
	w *syntax.Word,
) ([]model.Command, error) {
	var subs []model.Command
	for _, part := range w.Parts {
		s, err := b.extractSubsFromPart(part)
		if err != nil {
			return nil, err
		}
		subs = append(subs, s...)
	}
	return subs, nil
}

func (b *breaker) extractSubsFromPart(
	part syntax.WordPart,
) ([]model.Command, error) {
	switch p := part.(type) {
	case *syntax.Lit, *syntax.SglQuoted:
		return nil, nil
	case *syntax.DblQuoted:
		var subs []model.Command
		for _, inner := range p.Parts {
			s, err := b.extractSubsFromPart(inner)
			if err != nil {
				return nil, err
			}
			subs = append(subs, s...)
		}
		return subs, nil
	case *syntax.CmdSubst:
		restoreCwd := b.saveCwd()
		innerResult, err := b.processStmts(
			p.Stmts, 0)
		restoreCwd()
		if err != nil {
			return nil, err
		}
		return innerResult.Commands, nil
	case *syntax.ParamExp:
		return b.walkParamExpForSubs(p)
	case *syntax.ArithmExp:
		return nil, unsupported(
			"arithmetic expansion $(())")
	case *syntax.ProcSubst:
		restoreCwd := b.saveCwd()
		innerResult, err := b.processStmts(
			p.Stmts, 0)
		restoreCwd()
		if err != nil {
			return nil, err
		}
		return innerResult.Commands, nil
	case *syntax.ExtGlob:
		return nil, unsupported("extended glob")
	case *syntax.BraceExp:
		return nil, unsupported("brace expansion")
	default:
		return nil, unsupported(nodeTypeName(part))
	}
}

// walkParamExpForSubs walks a ParamExp's sub-expressions
// looking for CmdSubst. Returns any commands found.
func (b *breaker) walkParamExpForSubs(
	pe *syntax.ParamExp,
) ([]model.Command, error) {
	var cmds []model.Command

	// Walk all child nodes looking for CmdSubst.
	var walkErr error
	syntax.Walk(pe, func(node syntax.Node) bool {
		if walkErr != nil {
			return false
		}
		switch n := node.(type) {
		case *syntax.CmdSubst:
			restoreCwd := b.saveCwd()
			innerResult, err := b.processStmts(
				n.Stmts, 0)
			restoreCwd()
			if err != nil {
				walkErr = err
				return false
			}
			cmds = append(cmds, innerResult.Commands...)
			return false
		case *syntax.ArithmExp:
			walkErr = unsupported(
				"arithmetic expansion inside " +
					"parameter expansion")
			return false
		}
		return true
	})

	if walkErr != nil {
		return nil, walkErr
	}
	return cmds, nil
}

func unsupported(what string) error {
	return fmt.Errorf("unsupported: %s", what)
}

func nodeTypeName(node syntax.Node) string {
	return fmt.Sprintf("%T", node)
}
