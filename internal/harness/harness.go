// Package harness abstracts the surfaces that vary between agent harnesses. The
// hook produces decisions in a harness-neutral form, and the harness translates
// them into its own output text and protocol shapes. Claude Code is the only
// concrete harness today.
package harness

// Harness is the per-process binding to one agent harness. Strings come from it
// because harnesses differ in how a user adds a permission, such as Claude
// Code's /permissions command.
type Harness interface {
	// UnknownCommandHeader returns the prompt-text line shown before
	// listing suggested patterns for unknown commands. The hook emits
	// "Unknown command(s). <header>:\n* <pattern>...".
	UnknownCommandHeader() string
}

// ClaudeCode's UnknownCommandHeader points at the /permissions command, which
// is how Claude Code surfaces the pattern editor.
type ClaudeCode struct{}

func (ClaudeCode) UnknownCommandHeader() string {
	return "Add to /permissions to auto-allow"
}

// Placeholder serves tools that run from a developer's terminal rather than
// under a harness, returning `<placeholder>` strings so harness-specific
// surfaces cannot be mistaken for real output.
type Placeholder struct{}

func (Placeholder) UnknownCommandHeader() string {
	return "<unknown-command-header>"
}
