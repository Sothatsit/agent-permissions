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
			model.Flag("-e", "--edit").Deny(
				"interactive"),
			model.Flag("--upload-pack",
				"--receive-pack",
				"--open-files-in-pager",
			).Deny("can execute arbitrary commands"),
			model.Flag("-c").
				ValueCouldContain("=").Deny(
				"can execute arbitrary commands " +
					"via hooks/editor/pager " +
					"config — use git config " +
					"instead"),
			model.Subcmd("branch").DefaultDeny(
				"unrecognised flag").Rules(
				model.Hook("gitBranch",
					classifyGitBranch),
			),
			model.Subcmd("tag").DefaultDeny(
				"unrecognised flag").Rules(
				model.Hook("gitTag",
					classifyGitTag),
			),
			model.Subcmd("remote").DefaultAllow(
				"read-only").Rules(
				model.Subcmd("add", "rename",
					"remove", "rm", "set-head",
					"set-branches", "set-url",
					"prune", "update",
				).SoftAsk("git remote: write operation"),
			),
		},
	}

	// gh: classify gh api by HTTP method.
	r["gh"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Subcmd("api").DefaultDeny(
				"unrecognised flag").Rules(
				model.Hook("ghApi",
					classifyGhApi),
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
			).Deny("can execute arbitrary commands"),
			model.Flag(
				"--checkpoint-action",
			).ValueMayHavePrefix(
				"exec=",
			).Deny("can execute arbitrary commands"),
			model.Flag("-I").Deny(
				"can execute arbitrary commands"),
		},
	}

	// sort: --compress-program runs an external command.
	r["sort"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Flag("--compress-program").Deny(
				"can execute arbitrary commands"),
		},
	}

	// man: deny pager/browser flags that execute programs.
	r["man"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Flag("--html", "--pager",
				"-H", "-P",
			).Deny("can execute arbitrary commands"),
		},
	}

	// make: --eval executes arbitrary makefile code.
	r["make"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Flag("--eval").Deny(
				"can execute arbitrary commands"),
		},
	}

	// zip: -TT runs a test command on each file.
	r["zip"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Flag("-TT").Deny(
				"can execute arbitrary commands"),
		},
	}

	// patch: -e/--ed interprets patch as ed script which
	// can execute shell commands via ! escape. Auto-format
	// detection can also trigger this without the flag.
	r["patch"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Flag("-e", "--ed").Deny(
				"ed script mode can execute " +
					"shell commands"),
		},
	}

	// nm: --plugin loads an arbitrary shared library.
	r["nm"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Flag("--plugin").Deny(
				"can load arbitrary shared libraries"),
		},
	}

	// sed: deny scripts containing e (execute) command.
	r["sed"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Hook("sed", hookCheckSed),
		},
	}

	// awk: deny programs using system() or shell pipes.
	r["awk"] = &model.CommandRules{
		Rules: []model.Rule{
			model.Hook("awk", hookCheckAwk),
		},
	}

	// --- Guarded with breakdown ---

	// find: extract inner commands from -exec/-execdir.
	// KeepOuter so the rules layer can deny -ok/-okdir
	// and flattening catches cmd subs in other args.
	r["find"] = &model.CommandRules{
		Breakdown: breakdownFind,
		Rules: []model.Rule{
			model.Flag("-ok", "-okdir").Deny(
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
		Parser:    pythonParser,
		Breakdown: breakdownPython,
		PathMode:  model.PathAllow,
	}
	r["python3"] = pythonRules
	r["python"] = pythonRules

	// perl
	r["perl"] = &model.CommandRules{
		Parser:    perlParser,
		Breakdown: breakdownPerl,
		PathMode:  model.PathAllow,
	}

	// ruby
	r["ruby"] = &model.CommandRules{
		Parser:    rubyParser,
		Breakdown: breakdownRuby,
		PathMode:  model.PathAllow,
	}

	// node
	r["node"] = &model.CommandRules{
		Parser:    nodeParser,
		Breakdown: breakdownNode,
		PathMode:  model.PathAllow,
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
		Rules: []model.Rule{
			model.Hook("bash", hookFormatBashDenial),
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
	// Denies -E/--env (can inject env vars).
	r["strace"] = &model.CommandRules{
		Parser:    straceParser,
		Breakdown: breakdownStrace,
	}

	// xargs: skip flags, extract inner command.
	// Denies -p/--interactive (hangs) and
	// -o/--open-tty (opens /dev/tty).
	r["xargs"] = &model.CommandRules{
		Parser:    xargsParser,
		Breakdown: breakdownXargs,
	}

	// --- Shell builtins ---

	// eval: join args and re-parse as code. All args
	// must be static — variables could execute anything.
	r["eval"] = &model.CommandRules{
		Breakdown: breakdownEval,
	}

	// trap: extract the code string (first positional)
	// for re-parsing. -l/-p and signal resets are safe.
	r["trap"] = &model.CommandRules{
		Breakdown: breakdownTrap,
	}

	// command: strip -v/-V/-p/-- flags, extract the
	// inner command. Rejects unknown flags.
	r["command"] = &model.CommandRules{
		Breakdown: breakdownCommand,
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
	// this just wires them into the map.
	// =========================================================

	s := map[string]*model.SnippetLang{
		model.LangPython: {
			StripComments: syntaxPython.stripComments,
			Rules: []model.SnippetRule{
				pythonImport("subprocess", nil).
					Deny("subprocess (shell command execution)"),
				pythonImport("ctypes", nil).
					Deny("ctypes (native code execution)"),
				pythonImport("cffi", nil).
					Deny("cffi (native code execution)"),
				pythonImport("os", pythonDangerousOSFuncs).
					Deny("os (shell command execution)"),
				pythonCall("os", pythonDangerousOSFuncs).
					Deny("os (shell command execution)"),
			},
		},

		model.LangPerl: {
			StripComments: syntaxPerl.stripComments,
			Rules: []model.SnippetRule{
				perlUse("IPC::").
					Deny("IPC (shell command execution)"),
				perlUse("Inline::").
					Deny("Inline (native code execution)"),
				perlUse("FFI::").
					Deny("FFI (native code execution)"),
				perlUse("DynaLoader").
					Deny("DynaLoader (native code loading)"),
				perlUse("XSLoader").
					Deny("XSLoader (native code loading)"),
				perlBareCall("system", "exec").
					Deny("shell command execution"),
				syntaxPerl.match("`" + `|\bqx\b`).
					Deny("shell execution (backtick/qx)"),
			},
		},

		model.LangRuby: {
			StripComments: syntaxRuby.stripComments,
			Rules: []model.SnippetRule{
				rubyRequire("open3", "open4").
					Deny("open3/open4 (shell command execution)"),
				rubyRequire("fiddle").
					Deny("fiddle (native code execution)"),
				rubyRequire("ffi").
					Deny("ffi (native code execution)"),
				rubyBareCall(
					"system", "exec", "spawn").
					Deny("shell command execution"),
				syntaxRuby.match(`\bIO\.popen`).
					Deny("IO.popen (shell command execution)"),
				syntaxRuby.match(
					`\bOpen3\.(?:popen|capture|pipeline)`).
					Deny("Open3 (shell command execution)"),
				syntaxRuby.match(
					"`" + `|%x(?:[^a-zA-Z0-9_]|$)`).
					Deny("shell execution (backtick/%x)"),
			},
		},

		model.LangNode: {
			StripComments: syntaxNode.stripComments,
			Rules: []model.SnippetRule{
				nodeRequire("child_process").
					Deny("child_process (shell command execution)"),
				nodeRequire("ffi", "ffi-napi").
					Deny("ffi (native code execution)"),
				nodeRequire("ref", "ref-napi").
					Deny("ref (native memory access)"),
				syntaxNode.match(
					`\bprocess\.(?:binding|dlopen)`).
					Deny("process (native code loading)"),
			},
		},
	}

	return r, s
}
