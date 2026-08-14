// Package word provides operations on bash AST Words
// (mvdan.cc/sh/v3/syntax.Word).
//
// Matching operations (Equal, HasPrefix, Contains) return
// a tri-state Match value: Yes, No, or Maybe. The caller
// chooses the threshold:
//
//   - == Yes: strict - only when the value is certain.
//     Use for structural parsing where correctness
//     requires knowing the exact value (e.g. detecting
//     "--" separator, "-exec" in find).
//   - != No: conservative - matches unless we're certain
//     it doesn't. Use for deny/ask rules where false
//     positives are safe but false negatives are not
//     (e.g. checking if a flag value contains "exec=").
package word

import (
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

var printer = syntax.NewPrinter()

// --- Construction ---

// Lit creates a Word containing a single Lit part.
func Lit(s string) *syntax.Word {
	return &syntax.Word{
		Parts: []syntax.WordPart{
			&syntax.Lit{Value: s},
		},
	}
}

// FromStrings creates Words from strings, each containing
// a single Lit part.
func FromStrings(ss []string) []*syntax.Word {
	words := make([]*syntax.Word, len(ss))
	for i, s := range ss {
		words[i] = Lit(s)
	}
	return words
}

// --- Text resolution ---

// Text resolves a Word to text after literal quote and backslash removal.
// Opaque content (ParamExp, CmdSubst) keeps its source representation. The
// shell may still change the value through pathname, tilde, brace, or locale
// expansion. Use this only at boundaries that need a string. For comparisons
// and matching, prefer the strict or conservative operations below.
func Text(w *syntax.Word) string {
	if w == nil {
		return ""
	}
	// Fast path: single Lit (the overwhelmingly common
	// case - plain args like "ls", "-la", "foo.txt")
	// or single non-dollar SglQuoted ('hello').
	if len(w.Parts) == 1 {
		if lit, ok := w.Parts[0].(*syntax.Lit); ok {
			return UnescapeBackslashes(lit.Value)
		}
		if sq, ok := w.Parts[0].(*syntax.SglQuoted); ok && !sq.Dollar {
			return sq.Value
		}
	}
	var b strings.Builder
	for _, part := range w.Parts {
		writePartText(&b, part)
	}
	return b.String()
}

// Texts resolves a slice of Words to strings.
func Texts(words []*syntax.Word) []string {
	texts := make([]string, len(words))
	for i, w := range words {
		texts[i] = Text(w)
	}
	return texts
}

func writePartText(
	b *strings.Builder, part syntax.WordPart,
) {
	switch p := part.(type) {
	case *syntax.Lit:
		b.WriteString(UnescapeBackslashes(p.Value))
	case *syntax.SglQuoted:
		if p.Dollar {
			// ANSI-C quoting - can't resolve escape
			// sequences; use printer for source form.
			printer.Print(b, p)
		} else {
			b.WriteString(p.Value)
		}
	case *syntax.DblQuoted:
		for _, inner := range p.Parts {
			writePartText(b, inner)
		}
	default:
		// ParamExp, CmdSubst, etc. - use printer
		// for source representation.
		printer.Print(b, part)
	}
}

// --- Introspection ---

// Static reports whether a Word contains no opaque AST parts such as ParamExp,
// CmdSubst, or ANSI-C quoting. It does not account for later pathname, tilde,
// brace, or locale expansion.
func Static(w *syntax.Word) bool {
	if w == nil {
		return true
	}
	for _, part := range w.Parts {
		if !partStatic(part) {
			return false
		}
	}
	return true
}

// HasUnquotedGlob reports pathname expansion syntax in unquoted literal parts.
// Quoted literal parts cannot expand paths and therefore do not count.
func HasUnquotedGlob(w *syntax.Word) bool {
	for _, part := range w.Parts {
		lit, ok := part.(*syntax.Lit)
		if !ok {
			continue
		}
		for i := 0; i < len(lit.Value); i++ {
			if lit.Value[i] == '\\' && i+1 < len(lit.Value) {
				i++
				continue
			}
			if lit.Value[i] == '*' || lit.Value[i] == '?' ||
				lit.Value[i] == '[' {
				return true
			}
		}
	}
	return false
}

func partStatic(part syntax.WordPart) bool {
	switch p := part.(type) {
	case *syntax.Lit:
		return true
	case *syntax.SglQuoted:
		return !p.Dollar
	case *syntax.DblQuoted:
		for _, inner := range p.Parts {
			if !partStatic(inner) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// OpaqueReason returns a human-readable reason why a Word
// is not static, or "" if the Word is static.
func OpaqueReason(w *syntax.Word) string {
	if w == nil {
		return ""
	}
	for _, part := range w.Parts {
		if r := partOpaqueReason(part); r != "" {
			return r
		}
	}
	return ""
}

func partOpaqueReason(part syntax.WordPart) string {
	switch p := part.(type) {
	case *syntax.Lit:
		return ""
	case *syntax.SglQuoted:
		if p.Dollar {
			return "ANSI-C quoting ($'...')"
		}
		return ""
	case *syntax.DblQuoted:
		for _, inner := range p.Parts {
			if r := partOpaqueReason(inner); r != "" {
				return r
			}
		}
		return ""
	case *syntax.ParamExp:
		return "variable expansion"
	case *syntax.CmdSubst:
		return "command substitution"
	default:
		return "unsupported syntax"
	}
}

// ExpansionReason returns a human-readable reason if the
// Word contains variable expansion or ANSI-C quoting.
// This content's runtime value differs from the source
// representation returned by Text. Returns "" when the
// Word has no expansion. CmdSubst is NOT flagged: its
// source form ($(...)) is preserved by Text and
// faithfully re-parsed by breakdownAt.
func ExpansionReason(w *syntax.Word) string {
	if w == nil {
		return ""
	}
	for _, part := range w.Parts {
		if r := partExpansionReason(part); r != "" {
			return r
		}
	}
	return ""
}

func partExpansionReason(part syntax.WordPart) string {
	switch p := part.(type) {
	case *syntax.Lit, *syntax.CmdSubst:
		return ""
	case *syntax.SglQuoted:
		if p.Dollar {
			return "ANSI-C quoting ($'...')"
		}
		return ""
	case *syntax.DblQuoted:
		for _, inner := range p.Parts {
			if r := partExpansionReason(inner); r != "" {
				return r
			}
		}
		return ""
	case *syntax.ParamExp:
		return "variable expansion"
	default:
		return "unsupported syntax"
	}
}

// --- Matching ---

// Match is a tri-state result for Word comparisons.
type Match int

const (
	No    Match = iota // definitely does not match
	Maybe              // can't determine - opaque content
	Yes                // definitely matches
)

// Equal checks whether the Word's text equals s. Walks
// parts directly without allocation. For mixed words
// (static prefix + opaque suffix), returns No when the
// static prefix rules out a match.
func Equal(w *syntax.Word, s string) Match {
	if w == nil {
		if s == "" {
			return Yes
		}
		return No
	}
	// Fast path: single Lit (overwhelmingly common).
	if len(w.Parts) == 1 {
		if lit, ok := w.Parts[0].(*syntax.Lit); ok {
			if litEqual(lit.Value, s) {
				return Yes
			}
			return No
		}
	}
	si, m := matchParts(w.Parts, s, 0)
	if m != Yes {
		return m
	}
	if si == len(s) {
		return Yes
	}
	return No
}

// HasPrefix checks whether the Word's text starts with
// s. For mixed words, returns a definitive answer when
// the static prefix is long enough to decide.
func HasPrefix(w *syntax.Word, s string) Match {
	if len(s) == 0 {
		return Yes
	}
	if w == nil {
		return No
	}
	// Fast path: single Lit.
	if len(w.Parts) == 1 {
		if lit, ok := w.Parts[0].(*syntax.Lit); ok {
			if litHasPrefix(lit.Value, s) {
				return Yes
			}
			return No
		}
	}
	si, m := matchPartsPrefix(w.Parts, s, 0)
	if m != Yes {
		return m
	}
	if si >= len(s) {
		return Yes
	}
	return No // word shorter than s
}

// Contains checks whether the Word's text contains s.
// Single-part fast path avoids allocation. For multi-part
// words, checks each static part individually - finding
// s in any part returns Yes even if the word has opaque
// parts elsewhere. Falls back to Text() only for
// cross-part boundary matches in fully-static words.
func Contains(w *syntax.Word, s string) Match {
	if w == nil {
		if s == "" {
			return Yes
		}
		return No
	}
	if s == "" {
		return Yes
	}
	// Fast path: single Lit.
	if len(w.Parts) == 1 {
		if lit, ok := w.Parts[0].(*syntax.Lit); ok {
			if litContains(lit.Value, s) {
				return Yes
			}
			return No
		}
	}
	// Check each part individually. If any static
	// part contains s, return Yes - s is definitely
	// present regardless of opaque parts elsewhere.
	hasOpaque := false
	for _, part := range w.Parts {
		found, opaque := partContains(part, s)
		if found {
			return Yes
		}
		if opaque {
			hasOpaque = true
		}
	}
	if hasOpaque {
		return Maybe
	}
	// All static, no single part matched. Check
	// cross-boundary matches via Text(). This is the
	// only path that allocates, and it's rare
	// (multi-part fully-static words where the match
	// spans parts).
	if strings.Contains(Text(w), s) {
		return Yes
	}
	return No
}

// --- Match internals: Equal ---

// matchParts walks parts comparing against s[si:] for
// exact matching. Returns (newSi, Yes) when all part
// characters matched, (_, No) on static mismatch or
// word longer than s, (_, Maybe) on opaque content.
func matchParts(
	parts []syntax.WordPart, s string, si int,
) (int, Match) {
	for _, part := range parts {
		newSi, m := matchPart(part, s, si)
		if m != Yes {
			return 0, m
		}
		si = newSi
	}
	return si, Yes
}

func matchPart(
	part syntax.WordPart, s string, si int,
) (int, Match) {
	switch p := part.(type) {
	case *syntax.Lit:
		newSi := litMatch(p.Value, s, si)
		if newSi < 0 {
			return 0, No
		}
		return newSi, Yes
	case *syntax.SglQuoted:
		if p.Dollar {
			return 0, Maybe
		}
		newSi := plainMatch(p.Value, s, si)
		if newSi < 0 {
			return 0, No
		}
		return newSi, Yes
	case *syntax.DblQuoted:
		return matchParts(p.Parts, s, si)
	default:
		return 0, Maybe
	}
}

// litMatch compares unescaped litVal against s[si:].
// Returns new si on success, -1 on mismatch (including
// when the word is longer than s).
func litMatch(litVal string, s string, si int) int {
	for li := 0; li < len(litVal); li++ {
		ch := litVal[li]
		if ch == '\\' && li+1 < len(litVal) {
			li++
			ch = litVal[li]
		}
		if si >= len(s) || s[si] != ch {
			return -1
		}
		si++
	}
	return si
}

// plainMatch compares val against s[si:] byte-for-byte
// (no escape handling). Used for SglQuoted values.
func plainMatch(val string, s string, si int) int {
	end := si + len(val)
	if end > len(s) {
		return -1
	}
	if s[si:end] != val {
		return -1
	}
	return end
}

// litEqual compares unescaped litVal against s for
// exact equality. No allocation when no backslashes.
func litEqual(litVal, s string) bool {
	if !strings.Contains(litVal, `\`) {
		return litVal == s
	}
	return litMatch(litVal, s, 0) == len(s)
}

// --- Match internals: HasPrefix ---

// matchPartsPrefix walks parts comparing against s[si:]
// for prefix matching. Returns early with (len(s), Yes)
// as soon as s is fully consumed. Returns (newSi, Yes)
// when parts are exhausted, (_, No) on mismatch,
// (_, Maybe) on opaque content.
func matchPartsPrefix(
	parts []syntax.WordPart, s string, si int,
) (int, Match) {
	for _, part := range parts {
		if si >= len(s) {
			return si, Yes
		}
		newSi, m := matchPartPrefix(part, s, si)
		if m != Yes {
			return 0, m
		}
		si = newSi
	}
	return si, Yes
}

func matchPartPrefix(
	part syntax.WordPart, s string, si int,
) (int, Match) {
	switch p := part.(type) {
	case *syntax.Lit:
		newSi := litMatchPrefix(p.Value, s, si)
		if newSi < 0 {
			return 0, No
		}
		return newSi, Yes
	case *syntax.SglQuoted:
		if p.Dollar {
			return 0, Maybe
		}
		newSi := plainMatchPrefix(p.Value, s, si)
		if newSi < 0 {
			return 0, No
		}
		return newSi, Yes
	case *syntax.DblQuoted:
		return matchPartsPrefix(p.Parts, s, si)
	default:
		return 0, Maybe
	}
}

// litMatchPrefix compares unescaped litVal against
// s[si:], succeeding early when si reaches len(s).
// Returns new si, or -1 on character mismatch.
func litMatchPrefix(
	litVal string, s string, si int,
) int {
	for li := 0; li < len(litVal); li++ {
		if si >= len(s) {
			return si
		}
		ch := litVal[li]
		if ch == '\\' && li+1 < len(litVal) {
			li++
			ch = litVal[li]
		}
		if s[si] != ch {
			return -1
		}
		si++
	}
	return si
}

// plainMatchPrefix compares val against s[si:],
// succeeding early when si reaches len(s).
func plainMatchPrefix(
	val string, s string, si int,
) int {
	for i := 0; i < len(val); i++ {
		if si >= len(s) {
			return si
		}
		if s[si] != val[i] {
			return -1
		}
		si++
	}
	return si
}

// litHasPrefix checks whether unescaped litVal starts
// with s. No allocation when no backslashes.
func litHasPrefix(litVal, s string) bool {
	if !strings.Contains(litVal, `\`) {
		return strings.HasPrefix(litVal, s)
	}
	return litMatchPrefix(litVal, s, 0) >= len(s)
}

// --- Match internals: Contains ---

// partContains checks whether a single part's resolved
// text contains s. Does not check cross-part boundaries
// within DblQuoted - the caller handles that via the
// Text() fallback for fully-static words.
func partContains(
	part syntax.WordPart, s string,
) (found bool, opaque bool) {
	switch p := part.(type) {
	case *syntax.Lit:
		return litContains(p.Value, s), false
	case *syntax.SglQuoted:
		if p.Dollar {
			return false, true
		}
		return strings.Contains(p.Value, s), false
	case *syntax.DblQuoted:
		hasOpaque := false
		for _, inner := range p.Parts {
			f, o := partContains(inner, s)
			if f {
				return true, false
			}
			if o {
				hasOpaque = true
			}
		}
		return false, hasOpaque
	default:
		return false, true
	}
}

// litContains checks whether unescaped litVal contains
// s. No allocation when no backslashes.
func litContains(litVal, s string) bool {
	if !strings.Contains(litVal, `\`) {
		return strings.Contains(litVal, s)
	}
	return strings.Contains(
		UnescapeBackslashes(litVal), s)
}

// --- Strict convenience ---
//
// These return true only when the Match is Yes (the
// word definitely matches). Use for structural parsing
// where correctness requires knowing the exact value.

// DefinitelyEqual returns true if the Word's text
// definitely equals s. Requires all parts to be static.
func DefinitelyEqual(w *syntax.Word, s string) bool {
	return Equal(w, s) == Yes
}

// DefinitelyHasPrefix returns true if the Word's text
// definitely starts with s. Can succeed for mixed words
// when the static prefix is long enough.
func DefinitelyHasPrefix(
	w *syntax.Word, s string,
) bool {
	return HasPrefix(w, s) == Yes
}

// DefinitelyContains returns true if the Word's text
// definitely contains s. Can succeed for mixed words
// when s is found within a static part.
func DefinitelyContains(
	w *syntax.Word, s string,
) bool {
	return Contains(w, s) == Yes
}

// --- Conservative convenience ---
//
// These return true when the Match is Yes or Maybe.
// Use for deny/ask rules where false positives are
// safe but false negatives are not.

// MayEqual returns true if the Word could equal s.
func MayEqual(w *syntax.Word, s string) bool {
	return Equal(w, s) != No
}

// MayContain returns true if the Word could contain s.
func MayContain(w *syntax.Word, s string) bool {
	return Contains(w, s) != No
}

// MayHavePrefix returns true if the Word could start
// with s.
func MayHavePrefix(w *syntax.Word, s string) bool {
	return HasPrefix(w, s) != No
}

// --- Splitting ---

// SplitEq splits a --flag=value Word at the first = in
// the first Lit part, respecting backslash escapes. A
// \= in the raw Lit is a literal = in the resolved text
// and is a valid split point. Returns the flag name text
// and a synthetic Word for the value. The value Word
// preserves the original structure - if the value
// contains ParamExp or CmdSubst, they appear intact
// (e.g. --flag=$VALUE produces name="--flag" and a
// value Word containing the ParamExp).
//
// Returns ("", nil) if no = found or the first part is
// not a Lit.
func SplitEq(
	w *syntax.Word,
) (string, *syntax.Word) {
	if len(w.Parts) == 0 {
		return "", nil
	}
	lit, ok := w.Parts[0].(*syntax.Lit)
	if !ok {
		return "", nil
	}
	// Find the first = in the resolved text. An
	// escaped \= produces a literal = and is a valid
	// split point - nameEnd is the raw position of
	// the \, valueStart is the position after the =.
	nameEnd := -1
	valueStart := -1
	for i := 0; i < len(lit.Value); i++ {
		if lit.Value[i] == '\\' &&
			i+1 < len(lit.Value) {
			if lit.Value[i+1] == '=' {
				nameEnd = i
				valueStart = i + 2
				break
			}
			i++ // skip escaped non-= char
			continue
		}
		if lit.Value[i] == '=' {
			nameEnd = i
			valueStart = i + 1
			break
		}
	}
	if nameEnd < 0 {
		return "", nil
	}
	name := UnescapeBackslashes(lit.Value[:nameEnd])

	var valueParts []syntax.WordPart
	after := lit.Value[valueStart:]
	if after != "" {
		valueParts = append(valueParts,
			&syntax.Lit{Value: after})
	}
	valueParts = append(valueParts, w.Parts[1:]...)
	if len(valueParts) == 0 {
		// --flag= with nothing after - empty value.
		valueParts = []syntax.WordPart{
			&syntax.Lit{Value: ""},
		}
	}
	return name, &syntax.Word{Parts: valueParts}
}

// SplitPrefix splits a -nVALUE Word where the flag name
// occupies nameLen bytes of the first Lit part. Returns
// the flag name text and a synthetic Word for the value.
//
// Returns ("", nil) if the first part is not a Lit or
// nameLen exceeds the Lit. Returns (name, nil) if
// nameLen exactly matches the first Lit and there are no
// remaining parts (bare prefix flag, no value).
func SplitPrefix(
	w *syntax.Word, nameLen int,
) (string, *syntax.Word) {
	if len(w.Parts) == 0 || nameLen <= 0 {
		return "", nil
	}
	lit, ok := w.Parts[0].(*syntax.Lit)
	if !ok {
		return "", nil
	}
	if nameLen > len(lit.Value) {
		return "", nil
	}
	name := UnescapeBackslashes(lit.Value[:nameLen])

	var valueParts []syntax.WordPart
	after := lit.Value[nameLen:]
	if after != "" {
		valueParts = append(valueParts,
			&syntax.Lit{Value: after})
	}
	valueParts = append(valueParts, w.Parts[1:]...)
	if len(valueParts) == 0 {
		return name, nil
	}
	return name, &syntax.Word{Parts: valueParts}
}

// --- Utilities ---

// DirectPath returns a path suitable for direct
// execution. Prepends "./" for relative paths (e.g.
// "script.sh" -> "./script.sh") but leaves absolute
// paths unchanged.
func DirectPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return "./" + path
}

// UnescapeBackslashes strips backslash escapes. In bash,
// \c outside quotes produces literal c for any character
// c. mvdan/sh preserves these backslashes in the AST.
func UnescapeBackslashes(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			b.WriteByte(s[i])
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
