package mcpserver_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// A question reaches a person in the shape they answer it in: a short title, what each option trades off, and what
// the answer unblocks, each as the lane wrote it.
func TestAskPassesTheShapeOn(t *testing.T) {
	w := &worker{}
	ask(t, w, "tools/call", map[string]any{"name": lanes.ToolAsk, "arguments": map[string]any{
		"text": "Which retry?", "title": "Forge retries", "options": []string{"A", "B"}, "recommendation": "A",
		"recommendation_rationale": "It is simpler.", "tradeoffs": map[string]string{"A": "It may give up early.", "B": "It holds the lane."},
		"unblocks": "The forge client.",
	}})
	var got *lanes.AskRequest
	for _, request := range w.requests {
		if request.Ask != nil {
			got = request.Ask
		}
	}
	if got == nil || got.Title != "Forge retries" || got.Tradeoffs["A"] != "It may give up early." || got.Tradeoffs["B"] != "It holds the lane." || got.Unblocks != "The forge client." {
		t.Errorf("the worker was asked %+v", got)
	}
}

// A question with no recommendation, or none that says why, would reach a person half-formed, so the tool sends it
// back to the lane, says what to add, and asks the worker nothing.
func TestAskSendsBackAQuestionWithNoRecommendation(t *testing.T) {
	for name, arguments := range map[string]map[string]any{
		"no recommendation":      {"text": "Which retry?", "options": []string{"A", "B"}},
		"a blank recommendation": {"text": "Which retry?", "options": []string{"A", "B"}, "recommendation": "  ", "recommendation_rationale": "Simpler."},
		"no reason for it":       {"text": "Which retry?", "options": []string{"A", "B"}, "recommendation": "A"},
	} {
		t.Run(name, func(t *testing.T) {
			w := &worker{}
			got := ask(t, w, "tools/call", map[string]any{"name": lanes.ToolAsk, "arguments": arguments})
			if !got.Result.IsError || len(got.Result.Content) != 1 {
				t.Fatalf("the tool answered %+v", got)
			}
			text := got.Result.Content[0].Text
			for _, want := range []string{"not asked", "recommendation", "recommendation_rationale", "ask again"} {
				if !strings.Contains(text, want) {
					t.Errorf("the refusal %q says nothing of %q", text, want)
				}
			}
			for _, request := range w.requests {
				if request.Ask != nil {
					t.Errorf("the worker was asked %+v", request.Ask)
				}
			}
		})
	}
}
