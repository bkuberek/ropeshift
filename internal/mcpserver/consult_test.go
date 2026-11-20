package mcpserver_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/mcpserver"
)

// An orchestrator's `consult` reaches its worker as it was written, with the question, what to explore, and the
// agent it names, and its schema asks for the first two (FR-ORCH-9).
func TestConsultReachesTheWorkerWithWhatItAsks(t *testing.T) {
	w := &worker{owner: lanes.OwnerOrchestrator, work: json.RawMessage(`{"consultation":"c1"}`)}
	arguments := map[string]any{"project": "p1", "question": "q1", "ask": "Compare the two retry budgets.", "agent": "review"}
	got := ask(t, w, "tools/call", map[string]any{"name": lanes.ToolConsult, "arguments": arguments})
	if got.Error != nil || got.Result.IsError || !strings.Contains(got.Result.Content[0].Text, "c1") {
		t.Fatalf("answer %+v", got)
	}
	calls := w.works()
	if len(calls) != 1 || calls[0].Tool != lanes.ToolConsult {
		t.Fatalf("calls %+v", calls)
	}
	var sent map[string]string
	if err := json.Unmarshal(calls[0].Args, &sent); err != nil || sent["question"] != "q1" || sent["ask"] != "Compare the two retry budgets." || sent["agent"] != "review" {
		t.Errorf("the worker read %s: %v", calls[0].Args, err)
	}
	for _, tool := range ask(t, w, "tools/list", map[string]any{}).Result.Tools {
		if tool.Name != lanes.ToolConsult {
			continue
		}
		required, _ := tool.InputSchema["required"].([]any)
		if len(required) != 3 || required[0] != "project" || required[1] != "question" || required[2] != "ask" {
			t.Errorf("consult requires %v", required)
		}
	}
}

// A consultant reports through its own tool, which reaches the worker under its name. Nobody else holds it, and a
// consultant holds none of an orchestrator's tools. The worker refuses a lane's request on its token too, in a test of
// its own, because a session can reach the socket without this process.
func TestOnlyAConsultantReportsAConsultation(t *testing.T) {
	report := map[string]any{"options": []string{"a", "b"}, "recommendation": "a", "rationale": "It costs less."}
	consultant := &worker{owner: mcpserver.OwnerConsultant}
	got := ask(t, consultant, "tools/call", map[string]any{"name": lanes.ToolConsultReport, "arguments": report})
	if got.Error != nil || got.Result.IsError {
		t.Fatalf("answer %+v", got)
	}
	if calls := consultant.works(); len(calls) != 1 || calls[0].Tool != lanes.ToolConsultReport {
		t.Errorf("calls %+v", calls)
	}
	for name, w := range map[string]*worker{"a lane": {}, "an orchestrator": {owner: lanes.OwnerOrchestrator}, "an assistant": {owner: mcpserver.OwnerAssistant}} {
		if got := ask(t, w, "tools/call", map[string]any{"name": lanes.ToolConsultReport, "arguments": report}); got.Error == nil {
			t.Errorf("%s reported a consultation: %+v", name, got)
		}
		if calls := w.works(); len(calls) != 0 {
			t.Errorf("%s reached the worker: %+v", name, calls)
		}
	}
	for _, tool := range []string{lanes.ToolConsult, lanes.ToolWorkClaim, lanes.ToolDecisionRecord, lanes.ToolApprovalDecide} {
		if got := ask(t, consultant, "tools/call", map[string]any{"name": tool, "arguments": map[string]any{"question": "q1"}}); got.Error == nil && !got.Result.IsError {
			t.Errorf("a consultant called %s: %+v", tool, got)
		}
	}
}
