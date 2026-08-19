// Package word provides operations on bash AST Words
// (mvdan.cc/sh/v3/syntax.Word).
//
// Matching operations (Equal, HasPrefix, Contains) return a tri-state Match
// value: Yes, No, or Maybe. The caller chooses the threshold:
//
//   - == Yes: strict - only when the value is certain. Use for structural
//     parsing where correctness requires knowing the exact value (e.g.
//     detecting "--" separator, "-exec" in find).
//   - != No: conservative - matches unless we're certain it doesn't. Use for
//     deny/ask rules where false positives are safe but false negatives are not
//     (e.g. checking if a flag value contains "exec=").
package word

import (
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

var printer = syntax.NewPrinter()

// --- Construction ---

func Lit(s string) *syntax.Word {
	return &syntax.Word{
		Parts: []syntax.WordPart{
			&syntax.Lit{Value: s},
		},
	}
}

func FromStrings(ss []string) []*syntax.Word {
	words := make([]*syntax.Word, len(ss))
	for i, s := range ss {
		words[i] = Lit(s)
	}

	return words
}

// --- Text resolution ---

// Text resolves a Word to text after literal quote and backslash removal,
// keeping the source representation of opaque content. The shell may still
// change the value through pathname, tilde, brace, or locale expansion, so use
// this only at boundaries that need a string, and the operations below for
// matching.
func Text(w *syntax.Word) string {
	if w == nil {
		return ""
	}
	// Fast path for the overwhelmingly common single-part word.
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
			// ANSI-C escape sequences cannot be resolved, so print
			// the source form.
			printer.Print(b, p)
		} else {
			b.WriteString(p.Value)
		}
	case *syntax.DblQuoted:
		for _, inner := range p.Parts {
			writePartText(b, inner)
		}
	default:
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

// OpaqueReason says why a Word is not static, or "" if it is.
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

// ExpansionReason reports variable expansion or ANSI-C quoting, whose runtime
// value differs from the source representation Text returns. CmdSubst is not
// flagged: Text preserves its $(...) source form, which breakdownAt re-parses
// faithfully.
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

// Equal walks parts without allocating. A mixed word answers No when its static
// prefix rules out a match.
func Equal(w *syntax.Word, s string) Match {
	if w == nil {
		if s == "" {
			return Yes
		}

		return No
	}
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

// HasPrefix checks whether the Word's text starts with s. For mixed words,
// returns a definitive answer when the static prefix is long enough to decide.
func HasPrefix(w *syntax.Word, s string) Match {
	if len(s) == 0 {
		return Yes
	}
	if w == nil {
		return No
	}
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

// Contains checks each static part on its own, so s found in any part is Yes
// even when other parts are opaque. Only a cross-part match in a fully-static
// word falls back to Text.
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
	if len(w.Parts) == 1 {
		if lit, ok := w.Parts[0].(*syntax.Lit); ok {
			if litContains(lit.Value, s) {
				return Yes
			}

			return No
		}
	}

	// s inside a static part is definitely present, whatever the opaque
	// parts hold.
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
	// All static and no single part matched, so the only match left
	// spans parts. This is the one path that allocates, and it is
	// rare.
	if strings.Contains(Text(w), s) {
		return Yes
	}

	return No
}

// --- Match internals: Equal ---

// matchParts walks parts comparing against s[si:] for exact matching. Returns
// (newSi, Yes) when all part characters matched, (_, No) on static mismatch or
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

// litMatch returns the new si, or -1 on mismatch, including a word longer than
// s.
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

// plainMatch compares SglQuoted values byte-for-byte, with no escape handling.
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

// litEqual does not allocate when there are no backslashes.
func litEqual(litVal, s string) bool {
	if !strings.Contains(litVal, `\`) {
		return litVal == s
	}

	return litMatch(litVal, s, 0) == len(s)
}

// --- Match internals: HasPrefix ---

// matchPartsPrefix returns Yes as soon as s is consumed or the parts are
// exhausted, No on mismatch, and Maybe on opaque content.
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

// litMatchPrefix returns the new si, or -1 on mismatch.
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

// plainMatchPrefix succeeds early when si reaches len(s).
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

// litHasPrefix does not allocate when there are no backslashes.
func litHasPrefix(litVal, s string) bool {
	if !strings.Contains(litVal, `\`) {
		return strings.HasPrefix(litVal, s)
	}

	return litMatchPrefix(litVal, s, 0) >= len(s)
}

// --- Match internals: Contains ---

// partContains checks whether a single part's resolved text contains s. Does
// not check cross-part boundaries within DblQuoted - the caller handles that
// via the Text() fallback for fully-static words.
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

// litContains does not allocate when there are no backslashes.
func litContains(litVal, s string) bool {
	if !strings.Contains(litVal, `\`) {
		return strings.Contains(litVal, s)
	}

	return strings.Contains(
		UnescapeBackslashes(litVal), s)
}

// --- Strict convenience ---
//
// These return true only when the word definitely matches. Use
// them for structural parsing that needs the exact value.

// DefinitelyEqual requires every part to be static.
func DefinitelyEqual(w *syntax.Word, s string) bool {
	return Equal(w, s) == Yes
}

// DefinitelyHasPrefix can succeed on a mixed word whose static prefix is long
// enough.
func DefinitelyHasPrefix(
	w *syntax.Word, s string,
) bool {
	return HasPrefix(w, s) == Yes
}

// DefinitelyContains can succeed on a mixed word when s is inside a static
// part.
func DefinitelyContains(
	w *syntax.Word, s string,
) bool {
	return Contains(w, s) == Yes
}

// --- Conservative convenience ---
//
// These return true when the word matches or might match. Use
// them for deny and ask rules, where a false positive is safe but
// a false negative is not.

func MayEqual(w *syntax.Word, s string) bool {
	return Equal(w, s) != No
}

func MayContain(w *syntax.Word, s string) bool {
	return Contains(w, s) != No
}

func MayHavePrefix(w *syntax.Word, s string) bool {
	return HasPrefix(w, s) != No
}

// --- Splitting ---

// SplitEq splits a --flag=value Word at the first = in its first
// Lit part, where a \= counts as a literal = and a valid split
// point. The value Word keeps the original structure, so
// --flag=$VALUE yields name="--flag" and a value Word holding the
// ParamExp. Returns ("", nil) with no =, or when the first part is
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

	// An escaped \= produces a literal =, so nameEnd is the raw position of
	// the backslash and valueStart follows the =.
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

// SplitPrefix splits a -nVALUE Word whose flag name occupies nameLen bytes of
// the first Lit part. Returns ("", nil) when that part is not a Lit or nameLen
// overruns it, and (name, nil) for a bare prefix flag with no value.
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

// DirectPath prepends "./" to a relative path, leaving an absolute one alone.
func DirectPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}

	return "./" + path
}

// UnescapeBackslashes strips backslash escapes. In bash, \c outside quotes
// produces literal c for any character c. mvdan/sh preserves these backslashes
// in the AST.
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
