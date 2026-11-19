package ledger_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

func TestAgeWordsNamesTheLargestWholeUnit(t *testing.T) {
	for _, tt := range []struct {
		ms   int64
		want string
	}{
		{-5, "0s"}, {999, "0s"}, {59_000, "59s"}, {60_000, "1m"}, {3_599_000, "59m"}, {3_600_000, "1h"},
		{47 * 3_600_000, "47h"}, {48 * 3_600_000, "2d"},
	} {
		if got := ledger.AgeWords(tt.ms); got != tt.want {
			t.Errorf("AgeWords(%d) = %q, want %q", tt.ms, got, tt.want)
		}
	}
}

func TestPlainTextKeepsAWordsAndTakesOutWhatHidesThem(t *testing.T) {
	const token = "ghp_a1B2a1B2a1B2a1B2a1B2a1B2a1B2a1B2a1B2"
	got := ledger.PlainText("a\u202eb\x1b[31mc\x00d\r\ne\u2028f\tg " + token)
	if want := "abcd\ne\nf\tg " + token; got != want {
		t.Errorf("PlainText = %q, want %q", got, want)
	}
	if got := ledger.SanitizeCIText(token); got == token {
		t.Error("SanitizeCIText kept a credential's shape")
	}
}
