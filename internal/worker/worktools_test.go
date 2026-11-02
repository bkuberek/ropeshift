package worker_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// toolRig is an orchestrator at rest, and the token its session holds.
type toolRig struct {
	*orchestratorRig
	token string
}

func aToolRig(t *testing.T) *toolRig {
	t.Helper()
	o := newOrchestratorRig(t, aSession())
	o.wokenOnce(t)
	r := &toolRig{orchestratorRig: o}
	for _, entry := range o.specs()[0].Env {
		if value, ok := strings.CutPrefix(entry, lanes.EnvLaneToken+"="); ok {
			r.token = value
		}
	}
	return r
}

func callTool(t *testing.T, subject *worker.Worker, token, tool string, args map[string]any) lanes.Reply {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: token, Work: &lanes.WorkRequest{Tool: tool, Args: raw}})
	if err != nil {
		t.Fatal(err)
	}
	return subject.AnswerLane(t.Context(), line)
}

func (r *toolRig) tool(t *testing.T, tool string, args map[string]any) lanes.Reply {
	t.Helper()
	return callTool(t, r.subject, r.token, tool, args)
}

func (r *toolRig) done(t *testing.T, tool string, args map[string]any) json.RawMessage {
	t.Helper()
	reply := r.tool(t, tool, args)
	if !reply.OK {
		t.Fatalf("%s: %+v", tool, reply)
	}
	return reply.Work
}

