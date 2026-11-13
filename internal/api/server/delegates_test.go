package server_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/roster"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

const aWhy = "The checks passed, and the plan names this step next."

// aDelegate makes a delegate credential for the harness's member, in a project, and returns it with its
// token.
func aDelegate(t *testing.T, client *api.Client, project string) api.DelegateCreated {
	t.Helper()
	created, err := client.CreateDelegate(t.Context(), api.CreateDelegate{Project: project, Label: "planning assistant", ExpiresInMs: 24 * 60 * 60 * 1000})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

// asDelegate is a client on a socket that presents a delegate credential, with a reason for each call.
func asDelegate(t *testing.T, socket, token, project string) *api.ProjectClient {
	t.Helper()
	client := api.NewUnixClient(socket)
	t.Cleanup(client.Close)
	client.UseDelegate(token)
	client.UseWhy(aWhy)
	return client.Project(project)
}

// preparedCard reads the card a call became.
func preparedCard(t *testing.T, err error) authority.Approval {
	t.Helper()
	var prepared *api.Prepared
	if !errors.As(err, &prepared) {
		t.Fatalf("got %v, want a prepared card", err)
	}
	if prepared.Card.Kind != authority.KindConfirm || prepared.Card.State != authority.ApprovalPending {
		t.Fatalf("the card is %+v", prepared.Card)
	}
	return prepared.Card
}

func cardDetail(t *testing.T, card authority.Approval) authority.CardDetail {
	t.Helper()
	detail, err := authority.CardDetailOf(card)
	if err != nil {
		t.Fatal(err)
	}
	return detail
}

func TestADelegateCredentialIsShownOnceAndKeptAsADigest(t *testing.T) {
	d := apitest.Start(t)
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	if created.Token == "" || !strings.HasPrefix(created.ID, authority.DelegateIDPrefix) || created.State != authority.DelegateActive ||
		created.Project != apitest.ProjectName || created.Kind != authority.DelegateExternal || created.LastUsedAt != nil {
		t.Fatalf("created %+v", created)
	}
	list, err := d.Client.Delegates(t.Context())
	if err != nil || len(list.Delegates) != 1 {
		t.Fatalf("list %+v: %v", list, err)
	}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), created.Token) || strings.Contains(string(raw), `"token"`) {
		t.Errorf("the list holds the token: %s", raw)
	}
	sum := sha256.Sum256([]byte(created.Token))
	err = d.Store.Installation(apitest.Scope).Read(t.Context(), func(tx ports.SharedReadTx) error {
		row, err := tx.Members().Delegate(created.ID)
		if err != nil {
			return err
		}
		if row.Digest != hex.EncodeToString(sum[:]) {
			t.Errorf("the record keeps %q", row.Digest)
		}
		events, err := tx.Events(0, 1000)
		for _, event := range events {
			if strings.Contains(string(event.Data), created.Token) || strings.Contains(string(event.Data), row.Digest) {
				t.Errorf("event %s holds the credential", event.Type)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestADelegateIsCheckedWhenMade(t *testing.T) {
	d := apitest.Start(t)
	ctx := t.Context()
	day := int64(24 * 60 * 60 * 1000)
	for name, in := range map[string]api.CreateDelegate{
		"no label":         {Project: apitest.ProjectName, ExpiresInMs: day},
		"a long label":     {Project: apitest.ProjectName, Label: strings.Repeat("x", 65), ExpiresInMs: day},
		"under a minute":   {Project: apitest.ProjectName, Label: "x", ExpiresInMs: 59_999},
		"over thirty days": {Project: apitest.ProjectName, Label: "x", ExpiresInMs: 30*day + 1},
		"no expiry":        {Project: apitest.ProjectName, Label: "x"},
		"no such project":  {Project: "nowhere", Label: "x", ExpiresInMs: day},
		"another kind":     {Project: apitest.ProjectName, Label: "x", ExpiresInMs: day, Kind: "assistant"},
		// The label `assistant` is the assistant's alone, so no outside credential can pass for it.
		"the assistant's label":           {Project: apitest.ProjectName, Label: "assistant", ExpiresInMs: day},
		"the assistant's label, cased":    {Project: apitest.ProjectName, Label: "Assistant ", ExpiresInMs: day},
		"the assistant's label, in space": {Project: apitest.ProjectName, Label: "  ASSISTANT", ExpiresInMs: day},
	} {
		_, err := d.Client.CreateDelegate(ctx, in)
		var refused *api.Error
		if !errors.As(err, &refused) || refused.Status != http.StatusBadRequest || refused.Detail.Code != api.CodeInvalid {
			t.Errorf("%s: got %v", name, err)
		}
	}
	anonymous := api.NewUnixClient(d.Socket)
	defer anonymous.Close()
	_, err := anonymous.CreateDelegate(ctx, api.CreateDelegate{Project: apitest.ProjectName, Label: "x", ExpiresInMs: day})
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
}

func TestARequestCarriesASessionOrADelegateNeverBoth(t *testing.T) {
	d := apitest.Start(t)
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	both := api.NewUnixClient(d.Socket)
	defer both.Close()
	both.UseSession(d.Session)
	both.UseDelegate(created.Token)
	_, err := both.Project(apitest.ProjectName).WorkOrders(t.Context(), "")
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
}

func TestADelegateThatDoesNotActIsRefusedNamingWhy(t *testing.T) {
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	ctx := t.Context()
	unknown := asDelegate(t, d.Socket, "a token nobody made", apitest.ProjectName)
	_, err := unknown.WorkOrders(ctx, "")
	refusedWith(t, err, http.StatusUnauthorized, api.CodeBadDelegate)
	if !strings.Contains(err.Error(), "not known") {
		t.Errorf("an unknown credential: %v", err)
	}
	short, err := d.Client.CreateDelegate(ctx, api.CreateDelegate{Project: apitest.ProjectName, Label: "brief", ExpiresInMs: authority.MinDelegateTTLMs})
	if err != nil {
		t.Fatal(err)
	}
	brief := asDelegate(t, d.Socket, short.Token, apitest.ProjectName)
	if _, err := brief.WorkOrders(ctx, ""); err != nil {
		t.Fatalf("an active credential: %v", err)
	}
	listed, err := d.Client.Delegates(ctx)
	if err != nil || len(listed.Delegates) != 1 || listed.Delegates[0].LastUsedAt == nil {
		t.Fatalf("a used credential lists %+v: %v", listed, err)
	}
	firstUse := *listed.Delegates[0].LastUsedAt
	if _, err := brief.WorkOrders(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if listed, err = d.Client.Delegates(ctx); err != nil || *listed.Delegates[0].LastUsedAt != firstUse {
		t.Errorf("a use within the minute moved the last use: %+v %v", listed, err)
	}
	clock.Advance(61 * time.Second)
	if _, err := brief.WorkOrders(ctx, ""); err == nil {
		t.Fatal("an expired credential read")
	} else if !strings.Contains(err.Error(), "expired") {
		t.Errorf("an expired credential: %v", err)
	}
	long := aDelegate(t, d.Client.Client, apitest.ProjectName)
	acting := asDelegate(t, d.Socket, long.Token, apitest.ProjectName)
	if _, err := acting.WorkOrders(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if listed, err = d.Client.Delegates(ctx); err != nil || *listed.Delegates[1].LastUsedAt <= firstUse {
		t.Errorf("a later use did not move the last use: %+v %v", listed, err)
	}
	if _, err := d.Client.RevokeDelegate(ctx, long.ID); err != nil {
		t.Fatal(err)
	}
	_, err = acting.WorkOrders(ctx, "")
	refusedWith(t, err, http.StatusUnauthorized, api.CodeBadDelegate)
	if !strings.Contains(err.Error(), "revoked") {
		t.Errorf("a revoked credential: %v", err)
	}
}

func TestADelegateReachesOnlyItsOwnProject(t *testing.T) {
	in := apitest.StartInstallation(t, "alpha", "beta")
	ctx := t.Context()
	created := aDelegate(t, in.Client.Client, "alpha")
	alpha := asDelegate(t, in.Socket, created.Token, "alpha")
	if _, err := alpha.WorkOrders(ctx, ""); err != nil {
		t.Fatalf("its own project: %v", err)
	}
	beta := asDelegate(t, in.Socket, created.Token, "beta")
	_, err := beta.WorkOrders(ctx, "")
	refusedWith(t, err, http.StatusForbidden, api.CodeOutsideProject)
	_, err = beta.CreateWorkOrder(ctx, api.CreateWorkOrder{ID: "w1", Title: "Elsewhere"})
	refusedWith(t, err, http.StatusForbidden, api.CodeOutsideProject)
	for _, read := range []string{"/v1/members", "/v1/health", "/v1/projects", "/v1/needs-you", "/v1/audit", "/v1/workers", "/v1/projects/alpha"} {
		err := alpha.Do(ctx, http.MethodGet, read, nil, nil)
		refusedWith(t, err, http.StatusForbidden, api.CodeOutsideProject)
	}
	for _, change := range []struct{ method, path string }{
		{http.MethodPost, "/v1/projects"}, {http.MethodPost, "/v1/projects/alpha/archive"}, {http.MethodPost, "/v1/workers/tokens"},
		{http.MethodPut, "/v1/workers/w1/orchestrator"}, {http.MethodPost, "/v1/roster/changes"},
	} {
		err := alpha.Do(ctx, change.method, change.path, struct{}{}, nil)
		refusedWith(t, err, http.StatusForbidden, api.CodeOutsideProject)
	}
	for _, change := range []struct{ method, path string }{
		{http.MethodGet, "/v1/delegates"}, {http.MethodPost, "/v1/delegates"}, {http.MethodPost, "/v1/delegates/" + created.ID + "/revoke"},
		{http.MethodPost, "/v1/delegates/" + created.ID + "/remove"}, {http.MethodPost, "/v1/sessions/end"}, {http.MethodPost, "/v1/sessions/end-all"},
		{http.MethodPut, "/v1/passphrase"}, {http.MethodPost, "/v1/installation/backup"}, {http.MethodPost, api.InstallationBackupDownloadPath},
	} {
		err := alpha.Do(ctx, change.method, change.path, struct{}{}, nil)
		refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
	}
	listed, err := in.Client.Delegates(ctx)
	if err != nil || len(listed.Delegates) != 1 || listed.Delegates[0].State != authority.DelegateActive {
		t.Errorf("a refused delegate changed its own record: %+v %v", listed, err)
	}
}

func TestADelegateUsesOnlyTheSocket(t *testing.T) {
	d := apitest.Start(t)
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	web := d.ServeWeb(t, nil, 0)
	for _, path := range []string{"/v1/projects", apitest.Route("/v1/work-orders"), "/"} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, web.URL(path), nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set(api.HeaderDelegate, created.Token)
		response, err := web.HTTP.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		var body api.ErrorBody
		if err := json.Unmarshal(raw, &body); err != nil || response.StatusCode != http.StatusForbidden || body.Error.Code != api.CodeForbidden ||
			body.Error.Message != "A delegate uses the local socket, never the web listener." {
			t.Errorf("%s answered %d %s", path, response.StatusCode, raw)
		}
	}
}

// TestEveryRouteTakesItsClassForADelegate calls every changing route of a fixed class as a delegate: a
// member-only route refuses, a consent route records a card and runs nothing, and an operational route
// runs, whatever its own handler then says.
func TestEveryRouteTakesItsClassForADelegate(t *testing.T) {
	d := apitest.Start(t)
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	if _, err := delegate.WorkOrders(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.ChangeRoster(t.Context(), roster.Change{Op: roster.OpDisable, Agent: "lookup"}); err != nil {
		t.Fatal(err)
	}
	// A repository route finds its record before it says what the call would do.
	detected(t, d, "w1", "main")
	// bodies are what a route needs before it can say what a card would change.
	bodies := map[string]any{
		"POST /v1/roster/changes": api.RosterChange{Scope: roster.ScopeProject, Change: roster.Change{Op: roster.OpEnable, Agent: "lookup"}},
		"POST /v1/roster/revert":  api.RosterRevert{Scope: roster.ScopeProject, Version: 1},
	}
	checked := 0
	for pattern, route := range server.RouteOperations() {
		if route.Resolved {
			continue
		}
		op, ok := authority.OperationOf(route.IDs[0])
		if !ok {
			t.Errorf("%s names %s, which the class table lacks", pattern, route.IDs[0])
			continue
		}
		method, path, _ := strings.Cut(pattern, " ")
		concrete := strings.NewReplacer("{id}", "w1", "{claim}", "c1", "{project}", apitest.ProjectName).Replace(path)
		target := reached(concrete)
		inProject := target != concrete
		before := revisionOf(t, d)
		var body any = struct{}{}
		if given, ok := bodies[pattern]; ok {
			body = given
		}
		err := delegate.Do(t.Context(), method, target, body, nil)
		checked++
		var refused *api.Error
		var prepared *api.Prepared
		switch {
		case !inProject && op.Class == authority.ClassMemberOnly:
			refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
		case !inProject:
			refusedWith(t, err, http.StatusForbidden, api.CodeOutsideProject)
		case op.Class == authority.ClassMemberOnly:
			refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
		case op.Class == authority.ClassConsent:
			if !errors.As(err, &prepared) {
				t.Errorf("%s (%s) answered %v, want a card", pattern, op.ID, err)
				continue
			}
			if detail := cardDetail(t, prepared.Card); detail.Operation != op.ID || detail.Method != method || detail.Why != aWhy {
				t.Errorf("%s made a card of %+v", pattern, detail)
			}
		case errors.As(err, &prepared):
			t.Errorf("%s (%s, operational) made a card", pattern, op.ID)
		case errors.As(err, &refused) && (refused.Detail.Code == api.CodeMemberOnly || refused.Detail.Code == api.CodeOutsideProject ||
			refused.Detail.Code == api.CodeBadDelegate || refused.Detail.Code == api.CodeSessionRequired):
			t.Errorf("%s (%s, operational) refused the delegate: %v", pattern, op.ID, err)
		}
		if op.Class != authority.ClassOperational && revisionOf(t, d) > before+1 {
			t.Errorf("%s (%s) changed more than its card", pattern, op.ID)
		}
	}
	if checked < 50 {
		t.Fatalf("checked %d routes", checked)
	}
}

func TestAConsentCallRunsNothingAndPreparesACard(t *testing.T) {
	d := apitest.Start(t)
	createReady(t, d, "w1")
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	ctx := t.Context()
	_, err := delegate.CancelWork(ctx, "w1", api.CancelWork{Reason: "The plan dropped it."})
	card := preparedCard(t, err)
	if row, err := d.Client.WorkOrder(ctx, "w1"); err != nil || row.State == workorders.Canceled {
		t.Fatalf("a card ran: %+v %v", row, err)
	}
	detail := cardDetail(t, card)
	if card.Subject != "work_order:w1" || card.Summary != "Cancel work: w1" || card.RequestedBy != "delegate:"+memberOf(t, d)+"/"+created.ID ||
		card.ExpiresAt != 0 || card.Lane != "" || !strings.HasPrefix(card.SubjectDigest, "sha256:") {
		t.Errorf("the card is %+v", card)
	}
	if detail.Operation != authority.OpWorkOrderCancel || detail.Label != "Cancel work" || detail.Method != http.MethodPost ||
		detail.Path != "/v1/projects/default/work-orders/w1/cancel" || detail.Why != aWhy || !strings.Contains(string(detail.Arguments), "The plan dropped it.") {
		t.Errorf("the detail is %+v", detail)
	}
	var changes []authority.ArgumentChange
	if err := json.Unmarshal(detail.Changes, &changes); err != nil || len(changes) != 1 || changes[0].Field != "reason" || changes[0].To != "The plan dropped it." {
		t.Errorf("the changes are %s: %v", detail.Changes, err)
	}
	quiet := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	quiet.UseWhy("")
	_, err = quiet.CancelWork(ctx, "w1", api.CancelWork{Reason: "Again."})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	if !strings.Contains(err.Error(), "A card needs the reason the delegate proposes it.") {
		t.Errorf("no reason: %v", err)
	}
	quiet.UseWhy(strings.Repeat("x", authority.MaxWhy+1))
	_, err = quiet.CancelWork(ctx, "w1", api.CancelWork{Reason: "Again."})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
}

func memberOf(t *testing.T, d *apitest.Daemon) string {
	t.Helper()
	list, err := d.Client.Members(t.Context())
	if err != nil || len(list.Members) == 0 {
		t.Fatalf("members %+v: %v", list, err)
	}
	return list.Members[0].ID
}

func TestConfirmingRunsTheCardsExactCallAsTheMember(t *testing.T) {
	d := apitest.Start(t)
	createReady(t, d, "w1")
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	ctx := t.Context()
	_, err := delegate.CancelWork(ctx, "w1", api.CancelWork{Reason: "The plan dropped it."})
	card := preparedCard(t, err)
	_, err = delegate.DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: true})
	refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
	other := aDelegate(t, d.Client.Client, apitest.ProjectName)
	_, err = asDelegate(t, d.Socket, other.Token, apitest.ProjectName).DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: true})
	refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
	if row, err := d.Client.Approval(ctx, card.ID); err != nil || row.State != authority.ApprovalPending {
		t.Fatalf("a delegate's word moved the card: %+v %v", row, err)
	}
	confirmed, err := d.Client.DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: true})
	if err != nil {
		t.Fatal(err)
	}
	member := memberOf(t, d)
	if confirmed.State != authority.ApprovalUsed || confirmed.DecidedBy != "member:"+member || confirmed.Channel != authority.ChannelCLI || confirmed.DecidedAt == 0 {
		t.Errorf("the confirmed card is %+v", confirmed)
	}
	row, err := d.Client.WorkOrder(ctx, "w1")
	if err != nil || row.State != workorders.Canceled {
		t.Fatalf("the confirmed call left %+v: %v", row, err)
	}
	var canceledBy ledger.Actor
	err = d.Store.Read(ctx, apitest.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: workorders.SubjectKind, ID: "w1"})
		for _, event := range events {
			if strings.Contains(event.Type, "cancel") {
				canceledBy = event.Actor
			}
		}
		return err
	})
	if err != nil || canceledBy != (ledger.Actor{Kind: ledger.ActorMember, ID: member}) {
		t.Errorf("the cancel was by %+v: %v", canceledBy, err)
	}
	again, err := d.Client.DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: true})
	if err != nil || again.State != authority.ApprovalUsed {
		t.Errorf("a repeated confirmation: %+v %v", again, err)
	}
}

