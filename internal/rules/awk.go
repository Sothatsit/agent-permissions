package rules

import (
	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// hookCheckAwk is a HookFunc that checks awk arguments
// for dangerous patterns (system(), pipes, command
// substitution). Also denies opaque arguments since
// hidden content could contain dangerous patterns.
func hookCheckAwk(
	input model.ParseResult,
) (model.Decision, string) {
	for _, w := range input.Raw {
		if word.DefinitelyHasPrefix(w, "-") {
			continue
		}
		if !word.Static(w) {
			return model.Deny,
				"awk argument contains " +
					word.OpaqueReason(w) +
					" — cannot verify safety"
		}
		if word.DefinitelyContains(w, "system(") {
			return model.Deny,
				"awk system() can execute " +
					"shell commands"
		}
		if word.DefinitelyContains(w, "`") ||
			word.DefinitelyContains(w, "$(") {
			return model.Deny,
				"awk program contains " +
					"command substitution"
		}
		if word.DefinitelyContains(w, "{") &&
			word.DefinitelyContains(w, "|") {
			return model.Deny,
				"awk pipe can execute commands"
		}
		if word.DefinitelyContains(w, "|") &&
			word.DefinitelyContains(w, "getline") {
			return model.Deny,
				"awk getline pipe can " +
					"execute commands"
		}
	}
	return model.Undecided, ""
}
