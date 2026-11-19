package contract

import (
	"errors"
	"testing"
)

// The comparison fails when a flow decides differently with findings than without, by an outcome or by an event, and
// passes when it does not, so the checks that use it can fail.
func TestThePracticesNeverBlockComparisonCanFail(t *testing.T) {
	t.Parallel()
	run := func(differ func(withFindings bool, d *decided)) error {
		return sameWithAndWithoutFindings(NewRig(t, SQLiteMemory), func(r *Rig, seed seeder) (decided, error) {
			d := decided{outcomes: []string{"claimed"}, events: []string{"worker lane lane.started"}}
			differ(seed != nil, &d)
			return d, nil
		})
	}
	if err := run(func(bool, *decided) {}); err != nil {
		t.Errorf("flows that decide the same differ: %v", err)
	}
	var violation *Violation
	if err := run(func(with bool, d *decided) {
		if with {
			d.outcomes = []string{"refused"}
		}
	}); !errors.As(err, &violation) {
		t.Errorf("an outcome that differs with findings was not caught: %v", err)
	}
	if err := run(func(with bool, d *decided) {
		if with {
			d.events = append(d.events, "system work_order work_order.held")
		}
	}); !errors.As(err, &violation) {
		t.Errorf("an event that differs with findings was not caught: %v", err)
	}
	if err := run(func(_ bool, d *decided) { d.events = nil }); !errors.As(err, &violation) {
		t.Errorf("a flow that records no events proves nothing, and was not refused: %v", err)
	}
}
