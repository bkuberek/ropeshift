package server_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// practicesDaemon serves a project whose workers keep the checkouts given, by worker ID, and whose own worker is the
// first named.
func practicesDaemon(t *testing.T, keeps map[string][]string, local string) *apitest.Daemon {
	t.Helper()
	return apitest.StartWith(t, apitest.Options{
		AssistantHosts: func(context.Context, ledger.WorkspaceID) (map[string]control.AssistantHost, error) {
			hosts := map[string]control.AssistantHost{}
			for worker, repositories := range keeps {
				host := control.AssistantHost{Checkouts: map[string]bool{}}
				for _, repository := range repositories {
					host.Checkouts[repository] = true
				}
				hosts[worker] = host
			}
			return hosts, nil
		},
		LocalWorker: func(context.Context) (string, error) { return local, nil },
	})
}

// connectWorker registers a worker and opens its session on a protocol.
func connectWorker(t *testing.T, d *apitest.Daemon, id string, protocol int) {
	t.Helper()
	token, err := d.Client.CreateJoinToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	credential, err := d.Client.Register(t.Context(), ledger.Registration{JoinToken: token, WorkerID: id, Name: id})
	if err != nil {
		t.Fatal(err)
	}
	worker := workerOf(t, d, credential.Credential)
	if _, err := worker.OpenSession(t.Context(), id, ledger.SessionRequest{Protocol: protocol}); err != nil {
		t.Fatal(err)
	}
}

