package workorders

import (
	"errors"
	"strings"
	"testing"
)

func noted(point NotePoint) ReviewNote {
	return ReviewNote{Point: point, Reasons: "because " + string(point), Citations: []string{"main.go:12"}}
}

func TestANoteNeedsAKnownPointAndReasons(t *testing.T) {
	for name, n := range map[string]ReviewNote{
		"no point":           {Reasons: "x"},
		"a point nobody has": {Point: "style", Reasons: "x"},
		"no reasons":         {Point: PointSecrets},
		"blank reasons":      {Point: PointSecrets, Reasons: " \n"},
		"reasons over 4 KiB": {Point: PointSecrets, Reasons: strings.Repeat("x", MaxNoteReasons+1)},
		"an empty citation":  {Point: PointSecrets, Reasons: "x", Citations: []string{"main.go:1", " "}},
	} {
		if err := n.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, point := range NotePoints() {
		if err := noted(point).Validate(); err != nil {
			t.Errorf("%s: %v", point, err)
		}
	}
	if err := (ReviewNote{Point: PointInjection, Reasons: strings.Repeat("x", MaxNoteReasons)}).Validate(); err != nil {
		t.Errorf("reasons of exactly 4 KiB: %v", err)
	}
}

func TestTheNotePointsAreTheFourSecurityPoints(t *testing.T) {
	got := NotePoints()
	want := []NotePoint{PointSecrets, PointInjection, PointDependencies, PointAuthority}
	if len(got) != len(want) {
		t.Fatalf("the points are %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("point %d is %s, want %s", i, got[i], want[i])
		}
	}
}

func TestAReportRefusesNotesThatAreNotWhole(t *testing.T) {
	many := make([]ReviewNote, MaxNotes+1)
	for i := range many {
		many[i] = noted(PointSecrets)
	}
	for name, notes := range map[string][]ReviewNote{
		"an unknown point": {noted(PointSecrets), {Point: "style", Reasons: "x"}},
		"an empty reason":  {{Point: PointAuthority}},
		"too many":         many,
	} {
		report := Report{Outcome: OutcomeCompleted, Verdicts: []CriterionVerdict{said("c1", VerdictPass)}, Notes: notes}
		if err := report.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	atTheBound := Report{Outcome: OutcomeCompleted, Notes: many[:MaxNotes]}
	if err := atTheBound.Validate(); err != nil {
		t.Errorf("a report with %d notes: %v", MaxNotes, err)
	}
}

func TestOnlyTheNotesOfACountedReviewCount(t *testing.T) {
	held := []Note{
		{ReviewNote: noted(PointSecrets), Review: "rv-1", Attempt: 2, Commit: "aaa", Seq: 0},
		{ReviewNote: noted(PointInjection), Review: "rv-1", Attempt: 2, Commit: "aaa", Seq: 1},
		{ReviewNote: noted(PointAuthority), Review: "rv-0", Attempt: 1, Commit: "aaa", Seq: 0},
		{ReviewNote: noted(PointDependencies), Review: "rv-2", Attempt: 2, Commit: "bbb", Seq: 0},
	}
	got := CountedNotes(held, 2, "aaa")
	if len(got) != 2 || got[0].Point != PointSecrets || got[1].Point != PointInjection {
		t.Errorf("the notes that count are %+v", got)
	}
	if got := CountedNotes(held, 2, ""); len(got) != 0 {
		t.Errorf("work that left no commit has notes %+v", got)
	}
}
