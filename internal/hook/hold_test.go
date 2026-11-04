package hook

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/lanes"
)

func toolCall(tool string) string {
	raw, _ := json.Marshal(map[string]any{"session_id": "s1", "tool_name": tool, "tool_input": map[string]string{"command": "true"}})
	return string(raw)
}

// held is a lane the context policy asked to hand off, whose worker need not be up.
func held() lanes.DenyPolicy {
	return lanes.DenyPolicy{Hold: lanes.ReasonHandOff, OwnTools: "mcp__ropeshift__", QuestionTool: lanes.QuestionToolDefer}
}

func TestAHoldDeniesTheNextToolWithItsReason(t *testing.T) {
	out, _ := questionHook(t, held(), toolCall(lanes.ToolBash))
	if !strings.Contains(out, `"deny"`) || !strings.Contains(out, lanes.ToolLaneHandoff) {
		t.Errorf("output %s", out)
	}
}

// The way out stays open, or a held lane could not do what it is told.
func TestAHoldLeavesTheLanesOwnTools(t *testing.T) {
	for _, tool := range []string{"mcp__ropeshift__lane_handoff", "mcp__ropeshift__lane_report"} {
		if out, _ := questionHook(t, held(), toolCall(tool)); strings.Contains(out, `"deny"`) {
			t.Errorf("%s: %s", tool, out)
		}
	}
}

// A hold stops the agent's question tool too, which would otherwise end the turn on a question.
func TestAHoldComesBeforeTheQuestionTool(t *testing.T) {
	out, _ := questionHook(t, held(), askUserInput)
	if !strings.Contains(out, `"deny"`) || !strings.Contains(out, lanes.ToolLaneHandoff) {
		t.Errorf("output %s", out)
	}
}

func TestNoHoldLeavesAToolAlone(t *testing.T) {
	policy := held()
	policy.Hold = ""
	if out, _ := questionHook(t, policy, toolCall(lanes.ToolBash)); strings.Contains(out, `"deny"`) {
		t.Errorf("output %s", out)
	}
}

// With no prefix named, nothing is the lane's own, and an empty prefix matches no tool by accident.
func TestAHoldWithNoOwnToolsDeniesEveryTool(t *testing.T) {
	policy := held()
	policy.OwnTools = ""
	if out, _ := questionHook(t, policy, toolCall("mcp__ropeshift__lane_handoff")); !strings.Contains(out, `"deny"`) {
		t.Errorf("output %s", out)
	}
}
