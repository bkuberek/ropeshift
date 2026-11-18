package contract

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
)

// PracticesChecks are what the ledger keeps of the practices check (FR-PRAC-1): the requests that a worker run it,
// each repository's latest result, and the commit its last completed secret scan read.
var PracticesChecks = []Check{
	{"PRQ1", "a request is read back by its ID, by its worker while it is offered, and by its repository while it is open", checkPracticesRequestsAreRead},
	{"PRQ2", "the first report of a request is the one recorded, whether the request was offered or stored as expired", checkPracticesFirstReportWins},
	{"PRQ3", "expiring stores as expired the offered requests whose expiry passed, and only those", checkPracticesExpiry},
	{"PRQ4", "a repository keeps its latest result, whole, and a later save replaces it", checkPracticesLatestResult},
	{"PRQ5", "a repository's scan tip is set, replaced, and cleared on its own", checkPracticesScanTip},
	{"PRQ6", "registering clones requests one check of each repository registered, as a registration, and a repository no worker keeps, a known one, and a dry run request none", checkRegistrationRequestsACheckOfEachRepository},
	{"PRQ7", "registering from a manifest requests one check of each repository it registers", checkManifestRegistrationRequestsACheckOfEachRepository},
}

// practicesWrite runs one write transaction on the practices records, with the event any committed transaction needs.
func practicesWrite(r *Rig, s ports.Store, fn func(ports.PracticesWriter) error) error {
	return s.Write(r.Ctx, r.Scope, "practices.contract", func(tx ports.WriteTx) error {
		if err := fn(tx.Practices()); err != nil {
			return err
		}
		_, err := tx.Append(r.noteDraft(r.ID("note")))
		return err
	})
}

func practicesRead(r *Rig, s ports.Store, fn func(ports.PracticesReader) error) error {
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error { return fn(tx.Practices()) })
}

func aPracticesRequest(id, repository, worker string, requestedAt, expiresAt int64) ledger.PracticesRequest {
	return ledger.PracticesRequest{
		ID: id, Repository: repository, Worker: worker, Cause: ledger.PracticesMember, RequestedBy: "member-1",
		RequestedAt: requestedAt, ExpiresAt: expiresAt, State: ledger.PracticesOffered,
	}
}

func idsOfPracticesRequests(requests []ledger.PracticesRequest) []string {
	ids := make([]string, 0, len(requests))
	for _, request := range requests {
		ids = append(ids, request.ID)
	}
	return ids
}

func checkPracticesRequestsAreRead(r *Rig) error {
	s := r.Store()
	first := aPracticesRequest("req-1", "repo-a", "worker-1", 1000, 5000)
	second := aPracticesRequest("req-2", "repo-b", "worker-1", 2000, 6000)
	other := aPracticesRequest("req-3", "repo-a", "worker-2", 3000, 7000)
	if err := practicesWrite(r, s, func(p ports.PracticesWriter) error {
		for _, request := range []ledger.PracticesRequest{second, first, other} {
			if err := p.Insert(request); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return Violatedf("inserting requests: %v", err)
	}
	return practicesRead(r, s, func(p ports.PracticesReader) error {
		got, err := p.Request("req-1")
		if err != nil || got != first {
			return Violatedf("a request reads back as %+v, %v; want %+v", got, err, first)
		}
		if _, err := p.Request("missing"); !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("an unknown request reads %v, want not found", err)
		}
		offered, err := p.Offered("worker-1")
		if err != nil || !slices.Equal(idsOfPracticesRequests(offered), []string{"req-1", "req-2"}) {
			return Violatedf("worker-1 is offered %v, %v; want req-1 and req-2 by age", idsOfPracticesRequests(offered), err)
		}
		if offered, err = p.Offered("nobody"); err != nil || len(offered) != 0 {
			return Violatedf("a worker with no request is offered %v, %v", offered, err)
		}
		open, err := p.Open("repo-b", 1500)
		if err != nil || open.ID != "req-2" {
			return Violatedf("repo-b's open request is %+v, %v; want req-2", open, err)
		}
		if _, err := p.Open("repo-b", 6000); !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("a request is open at its expiry: %v", err)
		}
		if _, err := p.Open("repo-c", 1500); !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("a repository with no request has an open one: %v", err)
		}
		return nil
	})
}

