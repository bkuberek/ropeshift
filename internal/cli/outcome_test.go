package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// A lane whose worktree root lies inside a clone never starts, and `work show` says the root, the clone, the
// layer that set the root, and the fix (FR-PROF-3).
func TestWorkShowSaysAWorktreeRootInsideACloneAndHowToFixIt(t *testing.T) {
	s := newSession(t)
	w := apitest.NewWorld(t, s.daemon)
	const detail = "worktree root /work/app/lanes is inside the clone /work/app, and the user's profile layer set it: " +
		"set `[worktree] root` in your profile to a folder outside every clone, or remove it to use the built-in root"
	w.Running("wo-inside", "Fix the build").EndSaying(workorders.OutcomeFailed, workorders.ReasonWorktreeInsideClone, detail)
	if text := s.must("work", "show", "wo-inside"); !strings.Contains(text, "outcome\tworktree_inside_clone\t"+detail+"\n") {
		t.Errorf("a lane refused for its root says:\n%s", text)
	}
}

// A lane that never started, or ended in a way its reason names, says so where a person asks about its work:
// `work show` prints the reason its outcome gave, and what the worker said of it, such as the delegate policy
// version that refused the start (FR-AUTH-16). The event stream carries no record's content, so the work's own
// page is where the command line says why. Work whose lane still runs, or that ended with no reason, says
// nothing of one, and what a lane wrote is made safe for a terminal: the ledger keeps its detail as plain text, and the command line escapes what it is handed raw.
func TestWorkShowSaysWhyAWorksLaneEnded(t *testing.T) {
	s := newSession(t)
	w := apitest.NewWorld(t, s.daemon)
	const detail = `delegate policy version 2 keeps a delegate's lane start from raising agent.effort from "medium" to "max"`
	w.Running("wo-refused", "Raise the effort").EndSaying(workorders.OutcomeFailed, workorders.ReasonDelegatePolicy, detail)
	w.Running("wo-crashed", "Fix the build").End(workorders.OutcomeFailed, workorders.ReasonCrashed)
	w.Running("wo-hostile", "Tidy the logs").EndSaying(workorders.OutcomeFailed, "gave up\nsecond line", "cleared \x1b[2J the screen")
	w.Running("wo-live", "Still going")

	show := func(id string) string {
		t.Helper()
		return s.must("work", "show", id)
	}
	if text := show("wo-refused"); !strings.Contains(text, "outcome\tdelegate_policy\t"+detail+"\n") {
		t.Errorf("a refused lane says nothing of why:\n%s", text)
	}
	if text := show("wo-crashed"); !strings.Contains(text, "outcome\tcrashed\n") {
		t.Errorf("a lane that crashed says nothing of why:\n%s", text)
	}
	text := show("wo-hostile")
	// The control plane keeps a lane's detail as plain text, so the escape is gone before the command line reads it.
	if strings.Contains(text, "\x1b") || !strings.Contains(text, "outcome\tgave up second line\tcleared the screen\n") {
		t.Errorf("what a lane wrote reaches the terminal as it is:\n%q", text)
	}
	if text := show("wo-live"); strings.Contains(text, "outcome") {
		t.Errorf("work whose lane runs has an outcome:\n%s", text)
	}

	var detailJSON map[string]any
	if err := json.Unmarshal([]byte(s.must("work", "show", "wo-refused", "--json")), &detailJSON); err != nil {
		t.Fatal(err)
	}
	if detailJSON["reason"] != workorders.ReasonDelegatePolicy || detailJSON["detail"] != detail {
		t.Errorf("the JSON says %v and %v", detailJSON["reason"], detailJSON["detail"])
	}
}

// What a lane wrote reaches the command line made safe for a terminal, whatever the ledger kept. The control plane
// stores a lane's detail as plain text with no credential, so a detail that went through it holds no escape, but the
// command line does not rely on that: it prints an outcome it was handed raw, such as one an older ledger stored, as
// text with no escape and no line break.
func TestWorkShowEscapesAnOutcomeTheLedgerStoredRaw(t *testing.T) {
	text := cli.WorkDetailText(api.WorkDetail{
		WorkOrder: workorders.WorkOrder{ID: "wo-hostile", Title: "Tidy the logs", State: workorders.Failed},
		Reason:    "gave up\nsecond line",
		Detail:    "cleared \x1b[2J the screen\x07\nand a second line",
	})
	if strings.ContainsAny(text, "\x1b\x07") {
		t.Errorf("a stored outcome reaches the terminal with a control character:\n%q", text)
	}
	if !strings.Contains(text, "outcome\tgave up second line\tcleared ?[2J the screen? and a second line\n") {
		t.Errorf("a stored outcome is not printed on one line, made safe:\n%q", text)
	}
}
