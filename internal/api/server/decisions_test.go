package server_test

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/test/apitest"
)

const testWorker = "worker-1"

type rulingRig struct {
	daemon *apitest.Daemon
	use    *control.Questions
	worker *api.Client
	epoch  int64
}

// newRulingRig registers a worker, because an orchestrator reaches the API through its own.
func newRulingRig(t *testing.T) rulingRig {
	t.Helper()
	return rulingRigOn(t, apitest.Start(t))
}

func rulingRigOn(t *testing.T, daemon *apitest.Daemon) rulingRig {
	t.Helper()
	ctx := t.Context()
	token, err := daemon.Client.CreateJoinToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := daemon.Client.Register(ctx, ledger.Registration{JoinToken: token, WorkerID: testWorker, Name: testWorker})
	if err != nil {
		t.Fatal(err)
	}
	worker := daemon.Client.WithCredential(credential.Credential)
	session, err := worker.OpenSession(ctx, testWorker, ledger.SessionRequest{Protocol: ledger.ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	return rulingRig{daemon: daemon, use: control.NewQuestions(daemon.Store, questions.BatchPolicy{}), worker: worker, epoch: session.Epoch}
}

// onRung records a question and climbs it for this worker's orchestrator.
func (r rulingRig) onRung(t *testing.T, id, text, category string, rung int64) {
	t.Helper()
	ctx := t.Context()
	if _, err := r.daemon.Client.RecordQuestion(ctx, api.RecordQuestion{ID: id, Text: text, Categories: []string{category}}); err != nil {
		t.Fatal(err)
	}
	orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: testWorker}
	if _, err := r.use.Take(ctx, apitest.Scope, id, 60_000, orchestrator); err != nil {
		t.Fatal(err)
	}
	for step := questions.RungLookup; step < rung; step++ {
		if _, err := r.use.Climb(ctx, apitest.Scope, id, questions.Step{Rung: step, Outcome: "no match"}, orchestrator); err != nil {
			t.Fatal(err)
		}
	}
}

func (r rulingRig) rule(t *testing.T, question string, ruling questions.Ruling) (questions.Decision, error) {
	t.Helper()
	return r.worker.Rule(t.Context(), testWorker, questions.WorkerRuling{Epoch: r.epoch, Question: question, Ruling: ruling})
}

func draftRuling(id string, status questions.DecisionStatus) questions.Ruling {
	return questions.Ruling{
		ID: id, Status: status, Statement: "The retry budget is three calls.", Rationale: "It follows from the contract.",
		Citations: []string{"PAY-12"}, Scope: "checkout", Reversibility: questions.Reversible,
	}
}

// TestOneWayCategoryRefused holds FR-ORCH-5 at the API: an orchestrator's ruling on a
// one-way category leaves nothing behind.
func TestOneWayCategoryRefused(t *testing.T) {
	r := newRulingRig(t)
	r.onRung(t, "q1", "May audit data leave the region?", questions.CategorySecurity, questions.RungDecide)
	_, err := r.rule(t, "q1", draftRuling("d1", questions.DecisionDecided))
	refusedWith(t, err, http.StatusForbidden, api.CodeOneWay)
	_, err = r.rule(t, "q1", draftRuling("d2", questions.DecisionAssumed))
	refusedWith(t, err, http.StatusForbidden, api.CodeOneWay)
	for _, id := range []string{"d1", "d2"} {
		if _, err := r.daemon.Client.Decision(t.Context(), id); err == nil {
			t.Errorf("decision %s exists", id)
		}
	}
}

func TestRungsWriteThroughTheAPI(t *testing.T) {
	r := newRulingRig(t)
	r.onRung(t, "q1", "What is the retry budget?", questions.CategoryProduct, questions.RungDecide)
	decided, err := r.rule(t, "q1", draftRuling("d1", questions.DecisionDecided))
	if err != nil {
		t.Fatal(err)
	}
	if decided.Status != questions.DecisionDecided || decided.DecidedBy != testWorker || decided.PolicyVersion != 1 {
		t.Errorf("decision %+v", decided)
	}
	r.onRung(t, "q2", "Which log level do retries use?", questions.CategoryProduct, questions.RungAssume)
	draft := draftRuling("d2", questions.DecisionAssumed)
	_, err = r.rule(t, "q2", draft)
	refusedWith(t, err, http.StatusConflict, api.CodeOverlap)
	var refused *api.Error
	if !errors.As(err, &refused) || !slices.Equal(refused.Detail.Decisions, []string{"d1"}) {
		t.Fatalf("the refusal names %+v", err)
	}
	draft.NoConflict = []string{"d1"}
	assumed, err := r.rule(t, "q2", draft)
	if err != nil {
		t.Fatal(err)
	}
	if assumed.Status != questions.DecisionAssumed || !slices.Equal(assumed.Overlaps, []string{"d1"}) {
		t.Errorf("assumption %+v", assumed)
	}
	question, err := r.daemon.Client.Question(t.Context(), "q2")
	if err != nil || question.Status != questions.StatusAnswered || question.DecisionID != "d2" {
		t.Errorf("question %+v: %v", question, err)
	}
}

func TestARulingNeedsTheWorkersOwnSession(t *testing.T) {
	r := newRulingRig(t)
	r.onRung(t, "q1", "What is the retry budget?", questions.CategoryProduct, questions.RungDecide)
	_, err := r.daemon.Client.WithCredential("wrong").Rule(t.Context(), testWorker, questions.WorkerRuling{
		Epoch: r.epoch, Question: "q1", Ruling: draftRuling("d1", questions.DecisionDecided),
	})
	refusedWith(t, err, http.StatusUnauthorized, api.CodeBadCredential)
	_, err = r.worker.Rule(t.Context(), testWorker, questions.WorkerRuling{
		Epoch: r.epoch + 1, Question: "q1", Ruling: draftRuling("d1", questions.DecisionDecided),
	})
	refusedWith(t, err, http.StatusConflict, api.CodeStaleEpoch)
}

func TestRulingRefusalsHaveTheirOwnCodes(t *testing.T) {
	r := newRulingRig(t)
	r.onRung(t, "q1", "What is the retry budget?", questions.CategoryProduct, questions.RungDecide)
	_, err := r.rule(t, "q1", draftRuling("d1", questions.DecisionAssumed))
	refusedWith(t, err, http.StatusConflict, api.CodeWrongRung)
	if _, err := r.daemon.Client.RecordQuestion(t.Context(), api.RecordQuestion{ID: "q2", Text: "Which log level?"}); err != nil {
		t.Fatal(err)
	}
	_, err = r.rule(t, "q2", draftRuling("d2", questions.DecisionDecided))
	refusedWith(t, err, http.StatusConflict, api.CodeNotHeld)
}

const sevenBody = `# Reviewed with the team.
[decision_policy]
version = 7
reversible_default = "assume"
one_way_categories = ["security", "compliance", "data", "product"]
max_assumptions_per_work_order = 2
`

func TestAPolicyIsPublishedAndReviewedThroughTheAPI(t *testing.T) {
	daemon := apitest.Start(t)
	ctx := t.Context()
	before, err := daemon.Client.Policies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || before[0].Version != 1 || before[0].Body != questions.BuiltInPolicyBody {
		t.Fatalf("a new workspace reviews %+v", before)
	}
	published, err := daemon.Client.PublishPolicy(ctx, sevenBody)
	if err != nil {
		t.Fatal(err)
	}
	if published.Version != 7 || published.Body != sevenBody || !strings.HasPrefix(published.Digest, "sha256:") {
		t.Fatalf("published %+v", published)
	}
	read, err := daemon.Client.Policy(ctx, 7)
	if err != nil || read.Digest != published.Digest {
		t.Fatalf("read %+v: %v", read, err)
	}
	rows, err := daemon.Client.Policies(ctx)
	if err != nil || len(rows) != 2 || rows[0].Version != 7 {
		t.Fatalf("versions %+v: %v", rows, err)
	}
	_, err = daemon.Client.Policy(ctx, 3)
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
}

func TestALanePolicyIsPublishedAndReviewedThroughTheAPI(t *testing.T) {
	daemon := apitest.Start(t)
	ctx := t.Context()
	before, err := daemon.Client.LanePolicies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || before[0].Version != 1 || before[0].Body != authority.BuiltInLanePolicyBody {
		t.Fatalf("a new workspace reviews %+v", before)
	}
	body := strings.Replace(strings.Replace(authority.BuiltInLanePolicyBody, "version = 1", "version = 2", 1), `on_contract_version = "notify"`, `on_contract_version = "park"`, 1)
	published, err := daemon.Client.PublishPolicy(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if published.Kind != authority.PolicyLane || published.Version != 2 {
		t.Fatalf("published %+v", published)
	}
	read, err := daemon.Client.LanePolicy(ctx, 2)
	if err != nil || read.Digest != published.Digest {
		t.Fatalf("read %+v: %v", read, err)
	}
	decisions, err := daemon.Client.Policies(ctx)
	if err != nil || len(decisions) != 1 || decisions[0].Kind != authority.PolicyDecision {
		t.Fatalf("the decision policy reads %+v: %v", decisions, err)
	}
	var inForce authority.LanePolicy
	err = daemon.Store.Read(ctx, apitest.Scope, func(tx ports.ReadTx) error {
		version, err := tx.Policies().InForce(authority.PolicyLane)
		if err != nil {
			return err
		}
		inForce, err = authority.ReadLanePolicy(version)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := (authority.LanePolicy{Version: 2, OnContractVersion: authority.LanePark, OnOneWayDecision: authority.LaneNotify}); inForce != want {
		t.Errorf("in force %+v, want %+v", inForce, want)
	}
}

const delegationBody = `[delegation_policy]
version = 2

[[delegation_policy.classes]]
tool = "Bash"
command_prefix = "go test"
`

// A new workspace delegates nothing, and what a member publishes is what a store holds in force.
func TestADelegationPolicyIsPublishedAndReviewedThroughTheAPI(t *testing.T) {
	daemon := apitest.Start(t)
	ctx := t.Context()
	before, err := daemon.Client.DelegationPolicies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || before[0].Version != 1 || before[0].Body != authority.BuiltInDelegationBody {
		t.Fatalf("a new workspace reviews %+v", before)
	}
	published, err := daemon.Client.PublishPolicy(ctx, delegationBody)
	if err != nil {
		t.Fatal(err)
	}
	if published.Kind != authority.PolicyDelegation || published.Version != 2 {
		t.Fatalf("published %+v", published)
	}
	read, err := daemon.Client.DelegationPolicy(ctx, 2)
	if err != nil || read.Digest != published.Digest || read.Body != delegationBody {
		t.Fatalf("read %+v: %v", read, err)
	}
	var inForce authority.DelegationPolicy
	err = daemon.Store.Read(ctx, apitest.Scope, func(tx ports.ReadTx) error {
		version, err := tx.Policies().InForce(authority.PolicyDelegation)
		if err != nil {
			return err
		}
		inForce, err = authority.ReadDelegation(version)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inForce.Classes) != 1 || inForce.Classes[0] != (authority.DelegationClass{Tool: "Bash", CommandPrefix: "go test"}) {
		t.Errorf("in force %+v", inForce)
	}
}

// A version older than the one in force is refused even where nothing holds that number, because
// a publish must not hand back what a member took away.
func TestADelegationPolicyOnlyMovesForward(t *testing.T) {
	daemon := apitest.Start(t)
	if _, err := daemon.Client.PublishPolicy(t.Context(), strings.Replace(delegationBody, "version = 2", "version = 5", 1)); err != nil {
		t.Fatal(err)
	}
	_, err := daemon.Client.PublishPolicy(t.Context(), strings.Replace(delegationBody, "version = 2", "version = 4", 1))
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
}

func TestADelegationPolicyBodyMustBeComplete(t *testing.T) {
	daemon := apitest.Start(t)
	tests := []struct{ name, body string }{
		{"a misspelled key", strings.Replace(delegationBody, "command_prefix", "command_start", 1)},
		{"no version", strings.Replace(delegationBody, "version = 2\n", "", 1)},
		{"an old version", strings.Replace(delegationBody, "version = 2", "version = 1", 1)},
		{"a whole tool", strings.Replace(delegationBody, "command_prefix = \"go test\"\n", "", 1)},
		{"shell syntax in a prefix", strings.Replace(delegationBody, "go test", "go test; rm", 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := daemon.Client.PublishPolicy(t.Context(), tt.body)
			refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
		})
	}
}

func TestALanePolicyBodyMustBeComplete(t *testing.T) {
	daemon := apitest.Start(t)
	base := strings.Replace(authority.BuiltInLanePolicyBody, "version = 1", "version = 2", 1)
	tests := []struct{ name, body string }{
		{"a misspelled key", strings.Replace(base, "on_contract_version", "on_contract", 1)},
		{"no setting for a contract", strings.Replace(base, "on_contract_version = \"notify\"\n", "", 1)},
		{"no setting for a one-way decision", strings.Replace(base, "on_one_way_decision = \"notify\"\n", "", 1)},
		{"no version", strings.Replace(base, "version = 2\n", "", 1)},
		{"an action nobody named", strings.Replace(base, `"notify"`, `"ignore"`, 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := daemon.Client.PublishPolicy(t.Context(), tt.body)
			refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
		})
	}
}

func TestAPolicyBodyMustBeComplete(t *testing.T) {
	daemon := apitest.Start(t)
	tests := []struct{ name, body string }{
		{"not TOML", "[decision_policy"},
		{"no table", "version = 2\n"},
		{"a misspelled key", strings.Replace(sevenBody, "one_way_categories", "one_way_category", 1)},
		{"no limit", strings.Replace(sevenBody, "max_assumptions_per_work_order = 2\n", "", 1)},
		{"no list", strings.Replace(sevenBody, `one_way_categories = ["security", "compliance", "data", "product"]`+"\n", "", 1)},
		{"an unknown default", strings.Replace(sevenBody, `"assume"`, `"ask"`, 1)},
		{"an unknown category", strings.Replace(sevenBody, `"product"`, `"prodct"`, 1)},
		{"a limit below zero", strings.Replace(sevenBody, "= 2", "= -1", 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := daemon.Client.PublishPolicy(t.Context(), tt.body)
			refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
		})
	}
}

// TestTheBuiltInBodySaysTheDefaultPolicy keeps the two forms of the first version together,
// because the ledger stores both.
func TestTheBuiltInBodySaysTheDefaultPolicy(t *testing.T) {
	daemon := apitest.Start(t)
	ctx := t.Context()
	body := strings.Replace(questions.BuiltInPolicyBody, "version = 1", "version = 2", 1)
	if _, err := daemon.Client.PublishPolicy(ctx, body); err != nil {
		t.Fatal(err)
	}
	var published questions.Policy
	err := daemon.Store.Read(ctx, apitest.Scope, func(tx ports.ReadTx) error {
		version, err := tx.Policies().InForce("decision")
		if err != nil {
			return err
		}
		published, err = questions.ReadPolicy(version)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	builtIn := questions.DefaultPolicy()
	if published.ReversibleDefault != builtIn.ReversibleDefault || !slices.Equal(published.OneWayCategories, builtIn.OneWayCategories) ||
		published.MaxAssumptionsPerWorkOrder != builtIn.MaxAssumptionsPerWorkOrder {
		t.Errorf("the body says %+v, the default %+v", published, builtIn)
	}
}

func TestAMemberOverturnsThroughTheAPI(t *testing.T) {
	r := newRulingRig(t)
	ctx := t.Context()
	r.onRung(t, "q1", "Which log level do retries use?", questions.CategoryProduct, questions.RungAssume)
	if _, err := r.rule(t, "q1", draftRuling("d1", questions.DecisionAssumed)); err != nil {
		t.Fatal(err)
	}
	result, err := r.daemon.Client.OverturnDecision(ctx, "d1", api.OverturnDecision{ID: "d2", Statement: "Retries log at warn."})
	if err != nil {
		t.Fatal(err)
	}
	if result.Old.Status != questions.DecisionOverturned || result.New.Supersedes != "d1" {
		t.Fatalf("result %+v", result)
	}
	read, err := r.daemon.Client.Decision(ctx, "d1")
	if err != nil || read.Status != questions.DecisionOverturned || read.Statement != "The retry budget is three calls." {
		t.Errorf("the overturned decision reads %+v: %v", read, err)
	}
	rows, err := r.daemon.Client.Decisions(ctx, api.RecordFilter{Status: string(questions.DecisionOverturned)})
	if err != nil || len(rows) != 1 {
		t.Errorf("overturned %+v: %v", rows, err)
	}
	_, err = r.daemon.Client.OverturnDecision(ctx, "d1", api.OverturnDecision{ID: "d3", Statement: "Again."})
	refusedWith(t, err, http.StatusConflict, api.CodeDecisionEnded)
}

func TestOverturnNeedsASession(t *testing.T) {
	r := newRulingRig(t)
	r.onRung(t, "q1", "Which log level do retries use?", questions.CategoryProduct, questions.RungAssume)
	if _, err := r.rule(t, "q1", draftRuling("d1", questions.DecisionAssumed)); err != nil {
		t.Fatal(err)
	}
	anonymous := api.NewUnixClient(r.daemon.Socket)
	t.Cleanup(anonymous.Close)
	_, err := anonymous.OverturnDecision(t.Context(), "d1", api.OverturnDecision{ID: "d2", Statement: "No."})
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
	_, err = anonymous.PublishPolicy(t.Context(), sevenBody)
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
}
