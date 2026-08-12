package word

import (
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

// --- helpers ---

func litWord(s string) *syntax.Word {
	return Lit(s)
}

func paramWord(name string) *syntax.Word {
	return &syntax.Word{
		Parts: []syntax.WordPart{
			&syntax.ParamExp{
				Param: &syntax.Lit{Value: name},
			},
		},
	}
}

func cmdSubWord() *syntax.Word {
	return &syntax.Word{
		Parts: []syntax.WordPart{
			&syntax.CmdSubst{},
		},
	}
}

func dblQuotedWord(
	parts ...syntax.WordPart,
) *syntax.Word {
	return &syntax.Word{
		Parts: []syntax.WordPart{
			&syntax.DblQuoted{Parts: parts},
		},
	}
}

func dollarSglWord(s string) *syntax.Word {
	return &syntax.Word{
		Parts: []syntax.WordPart{
			&syntax.SglQuoted{Value: s, Dollar: true},
		},
	}
}

// mixedWord creates a Word with a Lit prefix followed by
// a ParamExp. E.g. "--flag=$VAR".
func mixedWord(
	prefix string, paramName string,
) *syntax.Word {
	return &syntax.Word{
		Parts: []syntax.WordPart{
			&syntax.Lit{Value: prefix},
			&syntax.ParamExp{
				Param: &syntax.Lit{Value: paramName},
			},
		},
	}
}

// --- Text ---

func TestText(t *testing.T) {
	tests := []struct {
		name string
		w    *syntax.Word
		want string
	}{
		{"nil", nil, ""},
		{"lit", litWord("hello"), "hello"},
		{"sgl quoted", &syntax.Word{
			Parts: []syntax.WordPart{
				&syntax.SglQuoted{Value: "hi"},
			},
		}, "hi"},
		{"backslash", litWord(`he\llo`), "hello"},
		{"param prints source", paramWord("VAR"),
			"${VAR}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Text(tt.w)
			if got != tt.want {
				t.Errorf("Text() = %q, want %q",
					got, tt.want)
			}
		})
	}
}

func TestTexts(t *testing.T) {
	words := []*syntax.Word{
		litWord("a"), litWord("b"), litWord("c"),
	}
	got := Texts(words)
	if len(got) != 3 || got[0] != "a" ||
		got[1] != "b" || got[2] != "c" {
		t.Errorf("Texts() = %v", got)
	}
}

// --- Static ---

func TestStatic(t *testing.T) {
	tests := []struct {
		name string
		w    *syntax.Word
		want bool
	}{
		{"nil", nil, true},
		{"lit", litWord("foo"), true},
		{"sgl quoted", &syntax.Word{
			Parts: []syntax.WordPart{
				&syntax.SglQuoted{Value: "x"},
			},
		}, true},
		{"dbl quoted static",
			dblQuotedWord(
				&syntax.Lit{Value: "x"}), true},
		{"param", paramWord("X"), false},
		{"cmd sub", cmdSubWord(), false},
		{"dollar sgl", dollarSglWord("x"), false},
		{"dbl with param",
			dblQuotedWord(
				&syntax.Lit{Value: "a"},
				&syntax.ParamExp{
					Param: &syntax.Lit{Value: "X"},
				},
			), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Static(tt.w)
			if got != tt.want {
				t.Errorf("Static() = %v, want %v",
					got, tt.want)
			}
		})
	}
}

