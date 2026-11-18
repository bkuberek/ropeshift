package workorders

import (
	"encoding/json"
	"testing"
)

// The web UI reads a held start from the work order's own JSON, under the names below, so a rename here
// breaks the page that says why ready work is held.
func TestAHeldStartReadsAsStartHeldInTheWorkOrderJSON(t *testing.T) {
	t.Parallel()
	order := WorkOrder{ID: "wo-1", State: Ready, StartHeld: &StartHold{Reason: HoldWaitingForMerge, Dependency: "wo-0", Detail: "not yet in origin/main"}}
	raw, err := json.Marshal(order)
	if err != nil {
		t.Fatal(err)
	}
	var read struct {
		StartHeld map[string]string `json:"start_held"`
	}
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"reason": "waiting_for_merge", "dependency": "wo-0", "detail": "not yet in origin/main"}
	for key, value := range want {
		if read.StartHeld[key] != value {
			t.Errorf("start_held.%s = %q, want %q in %s", key, read.StartHeld[key], value, raw)
		}
	}
	raw, err = json.Marshal(WorkOrder{ID: "wo-2", State: Ready})
	if err != nil {
		t.Fatal(err)
	}
	var open struct {
		StartHeld map[string]string `json:"start_held"`
	}
	if err := json.Unmarshal(raw, &open); err != nil || open.StartHeld != nil {
		t.Errorf("a work order that is not held reads %v in %s: %v", open.StartHeld, raw, err)
	}
}
