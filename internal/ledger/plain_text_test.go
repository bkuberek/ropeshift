package ledger_test

import (
	"testing"

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
		"<b>x</b> [a](https://e.example/)": "<b>x</b> [a](https://e.example/)",
		"":                                 "",
	} {
		if got := ledger.PlainText(in); got != want {
			t.Errorf("PlainText(%q) = %q, want %q", in, got, want)
		}
	}
}
