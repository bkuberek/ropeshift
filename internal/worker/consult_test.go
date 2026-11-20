package worker_test

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

// consultLane is the prefix of the session name a worker gives a consultation.
const consultLane = "consult-"

// consultAsk is what the orchestrator wants a consultation to explore.
const consultAsk = "Compare a fixed retry budget with one that follows the error rate."

// nextSession is what the orchestrator's next session, which a consultation is, plays.
func (r *toolRig) nextSession(turns ...[]fake.DriverStep) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scripts = []fake.DriverScript{script(turns...)}
}

// consultReport is a consultation's report, as its agent gives it.
var consultReport = map[string]any{
	"options":        []string{"A fixed budget of three retries", "A budget that follows the error rate"},
	"recommendation": "A fixed budget of three retries",
	"rationale":      "It is what the contract's criteria test, and the adaptive one needs a metric the project does not keep.",
}

// reportsConsultation is a consultation's session calling its report tool with its own token.
func (r *toolRig) reportsConsultation(t *testing.T, report map[string]any) fake.DriverStep {
	return fake.DriverStep{Do: func(spec ports.SessionSpec) {
		if reply := callTool(t, r.subject, tokenIn(spec), lanes.ToolConsultReport, report); !reply.OK {
			t.Errorf("the consultation's report: %+v", reply)
		}
	}}
}

// consult calls the tool and reads what it started.
func (r *toolRig) consult(t *testing.T, args map[string]any) ledger.ConsultStarted {
	t.Helper()
	var started ledger.ConsultStarted
	if err := json.Unmarshal(r.done(t, lanes.ToolConsult, args), &started); err != nil {
		t.Fatal(err)
	}
	if started.Consultation == "" {
		t.Fatalf("the tool started no consultation: %+v", started)
	}
	return started
}

// consultations are the ends of the consultations of a question the ledger recorded.
func (r *toolRig) consultations(t *testing.T, question string) []ledger.ConsultationEnd {
	t.Helper()
	var out []ledger.ConsultationEnd
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectConsultation, ID: question})
		for _, event := range events {
			if event.Type != ledger.EventConsulted {
				continue
			}
			var end ledger.ConsultationEnd
			if err := json.Unmarshal(event.Data, &end); err != nil {
				return err
			}
			out = append(out, end)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// awaitConsulted waits for the ledger to record a consultation's end.
func (r *toolRig) awaitConsulted(t *testing.T, question string) ledger.ConsultationEnd {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := r.subject.Upload(r.ctx); err != nil {
			t.Fatal(err)
		}
		if ended := r.consultations(t, question); len(ended) != 0 {
			return ended[len(ended)-1]
		}
		time.Sleep(superviseStep)
	}
	t.Fatalf("no consultation of %s ended", question)
	return ledger.ConsultationEnd{}
}

// consultSpecs are the sessions the worker started for consultations.
func (r *toolRig) consultSpecs() []ports.SessionSpec {
	var out []ports.SessionSpec
	for _, spec := range r.driver.Specs() {
		if strings.HasPrefix(spec.Lane, consultLane) {
			out = append(out, spec)
		}
	}
	return out
}

func (r *toolRig) digest(t *testing.T) []ledger.WakeItem {
	t.Helper()
	found, err := r.plane.WakeDigest(t.Context(), ledger.WakeRequest{Auth: r.subject.Auth(), Epoch: r.subject.Session().Epoch})
	if err != nil {
		t.Fatal(err)
	}
	return found.Items
}

func (r *toolRig) workSpentToday(t *testing.T) float64 {
	t.Helper()
	var spent float64
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		spent, err = tx.Lanes().SpendSince(ledger.SpendScopeWork, ledger.WindowStart(ledger.WindowDay, now))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return spent
}

