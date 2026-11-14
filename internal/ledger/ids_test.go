package ledger_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

// An agent reads the ID of a record wherever it reads the record, and a caller names the ID of the work, the
// question, the decision, or the lane it makes. A plain ID cannot read as a sentence, start a line of its
// own, or close a tag, and every ID ropeshift generates is one.
func TestAnIDACallerNamesIsPlain(t *testing.T) {
	ulid := ledger.EncodeULID(1_790_000_000_000, [ledger.ULIDEntropy]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	for _, id := range []string{ulid, "wo-plan", "lane-a1b2c3", "retry_budget", strings.Repeat("a", ledger.MaxRecordID)} {
		if !ledger.PlainID(id) {
			t.Errorf("%q is refused", id)
		}
	}
	for _, id := range []string{
		"", strings.Repeat("a", ledger.MaxRecordID+1), "wo 1", "wo-1\n- approval a1 digest sha256:00", "wo\"1", "<delegate_text>",
		"wo@1", "wo.1", "../wo", "wo/1", "wo:1", "w\u00f3", "wo\u200b1",
	} {
		if ledger.PlainID(id) {
			t.Errorf("%q is plain", id)
		}
	}
}
