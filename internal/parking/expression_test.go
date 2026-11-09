package parking_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/parking"
)

const lane = "lane-1"

func parse(t *testing.T, text string) parking.Expr {
	t.Helper()
	expr, err := parking.Parse(text, lane)
	if err != nil {
		t.Fatalf("%s: %v", text, err)
	}
	return expr
}

func fact(kind parking.Kind, key string) parking.Fact { return parking.Fact{Kind: kind, Key: key} }

func TestEachTermIsTrueWhenItsFactHolds(t *testing.T) {
	tests := []struct {
		name string
		text string
		fact parking.Fact
	}{
		{"a question", `{"question_answered":"q1"}`, fact(parking.QuestionAnswered, "q1")},
		{"an approval", `{"approval_decided":"a1"}`, fact(parking.ApprovalDecided, "a1")},
		{"a decision", `{"decision_recorded":"payments"}`, fact(parking.DecisionRecorded, "payments")},
		{"a usage limit", `{"usage_reset":"worker:driver"}`, fact(parking.UsageReset, "worker:driver")},
		{"a sign-in", `{"signin_restored":"worker:driver"}`, fact(parking.SigninRestored, "worker:driver")},
		{"a work order", `{"work_order_done":"wo-1"}`, fact(parking.WorkOrderDone, "wo-1")},
		{"a check", `{"external_check":"ci"}`, fact(parking.ExternalCheck, "ci")},
		{"a person", `{"human_resume":true}`, fact(parking.HumanResume, lane)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr := parse(t, tt.text)
			if expr.Holds(parking.Facts{}, 0) {
				t.Error("true with no facts")
			}
			if !expr.Holds(parking.Facts{tt.fact: true}, 0) {
				t.Error("false with its fact")
			}
			if got := expr.Facts(); !slices.Equal(got, []parking.Fact{tt.fact}) {
				t.Errorf("facts %v", got)
			}
			if !expr.Names(tt.fact.Kind) {
				t.Errorf("does not name %s", tt.fact.Kind)
			}
			if expr.Names(parking.Kind("other")) {
				t.Error("names what it does not hold")
			}
		})
	}
}

// A timer fires at or after its time and never before, and the time is an input.
func TestATimerReadsTheTimeItIsGiven(t *testing.T) {
	expr := parse(t, `{"at":1000}`)
	tests := []struct {
		now  int64
		want bool
	}{{999, false}, {1000, true}, {1001, true}}
	for _, tt := range tests {
		if got := expr.Holds(parking.Facts{}, tt.now); got != tt.want {
			t.Errorf("at %d: %v", tt.now, got)
		}
	}
}

func TestCompositesCombineTheirTerms(t *testing.T) {
	printed := `{"any_of":[{"all_of":[{"question_answered":"a"},{"question_answered":"b"}]},{"human_resume":true}]}`
	a, b, person := fact(parking.QuestionAnswered, "a"), fact(parking.QuestionAnswered, "b"), fact(parking.HumanResume, lane)
	tests := []struct {
		name  string
		facts parking.Facts
		want  bool
	}{
		{"nothing", parking.Facts{}, false},
		{"one of two", parking.Facts{a: true}, false},
		{"both", parking.Facts{a: true, b: true}, true},
		{"a person alone", parking.Facts{person: true}, true},
	}
	expr := parse(t, printed)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expr.Holds(tt.facts, 0); got != tt.want {
				t.Errorf("got %v", got)
			}
		})
	}
	if got := expr.Facts(); len(got) != 3 {
		t.Errorf("facts %v", got)
	}
}

// After a false evaluation the tick needs the next moment anything can change, or none.
func TestTheNextTimeIsTheEarliestStillAhead(t *testing.T) {
	tests := []struct {
		name string
		text string
		now  int64
		next int64
		has  bool
	}{
		{"one ahead", `{"at":100}`, 50, 100, true},
		{"one past", `{"at":100}`, 150, 0, false},
		{"exactly now is not ahead", `{"at":100}`, 100, 0, false},
		{"no time at all", `{"question_answered":"q"}`, 50, 0, false},
		{"the earlier of two", `{"any_of":[{"at":300},{"at":200}]}`, 50, 200, true},
		{"past one and not the other", `{"any_of":[{"at":100},{"at":200}]}`, 150, 200, true},
		{"inside all_of", `{"all_of":[{"at":100},{"question_answered":"q"}]}`, 50, 100, true},
		{"past, with a question left", `{"all_of":[{"at":100},{"question_answered":"q"}]}`, 150, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, has := parse(t, tt.text).NextTime(tt.now)
			if has != tt.has || next != tt.next {
				t.Errorf("next %d, %v", next, has)
			}
		})
	}
}

func TestTheFirstTimeIsTheEarliestWhateverNowIs(t *testing.T) {
	first, has := parse(t, `{"any_of":[{"at":300},{"all_of":[{"at":100},{"question_answered":"q"}]}]}`).FirstTime()
	if !has || first != 100 {
		t.Errorf("first %d, %v", first, has)
	}
	if _, has := parse(t, `{"human_resume":true}`).FirstTime(); has {
		t.Error("an event-only wake has a time")
	}
}

func TestWhatABuildCannotReadIsRefused(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"a term from a newer build", `{"moon_phase":"full"}`},
		{"a newer term inside a composite", `{"any_of":[{"human_resume":true},{"moon_phase":"full"}]}`},
		{"not JSON", `{`},
		{"not an object", `["at"]`},
		{"two terms in one object", `{"at":1,"human_resume":true}`},
		{"nothing", `{}`},
		{"an empty any_of", `{"any_of":[]}`},
		{"an empty all_of", `{"all_of":[]}`},
		{"a composite that holds no list", `{"all_of":{"at":1}}`},
		{"a time that is no number", `{"at":"soon"}`},
		{"a time that is no whole number", `{"at":1.5}`},
		{"a key that is no string", `{"question_answered":7}`},
		{"an empty key", `{"question_answered":""}`},
		{"a resume that says false", `{"human_resume":false}`},
		{"two documents", `{"at":1}{"at":2}`},
		{"too large", `{"question_answered":"` + strings.Repeat("x", parking.MaxExprBytes) + `"}`},
		{"too deep", strings.Repeat(`{"any_of":[`, parking.MaxDepth+1) + `{"at":1}` + strings.Repeat(`]}`, parking.MaxDepth+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parking.Parse(tt.text, lane); !errors.Is(err, parking.ErrUnreadable) {
				t.Errorf("parsed: %v", err)
			}
		})
	}
}

// The same bytes read the same way, so a replay reads what a firing read.
func TestAReadingIsPure(t *testing.T) {
	text := `{"all_of":[{"question_answered":"q"},{"at":100}]}`
	facts := parking.Facts{fact(parking.QuestionAnswered, "q"): true}
	first := parse(t, text)
	for range 3 {
		if !parse(t, text).Holds(facts, 100) || !first.Holds(facts, 100) {
			t.Fatal("one reading differs from another")
		}
	}
	if len(facts) != 1 {
		t.Errorf("a reading changed its facts: %v", facts)
	}
}
