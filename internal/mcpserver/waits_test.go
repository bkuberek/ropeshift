package mcpserver_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/mcpserver"
)

// serve runs one request against a worker that answers every call with a reply.
func serve(t *testing.T, method string, params any, reply lanes.Reply) (map[string]any, []lanes.Request) {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":` + string(raw) + "}\n"
	var calls []lanes.Request
	var out bytes.Buffer
	call := func(request lanes.Request) (lanes.Reply, error) {
		calls = append(calls, request)
		if request.Who {
			return lanes.Reply{OK: true}, nil
		}
		return reply, nil
	}
	if err := mcpserver.Serve(strings.NewReader(line), &out, call); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("%q: %v", out.String(), err)
	}
	return got, calls
}

// FR-SPEND-1: the tool's description says a lane never waits for CI, a review, or a merge, and offers no CI run
// as an example of a wait.
func TestLaneWaitSaysALaneNeverWaitsForCI(t *testing.T) {
	got, _ := serve(t, "tools/list", map[string]any{}, lanes.Reply{OK: true})
	tools := got["result"].(map[string]any)["tools"].([]any)
	for _, tool := range tools {
		entry := tool.(map[string]any)
		if entry["name"] != lanes.ToolLaneWait {
			continue
		}
		description := entry["description"].(string)
		if strings.Contains(description, "a CI run") || strings.Contains(description, "such as a CI") {
			t.Errorf("the description offers CI as a wait: %q", description)
		}
		for _, want := range []string{"never wait for CI, a review, or a merge", lanes.ToolLaneReport, "parking"} {
			if !strings.Contains(description, want) {
				t.Errorf("the description leaves out %q: %q", want, description)
			}
		}
		return
	}
	t.Fatal("no lane_wait tool")
}

// A wait the worker parks comes back as `parking`, with what to do next, and is no error.
func TestAWaitTheWorkerParksReadsAsParking(t *testing.T) {
	got, calls := serve(t, "tools/call", map[string]any{"name": lanes.ToolLaneWait, "arguments": map[string]any{"minutes": 30, "reason": "a timer"}},
		lanes.Reply{OK: true, Parking: true})
	result := got["result"].(map[string]any)
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if result["isError"] == true || !strings.HasPrefix(text, "parking") || !strings.Contains(text, "end your turn") {
		t.Errorf("the lane hears %v: %q", result["isError"], text)
	}
	var waits int
	for _, call := range calls {
		if call.Wait != nil && call.Wait.Minutes == 30 {
			waits++
		}
	}
	if waits != 1 {
		t.Errorf("the worker heard %d waits of 30 minutes", waits)
	}
}

// A wait that stays live is recorded as it always was.
func TestAWaitThatStaysLiveIsRecorded(t *testing.T) {
	got, _ := serve(t, "tools/call", map[string]any{"name": lanes.ToolLaneWait, "arguments": map[string]any{"minutes": 3}}, lanes.Reply{OK: true})
	result := got["result"].(map[string]any)
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if result["isError"] == true || strings.HasPrefix(text, "parking") || !strings.Contains(text, "recorded") {
		t.Errorf("the lane hears %v: %q", result["isError"], text)
	}
}
