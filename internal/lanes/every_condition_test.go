package lanes_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
)

// TestEveryConditionIsListed holds the list a page's words are tested against to what Conditions can
// raise. Each flag of Facts stands alone, so a new flag that raises a new condition fails here.
func TestEveryConditionIsListed(t *testing.T) {
	seen := map[lanes.Condition]bool{}
	note := func(facts lanes.Facts) {
		for _, condition := range lanes.Conditions(facts) {
			seen[condition] = true
		}
	}
	note(lanes.Facts{State: lanes.Working, SinceProgress: time.Hour, StallAfter: time.Minute})
	note(lanes.Facts{ContextTokens: 2, WarnTokens: 1})
	note(lanes.Facts{State: lanes.Tripped, DayCapped: true})
	shape := reflect.TypeFor[lanes.Facts]()
	for i := range shape.NumField() {
		if shape.Field(i).Type.Kind() != reflect.Bool {
			continue
		}
		var facts lanes.Facts
		reflect.ValueOf(&facts).Elem().Field(i).SetBool(true)
		note(facts)
	}
	for condition := range seen {
		if !slices.Contains(lanes.EveryCondition, condition) {
			t.Errorf("%s is raised and not listed", condition)
		}
	}
	for _, condition := range lanes.EveryCondition {
		if !seen[condition] {
			t.Errorf("%s is listed and never raised", condition)
		}
	}
}
