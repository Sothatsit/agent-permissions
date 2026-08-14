package rules

import (
	"testing"
)

// --- stripComments: line comments ---

func TestStripLineComment(t *testing.T) {
	s := &langSyntax{
		LineComments: []string{"#"},
	}
	got := s.stripComments("x = 1 # comment\ny = 2")
	want := "x = 1 \ny = 2"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripLineCommentPreservesStrings(
	t *testing.T,
) {
	s := &langSyntax{
		Quotes:       []quoteDef{{Delim: `"`}},
		LineComments: []string{"#"},
	}
	// # inside a string should not be treated as a comment.
	got := s.stripComments(`x = "has # in it"`)
	want := `x = "has # in it"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripSlashSlashComment(t *testing.T) {
	s := &langSyntax{
		LineComments: []string{"//"},
	}
	got := s.stripComments("x = 1 // comment\ny = 2")
	want := "x = 1 \ny = 2"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- stripComments: block comments ---

func TestStripBlockComment(t *testing.T) {
	s := &langSyntax{
		BlockComments: []blockComment{{"/*", "*/"}},
	}
	got := s.stripComments("a /* block */ b")
	want := "a  b"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripBlockCommentMultiline(t *testing.T) {
	s := &langSyntax{
		BlockComments: []blockComment{{"/*", "*/"}},
	}
	got := s.stripComments("a /*\nblock\n*/ b")
	want := "a  b"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripNilSyntax(t *testing.T) {
	var s *langSyntax
	got := s.stripComments("# not stripped")
	if got != "# not stripped" {
		t.Errorf("nil syntax should be a no-op")
	}
}

// --- SKIP/FAIL matching ---

func TestMatchOutsideString(t *testing.T) {
	s := &langSyntax{
		Quotes: []quoteDef{{Delim: `"`}},
	}
	m := s.match(`\bdangerous\b`)
	if !m.check("dangerous()") {
		t.Error("should match bare code")
	}
}

func TestMatchInsideStringSkipped(t *testing.T) {
	s := &langSyntax{
		Quotes: []quoteDef{{Delim: `"`}},
	}
	m := s.match(`\bdangerous\b`)
	if m.check(`x = "dangerous"`) {
		t.Error("should not match inside string")
	}
}

func TestMatchAfterString(t *testing.T) {
	s := &langSyntax{
		Quotes: []quoteDef{{Delim: `"`}},
	}
	m := s.match(`\bdangerous\b`)
	if !m.check(`x = "safe"; dangerous()`) {
		t.Error("should match code after string")
	}
}

func TestMatchEscapedQuote(t *testing.T) {
	s := &langSyntax{
		Quotes: []quoteDef{{Delim: `"`}},
	}
	m := s.match(`\bdangerous\b`)
	// The escaped quote should not end the string.
	if m.check(`x = "has \" dangerous"`) {
		t.Error(
			"should not match — dangerous " +
				"is inside escaped string")
	}
}

func TestMatchMultilineString(t *testing.T) {
	s := &langSyntax{
		Quotes: []quoteDef{
			{Delim: `"""`, Multiline: true},
			{Delim: `"`},
		},
	}
	m := s.match(`\bdangerous\b`)
	code := `x = """
dangerous
"""`
	if m.check(code) {
		t.Error(
			"should not match inside " +
				"multiline string")
	}
}

func TestMatchNoQuotes(t *testing.T) {
	// A syntax with no quotes should still match.
	s := &langSyntax{}
	m := s.match(`\bdangerous\b`)
	if !m.check("dangerous()") {
		t.Error("should match with no quotes")
	}
}

func TestMatchMultipleStringsBeforeCode(t *testing.T) {
	s := &langSyntax{
		Quotes: []quoteDef{{Delim: `"`}},
	}
	m := s.match(`\bdangerous\b`)
	code := `a = "safe"; b = "also safe"; dangerous()`
	if !m.check(code) {
		t.Error(
			"should match after skipping " +
				"multiple strings")
	}
}

// --- skipCache ---

func TestSkipCacheComputedOnce(t *testing.T) {
	s := &langSyntax{
		Quotes: []quoteDef{{Delim: `"`}},
	}
	p1 := s.stringSkipPattern()
	p2 := s.stringSkipPattern()
	if p1 != p2 {
		t.Error("cache should return same value")
	}

	if !s.skipCacheSet {
		t.Error("skipCacheSet should be true")
	}
}

func TestSkipCacheEmptyQuotes(t *testing.T) {
	s := &langSyntax{}
	p := s.stringSkipPattern()
	if p != "" {
		t.Errorf("expected empty, got %q", p)
	}

	// Second call should not recompute.
	if !s.skipCacheSet {
		t.Error("skipCacheSet should be true")
	}
}

// --- Deny builder ---

func TestMatchBuilderDeny(t *testing.T) {
	s := &langSyntax{}
	rule := s.match(`\bsystem\b`).Deny("bad")
	if !rule.Check("system()") {
		t.Error("rule should match")
	}

	if rule.Action.Reason != "bad" {
		t.Errorf("reason = %q", rule.Action.Reason)
	}
}

// --- reAlternation ---

func TestReAlternation(t *testing.T) {
	got := reAlternation(
		[]string{"foo.bar", "baz"})
	want := `foo\.bar|baz`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
