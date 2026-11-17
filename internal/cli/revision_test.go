package cli_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
)

// run runs a command against the installation, as a person with no terminal to attach.
func (i *installation) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	t.Cleanup(cli.PaneTiming(2*time.Millisecond, 400*time.Millisecond))
	t.Cleanup(cli.FollowTiming(2*time.Millisecond, 100*time.Millisecond))
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, &stdout, &stderr, cli.Options{Socket: i.Socket, Panes: i.held})
	return code, stdout.String(), stderr.String()
}

// approvedWithADecision approves the plan of plannedWithADraft and records a decision of a member about it, which
// leaves the plan needing revision.
func (s *session) approvedWithADecision() (plan string) {
	s.t.Helper()
	plan, _ = s.plannedWithADraft()
	s.must("plan", "approve", plan)
	s.must("decision", "record", "--id", "d-logo", "--statement", "Use the wordmark.", "--rationale", "It reads at small sizes.",
		"--citation", "brand-guide", "--scope", "plan:"+plan, "--reversibility", "reversible", "--category", "product")
	return plan
}

func TestPlanRevisionNamesTheDecisionsMadeSinceTheApprovedVersion(t *testing.T) {
	s := newSession(t)
	plan, _ := s.plannedWithADraft()
	s.must("plan", "approve", plan)
	if none := s.must("plan", "revision", plan); !strings.Contains(none, "no revision is open") {
		t.Errorf("a plan nobody decided anything inside printed %q", none)
	}
	s.approvedWithADecisionOn(plan)
	text := s.must("plan", "revision", plan)
	for _, want := range []string{"revision", "version 1", "d-logo", "reversible", "plan:" + plan, "Use the wordmark.", "waiting", "The work order of budget"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}
	var view api.PlanRevisionView
	if err := json.Unmarshal([]byte(s.must("plan", "revision", plan, "--json")), &view); err != nil || view.Revision == nil || !slices.Equal(view.Revision.DecisionIDs, []string{"d-logo"}) {
		t.Errorf("json %+v: %v", view, err)
	}
	shown := s.must("plan", "show", plan)
	if !strings.Contains(shown, "revision") || !strings.Contains(shown, "d-logo") {
		t.Errorf("plan show says nothing of the revision:\n%s", shown)
	}
	if code, _, _ := s.run("plan", "revision"); code != cli.ExitUsage {
		t.Errorf("exit %d with no plan", code)
	}
	if code, _, _ := s.run("plan", "revision", "absent"); code != cli.ExitNotFound {
		t.Errorf("exit %d for a plan nobody has", code)
	}
}

// approvedWithADecisionOn records the decision of approvedWithADecision about a plan already approved.
func (s *session) approvedWithADecisionOn(plan string) {
	s.t.Helper()
	s.must("decision", "record", "--id", "d-logo", "--statement", "Use the wordmark.", "--rationale", "It reads at small sizes.",
		"--citation", "brand-guide", "--scope", "plan:"+plan, "--reversibility", "reversible", "--category", "product")
}

func TestPlanConfirmClosesTheRevisionAndReleasesTheWorkThatWaited(t *testing.T) {
	s := newSession(t)
	plan := s.approvedWithADecision()
	waiting, err := s.daemon.Client.WorkOrders(t.Context(), workorders.ContractReview)
	if err != nil || len(waiting) != 1 {
		t.Fatalf("work in review %+v: %v", waiting, err)
	}
	text := s.must("plan", "confirm", plan)
	for _, want := range []string{"confirmed", "version 1", "1 decision"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}
	if none := s.must("plan", "revision", plan); !strings.Contains(none, "no revision is open") {
		t.Errorf("a confirmed plan printed %q", none)
	}
	released, err := s.daemon.Client.WorkOrder(t.Context(), waiting[0].ID)
	if err != nil || released.State != workorders.Ready {
		t.Errorf("the work that waited is %s: %v", released.State, err)
	}
	if code, _, stderr := s.run("plan", "confirm", plan); code != cli.ExitNotFound {
		t.Errorf("a second confirm: exit %d: %s", code, stderr)
	}
	if code, _, _ := s.run("plan", "confirm"); code != cli.ExitUsage {
		t.Errorf("exit %d with no plan", code)
	}
}

func TestPlanShowNamesTheDecisionsSinceBesideTheDifferenceOfTheVersionThatWaits(t *testing.T) {
	s := newSession(t)
	plan, source := s.plannedWithADraft()
	s.must("plan", "approve", plan)
	s.approvedWithADecisionOn(plan)
	source.Write("spec.md", []byte(contract.DocumentText("spec", "api", "The API", "a-plan", "The second draft.")))
	s.poll(source)
	shown := s.must("plan", "show", plan)
	for _, want := range []string{"version 2", "changed: spec:api", "decisions since version 1", "d-logo", "Use the wordmark."} {
		if !strings.Contains(shown, want) {
			t.Errorf("no %q in:\n%s", want, shown)
		}
	}
	s.must("plan", "approve", plan, "--version", "2")
	if closed := s.must("plan", "show", plan); !strings.Contains(closed, "decisions since version 1") || !strings.Contains(closed, "d-logo") {
		t.Errorf("the approved version no longer shows the decisions it was approved beside:\n%s", closed)
	}
}

