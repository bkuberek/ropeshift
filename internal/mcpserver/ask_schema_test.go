package mcpserver_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

// A lane reads the ask tool's schema to learn what a question may carry, so the schema is held to a golden:
// options, a recommendation that is one of them, and the rationale for it.
func TestTheAskToolSchemaIsHeldToAGolden(t *testing.T) {
	for _, tool := range ask(t, &worker{}, "tools/list", map[string]any{}).Result.Tools {
		if tool.Name != lanes.ToolAsk {
			continue
		}
		got, err := json.MarshalIndent(tool.InputSchema, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join("testdata", "ask-schema.golden.json"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got)+"\n" != string(want) {
			t.Errorf("the ask schema:\n--- got\n%s\n--- want\n%s", got, want)
		}
		return
	}
	t.Fatal("the tool is not listed")
}

// A question's recommendation and its rationale reach the worker as the lane wrote them.
func TestAskPassesTheRecommendationOn(t *testing.T) {
	w := &worker{}
	ask(t, w, "tools/call", map[string]any{"name": lanes.ToolAsk, "arguments": map[string]any{
		"text": "Which?", "options": []string{"A", "B"}, "recommendation": "A", "recommendation_rationale": "It is simpler.",
	}})
	var got *lanes.AskRequest
	for _, request := range w.requests {
		if request.Ask != nil {
			got = request.Ask
		}
	}
	if got == nil || got.Recommendation != "A" || got.RecommendationRationale != "It is simpler." || len(got.Options) != 2 {
		t.Errorf("the worker was asked %+v", got)
	}
}
