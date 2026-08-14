// Package harness abstracts the surfaces that vary between agent harnesses
// (Claude Code, future Codex/Copilot adapters). The hook produces decisions in
// a harness-neutral form; the harness layer translates those decisions into
// harness-specific output text and protocol shapes.
//
// Today the only concrete harness is Claude Code, used by the `claude-hook`
// subcommand. The package also ships a Placeholder harness used by
// harness-agnostic tools like `check` and `validate`: those run from the
// developer's terminal, not under a particular agent harness, so they emit
// `<placeholder>` strings to make the harness-specific surfaces visible at a
// glance.
package harness

// Harness is the per-process binding to a particular agent harness. The hook
// reads tool input in the harness's wire format, produces decisions, and emits
// output text the harness's user-facing prompt will render. Strings come from
// the harness because different harnesses have different conventions for how a
// user adds a permission (Claude Code's /permissions slash command vs whatever
// Codex/Copilot expose).
type Harness interface {
	// UnknownCommandHeader returns the prompt-text line shown before
	// listing suggested patterns for unknown commands. The hook emits
	// "Unknown command(s). <header>:\n* <pattern>...".
	UnknownCommandHeader() string
}

// ClaudeCode is the Claude Code adapter. Its UnknownCommandHeader references
// the /permissions slash command, which is how Claude Code surfaces the user's
// pattern editor.
type ClaudeCode struct{}

func (ClaudeCode) UnknownCommandHeader() string {
	return "Add to /permissions to auto-allow"
}

// Placeholder is the harness used by tools that aren't running under a specific
// agent harness - `check` and `validate` from the developer's terminal. It
// returns `<placeholder>` strings so harness-specific surfaces are visibly
// marked and developers don't mistake them for the production output.
type Placeholder struct{}

func (Placeholder) UnknownCommandHeader() string {
	return "<unknown-command-header>"
}