func TestPlanReviseRecordsTheSessionOfTheRevisionAndChainsTheMemberCalls(t *testing.T) {
	i := newInstallation(t)
	i.on("GET /v1/plans/{id}/revision", func(r *http.Request, _ []byte) (any, *api.Error) {
		return api.PlanRevisionView{
			Plan: r.PathValue("id"), Title: "The plan", Revision: &planning.Revision{ID: "r1", PlanID: r.PathValue("id"), ApprovedVersion: 1, DecisionIDs: []string{"d-logo"}},
			Decisions: []api.RevisionDecision{{ID: "d-logo", Statement: "Use the wordmark.", Reversibility: questions.Reversible}},
		}, nil
	})
	i.on("POST /v1/plans/{id}/revision/sessions", func(r *http.Request, body []byte) (any, *api.Error) {
		var in api.ReviseSession
		_ = json.Unmarshal(body, &in)
		return workorders.WorkOrder{ID: in.ID, Kind: workorders.KindPlan, Title: "Revise The plan", State: workorders.ContractReview}, nil
	})
	code, stdout, stderr := i.run(t, "plan", "revise", "a-plan", "--no-attach")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var sent api.ReviseSession
	bodies := i.bodies("POST", "/v1/plans/a-plan/revision/sessions")
	if len(bodies) != 1 || json.Unmarshal([]byte(bodies[0]), &sent) != nil || sent.ID == "" || sent.Repository != "shop" {
		t.Fatalf("session request %v", bodies)
	}
	want := []string{
		"POST /v1/plans/a-plan/revision/sessions", "POST /v1/work-orders/" + sent.ID + "/review",
		"POST /v1/work-orders/" + sent.ID + "/claims", "POST /v1/work-orders/" + sent.ID + "/lanes",
	}
	if wrote := i.wrote(); !slices.Equal(wrote, want) {
		t.Errorf("writes\n got %v\nwant %v", wrote, want)
	}
	if !strings.Contains(stdout, "Work order "+sent.ID) || !strings.Contains(stdout, "ropeshift lane attach") {
		t.Errorf("steps:\n%s", stdout)
	}
}

func TestPlanReviseStartsNothingForAPlanWithNoRevision(t *testing.T) {
	i := newInstallation(t)
	i.on("GET /v1/plans/{id}/revision", func(r *http.Request, _ []byte) (any, *api.Error) {
		return api.PlanRevisionView{Plan: r.PathValue("id"), Title: "The plan"}, nil
	})
	code, _, stderr := i.run(t, "plan", "revise", "a-plan", "--no-attach")
	if code != cli.ExitNotFound || !strings.Contains(stderr, "no revision") {
		t.Errorf("exit %d: %s", code, stderr)
	}
	if wrote := i.wrote(); len(wrote) != 0 {
		t.Errorf("wrote %v", wrote)
	}
}

func TestWorkDecisionReworkNamesTheDecisionsItIsFor(t *testing.T) {
	p := newPlane(t)
	p.on("POST /v1/work-orders/{id}/decision-rework", func(r *http.Request, body []byte) (any, *api.Error) {
		var in api.DecisionRework
		_ = json.Unmarshal(body, &in)
		return api.DecisionReworked{WorkOrder: r.PathValue("id"), Rework: "redo-1"}, nil
	})
	var stdout, stderr strings.Builder
	code := cli.Run([]string{"work", "decision-rework", "done-1", "--decision", "d-1", "--decision", "d-2"}, &stdout, &stderr, cli.Options{Socket: p.Socket})
	if code != cli.ExitOK || !strings.Contains(stdout.String(), "redo-1") {
		t.Fatalf("exit %d: %s\n%s", code, stderr.String(), stdout.String())
	}
	var sent api.DecisionRework
	bodies := p.bodies("POST", "/v1/work-orders/done-1/decision-rework")
	if len(bodies) != 1 || json.Unmarshal([]byte(bodies[0]), &sent) != nil || !slices.Equal(sent.Decisions, []string{"d-1", "d-2"}) {
		t.Errorf("request %v", bodies)
	}
	for _, args := range [][]string{{"work", "decision-rework"}, {"work", "decision-rework", "done-1"}} {
		if code := cli.Run(args, &stdout, &stderr, cli.Options{Socket: p.Socket}); code != cli.ExitUsage {
			t.Errorf("%v: exit %d, want usage", args, code)
		}
	}
}
