package rules

import (
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
	"github.com/sothatsit/agent-permissions/internal/word"
)

// hookCheckSed is a HookFunc that checks sed arguments
// for dangerous patterns (e flag, e command). Also denies
// opaque arguments (variable expansion, command
// substitution) since hidden content could contain
// dangerous patterns.
func hookCheckSed(
	input model.ParseResult,
) (model.Decision, string) {
	for _, w := range input.Raw {
		// Deny opaque args — can't verify safety.
		if !word.Static(w) {
			return model.Deny,
				"sed argument contains " +
					word.OpaqueReason(w) +
					" — cannot verify safety"
		}
		text := word.Text(w)
		if dangerous, reason :=
			checkSedArg(text); dangerous {
			return model.Deny, reason
		}
	}
	return model.Undecided, ""
}

// checkSedArg checks a single sed argument for dangerous
// patterns. Handles multi-command scripts separated by
// semicolons or newlines (e.g. '1d; e ssh evil' or
// $'1d\ne ssh evil').
func checkSedArg(arg string) (bool, string) {
	segments := strings.FieldsFunc(
		arg, func(r rune) bool {
			return r == ';' || r == '\n'
		})
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if len(seg) == 0 {
			continue
		}
		// s-command with e flag: s/foo/bar/e
		if len(seg) > 1 && seg[0] == 's' {
			delim := seg[1]
			count := 0
			flagStart := -1
			for j := 1; j < len(seg); j++ {
				if seg[j] == delim {
					count++
					if count == 3 {
						flagStart = j + 1
						break
					}
				}
			}
			if flagStart > 0 &&
				flagStart < len(seg) {
				flags := seg[flagStart:]
				if strings.Contains(flags, "e") {
					return true,
						"sed e modifier can " +
							"execute shell commands"
				}
				if strings.ContainsAny(
					flags, "$`") {
					return true,
						"sed substitution flags " +
							"contain expansion — " +
							"cannot verify safety"
				}
			}
		}
		// e command: '1e cmd', 'e cmd', etc.
		trimmed := strings.TrimLeft(
			seg, "0123456789")
		if strings.HasPrefix(trimmed, "e") &&
			(len(trimmed) == 1 ||
				trimmed[1] == ' ') {
			return true, "sed e command can " +
				"execute shell commands"
		}
	}
	return false, ""
}