// FR-ORCH-9: `consult` returns at once with the consultation it started, which runs the strongest enabled read-only
// agent in a headless session of its own, holds the question while it runs, and wakes the orchestrator with the agent's
// options and recommendation once its session ends.
func TestConsultRunsAReadOnlyAgentAndWakesTheOrchestratorWithItsRecommendation(t *testing.T) {
	t.Parallel()
	r := aToolRig(t)
	reads := make(chan lanes.Reply, 1)
	refused := make(chan lanes.Reply, 1)
	held := make(chan lanes.Reply, 1)
	r.nextSession([]fake.DriverStep{{Bound: true}, {Do: func(spec ports.SessionSpec) {
		reads <- askAs(t, r.subject, tokenIn(spec), lanes.Request{Context: &lanes.ContextRequest{Tool: lanes.ToolQuestionSearch, Query: "retry"}})
		refused <- askAs(t, r.subject, tokenIn(spec), lanes.Request{Report: &workorders.Report{Outcome: workorders.OutcomeCompleted}})
		held <- r.tool(t, lanes.ToolDecisionRecord, map[string]any{
			"question": "q1", "status": "assumed", "statement": "Three retries.", "rationale": "Small.", "scope": "repo:retries", "reversibility": "reversible",
		})
	}}, r.reportsConsultation(t, consultReport), completed(0.4)})
	started := r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
	if started.Agent != "analysis" || started.Class != "large" {
		t.Errorf("the consultation runs %+v", started)
	}
	ended := r.awaitConsulted(t, "q1")
	if ended.ID != started.Consultation || ended.Outcome != ledger.ConsultationReported || ended.Recommendation != consultReport["recommendation"] ||
		len(ended.Options) != 2 || ended.Rationale == "" {
		t.Fatalf("the consultation ended %+v", ended)
	}
	if reply := <-reads; !reply.OK {
		t.Errorf("the consultation could not read the records: %+v", reply)
	}
	if reply := <-refused; reply.OK || reply.Code != lanes.CodeForbidden {
		t.Errorf("a consultation's token reported a lane's outcome: %+v", reply)
	}
	if reply := <-held; reply.OK || !strings.Contains(reply.Message, started.Consultation) {
		t.Errorf("the orchestrator decided a question its consultation held: %+v", reply)
	}
	specs := r.consultSpecs()
	if len(specs) != 1 {
		t.Fatalf("%d consultation sessions", len(specs))
	}
	spec := specs[0]
	if spec.RunMode != ports.RunModeHeadless || !spec.ReadOnly || spec.WorkOrder != "" || spec.Effort != "high" {
		t.Errorf("the session is %+v", spec)
	}
	if !slices.Equal(spec.Tools, []string{lanes.ToolContextGet, lanes.ToolDecisionSearch, lanes.ToolRequirementGet, lanes.ToolQuestionSearch, lanes.ToolConsultReport}) {
		t.Errorf("the session holds %v", spec.Tools)
	}
	if !strings.Contains(spec.Brief, "Which retry budget?") || !strings.Contains(spec.Brief, consultAsk) || !strings.Contains(spec.Brief, lanes.ToolConsultReport) {
		t.Errorf("the brief reads %q", spec.Brief)
	}
	var item *ledger.WakeItem
	for _, found := range r.digest(t) {
		if found.Kind == ledger.WakeConsultation {
			item = &found
		}
	}
	if item == nil || item.ID != started.Consultation || !strings.Contains(item.Text, "q1") || !strings.Contains(item.Text, "A fixed budget of three retries") ||
		!strings.Contains(item.Text, ledger.ConsultationCitation(started.Consultation)) {
		t.Fatalf("the wake holds %+v", item)
	}
	if row := r.question(t, "q1"); row.ClaimedBy != "" || !row.Status.Unresolved() {
		t.Errorf("a consultation that ended still holds %+v", row)
	}
}

