package control

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/workorders"
)

// TestWithoutNotesRefusesAPayloadThatIsNoReport holds that the report a canceled lane ends with is a JSON object: an
// empty payload, a null, and any other value are refused, because the event that ends the lane would hold nothing.
func TestWithoutNotesRefusesAPayloadThatIsNoReport(t *testing.T) {
	for name, payload := range map[string]string{
		"nothing": "", "a null": "null", "a padded null": " null\n", "an array": "[]", "a string": `"notes"`, "a number": "3",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := withoutNotes([]byte(payload))
			if !errors.Is(err, workorders.ErrInvalid) {
				t.Errorf("got %s, %v, want ErrInvalid", got, err)
			}
		})
	}
}

// TestWithoutNotesKeepsTheRestOfAReport holds that only the notes leave a report, and that an object with none, or
// an empty one, stays an object.
func TestWithoutNotesKeepsTheRestOfAReport(t *testing.T) {
	tests := map[string]struct{ payload, want string }{
		"no notes":          {`{"outcome":"failed"}`, `{"outcome":"failed"}`},
		"notes and a field": {`{"notes":[{"point":"secrets"}],"outcome":"failed"}`, `{"outcome":"failed"}`},
		"only notes":        {`{"notes":[]}`, `{}`},
		"an empty object":   {`{}`, `{}`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := withoutNotes([]byte(tt.payload))
			if err != nil || string(got) != tt.want {
				t.Errorf("got %s, %v, want %s", got, err, tt.want)
			}
		})
	}
}

// TestRedactedVerdictsAreCleanedAndHeldToTheirBound holds that a verdict's reasons and citations lose the shape of a
// credential, that a citation is held to one line, and that the reasons stay within their bound after a marker
// replaces what it was longer than.
func TestRedactedVerdictsAreCleanedAndHeldToTheirBound(t *testing.T) {
	token := "ghp_" + strings.Repeat("a1", 12)
	got := redactedVerdicts([]workorders.CriterionVerdict{
		{
			Criterion: "c1", Verdict: workorders.VerdictFail, Reasons: "key " + token + "\nsecond line",
			Citations: []string{"a.go:1\r\nb.go:2", "k " + token, "x y z", "main.go:3"},
		},
		{Criterion: "c2", Verdict: workorders.VerdictPass, Reasons: strings.Repeat("a", workorders.MaxVerdictReasons-1) + token},
	})
	if got[0].Reasons != "key [redacted: github_token]\nsecond line" {
		t.Errorf("reasons %q", got[0].Reasons)
	}
	if want := []string{"a.go:1 b.go:2", "k [redacted: github_token]", "x y z", "main.go:3"}; strings.Join(got[0].Citations, "|") != strings.Join(want, "|") {
		t.Errorf("citations %q, want %q", got[0].Citations, want)
	}
	if len(got[1].Reasons) > workorders.MaxVerdictReasons || strings.Contains(got[1].Reasons, "ghp_") {
		t.Errorf("the second verdict keeps %d bytes of reasons", len(got[1].Reasons))
	}
	if err := got[1].Validate(); err != nil {
		t.Errorf("a verdict after redaction: %v", err)
	}
}
