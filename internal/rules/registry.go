package rules

import "github.com/sothatsit/agent-permissions/internal/model"

// Command rules come in two styles:
//
// Guarded: no Default. Rules only fire for specific
// dangerous flags or subcommands; everything else falls
// through to the permissions layer (Allow/Ask patterns).
// Used for commands like git/tar/sed where most
// invocations are safe and handled by patterns.
//
// Managed: has a Default. The rules layer must produce
// a decision for every invocation; nothing falls through.
// Used for commands like bash/sh where no invocation
// should be silently allowed.

// Registry returns the command rules and snippet rules.
// Command rules are keyed by command name (git, python3,
// etc.). Snippet rules are keyed by language (python,
// perl, etc.) — a language may have multiple commands
// (python3 and python both produce Python snippets).
func Registry() (
	map[string]*model.CommandRules,
	map[string]*model.SnippetLang,
) {
	r := make(map[string]*model.CommandRules)

	// --- Guarded: deny specific dangerous flags ---

	// git: strip -C <path> in breakdown so permission
	// patterns match plain git <subcommand>. Deny flags
	// that open editors, execute arbitrary commands, or
	// inject config.
	r["git"] = &model.CommandRules{
		Breakdown: breakdownGit,
		PathMode:  model.PathSkip,
		Rules: []model.Rule{
			model.Flag("-e", "--edit").
				WithRuleDef(gitInteractive).Deny(
				"interactive"),
			model.Flag("--upload-pack",
				"--receive-pack",
				"--open-files-in-pager",
			).WithRuleDef(gitCommandExec).Deny(
				"can execute arbitrary commands"),
			model.Flag("-c").
				WithRuleDef(gitConfigInject).
				ValueCouldContain("=").Deny(
				"can execute arbitrary commands " +
					"via hooks/editor/pager " +
					"config — use git config " +
					"instead"),
			// git branch/tag: the subcommand carries the
			// def and a DefaultDeny safety net; the hook
			// (under it, governed by the same def) does the
			// classification. classifyGit* always decides,
			// so the default only fires if that ever changes.
			model.Subcmd("branch").
				WithRuleDef(gitBranchWrites).DefaultDeny(
				"unrecognised flag").Rules(
				model.Always().Hook(classifyGitBranch),
			),
			model.Subcmd("tag").
				WithRuleDef(gitTagWrites).DefaultDeny(
				"unrecognised flag").Rules(
				model.Always().Hook(classifyGitTag),
			),
			model.Subcmd("remote").DefaultAllow(
				"read-only").Rules(
				model.Subcmd("add", "rename",
					"remove", "rm", "set-head",
					"set-branches", "set-url",
					"prune", "update",
				).WithRuleDef(gitRemoteWrites).SoftAsk(
					"write operation"),
			),
		},
	}

	// gh: classify gh api by HTTP method.
	r["gh"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Subcmd("api").
				WithRuleDef(ghAPIWrites).DefaultDeny(
				"unrecognised flag").Rules(
				model.Always().Hook(classifyGhApi),
			),
		},
	}

	// tar: deny flags that execute external programs.
	r["tar"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Flag("--to-command",
				"--use-compress-program",
				"--rsh-command", "--rmt-command",
				"--info-script",
			).WithRuleDef(tarCommandExec).Deny(
				"can execute arbitrary commands"),
			model.Flag(
				"--checkpoint-action",
			).WithRuleDef(tarCommandExec).
				ValueMayHavePrefix(
					"exec=",
				).Deny("can execute arbitrary commands"),
			model.Flag("-I").
				WithRuleDef(tarCommandExec).Deny(
				"can execute arbitrary commands"),
		},
	}

	// sort: --compress-program runs an external command.
	r["sort"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Flag("--compress-program").
				WithRuleDef(sortCommandExec).Deny(
				"can execute arbitrary commands"),
		},
	}

	// man: deny pager/browser flags that execute programs.
	r["man"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Flag("--html", "--pager",
				"-H", "-P",
			).WithRuleDef(manCommandExec).Deny(
				"can execute arbitrary commands"),
		},
	}

	// make: --eval executes arbitrary makefile code.
	r["make"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Flag("--eval").
				WithRuleDef(makeCommandExec).Deny(
				"can execute arbitrary commands"),
		},
	}

	// zip: -TT runs a test command on each file.
	r["zip"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Flag("-TT").
				WithRuleDef(zipCommandExec).Deny(
				"can execute arbitrary commands"),
		},
	}

	// patch: -e/--ed interprets patch as ed script which
	// can execute shell commands via ! escape. Auto-format
	// detection can also trigger this without the flag.
	r["patch"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Flag("-e", "--ed").
				WithRuleDef(patchCommandExec).Deny(
				"ed script mode can execute " +
					"shell commands"),
		},
	}

	// nm: --plugin loads an arbitrary shared library,
	// turning a benign-looking symbol lister into a native
	// code loader. Near-zero legitimate use, so blocking it
	// costs little.
	r["nm"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Flag("--plugin").
				WithRuleDef(nmCommandExec).Deny(
				"can load arbitrary shared libraries"),
		},
	}

	// sed: deny scripts containing e (execute) command.
	r["sed"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Always().WithRuleDef(sedCommandExec).
				Hook(hookCheckSed),
		},
	}

	// awk: deny programs using system() or shell pipes.
	r["awk"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Always().WithRuleDef(awkCommandExec).
				Hook(hookCheckAwk),
		},
	}

	// --- Guarded with breakdown ---

	// find: extract inner commands from -exec/-execdir.
	// KeepOuter so the rules layer can deny -ok/-okdir
	// and flattening catches cmd subs in other args.
	r["find"] = &model.CommandRules{
		Breakdown: breakdownFind,
		Rules: []model.Rule{
			model.Flag("-ok", "-okdir").
				WithRuleDef(findInteractive).Deny(
				"interactive"),
		},
	}

	// --- Interpreters ---
	//
	// Each interpreter reads script files or inline code
	// and produces code snippets for scanning. Non-code
	// invocations (--version, -m, bare) fall through to
	// the permissions layer. Snippet rules for each
	// language are defined below alongside the command
	// registration.

	// python/python3 — PathAllow so path-invoked
	// interpreters (e.g. /path/to/venv/bin/python3)
	// still extract code for scanning but fall through
	// to permissions, where users can allow specific
	// interpreter paths.
	pythonRules := &model.CommandRules{
		Parser:     pythonParser,
		Breakdown:  breakdownPython,
		PathMode:   model.PathAllow,
		Unverified: pythonUnverified,
	}
	r["python3"] = pythonRules
	r["python"] = pythonRules

	// perl
	r["perl"] = &model.CommandRules{
		Parser:     perlParser,
		Breakdown:  breakdownPerl,
		PathMode:   model.PathAllow,
		Unverified: perlUnverified,
	}

	// ruby
	r["ruby"] = &model.CommandRules{
		Parser:     rubyParser,
		Breakdown:  breakdownRuby,
		PathMode:   model.PathAllow,
		Unverified: rubyUnverified,
	}

	// node
	r["node"] = &model.CommandRules{
		Parser:     nodeParser,
		Breakdown:  breakdownNode,
		PathMode:   model.PathAllow,
		Unverified: nodeUnverified,
	}

	// --- Managed ---

	// bash/sh: -n (syntax check) is allowed outright.
	// -c extracts the code string — only when -c is
	// the sole flag; other flags (--rcfile, -i, etc.)
	// fall through to default deny since they source
	// arbitrary code before the -c body.
	bashRules := &model.CommandRules{
		Breakdown: breakdownBash,
		Default: model.DenyAction(
			"invocation could not be verified"),
		Unverified: bashUnverified,
		Rules: []model.Rule{
			model.Always().WithRuleDef(bashUnverified).
				Hook(hookFormatBashDenial),
		},
	}
	r["bash"] = bashRules
	r["sh"] = bashRules

	// --- Transparent wrappers ---
	// Skip wrapper flags, extract the inner command.
	// Deny flags enumerated in each wrapperDef.

	// time: skip formatting/output flags, extract inner.
	// Bash's `time` keyword is already transparent in the
	// AST — this handles external /usr/bin/time.
	r["time"] = &model.CommandRules{
		Parser:    timeParser,
		Breakdown: breakdownTime,
	}

	// timeout: skip duration + flags, extract inner.
	r["timeout"] = &model.CommandRules{
		Parser:    timeoutParser,
		Breakdown: breakdownTimeout,
	}

	// stdbuf: skip buffering flags, extract inner.
	r["stdbuf"] = &model.CommandRules{
		Parser:    stdbufParser,
		Breakdown: breakdownStdbuf,
	}

	// strace: skip tracing flags, extract inner.
	// Denies -E/--env (can inject env vars) via
	// strace.env-injection.
	r["strace"] = &model.CommandRules{
		Parser:    straceParser,
		Breakdown: breakdownStrace,
	}

	// xargs: skip flags, extract inner command.
	// Denies -p/--interactive (hangs) and
	// -o/--open-tty (opens /dev/tty) via xargs.interactive.
	// The -I-from-stdin / ambiguous-replacement / unknown-
	// flag denials are gated by xargs.unverified.
	r["xargs"] = &model.CommandRules{
		Parser:     xargsParser,
		Breakdown:  breakdownXargs,
		Unverified: xargsUnverified,
	}

	// --- Exec wrappers ---
	// Run an inner command after consuming their own args.
	// Like timeout/strace, the inner command is re-analysed.
	// They exec directly (execvp), so a leading NAME=val is
	// the program name, not an assignment — except env, which
	// honours assignments and feeds them to the EnvVars axis.

	// env: parse env's flags, split leading NAME=val into
	// honoured assignments (-> EnvVars deny axis + value
	// cmd-subs), extract the inner command. -S/--split-string
	// is denied (env's splitter is not the shell). PathAllow so
	// /usr/bin/env python3 (shebang style) still unwraps and
	// scans the inner interpreter.
	r["env"] = &model.CommandRules{
		Parser:     envParser,
		Breakdown:  breakdownEnv,
		PathMode:   model.PathAllow,
		Unverified: envUnverified,
	}

	// nohup/setsid/nice/ionice/exec: transparent exec wrappers.
	// Skip own flags, extract the inner command; empty inner is
	// safe (e.g. ionice -p PID, exec > log).
	r["nohup"] = &model.CommandRules{
		Parser: nohupParser, Breakdown: breakdownNohup}
	r["setsid"] = &model.CommandRules{
		Parser: setsidParser, Breakdown: breakdownSetsid}
	r["nice"] = &model.CommandRules{
		Parser: niceParser, Breakdown: breakdownNice}
	r["ionice"] = &model.CommandRules{
		Parser: ioniceParser, Breakdown: breakdownIonice}
	r["exec"] = &model.CommandRules{
		Parser: execParser, Breakdown: breakdownExec}

	// chroot: skip NEWROOT, extract inner command. With no
	// command it runs an interactive $SHELL — denied via
	// chroot.unverified.
	r["chroot"] = &model.CommandRules{
		Parser:     chrootParser,
		Breakdown:  breakdownChroot,
		Unverified: chrootUnverified,
	}

	// flock: skip the lock file, extract the inner command;
	// flock FILE -c STR runs STR via a shell, re-parsed as code.
	r["flock"] = &model.CommandRules{
		Parser:    flockParser,
		Breakdown: breakdownFlock,
	}

	// runuser/setpriv/setarch: privilege/personality wrappers
	// with shell-string, interactive, and ambiguous-positional
	// forms we cannot model safely. Deny outright (suppressed,
	// falling through to permissions, when their rule is off).
	r["runuser"] = &model.CommandRules{Breakdown: breakdownRunuser}
	r["setpriv"] = &model.CommandRules{Breakdown: breakdownSetpriv}
	r["setarch"] = &model.CommandRules{Breakdown: breakdownSetarch}

	// --- Shell builtins ---

	// eval: join args and re-parse as code. All args
	// must be static — variables could execute anything.
	r["eval"] = &model.CommandRules{
		Breakdown:  breakdownEval,
		Unverified: evalUnverified,
	}

	// trap: extract the code string (first positional)
	// for re-parsing. -l/-p and signal resets are safe.
	r["trap"] = &model.CommandRules{
		Breakdown:  breakdownTrap,
		Unverified: trapUnverified,
	}

	// command: strip -v/-V/-p/-- flags, extract the
	// inner command. Rejects unknown flags.
	r["command"] = &model.CommandRules{
		Breakdown:  breakdownCommand,
		Unverified: commandUnverified,
	}

	// cd/pushd/popd: track cwd changes. cd with a
	// static absolute path at unconditional scope
	// updates Cwd; everything else marks cwd unknown.
	// Falls through to normal flattening.
	r["cd"] = &model.CommandRules{
		Breakdown: breakdownCd,
	}
	r["pushd"] = &model.CommandRules{
		Breakdown: breakdownCd,
	}
	r["popd"] = &model.CommandRules{
		Breakdown: breakdownCd,
	}

	// unset: record SawUnsetF when -f is seen so
	// function-call detection knows functions may have
	// been removed. Falls through to normal flattening.
	r["unset"] = &model.CommandRules{
		Breakdown: breakdownUnset,
	}

	// =========================================================
	// Snippet rules — dangerous patterns in code snippets.
	//
	// Keyed by language, not command. A language may be
	// produced by multiple commands (python3 and python
	// both emit LangPython snippets). Patterns and rules
	// are defined in each language file (python.go, etc.);
	// this just wires them into the map. All of a language's
	// snippet rules detect the same threat — running shell
	// commands from inside the script — so they share one
	// def (on the SnippetLang) and are enabled or disabled
	// together.
	// =========================================================

	s := map[string]*model.SnippetLang{
		model.LangPython: snippetLang(
			pythonCommandExec,
			syntaxPython.stripComments,
			pythonImport("subprocess", nil).
				Deny("subprocess (shell command execution)"),
			pythonImport("os", pythonDangerousOSFuncs).
				Deny("os (shell command execution)"),
			pythonCall("os", pythonDangerousOSFuncs).
				Deny("os (shell command execution)"),
		),

		model.LangPerl: snippetLang(
			perlCommandExec,
			syntaxPerl.stripComments,
			perlUse("IPC::").
				Deny("IPC (shell command execution)"),
			perlBareCall("system", "exec").
				Deny("shell command execution"),
			syntaxPerl.match("`"+`|\bqx\b`).
				Deny("shell execution (backtick/qx)"),
		),

		model.LangRuby: snippetLang(
			rubyCommandExec,
			syntaxRuby.stripComments,
			rubyRequire("open3", "open4").
				Deny("open3/open4 (shell command execution)"),
			rubyBareCall(
				"system", "exec", "spawn").
				Deny("shell command execution"),
			syntaxRuby.match(`\bIO\.popen`).
				Deny("IO.popen (shell command execution)"),
			syntaxRuby.match(
				`\bOpen3\.(?:popen|capture|pipeline)`).
				Deny("Open3 (shell command execution)"),
			syntaxRuby.match(
				"`"+`|%x(?:[^a-zA-Z0-9_]|$)`).
				Deny("shell execution (backtick/%x)"),
		),

		model.LangNode: snippetLang(
			nodeCommandExec,
			syntaxNode.stripComments,
			nodeRequire("child_process").
				Deny("child_process (shell command execution)"),
		),
	}

	return r, s
}

// snippetLang builds a SnippetLang governed by def. All of a
// language's snippet rules share that one def, so users enable
// or disable the whole set at once.
func snippetLang(
	def *model.RuleDef,
	strip func(string) string,
	rules ...model.SnippetRule,
) *model.SnippetLang {
	return &model.SnippetLang{
		Def:           def,
		StripComments: strip,
		Rules:         rules,
	}
}
