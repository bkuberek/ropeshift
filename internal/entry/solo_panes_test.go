package entry

import (
	"errors"
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/claudecode"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
)

// The web pane reaches a lane's pane through the worker's terminal with no client attached, so what a member
// types there is recorded for the worker, which reads it as a person at the pane while a planning lane waits.
// Input that reached no pane records nothing.
func TestTheWebPaneRecordsWhatAMemberTypesForTheWorker(t *testing.T) {
	t.Parallel()
	terminal := fake.NewTerminal()
	if _, err := terminal.Open(t.Context(), ports.PaneSpec{Name: claudecode.PaneName("lane-1", 1)}); err != nil {
		t.Fatal(err)
	}
	inputs := &worker.PaneInputs{}
	panes := soloPanes(terminal, inputs)
	before := time.Now()

	if err := panes.Send(t.Context(), "lane-2", 1, server.PaneInput{Keys: []string{"Down"}}); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("typing into a pane nobody holds: %v", err)
	}
	if inputs.TypedSince("lane-2", 1, before) {
		t.Error("input that reached no pane was recorded")
	}
	if err := panes.Send(t.Context(), "lane-1", 1, server.PaneInput{Keys: []string{"Down"}}); err != nil {
		t.Fatal(err)
	}
	if keys := terminal.KeysOf(claudecode.PaneName("lane-1", 1)); !slices.Equal(keys, []string{"Down"}) {
		t.Fatalf("the pane took %v", keys)
	}
	if !inputs.TypedSince("lane-1", 1, before) {
		t.Error("what a member typed into the pane was not recorded for the worker")
	}
	if inputs.TypedSince("lane-1", 2, before) {
		t.Error("input into one generation's pane was recorded for the next")
	}
	if inputs.TypedSince("lane-1", 1, time.Now().Add(time.Second)) {
		t.Error("input was recorded as later than it came")
	}
}
