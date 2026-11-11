package mcpserver_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/mcpserver"
	"ropeshift.com/ropeshift/internal/workorders"
)

// worker stands in for the socket, and keeps what reached it.
type worker struct {
	owner    string
	down     bool
	refuse   string
	requests []lanes.Request
}

func (w *worker) call(request lanes.Request) (lanes.Reply, error) {
	w.requests = append(w.requests, request)
	switch {
	case w.down:
		return lanes.Reply{}, errors.New("the worker is not there")
	case request.Who:
		return lanes.Reply{OK: true, Owner: w.owner}, nil
	case w.refuse != "":
		return lanes.Reply{Code: lanes.CodeForbidden, Message: w.refuse}, nil
	}
	return lanes.Reply{OK: true}, nil
}

func (w *worker) works() []lanes.WorkRequest {
	var out []lanes.WorkRequest
	for _, request := range w.requests {
		if request.Work != nil {
			out = append(out, *request.Work)
		}
	}
	return out
}

func (w *worker) decisions() []lanes.DecideRequest {
	var out []lanes.DecideRequest
	for _, request := range w.requests {
		if request.Decide != nil {
			out = append(out, *request.Decide)
		}
	}
	return out
}

type answer struct {
	Result struct {
		Tools []struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func ask(t *testing.T, w *worker, method string, params any) answer {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":%s}`+"\n", method, raw)
	var out bytes.Buffer
	if err := mcpserver.Serve(strings.NewReader(line), &out, w.call); err != nil {
		t.Fatal(err)
	}
	var got answer
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("%q: %v", out.String(), err)
	}
	return got
}

func listed(t *testing.T, w *worker) []string {
	t.Helper()
	var names []string
	for _, tool := range ask(t, w, "tools/list", map[string]any{}).Result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func decide(arguments map[string]any) map[string]any {
	return map[string]any{"name": lanes.ToolApprovalDecide, "arguments": arguments}
}

// The tools depend on the caller, and only the worker knows who a token stands for.
func TestToolsDependOnWhoTheTokenStandsFor(t *testing.T) {
	tests := []struct {
		name   string
		worker worker
		want   []string
	}{
		{name: "a lane", want: lanes.Tools},
		{name: "an orchestrator", worker: worker{owner: lanes.OwnerOrchestrator}, want: lanes.OrchestratorTools},
		{name: "a worker out of reach", worker: worker{owner: lanes.OwnerOrchestrator, down: true}, want: lanes.Tools},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listed(t, &tt.worker); !slices.Equal(got, tt.want) {
				t.Errorf("listed %v, want %v", got, tt.want)
			}
		})
	}
}

// The numbers are the decision's (ADR-0014): more tools raise prompt cost and widen authority, so a
// new one has to change this test.
func TestEachCallerSeesExactlyItsTools(t *testing.T) {
	lane := []string{"lane_report", "lane_wait", "ask", "lane_handoff", "reply", "context_get", "decision_search", "requirement_get", "question_search"}
	orchestrator := []string{
		"context_get", "decision_search", "requirement_get", "question_search",
		"work_candidates", "work_claim", "lane_start", "lane_stop",
		"question_next", "question_attach", "decision_record", "question_escalate",
		"work_decline", "approval_decide",
	}
	if got := listed(t, &worker{}); !slices.Equal(got, lane) {
		t.Errorf("a lane sees %d: %v", len(got), got)
	}
	if got := listed(t, &worker{owner: lanes.OwnerOrchestrator}); !slices.Equal(got, orchestrator) {
		t.Errorf("an orchestrator sees %d: %v", len(got), got)
	}
}

// Every tool an orchestrator lists takes a schema a session can read, and each reaches its worker
// under its own name.
func TestEveryOrchestratorToolReachesItsWorker(t *testing.T) {
	for _, name := range lanes.OrchestratorTools {
		switch name {
		case lanes.ToolContextGet, lanes.ToolDecisionSearch, lanes.ToolRequirementGet, lanes.ToolQuestionSearch, lanes.ToolApprovalDecide:
			continue
		}
		t.Run(name, func(t *testing.T) {
			w := &worker{owner: lanes.OwnerOrchestrator}
			got := ask(t, w, "tools/call", map[string]any{"name": name, "arguments": map[string]any{"work_order": "wo"}})
			if got.Error != nil || got.Result.IsError {
				t.Fatalf("answer %+v", got)
			}
			if calls := w.works(); len(calls) != 1 || calls[0].Tool != name {
				t.Errorf("calls %+v", calls)
			}
		})
	}
}

