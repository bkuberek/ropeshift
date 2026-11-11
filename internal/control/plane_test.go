package control_test

import (
	"encoding/json"
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/lexical"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// apiKeyManifest is a worker on an API key, whose autonomy lets it claim with nobody there.
var apiKeyManifest = ledger.Manifest{Drivers: []ledger.DriverManifest{{
	Driver: "claude-code", Present: true, Qualified: true, SignIn: ledger.SignIn{Kind: ledger.SignInAPIKey, Status: ledger.SignedIn},
}}}

// planeRig is an installation of two projects, `alpha` the default one, with one registered worker.
type planeRig struct {
	t      *testing.T
	state  *sqlite.State
	plane  *control.Plane
	stores map[ledger.WorkspaceID]ports.Store
	auth   ledger.WorkerAuth
	epoch  int64
}

func newPlaneRig(t *testing.T, protocol int) *planeRig {
	t.Helper()
	ctx := t.Context()
	state, err := sqlite.OpenState(ctx, t.TempDir(), sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	r := &planeRig{t: t, state: state, stores: map[ledger.WorkspaceID]ports.Store{}}
	for _, p := range []ledger.Project{{ID: "pa", Name: "alpha"}, {ID: "pb", Name: "beta"}} {
		_, store, err := state.CreateProject(ctx, p, projectMember)
		if err != nil {
			t.Fatal(err)
		}
		r.stores[p.ID] = store
	}
	r.plane = control.NewPlane(state, state.Installation, "pa", control.WorkerOptions{}, func(store ports.Store) ports.Matcher {
		return lexical.Matcher{Store: store}
	})
	token, err := r.plane.CreateJoinToken(ctx, projectMember)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := r.plane.Register(ctx, ledger.Registration{JoinToken: token, WorkerID: "worker-1", Name: "w"})
	if err != nil {
		t.Fatal(err)
	}
	r.auth = ledger.WorkerAuth{WorkerID: "worker-1", Credential: credential.Credential}
	session, err := r.plane.OpenSession(ctx, ledger.SessionRequest{Auth: r.auth, Protocol: protocol, Manifest: apiKeyManifest})
	if err != nil {
		t.Fatal(err)
	}
	r.epoch = session.Epoch
	return r
}

// ready records a work order a worker may claim in a project.
func (r *planeRig) ready(project ledger.WorkspaceID, id string) {
	r.t.Helper()
	store := r.stores[project]
	_, err := control.NewWorkOrders(store).Record(r.t.Context(), project, control.LedgerWork{
		Order:    workorders.WorkOrder{ID: id, Kind: workorders.KindChange, Title: "T"},
		Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
	}, projectMember)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := control.NewReviews(store).Pass(r.t.Context(), project, id, projectMember); err != nil {
		r.t.Fatal(err)
	}
}

func (r *planeRig) claim(project, id string) error {
	_, err := r.plane.Claim(r.t.Context(), r.auth, workorders.ClaimRequest{Project: project, WorkOrder: id, ClaimID: "claim-" + project + "-" + id})
	return err
}

func (r *planeRig) desired() ledger.DesiredState {
	r.t.Helper()
	state, err := r.plane.Desired(r.t.Context(), ledger.DesiredRequest{Auth: r.auth})
	if err != nil {
		r.t.Fatal(err)
	}
	return state
}

// TestTheWorkerClaimsInEveryActiveProject claims one work order in each project through the plane: each
// lease names its project, and desired state names both projects with a revision each.
func TestTheWorkerClaimsInEveryActiveProject(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	r.ready("pa", "wo")
	r.ready("pb", "wo")
	for _, project := range []string{"pa", "pb"} {
		if err := r.claim(project, "wo"); err != nil {
			t.Fatalf("a claim in %s: %v", project, err)
		}
	}
	state := r.desired()
	held := map[string]bool{}
	for _, lease := range state.Leases {
		held[lease.Project+"/"+lease.WorkOrder] = true
	}
	if !held["pa/wo"] || !held["pb/wo"] || len(state.Leases) != 2 {
		t.Errorf("leases %+v", state.Leases)
	}
	if len(state.Projects) != 2 || state.Revisions["pa"] == 0 || state.Revisions["pb"] == 0 {
		t.Errorf("projects %+v at %v", state.Projects, state.Revisions)
	}
	again, err := r.plane.Desired(t.Context(), ledger.DesiredRequest{Auth: r.auth, Since: state.Rev, Revisions: state.Revisions})
	if err != nil || !again.Unchanged {
		t.Errorf("a read at the same revisions: %+v, %v", again, err)
	}
}

// TestAnArchivedProjectTakesNoClaim archives a project: the plane refuses a claim in it, offers nothing of
// it, and leaves the active project as it was.
func TestAnArchivedProjectTakesNoClaim(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	r.ready("pa", "wo")
	r.ready("pb", "wo")
	if _, err := r.state.SetState(t.Context(), "pb", ledger.ProjectArchived, projectMember); err != nil {
		t.Fatal(err)
	}
	if err := r.claim("pb", "wo"); !errors.Is(err, ledger.ErrProjectArchived) {
		t.Errorf("a claim in an archived project: %v", err)
	}
	if err := r.claim("pa", "wo"); err != nil {
		t.Errorf("a claim in the active project: %v", err)
	}
	state := r.desired()
	for _, project := range state.Projects {
		if project.ID == "pb" {
			t.Errorf("desired state serves the archived project: %+v", state.Projects)
		}
	}
	if _, ok := state.Revisions["pb"]; ok {
		t.Errorf("desired state counts the archived project: %v", state.Revisions)
	}
}

// TestAWorkerOneVersionBehindServesOnlyTheDefaultProject opens a session at the protocol before projects:
// its claim names no project and lands in the default project, its desired state names no project, and
// once the installation removes the default project it is refused with a protocol error.
func TestAWorkerOneVersionBehindServesOnlyTheDefaultProject(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolProjects-1)
	r.ready("pa", "wo")
	r.ready("pb", "other")
	if err := r.claim("", "wo"); err != nil {
		t.Fatal(err)
	}
	if err := r.claim("pb", "other"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a claim in another project reached it: %v", err)
	}
	state := r.desired()
	if len(state.Leases) != 1 || state.Leases[0].Project != "" || state.Projects != nil || state.Revisions != nil {
		t.Errorf("an old worker's desired state %+v", state)
	}
	if _, err := r.state.SetState(t.Context(), "pa", ledger.ProjectArchived, projectMember); err != nil {
		t.Fatal(err)
	}
	if err := r.state.Remove(t.Context(), ledger.Project{ID: "pa", Name: "alpha", Database: "projects/pa.db"}, projectMember); err != nil {
		t.Fatal(err)
	}
	r.plane.Forget("pa")
	_, err := r.plane.OpenSession(t.Context(), ledger.SessionRequest{Auth: r.auth, Protocol: ledger.ProtocolProjects - 1, Manifest: apiKeyManifest})
	var refused *ledger.ProtocolError
	if !errors.As(err, &refused) {
		t.Errorf("an old worker with no default project: %v", err)
	}
}

// orchestrating starts the worker's orchestrator, as a member does, and returns the call its session makes.
func (r *planeRig) orchestrating() ledger.OrchestratorCall {
	r.t.Helper()
	row, err := r.plane.SetOrchestrator(r.t.Context(), "worker-1", ledger.OrchestratorRunning, projectMember)
	if err != nil {
		r.t.Fatal(err)
	}
	return ledger.OrchestratorCall{Auth: r.auth, Epoch: r.epoch, Lane: row.Lane()}
}

// TestOneOrchestratorServesEveryProject runs one orchestrator over two projects: its candidate list holds
// both projects' work, each item named with its project, and the next question names its project too.
func TestOneOrchestratorServesEveryProject(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	ctx := t.Context()
	r.ready("pa", "wo-a")
	r.ready("pb", "wo-b")
	if _, err := control.NewQuestions(r.stores["pb"], questions.BatchPolicy{}).RecordQuestion(ctx, "pb", questions.Question{ID: "q-b", Text: "Which queue carries retries?"}, projectMember); err != nil {
		t.Fatal(err)
	}
	call := r.orchestrating()
	reply, err := r.plane.Orchestrate(ctx, ledger.VerbCandidates, call)
	if err != nil {
		t.Fatal(err)
	}
	var offered []struct {
		ID      string `json:"id"`
		Project string `json:"project"`
		Name    string `json:"project_name"`
	}
	if err := json.Unmarshal(reply.Result, &offered); err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, item := range offered {
		labels[item.ID] = item.Project + " " + item.Name
	}
	if len(offered) != 2 || labels["wo-a"] != "pa alpha" || labels["wo-b"] != "pb beta" {
		t.Errorf("candidates %s", reply.Result)
	}
	reply, err = r.plane.Orchestrate(ctx, ledger.VerbNext, call)
	if err != nil {
		t.Fatal(err)
	}
	var next struct {
		Question questions.Question `json:"question"`
		Project  string             `json:"project"`
	}
	if err := json.Unmarshal(reply.Result, &next); err != nil {
		t.Fatal(err)
	}
	if next.Question.ID != "q-b" || next.Project != "pb" {
		t.Errorf("the next question %s", reply.Result)
	}
	if _, err := r.plane.Orchestrate(ctx, ledger.VerbClaim, ledger.OrchestratorCall{
		Auth: r.auth, Epoch: r.epoch, Lane: call.Lane, Project: "pb", WorkOrder: "wo-a", Target: "claim-1",
	}); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a claim of one project's work in another: %v", err)
	}
}