func TestDecliningACardRunsNothing(t *testing.T) {
	d := apitest.Start(t)
	createReady(t, d, "w1")
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	ctx := t.Context()
	_, err := asDelegate(t, d.Socket, created.Token, apitest.ProjectName).CancelWork(ctx, "w1", api.CancelWork{Reason: "Dropped."})
	card := preparedCard(t, err)
	declined, err := d.Client.DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: false, Reason: "Keep it."})
	if err != nil || declined.State != authority.ApprovalDenied || declined.Reason != "Keep it." {
		t.Fatalf("declined %+v: %v", declined, err)
	}
	if row, err := d.Client.WorkOrder(ctx, "w1"); err != nil || row.State == workorders.Canceled {
		t.Errorf("a declined card ran: %+v %v", row, err)
	}
}

func TestACardWhoseSubjectMovedGoesVoid(t *testing.T) {
	d := apitest.Start(t)
	ctx := t.Context()
	if _, err := d.Client.RecordQuestion(ctx, api.RecordQuestion{ID: "q1", Text: "What is the retry budget?"}); err != nil {
		t.Fatal(err)
	}
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	err := asDelegate(t, d.Socket, created.Token, apitest.ProjectName).Do(ctx, http.MethodPost, apitest.Route("/v1/questions/q1/dismiss"), struct{}{}, nil)
	card := preparedCard(t, err)
	if _, err := d.Client.AnswerQuestion(ctx, "q1", api.AnswerQuestion{ID: "d1", Answer: "Two retries."}); err != nil {
		t.Fatal(err)
	}
	_, err = d.Client.DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: true})
	refusedWith(t, err, http.StatusConflict, api.CodeStaleDigest)
	row, err := d.Client.Approval(ctx, card.ID)
	if err != nil || row.State != authority.ApprovalVoid || row.DecidedBy != "" {
		t.Fatalf("the card is %+v: %v", row, err)
	}
	if question, err := d.Client.Question(ctx, "q1"); err != nil || question.Status != questions.StatusAnswered {
		t.Errorf("a void card ran: %+v %v", question, err)
	}
}

