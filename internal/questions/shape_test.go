package questions

import (
	"errors"
	"maps"
	"strings"
	"testing"
)

// A question reaches a person in a shape they can answer at a glance: a short title, what each option trades off,
// and what the answer unblocks, as the asker wrote them.
func TestADraftKeepsItsTitleTradeoffsAndWhatItUnblocks(t *testing.T) {
	got, err := New(Question{
		ID: "q", Text: "How often does a call to the forge retry before it gives up?", Title: "  Forge retries ",
		Options:        []string{"Twice", "Until the deadline"},
		Recommendation: "Twice", RecommendationRationale: "It fits the deadline.",
		Tradeoffs: map[string]string{" Twice ": " Cheap, and it may give up early. ", "Until the deadline": "It holds a lane longer."},
		Unblocks:  " The retry policy of the forge client. ",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"Twice": "Cheap, and it may give up early.", "Until the deadline": "It holds a lane longer."}
	if got.Title != "Forge retries" || !maps.Equal(got.Tradeoffs, want) || got.Unblocks != "The retry policy of the forge client." {
		t.Errorf("recorded the title %q, the tradeoffs %q, and unblocks %q", got.Title, got.Tradeoffs, got.Unblocks)
	}
}

func TestADraftRefusesAShapeItCannotHold(t *testing.T) {
	draft := Question{ID: "q", Text: "How often does a call retry?", Options: []string{"Twice", "Until the deadline"}}
	for name, change := range map[string]func(*Question){
		"a tradeoff of an option it does not offer": func(q *Question) { q.Tradeoffs = map[string]string{"Never": "Nothing retries."} },
		"tradeoffs with no options":                 func(q *Question) { q.Options, q.Tradeoffs = nil, map[string]string{"Twice": "Cheap."} },
		"a title on two lines":                      func(q *Question) { q.Title = "Forge\nretries" },
		"a title over its bound":                    func(q *Question) { q.Title = strings.Repeat("t", MaxTitleBytes+1) },
		"a tradeoff over its bound":                 func(q *Question) { q.Tradeoffs = map[string]string{"Twice": strings.Repeat("x", MaxFieldBytes+1)} },
		"what it unblocks over its bound":           func(q *Question) { q.Unblocks = strings.Repeat("u", MaxFieldBytes+1) },
	} {
		t.Run(name, func(t *testing.T) {
			q := draft
			change(&q)
			if got, err := New(q); !errors.Is(err, ErrInvalid) {
				t.Errorf("accepted %+v, %v", got, err)
			}
		})
	}
}

// A tradeoff left empty says nothing, so it is not kept.
func TestADraftDropsATradeoffThatSaysNothing(t *testing.T) {
	got, err := New(Question{ID: "q", Text: "Which?", Options: []string{"A", "B"}, Tradeoffs: map[string]string{"A": "Fast.", "B": "  "}})
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(got.Tradeoffs, map[string]string{"A": "Fast."}) {
		t.Errorf("recorded the tradeoffs %q", got.Tradeoffs)
	}
}

// A batch shows each question in its shape, and a tradeoff only for an option the question still offers, since an
// escalation may have replaced them.
func TestPresentShowsEachQuestionInItsShape(t *testing.T) {
	rows := []Question{{
		ID: "q1", Text: "Which?", Status: StatusNeedsHuman, Title: "Which one", Options: []string{"A", "C"},
		Tradeoffs: map[string]string{"A": "Fast.", "B": "Gone."}, Unblocks: "The import.",
	}}
	items := Present(Batch{ID: "b1"}, rows, "", DefaultPolicy()).Items
	if len(items) != 1 {
		t.Fatalf("items %+v", items)
	}
	if got := items[0]; got.Title != "Which one" || !maps.Equal(got.Tradeoffs, map[string]string{"A": "Fast."}) || got.Unblocks != "The import." {
		t.Errorf("presented %+v", got)
	}
}