func checkPracticesFirstReportWins(r *Rig) error {
	s := r.Store()
	if err := practicesWrite(r, s, func(p ports.PracticesWriter) error {
		if err := p.Insert(aPracticesRequest("req-1", "repo-a", "worker-1", 1000, 5000)); err != nil {
			return err
		}
		return p.Insert(aPracticesRequest("req-2", "repo-b", "worker-1", 1000, 5000))
	}); err != nil {
		return Violatedf("inserting requests: %v", err)
	}
	var first, second bool
	if err := practicesWrite(r, s, func(p ports.PracticesWriter) (err error) {
		if first, err = p.Settle("req-1", 2000); err != nil {
			return err
		}
		second, err = p.Settle("req-1", 3000)
		return err
	}); err != nil {
		return Violatedf("settling: %v", err)
	}
	if !first || second {
		return Violatedf("the first report was recorded %v and the second %v, want true and false", first, second)
	}
	var late bool
	if err := practicesWrite(r, s, func(p ports.PracticesWriter) error {
		if _, err := p.Expire(9000); err != nil {
			return err
		}
		var err error
		late, err = p.Settle("req-2", 9500)
		return err
	}); err != nil {
		return Violatedf("settling an expired request: %v", err)
	}
	if !late {
		return Violatedf("a report of an expired request was not recorded")
	}
	return practicesRead(r, s, func(p ports.PracticesReader) error {
		for id, at := range map[string]int64{"req-1": 2000, "req-2": 9500} {
			got, err := p.Request(id)
			if err != nil || got.State != ledger.PracticesReported || got.ReportedAt != at {
				return Violatedf("%s reads %+v, %v; want reported at %d", id, got, err, at)
			}
		}
		if offered, err := p.Offered("worker-1"); err != nil || len(offered) != 0 {
			return Violatedf("a reported request is still offered: %v, %v", offered, err)
		}
		if _, err := p.Open("repo-a", 2500); !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("a reported request is open: %v", err)
		}
		return nil
	})
}

func checkPracticesExpiry(r *Rig) error {
	s := r.Store()
	if err := practicesWrite(r, s, func(p ports.PracticesWriter) error {
		for _, request := range []ledger.PracticesRequest{
			aPracticesRequest("req-old", "repo-a", "worker-1", 1000, 4000),
			aPracticesRequest("req-new", "repo-b", "worker-1", 2000, 8000),
			aPracticesRequest("req-done", "repo-c", "worker-1", 500, 3000),
		} {
			if err := p.Insert(request); err != nil {
				return err
			}
		}
		_, err := p.Settle("req-done", 1000)
		return err
	}); err != nil {
		return Violatedf("inserting requests: %v", err)
	}
	var expired []ledger.PracticesRequest
	if err := practicesWrite(r, s, func(p ports.PracticesWriter) (err error) {
		expired, err = p.Expire(5000)
		return err
	}); err != nil {
		return Violatedf("expiring: %v", err)
	}
	if !slices.Equal(idsOfPracticesRequests(expired), []string{"req-old"}) || expired[0].State != ledger.PracticesOffered {
		return Violatedf("expiring at 5000 returned %+v, want req-old as it was offered", expired)
	}
	var again []ledger.PracticesRequest
	if err := practicesWrite(r, s, func(p ports.PracticesWriter) (err error) {
		again, err = p.Expire(5000)
		return err
	}); err != nil {
		return Violatedf("expiring again: %v", err)
	}
	if len(again) != 0 {
		return Violatedf("a second pass expired %v", idsOfPracticesRequests(again))
	}
	return practicesRead(r, s, func(p ports.PracticesReader) error {
		for id, want := range map[string]ledger.PracticesState{
			"req-old": ledger.PracticesExpired, "req-new": ledger.PracticesOffered, "req-done": ledger.PracticesReported,
		} {
			if got, err := p.Request(id); err != nil || got.State != want {
				return Violatedf("%s is %q, %v; want %q", id, got.State, err, want)
			}
		}
		offered, err := p.Offered("worker-1")
		if err != nil || !slices.Equal(idsOfPracticesRequests(offered), []string{"req-new"}) {
			return Violatedf("worker-1 is offered %v, %v; want only req-new", idsOfPracticesRequests(offered), err)
		}
		return nil
	})
}

