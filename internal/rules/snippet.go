// Package rules provides shared infrastructure for
// snippet pattern matching. Language-specific patterns
// and rules are defined in python.go, perl.go, ruby.go,
// and node.go. Registry.go wires them together.
package rules

import (
	"regexp"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
)

// --- Language syntax types ---

// langSyntax describes how a language uses quotes and
// comments, for comment stripping before regex matching.
type langSyntax struct {
	// Quotes lists string literal delimiters, ordered
	// longest-first so """ matches before ".
	Quotes []quoteDef
	// LineComments lists line comment prefixes
	// (e.g. "#", "//").
	LineComments []string
	// BlockComments lists block comment delimiters.
	BlockComments []blockComment

	// skipCache holds the SKIP regex alternation for
	// string literals, built once by stringSkipPattern.
	skipCache    string
	skipCacheSet bool
}

// quoteDef describes one kind of string literal.
// Backslash escapes are always handled.
type quoteDef struct {
	Delim     string // delimiter (open and close)
	Multiline bool   // can span newlines
}

// blockComment describes a block comment style.
type blockComment struct {
	Open  string
	Close string
}

// --- Comment stripping ---

// stripComments removes comments from code, preserving
// string literals. Returns code with comments excised.
func (s *langSyntax) stripComments(
	code string,
) string {
	if s == nil {
		return code
	}
	var b strings.Builder
	b.Grow(len(code))
	i := 0
	for i < len(code) {
		if n := skipQuote(code, i, s); n > 0 {
			b.WriteString(code[i : i+n])
			i += n
			continue
		}
		if n := skipLineComment(code, i, s); n > 0 {
			i += n
			continue
		}
		if n := skipBlockComment(
			code, i, s); n > 0 {
			i += n
			continue
		}
		b.WriteByte(code[i])
		i++
	}
	return b.String()
}

// skipQuote returns the length of the string literal
// starting at code[i], or 0 if none starts there.
func skipQuote(
	code string, i int, syntax *langSyntax,
) int {
	for _, q := range syntax.Quotes {
		if !hasPrefix(code, i, q.Delim) {
			continue
		}
		j := i + len(q.Delim)
		for j < len(code) {
			if code[j] == '\\' &&
				j+1 < len(code) {
				j += 2
				continue
			}
			if hasPrefix(code, j, q.Delim) {
				j += len(q.Delim)
				return j - i
			}
			if !q.Multiline &&
				code[j] == '\n' {
				return j - i
			}
			j++
		}
		return j - i
	}
	return 0
}

// skipLineComment returns the length of the line
// comment starting at code[i], or 0 if none starts
// there. The trailing newline is not included.
func skipLineComment(
	code string, i int, syntax *langSyntax,
) int {
	for _, prefix := range syntax.LineComments {
		if !hasPrefix(code, i, prefix) {
			continue
		}
		j := i
		for j < len(code) && code[j] != '\n' {
			j++
		}
		return j - i
	}
	return 0
}

// skipBlockComment returns the length of the block
// comment starting at code[i], or 0 if none starts
// there.
func skipBlockComment(
	code string, i int, syntax *langSyntax,
) int {
	for _, bc := range syntax.BlockComments {
		if !hasPrefix(code, i, bc.Open) {
			continue
		}
		j := i + len(bc.Open)
		for j < len(code) {
			if hasPrefix(code, j, bc.Close) {
				j += len(bc.Close)
				return j - i
			}
			j++
		}
		return j - i
	}
	return 0
}

func hasPrefix(s string, i int, prefix string) bool {
	return i+len(prefix) <= len(s) &&
		s[i:i+len(prefix)] == prefix
}

