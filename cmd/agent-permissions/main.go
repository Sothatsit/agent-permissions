// Command agent-permissions is a toolkit for managing AI
// agent permissions. Subcommands:
//
//	claude-hook    PreToolUse hook for Claude Code
//	check          Simulate the hook and print the decision
//	validate       Report config problems: malformed entries
//	               and bad rule/preset references fail
//	               (exit 2); empty reasons are an
//	               informational note (exit 0)
//	setup          Write a starter ~/.agents/permissions.json
//	install        Wire the hook into known harness configs
//	presets list   Show enforced, enabled, and disabled
//	               presets
//	rules list     List built-in rules as 'id - description'
//
// Top-level exit codes:
//
//	0 - subcommand completed successfully
//	2 - usage error or internal failure. This fails closed
//	    because Claude Code blocks the tool when the hook exits 2.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/sothatsit/agent-permissions/internal/harness"
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/perms"
)

// version is set at build time via ldflags.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run() error {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		return fmt.Errorf("missing subcommand")
	}

	switch os.Args[1] {
	case "--version", "-v":
		fmt.Println(version)
		return nil
	case "--help", "-h", "help":
		printUsage(os.Stdout)
		return nil
	case "claude-hook":
		return runClaudeHook()
	case "check":
		return check(os.Args[2:])
	case "validate":
		return validate(os.Args[2:])
	case "setup":
		return setup(os.Args[2:])
	case "presets":
		return runPresetsCommand(os.Args[2:])
	case "rules":
		return runRulesCommand(os.Args[2:])
	case "install":
		return install(os.Args[2:])
	default:
		printUsage(os.Stderr)
		return fmt.Errorf(
			"unknown subcommand: %s", os.Args[1])
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w,
		"agent-permissions — toolkit for AI agent bash permissions")
	fmt.Fprintf(w, "Version: %s\n", version)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage: agent-permissions <subcommand>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w,
		"  claude-hook       PreToolUse hook for Claude Code "+
			"(reads JSON on stdin)")
	fmt.Fprintln(w,
		"  check '<cmd>'     Simulate the hook on a command "+
			"and print the decision")
	fmt.Fprintln(w,
		"  validate          Report malformed entries and bad "+
			"rule/preset references")
	fmt.Fprintln(w,
		"  setup             Write a starter "+
			"~/.agents/permissions.json")
	fmt.Fprintln(w,
		"  presets list      List enforced, enabled, and "+
			"disabled presets")
	fmt.Fprintln(w,
		"  rules list        List built-in rules as "+
			"'id - description'")
	fmt.Fprintln(w,
		"  install           Wire the hook into known harness "+
			"configs (e.g. ~/.claude/settings.json)")
	fmt.Fprintln(w, "  --version         Print version")
	fmt.Fprintln(w, "  --help            Print this help")
}

// hookInput is the JSON structure received from Claude
// Code on a PreToolUse event.
type hookInput struct {
	ToolName       string `json:"tool_name"`
	PermissionMode string `json:"permission_mode"`
	ToolInput      struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	CWD string `json:"cwd"`
}

// hookOutput is the JSON structure returned to Claude
// Code.
type hookOutput struct {
	HookSpecificOutput hookSpecific `json:"hookSpecificOutput"`
}

type hookSpecific struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
}

// runClaudeHook runs the Claude Code PreToolUse hook flow:
// read JSON from stdin, classify the bash command, and
// emit a decision on stdout.
func runClaudeHook() error {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf(
			"failed to read stdin: %v", err)
	}

	var input hookInput
	if err := json.Unmarshal(data, &input); err != nil {
		return fmt.Errorf(
			"failed to parse hook input: %v", err)
	}

	if input.ToolName != "Bash" {
		// Other tools pass through silently.
		return nil
	}

	if input.ToolInput.Command == "" {
		return fmt.Errorf("empty command in hook input")
	}

	configDir, err := resolveClaudeConfigDir()
	if err != nil {
		return err
	}

	resolved, err := perms.Resolve(configDir, input.CWD)
	if err != nil {
		return fmt.Errorf(
			"failed to load permissions: %v", err)
	}
	permissions := resolved.Permissions
	// claude-hook is the Claude-Code-bound entrypoint, so
	// swap the loader's Placeholder harness for the real
	// one - the user-visible text will reference
	// /permissions etc.
	permissions.Harness = harness.ClaudeCode{}

	br, err := resolved.Breakdown(input.ToolInput.Command)
	if err != nil {
		r := perms.DenyResult(breakdownDenialReason(err))
		return writeDecision(model.Deny, r.Reason)
	}

	// Check permissions.
	result := permissions.Check(br)

	switch result.Decision {
	case model.Allow:
		// In auto mode, inline code snippets that
		// passed our static filters get a second
		// review by the classifier. The classifier
		// sees the full command string (including
		// the -c/-e code) and can catch patterns
		// our rules miss.
		if input.PermissionMode == "auto" &&
			hasInlineSnippets(&br) {
			return nil
		}
		return writeDecision(model.Allow, result.Reason)
	case model.SoftAsk:
		// In auto mode, fall through to the
		// classifier for per-invocation judgment.
		// In other modes, prompt the user.
		if input.PermissionMode == "auto" {
			return nil
		}
		return writeDecision(model.Ask,
			"\n"+result.Reason+"\n\n")
	case model.Ask:
		return writeDecision(
			model.Ask, "\n"+result.Reason+"\n\n")
	case model.Deny:
		return writeDecision(model.Deny, result.Reason)
	case model.Undecided:
		// Truly no opinion (e.g. bare assignment,
		// suspicious env vars) - always fall through
		// to Claude Code's own prompt.
		return nil
	}

	return nil
}

// hasInlineSnippets reports whether the breakdown
// produced any inline code snippets (SourceFile is
// empty). These are agent-generated code (-c/-e) as
// opposed to user-authored script files.
func hasInlineSnippets(
	br *model.BreakdownResult,
) bool {
	for i := range br.CodeSnippets {
		if br.CodeSnippets[i].SourceFile == "" {
			return true
		}
	}
	return false
}

// breakdownDenialReason renders a breakdown error as a deny
// reason. When the denial came from a specific rule (the
// imperative wrapper/xargs checks return a *model.RuleError),
// it appends "(from rule:<id>)" so the attribution matches
// the permissions layer and names the ID to disable.
func breakdownDenialReason(err error) string {
	reason := err.Error()
	var re *model.RuleError
	if errors.As(err, &re) && re.Def != nil {
		reason += "  (from rule:" + re.Def.ID + ")"
	}
	return reason
}

func writeDecision(decision model.Decision, reason string) error {
	output := hookOutput{
		HookSpecificOutput: hookSpecific{
			HookEventName:            "PreToolUse",
			PermissionDecision:       decision.String(),
			PermissionDecisionReason: reason,
		},
	}
	data, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("failed to marshal output: %w", err)
	}

	if _, err := fmt.Println(string(data)); err != nil {
		return fmt.Errorf("write hook output: %w", err)
	}

	return nil
}

func resolveClaudeConfigDir() (string, error) {
	if configDir := os.Getenv("CLAUDE_CONFIG_DIR"); configDir != "" {
		return configDir, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf(
			"cannot determine home directory: %w", err)
	}

	return filepath.Join(home, ".claude"), nil
}
