package server_test

import (
	"net/http"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

const aSubscriptionWorker = "worker-on-a-plan"

// joinWorker registers a worker on a subscription, and returns its credential.
func joinWorker(t *testing.T, d *apitest.Daemon) (string, int64) {
	t.Helper()
	token, err := d.Client.CreateJoinToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	credential, err := d.Client.Register(t.Context(), ledger.Registration{JoinToken: token, WorkerID: aSubscriptionWorker, Name: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	worker := api.NewUnixClient(d.Socket).Project(apitest.ProjectName).WithCredential(credential.Credential)
	t.Cleanup(worker.Close)
	session, err := worker.OpenSession(t.Context(), aSubscriptionWorker, ledger.SessionRequest{Protocol: ledger.ProtocolVersion, Manifest: ledger.Manifest{
		Drivers: []ledger.DriverManifest{{Driver: "claude-code", Present: true, SignIn: ledger.SignIn{Kind: ledger.SignInSubscription, Status: ledger.SignedIn}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return credential.Credential, session.Epoch
}

func withSession(token string) func(*api.ProjectClient) *api.ProjectClient {
	return func(c *api.ProjectClient) *api.ProjectClient {
		c.UseSession(token)
		return c
	}
}

// memberActs are the acts that keep a person near automated use, as a caller's client makes them.
func memberActs(client *api.ProjectClient) map[string]func(*testing.T) error {
	return map[string]func(*testing.T) error{
		"open a window": func(t *testing.T) error {
			_, err := client.OpenWindow(t.Context(), aSubscriptionWorker, api.OpenWindow{ID: "w1", LengthMs: 60_000})
			return err
		},
		"close a window": func(t *testing.T) error { return client.CloseWindow(t.Context(), aSubscriptionWorker) },
		"acknowledge the risk": func(t *testing.T) error {
			return client.AcknowledgeRisk(t.Context(), aSubscriptionWorker, api.AcknowledgeRisk{Digest: authority.RiskDigest()})
		},
		"choose a level": func(t *testing.T) error {
			return client.SetAutonomy(t.Context(), aSubscriptionWorker, api.SetAutonomy{Level: authority.LevelUnattended})
		},
		"publish a policy": func(t *testing.T) error {
			_, err := client.PublishPolicy(t.Context(), strings.ReplaceAll(authority.BuiltInAutonomyBody, "version = 1", "version = 2"))
			return err
		},
	}
}

// TestAnAgentCannotOpenAWindowOrAcknowledgeTheRisk is the attack the unit exists to stop. A lane runs
// as the same user, so it reaches this socket, and it holds what a lane holds: a token, the worker's
// credential if it read the file, and whatever it can guess. None of it is a member's session.
func TestAnAgentCannotOpenAWindowOrAcknowledgeTheRisk(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	credential, _ := joinWorker(t, d)
	spent, err := d.Client.SignIn(t.Context(), api.SignIn{Passphrase: apitest.Passphrase, TTLMs: 1})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		setup func(*api.ProjectClient) *api.ProjectClient
	}{
		{"a caller with nothing", func(c *api.ProjectClient) *api.ProjectClient { return c }},
		{"the worker's credential", func(c *api.ProjectClient) *api.ProjectClient { return c.WithCredential(credential) }},
		{"the credential where a session goes", withSession(credential)},
		{"a lane's token where a session goes", withSession("a-lane-token")},
		{"a session that ended", withSession(spent.Token)},
	}
	for _, tt := range cases {
		plain := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
		t.Cleanup(plain.Close)
		caller := tt.setup(plain)
		for act, attempt := range memberActs(caller) {
			t.Run(tt.name+"/"+act, func(t *testing.T) {
				refusedWith(t, attempt(t), http.StatusUnauthorized, api.CodeSessionRequired)
			})
		}
	}
	view, err := d.Client.Autonomy(t.Context(), aSubscriptionWorker)
	if err != nil {
		t.Fatal(err)
	}
	if view.Level != authority.LevelAttended || view.Window != nil || view.Acknowledged || view.Chosen != "" {
		t.Fatalf("a refusal changed something: %+v", view)
	}
	events := 0
	err = d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.Events(0, 10_000)
		for _, row := range rows {
			if row.Subject.Kind == authority.SubjectAutonomy {
				events++
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("%d events", events)
	}
}

// The worker's own route carries an orchestrator's acts, and a window is not one of them, whatever it
// is called.
func TestAWorkerCarriesNoSuchAct(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	credential, epoch := joinWorker(t, d)
	running, err := d.Client.SetOrchestrator(t.Context(), aSubscriptionWorker, api.SetOrchestrator{Desired: ledger.OrchestratorRunning})
	if err != nil {
		t.Fatal(err)
	}
	worker := api.NewUnixClient(d.Socket).Project(apitest.ProjectName).WithCredential(credential)
	defer worker.Close()
	call := ledger.OrchestratorCall{Epoch: epoch, Lane: running.Lane()}
	path := "/v1/workers/" + aSubscriptionWorker + "/orchestrator/acts/"
	if err := worker.Do(t.Context(), http.MethodPost, path+ledger.VerbCandidates, call, nil); err != nil {
		t.Fatalf("a real act was refused, so nothing below proves anything: %v", err)
	}
	for _, verb := range []string{"window", "open_window", "window_open", "acknowledge", "acknowledge_risk", "autonomy", "level", "set_level"} {
		var out ledger.OrchestratorReply
		err := worker.Do(t.Context(), http.MethodPost, path+verb, call, &out)
		refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	}
}

func TestAMemberOpensAWindowAndReadsItBack(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	joinWorker(t, d)
	window, err := d.Client.OpenWindow(t.Context(), aSubscriptionWorker, api.OpenWindow{ID: "w1", LengthMs: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	if window.OpenedKind != string(ledger.ActorMember) || window.ClosesAt-window.OpenedAt != 60_000 {
		t.Fatalf("window %+v", window)
	}
	view, err := d.Client.Autonomy(t.Context(), aSubscriptionWorker)
	if err != nil {
		t.Fatal(err)
	}
	if !view.MayClaim || view.Window == nil || view.Window.ID != "w1" {
		t.Fatalf("view %+v", view)
	}
	_, err = d.Client.OpenWindow(t.Context(), aSubscriptionWorker, api.OpenWindow{ID: "w2"})
	refusedWith(t, err, http.StatusConflict, api.CodeWindowOpen)
	if err := d.Client.CloseWindow(t.Context(), aSubscriptionWorker); err != nil {
		t.Fatal(err)
	}
	if view, err = d.Client.Autonomy(t.Context(), aSubscriptionWorker); err != nil || view.MayClaim || view.Window != nil {
		t.Fatalf("after a close: %+v, %v", view, err)
	}
}

func TestUnattendedAsksForTheAcknowledgementFirst(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	joinWorker(t, d)
	err := d.Client.SetAutonomy(t.Context(), aSubscriptionWorker, api.SetAutonomy{Level: authority.LevelUnattended})
	refusedWith(t, err, http.StatusForbidden, api.CodeRiskNotAcknowledged)
	err = d.Client.AcknowledgeRisk(t.Context(), aSubscriptionWorker, api.AcknowledgeRisk{Digest: "sha256:another"})
	refusedWith(t, err, http.StatusConflict, api.CodeStaleStatement)
	risk, err := d.Client.Risk(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if risk.Statement != authority.RiskStatement {
		t.Fatalf("statement %q", risk.Statement)
	}
	if err := d.Client.AcknowledgeRisk(t.Context(), aSubscriptionWorker, api.AcknowledgeRisk{Digest: risk.Digest}); err != nil {
		t.Fatal(err)
	}
	if err := d.Client.SetAutonomy(t.Context(), aSubscriptionWorker, api.SetAutonomy{Level: authority.LevelUnattended}); err != nil {
		t.Fatal(err)
	}
	view, err := d.Client.Autonomy(t.Context(), aSubscriptionWorker)
	if err != nil {
		t.Fatal(err)
	}
	if view.Level != authority.LevelUnattended || !view.Acknowledged || !view.MayClaim {
		t.Fatalf("view %+v", view)
	}
}

// A policy states what it means: a key left out would loosen a bound nobody chose to loosen.
func TestAnAutonomyPolicyStatesEveryKey(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	cases := []struct {
		name string
		body string
	}{
		{"a bound left out", "[autonomy_policy]\nversion = 2\nsubscription_default = \"attended\"\napi_key_default = \"unattended\"\nattended_max_lanes = 2\n"},
		{"a setting nobody knows", "[autonomy_policy]\nversion = 2\nsubscription_default = \"attended\"\napi_key_default = \"unattended\"\nattended_max_lanes = 2\nattended_max_respawns_per_day = 2\nno_person_needed = true\n"},
		{"an unknown level", "[autonomy_policy]\nversion = 2\nsubscription_default = \"free\"\napi_key_default = \"unattended\"\nattended_max_lanes = 2\nattended_max_respawns_per_day = 2\n"},
		{"an older version", "[autonomy_policy]\nversion = 1\nsubscription_default = \"manual\"\napi_key_default = \"unattended\"\nattended_max_lanes = 2\nattended_max_respawns_per_day = 2\n"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := d.Client.PublishPolicy(t.Context(), tt.body)
			refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
		})
	}
}
