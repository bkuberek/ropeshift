package ledger_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/ledger"
)

func TestPlainTextDropsEscapeSequencesAndControlCharactersAndKeepsNewlineAndTab(t *testing.T) {
	for in, want := range map[string]string{
		"\x1b[31mred\x1b[0m":               "red",
		"a\x1b]0;title\x07b":               "ab",
		"a\x1b]0;title\x1b\\b":             "ab",
		"a\u009b2Jb\u009d0;t\u009cc":       "abc",
		"line\r\nnext\tcell\x00\x08\x7f":   "line\nnext\tcell",
		"\u202ereversed\u200b":             "reversed",
		"line\u2028break\u2029para":        "line\nbreak\npara",
		"a\u2066b\u2067c\u2068d\u2069e":    "abcde",
		"a\u200eb\u200fc\u061cd":           "abcd",
		"<b>x</b> [a](https://e.example/)": "<b>x</b> [a](https://e.example/)",
		"":                                 "",
	} {
		if got := ledger.PlainText(in); got != want {
			t.Errorf("PlainText(%q) = %q, want %q", in, got, want)
		}
	}
}

// The rows where a PlainText that reads the C1 forms of CSI and OSC, and one that reads text a stranger wrote into an
// agent's brief, meet: what the text says stays apart from what a terminal obeys, and no word is joined or lost.
func TestPlainTextKeepsWordsApartAndNothingOfASequenceBehind(t *testing.T) {
	for name, tt := range map[string]struct{ in, want string }{
		"a C1 sequence leaves none of its parameters":           {"a\u009b2Jb\u009d0;t\u009cc", "abc"},
		"a C1 sequence with intermediate bytes":                 {"a\u009b1 qb", "ab"},
		"an unterminated OSC stops at the newline":              {"a\x1b]0;title\nnext line", "a\nnext line"},
		"an unterminated C1 OSC stops at the newline":           {"a\u009d0;title\nnext line", "a\nnext line"},
		"an unterminated OSC with no newline ends the text":     {"a\x1b]0;title", "a"},
		"a terminated OSC leaves the text after it":             {"a\x1b]0;t\x07b\nc", "ab\nc"},
		"an unterminated OSC takes the words up to the newline": {"a\x1b]0;x kept\nnext", "a\nnext"},
		"an unterminated OSC stops at a line separator":         {"a\x1b]0;x\u2028kept\nnext", "a\nkept\nnext"},
		"an unterminated OSC stops at a paragraph separator":    {"a\x1b]0;x\u2029kept\nnext", "a\nkept\nnext"},
		"an unterminated C1 OSC stops at a line separator":      {"a\u009d0;x\u2028kept", "a\nkept"},
		"a line separator becomes a newline":                    {"one\u2028two", "one\ntwo"},
		"a paragraph separator becomes a newline":               {"one\u2029two", "one\ntwo"},
		"invalid UTF-8 is made valid":                           {"a\xffb\xc3", "a\ufffdb\ufffd"},
		"a two-character escape takes its intermediate bytes":   {"a\x1b(Bb", "ab"},
		"a carriage return before a newline is one newline":     {"a\r\nb", "a\nb"},
	} {
		t.Run(name, func(t *testing.T) {
			got := ledger.PlainText(tt.in)
			if got != tt.want {
				t.Errorf("PlainText(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("PlainText(%q) = %q, which is not valid UTF-8", tt.in, got)
			}
		})
	}
}

// The text a CI log or a brief shows goes through PlainText first, and then has the shapes of credentials taken out.
func TestSanitizeCITextIsPlainTextWithCredentialsTakenOut(t *testing.T) {
	const token = "ghp_a1B2a1B2a1B2a1B2a1B2a1B2a1B2a1B2a1B2"
	in := "a\u202eb\x1b[31mc\x00d\r\ne\u2028f\tg " + token
	plain := ledger.PlainText(in)
	if want := "abcd\ne\nf\tg " + token; plain != want {
		t.Errorf("PlainText = %q, want %q", plain, want)
	}
	if got, want := ledger.SanitizeCIText(in), ledger.RedactCredentials(plain); got != want || strings.Contains(got, token) {
		t.Errorf("SanitizeCIText = %q, want %q", got, want)
	}
}

func TestPlainTextReadsABlankBrailleCharacterAsASpace(t *testing.T) {
	if got := ledger.PlainText("a\u2800b"); got != "a b" {
		t.Errorf("got %q, want %q", got, "a b")
	}
}
