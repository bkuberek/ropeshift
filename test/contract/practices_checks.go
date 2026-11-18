package contract

import (
	"encoding/json"
	"errors"
	"slices"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
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
