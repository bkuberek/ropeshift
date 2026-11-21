package ledger

import "testing"

func TestMergeTrailersNameTheWorkOrderAndTheReviewedCommit(t *testing.T) {
	got := MergeTrailers("wo-1", "abc")
	want := "Ropeshift-Work-Order: wo-1\nRopeshift-Reviewed-Commit: abc\n"
	if got != want {
		t.Errorf("the trailers read %q, want %q", got, want)
	}
}

func TestTrailersJoinAMessageAfterABlankLineOrTheBlockItEndsIn(t *testing.T) {
	trailers := MergeTrailers("wo-1", "abc")
	tests := []struct {
		name, message, want string
	}{
		{"a subject only", "Add the step", "Add the step\n\n" + trailers},
		{"a subject with a newline", "Add the step\n", "Add the step\n\n" + trailers},
		{"a body", "Add the step\n\nIt follows the first.\n", "Add the step\n\nIt follows the first.\n\n" + trailers},
		{"a body that ends in a line with a colon", "Add the step\n\nNote: it follows the first, which\nis here.\n", "Add the step\n\nNote: it follows the first, which\nis here.\n\n" + trailers},
		{"an existing trailer block", "Add the step\n\nSigned-off-by: A <a@example.invalid>\n", "Add the step\n\nSigned-off-by: A <a@example.invalid>\n" + trailers},
		{"a block after a body", "Add the step\n\nBody.\n\nSigned-off-by: A <a@example.invalid>\nReviewed-by: B <b@example.invalid>\n", "Add the step\n\nBody.\n\nSigned-off-by: A <a@example.invalid>\nReviewed-by: B <b@example.invalid>\n" + trailers},
		{"a folded trailer", "Add the step\n\nCo-Debugged-by: A\n  and B\n", "Add the step\n\nCo-Debugged-by: A\n  and B\n" + trailers},
		{"trailers already there", "Add the step\n\n" + trailers, "Add the step\n\n" + trailers},
		{"one of them already there", "Add the step\n\nRopeshift-Work-Order: wo-1\n", "Add the step\n\nRopeshift-Work-Order: wo-1\nRopeshift-Reviewed-Commit: abc\n"},
		{"no message", "", trailers},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WithTrailers(tt.message, trailers); got != tt.want {
				t.Errorf("got\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}
