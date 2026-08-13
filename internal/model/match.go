package model

import (
	"strings"

	"github.com/sothatsit/agent-permissions/internal/word"

	"mvdan.cc/sh/v3/syntax"
)

// FlagMatcher matches a flag by name, optionally checking
// its value. Value conditions use "could" semantics: if
// the value is opaque (contains ParamExp, CmdSubst, etc.),
// the condition conservatively matches (over-match). This
// is safe for deny/ask rules.
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
		// Short flag: check if any rule name (stripped
		// of -) appears in the token text. This
		// over-matches for clustered flags, which is
		// acceptable for deny/ask rules.
		matched, which := m.shortFlagMatches(pf)
		if !matched {
			continue
		}
		return true, input, which
	}
	return false, ParseResult{}, ""
}

// shortFlagMatches checks a short-flag PossibleFlag
// against the matcher's names. For value conditions,
// checks both the token text (may contain embedded value)
// and the value Word (next arg).
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
		if m.ValueCouldContain == "" &&
			m.ValueMayHavePrefix == "" {
			return true, name
		}
		// Check embedded value in token text
		// (short flags can have values jammed in,
		// e.g. -Iexec=foo).
		if m.textMayMatch(pf.Name) {
			return true, name
		}
		// Check next-arg value Word.
		if m.valueMatchesWord(pf.Value) {
			return true, name
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

// valueMatchesFlag checks a flag's value using conservative word operations
// (over-match on opaque).
func (m *FlagMatcher) valueMatchesFlag(
	f ParsedFlag,
) bool {
	if m.ValueCouldContain == "" &&
		m.ValueMayHavePrefix == "" {
		return true
	}
	return m.valueMatchesWord(f.Value)
}

// valueMatchesWord checks a Word value. Over-matches if
// the Word is opaque.
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

// textMayMatch checks value conditions against a plain
// string. Used for short-flag embedded values where the
// value is part of the token text, not a separate Word.
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

// SubcmdMatcher matches the first positional argument
// as a subcommand name.
type SubcmdMatcher struct {
	Names []string
}

func (m *SubcmdMatcher) Match(
	input ParseResult,
) (bool, ParseResult, string) {
	// Match the first raw argument if it does not look like a flag.
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

// AlwaysMatcher always matches, passing through the input
// unchanged.
type AlwaysMatcher struct{}

func (*AlwaysMatcher) Match(
	input ParseResult,
) (bool, ParseResult, string) {
	return true, input, ""
}
