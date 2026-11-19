package ledger_test

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/practices"
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

func openFindings(n int, severity practices.Severity, prefix string) []ledger.PracticesOpenFinding {
	out := make([]ledger.PracticesOpenFinding, 0, n)
	for i := range n {
		out = append(out, ledger.PracticesOpenFinding{ID: fmt.Sprintf("%s:%03d", prefix, i), Severity: severity})
	}
	return out
}

// A summary carries at most 50 open findings: the high ones first, then the medium, then the low, each by ID, so the
// cut is the same whatever order the result listed them in. The findings left out are counted.
func TestACappedSummaryKeepsTheMostSeriousOpenFindingsByID(t *testing.T) {
	open := slices.Concat(openFindings(30, practices.Low, "l"), openFindings(30, practices.High, "h"), openFindings(30, practices.Medium, "m"))
	slices.Reverse(open)
	summary := ledger.PracticesSummary{Name: "widgets", Checked: true, Open: open}

	got := summary.Capped()

	if len(got.Open) != ledger.PracticesOpenLimit || got.More != 40 {
		t.Fatalf("kept %d findings and counted %d more, want %d and 40", len(got.Open), got.More, ledger.PracticesOpenLimit)
	}
	var want []string
	for i := range 30 {
		want = append(want, fmt.Sprintf("h:%03d", i))
	}
	for i := range 20 {
		want = append(want, fmt.Sprintf("m:%03d", i))
	}
	var ids []string
	for _, f := range got.Open {
		ids = append(ids, f.ID)
	}
	if !slices.Equal(ids, want) {
		t.Errorf("kept %v, want %v", ids, want)
	}
	if summary.Open[0].ID != "m:029" {
		t.Error("Capped changed the summary it was called on")
	}
	if again := got.Capped(); !reflect.DeepEqual(again, got) {
		t.Errorf("a summary already capped changes: %+v", again)
	}
}

func TestACappedSummaryUnderTheLimitCountsNothingMore(t *testing.T) {
	got := ledger.PracticesSummary{Open: openFindings(ledger.PracticesOpenLimit, practices.Low, "l")}.Capped()
	if len(got.Open) != ledger.PracticesOpenLimit || got.More != 0 {
		t.Errorf("kept %d and counted %d more", len(got.Open), got.More)
	}
	if more := (ledger.PracticesSummary{Open: openFindings(52, practices.Low, "l"), More: 3}).Capped(); more.More != 5 {
		t.Errorf("a summary that counted 3 left out and cut 2 more counts %d", more.More)
	}
}
