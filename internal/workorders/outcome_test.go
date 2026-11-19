package workorders

import (
	"encoding/json"
	"strings"
	"testing"
)

// The merge commit a worker made to import a design repository's history is carried by a lane's end under its own
// key, and is left out of a lane that imported nothing.
func TestALaneResultCarriesTheImportMerge(t *testing.T) {
	raw, err := json.Marshal(LaneResult{Report: Report{Outcome: OutcomeCompleted}, ImportMerge: "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	var back LaneResult
	if err := json.Unmarshal(raw, &back); err != nil || back.ImportMerge != "abc123" {
		t.Errorf("the lane's end reads %+v back from %s: %v", back, raw, err)
	}
	if plain, err := json.Marshal(LaneResult{Report: Report{Outcome: OutcomeCompleted}}); err != nil || strings.Contains(string(plain), "import_merge") {
		t.Errorf("a lane that imported nothing is encoded as %s: %v", plain, err)
	}
}
