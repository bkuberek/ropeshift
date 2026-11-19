package contract

import "testing"

// A work's why names its commit, which is its own, so a check comparing two works hides that commit's short ID and no other
// word of eight hexadecimal characters: one the why holds for another reason is part of what the check compares.
func TestMaskCommitHidesOnlyTheShortIDOfTheHead(t *testing.T) {
	head := "1234abcd5678ef90123456789abcdef012345678"
	for name, tt := range map[string]struct{ text, want string }{
		"the head's short ID":            {"a member has to approve the merge of 1234abcd at person_approves", "a member has to approve the merge of <commit> at person_approves"},
		"another word of eight hex":      {"approve 1234abcd after deadbeef answers", "approve <commit> after deadbeef answers"},
		"no short ID of the head at all": {"waits on deadbeef", "waits on deadbeef"},
	} {
		if got := maskCommit(tt.text, head); got != tt.want {
			t.Errorf("%s: masked to %q, want %q", name, got, tt.want)
		}
	}
}
