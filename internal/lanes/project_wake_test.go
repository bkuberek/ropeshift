package lanes_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/parking"
)

// A lane a project's limit parked waits on the project's limit rising, on the end of the window that held the project
// back, or on a person. The first is the fact a raise settles, and the second a timer the control plane's tick reads, so
// a day that ends wakes the lane once with no one there.
func TestAProjectLimitWakeNamesTheProjectTheEndOfTheWindowAndAPerson(t *testing.T) {
	expr, err := parking.Parse(lanes.ProjectLimitWake(1_800_000_000_000), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	limit := parking.Fact{Kind: parking.LimitRaised, Key: parking.ProjectLimit}
	person := parking.Fact{Kind: parking.HumanResume, Key: "lane-1"}
	if !expr.Names(parking.LimitRaised) {
		t.Error("the wake names no limit, so a raise reads no lane it parked")
	}
	for name, tt := range map[string]struct {
		facts parking.Facts
		now   int64
		want  bool
	}{
		"before the window ends":      {parking.Facts{}, 1_799_999_999_999, false},
		"at the end of the window":    {parking.Facts{}, 1_800_000_000_000, true},
		"a raised limit":              {parking.Facts{limit: true}, 1, true},
		"a person":                    {parking.Facts{person: true}, 1, true},
		"a limit that is still below": {parking.Facts{limit: false}, 1, false},
	} {
		if got := expr.Holds(tt.facts, tt.now); got != tt.want {
			t.Errorf("%s: the wake holds %v, want %v", name, got, tt.want)
		}
	}
	if next, found := expr.FirstTime(); !found || next != 1_800_000_000_000 {
		t.Errorf("the wake is read at %d, %v, and the tick must find it at the end of the window", next, found)
	}
}