func aPracticesResult(repository, commit string) practices.Result {
	return practices.Result{
		Repository: repository, Commit: commit, BaseBranch: "main", Worker: "worker-1",
		CheckedAt: time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC), TableVersion: practices.TableVersion, RulesVersion: 1,
		ScannedFrom: "0000000000000000000000000000000000000001", Skipped: 2,
		Practices: []practices.PracticeStatus{
			{ID: practices.EnvIgnored, Status: practices.StatusFinding},
			{ID: practices.Secrets, Status: practices.StatusUnknown, Reason: "the secret scan failed"},
		},
		Findings: []practices.Finding{{
			ID: "env-ignored:ignore:.", Practice: practices.EnvIgnored, Subject: "ignore:.", Severity: practices.High, Detail: "`.env` is not ignored",
		}},
	}
}

func checkPracticesLatestResult(r *Rig) error {
	s := r.Store()
	first := ledger.PracticesRecord{Repository: "repo-a", Request: "req-1", Commit: "c1", Result: aPracticesResult("repo-a", "c1"), CheckedAt: 1000}
	other := ledger.PracticesRecord{Repository: "repo-b", Request: "req-2", Commit: "c9", Result: aPracticesResult("repo-b", "c9"), CheckedAt: 1500}
	if err := practicesRead(r, s, func(p ports.PracticesReader) error {
		if _, err := p.Result("repo-a"); !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("a repository never checked has a result: %v", err)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := practicesWrite(r, s, func(p ports.PracticesWriter) error {
		if err := p.SaveResult(other); err != nil {
			return err
		}
		return p.SaveResult(first)
	}); err != nil {
		return Violatedf("saving results: %v", err)
	}
	same := func(a, b ledger.PracticesRecord) bool {
		x, errA := json.Marshal(a)
		y, errB := json.Marshal(b)
		return errA == nil && errB == nil && string(x) == string(y)
	}
	if err := practicesRead(r, s, func(p ports.PracticesReader) error {
		got, err := p.Result("repo-a")
		if err != nil || !same(got, first) {
			return Violatedf("repo-a's result reads %+v, %v; want %+v", got, err, first)
		}
		all, err := p.Results()
		if err != nil || len(all) != 2 || all[0].Repository != "repo-a" || all[1].Repository != "repo-b" {
			return Violatedf("the results read %+v, %v; want repo-a and repo-b", all, err)
		}
		return nil
	}); err != nil {
		return err
	}
	later := ledger.PracticesRecord{Repository: "repo-a", Request: "req-3", Commit: "c2", Result: aPracticesResult("repo-a", "c2"), CheckedAt: 2000}
	later.Result.Findings = nil
	if err := practicesWrite(r, s, func(p ports.PracticesWriter) error { return p.SaveResult(later) }); err != nil {
		return Violatedf("saving a later result: %v", err)
	}
	return practicesRead(r, s, func(p ports.PracticesReader) error {
		got, err := p.Result("repo-a")
		if err != nil || !same(got, later) {
			return Violatedf("repo-a's result after a later save reads %+v, %v; want %+v", got, err, later)
		}
		if all, err := p.Results(); err != nil || len(all) != 2 {
			return Violatedf("a later save left %d results, %v; want one per repository", len(all), err)
		}
		return nil
	})
}

func checkPracticesScanTip(r *Rig) error {
	s := r.Store()
	tipOf := func(repository string) (string, error) {
		var tip string
		err := practicesRead(r, s, func(p ports.PracticesReader) (err error) {
			tip, err = p.ScannedTip(repository)
			return err
		})
		return tip, err
	}
	if tip, err := tipOf("repo-a"); err != nil || tip != "" {
		return Violatedf("a repository never scanned has the tip %q, %v", tip, err)
	}
	for _, step := range []struct{ repository, tip string }{{"repo-a", "c1"}, {"repo-b", "c7"}, {"repo-a", "c2"}} {
		if err := practicesWrite(r, s, func(p ports.PracticesWriter) error { return p.SaveScan(step.repository, step.tip) }); err != nil {
			return Violatedf("saving the tip of %s: %v", step.repository, err)
		}
	}
	for repository, want := range map[string]string{"repo-a": "c2", "repo-b": "c7"} {
		if tip, err := tipOf(repository); err != nil || tip != want {
			return Violatedf("%s has the tip %q, %v; want %q", repository, tip, err, want)
		}
	}
	if err := practicesWrite(r, s, func(p ports.PracticesWriter) error { return p.SaveScan("repo-a", "") }); err != nil {
		return Violatedf("clearing the tip: %v", err)
	}
	if tip, err := tipOf("repo-a"); err != nil || tip != "" {
		return Violatedf("a cleared tip reads %q, %v", tip, err)
	}
	if tip, err := tipOf("repo-b"); err != nil || tip != "c7" {
		return Violatedf("clearing repo-a's tip left repo-b's as %q, %v", tip, err)
	}
	return nil
}

// practicesWorker registers a worker and opens its session on the current protocol, so a check can be asked of it, and
// returns the use cases that register repositories and ask it for a check of each repository the machine says it keeps.
func (r *Rig) practicesWorker(s ports.Store, keeps ...string) (*control.Repositories, error) {
	plane := control.NewWorkers(s, r.Scope, control.WorkerOptions{HeartbeatMs: statusHeartbeatMs, LeaseTTLMs: TTLMs})
	token, err := plane.CreateJoinToken(r.Ctx, r.member())
	if err != nil {
		return nil, err
	}
	id := r.ID("worker")
	credential, err := plane.Register(r.Ctx, ledger.Registration{JoinToken: token, WorkerID: id, Name: "w"})
	if err != nil {
		return nil, err
	}
	auth := ledger.WorkerAuth{WorkerID: id, Credential: credential.Credential}
	if _, err := plane.OpenSession(r.Ctx, ledger.SessionRequest{Auth: auth, Protocol: ledger.ProtocolVersion}); err != nil {
		return nil, err
	}
	checkouts := map[string]bool{}
	for _, repository := range keeps {
		checkouts[repository] = true
	}
	hosts := func(context.Context, ledger.WorkspaceID) (map[string]control.AssistantHost, error) {
		return map[string]control.AssistantHost{id: {Checkouts: checkouts}}, nil
	}
	return control.NewRepositories(s).Practicing(control.NewPractices(s).Placing(hosts, 0, nil)), nil
}

// openCheck is the open request for a repository, or none.
func (r *Rig) openCheck(s ports.Store, repository string) (ledger.PracticesRequest, bool, error) {
	var request ledger.PracticesRequest
	found := false
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		request, err = tx.Practices().Open(repository, now)
		if errors.Is(err, ports.ErrNotFound) {
			return nil
		}
		found = err == nil
		return err
	})
	return request, found, err
}