// TestAnOrchestratorCannotCiteAnotherProjectsRecord holds ADR-0021's citation rule: a question of one
// project is answered from that project's records only. Its ladder lookup finds no decision of another
// project, a decision of another project neither answers it nor reads, and the same decision answers in
// its own project.
func TestAnOrchestratorCannotCiteAnotherProjectsRecord(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	ctx := t.Context()
	member := projectMember
	if _, err := control.NewQuestions(r.stores["pa"], questions.BatchPolicy{}).RecordQuestion(ctx, "pa", questions.Question{ID: "q-a", Text: "What is the retry budget?"}, member); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewQuestions(r.stores["pb"], questions.BatchPolicy{}).RecordDecision(ctx, "pb", questions.Decision{
		ID: "d-b", Status: questions.DecisionDecided, Statement: "The retry budget is two retries.", Rationale: "It follows.",
		Scope: "retries", Reversibility: questions.Reversible, DecidedBy: member.ID, Citations: []string{"PAY-12"},
	}, member); err != nil {
		t.Fatal(err)
	}
	call := r.orchestrating()
	call.Project = "pa"
	reply, err := r.plane.Orchestrate(ctx, ledger.VerbNext, call)
	if err != nil {
		t.Fatal(err)
	}
	var read control.LadderRead
	if err := json.Unmarshal(reply.Result, &read); err != nil {
		t.Fatal(err)
	}
	if read.Question.ID != "q-a" {
		t.Fatalf("the next question %s", reply.Result)
	}
	for _, candidate := range read.Lookup {
		if candidate.Ref == "d-b" {
			t.Errorf("the ladder's lookup in alpha found beta's decision: %+v", read.Lookup)
		}
	}
	resolve := call
	resolve.Question, resolve.Target = "q-a", "d-b"
	if _, err := r.plane.Orchestrate(ctx, ledger.VerbResolve, resolve); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("alpha's question answered by beta's decision: %v", err)
	}
	read2 := func(project string) []ledger.ContextRecord {
		found, err := r.plane.LaneContext(ctx, ledger.LaneContextRequest{
			Auth: r.auth, Epoch: r.epoch, Project: project, Lane: call.Lane, Tool: lanes.ToolContextGet, Kind: "decision", Ref: "d-b",
		})
		if err != nil {
			t.Fatal(err)
		}
		return found.Records
	}
	if records := read2("pa"); len(records) != 0 {
		t.Errorf("alpha reads beta's decision: %+v", records)
	}
	if records := read2("pb"); len(records) != 1 {
		t.Errorf("beta does not read its own decision: %+v", records)
	}
}