// A lane's token is refused on `work_claim` here, and again at the worker, because a lane can reach
// the socket without this process.
func TestALaneCallingAnOrchestratorsToolReachesNoWorker(t *testing.T) {
	for _, name := range lanes.OrchestratorTools {
		switch name {
		case lanes.ToolContextGet, lanes.ToolDecisionSearch, lanes.ToolRequirementGet, lanes.ToolQuestionSearch:
			continue
		}
		t.Run(name, func(t *testing.T) {
			w := &worker{}
			got := ask(t, w, "tools/call", map[string]any{"name": name, "arguments": map[string]any{"work_order": "wo"}})
			if got.Error == nil {
				t.Errorf("answer %+v", got)
			}
			if calls := w.works(); len(calls) != 0 {
				t.Errorf("a call reached the worker: %+v", calls)
			}
		})
	}
}

// A lane that calls the tool by its name reaches nothing. The worker refuses it too, in a test of
// its own, because a lane can reach the socket without this process.
func TestALaneCallingDecideByNameReachesNoWorker(t *testing.T) {
	for name, w := range map[string]*worker{
		"a lane":                {},
		"a worker out of reach": {owner: lanes.OwnerOrchestrator, down: true},
	} {
		t.Run(name, func(t *testing.T) {
			got := ask(t, w, "tools/call", decide(map[string]any{"id": "a1", "digest": "sha256:1", "allow": true}))
			if got.Error == nil {
				t.Errorf("answer %+v", got)
			}
			if decisions := w.decisions(); len(decisions) != 0 {
				t.Errorf("a decision reached the worker: %+v", decisions)
			}
		})
	}
}

// What an orchestrator read is what reaches its worker, and nothing else stands in for it.
func TestAnOrchestratorsDecisionNamesTheDigestItRead(t *testing.T) {
	w := &worker{owner: lanes.OwnerOrchestrator}
	got := ask(t, w, "tools/call", decide(map[string]any{"id": "a1", "digest": "sha256:1", "allow": true, "reason": "a declared class"}))
	if got.Error != nil || got.Result.IsError {
		t.Fatalf("answer %+v", got)
	}
	want := lanes.DecideRequest{ID: "a1", Digest: "sha256:1", Allow: true, Reason: "a declared class"}
	if decisions := w.decisions(); len(decisions) != 1 || decisions[0] != want {
		t.Errorf("decisions %+v", decisions)
	}
}

// An argument left out denies, because a decision that says nothing cannot read as a yes.
func TestADecisionThatSaysNothingDenies(t *testing.T) {
	w := &worker{owner: lanes.OwnerOrchestrator}
	ask(t, w, "tools/call", decide(map[string]any{"id": "a1", "digest": "sha256:1"}))
	if decisions := w.decisions(); len(decisions) != 1 || decisions[0].Allow {
		t.Errorf("decisions %+v", decisions)
	}
}

// A decision that did not land says so, because a session must not believe it decided.
func TestARefusedDecisionReadsAsAToolError(t *testing.T) {
	w := &worker{owner: lanes.OwnerOrchestrator, refuse: "this request is outside the delegation policy"}
	got := ask(t, w, "tools/call", decide(map[string]any{"id": "a1", "digest": "sha256:1", "allow": true}))
	if !got.Result.IsError || len(got.Result.Content) != 1 || !strings.Contains(got.Result.Content[0].Text, w.refuse) {
		t.Errorf("answer %+v", got)
	}
}

// A schema lists what a decision binds to, so a session does not guess at its arguments.
func TestDecideDeclaresWhatItBindsTo(t *testing.T) {
	w := &worker{owner: lanes.OwnerOrchestrator}
	for _, tool := range ask(t, w, "tools/list", map[string]any{}).Result.Tools {
		if tool.Name != lanes.ToolApprovalDecide {
			continue
		}
		required, _ := tool.InputSchema["required"].([]any)
		if !slices.Contains(required, any("digest")) || !slices.Contains(required, any("id")) || !slices.Contains(required, any("allow")) {
			t.Errorf("schema %+v", tool.InputSchema)
		}
		return
	}
	t.Fatal("the tool is not listed")
}