func TestACardTheAPIRefusesFails(t *testing.T) {
	d := apitest.Start(t)
	createReady(t, d, "w1")
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	ctx := t.Context()
	_, err := asDelegate(t, d.Socket, created.Token, apitest.ProjectName).Accept(ctx, "w1")
	card := preparedCard(t, err)
	failed, err := d.Client.DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: true})
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != authority.ApprovalFailed || !strings.HasPrefix(failed.Reason, api.CodeIllegalMove+":") {
		t.Errorf("the card is %+v", failed)
	}
}

func TestTheClassOfACallTurnsOnTheCall(t *testing.T) {
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	ctx := t.Context()
	cardFor := func(name string, err error, operation string) {
		t.Helper()
		var prepared *api.Prepared
		if !errors.As(err, &prepared) {
			t.Errorf("%s: got %v, want a card", name, err)
			return
		}
		if detail := cardDetail(t, prepared.Card); detail.Operation != operation {
			t.Errorf("%s: a card of %s, want %s", name, detail.Operation, operation)
		}
	}
	ran := func(name string, err error) {
		t.Helper()
		var prepared *api.Prepared
		var refused *api.Error
		if errors.As(err, &prepared) || (errors.As(err, &refused) && (refused.Detail.Code == api.CodeMemberOnly || refused.Detail.Code == api.CodeBadDelegate)) {
			t.Errorf("%s: got %v, want the call to run", name, err)
		}
	}

	// A review: planning work is operational, and any other is consent.
	createReady(t, d, "w1")
	if _, err := control.NewWorkOrders(d.Store).Create(ctx, apitest.Scope, workorders.WorkOrder{ID: "p1", Kind: workorders.KindPlan, Title: "Plan the release"}, ledger.Actor{Kind: ledger.ActorMember, ID: "m"}); err != nil {
		t.Fatal(err)
	}
	_, err := delegate.Review(ctx, "w1")
	cardFor("a review", err, authority.OpWorkOrderReview)
	_, err = delegate.Review(ctx, "p1")
	ran("a planning review", err)

	// A retry: with no attempt left it is consent, and with one left it is operational.
	w.Running("w2", "Try the flaky call").End(workorders.OutcomeFailed, workorders.ReasonCrashed)
	_, err = delegate.Retry(ctx, "w2", api.Retry{Reason: "The network came back."})
	cardFor("a retry with no attempt left", err, authority.OpWorkOrderRetryExtra)
	if _, err := d.Client.Retry(ctx, "w2", api.Retry{Reason: "The network came back."}); err != nil {
		t.Fatal(err)
	}
	_, err = delegate.Retry(ctx, "w2", api.Retry{Reason: "Again."})
	refusedWith(t, err, http.StatusConflict, api.CodeIllegalMove)

	// A fleet: pausing is operational, and running is consent.
	if _, err := delegate.SetFleetMode(ctx, api.SetFleetMode{Mode: ledger.FleetPause}); err != nil {
		t.Errorf("a pause: %v", err)
	}
	if _, err := d.Client.SetFleetMode(ctx, api.SetFleetMode{Mode: ledger.FleetRun}); err != nil {
		t.Fatal(err)
	}
	_, err = delegate.SetFleetMode(ctx, api.SetFleetMode{Mode: ledger.FleetRun})
	cardFor("a run", err, authority.OpFleetRun)
	_, err = delegate.SetFleetCaps(ctx, ledger.Caps{WIPLimit: 9, RepositoryLimit: 2, WorkerLimit: 3})
	caps := preparedCard(t, err)
	var capsChanged []authority.FieldChange
	if err := json.Unmarshal(cardDetail(t, caps).Changes, &capsChanged); err != nil || len(capsChanged) != 3 || capsChanged[2].Field != "wip_limit" || capsChanged[2].To != float64(9) {
		t.Errorf("the caps change %s: %v", cardDetail(t, caps).Changes, err)
	}

	// A repository: its base, lowering merge authority, and review on the branch are operational; raising
	// merge authority and review on a pull request are never exempt; a forge binding is consent.
	detected(t, d, aRepository, "main")
	base, raised, lowered := "trunk", ledger.ReviewApproves, ledger.PersonMerges
	branch, pullRequest, binding := ledger.ReviewOnBranch, ledger.ReviewOnPullRequest, "github.com/acme/app"
	if _, err := delegate.SetRepository(ctx, aRepository, api.RepositoryChange{Base: &base}); err != nil {
		t.Errorf("a base: %v", err)
	}
	_, err = delegate.SetRepository(ctx, aRepository, api.RepositoryChange{MergeAuthority: &raised})
	raise := preparedCard(t, err)
	var raiseChanged []authority.FieldChange
	if err := json.Unmarshal(cardDetail(t, raise).Changes, &raiseChanged); err != nil || len(raiseChanged) != 1 || raiseChanged[0].From != string(ledger.PersonMerges) {
		t.Errorf("the raise changes %s: %v", cardDetail(t, raise).Changes, err)
	}
	if cardDetail(t, raise).Operation != authority.OpRepositoryRaiseMergeAuthority {
		t.Errorf("a raise is a card of %s", cardDetail(t, raise).Operation)
	}
	_, err = delegate.SetRepository(ctx, aRepository, api.RepositoryChange{ReviewTarget: &pullRequest, ForgeBinding: &binding})
	cardFor("review on a pull request", err, authority.OpRepositoryReviewTargetPullRequest)
	_, err = delegate.SetRepository(ctx, aRepository, api.RepositoryChange{ForgeBinding: &binding})
	cardFor("a forge binding", err, authority.OpRepositoryForge)
	if _, err := delegate.SetRepository(ctx, aRepository, api.RepositoryChange{ReviewTarget: &branch}); err != nil {
		t.Errorf("review on the branch: %v", err)
	}
	approves := ledger.PersonApproves
	if _, err := d.Client.SetRepository(ctx, aRepository, api.RepositoryChange{MergeAuthority: &approves}); err != nil {
		t.Fatal(err)
	}
	if _, err := delegate.SetRepository(ctx, aRepository, api.RepositoryChange{MergeAuthority: &lowered}); err != nil {
		t.Errorf("lowering merge authority: %v", err)
	}

	// A request: one the delegation policy covers is operational, and any other is consent.
	lane := w.Running("w3", "Run the tests")
	outside := lane.Approval("a1", `{"command":"git tag v1"}`)
	inside := lane.Approval("a2", `{"command":"go test ./..."}`)
	if _, err := d.Client.PublishPolicy(ctx, "[delegation_policy]\nversion = 2\n\n[[delegation_policy.classes]]\ntool = \"Bash\"\ncommand_prefix = \"go test\"\n"); err != nil {
		t.Fatal(err)
	}
	_, err = delegate.DecideApproval(ctx, outside.ID, api.DecideApproval{Digest: outside.SubjectDigest, Allow: true})
	cardFor("a request outside the delegation policy", err, authority.OpApprovalDecide)
	decided, err := delegate.DecideApproval(ctx, inside.ID, api.DecideApproval{Digest: inside.SubjectDigest, Allow: true})
	if err != nil || decided.DecidedKind != string(ledger.ActorDelegate) || decided.DecidedBy != "delegate:"+memberOf(t, d)+"/"+created.ID {
		t.Errorf("a covered request: %+v %v", decided, err)
	}

	// A decision: one an orchestrator could record is operational, and any other is consent.
	within := api.RecordDecision{
		ID: "d1", Status: questions.DecisionDecided, Statement: "Retry twice.", Rationale: "The budget allows it.", Scope: "retries",
		Reversibility: questions.Reversible, Citations: []string{"docs/retries.md"}, Categories: []string{questions.CategoryProduct},
	}
	if _, err := delegate.RecordDecision(ctx, within); err != nil {
		t.Errorf("a decision within policy: %v", err)
	}
	unbound := api.RecordDecision{ID: "d2", Status: questions.DecisionDecided, Statement: "Retry three times."}
	_, err = delegate.RecordDecision(ctx, unbound)
	cardFor("a decision outside policy", err, authority.OpDecisionRecord)
}

