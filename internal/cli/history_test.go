package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/test/apitest"
)

// TestMeasuresHistoryPrintsWhatAPlanningSessionSees shows a person the delivery history a planning lane's pack holds
// for the project, as text and as the fields behind it, from the project's own finished work (FR-EST-12).
func TestMeasuresHistoryPrintsWhatAPlanningSessionSees(t *testing.T) {
	s := newSession(t)
	if text := s.must("measures", "history"); !strings.Contains(text, "No work has finished in this project yet, so it has no history.") {
		t.Errorf("the history of a project with no finished work:\n%s", text)
	}
	s.completed("wo")
	s.must("work", "accept", "wo")
	if _, err := control.NewDeliveryFold(s.daemon.Store).Fold(t.Context(), apitest.Scope); err != nil {
		t.Fatal(err)
	}
	text := s.must("measures", "history")
	for _, want := range []string{"### By kind and size", "### By kind, size, capability class, and repository", "- change, unknown: 1 finished; delivery p50 ", "Low confidence."} {
		if !strings.Contains(text, want) {
			t.Errorf("the history leaves out %q:\n%s", want, text)
		}
	}
	var view api.DeliveryHistory
	if err := json.Unmarshal([]byte(s.must("measures", "history", "--json")), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.ByKindSize) != 1 || view.ByKindSize[0].Count != 1 || view.MinSamples == 0 || view.Repositories == nil {
		t.Errorf("the history reads %+v", view)
	}
	if code, _, _ := s.run("measures", "history", "extra"); code != cli.ExitUsage {
		t.Errorf("an extra argument exits %d", code)
	}
}

// TestTheHistoryGoldensPrintAsTheWebTestsRead writes the text `measures history` prints for the goldens the API test
// wrote, which the web tests hold the planning pane's History tab to.
func TestTheHistoryGoldensPrintAsTheWebTestsRead(t *testing.T) {
	for _, name := range []string{"history", "history-empty"} {
		var view api.DeliveryHistory
		readGolden(t, name+".json", &view)
		holdsText(t, name+".cli.txt", cli.HistoryText(view))
	}
}
