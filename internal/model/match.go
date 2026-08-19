package model

import (
	"strings"

	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// FlagMatcher matches a flag by name, optionally checking its value. An opaque
// value matches conservatively, which over-matches and is safe for deny and ask
// rules.
type FlagMatcher struct {
	Names              []string
	ValueCouldContain  string
	ValueMayHavePrefix string
}

func (m *FlagMatcher) Match(
	input ParseResult,
) (bool, ParseResult, string) {
	for _, pf := range input.PossibleFlags {
		if strings.HasPrefix(pf.Name, "--") {
			if !m.nameMatches(pf.Name) {
				continue
			}
			if !m.valueMatchesFlag(pf) {
				continue
			}

			return true, input, pf.Name
		}

		matched, which := m.shortFlagMatches(pf)
		if !matched {
			continue
		}

		return true, input, which
	}

	return false, ParseResult{}, ""
}

// shortFlagMatches checks a short flag against the matcher's names, testing
// value conditions against both the token text and the next argument.
func (m *FlagMatcher) shortFlagMatches(
	pf ParsedFlag,
) (bool, string) {
	for _, name := range m.Names {
		if !strings.HasPrefix(name, "-") ||
			strings.HasPrefix(name, "--") {
			continue
		}

		stripped := strings.TrimPrefix(name, "-")
		if !strings.Contains(pf.Name, stripped) {
			continue
		}

		// Naming the token says why a flag nobody typed was matched:
		// -e reported "in -Slogged" is the l of a pickaxe value, not an
		// editor. Which short options take a jammed-in value is
		// per-subcommand knowledge this parser does not keep, so the
		// over-match stands and the message carries the evidence.
		which := name
		if pf.Name != name {
			which = name + " in " + pf.Name
		}

		if m.ValueCouldContain == "" &&
			m.ValueMayHavePrefix == "" {
			return true, which
		}
		if m.textMayMatch(pf.Name) {
			return true, which
		}
		if m.valueMatchesWord(pf.Value) {
			return true, which
		}
	}

	return false, ""
}

func (m *FlagMatcher) nameMatches(name string) bool {
	for _, n := range m.Names {
		if n == name {
			return true
		}
	}

	return false
}

func (m *FlagMatcher) valueMatchesFlag(
	f ParsedFlag,
) bool {
	if m.ValueCouldContain == "" &&
		m.ValueMayHavePrefix == "" {
		return true
	}

	return m.valueMatchesWord(f.Value)
}

func (m *FlagMatcher) valueMatchesWord(
	w *syntax.Word,
) bool {
	if w == nil {
		return false
	}
	if m.ValueCouldContain != "" &&
		!word.MayContain(w, m.ValueCouldContain) {
		return false
	}
	if m.ValueMayHavePrefix != "" &&
		!word.MayHavePrefix(w, m.ValueMayHavePrefix) {
		return false
	}

	return true
}

// textMayMatch tests value conditions against a short flag's embedded value,
// which lives in the token text rather than a separate Word.
func (m *FlagMatcher) textMayMatch(text string) bool {
	if m.ValueCouldContain != "" &&
		!strings.Contains(text, m.ValueCouldContain) {
		return false
	}
	if m.ValueMayHavePrefix != "" &&
		!strings.HasPrefix(text, m.ValueMayHavePrefix) {
		return false
	}

	return true
}

// SubcmdMatcher matches the first positional argument.
type SubcmdMatcher struct {
	Names []string
}

func (m *SubcmdMatcher) Match(
	input ParseResult,
) (bool, ParseResult, string) {
	if len(input.Raw) == 0 {
		return false, ParseResult{}, ""
	}

	w := input.Raw[0]
	if word.MayHavePrefix(w, "-") {
		return false, ParseResult{}, ""
	}

	for _, name := range m.Names {
		if word.MayEqual(w, name) {
			child := ParseResult{
				Raw: input.Raw[1:],
			}
			PopulatePossibleFlags(&child)
			return true, child, name
		}
	}

	return false, ParseResult{}, ""
}

type AlwaysMatcher struct{}

func (*AlwaysMatcher) Match(
	input ParseResult,
) (bool, ParseResult, string) {
	return true, input, ""
}
