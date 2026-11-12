package roster

import (
	"slices"
	"strings"
	"testing"
)

var native = Support{Subagents: SubagentsNative, SubagentTools: ToolsEnforced}

// TestEachSessionReceivesTheAgentsItsRulesFit is FR-AGENT-9 and FR-AGENT-10: a writing lane gets every
// enabled agent, a review lane and the assistant only read-only ones, the orchestrator none, and no
// agent's tools reach past what its session's rules allow.
func TestEachSessionReceivesTheAgentsItsRulesFit(t *testing.T) {
	base := Resolve(Layer{}, Layer{})
	off := Resolve(mustApply(t, Layer{}, base, Change{Op: OpDisable, Agent: "lookup"}), Layer{})
	names := func(fitted []Fitted) []string {
		var out []string
		for _, f := range fitted {
			out = append(out, f.Name)
		}
		return out
	}
	writing := off.ForSession(SessionWriting, native)
	if got := names(writing); slices.Contains(got, "lookup") || len(got) != 9 {
		t.Errorf("a writing lane got %q", got)
	}
	readOnly := []string{"explore", "analysis", "review"}
	if got := names(off.ForSession(SessionReview, native)); !slices.Equal(got, readOnly) {
		t.Errorf("a review lane got %q", got)
	}
	if got := names(off.ForSession(SessionAssistant, native)); !slices.Equal(got, readOnly) {
		t.Errorf("the assistant got %q", got)
	}
	if got := off.ForSession(SessionOrchestrator, native); len(got) != 0 {
		t.Errorf("the orchestrator got %q", names(got))
	}
	if got := base.ForSession(SessionWriting, Support{Subagents: SubagentsNone}); len(got) != 0 {
		t.Errorf("a driver with no subagents got %q", names(got))
	}

	for _, session := range []Session{SessionWriting, SessionReview, SessionAssistant} {
		permitted := Permitted(session)
		for _, f := range base.ForSession(session, native) {
			for _, tool := range f.Tools {
				if !slices.Contains(permitted, tool) {
					t.Errorf("%s in a %s session holds %s", f.Name, session, tool)
				}
				if slices.Contains(f.Agent.Tools.Deny, tool) {
					t.Errorf("%s holds %s, which it denies", f.Name, tool)
				}
			}
			if slices.Contains(f.Tools, ToolDelegate) || !slices.Contains(f.Denied, ToolDelegate) {
				t.Errorf("%s may start another agent: %+v", f.Name, f)
			}
			if f.Access == AccessReadOnly && (slices.Contains(f.Tools, ToolEdit) || !slices.Contains(f.Denied, ToolEdit)) {
				t.Errorf("read-only %s may edit: %+v", f.Name, f)
			}
			if len(f.Tools) == 0 {
				t.Errorf("%s holds no tool", f.Name)
			}
		}
	}
	analysis := fitted(t, base.ForSession(SessionWriting, native), "analysis")
	if slices.Contains(analysis.Tools, ToolShell) {
		t.Errorf("a read-only agent in a writing lane holds a shell: %+v", analysis)
	}
	if review := fitted(t, base.ForSession(SessionReview, native), "analysis"); !slices.Contains(review.Tools, ToolShell) {
		t.Errorf("analysis in a review lane holds no shell: %+v", review)
	}
	for _, f := range base.ForSession(SessionAssistant, native) {
		if slices.Contains(f.Tools, ToolShell) {
			t.Errorf("the assistant's %s holds a shell", f.Name)
		}
	}
}

// TestADriverThatCannotEnforceToolsGivesAWritingSessionNoReadOnlyAgent is FR-AGENT-10.
func TestADriverThatCannotEnforceToolsGivesAWritingSessionNoReadOnlyAgent(t *testing.T) {
	base := Resolve(Layer{}, Layer{})
	loose := Support{Subagents: SubagentsNative, SubagentTools: ToolsInstructionsOnly}
	for _, f := range base.ForSession(SessionWriting, loose) {
		if f.Access == AccessReadOnly {
			t.Errorf("a writing session got read-only %s", f.Name)
		}
	}
	if got := base.ForSession(SessionReview, loose); len(got) != 4 {
		t.Errorf("a review session got %d agents", len(got))
	}
}

// TestTheCatalogCarriesTheGuideAndEachAgent is FR-AGENT-3.
func TestTheCatalogCarriesTheGuideAndEachAgent(t *testing.T) {
	base := Resolve(Layer{}, Layer{})
	lane, _ := base.Agent("implementation")
	text := Text(base.Guide, base.ForSession(SessionWriting, native), true, &lane.Agent)
	for _, want := range []string{base.Guide, "### deep-implementation", "approval for each start", "You run as the roster agent implementation", lane.Report} {
		if !strings.Contains(text, want) {
			t.Errorf("the text holds no %q:\n%s", want, text)
		}
	}
	none := Text(base.Guide, base.ForSession(SessionWriting, native), false, nil)
	if !strings.Contains(none, "cannot start these agents") {
		t.Errorf("a session with no subagents was not told:\n%s", none)
	}
}

func fitted(t *testing.T, all []Fitted, name string) Fitted {
	t.Helper()
	for _, f := range all {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no %s in %+v", name, all)
	return Fitted{}
}