// keepResult records a result as a worker's report would leave it, as the latest of its repository.
func keepResult(t *testing.T, d *apitest.Daemon, request string, result practices.Result) {
	t.Helper()
	err := d.Store.Write(t.Context(), apitest.Scope, "test.practices", func(tx ports.WriteTx) error {
		err := tx.Practices().SaveResult(ledger.PracticesRecord{Repository: result.Repository, Request: request, Commit: result.Commit, Result: result, CheckedAt: result.CheckedAt.UnixMilli()})
		if err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: ledger.EventPracticesChecked, Actor: ledger.Actor{Kind: ledger.ActorWorker, ID: "w"}, Subject: ledger.Subject{Kind: ledger.SubjectPracticesRequest, ID: request}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func entryOf(t *testing.T, checked api.PracticesChecked, name string) api.PracticesCheckEntry {
	t.Helper()
	for _, entry := range checked.Checks {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("the answer holds no entry for %s: %+v", name, checked.Checks)
	return api.PracticesCheckEntry{}
}

// The read lists each active repository by name, and one when it is named, whether by name, by identity, or by a
// prefix of it; a repository nothing checked reads with no result, and a name no repository has is not found.
func TestReadingThePracticesWithNoResultListsEachRepositoryByName(t *testing.T) {
	d := practicesDaemon(t, nil, "")
	d.HoldClone(t, "3f2a1b0c4d5e6f708192a3b4c5d6e7f8091a2b3c", "widgets")
	d.HoldClone(t, "9a8b7c6d5e4f30211203f4e5d6c7b8a9fedcba98", "gadgets")
	list, err := d.Client.Practices(t.Context(), "")
	if err != nil || len(list.Repositories) != 2 || list.Repositories[0].Name != "gadgets" || list.Repositories[1].Name != "widgets" {
		t.Fatalf("the read answered %+v (%v), want gadgets and widgets", list, err)
	}
	for _, view := range list.Repositories {
		if view.Result != nil || view.Request != nil {
			t.Errorf("%s reads %+v before any check", view.Name, view)
		}
	}
	for _, named := range []string{"widgets", "3f2a1b0c4d5e6f708192a3b4c5d6e7f8091a2b3c", "3f2a1b0"} {
		one, err := d.Client.Practices(t.Context(), named)
		if err != nil || len(one.Repositories) != 1 || one.Repositories[0].Name != "widgets" {
			t.Errorf("a read of %s answered %+v (%v)", named, one, err)
		}
	}
	_, err = d.Client.Practices(t.Context(), "nothing")
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
}

// The read carries the latest result, each practice and finding with the table's why, fix, template, and the command
// that prints the template.
func TestReadingThePracticesWithAResultCarriesTheTablesWords(t *testing.T) {
	d := practicesDaemon(t, nil, "")
	d.HoldClone(t, "repo-a", "widgets")
	checked := time.Now().UTC().Truncate(time.Millisecond)
	keepResult(t, d, "req-1", practices.Result{
		Repository: "repo-a", Commit: "1111111111111111111111111111111111111111", BaseBranch: "main", Worker: "plan", CheckedAt: checked,
		TableVersion: practices.TableVersion, RulesVersion: ledger.CredentialRulesVersion,
		Practices: []practices.PracticeStatus{{ID: practices.EnvIgnored, Status: practices.StatusFinding}, {ID: practices.Secrets, Status: practices.StatusMet}},
		Findings:  []practices.Finding{{ID: "env-ignored:ignore:.", Practice: practices.EnvIgnored, Subject: "ignore:.", Severity: practices.High, Detail: "`.env` is not ignored"}},
	})
	list, err := d.Client.Practices(t.Context(), "widgets")
	if err != nil || len(list.Repositories) != 1 {
		t.Fatalf("the read answered %+v (%v)", list, err)
	}
	result := list.Repositories[0].Result
	if result == nil || result.Request != "req-1" || result.Worker != "plan" || result.BaseBranch != "main" || !result.CheckedAt.Equal(checked) || len(result.Practices) != 2 || len(result.Findings) != 1 {
		t.Fatalf("the result reads %+v", result)
	}
	finding := result.Findings[0]
	if finding.Why == "" || finding.Fix == "" || finding.Template != "repository/gitignore" || finding.Command != "ropeshift template show repository/gitignore" {
		t.Errorf("the finding reads %+v", finding)
	}
	if secrets := result.Practices[1]; secrets.Status != practices.StatusMet || secrets.Command != "" {
		t.Errorf("secrets reads %+v", secrets)
	}
}

// A request is answered 202 with one entry for each repository: the request offered to the worker that keeps its
// checkout, and the reason no request was made for a repository no connected worker keeps and for one whose worker
// speaks an older protocol.
func TestAMemberRequestsThePracticesCheckAndEachRepositoryAnswersForItself(t *testing.T) {
	d := practicesDaemon(t, map[string][]string{"w-new": {"repo-a"}, "w-old": {"repo-c"}}, "w-new")
	connectWorker(t, d, "w-new", ledger.ProtocolVersion)
	connectWorker(t, d, "w-old", ledger.ProtocolPracticesChecks-1)
	d.HoldClone(t, "repo-a", "widgets")
	d.HoldClone(t, "repo-b", "gadgets")
	d.HoldClone(t, "repo-c", "gizmos")
	status, raw := rawCall(t, d.Socket, d.Session, http.MethodPost, apitest.Route(api.PracticesChecksPath), api.PracticesCheck{Repositories: []string{}})
	if status != http.StatusAccepted {
		t.Fatalf("the request answered %d: %s", status, raw)
	}
	checked, err := d.Client.CheckPractices(t.Context(), nil)
	if err != nil || len(checked.Checks) != 3 {
		t.Fatalf("the request answered %+v (%v), want one entry for each of three repositories", checked, err)
	}
	widgets := entryOf(t, checked, "widgets")
	if widgets.Request == nil || widgets.Request.Worker != "w-new" || widgets.Request.State != ledger.PracticesOffered || widgets.Request.Cause != ledger.PracticesMember || !widgets.Existing {
		t.Errorf("widgets answers %+v, want the request the first call made, found open", widgets)
	}
	if gadgets := entryOf(t, checked, "gadgets"); gadgets.Request != nil || gadgets.Reason != "no connected worker keeps a checkout of gadgets" {
		t.Errorf("gadgets answers %+v", gadgets)
	}
	if gizmos := entryOf(t, checked, "gizmos"); gizmos.Request != nil || gizmos.Reason != "worker w-old speaks protocol 6; upgrade it to run the practices check" {
		t.Errorf("gizmos answers %+v", gizmos)
	}
}

// The repositories a request names are the ones asked, in the order named, each by name, identity, or prefix, and a
// repository no project holds refuses the whole request.
func TestAPracticesRequestNamesTheRepositoriesItAsksOf(t *testing.T) {
	d := practicesDaemon(t, map[string][]string{"w": {"3f2a1b0c4d5e6f708192a3b4c5d6e7f8091a2b3c", "repo-b"}}, "w")
	connectWorker(t, d, "w", ledger.ProtocolVersion)
	d.HoldClone(t, "3f2a1b0c4d5e6f708192a3b4c5d6e7f8091a2b3c", "widgets")
	d.HoldClone(t, "repo-b", "gadgets")
	checked, err := d.Client.CheckPractices(t.Context(), []string{"gadgets", "3f2a1b0"})
	if err != nil || len(checked.Checks) != 2 || checked.Checks[0].Name != "gadgets" || checked.Checks[1].Name != "widgets" ||
		checked.Checks[0].Request == nil || checked.Checks[1].Request == nil || checked.Checks[1].Repository != "3f2a1b0c4d5e6f708192a3b4c5d6e7f8091a2b3c" {
		t.Fatalf("the request answered %+v (%v)", checked, err)
	}
	_, err = d.Client.CheckPractices(t.Context(), []string{"widgets", "nothing"})
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
}

// A request that finds one already open answers it and makes none, so the same request is read twice, and the read of
// the repository shows it open.
func TestAPracticesRequestFindsTheOpenOneAndTheReadShowsIt(t *testing.T) {
	d := practicesDaemon(t, map[string][]string{"w": {"repo-a"}}, "w")
	connectWorker(t, d, "w", ledger.ProtocolVersion)
	d.HoldClone(t, "repo-a", "widgets")
	first, err := d.Client.CheckPractices(t.Context(), []string{"widgets"})
	if err != nil || first.Checks[0].Request == nil || first.Checks[0].Existing {
		t.Fatalf("the first request answered %+v (%v)", first, err)
	}
	second, err := d.Client.CheckPractices(t.Context(), []string{"widgets"})
	if err != nil || second.Checks[0].Request == nil || !second.Checks[0].Existing || second.Checks[0].Request.ID != first.Checks[0].Request.ID {
		t.Fatalf("the second request answered %+v (%v), want the open one", second, err)
	}
	list, err := d.Client.Practices(t.Context(), "widgets")
	if err != nil || list.Repositories[0].Request == nil || list.Repositories[0].Request.ID != first.Checks[0].Request.ID {
		t.Errorf("the read shows %+v (%v), want the open request", list, err)
	}
}

// A request is read by its ID, with its state, and an ID no request has is not found.
func TestAPracticesRequestIsReadByItsID(t *testing.T) {
	d := practicesDaemon(t, map[string][]string{"w": {"repo-a"}}, "w")
	connectWorker(t, d, "w", ledger.ProtocolVersion)
	d.HoldClone(t, "repo-a", "widgets")
	checked, err := d.Client.CheckPractices(t.Context(), nil)
	if err != nil || len(checked.Checks) != 1 || checked.Checks[0].Request == nil {
		t.Fatalf("the request answered %+v (%v)", checked, err)
	}
	id := checked.Checks[0].Request.ID
	got, err := d.Client.PracticesRequest(t.Context(), id)
	if err != nil || got.ID != id || got.State != ledger.PracticesOffered || got.Worker != "w" || got.Repository != "repo-a" {
		t.Errorf("the read answered %+v (%v)", got, err)
	}
	_, err = d.Client.PracticesRequest(t.Context(), "nothing")
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
}

// Asking for a check needs a member's session, as every change does.
func TestRequestingThePracticesCheckNeedsASession(t *testing.T) {
	d := practicesDaemon(t, nil, "")
	anonymous := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer anonymous.Close()
	_, err := anonymous.CheckPractices(t.Context(), nil)
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
}

// openPractices reads the request open for each repository, by name.
func openPractices(t *testing.T, d *apitest.Daemon) map[string]*api.PracticesRequestView {
	t.Helper()
	list, err := d.Client.Practices(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	open := map[string]*api.PracticesRequestView{}
	for _, view := range list.Repositories {
		open[view.Name] = view.Request
	}
	return open
}

// Recording a planning session's work order asks for a check of every active repository, with the cause that says a
// planning session did, and a repository no worker keeps does not stop the session: its work order is recorded all
// the same. Work of any other kind asks for none, and recording the same session again asks for none twice.
func TestStartingAPlanningSessionRequestsACheckOfEveryActiveRepository(t *testing.T) {
	d := practicesDaemon(t, map[string][]string{"w": {"repo-a", "repo-b"}}, "w")
	connectWorker(t, d, "w", ledger.ProtocolVersion)
	d.HoldClone(t, "repo-a", "widgets")
	d.HoldClone(t, "repo-b", "gadgets")
	d.HoldClone(t, "repo-c", "gizmos")
	criteria := []planning.Criterion{{ID: "c1", Text: "A plan is committed"}}
	if _, err := d.Client.CreateWorkOrder(t.Context(), api.CreateWorkOrder{ID: "change-1", Kind: workorders.KindChange, Title: "A change", Statement: "Change it.", Criteria: criteria}); err != nil {
		t.Fatal(err)
	}
	for name, request := range openPractices(t, d) {
		if request != nil {
			t.Fatalf("recording a change asked for a check of %s: %+v", name, request)
		}
	}
	if _, err := d.Client.CreateWorkOrder(t.Context(), api.CreateWorkOrder{ID: "plan-1", Kind: workorders.KindPlan, Title: "Plan it", Statement: "Plan it.", Criteria: criteria}); err != nil {
		t.Fatal(err)
	}
	open := openPractices(t, d)
	for _, name := range []string{"widgets", "gadgets"} {
		if open[name] == nil || open[name].Cause != ledger.PracticesPlanningSession || open[name].State != ledger.PracticesOffered {
			t.Errorf("%s has %+v open, want a request with cause planning_session", name, open[name])
		}
	}
	if open["gizmos"] != nil {
		t.Errorf("gizmos, which no worker keeps, has %+v open", open["gizmos"])
	}
	if open["widgets"] == nil {
		t.Fatal("no request to read")
	}
	first := open["widgets"].ID
	if _, err := d.Client.CreateWorkOrder(t.Context(), api.CreateWorkOrder{ID: "plan-1", Kind: workorders.KindPlan, Title: "Plan it", Statement: "Plan it.", Criteria: criteria}); err != nil {
		t.Fatal(err)
	}
	if again := openPractices(t, d)["widgets"]; again == nil || again.ID != first {
		t.Errorf("recording the session again left %+v open, want the request of %s", again, first)
	}
}

// Starting the session that revises a plan asks for the same checks.
func TestStartingAPlanRevisionSessionRequestsACheckOfEveryActiveRepository(t *testing.T) {
	d := practicesDaemon(t, map[string][]string{apitest.WorldWorker: {"repo-a"}}, apitest.WorldWorker)
	w := apitest.NewWorld(t, d)
	d.HoldClone(t, "repo-a", "widgets")
	plan := approvedPlanWithADecisionOn(t, d, w, retrySource("The first draft."))
	if open := openPractices(t, d)["widgets"]; open != nil {
		t.Fatalf("approving a plan asked for a check: %+v", open)
	}
	if _, err := d.Client.ReviseSession(t.Context(), plan, api.ReviseSession{ID: "revise-1"}); err != nil {
		t.Fatal(err)
	}
	if open := openPractices(t, d)["widgets"]; open == nil || open.Cause != ledger.PracticesPlanningSession {
		t.Errorf("widgets has %+v open, want a request with cause planning_session", open)
	}
}

// A worker's name, which the request names beside the worker's ID so a client says which worker is asked.
func connectNamedWorker(t *testing.T, d *apitest.Daemon, id, name string) {
	t.Helper()
	token, err := d.Client.CreateJoinToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	credential, err := d.Client.Register(t.Context(), ledger.Registration{JoinToken: token, WorkerID: id, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workerOf(t, d, credential.Credential).OpenSession(t.Context(), id, ledger.SessionRequest{Protocol: ledger.ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
}

// Every view of a request names the worker it was asked of by its name as well as its ID.
func TestAPracticesRequestNamesItsWorkerByName(t *testing.T) {
	d := practicesDaemon(t, map[string][]string{"worker-7c41": {"repo-a"}}, "worker-7c41")
	connectNamedWorker(t, d, "worker-7c41", "atlas")
	d.HoldClone(t, "repo-a", "widgets")
	checked, err := d.Client.CheckPractices(t.Context(), nil)
	if err != nil || len(checked.Checks) != 1 || checked.Checks[0].Request == nil {
		t.Fatalf("the request answered %+v (%v)", checked, err)
	}
	request := checked.Checks[0].Request
	if request.Worker != "worker-7c41" || request.WorkerName != "atlas" {
		t.Errorf("the request names %q as %q", request.Worker, request.WorkerName)
	}
	read, err := d.Client.PracticesRequest(t.Context(), request.ID)
	if err != nil || read.WorkerName != "atlas" {
		t.Errorf("the request read by its ID names %q (%v)", read.WorkerName, err)
	}
	list, err := d.Client.Practices(t.Context(), "widgets")
	if err != nil || list.Repositories[0].Request == nil || list.Repositories[0].Request.WorkerName != "atlas" {
		t.Errorf("the open request reads %+v (%v)", list, err)
	}
}

// What the assistant ran by asking for the check is an entry of its conversation, as any call that ran is: the answer
// is 202 with no card, and a 202 with none is a call that ran.
func TestTheAssistantsPracticesCheckIsAnEntryOfItsConversation(t *testing.T) {
	d := startTalk(t)
	record, assistant, _ := d.assistant(t)
	checked, err := assistant.CheckPractices(t.Context(), nil)
	if err != nil || len(checked.Checks) != 1 || checked.Checks[0].Request == nil {
		t.Fatalf("the assistant's request answered %+v (%v)", checked, err)
	}
	var actions []api.TalkEntry
	for _, entry := range d.entries(t, record.ID) {
		if entry.Kind == api.TalkKindAction {
			actions = append(actions, entry)
		}
	}
	if len(actions) != 1 || actions[0].Action.Operation != authority.OpPracticesCheck || actions[0].Action.Outcome != conversations.OutcomeRan || actions[0].Action.Rev == 0 {
		t.Errorf("the conversation holds %+v, want the check as an action that ran", actions)
	}
}

// A delegate's request for the check runs as the delegate at once, with no card: the request and its event name the
// delegate, which is operational.
func TestADelegateRequestsThePracticesCheckAsTheDelegate(t *testing.T) {
	d := practicesDaemon(t, map[string][]string{"w": {"repo-a"}}, "w")
	connectWorker(t, d, "w", ledger.ProtocolVersion)
	d.HoldClone(t, "repo-a", "widgets")
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	checked, err := delegate.CheckPractices(t.Context(), []string{"widgets"})
	if err != nil || len(checked.Checks) != 1 || checked.Checks[0].Request == nil {
		t.Fatalf("the delegate's request answered %+v (%v), want the request, not a card", checked, err)
	}
	request := checked.Checks[0].Request
	if request.RequestedBy != "member-1/"+created.ID || request.Cause != ledger.PracticesMember {
		t.Errorf("the request was made by %q with cause %s", request.RequestedBy, request.Cause)
	}
	err = d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		for _, event := range events {
			if event.Type == ledger.EventPracticesRequested && (event.Actor.Kind != ledger.ActorDelegate || event.Actor.ID != "member-1/"+created.ID) {
				t.Errorf("the event is by %+v, want the delegate", event.Actor)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A prefix of an identity that two repositories share names none of them, and a request or a read that names it is
// refused as invalid, with nothing requested.
func TestAnAmbiguousRepositoryPrefixIsRefusedByAPracticesRequestAndRead(t *testing.T) {
	d := practicesDaemon(t, map[string][]string{"w": {"3f2a1b0c4d5e6f70aaaa", "3f2a1b0c4d5e6f70bbbb"}}, "w")
	connectWorker(t, d, "w", ledger.ProtocolVersion)
	d.HoldClone(t, "3f2a1b0c4d5e6f70aaaa", "widgets")
	d.HoldClone(t, "3f2a1b0c4d5e6f70bbbb", "gadgets")
	_, err := d.Client.CheckPractices(t.Context(), []string{"3f2a1b0c"})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	_, err = d.Client.Practices(t.Context(), "3f2a1b0c")
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	for name, request := range openPractices(t, d) {
		if request != nil {
			t.Errorf("the refused request left %+v open for %s", request, name)
		}
	}
}

// A repository the project removed is not one to check: naming it answers 404, by name or by identity, and a request
// that names it requests nothing for the others either.
func TestNamingARemovedRepositoryToThePracticesRoutesAnswersNotFound(t *testing.T) {
	d := practicesDaemon(t, map[string][]string{"w": {"repo-a", "repo-b"}}, "w")
	connectWorker(t, d, "w", ledger.ProtocolVersion)
	d.HoldClone(t, "repo-a", "widgets")
	d.HoldClone(t, "repo-b", "gadgets")
	if _, err := control.NewRepositories(d.Store).Remove(t.Context(), apitest.Scope, "repo-a", nil, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}); err != nil {
		t.Fatal(err)
	}
	for _, named := range []string{"widgets", "repo-a"} {
		_, err := d.Client.Practices(t.Context(), named)
		refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
		_, err = d.Client.CheckPractices(t.Context(), []string{"gadgets", named})
		refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
	}
	if open := openPractices(t, d); open["gadgets"] != nil {
		t.Errorf("the refused request left %+v open for gadgets", open["gadgets"])
	}
}

// A planning session whose check cannot be requested is recorded all the same, and the server's log hears why.
func TestAPlanningSessionIsRecordedWhenItsPracticesCheckCannotBeRequested(t *testing.T) {
	var warned []string
	d := apitest.StartWith(t, apitest.Options{
		AssistantHosts: func(context.Context, ledger.WorkspaceID) (map[string]control.AssistantHost, error) {
			return nil, errors.New("the workers could not be read")
		},
		Warn: func(message string) { warned = append(warned, message) },
	})
	d.HoldClone(t, "repo-a", "widgets")
	order, err := d.Client.CreateWorkOrder(t.Context(), api.CreateWorkOrder{
		ID: "plan-1", Kind: workorders.KindPlan, Title: "Plan it", Statement: "Plan it.", Criteria: []planning.Criterion{{ID: "c1", Text: "A plan is committed"}},
	})
	if err != nil || order.ID != "plan-1" {
		t.Fatalf("recording the session answered %+v (%v)", order, err)
	}
	if got, err := d.Client.WorkOrder(t.Context(), "plan-1"); err != nil || got.Kind != workorders.KindPlan {
		t.Errorf("the work order reads %+v (%v)", got, err)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "was not requested") || !strings.Contains(warned[0], "the workers could not be read") {
		t.Errorf("the log heard %q, want one line that says the check was not requested and why", warned)
	}
}