func TestHasUnquotedGlob(t *testing.T) {
	tests := []struct {
		name string
		word *syntax.Word
		want bool
	}{
		{"star", litWord("*.txt"), true},
		{"question", litWord("file?.txt"), true},
		{"class", litWord("file[0-9]"), true},
		{"escaped", litWord(`file\*.txt`), false},
		{"single quoted", &syntax.Word{Parts: []syntax.WordPart{
			&syntax.SglQuoted{Value: "*.txt"},
		}}, false},
		{"double quoted", dblQuotedWord(
			&syntax.Lit{Value: "*.txt"}), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasUnquotedGlob(tt.word); got != tt.want {
				t.Errorf("HasUnquotedGlob() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- OpaqueReason ---

func TestOpaqueReason(t *testing.T) {
	tests := []struct {
		name    string
		w       *syntax.Word
		wantSet bool
		wantSub string
	}{
		{"static", litWord("x"), false, ""},
		{"nil", nil, false, ""},
		{"param", paramWord("X"), true, "variable"},
		{"cmd sub", cmdSubWord(), true,
			"command substitution"},
		{"dollar sgl", dollarSglWord("x"), true,
			"ANSI-C"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := OpaqueReason(tt.w)
			if tt.wantSet && got == "" {
				t.Error("OpaqueReason() empty")
			}
			if !tt.wantSet && got != "" {
				t.Errorf("OpaqueReason() = %q", got)
			}
			if tt.wantSub != "" &&
				!strings.Contains(got, tt.wantSub) {
				t.Errorf(
					"OpaqueReason() = %q, "+
						"want substring %q",
					got, tt.wantSub)
			}
		})
	}
}

// --- ExpansionReason ---

func TestExpansionReason(t *testing.T) {
	tests := []struct {
		name    string
		w       *syntax.Word
		wantSet bool
		wantSub string
	}{
		{"static", litWord("x"), false, ""},
		{"nil", nil, false, ""},
		{"param", paramWord("X"), true, "variable"},
		// CmdSubst should NOT be flagged.
		{"cmd sub", cmdSubWord(), false, ""},
		{"dollar sgl", dollarSglWord("x"), true,
			"ANSI-C"},
		{"dbl with param only",
			dblQuotedWord(
				&syntax.ParamExp{
					Param: &syntax.Lit{Value: "X"},
				},
			), true, "variable"},
		{"dbl with cmd sub only",
			dblQuotedWord(
				&syntax.CmdSubst{},
			), false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpansionReason(tt.w)
			if tt.wantSet && got == "" {
				t.Error("ExpansionReason() empty")
			}
			if !tt.wantSet && got != "" {
				t.Errorf(
					"ExpansionReason() = %q", got)
			}
			if tt.wantSub != "" &&
				!strings.Contains(got, tt.wantSub) {
				t.Errorf(
					"ExpansionReason() = %q, "+
						"want substring %q",
					got, tt.wantSub)
			}
		})
	}
}

// --- Equal ---

func TestEqual(t *testing.T) {
	tests := []struct {
		name string
		w    *syntax.Word
		s    string
		want Match
	}{
		{"match", litWord("-c"), "-c", Yes},
		{"no match", litWord("-c"), "-x", No},
		{"empty", litWord(""), "", Yes},
		{"nil empty", nil, "", Yes},
		{"nil nonempty", nil, "x", No},
		{"opaque", paramWord("X"), "-c", Maybe},
		{"cmd sub", cmdSubWord(), "-c", Maybe},
		{"sgl quoted match", &syntax.Word{
			Parts: []syntax.WordPart{
				&syntax.SglQuoted{Value: "foo"},
			},
		}, "foo", Yes},
		{"backslash match", litWord(`he\llo`),
			"hello", Yes},
		{"backslash no match", litWord(`he\llo`),
			"hxllo", No},
		// Mixed words: static prefix can rule out.
		{"mixed prefix mismatch",
			mixedWord("--flag=", "V"), "foo", No},
		// Mixed words: can't confirm equality (opaque
		// suffix could be anything).
		{"mixed prefix matches but opaque remains",
			mixedWord("--flag=", "V"),
			"--flag=", Maybe},
		{"mixed equal s consumed opaque remains",
			mixedWord("hello", "V"),
			"hello", Maybe},
		// Multi-part static.
		{"dbl quoted static match",
			dblQuotedWord(
				&syntax.Lit{Value: "hello"},
			), "hello", Yes},
		{"dbl quoted static no match",
			dblQuotedWord(
				&syntax.Lit{Value: "hello"},
			), "world", No},
		// Trailing backslash (kept literal).
		{"trailing backslash match",
			litWord(`trail\`), "trail\\", Yes},
		{"trailing backslash no match",
			litWord(`trail\`), "trail", No},
		// Empty DblQuoted (zero inner parts).
		{"empty dbl quoted",
			dblQuotedWord(), "", Yes},
		{"empty dbl quoted nonempty s",
			dblQuotedWord(), "x", No},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Equal(tt.w, tt.s)
			if got != tt.want {
				t.Errorf("Equal() = %v, want %v",
					got, tt.want)
			}
		})
	}
}

// --- HasPrefix ---

func TestHasPrefix(t *testing.T) {
	tests := []struct {
		name string
		w    *syntax.Word
		s    string
		want Match
	}{
		{"match", litWord("--flag"), "--", Yes},
		{"no match", litWord("foo"), "--", No},
		{"single dash", litWord("-c"), "-", Yes},
		{"not flag", litWord("echo"), "-", No},
		{"empty prefix", litWord("anything"), "", Yes},
		{"nil empty prefix", nil, "", Yes},
		{"nil nonempty prefix", nil, "-", No},
		{"opaque", paramWord("X"), "-", Maybe},
		{"backslash match", litWord(`--fl\ag`),
			"--flag", Yes},
		{"backslash no match", litWord(`--fl\ag`),
			"--xyz", No},
		{"word shorter than s", litWord("-"),
			"--flag", No},
		// Mixed words: static prefix decides.
		{"mixed prefix yes",
			mixedWord("--flag=", "V"), "--", Yes},
		{"mixed prefix exact",
			mixedWord("--flag=", "V"),
			"--flag=", Yes},
		{"mixed prefix no",
			mixedWord("--flag=", "V"),
			"exec=", No},
		// s extends into opaque part.
		{"mixed s extends into opaque",
			mixedWord("--", "V"),
			"--flag", Maybe},
		// Multi-part static.
		{"sgl quoted prefix", &syntax.Word{
			Parts: []syntax.WordPart{
				&syntax.SglQuoted{Value: "hello"},
			},
		}, "hel", Yes},
		// Trailing backslash (kept literal).
		{"trailing backslash match",
			litWord(`trail\`), "trail\\", Yes},
		{"trailing backslash no match",
			litWord(`trail\`), "xyz", No},
		// Empty DblQuoted.
		{"empty dbl quoted empty s",
			dblQuotedWord(), "", Yes},
		{"empty dbl quoted nonempty s",
			dblQuotedWord(), "-", No},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasPrefix(tt.w, tt.s)
			if got != tt.want {
				t.Errorf("HasPrefix() = %v, want %v",
					got, tt.want)
			}
		})
	}
}

// --- Contains ---

func TestContains(t *testing.T) {
	tests := []struct {
		name string
		w    *syntax.Word
		s    string
		want Match
	}{
		{"found", litWord("system()"), "system(",
			Yes},
		{"not found", litWord("echo"), "system(",
			No},
		{"empty s", litWord("anything"), "", Yes},
		{"nil empty s", nil, "", Yes},
		{"nil nonempty s", nil, "x", No},
		{"opaque", paramWord("X"), "foo", Maybe},
		{"backslash found", litWord(`sys\tem(`),
			"system(", Yes},
		{"backslash not found", litWord(`sys\tem(`),
			"xyz", No},
		// Mixed words: s found in static part.
		{"mixed found in static part",
			mixedWord("--flag=", "V"), "flag", Yes},
		{"mixed found in static part eq",
			mixedWord("--flag=", "V"), "=", Yes},
		// Mixed words: s not in any static part.
		{"mixed not found maybe",
			mixedWord("--flag=", "V"), "value",
			Maybe},
		// Multi-part static cross-boundary.
		{"multi-part cross boundary",
			&syntax.Word{Parts: []syntax.WordPart{
				&syntax.Lit{Value: "hel"},
				&syntax.SglQuoted{Value: "lo"},
			}}, "ello", Yes},
		// Sgl quoted.
		{"sgl quoted found", &syntax.Word{
			Parts: []syntax.WordPart{
				&syntax.SglQuoted{Value: "hello"},
			},
		}, "ell", Yes},
		{"sgl quoted not found", &syntax.Word{
			Parts: []syntax.WordPart{
				&syntax.SglQuoted{Value: "hello"},
			},
		}, "xyz", No},
		// Trailing backslash (kept literal).
		{"trailing backslash found",
			litWord(`trail\`), "\\", Yes},
		{"trailing backslash not found",
			litWord(`trail\`), "xyz", No},
		// Empty DblQuoted.
		{"empty dbl quoted empty s",
			dblQuotedWord(), "", Yes},
		{"empty dbl quoted nonempty s",
			dblQuotedWord(), "x", No},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Contains(tt.w, tt.s)
			if got != tt.want {
				t.Errorf("Contains() = %v, want %v",
					got, tt.want)
			}
		})
	}
}

// --- DefinitelyEqual ---

func TestDefinitelyEqual(t *testing.T) {
	tests := []struct {
		name string
		w    *syntax.Word
		s    string
		want bool
	}{
		{"match", litWord("-c"), "-c", true},
		{"no match", litWord("-c"), "-x", false},
		{"opaque", paramWord("X"), "-c", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DefinitelyEqual(tt.w, tt.s)
			if got != tt.want {
				t.Errorf("got %v, want %v",
					got, tt.want)
			}
		})
	}
}

// --- DefinitelyHasPrefix ---

func TestDefinitelyHasPrefix(t *testing.T) {
	tests := []struct {
		name string
		w    *syntax.Word
		s    string
		want bool
	}{
		{"match", litWord("--flag"), "--", true},
		{"no match", litWord("echo"), "--", false},
		{"opaque", paramWord("X"), "--", false},
		// Mixed word: static prefix long enough.
		{"mixed yes",
			mixedWord("--flag=", "V"), "--", true},
		{"mixed no",
			mixedWord("--flag=", "V"),
			"exec=", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DefinitelyHasPrefix(tt.w, tt.s)
			if got != tt.want {
				t.Errorf("got %v, want %v",
					got, tt.want)
			}
		})
	}
}

// --- DefinitelyContains ---

func TestDefinitelyContains(t *testing.T) {
	tests := []struct {
		name string
		w    *syntax.Word
		s    string
		want bool
	}{
		{"found", litWord("system()"), "system(",
			true},
		{"not found", litWord("echo"), "system(",
			false},
		{"opaque", paramWord("X"), "foo", false},
		// Mixed word: s found in static part.
		{"mixed yes",
			mixedWord("--flag=", "V"), "=", true},
		{"mixed no in static",
			mixedWord("--flag=", "V"),
			"value", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DefinitelyContains(tt.w, tt.s)
			if got != tt.want {
				t.Errorf("got %v, want %v",
					got, tt.want)
			}
		})
	}
}

// --- MayEqual ---

func TestMayEqual(t *testing.T) {
	tests := []struct {
		name string
		w    *syntax.Word
		s    string
		want bool
	}{
		{"static match", litWord("-c"), "-c", true},
		{"static no match", litWord("-c"), "-x",
			false},
		{"opaque always true", paramWord("X"), "-c",
			true},
		{"opaque always true 2", paramWord("X"), "",
			true},
		{"cmd sub always true", cmdSubWord(), "y",
			true},
		// Mixed: static prefix rules out match.
		{"mixed prefix mismatch",
			mixedWord("--flag=", "V"), "foo",
			false},
		// Mixed: can't rule out (prefix matches).
		{"mixed prefix matches",
			mixedWord("hello", "V"), "hello",
			true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MayEqual(tt.w, tt.s)
			if got != tt.want {
				t.Errorf("MayEqual() = %v, want %v",
					got, tt.want)
			}
		})
	}
}

// --- MayContain ---

func TestMayContain(t *testing.T) {
	tests := []struct {
		name string
		w    *syntax.Word
		s    string
		want bool
	}{
		{"static found", litWord("hello=world"), "=",
			true},
		{"static not found", litWord("hello"), "=",
			false},
		{"opaque", paramWord("X"), "=", true},
		{"mixed word", mixedWord("--flag=", "V"),
			"=", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MayContain(tt.w, tt.s)
			if got != tt.want {
				t.Errorf(
					"MayContain() = %v, want %v",
					got, tt.want)
			}
		})
	}
}

// --- MayHavePrefix ---

func TestMayHavePrefix(t *testing.T) {
	tests := []struct {
		name string
		w    *syntax.Word
		s    string
		want bool
	}{
		{"static match", litWord("exec=foo"),
			"exec=", true},
		{"static no match", litWord("keep=foo"),
			"exec=", false},
		{"opaque", paramWord("X"), "exec=", true},
		{"mixed starts static",
			mixedWord("exec=", "V"), "exec=", true},
		// Mixed: static prefix rules out.
		{"mixed prefix mismatch",
			mixedWord("--flag=", "V"),
			"exec=", false},
		// Mixed: s extends into opaque part.
		{"mixed s into opaque",
			mixedWord("--", "V"),
			"--flag=", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MayHavePrefix(tt.w, tt.s)
			if got != tt.want {
				t.Errorf(
					"MayHavePrefix() = %v, want %v",
					got, tt.want)
			}
		})
	}
}

// --- SplitEq ---

func TestSplitEq(t *testing.T) {
	tests := []struct {
		name      string
		w         *syntax.Word
		wantName  string
		wantValue string
		wantNil   bool
	}{
		{"simple", litWord("--flag=value"),
			"--flag", "value", false},
		{"empty value", litWord("--flag="),
			"--flag", "", false},
		{"no equals", litWord("--flag"),
			"", "", true},
		{"value with embedded equals",
			litWord("--flag=a=b"),
			"--flag", "a=b", false},
		// --flag=$VALUE: structural split preserves
		// the ParamExp in the value Word. Text()
		// prints it as ${VALUE} (printer format).
		{"value with param",
			mixedWord("--flag=", "VALUE"),
			"--flag", "${VALUE}", false},
		{"non-lit first", paramWord("X"),
			"", "", true},
		{"backslash in name",
			litWord(`--fl\ag=value`),
			"--flag", "value", false},
		// Escaped \= produces literal = in resolved
		// text. It's the first = so it's the split
		// point — prevents bypass of flag=value
		// splitting via backslash escaping.
		{"escaped equals is split point",
			litWord(`--action\=exec=ssh`),
			"--action", "exec=ssh", false},
		{"escaped equals only",
			litWord(`--action\=value`),
			"--action", "value", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, vw := SplitEq(tt.w)
			if tt.wantNil {
				if vw != nil {
					t.Error("want nil value Word")
				}
				if name != "" {
					t.Errorf("name = %q, want empty",
						name)
				}
				return
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q",
					name, tt.wantName)
			}
			if vw == nil {
				t.Fatal("value Word is nil")
			}
			gotValue := Text(vw)
			if gotValue != tt.wantValue {
				t.Errorf("value = %q, want %q",
					gotValue, tt.wantValue)
			}
		})
	}
}

// --- SplitPrefix ---

func TestSplitPrefix(t *testing.T) {
	tests := []struct {
		name      string
		w         *syntax.Word
		nameLen   int
		wantName  string
		wantValue string
		wantNil   bool
	}{
		{"simple", litWord("-n5"), 2,
			"-n", "5", false},
		// -n$VAR: structural split preserves the
		// ParamExp in the value Word. Text() prints
		// it as ${VAR} (printer format).
		{"with param",
			mixedWord("-n", "VAR"), 2,
			"-n", "${VAR}", false},
		{"bare flag exact", litWord("-n"), 2,
			"-n", "", true},
		{"nameLen exceeds", litWord("-n"), 5,
			"", "", true},
		{"zero nameLen", litWord("-n"), 0,
			"", "", true},
		{"non-lit first", paramWord("X"), 2,
			"", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, vw := SplitPrefix(tt.w, tt.nameLen)
			if tt.wantNil {
				if vw != nil {
					t.Error("want nil value Word")
				}
				if tt.wantName != "" &&
					name != tt.wantName {
					t.Errorf("name = %q, want %q",
						name, tt.wantName)
				}
				return
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q",
					name, tt.wantName)
			}
			if vw == nil {
				t.Fatal("value Word is nil")
			}
			gotValue := Text(vw)
			if gotValue != tt.wantValue {
				t.Errorf("value = %q, want %q",
					gotValue, tt.wantValue)
			}
		})
	}
}

// --- Construction ---

func TestLit(t *testing.T) {
	w := Lit("hello")
	if len(w.Parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(w.Parts))
	}
	if lit, ok := w.Parts[0].(*syntax.Lit); !ok {
		t.Error("part is not Lit")
	} else if lit.Value != "hello" {
		t.Errorf("value = %q", lit.Value)
	}
}

func TestFromStrings(t *testing.T) {
	ws := FromStrings([]string{"a", "b"})
	if len(ws) != 2 {
		t.Fatalf("len = %d", len(ws))
	}
	if Text(ws[0]) != "a" || Text(ws[1]) != "b" {
		t.Error("wrong values")
	}
}

// --- Utilities ---

func TestDirectPath(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"script.sh", "./script.sh"},
		{"/usr/bin/bash", "/usr/bin/bash"},
		{"./already", "././already"},
	}
	for _, tt := range tests {
		got := DirectPath(tt.in)
		if got != tt.want {
			t.Errorf("DirectPath(%q) = %q, want %q",
				tt.in, got, tt.want)
		}
	}
}

func TestUnescapeBackslashes(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"hello", "hello"},
		{`he\llo`, "hello"},
		{`a\b\c`, "abc"},
		{`trail\`, "trail\\"},
	}
	for _, tt := range tests {
		got := UnescapeBackslashes(tt.in)
		if got != tt.want {
			t.Errorf(
				"UnescapeBackslashes(%q) = %q, "+
					"want %q",
				tt.in, got, tt.want)
		}
	}
}
