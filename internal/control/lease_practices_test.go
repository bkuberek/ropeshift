package control_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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

// readFault is a store whose reads of the practices records fail, as an undecodable stored result or a lost
// connection does, and whose every other read is the store's own.
type readFault struct {
	ports.Store
	result, acknowledgements error
}

func (s readFault) Read(ctx context.Context, scope ledger.WorkspaceID, fn func(ports.ReadTx) error) error {
	return s.Store.Read(ctx, scope, func(tx ports.ReadTx) error { return fn(faultyTx{ReadTx: tx, fault: s}) })
}

type faultyTx struct {
	ports.ReadTx
	fault readFault
}

func (t faultyTx) Practices() ports.PracticesReader {
	return faultyPractices{PracticesReader: t.ReadTx.Practices(), fault: t.fault}
}

type faultyPractices struct {
	ports.PracticesReader
	fault readFault
}

func (p faultyPractices) Result(repository string) (ledger.PracticesRecord, error) {
	if p.fault.result != nil {
		return ledger.PracticesRecord{}, p.fault.result
	}
	return p.PracticesReader.Result(repository)
}

func (p faultyPractices) Acknowledgements(repository string) ([]ledger.PracticesAcknowledgement, error) {
	if p.fault.acknowledgements != nil {
		return nil, p.fault.acknowledgements
	}
	return p.PracticesReader.Acknowledgements(repository)
}

// No finding decides anything (FR-PRAC-11), and none may stop a lane: a practices record the control plane cannot read
// leaves its repository unreadable in the planning lease, and the lease, the worker's other leases, and the rest of
// desired state are served as they were.
func TestAPracticesRecordThatCannotBeReadStallsNoLaneOnTheWorker(t *testing.T) {
	for name, fault := range map[string]readFault{
		"a result that cannot be read":         {result: errors.New("decode result: invalid character")},
		"acknowledgements that cannot be read": {acknowledgements: errors.New("database is locked")},
	} {
		t.Run(name, func(t *testing.T) {
			r := newPracticesRig(t, ledger.ProtocolVersion)
			widgets := r.register("repo-a", "widgets")
			r.register("repo-b", "gadgets")
			r.checked(widgets.ID, time.Minute, widgetsResult())
			r.leased(workorders.WorkOrder{ID: "wo-change", Kind: workorders.KindChange})
			r.leased(workorders.WorkOrder{ID: "wo-plan", Kind: workorders.KindPlan})
			var warned []string
			fault.Store = r.store
			link := control.NewWorkers(fault, "pa", control.WorkerOptions{Warn: func(line string) { warned = append(warned, line) }})

			state, err := link.Desired(t.Context(), ledger.DesiredRequest{Auth: r.auth})

			if err != nil {
				t.Fatalf("a practices record that cannot be read fails desired state: %v", err)
			}
			leases := map[string]ledger.DesiredLease{}
			for _, lease := range state.Leases {
				leases[lease.WorkOrder] = lease
			}
			if leases["wo-change"].Lane != "lane-wo-change" || leases["wo-plan"].Lane != "lane-wo-plan" {
				t.Fatalf("the worker's leases are %+v", leases)
			}
			plan := leases["wo-plan"].Practices
			if len(plan) == 0 {
				t.Fatalf("the planning lease holds no summaries: %+v", plan)
			}
			for _, summary := range plan {
				if summary.Name == "widgets" && (!summary.Unreadable || summary.Open != nil || summary.Checked) {
					t.Errorf("the repository that could not be read is %+v", summary)
				}
			}
			if !slices.ContainsFunc(warned, func(line string) bool { return strings.Contains(line, widgets.ID) }) {
				t.Errorf("the control plane warned %q, want a line naming the repository", warned)
			}
		})
	}
}

// With no one to hear a warning, a record that cannot be read is left out all the same.
func TestAPracticesRecordThatCannotBeReadIsLeftOutWithNoWarn(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-a", "widgets")
	r.leased(workorders.WorkOrder{ID: "wo-plan", Kind: workorders.KindPlan})
	link := control.NewWorkers(readFault{Store: r.store, result: errors.New("lost")}, "pa", control.WorkerOptions{})
	state, err := link.Desired(t.Context(), ledger.DesiredRequest{Auth: r.auth})
	if err != nil || len(state.Leases) != 1 || len(state.Leases[0].Practices) != 1 || !state.Leases[0].Practices[0].Unreadable {
		t.Errorf("desired state is %+v, %v", state, err)
	}
}

// A repository's lease summary holds at most 50 open findings, the high ones first and then by ID, and counts the rest.
func TestAPlanningLeaseCapsEachRepositorysOpenFindings(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	widgets := r.register("repo-a", "widgets")
	result := widgetsResult()
	result.Findings = nil
	for i := range 60 {
		id := fmt.Sprintf("lockfile:npm:app%02d", i)
		severity := practices.Low
		if i%3 == 0 {
			severity = practices.High
		}
		result.Findings = append(result.Findings, practices.Finding{ID: id, Practice: practices.Lockfile, Subject: "npm:app", Severity: severity, Detail: "no lockfile"})
	}
	r.checked(widgets.ID, time.Minute, result)

	got := summaryOf(t, r.leased(workorders.WorkOrder{ID: "wo-plan", Kind: workorders.KindPlan}), "widgets")

	if len(got.Open) != ledger.PracticesOpenLimit || got.More != 10 {
		t.Fatalf("the summary holds %d open findings and counts %d more, want 50 and 10", len(got.Open), got.More)
	}
	for i, finding := range got.Open[:20] {
		if finding.Severity != practices.High || (i > 0 && got.Open[i-1].ID >= finding.ID) {
			t.Errorf("finding %d is %+v: the 20 high findings come first, by ID", i, finding)
		}
	}
}

// The acknowledged findings and the unsettled practices of a lease summary are capped at 50 each, by ID, and counted.
func TestAPlanningLeaseCapsTheAcknowledgedFindings(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	widgets := r.register("repo-a", "widgets")
	result := widgetsResult()
	result.Findings = nil
	for i := range 60 {
		result.Findings = append(result.Findings, practices.Finding{
			ID: fmt.Sprintf("lockfile:npm:app%02d", i), Practice: practices.Lockfile, Subject: "npm:app", Severity: practices.Low, Detail: "no lockfile",
		})
	}
	r.checked(widgets.ID, time.Minute, result)
	for i := 59; i >= 0; i-- {
		r.acknowledge(widgets.ID, fmt.Sprintf("lockfile:npm:app%02d", i), "kept")
	}

	got := summaryOf(t, r.leased(workorders.WorkOrder{ID: "wo-plan", Kind: workorders.KindPlan}), "widgets")

	if len(got.Open) != 0 || len(got.Acknowledged) != ledger.PracticesListLimit || got.MoreAcknowledged != 10 {
		t.Fatalf("open %d, acknowledged %d with %d more, want none open and 50 with 10", len(got.Open), len(got.Acknowledged), got.MoreAcknowledged)
	}
	if got.Acknowledged[0].ID != "lockfile:npm:app00" || got.Acknowledged[49].ID != "lockfile:npm:app49" {
		t.Errorf("the acknowledged findings are %s to %s, want the first 50 by ID", got.Acknowledged[0].ID, got.Acknowledged[49].ID)
	}
}
