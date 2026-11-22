package control

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/workorders"
)

// A canceled lane's report is the worker's word, and the event that ends the lane keeps it for every member to read, so
// its detail is redacted as its verdicts are, with verdicts or without.
func TestACanceledLanesReportKeepsARedactedDetail(t *testing.T) {
	t.Parallel()
	const npmToken = "npm_0123456789abcdefghijklmnopqrstuvwxyz"
	raw := "npm error //registry.npmjs.org/:_authToken=" + npmToken + " at https://oauth2:hunter2-secret@github.com/acme/app.git\x1b[31m"
	detail, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, payload string }{
		{"no verdicts", `{"outcome":"failed","detail":` + string(detail) + `}`},
		{"verdicts", `{"outcome":"failed","detail":` + string(detail) + `,"verdicts":[{"criterion":"c1","met":true,"reasons":"fine"}]}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, err := redactedReport(json.RawMessage(c.payload))
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{npmToken, "hunter2-secret", "\x1b", `\u001b`} {
				if strings.Contains(string(out), secret) {
					t.Errorf("the report keeps %q: %s", secret, out)
				}
			}
			var fields struct{ Detail string }
			if err := json.Unmarshal(out, &fields); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(fields.Detail, "[redacted: npm_token]") || !strings.Contains(fields.Detail, "[redacted: url_credentials]") {
				t.Errorf("the detail %q does not say what it redacted", fields.Detail)
			}
		})
	}
}

func TestACanceledLanesReportRefusesADetailThatIsNoString(t *testing.T) {
	t.Parallel()
	_, err := redactedReport(json.RawMessage(`{"detail":42}`))
	if !errors.Is(err, workorders.ErrInvalid) {
		t.Fatalf("a detail that is no string answered %v, want %v", err, workorders.ErrInvalid)
	}
}