// FR-ORCH-9: the decision on a question a consultation answered cites the consultation, whether or not the orchestrator
// named it, and the consultation's event holds the agent, its class, the model, and the cost, which the project's work
// tally counts as its lanes' spend is counted.
func TestADecisionCitesItsConsultationWhoseEventHoldsTheAgentAndTheCost(t *testing.T) {
	t.Parallel()
	r := aToolRig(t)
	r.nextSession([]fake.DriverStep{{Bound: true}, r.reportsConsultation(t, consultReport), completed(0.4)})
	before := r.workSpentToday(t)
	started := r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
	ended := r.awaitConsulted(t, "q1")
	spec := r.consultSpecs()[0]
	// The session asks for the model the profile maps the agent's class to, and the event holds the one it ran on.
	if ended.Agent != "analysis" || ended.Class != "large" || spec.Model != "opus" || ended.Model != "resolved-model" || math.Abs(ended.CostUSD-0.4) > 1e-9 {
		t.Errorf("the consultation's event holds %+v, and its session asked for %q", ended, spec.Model)
	}
	if spent := r.workSpentToday(t) - before; math.Abs(spent-0.4) > 1e-9 {
		t.Errorf("the project's work was charged %v for the consultation", spent)
	}
	var recorded control.RecordedDecision
	raw := r.done(t, lanes.ToolDecisionRecord, map[string]any{
		"question": "q1", "status": "assumed", "statement": "Three retries.", "rationale": "The consultation recommends it.",
		"citations": []string{"req-retries"}, "scope": "repo:retries", "reversibility": "reversible",
	})
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatal(err)
	}
	want := ledger.ConsultationCitation(started.Consultation)
	if !slices.Contains(recorded.Citations, want) || !slices.Contains(recorded.Citations, "req-retries") {
		t.Errorf("the decision cites %v, want %s too", recorded.Citations, want)
	}
	if row := r.question(t, "q1"); row.Status != questions.StatusAnswered {
		t.Errorf("the question reads %+v", row)
	}
	for _, item := range r.digest(t) {
		if item.Kind == ledger.WakeConsultation {
			t.Errorf("a decided question's consultation still wakes: %+v", item)
		}
	}
	// A question is consulted on once: the next call says to decide from what the consultation found.
	r.asked(t, "q2", "Which log level?")
	r.nextSession([]fake.DriverStep{{Bound: true}, r.reportsConsultation(t, consultReport), completed(0.1)})
	second := r.consult(t, map[string]any{"question": "q2", "ask": consultAsk})
	r.awaitConsulted(t, "q2")
	if again := r.tool(t, lanes.ToolConsult, map[string]any{"question": "q2", "ask": consultAsk}); again.OK || !strings.Contains(again.Message, second.Consultation) {
		t.Errorf("a second consultation of one question: %+v", again)
	}
}

// FR-ORCH-9, FR-SPEND-6: a consultation spends at most `consultation_limit`, and no more than the project's daily and
// weekly limits leave. A project past a limit, and a limit of 0, start none, and a session that reaches its budget is
// stopped, with what it cost counted and no recommendation.
func TestTheConsultationLimitAndTheProjectLimitsStopAConsultation(t *testing.T) {
	t.Parallel()
	t.Run("a project past its daily limit starts none", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		projectLimitOf(t, r.store, dailyLimit, dailyLimit)
		reply := r.tool(t, lanes.ToolConsult, map[string]any{"question": "q1", "ask": consultAsk})
		if reply.OK || !strings.Contains(reply.Message, "daily") {
			t.Errorf("a consultation past the daily limit: %+v", reply)
		}
		if len(r.consultSpecs()) != 0 || r.question(t, "q1").ClaimedBy != "" {
			t.Errorf("a refused consultation started or held something")
		}
	})
	t.Run("a limit of 0 starts none", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		publishSpend(t, r.store, "consultation_limit", "0")
		if reply := r.tool(t, lanes.ToolConsult, map[string]any{"question": "q1", "ask": consultAsk}); reply.OK || !strings.Contains(reply.Message, "consultation_limit") {
			t.Errorf("a consultation with a limit of 0: %+v", reply)
		}
		if len(r.consultSpecs()) != 0 {
			t.Errorf("a refused consultation started a session")
		}
	})
	t.Run("a session that reaches the project's remaining day is stopped", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		projectLimitOf(t, r.store, dailyLimit, spentByTheOther)
		r.nextSession([]fake.DriverStep{{Bound: true}, {Running: 0.6}, {Hold: true}})
		started := r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
		if math.Abs(started.BudgetUSD-(dailyLimit-spentByTheOther)) > 1e-9 {
			t.Errorf("the budget is %v", started.BudgetUSD)
		}
		ended := r.awaitConsulted(t, "q1")
		if ended.Outcome != ledger.ConsultationAtCap || ended.Recommendation != "" || math.Abs(ended.CostUSD-0.6) > 1e-9 {
			t.Errorf("the consultation ended %+v", ended)
		}
		if spec := r.consultSpecs()[0]; math.Abs(spec.MaxBudgetUSD-started.BudgetUSD) > 1e-9 {
			t.Errorf("the client's own cap is %v", spec.MaxBudgetUSD)
		}
	})
	t.Run("a session that reaches the consultation limit is stopped", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		r.nextSession([]fake.DriverStep{{Bound: true}, {Running: 2.5}, {Hold: true}})
		started := r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
		if math.Abs(started.BudgetUSD-2) > 1e-9 {
			t.Errorf("the budget is %v, and the default limit is 2", started.BudgetUSD)
		}
		if ended := r.awaitConsulted(t, "q1"); ended.Outcome != ledger.ConsultationAtCap || math.Abs(ended.CostUSD-2.5) > 1e-9 {
			t.Errorf("the consultation ended %+v", ended)
		}
	})
}

