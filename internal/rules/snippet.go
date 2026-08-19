// Package rules provides shared infrastructure for snippet pattern matching.
// Language-specific patterns and rules are defined in python.go, perl.go,
// ruby.go, and node.go. Registry.go wires them together.
package rules

import (
	"regexp"
	"strings"

	"github.com/sothatsit/agent-permissions/internal/model"
)

// --- Language syntax types ---

// langSyntax describes how a language uses quotes and comments, for comment
// stripping before regex matching.
type langSyntax struct {
	// Quotes are ordered longest-first, so """ matches before ".
	Quotes        []quoteDef
	LineComments  []string
	BlockComments []blockComment

	// skipCache is built once by stringSkipPattern.
	skipCache    string
	skipCacheSet bool
}

// quoteDef describes one kind of string literal. Backslash escapes are always
// handled.
type quoteDef struct {
	Delim     string // delimiter (open and close)
	Multiline bool   // can span newlines
}

type blockComment struct {
	Open  string
	Close string
}

// --- Comment stripping ---

// stripComments removes comments while preserving string literals.
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

// skipQuote returns the length of the string literal starting at code[i], or 0
// if none starts there.
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

// skipLineComment excludes the trailing newline.
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

// skipBlockComment returns the length of the block comment starting at code[i],
// or 0 if none starts there.
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

// interpolatedLiteralContents returns the contents of the quoted literals
// include chooses. It only finds quote boundaries and escapes the shared
// matcher already understands, so contents stay deliberately conservative.
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

// matchBuilder wraps a check function so .Deny(reason) produces a SnippetRule,
// mirroring model.RuleBuilder for command rules.
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

// match compiles a regex that skips string literals, so patterns only match
// code. The caller must have stripped comments already. Patterns must use only
// non-capturing groups: a capturing one shifts group numbering and silently
// breaks the SKIP/FAIL detection.
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
			// Group 1 matched, so the pattern is outside a string
			// literal.
			if loc[2] >= 0 {
				return true
			}

			// A string was consumed, so advance past it.
			code = code[loc[1]:]
		}
	}}
}

// stringSkipPattern caches this language's string-literal SKIP alternation on
// first call.
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

func reAlternation(names []string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = regexp.QuoteMeta(n)
	}

	return strings.Join(parts, "|")
}
