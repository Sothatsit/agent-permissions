package rules

import "github.com/sothatsit/agent-permissions/internal/model"

// The rule directory. Every user-disableable rule is declared here exactly once
// via defineRule, which records it in ruleCatalog and returns the
// *model.RuleDef that the registry references (WithRuleDef / Unverified / a
// wrapper's denyRule / a SnippetLang's Def). Because a rule can only obtain an
// ID by referencing one of these defs, the directory is the single source of
// truth: there is no separate ID list to drift from, and a typo is a compile
// error. Descriptions name the threat the rule mitigates, not the mechanism -
// they are what a user reads to decide whether to disable it.

// ruleCatalog is the ordered list of every rule, in declaration order below.
// defineRule appends to it at package-init time.
var ruleCatalog []*model.RuleDef

// defineRule records a rule in the directory and returns its def. Declaration
// order here is the order `rules list` and the catalog tests see.
func defineRule(id, description string) *model.RuleDef {
	d := &model.RuleDef{ID: id, Description: description}
	ruleCatalog = append(ruleCatalog, d)
	return d
}

var (
	gitInteractive = defineRule("git.interactive",
		"git -e/--edit opens an interactive editor")
	gitCommandExec = defineRule("git.command-execution",
		"git --upload-pack/--receive-pack/"+
			"--open-files-in-pager run arbitrary commands")
	gitConfigInject = defineRule("git.config-injection",
		"git -c injects config that can execute commands")
	gitBranchWrites = defineRule("git.branch-writes",
		"git branch creates, renames, or deletes branches")
	gitTagWrites = defineRule("git.tag-writes",
		"git tag creates, deletes, or signs tags")
	gitRemoteWrites = defineRule("git.remote-writes",
		"git remote adds, removes, or rewrites remotes")
	ghAPIWrites = defineRule("gh.api-writes",
		"gh api makes non-GET requests or sends fields")
	tarCommandExec = defineRule("tar.command-execution",
		"tar flags that run an external program")
	sortCommandExec = defineRule("sort.command-execution",
		"sort --compress-program runs an external program")
	manCommandExec = defineRule("man.command-execution",
		"man pager/browser flags run a program")
	makeCommandExec = defineRule("make.command-execution",
		"make --eval runs arbitrary makefile code")
	zipCommandExec = defineRule("zip.command-execution",
		"zip -TT runs a test command per file")
	patchCommandExec = defineRule("patch.command-execution",
		"patch ed-script mode runs shell commands")
	nmCommandExec = defineRule("nm.command-execution",
		"nm --plugin loads arbitrary native code")
	sedCommandExec = defineRule("sed.command-execution",
		"sed e command/modifier runs shell commands")
	awkCommandExec = defineRule("awk.command-execution",
		"awk programs can run shell commands or load code")
	findInteractive = defineRule("find.interactive",
		"find -ok/-okdir prompt interactively")
	xargsInteractive = defineRule("xargs.interactive",
		"xargs -p/-o hang or open a tty")
	xargsUnverified = defineRule("xargs.unverified",
		"xargs invocations whose command can't be verified")
	straceEnvInject = defineRule("strace.env-injection",
		"strace -E injects environment variables")
	pythonCommandExec = defineRule("python.command-execution",
		"python code that runs shell commands")
	perlCommandExec = defineRule("perl.command-execution",
		"perl code that runs shell commands")
	rubyCommandExec = defineRule("ruby.command-execution",
		"ruby code that runs shell commands")
	nodeCommandExec = defineRule("node.command-execution",
		"node code that runs shell commands")
	bashUnverified = defineRule("bash.unverified",
		"bash/sh invocations or Bash source that can't be verified")
	pythonUnverified = defineRule("python.unverified",
		"python invocations that can't be verified")
	perlUnverified = defineRule("perl.unverified",
		"perl invocations that can't be verified")
	rubyUnverified = defineRule("ruby.unverified",
		"ruby invocations that can't be verified")
	nodeUnverified = defineRule("node.unverified",
		"node invocations that can't be verified")
	evalUnverified = defineRule("eval.unverified",
		"eval of arguments that can't be verified")
	trapUnverified = defineRule("trap.unverified",
		"trap with code that can't be verified")
	commandUnverified = defineRule("command.unverified",
		"command builtin with flags that can't be verified")
	envUnverified = defineRule("env.unverified",
		"env -S/--split-string or flags that can't be verified")
	chrootUnverified = defineRule("chroot.unverified",
		"chroot with no command runs an interactive shell")
	runuserUnverified = defineRule("runuser.unverified",
		"runuser runs commands as another user")
	setprivUnverified = defineRule("setpriv.unverified",
		"setpriv alters process privileges before running")
	setarchUnverified = defineRule("setarch.unverified",
		"setarch sets the personality before running")
)

// AllRules returns the rule directory: every user-disableable rule, in
// declaration order. The preset-ownership invariant tests check the shipped
// rules against it; a future `rules list` subcommand will display it to users.
func AllRules() []*model.RuleDef {
	return ruleCatalog
}

// IsRuleID reports whether id is a known rule ID. The preset-ownership test
// uses it; a future `validate` check will use it to flag typos in config and
// preset Rules sections.
func IsRuleID(id string) bool {
	for _, r := range ruleCatalog {
		if r.ID == id {
			return true
		}
	}

	return false
}

// AllEnabled returns a RuleConfigs with every catalog rule enabled. For tests
// and callers that want the full ruleset without resolving presets - stated
// explicitly, never via a nil map (which panics). Production resolves the
// default presets instead.
func AllEnabled() model.RuleConfigs {
	out := make(model.RuleConfigs, len(ruleCatalog))
	for _, r := range ruleCatalog {
		out[r.ID] = model.RuleConfig{Enabled: true}
	}

	return out
}
