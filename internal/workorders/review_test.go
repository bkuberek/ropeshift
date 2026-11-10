package workorders

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

func reviewed(kind Kind) WorkOrder {
	return WorkOrder{
		ID: "wo-1", Kind: kind, State: Running, Title: "Add the retry budget", RepositoryID: "shop", Priority: 3, Size: "small",
		Requirements: []string{"FR-1"}, Needs: map[string]string{"os": "linux"}, ContractID: "c-1", ContractVersion: 4,
		Fence: 6, MaxAttempts: 2, Attempts: 1,
	}
}

// A review of a change names the work it judges and the attempt, pins the contract version the work
// ran on, and starts ready: it has no contract of its own to review.
func TestAReviewOfAChangeStartsReadyOnTheSameContract(t *testing.T) {
	review, err := NewReview("rv-1", reviewed(KindChange), 7)
	if err != nil {
		t.Fatal(err)
	}
	want := WorkOrder{
		ID: "rv-1", Kind: KindReview, State: Ready, Title: "Review: Add the retry budget", RepositoryID: "shop", Priority: 3, Size: "small",
		Requirements: []string{"FR-1"}, Needs: map[string]string{"os": "linux"}, ContractID: "c-1", ContractVersion: 4,
		Reviews: "wo-1", ReviewsFence: 7, MaxAttempts: 2,
	}
	if diff := Diff(review, want); len(diff) != 0 {
		t.Errorf("the review differs from what was meant in %v: %+v", diff, review)
	}
}

func TestAReworkIsReviewedToo(t *testing.T) {
	review, err := NewReview("rv-1", reviewed(KindRework), 7)
	if err != nil || review.Reviews != "wo-1" {
		t.Errorf("a review of a rework: %+v, %v", review, err)
	}
}

// Only work that produces a change is reviewed, and a review is never reviewed.
func TestOnlyAChangeOrAReworkIsReviewed(t *testing.T) {
	for _, kind := range []Kind{KindPlan, KindContractReview, KindReview} {
		if _, err := NewReview("rv-1", reviewed(kind), 7); !errors.Is(err, ErrInvalid) {
			t.Errorf("a review of a %s: %v", kind, err)
		}
	}
	if _, err := NewReview("", reviewed(KindChange), 7); !errors.Is(err, ErrInvalid) {
		t.Errorf("a review with no id: %v", err)
	}
}

// A client cannot make a review. The control plane makes one when a lane completes, and a draft that asks
// for one would start in `intake` with no work to judge.
func TestAClientCannotDraftAReview(t *testing.T) {
	if _, err := New(WorkOrder{ID: "rv-1", Kind: KindReview}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a drafted review: %v", err)
	}
}

// The log of a review replays through the table that every other work order uses, from its ready creation
// to done, and the replay agrees with the review it came from.
func TestAReviewReplaysFromReadyToDone(t *testing.T) {
	review, err := NewReview(testID, reviewed(KindChange), 7)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	events := []ledger.Event{{Rev: 1, Type: EventCreated, Subject: ledger.Subject{Kind: SubjectKind, ID: testID}, Data: data}}
	rev := int64(2)
	for _, change := range []Change{
		{Trigger: TriggerClaimGranted, Fence: 1, ClaimID: "c1", Worker: "w"},
		{Trigger: TriggerLaneStarted, Fence: 1},
		{Trigger: TriggerLaneCompleted, Fence: 2},
		{Trigger: TriggerAcceptancePassed},
	} {
		events = append(events, changeEvent(t, rev, change))
		rev++
	}
	book, err := Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	got := book[testID]
	if got.State != Done || got.Kind != KindReview || got.Reviews != "wo-1" || got.ReviewsFence != 7 || got.Fence != 2 {
		t.Errorf("replayed %+v", got)
	}
}

func TestTheDiffNamesWhatAReviewJudges(t *testing.T) {
	a, err := NewReview("rv-1", reviewed(KindChange), 7)
	if err != nil {
		t.Fatal(err)
	}
	b := a
	b.Reviews, b.ReviewsFence = "wo-2", 8
	if got := Diff(a, b); !slices.Equal(got, []string{"reviews", "reviews_fence"}) {
		t.Errorf("the diff names %v", got)
	}
}

// A review is open until it is done, failed, or canceled, and only a review can be.
func TestAReviewIsOpenUntilItEnds(t *testing.T) {
	for _, tt := range []struct {
		kind  Kind
		state State
		want  bool
	}{
		{KindReview, Ready, true}, {KindReview, Claimed, true}, {KindReview, Running, true}, {KindReview, Parked, true},
		{KindReview, Blocked, true}, {KindReview, Done, false}, {KindReview, Failed, false}, {KindReview, Canceled, false},
		{KindChange, Ready, false},
	} {
		if got := (WorkOrder{Kind: tt.kind, State: tt.state}).OpenReview(); got != tt.want {
			t.Errorf("a %s in %s: open is %v, want %v", tt.kind, tt.state, got, tt.want)
		}
	}
}
