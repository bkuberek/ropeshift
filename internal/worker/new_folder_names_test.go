package worker_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/ledger"
)

// A refusal repeats a name cut to 80 characters, counted in characters and not in bytes, so a name whose 80th character
// takes several bytes is cut after that character and never inside it, and a name of exactly 80 characters is not cut.
func TestTheRefusalCutsAFileNameAtEightyCharactersNotBytes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		file string
		want string
	}{
		{"an ASCII name past the cut", strings.Repeat("x", 80) + "-tail.txt", strings.Repeat("x", 80) + "..."},
		{"an ASCII name of exactly 80", strings.Repeat("x", 80), strings.Repeat("x", 80)},
		{"an ASCII name of 79", strings.Repeat("x", 79), strings.Repeat("x", 79)},
		{"three-byte characters past the cut", strings.Repeat("日", 81), strings.Repeat("日", 80) + "..."},
		{"three-byte characters of exactly 80", strings.Repeat("日", 80), strings.Repeat("日", 80)},
		{"a four-byte character as the 80th", strings.Repeat("a", 79) + "😀" + "tail", strings.Repeat("a", 79) + "😀" + "..."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newTemplateRig(t, nil)
			report := r.refusedFor(t, "request-cut", "monorepo", r.holding("harbor", map[string]string{tc.file: "x"}))
			if report.Reason != ledger.TemplateRefusedNotEmpty {
				t.Fatalf("the report is %+v", report)
			}
			if !utf8.ValidString(report.Detail) {
				t.Errorf("the refusal is not valid text: %q", report.Detail)
			}
			if want := `"` + tc.want + `"`; !strings.Contains(report.Detail, want) {
				t.Errorf("the refusal does not hold the name as %q: %q", want, report.Detail)
			}
			if cut := strings.HasSuffix(tc.want, "..."); cut != strings.Contains(report.Detail, `..."`) {
				t.Errorf("the name is cut: want %v, refusal %q", cut, report.Detail)
			}
		})
	}
}
