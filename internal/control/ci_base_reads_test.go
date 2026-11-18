package control

import (
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

// What a worker reports of how many reads of a pull request in a row could not read the base is a count the control plane
// keeps within what a worker counts to: never more than ledger.CIBaseUnreadReads, never below none, and none at all for a
// check whose base was read. A worker's word is not trusted to be a small number.
func TestTheCountOfRefusedBaseReadsIsKeptWithinWhatAWorkerCountsTo(t *testing.T) {
	t.Parallel()
	reported := []ledger.PullRequestCheck{
		{Name: "huge", BaseUnread: true, BaseUnreadReads: 1 << 40},
		{Name: "negative", BaseUnread: true, BaseUnreadReads: -7},
		{Name: "unmarked", BaseUnreadReads: ledger.CIBaseUnreadReads},
		{Name: "two", BaseUnread: true, BaseUnreadReads: 2},
		{Name: "red", RedOnBase: "abc", BaseUnread: true, BaseUnreadReads: ledger.CIBaseUnreadReads},
	}
	kept := clampedBaseReads(reported)
	want := map[string]int64{"huge": ledger.CIBaseUnreadReads, "negative": 0, "unmarked": 0, "two": 2, "red": 0}
	for _, check := range kept {
		if check.BaseUnreadReads != want[check.Name] {
			t.Errorf("%s is kept at %d refused reads, want %d", check.Name, check.BaseUnreadReads, want[check.Name])
		}
		if check.Name == "unmarked" && check.BaseUnread {
			t.Errorf("a count made a check unread")
		}
	}
	if reported[0].BaseUnreadReads != 1<<40 {
		t.Errorf("the report was changed in place")
	}
}