func (r *Rig) requestedEvents(s ports.Store) (int, error) {
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return 0, err
	}
	return len(slices.DeleteFunc(events, func(e ledger.Event) bool { return e.Type != ledger.EventPracticesRequested })), nil
}

func checkRegistrationRequestsACheckOfEachRepository(r *Rig) error {
	s := r.Store()
	repositories, err := r.practicesWorker(s, "repo-api", "repo-web", "repo-docs")
	if err != nil {
		return err
	}
	kept := fake.NewCheckouts()
	for _, name := range []string{"api", "web", "docs", "lonely"} {
		kept.Place(ports.Clone{Path: "/work/" + name, ID: "repo-" + name, Remote: "git@github.com:acme/" + name + ".git", DefaultBranch: "main", Directory: name})
	}
	added, err := repositories.Add(r.Ctx, r.Scope, control.RepositoryAdd{Paths: []string{"/work/api", "/work/web", "/work/lonely"}}, kept, r.member())
	if err != nil || len(added) != 3 {
		return Violatedf("registering three clones read %+v: %v", added, err)
	}
	for _, one := range added {
		if one.Outcome != ledger.Registered {
			return Violatedf("%s was %s, want registered", one.Path, one.Outcome)
		}
	}
	for _, id := range []string{"repo-api", "repo-web"} {
		request, ok, err := r.openCheck(s, id)
		if err != nil || !ok || request.Cause != ledger.PracticesRegistration || request.RequestedBy != r.member().ID || request.State != ledger.PracticesOffered {
			return Violatedf("%s has the open check %+v (%v, found %v), want one made by its registration", id, request, err, ok)
		}
	}
	if _, ok, err := r.openCheck(s, "repo-lonely"); err != nil || ok {
		return Violatedf("a repository no worker keeps has an open check: %v", err)
	}
	if record, err := r.theRepository(s, "repo-lonely"); err != nil || record.Removed() {
		return Violatedf("a refusal to request changed the registration: %+v, %v", record, err)
	}
	if n, err := r.requestedEvents(s); err != nil || n != 2 {
		return Violatedf("the log holds %d requests, want one for each repository the worker keeps: %v", n, err)
	}
	// A repository the project holds already, and a dry run, request nothing.
	if again, err := repositories.Add(r.Ctx, r.Scope, control.RepositoryAdd{Paths: []string{"/work/api"}}, kept, r.member()); err != nil || len(again) != 1 || again[0].Outcome != ledger.AlreadyRegistered {
		return Violatedf("registering a known clone read %+v: %v", again, err)
	}
	if dry, err := repositories.Add(r.Ctx, r.Scope, control.RepositoryAdd{Paths: []string{"/work/docs"}, DryRun: true}, kept, r.member()); err != nil || len(dry) != 1 {
		return Violatedf("a dry run read %+v: %v", dry, err)
	}
	if _, ok, err := r.openCheck(s, "repo-docs"); err != nil || ok {
		return Violatedf("a dry run requested a check: %v", err)
	}
	if n, err := r.requestedEvents(s); err != nil || n != 2 {
		return Violatedf("a known clone and a dry run left %d requests, want 2: %v", n, err)
	}
	return nil
}