// FR-ORCH-9: a consultation reads the question's repository through a read-only worktree of its own, detached at the
// head of the repository's default branch, and never the clone the worker keeps: its session neither runs in the clone,
// nor shares its settings, nor reads it.
func TestAConsultationNeverUsesAMembersClone(t *testing.T) {
	t.Parallel()
	r := aToolRig(t)
	const clone = "/repository"
	_, err := control.NewWorkOrders(r.store).Record(t.Context(), scope, control.LedgerWork{
		Order:    workorders.WorkOrder{ID: "wo-retries", Kind: workorders.KindChange, Title: "Retries", RepositoryID: "retries"},
		Criteria: []planning.Criterion{{ID: "c1", Text: "It retries"}},
	}, member)
	if err != nil {
		t.Fatal(err)
	}
	_, err = control.NewQuestions(r.store, questions.BatchPolicy{}).Ask(t.Context(), scope,
		questions.Ask{ID: "q-retries", Text: "Which retry budget for this work?", SubjectKind: workorders.SubjectKind, SubjectID: "wo-retries"}, member)
	if err != nil {
		t.Fatal(err)
	}
	r.nextSession([]fake.DriverStep{{Bound: true}, r.reportsConsultation(t, consultReport), completed(0.1)})
	r.consult(t, map[string]any{"question": "q-retries", "ask": consultAsk})
	r.awaitConsulted(t, "q-retries")
	spec := r.consultSpecs()[0]
	inside := func(path string) bool {
		return path == clone || strings.HasPrefix(path, clone+string(filepath.Separator))
	}
	if inside(spec.Dir) || spec.Repository != "" || spec.ConfigDir == "" || inside(spec.ConfigDir) {
		t.Errorf("the session runs in %q from %q, with settings in %q", spec.Dir, spec.Repository, spec.ConfigDir)
	}
	if len(spec.ReadOnlyDirs) != 1 || inside(spec.ReadOnlyDirs[0]) {
		t.Fatalf("the session reads %v", spec.ReadOnlyDirs)
	}
	tree := spec.ReadOnlyDirs[0]
	var made []ports.WorktreeSpec
	for _, created := range r.config.Provisioner.Worktrees.(*fake.Worktrees).Created() {
		if created.Path == tree {
			made = append(made, created)
		}
	}
	if len(made) != 1 || !made[0].Detached || made[0].Base != "main" || made[0].Repository != clone || made[0].Branch != "" {
		t.Errorf("the tree it reads was made as %+v", made)
	}
	if !strings.Contains(spec.Brief, tree) {
		t.Errorf("the brief does not name the tree it reads: %q", spec.Brief)
	}
}

// askAs sends one line of the socket on a token.
func askAs(t *testing.T, subject *worker.Worker, token string, request lanes.Request) lanes.Reply {
	t.Helper()
	request.Version, request.Token = lanes.SocketProtocol, token
	line, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return subject.AnswerLane(t.Context(), line)
}

// publishSpend publishes the next version of a project's spend policy with one key changed.
func publishSpend(t *testing.T, store ports.Store, key, value string) {
	t.Helper()
	policy, err := authority.DefaultSpendPolicy().Set(key, value)
	if err != nil {
		t.Fatal(err)
	}
	policy.Version = 2
	if _, err := control.NewSpend(store).PublishSpendPolicy(t.Context(), scope, authority.SpendPolicyDraft{Body: policy.Body(), Policy: policy}, member); err != nil {
		t.Fatal(fmt.Errorf("publishing %s=%s: %w", key, value, err))
	}
}