func (r *toolRig) question(t *testing.T, id string) questions.Question {
	t.Helper()
	var out questions.Question
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Questions().Question(id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A lane's token is refused here too, because a lane can reach the socket without `ropeshift mcp`,
// and this is where it would otherwise reach the ledger.
func TestALaneTokenIsRefusedOnWorkClaim(t *testing.T) {
	r := aToolRig(t)
	for _, tool := range []string{lanes.ToolWorkClaim, lanes.ToolLaneStart, lanes.ToolDecisionRecord, lanes.ToolQuestionEscalate, lanes.ToolAcceptanceRecord} {
		reply := callTool(t, r.subject, r.laneRig.token(t), tool, map[string]any{"work_order": "wo", "question": "q1"})
		if reply.OK || reply.Code != lanes.CodeForbidden {
			t.Errorf("%s: %+v", tool, reply)
		}
	}
}

func TestAnUnknownToolIsRefused(t *testing.T) {
	r := aToolRig(t)
	if reply := r.tool(t, "lane_report", nil); reply.OK {
		t.Errorf("reply %+v", reply)
	}
}

// A window and an acknowledgement are what keep a person near this session, so no token reaches
// either, whatever the call is named. What a tool does not exist for, the worker refuses on its own,
// because a session can reach this socket without `ropeshift mcp`.
func TestNoSessionOpensAWindowOrAcknowledgesTheRisk(t *testing.T) {
	r := aToolRig(t)
	worker := r.subject.Auth().WorkerID
	names := []string{
		"window_open", "run_window_open", "window_close", "open_window", "autonomy", "autonomy_set", "worker_level",
		"risk_acknowledge", "acknowledge_risk", "policy_publish",
	}
	for _, token := range []string{r.token, r.laneRig.token(t)} {
		for _, name := range names {
			args := map[string]any{"worker": worker, "digest": authority.RiskDigest(), "level": "unattended"}
			if reply := callTool(t, r.subject, token, name, args); reply.OK {
				t.Errorf("%s: %+v", name, reply)
			}
		}
	}
	view, err := control.NewAutonomy(r.store).View(t.Context(), scope, worker)
	if err != nil {
		t.Fatal(err)
	}
	if view.Window != nil || view.Acknowledged || view.Chosen != "" {
		t.Fatalf("something moved: %+v", view)
	}
	for _, tool := range lanes.OrchestratorTools {
		if strings.Contains(tool, "window") || strings.Contains(tool, "risk") || strings.Contains(tool, "autonomy") || strings.Contains(tool, "level") {
			t.Errorf("an orchestrator holds %s", tool)
		}
	}
}

// A window closed after a claim leaves the claim, and a session that goes on to start a lane hears
// why it cannot: a session starts on a person's say, and that person has gone.
func TestAStartWaitsForAWindowAsAClaimDoes(t *testing.T) {
	r := aToolRig(t)
	ops := control.NewAutonomy(r.store)
	worker := r.subject.Auth().WorkerID
	if err := ops.SetLevel(t.Context(), scope, worker, authority.LevelAttended, member); err != nil {
		t.Fatal(err)
	}
	if reply := r.tool(t, lanes.ToolWorkCandidates, nil); !reply.OK || strings.TrimSpace(string(reply.Work)) != "[]" {
		t.Fatalf("candidates with no window: %+v, %s", reply, reply.Work)
	}
	r.addOrder(t, "wo-window", 1, nil)
	if reply := r.tool(t, lanes.ToolWorkClaim, map[string]any{"work_order": "wo-window"}); reply.OK || !strings.Contains(reply.Message, "no run window") {
		t.Fatalf("a claim with no window: %+v", reply)
	}
	if _, err := ops.OpenWindow(t.Context(), scope, worker, "w1", 60_000, member); err != nil {
		t.Fatal(err)
	}
	r.done(t, lanes.ToolWorkClaim, map[string]any{"work_order": "wo-window"})
	if err := ops.CloseWindow(t.Context(), scope, worker, member); err != nil {
		t.Fatal(err)
	}
	if reply := r.tool(t, lanes.ToolLaneStart, map[string]any{"work_order": "wo-window"}); reply.OK || !strings.Contains(reply.Message, "no run window") {
		t.Fatalf("a start after the window closed: %+v", reply)
	}
	if _, err := ops.OpenWindow(t.Context(), scope, worker, "w2", 60_000, member); err != nil {
		t.Fatal(err)
	}
	r.done(t, lanes.ToolLaneStart, map[string]any{"work_order": "wo-window"})
}

// Rung 1: a decision on record answers it, and the core holds the answer to what the policy lets an
// orchestrator rely on.
func TestRungOneAnswersFromTheRecord(t *testing.T) {
	r := aToolRig(t)
	decision, err := control.NewQuestions(r.store, questions.BatchPolicy{}).RecordDecision(t.Context(), scope, questions.Decision{
		ID: "d1", Status: questions.DecisionDecided, Statement: "Twice.", Rationale: "It follows.", Scope: "retries", Reversibility: "reversible", DecidedBy: "member-1",
	}, member)
	if err != nil {
		t.Fatal(err)
	}
	r.done(t, lanes.ToolDecisionRecord, map[string]any{"question": "q1", "decision": decision.ID})
	if got := r.question(t, "q1"); got.Status != questions.StatusAnswered || got.DecisionID != decision.ID {
		t.Errorf("question %+v", got)
	}
}

// Rung 2: the same question twice leaves one, and the record says which.
func TestRungTwoAttachesToAnOpenQuestion(t *testing.T) {
	r := aToolRig(t)
	r.asked(t, "q2", "How many retries?")
	r.done(t, lanes.ToolQuestionAttach, map[string]any{"question": "q2", "into": "q1", "lookup": []string{"d0"}})
	got := r.question(t, "q2")
	if into, merged := got.MergedInto(); !merged || into != "q1" {
		t.Errorf("question %+v", got)
	}
	if !got.Ran(questions.RungLookup) {
		t.Errorf("rung 1 left no record: %+v", got.LadderTrace)
	}
}

func TestRungThreeDecides(t *testing.T) {
	r := aToolRig(t)
	raw := r.done(t, lanes.ToolDecisionRecord, map[string]any{
		"question": "q1", "status": "decided", "statement": "Twice.", "rationale": "It follows.", "scope": "retries", "reversibility": "reversible",
		"citations": []string{"PAY-12"}, "lookup": []string{"d0"}, "open": []string{"q0"},
	})
	var decision questions.Decision
	if err := json.Unmarshal(raw, &decision); err != nil {
		t.Fatal(err)
	}
	got := r.question(t, "q1")
	if got.Status != questions.StatusAnswered || got.DecisionID != decision.ID || !got.Ran(questions.RungOpen) {
		t.Errorf("question %+v", got)
	}
	if decision.DecidedBy != r.subject.Auth().WorkerID {
		t.Errorf("decided by %q", decision.DecidedBy)
	}
}

func TestRungFourAssumes(t *testing.T) {
	r := aToolRig(t)
	r.done(t, lanes.ToolDecisionRecord, map[string]any{
		"question": "q1", "status": "assumed", "statement": "Twice for now.", "rationale": "It can change.", "scope": "retries", "reversibility": "reversible",
	})
	got := r.question(t, "q1")
	if got.Status != questions.StatusAnswered || !got.Ran(questions.RungDecide) {
		t.Errorf("question %+v", got)
	}
}

// A one-way choice skips the rungs an orchestrator may decide on, whatever it asks for.
func TestAOneWayCategoryCannotBeDecided(t *testing.T) {
	r := aToolRig(t)
	if err := r.oneWay(t, "q2", "Which key?"); err != nil {
		t.Fatal(err)
	}
	reply := r.tool(t, lanes.ToolDecisionRecord, map[string]any{
		"question": "q2", "status": "decided", "statement": "This one.", "rationale": "Because.", "scope": "keys", "reversibility": "reversible",
	})
	if reply.OK || reply.Code != lanes.CodeForbidden {
		t.Errorf("reply %+v", reply)
	}
	if got := r.question(t, "q2"); got.Status == questions.StatusAnswered {
		t.Errorf("question %+v", got)
	}
}

func (r *toolRig) oneWay(t *testing.T, id, text string) error {
	t.Helper()
	_, err := control.NewQuestions(r.store, questions.BatchPolicy{}).Ask(t.Context(), scope, questions.Ask{ID: id, Text: text, Categories: []string{questions.CategorySecurity}}, member)
	return err
}

// Rung 5 goes through the gate: a refusal names what is missing and changes nothing, and a question
// that passes reaches a person.
func TestRungFiveGoesThroughTheGate(t *testing.T) {
	r := aToolRig(t)
	if err := r.oneWay(t, "q2", "Which key?"); err != nil {
		t.Fatal(err)
	}
	refused := r.tool(t, lanes.ToolQuestionEscalate, map[string]any{"question": "q2", "reason": "one_way"})
	if refused.OK || !strings.Contains(refused.Message, "option") {
		t.Fatalf("reply %+v", refused)
	}
	if got := r.question(t, "q2"); got.Status == questions.StatusNeedsHuman {
		t.Fatalf("a refusal moved it: %+v", got)
	}
	r.done(t, lanes.ToolQuestionEscalate, map[string]any{
		"question": "q2", "reason": "one_way", "options": []string{"rotate", "keep"}, "recommendation": "rotate", "lookup": []string{"d0"}, "open": []string{"q0"},
	})
	if got := r.question(t, "q2"); got.Status != questions.StatusNeedsHuman {
		t.Errorf("question %+v", got)
	}
}

// A call that failed part way can be made again, because a rung passed once is not passed again.
func TestAToolCanBeCalledAgainAfterARefusal(t *testing.T) {
	r := aToolRig(t)
	bad := r.tool(t, lanes.ToolDecisionRecord, map[string]any{"question": "q1", "status": "decided", "statement": ""})
	if bad.OK {
		t.Fatalf("reply %+v", bad)
	}
	r.done(t, lanes.ToolDecisionRecord, map[string]any{
		"question": "q1", "status": "decided", "statement": "Twice.", "rationale": "It follows.", "scope": "retries", "reversibility": "reversible",
		"citations": []string{"PAY-12"},
	})
}

// A tool reads its arguments by their exact names, because a decoder that folds case would take a
// field the schema never declared for one it did.
func TestAToolReadsArgumentsByTheirExactNames(t *testing.T) {
	r := aToolRig(t)
	reply := r.tool(t, lanes.ToolDecisionRecord, map[string]any{
		"QUESTION": "q1", "Status": "decided", "statement": "Twice.", "rationale": "It follows.", "scope": "retries", "reversibility": "reversible",
		"citations": []string{"PAY-12"},
	})
	if reply.OK {
		t.Errorf("reply %+v", reply)
	}
	if got := r.question(t, "q1"); got.Status == questions.StatusAnswered {
		t.Errorf("a key of another case decided: %+v", got)
	}
}

// Two orchestrators never work one question: one that another holds is not next, and it cannot be
// decided past that hold.
func TestAQuestionAnotherHoldsIsNotNext(t *testing.T) {
	r := aToolRig(t)
	other := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "another-worker"}
	if _, err := control.NewQuestions(r.store, questions.BatchPolicy{}).Take(t.Context(), scope, "q1", 60_000, other); err != nil {
		t.Fatal(err)
	}
	if got := r.done(t, lanes.ToolQuestionNext, nil); string(got) != "null" {
		t.Errorf("next %s", got)
	}
	reply := r.tool(t, lanes.ToolDecisionRecord, map[string]any{
		"question": "q1", "status": "decided", "statement": "Twice.", "rationale": "It follows.", "scope": "retries", "reversibility": "reversible",
	})
	if reply.OK {
		t.Errorf("reply %+v", reply)
	}
}

func TestABlockingQuestionComesNext(t *testing.T) {
	r := aToolRig(t)
	_, err := control.NewQuestions(r.store, questions.BatchPolicy{}).Ask(t.Context(), scope, questions.Ask{ID: "z-blocks", Text: "Which queue?", Blocking: true}, member)
	if err != nil {
		t.Fatal(err)
	}
	var read control.LadderRead
	if err := json.Unmarshal(r.done(t, lanes.ToolQuestionNext, nil), &read); err != nil {
		t.Fatal(err)
	}
	if read.Question.ID != "z-blocks" || read.Question.ClaimedBy != r.subject.Auth().WorkerID {
		t.Errorf("next %+v", read.Question)
	}
}

// recordOffers finds the candidate of a kind in what the ladder offered.
func recordOffers(read control.LadderRead, kind string) (control.LadderCandidate, bool) {
	for _, candidate := range read.Lookup {
		if candidate.Kind == kind {
			return candidate, true
		}
	}
	return control.LadderCandidate{}, false
}

// A session answers from what it can read. The ladder's first rung is the record, so it offers what
// each record says and the name a citation and the read tools take, because a row's ID leads nowhere
// and a session told only that a requirement exists has nothing to decide on. Two real panes took a
// question that a requirement answered, and neither recorded that answer.
func TestTheNextQuestionStatesWhatTheRecordSays(t *testing.T) {
	r := aToolRig(t)
	source := fake.NewWorkSource()
	source.Write("requirements.md", []byte("+++\nkind = \"requirements\"\nid = \"logs\"\n\n[[requirement]]\nid = \"LOG-1\"\ntext = \"Retries log at warn.\"\n+++\n"))
	source.Write("contract.md", []byte("+++\nkind = \"contract\"\nid = \"logging\"\ntitle = \"Log the retries\"\n\n[[criteria]]\nid = \"c1\"\ntext = \"A retry logs\"\n\n[[work_orders]]\nid = \"logging-work\"\ntitle = \"Log\"\nsize = \"small\"\n+++\n\nLog each retry.\n"))
	if _, err := control.NewIntake(r.store).Poll(t.Context(), scope, source); err != nil {
		t.Fatal(err)
	}
	ops := control.NewQuestions(r.store, questions.BatchPolicy{})
	earlier, err := ops.RecordDecision(t.Context(), scope, questions.Decision{
		ID: "an-earlier-call", Status: questions.DecisionDecided, Statement: "Retries use one log level for every path.", Rationale: "One rule.", Scope: "logging",
		Reversibility: "reversible", DecidedBy: "member-1",
	}, member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ops.Ask(t.Context(), scope, questions.Ask{ID: "a-sibling", Text: "Which log level do failed retries use?"}, member); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.Ask(t.Context(), scope, questions.Ask{ID: "z-blocks", Text: "Which log level do retries use?", Blocking: true}, member); err != nil {
		t.Fatal(err)
	}
	var read control.LadderRead
	if err := json.Unmarshal(r.done(t, lanes.ToolQuestionNext, nil), &read); err != nil {
		t.Fatal(err)
	}
	requirement, found := recordOffers(read, questions.KindRequirement)
	if !found || requirement.Ref != "LOG-1" || requirement.Text != "Retries log at warn." {
		t.Errorf("a requirement reads %+v in %+v", requirement, read.Lookup)
	}
	decision, found := recordOffers(read, questions.KindDecision)
	if !found || decision.Ref != earlier.ID || !strings.Contains(decision.Text, "one log level") {
		t.Errorf("a decision reads %+v in %+v", decision, read.Lookup)
	}
	contract, found := recordOffers(read, questions.KindContract)
	if !found || contract.Ref == "" || !strings.Contains(contract.Text, "Log each retry.") {
		t.Errorf("a contract reads %+v in %+v", contract, read.Lookup)
	}
	if !slices.ContainsFunc(read.Open, func(candidate control.LadderCandidate) bool {
		return candidate.Ref == "a-sibling" && strings.Contains(candidate.Text, "failed retries")
	}) {
		t.Errorf("an open question reads %+v", read.Open)
	}
}

// addOrder puts work in front of the worker.
func (r *toolRig) addOrder(t *testing.T, id string, priority int64, needs map[string]string) {
	t.Helper()
	_, err := control.NewWorkOrders(r.store).Record(t.Context(), scope, control.LedgerWork{
		Order:    workorders.WorkOrder{ID: id, Kind: workorders.KindChange, Title: "T", Priority: priority, Needs: needs},
		Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
	}, member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewReviews(r.store).Pass(t.Context(), scope, id, member); err != nil {
		t.Fatal(err)
	}
}

func (r *toolRig) orderOf(t *testing.T, id string) workorders.WorkOrder {
	t.Helper()
	var out workorders.WorkOrder
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.WorkOrders().Get(id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Candidates hold only what this worker can run, woken lanes first, then priority, then age.
func TestCandidatesComeInOrder(t *testing.T) {
	r := aToolRig(t)
	r.addOrder(t, "01-old-low", 1, nil)
	r.addOrder(t, "02-new-high", 5, nil)
	r.addOrder(t, "03-newer-high", 5, nil)
	r.addOrder(t, "04-not-for-this-worker", 9, map[string]string{"driver": "no-such-driver"})
	var rows []workorders.WorkOrder
	if err := json.Unmarshal(r.done(t, lanes.ToolWorkCandidates, nil), &rows); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, row := range rows {
		got = append(got, row.ID)
	}
	if want := []string{"02-new-high", "03-newer-high", "01-old-low"}; !slices.Equal(got, want) {
		t.Errorf("candidates %v, want %v", got, want)
	}
	reply := r.tool(t, lanes.ToolWorkClaim, map[string]any{"work_order": "04-not-for-this-worker"})
	if reply.OK {
		t.Errorf("a claim of what this worker cannot run: %+v", reply)
	}
}

// A workspace that has closed many questions still has its next one: the wake that names it and the
// tool that takes it read the same list.
func TestAQuestionPastTheFirstPageIsNext(t *testing.T) {
	r := aToolRig(t)
	ops := control.NewQuestions(r.store, questions.BatchPolicy{})
	if err := ops.Dismiss(t.Context(), scope, "q1", member); err != nil {
		t.Fatal(err)
	}
	for i := range 201 {
		id := fmt.Sprintf("a-closed-%03d", i)
		if _, err := ops.Ask(t.Context(), scope, questions.Ask{ID: id, Text: "A closed question " + id}, member); err != nil {
			t.Fatal(err)
		}
		if err := ops.Dismiss(t.Context(), scope, id, member); err != nil {
			t.Fatal(err)
		}
	}
	r.asked(t, "z-open", "Which queue?")
	if got := kindsOf(r.digest(t)); !slices.Equal(got, []string{"question:z-open"}) {
		t.Fatalf("digest %v", got)
	}
	var read control.LadderRead
	if err := json.Unmarshal(r.done(t, lanes.ToolQuestionNext, nil), &read); err != nil {
		t.Fatal(err)
	}
	if read.Question.ID != "z-open" {
		t.Errorf("next %+v", read.Question)
	}
}

// A wake costs a whole context, and a fleet that takes no claims has no work to offer, so what is
// claimable wakes nobody until it can be claimed.
func TestAFleetThatTakesNoClaimsOffersNoWork(t *testing.T) {
	for _, mode := range []ledger.FleetMode{ledger.FleetPause, ledger.FleetDrain} {
		t.Run(string(mode), func(t *testing.T) {
			o := newOrchestratorRig(t, aSession())
			r := &toolRig{orchestratorRig: o}
			r.addOrder(t, "wo-2", 1, nil)
			if got := kindsOf(o.digest(t)); !slices.Equal(got, []string{"work:wo-2"}) {
				t.Fatalf("digest %v", got)
			}
			if err := control.NewFleet(o.store).SetMode(t.Context(), scope, mode, member); err != nil {
				t.Fatal(err)
			}
			if got := o.digest(t); len(got) != 0 {
				t.Errorf("digest %+v", got)
			}
		})
	}
}

// This is why a token came before the routes: a request reaches an orchestrator in its wake, with the
// digest a decision binds to, and the decision it makes with that digest is the one that stands.
func TestAnOrchestratorDecidesWhatItsWakeNamed(t *testing.T) {
	r := aToolRig(t)
	policy := authority.DelegationPolicy{Version: 2, Classes: []authority.DelegationClass{{Tool: "Bash", CommandPrefix: "git tag"}}}
	body := "[delegation_policy]\nversion = 2\n\n[[delegation_policy.classes]]\ntool = \"Bash\"\ncommand_prefix = \"git tag\"\n"
	if _, err := control.NewApprovals(r.store).PublishDelegation(t.Context(), scope, authority.DelegationDraft{Body: body, Policy: policy}, member); err != nil {
		t.Fatal(err)
	}
	input := `{"command":"git tag v1"}`
	lane := ledger.Actor{Kind: ledger.ActorLane, ID: "lane-1"}
	err := control.NewApprovals(r.store).Raise(t.Context(), scope, authority.Approval{
		ID: "a1", Lane: "lane-1", WorkOrder: "wo", Generation: 1, Kind: authority.KindToolCall, Subject: "Bash",
		SubjectDigest: authority.LandingDigest("Bash", input, ""), Input: input, Summary: "git tag v1", Command: "git tag v1", RequestedBy: authority.Principal(lane),
	}, lane)
	if err != nil {
		t.Fatal(err)
	}
	var read ledger.WakeItem
	for _, item := range r.digest(t) {
		if item.Kind == ledger.WakeApproval {
			read = item
		}
	}
	if read.ID != "a1" || read.Digest == "" {
		t.Fatalf("the digest holds %+v", read)
	}
	line, err := json.Marshal(lanes.Request{
		Version: lanes.SocketProtocol, Token: r.token, Decide: &lanes.DecideRequest{ID: read.ID, Digest: read.Digest, Allow: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply := r.subject.AnswerLane(t.Context(), line); !reply.OK {
		t.Fatalf("reply %+v", reply)
	}
	rows, err := control.NewApprovals(r.store).List(t.Context(), scope, authority.ApprovalApproved)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "a1" || rows[0].DecidedDigest != read.Digest {
		t.Errorf("requests %+v", rows)
	}
}

// Pulling work is a claim and then a lane, and each is its own act.
func TestAnOrchestratorPullsWork(t *testing.T) {
	r := aToolRig(t)
	r.addOrder(t, "wo-2", 1, nil)
	r.done(t, lanes.ToolWorkClaim, map[string]any{"work_order": "wo-2"})
	if again := r.tool(t, lanes.ToolWorkClaim, map[string]any{"work_order": "wo-2"}); again.OK {
		t.Errorf("a second claim of one work order: %+v", again)
	}
	r.done(t, lanes.ToolLaneStart, map[string]any{"work_order": "wo-2"})
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		r.tick(t)
		for _, spec := range r.driver.Specs() {
			if spec.WorkOrder == "wo-2" {
				// A socket's path has a limit, and a lane's name is part of it.
				if len(spec.Lane) > 24 {
					t.Errorf("lane %q is %d bytes", spec.Lane, len(spec.Lane))
				}
				return
			}
		}
	}
	t.Errorf("no lane started: %+v", r.orderOf(t, "wo-2"))
}

// Any orchestrator can work any question, and each runs only what its own machine holds.
func TestAnOrchestratorRunsOnlyWhatItsWorkerHolds(t *testing.T) {
	r := aToolRig(t)
	token, err := r.plane.CreateJoinToken(t.Context(), member)
	if err != nil {
		t.Fatal(err)
	}
	other, err := r.plane.Register(t.Context(), ledger.Registration{WorkerID: "another", Name: "another", JoinToken: token})
	if err != nil {
		t.Fatal(err)
	}
	r.claimFor("wo-2", other.WorkerID)
	if reply := r.tool(t, lanes.ToolLaneStart, map[string]any{"work_order": "wo-2"}); reply.OK {
		t.Errorf("a lane on another worker's claim: %+v", reply)
	}
	if _, err := control.NewLeases(r.store).Dispatch(t.Context(), scope, "wo-2", "lane-2", member); err != nil {
		t.Fatal(err)
	}
	if reply := r.tool(t, lanes.ToolLaneStop, map[string]any{"work_order": "wo-2"}); reply.OK {
		t.Errorf("a stop of another worker's lane: %+v", reply)
	}
}

// `lane_stop` ends a stuck session and keeps the work. Canceling it is a member's.
func TestLaneStopReplacesASession(t *testing.T) {
	r := aToolRig(t)
	r.done(t, lanes.ToolLaneStop, map[string]any{"work_order": "wo"})
	if got := r.orderOf(t, "wo"); got.State != workorders.Running {
		t.Errorf("work order %+v", got)
	}
	found := false
	for _, event := range r.events() {
		found = found || event.Type == ledger.EventLaneRespawn
	}
	if !found {
		t.Error("no respawn on record")
	}
}

func TestDecliningNeedsAReason(t *testing.T) {
	r := aToolRig(t)
	r.addOrder(t, "wo-2", 1, nil)
	if reply := r.tool(t, lanes.ToolWorkDecline, map[string]any{"work_order": "wo-2"}); reply.OK {
		t.Errorf("reply %+v", reply)
	}
	r.done(t, lanes.ToolWorkDecline, map[string]any{"work_order": "wo-2", "reason": "It needs a person first."})
	if row := r.orderOf(t, "wo-2"); row.State != workorders.Ready {
		t.Errorf("a decline moved the work: %+v", row)
	}
	declined := 0
	for _, event := range r.events() {
		if event.Type == ledger.EventOrchestratorDeclined {
			declined++
		}
	}
	if declined != 1 {
		t.Errorf("%d declines on record", declined)
	}
}

// Near a limit, a worker starts nothing new, and only this machine knows that.
func TestNoClaimAtALimit(t *testing.T) {
	o := newOrchestratorRig(t)
	o.scripts = []fake.DriverScript{script(
		[]fake.DriverStep{{Bound: true}, *rejected(o.clock.Now().Add(time.Hour).UnixMilli()), completed(0.01)},
	)}
	o.wokenOnce(t)
	r := &toolRig{orchestratorRig: o}
	for _, entry := range o.specs()[0].Env {
		if value, ok := strings.CutPrefix(entry, lanes.EnvLaneToken+"="); ok {
			r.token = value
		}
	}
	r.addOrder(t, "wo-2", 1, nil)
	reply := r.tool(t, lanes.ToolWorkClaim, map[string]any{"work_order": "wo-2"})
	if reply.OK || !strings.Contains(reply.Message, "limit") {
		t.Errorf("reply %+v", reply)
	}
}

// A worker's credential is not enough: with no run a member asked for, nothing acts.
func TestAnActNeedsTheRunAMemberAskedFor(t *testing.T) {
	r := aToolRig(t)
	row := r.row(t)
	stale := ledger.OrchestratorCall{Auth: r.subject.Auth(), Epoch: r.subject.Session().Epoch, Lane: row.Lane() + "0"}
	if _, err := r.plane.Orchestrate(t.Context(), ledger.VerbCandidates, stale); !errors.Is(err, workorders.ErrForbidden) {
		t.Errorf("a run nobody asked for: %v", err)
	}
	if _, err := r.plane.SetOrchestrator(t.Context(), r.subject.Auth().WorkerID, ledger.OrchestratorStopped, member); err != nil {
		t.Fatal(err)
	}
	current := ledger.OrchestratorCall{Auth: r.subject.Auth(), Epoch: r.subject.Session().Epoch, Lane: row.Lane()}
	if _, err := r.plane.Orchestrate(t.Context(), ledger.VerbCandidates, current); !errors.Is(err, workorders.ErrForbidden) {
		t.Errorf("a stopped orchestrator: %v", err)
	}
}

func TestAnUnknownVerbActsOnNothing(t *testing.T) {
	r := aToolRig(t)
	call := ledger.OrchestratorCall{Auth: r.subject.Auth(), Epoch: r.subject.Session().Epoch, Lane: r.row(t).Lane()}
	if _, err := r.plane.Orchestrate(t.Context(), "dismiss", call); !errors.Is(err, workorders.ErrInvalid) {
		t.Errorf("err %v", err)
	}
}