func checkManifestRegistrationRequestsACheckOfEachRepository(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	kept, err := r.planningMachine()
	if err != nil {
		return err
	}
	kept.Place(cloneOf("billing", "billing"))
	kept.Place(cloneOf("search", "search"))
	manifest := planning.RepositoriesFile{ID: "platform", Repositories: []planning.ManifestRepository{
		{Name: "billing", Remote: "git@github.com:acme/billing.git", Path: "../billing"},
		{Name: "search", Remote: "git@github.com:acme/search.git", Path: "../search"},
		{Name: "missing", Remote: "git@github.com:acme/missing.git", Path: "../missing"},
	}}
	repositories, err := r.practicesWorker(s, "repo-billing", "repo-search", "repo-missing")
	if err != nil {
		return err
	}
	added, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, false, r.member())
	if err != nil {
		return err
	}
	registered := 0
	for _, one := range added {
		if one.Outcome == ledger.Registered {
			registered++
		}
	}
	if registered != 2 {
		return Violatedf("the manifest registered %d repositories, want billing and search: %+v", registered, added)
	}
	for _, id := range []string{"repo-billing", "repo-search"} {
		if request, ok, err := r.openCheck(s, id); err != nil || !ok || request.Cause != ledger.PracticesRegistration {
			return Violatedf("%s has the open check %+v (%v, found %v), want one made by its registration", id, request, err, ok)
		}
	}
	if _, ok, err := r.openCheck(s, "repo-missing"); err != nil || ok {
		return Violatedf("a repository the manifest did not register has an open check: %v", err)
	}
	if n, err := r.requestedEvents(s); err != nil || n != 2 {
		return Violatedf("the log holds %d requests, want two: %v", n, err)
	}
	return nil
}
