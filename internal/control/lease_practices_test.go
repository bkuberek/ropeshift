package control_test

import (
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/internal/workorders"
)

// leased records a work order in the project, claims it, starts a lane for it, and reads the lease the worker is
// offered.
func (r *practicesRig) leased(order workorders.WorkOrder) ledger.DesiredLease {
	r.t.Helper()
	id := order.ID
	order.Title = "T"
	_, err := control.NewWorkOrders(r.store).Record(r.t.Context(), "pa", control.LedgerWork{
		Order:    order,
		Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
	}, projectMember)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := control.NewReviews(r.store).Pass(r.t.Context(), "pa", id, projectMember); err != nil {
		r.t.Fatal(err)
	}
	if err := r.claim("pa", id); err != nil {
		r.t.Fatal(err)
	}
	if _, err := control.NewLeases(r.store).Dispatch(r.t.Context(), "pa", id, "lane-"+id, projectMember); err != nil {
		r.t.Fatal(err)
	}
	for _, lease := range r.desired().Leases {
		if lease.WorkOrder == id {
			return lease
		}
	}
	r.t.Fatalf("no lease of %s in %+v", id, r.desired().Leases)
	return ledger.DesiredLease{}
}

// checked keeps a result as a repository's latest, checked the time before the database's now.
func (r *practicesRig) checked(repository string, ago time.Duration, result practices.Result) {
	r.t.Helper()
	err := r.store.Write(r.t.Context(), "pa", "test.practices_result", func(tx ports.WriteTx) error {
		at := tx.Now() - ago.Milliseconds()
		result.Repository, result.CheckedAt = repository, time.UnixMilli(at).UTC()
		if err := tx.Practices().SaveResult(ledger.PracticesRecord{Repository: repository, Request: "req-" + repository, Commit: result.Commit, Result: result, CheckedAt: at}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.checked", Actor: projectMember, Subject: ledger.Subject{Kind: ledger.SubjectPracticesRequest, ID: "req-" + repository}})
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

func (r *practicesRig) acknowledge(repository, finding, reason string) {
	r.t.Helper()
	if _, err := r.practices.Acknowledge(r.t.Context(), "pa", repository, finding, reason, projectMember); err != nil {
		r.t.Fatal(err)
	}
}

// widgetsResult is a result with an open `env-ignored` finding, an open `codeowners` finding, an acknowledged
// `security-policy` finding, and a `license` practice the check could not settle.
func widgetsResult() practices.Result {
	result := *aResult("", tipOne, "")
	result.Findings = []practices.Finding{
		{ID: "env-ignored:ignore:.", Practice: practices.EnvIgnored, Subject: "ignore:.", Severity: practices.High, Detail: "`.env` is not ignored"},
		{ID: "codeowners", Practice: practices.Codeowners, Severity: practices.Low, Detail: "no CODEOWNERS file"},
		{ID: "security-policy", Practice: practices.SecurityPolicy, Severity: practices.Low, Detail: "no SECURITY.md"},
	}
	for i, status := range result.Practices {
		switch status.ID {
		case practices.Codeowners, practices.SecurityPolicy:
			result.Practices[i].Status = practices.StatusFinding
		case practices.License:
			result.Practices[i] = practices.PracticeStatus{ID: practices.License, Status: practices.StatusUnknown, Reason: "the forge refused the read: HTTP 403"}
		}
	}
	return result
}

func summaryOf(t *testing.T, lease ledger.DesiredLease, name string) ledger.PracticesSummary {
	t.Helper()
	at := slices.IndexFunc(lease.Practices, func(s ledger.PracticesSummary) bool { return s.Name == name })
	if at < 0 {
		t.Fatalf("the lease holds no summary of %s: %+v", name, lease.Practices)
	}
	return lease.Practices[at]
}

// A planning lease carries, for each active repository, the commit and age of its latest result, its open findings with
// the fix of their practice, the findings a member acknowledged with the reason, and the practices the check could not
// settle (FR-PRAC-8).
func TestAPlanningLeaseCarriesEachActiveRepositorysFindings(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	widgets := r.register("repo-a", "widgets")
	r.register("repo-b", "gadgets")
	gone := r.register("repo-c", "gizmos")
	r.checked(widgets.ID, 90*time.Second, widgetsResult())
	r.checked(gone.ID, time.Minute, widgetsResult())
	r.acknowledge(widgets.ID, "security-policy", "Reports go through the disclosure page.")
	if _, err := control.NewRepositories(r.store).Remove(t.Context(), "pa", gone.ID, nil, projectMember); err != nil {
		t.Fatal(err)
	}

	lease := r.leased(workorders.WorkOrder{ID: "wo-plan", Kind: workorders.KindPlan})

	if len(lease.Practices) != 2 {
		t.Fatalf("the lease holds %d summaries, want one for each of the two active repositories: %+v", len(lease.Practices), lease.Practices)
	}
	got := summaryOf(t, lease, "widgets")
	if got.Repository != widgets.ID || !got.Checked || got.Commit != tipOne {
		t.Errorf("the summary names %s, checked %v, commit %q", got.Repository, got.Checked, got.Commit)
	}
	if got.AgeMs < 90_000 || got.AgeMs > 90_000+30_000 {
		t.Errorf("the age is %d ms, want about 90 seconds", got.AgeMs)
	}
	fix := func(id practices.ID) string { p, _ := practices.Lookup(id); return p.Fix }
	wantOpen := []ledger.PracticesOpenFinding{
		{ID: "env-ignored:ignore:.", Severity: practices.High, Detail: "`.env` is not ignored", Fix: fix(practices.EnvIgnored)},
		{ID: "codeowners", Severity: practices.Low, Detail: "no CODEOWNERS file", Fix: fix(practices.Codeowners)},
	}
	if !slices.Equal(got.Open, wantOpen) {
		t.Errorf("open findings %+v, want %+v", got.Open, wantOpen)
	}
	if want := []ledger.PracticesAcknowledgedFinding{{ID: "security-policy", Reason: "Reports go through the disclosure page."}}; !slices.Equal(got.Acknowledged, want) {
		t.Errorf("acknowledged findings %+v, want %+v", got.Acknowledged, want)
	}
	if want := []ledger.PracticesUnknownPractice{{Practice: practices.License, Reason: "the forge refused the read: HTTP 403"}}; !slices.Equal(got.Unknown, want) {
		t.Errorf("unknown practices %+v, want %+v", got.Unknown, want)
	}
	if gadgets := summaryOf(t, lease, "gadgets"); gadgets.Checked || gadgets.Commit != "" || gadgets.Open != nil || gadgets.Repository != "repo-b" {
		t.Errorf("a repository no check reported reads %+v, want not checked and nothing else", gadgets)
	}
}

// An acknowledgement of a finding the result does not hold, such as one a later check no longer reports, is not a
// finding of the repository, so the lease does not list it.
func TestAPlanningLeaseListsOnlyTheAcknowledgementsOfFindingsTheResultHolds(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	widgets := r.register("repo-a", "widgets")
	r.checked(widgets.ID, time.Minute, widgetsResult())
	r.acknowledge(widgets.ID, "tests:go", "No tests yet.")

	got := summaryOf(t, r.leased(workorders.WorkOrder{ID: "wo-plan", Kind: workorders.KindPlan}), "widgets")

	if len(got.Acknowledged) != 0 || len(got.Open) != 3 {
		t.Errorf("open %+v, acknowledged %+v: an acknowledgement of a finding the result lacks changes neither", got.Open, got.Acknowledged)
	}
}

// Only planning work, a planning session's and an adopt session's alike, is told the findings: a lease of any other
// kind carries none, though the project holds them.
func TestOnlyAPlanningLeaseCarriesTheFindings(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	widgets := r.register("repo-a", "widgets")
	r.checked(widgets.ID, time.Minute, widgetsResult())

	for _, tt := range []struct {
		order workorders.WorkOrder
		want  int
	}{
		{workorders.WorkOrder{ID: "wo-plan", Kind: workorders.KindPlan}, 1},
		{workorders.WorkOrder{ID: "wo-adopt", Kind: workorders.KindPlan, Adopt: true}, 1},
		{workorders.WorkOrder{ID: "wo-change", Kind: workorders.KindChange}, 0},
		{workorders.WorkOrder{ID: "wo-rework", Kind: workorders.KindRework}, 0},
	} {
		lease := r.leased(tt.order)
		if len(lease.Practices) != tt.want || (tt.want == 0 && lease.Practices != nil) {
			t.Errorf("a %s lease (adopt %v) holds %+v, want %d summaries", tt.order.Kind, tt.order.Adopt, lease.Practices, tt.want)
		}
	}
}
