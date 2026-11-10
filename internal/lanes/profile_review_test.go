package lanes_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

func TestReviewRoundsAreBoundedByDefault(t *testing.T) {
	for _, tt := range []struct {
		set, want int64
	}{{0, 3}, {-2, 3}, {1, 1}, {5, 5}} {
		if got := (lanes.ReviewSettings{MaxReviewRounds: tt.set}).Rounds(); got != tt.want {
			t.Errorf("max_review_rounds %d reads %d, want %d", tt.set, got, tt.want)
		}
	}
}

// A profile that sets nothing for review keeps the digest it had, so no lane's recorded profile moves.
func TestAProfileWithNoReviewSettingsHasNoReviewInItsDigest(t *testing.T) {
	raw, err := json.Marshal(lanes.Profile{Name: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "review") {
		t.Errorf("an unset review table is in the profile: %s", raw)
	}
	set, err := json.Marshal(lanes.Profile{Name: "default", Review: lanes.ReviewSettings{MaxReviewRounds: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(set), `"max_review_rounds":2`) {
		t.Errorf("a set bound is not in the profile: %s", set)
	}
}
