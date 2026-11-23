package worker

import "testing"

// A request a live hook raised between a journal reread's read and its note is not undone by the reread: the count the
// hook noted stands, and a read with nothing noted in between is noted as it read.
func TestAJournalRereadKeepsALiveRequestCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		live bool
		want int
	}{
		{name: "a hook noted a request meanwhile", live: true, want: 2},
		{name: "nothing was noted meanwhile", want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := &laneState{}
			mark := state.approvalsMark()
			if tt.live {
				state.noteApprovals(2)
			}
			state.noteApprovalsSince(mark, 1)
			if got := state.approvalsOpen(); got != tt.want {
				t.Errorf("%d approvals open after the reread, want %d", got, tt.want)
			}
		})
	}
}
