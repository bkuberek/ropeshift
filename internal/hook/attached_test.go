package hook

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
)

// markedAttached writes a marker as the worker would, last touched an age ago.
func markedAttached(t *testing.T, age time.Duration) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "attached-1")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	touched := time.Now().Add(-age)
	if err := os.Chtimes(path, touched, touched); err != nil {
		t.Fatal(err)
	}
	return path
}

func planning(marker string) lanes.DenyPolicy {
	return lanes.DenyPolicy{QuestionTool: lanes.QuestionToolTerminalIfAttached, AttachedMarker: marker}
}

// With a person attached to a planning lane's pane, the question tool is theirs: the hook gives no
// decision, so the terminal shows the tool, and it forwards no question, because the person answers it
// in the conversation and a recorded copy would ask the same thing again.
func TestAnAttachedPersonGetsTheQuestionTool(t *testing.T) {
	started := time.Now()
	out, events := questionHook(t, planning(markedAttached(t, 100*time.Millisecond)), askUserInput)
	if out != "" {
		t.Errorf("the hook decided: %q", out)
	}
	if len(events) != 1 || len(events[0].Asked) != 0 || events[0].Detail == lanes.DetailDenied {
		t.Errorf("events %+v", events)
	}
	if elapsed := time.Since(started); elapsed > hardDeadline {
		t.Errorf("the hook took %s, past its %s deadline", elapsed, hardDeadline)
	}
}

// With nobody attached, or nothing to say so, the question goes where every other lane's goes: it is
// refused, and the agent is told to use `ask`.
func TestAPlanningLaneWithNobodyAttachedIsToldToAsk(t *testing.T) {
	tests := []struct {
		name   string
		marker string
	}{
		{"a marker that went stale", markedAttached(t, lanes.AttachedStale+time.Second)},
		{"no marker", filepath.Join(t.TempDir(), "attached-1")},
		{"a marker in a directory that is gone", filepath.Join(t.TempDir(), "gone", "attached-1")},
		{"a policy with no marker path", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, events := questionHook(t, planning(tt.marker), askUserInput)
			golden(t, "question-interactive.golden", out)
			if len(events) != 1 || len(events[0].Asked) != 0 || events[0].Detail != lanes.DetailDenied {
				t.Errorf("events %+v", events)
			}
		})
	}
}

// A marker that says attached covers the question tool and nothing else: a lane that is not a planning
// lane keeps the answer its own policy gives.
func TestAnAttachedMarkerChangesNothingForAnotherLane(t *testing.T) {
	policy := lanes.DenyPolicy{QuestionTool: lanes.QuestionToolDeny, AttachedMarker: markedAttached(t, 0)}
	out, _ := questionHook(t, policy, askUserInput)
	golden(t, "question-interactive.golden", out)
}