// A planning lane's question goes straight to the person, who finds it by category, so the schema
// says a category is needed there before the refusal does.
func TestAskSaysAPlanningLaneNeedsACategory(t *testing.T) {
	for _, tool := range ask(t, &worker{}, "tools/list", map[string]any{}).Result.Tools {
		if tool.Name != lanes.ToolAsk {
			continue
		}
		properties, _ := tool.InputSchema["properties"].(map[string]any)
		categories, _ := properties["categories"].(map[string]any)
		description, _ := categories["description"].(string)
		if !strings.Contains(description, "planning lane") {
			t.Errorf("categories read %q", description)
		}
		return
	}
	t.Fatal("the tool is not listed")
}

// A pack names the documents its contract cites as `document spec:checkout-api@3`, so context_get
// has to offer that kind for a session to read one by the name the pack gives it.
func TestContextGetOffersEveryKindAPackNames(t *testing.T) {
	var kinds []any
	for _, tool := range ask(t, &worker{}, "tools/list", map[string]any{}).Result.Tools {
		if tool.Name != lanes.ToolContextGet {
			continue
		}
		properties, _ := tool.InputSchema["properties"].(map[string]any)
		kind, _ := properties["kind"].(map[string]any)
		kinds, _ = kind["enum"].([]any)
	}
	got := make([]string, len(kinds))
	for i, kind := range kinds {
		got[i], _ = kind.(string)
	}
	for _, want := range []string{"contract", "document", "requirement", "decision", "goal", "knowledge"} {
		if !slices.Contains(got, want) {
			t.Errorf("context_get offers %v, and not %q", got, want)
		}
	}
}

func report(arguments map[string]any) map[string]any {
	return map[string]any{"name": lanes.ToolLaneReport, "arguments": arguments}
}

// A review lane reports its verdicts through the tool every lane has, one per criterion, each with its
// reasons and citations, and the tool's schema says so (ADR-0014).
func TestLaneReportCarriesAVerdictPerCriterion(t *testing.T) {
	w := &worker{}
	var schema map[string]any
	for _, tool := range ask(t, w, "tools/list", map[string]any{}).Result.Tools {
		if tool.Name == lanes.ToolLaneReport {
			schema = tool.InputSchema
		}
	}
	verdicts, ok := schema["properties"].(map[string]any)["verdicts"].(map[string]any)
	if !ok {
		t.Fatalf("lane_report offers no verdicts: %v", schema)
	}
	item := verdicts["items"].(map[string]any)
	properties := item["properties"].(map[string]any)
	for _, field := range []string{"criterion", "verdict", "reasons", "citations"} {
		if _, ok := properties[field]; !ok {
			t.Errorf("a verdict has no %s", field)
		}
	}
	kinds := properties["verdict"].(map[string]any)["enum"].([]any)
	if len(kinds) != 3 || kinds[0] != "pass" || kinds[1] != "fail" || kinds[2] != "cannot_judge" {
		t.Errorf("a verdict is one of %v", kinds)
	}
	required := item["required"].([]any)
	if len(required) != 3 {
		t.Errorf("a verdict requires %v", required)
	}

	got := ask(t, w, "tools/call", report(map[string]any{
		"outcome": "completed",
		"verdicts": []map[string]any{
			{"criterion": "c1", "verdict": "pass", "reasons": "retry.go retries twice", "citations": []string{"retry.go:12", "FR-1"}},
			{"criterion": "c2", "verdict": "fail", "reasons": "nothing is logged"},
		},
	}))
	if got.Result.IsError {
		t.Fatalf("a whole report was refused: %+v", got.Result.Content)
	}
	var sent *workorders.Report
	for _, request := range w.requests {
		if request.Report != nil {
			sent = request.Report
		}
	}
	if sent == nil || len(sent.Verdicts) != 2 || sent.Verdicts[0].Criterion != "c1" || sent.Verdicts[0].Verdict != workorders.VerdictPass ||
		!slices.Equal(sent.Verdicts[0].Citations, []string{"retry.go:12", "FR-1"}) || sent.Verdicts[1].Verdict != workorders.VerdictFail {
		t.Errorf("the worker was sent %+v", sent)
	}
}

