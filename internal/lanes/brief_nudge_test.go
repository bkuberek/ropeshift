package lanes

import (
	"strings"
	"testing"
)

// A lane that rested on its own background work for as long as it may wait live is asked what its work reached and to
// say the background work still ran. The nudge names no outcome, because a lane that reads a job that outlasted the
// wait as a failure to proceed reports blocked for it, and the tool takes the outcome the work reached.
func TestTheBackgroundNudgeAsksForTheOutcomeTheWorkReachedAndThatTheWorkStillRan(t *testing.T) {
	for _, want := range []string{
		"background work you started still running",
		"Call the " + ToolLaneReport + " tool now, once",
		"with the outcome your work has reached",
		"say in the reason that the background work was still running",
	} {
		if !strings.Contains(BackgroundNudge, want) {
			t.Errorf("the nudge %q leaves out %q", BackgroundNudge, want)
		}
	}
	for _, outcome := range []string{"blocked", "failed", "completed"} {
		if strings.Contains(BackgroundNudge, outcome) {
			t.Errorf("the nudge %q names the outcome %q, which the lane's own work decides", BackgroundNudge, outcome)
		}
	}
	if strings.Contains(BackgroundNudge, "has not reported") {
		t.Errorf("the nudge %q states the wait as a failure to report", BackgroundNudge)
	}
}