func TestTheDelegatePolicyExemptsOnlyWhatItMay(t *testing.T) {
	d := apitest.Start(t)
	createReady(t, d, "w1")
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	ctx := t.Context()
	_, err := d.Client.PublishPolicy(ctx, "[delegate_policy]\nversion = 2\nexempt = [\"work_order.cancel\", \"work_order.create\", \"roster.change\", \"delegate.create\", \"work_order.teleport\"]\n")
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	for _, id := range []string{"work_order.create", "roster.change", "delegate.create", "work_order.teleport"} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("the refusal does not name %s: %v", id, err)
		}
	}
	_, err = delegate.PublishPolicy(ctx, "[delegate_policy]\nversion = 2\nexempt = [\"work_order.cancel\"]\n")
	refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
	builtIn, err := d.Client.PoliciesOf(ctx, authority.PolicyDelegate)
	if err != nil || len(builtIn) != 1 || builtIn[0].Version != 1 || !strings.Contains(builtIn[0].Body, "exempt = []") {
		t.Fatalf("the built-in delegate policy reads %+v: %v", builtIn, err)
	}
	version, err := d.Client.PublishPolicy(ctx, "[delegate_policy]\nversion = 2\nexempt = [\"work_order.cancel\", \"question.answer\"]\n")
	if err != nil || version.Kind != authority.PolicyDelegate || version.Version != 2 {
		t.Fatalf("published %+v: %v", version, err)
	}
	if _, err := delegate.CancelWork(ctx, "w1", api.CancelWork{Reason: "The plan dropped it."}); err != nil {
		t.Fatalf("an exempt cancel: %v", err)
	}
	var cancel, exempted ledger.Event
	err = d.Store.Read(ctx, apitest.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: workorders.SubjectKind, ID: "w1"})
		for _, event := range events {
			switch {
			case event.Type == control.EventDelegateExempted:
				exempted = event
			case strings.Contains(event.Type, "cancel"):
				cancel = event
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := authority.DelegateActor(memberOf(t, d), created.ID)
	if cancel.Actor != actor || exempted.Actor != actor || !strings.Contains(string(exempted.Data), `"policy_version":2`) ||
		!strings.Contains(string(exempted.Data), authority.OpWorkOrderCancel) {
		t.Errorf("the cancel is by %+v, and the exemption is %+v %s", cancel.Actor, exempted.Actor, exempted.Data)
	}
	if _, err := d.Client.PublishPolicy(ctx, "[decision_policy]\nversion = 2\nreversible_default = \"assume\"\none_way_categories = [\"security\"]\nmax_assumptions_per_work_order = 3\n"); err != nil {
		t.Fatal(err)
	}
	oneWay, err := d.Client.RecordQuestion(ctx, api.RecordQuestion{ID: "q1", Text: "May audit data leave the region?", Categories: []string{questions.CategorySecurity}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = delegate.AnswerQuestion(ctx, oneWay.ID, api.AnswerQuestion{ID: "d1", Answer: "Never."})
	if detail := cardDetail(t, preparedCard(t, err)); detail.Operation != authority.OpQuestionAnswer {
		t.Errorf("a one-way answer made a card of %s", detail.Operation)
	}
	reversible, err := d.Client.RecordQuestion(ctx, api.RecordQuestion{ID: "q2", Text: "How many retries?", Categories: []string{questions.CategoryProduct}})
	if err != nil {
		t.Fatal(err)
	}
	answered, err := delegate.AnswerQuestion(ctx, reversible.ID, api.AnswerQuestion{ID: "d2", Answer: "Two."})
	if err != nil || answered.QuestionID != "q2" {
		t.Errorf("an exempt answer outside a one-way category: %+v %v", answered, err)
	}
}

func TestSigningOutEverywhereEndsEverySessionAndDelegate(t *testing.T) {
	d := apitest.Start(t)
	ctx := t.Context()
	second := api.NewUnixClient(d.Socket)
	defer second.Close()
	if _, err := apitest.SignIn(ctx, second); err != nil {
		t.Fatal(err)
	}
	first, other := aDelegate(t, d.Client.Client, apitest.ProjectName), aDelegate(t, d.Client.Client, apitest.ProjectName)
	if _, err := d.Client.RevokeDelegate(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	var before int
	err := d.Store.Installation(apitest.Scope).Read(ctx, func(tx ports.SharedReadTx) error {
		events, err := tx.Events(0, 10_000)
		before = len(events)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	ended, err := second.SignOutAll(ctx)
	if err != nil || ended.Sessions != 2 || len(ended.Delegates) != 1 || ended.Delegates[0] != first.ID {
		t.Fatalf("signed out %+v: %v", ended, err)
	}
	_, err = d.Client.CreateWorkOrder(ctx, api.CreateWorkOrder{ID: "w1", Title: "After"})
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
	_, err = asDelegate(t, d.Socket, first.Token, apitest.ProjectName).WorkOrders(ctx, "")
	refusedWith(t, err, http.StatusUnauthorized, api.CodeBadDelegate)
	err = d.Store.Installation(apitest.Scope).Read(ctx, func(tx ports.SharedReadTx) error {
		events, err := tx.Events(0, 10_000)
		if len(events) != before+1 || events[len(events)-1].Type != control.EventSignedOutEverywhere {
			t.Errorf("signing out everywhere left %d events", len(events)-before)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTheAuditNamesADelegateByItsLabelAndMember(t *testing.T) {
	d := apitest.Start(t)
	ctx := t.Context()
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	if _, err := delegate.CreateWorkOrder(ctx, api.CreateWorkOrder{ID: "w1", Title: "Add the retry budget", Statement: "Retry twice."}); err != nil {
		t.Fatal(err)
	}
	member := memberOf(t, d)
	byDelegate := func() api.AuditEvent {
		t.Helper()
		page, err := d.Client.Audit(ctx, api.AuditQuery{Actor: string(ledger.ActorDelegate)})
		if err != nil || len(page.Events) == 0 {
			t.Fatalf("the audit %+v: %v", page, err)
		}
		return page.Events[0]
	}
	if event := byDelegate(); event.ActorFor != member || event.ActorLabel != "planning assistant" || event.ActorID != member+"/"+created.ID {
		t.Errorf("a delegate's event reads %+v", event.StreamEvent)
	}
	if _, err := d.Client.RevokeDelegate(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.Client.RemoveDelegate(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if event := byDelegate(); event.ActorLabel != created.ID || event.ActorFor != member {
		t.Errorf("a removed delegate's event reads %+v", event.StreamEvent)
	}
	page, err := d.Client.Audit(ctx, api.AuditQuery{Actor: string(ledger.ActorMember)})
	if err != nil || len(page.Events) == 0 || page.Events[0].ActorFor != "" || page.Events[0].ActorLabel != "" {
		t.Errorf("a member's event reads %+v: %v", page, err)
	}
}

func TestNeedsYouListsEveryOpenCard(t *testing.T) {
	d := apitest.Start(t)
	createReady(t, d, "w1")
	ctx := t.Context()
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	_, err := asDelegate(t, d.Socket, created.Token, apitest.ProjectName).CancelWork(ctx, "w1", api.CancelWork{Reason: "Dropped."})
	card := preparedCard(t, err)
	view, err := d.Client.NeedsYou(ctx)
	if err != nil {
		t.Fatal(err)
	}
	item := needOf(t, view, api.NeedConfirm)
	if item.ID != card.ID || item.Card == nil || item.Card.Label != "planning assistant" || item.Card.Member != memberOf(t, d) ||
		item.Card.Detail.Operation != authority.OpWorkOrderCancel || item.Project == nil || item.Project.Name != apitest.ProjectName {
		t.Errorf("the item is %+v", item)
	}
	if _, err := d.Client.DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: false}); err != nil {
		t.Fatal(err)
	}
	if view, err = d.Client.NeedsYou(ctx); err != nil {
		t.Fatal(err)
	}
	for _, item := range view.Items {
		if item.Kind == api.NeedConfirm {
			t.Errorf("a declined card still waits: %+v", item)
		}
	}
}

func TestTheOperationsRouteReadsTheClassTable(t *testing.T) {
	d := apitest.Start(t)
	list, err := d.Client.Operations(t.Context())
	if err != nil || len(list.Operations) != len(authority.Operations()) {
		t.Fatalf("operations %d: %v", len(list.Operations), err)
	}
	raw, err := json.Marshal(list.Operations[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"id"`, `"label"`, `"class"`, `"exemptable"`} {
		if !bytes.Contains(raw, []byte(field)) {
			t.Errorf("an operation reads %s", raw)
		}
	}
	if bytes.Contains(raw, []byte("actions")) {
		t.Errorf("an operation shows what it checks: %s", raw)
	}
}

// TestACardConfirmedOnTheWebSaysSo holds the channel a confirmation records: a click on the web listener
// is `web`, and the call it runs is that session's member's.
func TestACardConfirmedOnTheWebSaysSo(t *testing.T) {
	d := apitest.Start(t)
	createReady(t, d, "w1")
	ctx := t.Context()
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	_, err := asDelegate(t, d.Socket, created.Token, apitest.ProjectName).CancelWork(ctx, "w1", api.CancelWork{Reason: "Dropped."})
	card := preparedCard(t, err)
	r := webOn(t, d)
	cookies, csrf := r.signedIn(t)
	response, raw := r.send(t, call{
		method: http.MethodPost, path: apitest.Route("/v1/approvals/" + card.ID + "/decide"), cookies: cookies,
		body: api.DecideApproval{Digest: card.SubjectDigest, Allow: true}, headers: map[string]string{api.HeaderCSRF: csrf},
	})
	var confirmed authority.Approval
	if response.StatusCode != http.StatusOK || json.Unmarshal(raw, &confirmed) != nil {
		t.Fatalf("the web confirmation answered %d %s", response.StatusCode, raw)
	}
	if confirmed.State != authority.ApprovalUsed || confirmed.Channel != authority.ChannelWeb || confirmed.DecidedBy != "member:"+memberOf(t, d) {
		t.Errorf("the card is %+v", confirmed)
	}
	if row, err := d.Client.WorkOrder(ctx, "w1"); err != nil || row.State != workorders.Canceled {
		t.Errorf("the confirmed call left %+v: %v", row, err)
	}
}

// An outside delegate's events and cards name its kind, which a surface decides the assistant's form on, never
// its label: the audit row of an outside delegate reads `external`, whatever it is called.
func TestAnAuditRowNamesTheKindOfItsDelegate(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	outside := asDelegate(t, d.socket, aDelegate(t, d.Client.Client, apitest.ProjectName).Token, apitest.ProjectName)
	createReady(t, d.Daemon, "wo-ext")
	_, err := outside.CancelWork(ctx, "wo-ext", api.CancelWork{Reason: "Not needed."})
	card := preparedCard(t, err)
	_, inside, _ := d.assistant(t)
	createReady(t, d.Daemon, "wo-int")
	_, err = inside.CancelWork(ctx, "wo-int", api.CancelWork{Reason: "Not needed."})
	preparedCard(t, err)
	page, err := d.client.Audit(ctx, api.AuditQuery{Actor: "delegate", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, event := range page.Events {
		if event.ActorLabel == "planning assistant" && event.DelegateKind != authority.DelegateExternal {
			t.Errorf("an outside delegate's event reads as kind %q", event.DelegateKind)
		}
		kinds[event.DelegateKind] = true
	}
	if !kinds[authority.DelegateExternal] || !kinds[authority.DelegateAssistant] {
		t.Errorf("the audit names kinds %v, want external and assistant", kinds)
	}
	var needs api.NeedsYou
	if err := d.client.ReadView(ctx, api.ViewNeedsYou, "", &needs); err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, item := range needs.Items {
		if item.Card != nil {
			seen[item.Card.Label] = item.Card.DelegateKind
		}
	}
	if seen["planning assistant"] != authority.DelegateExternal || seen["assistant"] != authority.DelegateAssistant {
		t.Errorf("the cards name kinds %v (card %s)", seen, card.ID)
	}
}