// A verdict that is not whole never reaches the worker, and the lane is told what to mend.
func TestLaneReportRefusesAVerdictThatIsNotWhole(t *testing.T) {
	for name, verdict := range map[string]map[string]any{
		"a verdict nobody offers": {"criterion": "c1", "verdict": "maybe", "reasons": "x"},
		"no reasons":              {"criterion": "c1", "verdict": "pass"},
		"no criterion":            {"verdict": "pass", "reasons": "x"},
	} {
		w := &worker{}
		got := ask(t, w, "tools/call", report(map[string]any{"outcome": "completed", "verdicts": []map[string]any{verdict}}))
		if !got.Result.IsError || len(got.Result.Content) == 0 || got.Result.Content[0].Text == "" {
			t.Errorf("%s: %+v", name, got.Result)
		}
		for _, request := range w.requests {
			if request.Report != nil {
				t.Errorf("%s reached the worker", name)
			}
		}
	}
}

// What the worker refuses of a report, such as a criterion left out, comes back to the lane in words.
func TestLaneReportTellsTheLaneWhatTheWorkerRefused(t *testing.T) {
	w := &worker{refuse: "the review gives no verdict for criterion c2"}
	got := ask(t, w, "tools/call", report(map[string]any{
		"outcome": "completed", "verdicts": []map[string]any{{"criterion": "c1", "verdict": "pass", "reasons": "x"}},
	}))
	if !got.Result.IsError || !strings.Contains(got.Result.Content[0].Text, "criterion c2") {
		t.Errorf("the lane hears %+v", got.Result)
	}
}

// Acceptance rests on a review's verdicts and on forge checks, so an orchestrator is not a second principal
// and has no tool to accept work (ADR-0014). The tool is gone for every caller, whatever it asks.
func TestNoCallerHasAnAcceptanceTool(t *testing.T) {
	for _, owner := range []string{"", lanes.OwnerOrchestrator} {
		w := &worker{owner: owner}
		for _, name := range listed(t, w) {
			if strings.Contains(name, "accept") {
				t.Errorf("owner %q is offered %s", owner, name)
			}
		}
		got := ask(t, w, "tools/call", map[string]any{"name": "acceptance_record", "arguments": map[string]any{"work_order": "wo"}})
		if got.Error == nil || !strings.Contains(got.Error.Message, "no such tool") {
			t.Errorf("owner %q calling acceptance_record: %+v", owner, got)
		}
		if calls := w.works(); len(calls) != 0 {
			t.Errorf("owner %q reached the worker with %+v", owner, calls)
		}
	}
}

// One orchestrator serves every active project (ADR-0021): each tool that acts or reads names the
// project, and each list may name one or none, and labels its items with theirs.
func TestAnOrchestratorsToolsNameTheProject(t *testing.T) {
	lists := map[string]bool{lanes.ToolWorkCandidates: true, lanes.ToolQuestionNext: true}
	w := &worker{owner: lanes.OwnerOrchestrator}
	for _, tool := range ask(t, w, "tools/list", map[string]any{}).Result.Tools {
		properties, _ := tool.InputSchema["properties"].(map[string]any)
		if _, ok := properties["project"]; !ok {
			t.Errorf("%s takes no project: %+v", tool.Name, tool.InputSchema)
		}
		required, _ := tool.InputSchema["required"].([]any)
		if names := slices.Contains(required, any("project")); names == lists[tool.Name] {
			t.Errorf("%s requires the project %v, and it is a list %v", tool.Name, names, lists[tool.Name])
		}
	}
	for _, tool := range ask(t, &worker{}, "tools/list", map[string]any{}).Result.Tools {
		if properties, _ := tool.InputSchema["properties"].(map[string]any); properties["project"] != nil {
			t.Errorf("a lane's %s names a project, and a lane reaches only its own", tool.Name)
		}
	}
}
