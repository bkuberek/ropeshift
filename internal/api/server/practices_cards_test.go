package server_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/test/apitest"
)

// changeKeys is the keys of each row of a card's changes, in the order the card lists them.
func changeKeys(t *testing.T, raw json.RawMessage) [][]string {
	t.Helper()
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("the card's changes are %s: %v", raw, err)
	}
	keys := make([][]string, len(rows))
	for i, row := range rows {
		for _, key := range []string{"field", "from", "to"} {
			if _, held := row[key]; held {
				keys[i] = append(keys[i], key)
			}
		}
	}
	return keys
}

func wantKeys(t *testing.T, what string, got [][]string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s lists %d changes, want %d: %v", what, len(got), len(want), got)
	}
	for i := range want {
		if joined := strings.Join(got[i], ","); joined != want[i] {
			t.Errorf("%s: change %d holds %s, want %s", what, i, joined, want[i])
		}
	}
}

// A card for a practices call names no record to read a value from, so its finding, its reason, and why the practice matters
// are arguments, `{field, to}` with no `from`; only a withdrawal's reason, which stops holding, is a field
// that moves from a value to none.
func TestPracticesCardsSendArgumentsAsFieldAndToOnly(t *testing.T) {
	d := practicesDaemon(t, nil, "")
	d.HoldClone(t, "repo-a", "widgets")
	keepFindings(t, d)
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)

	_, err := delegate.AcknowledgePractices(t.Context(), api.PracticesAcknowledge{Repository: "widgets", Finding: secretFinding, Reason: "a fixture the parser tests read"})
	wantKeys(t, "the acknowledgement card", changeKeys(t, cardDetail(t, preparedCard(t, err)).Changes), "field,to", "field,to", "field,to")

	if _, err := d.Client.AcknowledgePractices(t.Context(), api.PracticesAcknowledge{Repository: "widgets", Finding: secretFinding, Reason: "a fixture the parser tests read"}); err != nil {
		t.Fatal(err)
	}
	_, err = delegate.WithdrawPractices(t.Context(), api.PracticesWithdraw{Repository: "widgets", Finding: secretFinding})
	wantKeys(t, "the withdrawal card", changeKeys(t, cardDetail(t, preparedCard(t, err)).Changes), "field,to", "field,to", "field,from,to")
}