// interpolatedLiteralContents returns the contents of quoted literals chosen
// by include. It only finds quote boundaries and escapes already understood by
// the shared matcher. Language-specific callbacks decide which literals can
// evaluate interpolation; their contents remain deliberately conservative.
func interpolatedLiteralContents(
	code string,
	syntax *langSyntax,
	include func(string, int, quoteDef, string) bool,
) []string {
	var contents []string
	for i := 0; i < len(code); {
		matchedQuote := false
		for _, quote := range syntax.Quotes {
			if !hasPrefix(code, i, quote.Delim) {
				continue
			}

			length := skipQuote(code, i, syntax)
			end := i + length
			contentEnd := end
			if end >= i+2*len(quote.Delim) &&
				hasPrefix(code, end-len(quote.Delim), quote.Delim) {
				contentEnd -= len(quote.Delim)
			}
			content := code[i+len(quote.Delim) : contentEnd]
			if include(code, i, quote, content) {
				contents = append(contents, content)
			}
			i = end
			matchedQuote = true
			break
		}
		if matchedQuote {
			continue
		}
		if length := skipLineComment(code, i, syntax); length > 0 {
			i += length
			continue
		}
		if length := skipBlockComment(code, i, syntax); length > 0 {
			i += length
			continue
		}
		i++
	}
	return contents
}

func hasUnescapedPrefix(text, prefix string) bool {
	for offset := 0; offset < len(text); {
		relative := strings.Index(text[offset:], prefix)
		if relative < 0 {
			return false
		}
		index := offset + relative
		backslashes := 0
		for i := index - 1; i >= 0 && text[i] == '\\'; i-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return true
		}
		offset = index + len(prefix)
	}
	return false
}

// --- Builder ---

// matchBuilder wraps a check function so you can call
// .Deny(reason) to produce a SnippetRule - same pattern
// as model.RuleBuilder for command rules.
type matchBuilder struct {
	check func(code string) bool
}

func (b matchBuilder) Deny(
	reason string,
) model.SnippetRule {
	return model.SnippetRule{
		Check:  b.check,
		Action: model.DenyAction(reason),
	}
}

// --- Core matching ---

// match compiles a regex that skips over string literals
// (SKIP/FAIL) so patterns only match in code, not inside
// strings. Comments should already be stripped by the
// caller. Patterns must use only non-capturing groups
// (?:) - a capturing () would shift group numbering
// and silently break the SKIP/FAIL detection.
func (s *langSyntax) match(
	pattern string,
) matchBuilder {
	skip := s.stringSkipPattern()
	var full string
	if skip == "" {
		full = `(` + pattern + `)`
	} else {
		full = skip + `|(` + pattern + `)`
	}
	re := regexp.MustCompile(full)
	return matchBuilder{check: func(
		code string,
	) bool {
		for {
			loc := re.FindStringSubmatchIndex(
				code)
			if loc == nil {
				return false
			}
			// Group 1 matched - pattern found
			// outside a string literal.
			if loc[2] >= 0 {
				return true
			}
			// String was consumed; advance past
			// it and continue scanning.
			code = code[loc[1]:]
		}
	}}
}

// stringSkipPattern returns the cached SKIP regex
// alternation for this language's string literals.
// Built once on first call.
func (s *langSyntax) stringSkipPattern() string {
	if s == nil || s.skipCacheSet {
		return s.skipCache
	}
	s.skipCacheSet = true
	if len(s.Quotes) == 0 {
		return s.skipCache
	}
	var parts []string
	for _, q := range s.Quotes {
		d := regexp.QuoteMeta(q.Delim)
		if q.Multiline {
			parts = append(parts,
				d+`[\s\S]*?`+d)
		} else {
			c := regexp.QuoteMeta(
				string(q.Delim[0]))
			parts = append(parts,
				d+`(?:[^`+c+`\\\n]|\\.)*`+d)
		}
	}
	s.skipCache = strings.Join(parts, "|")
	return s.skipCache
}

// --- Shared pattern helpers ---

// reAlternation builds a regex alternation from names,
// escaping each for use in a regex.
func reAlternation(names []string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = regexp.QuoteMeta(n)
	}
	return strings.Join(parts, "|")
}
