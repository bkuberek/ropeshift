package contract

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/internal/workorders"
)

// PracticesNeverBlockChecks are the proof of FR-PRAC-11: no claim, lane start, review, merge, plan approval, or adoption
// reads a finding. Each check runs the same steps on two rigs, one whose repository's latest practices result holds an
// open `secrets` finding, an open `ci` finding, an acknowledged finding, and an `unknown` practice, and one with no
// result recorded, and requires the outcomes and the events the steps record to be the same.
var PracticesNeverBlockChecks = []Check{
	{"PRN1", "a work order in a repository with open findings is claimed and its lane starts as it is with no result recorded", checkFindingsDoNotBlockAClaim},
	{"PRN2", "a review that passes and a merge that lands at review_approves on a fake forge are the same with open findings as with no result recorded", checkFindingsDoNotBlockAMerge},
	{"PRN3", "a plan version is approved the same with open findings as with no result recorded", checkFindingsDoNotBlockAPlanApproval},
	{"PRN4", "an adoption registers a repository the same with open findings as with no result recorded", checkFindingsDoNotBlockAnAdoption},
}

// seeder records a repository's latest practices result, when a flow has one to record.
type seeder func(r *Rig, s ports.Store, repository string) error

// seedFindings records the result of a check of a repository, as a worker's report leaves it, and an acknowledgement of
// one of its findings.
func seedFindings(r *Rig, s ports.Store, repository string) error {
	const commit = "1111111111111111111111111111111111111111"
	checked := time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)
	result := practices.Result{
		Repository: repository, Commit: commit, BaseBranch: "main", Worker: "worker-1", CheckedAt: checked,
		TableVersion: practices.TableVersion, RulesVersion: ledger.CredentialRulesVersion,
		Practices: []practices.PracticeStatus{
			{ID: practices.Secrets, Status: practices.StatusFinding},
			{ID: practices.CI, Status: practices.StatusFinding},
			{ID: practices.Codeowners, Status: practices.StatusFinding},
			{ID: practices.License, Status: practices.StatusUnknown, Reason: "the forge could not be read"},
		},
		Findings: []practices.Finding{
			{
				ID: "secrets:github_token:config/dev.yaml:1a2b3c4d5e6f", Practice: practices.Secrets, Severity: practices.High, Detail: "a github_token credential",
				Rule: "github_token", Path: "config/dev.yaml", Line: 3, Commit: "aaaaaaaaaaaa", Digest: "1a2b3c4d5e6f1a2b3c4d5e6f",
			},
			{ID: "ci", Practice: practices.CI, Severity: practices.Medium, Detail: "no CI workflow and no no-CI recipe"},
			{ID: "codeowners", Practice: practices.Codeowners, Severity: practices.Low, Detail: "no CODEOWNERS file"},
		},
	}
	return s.Write(r.Ctx, r.Scope, "practices.seed", func(tx ports.WriteTx) error {
		if err := tx.Practices().SaveResult(ledger.PracticesRecord{
			Repository: repository, Request: "request-" + repository, Commit: commit, Result: result, CheckedAt: checked.UnixMilli(),
		}); err != nil {
			return err
		}
		if _, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventPracticesChecked, Actor: r.worker("worker-1"), Subject: ledger.Subject{Kind: ledger.SubjectPracticesRequest, ID: "request-" + repository},
		}); err != nil {
			return err
		}
		if _, err := tx.Practices().Acknowledge(ledger.PracticesAcknowledgement{
			Repository: repository, Finding: "codeowners", Reason: "the team owns every path", Member: r.Actor.ID, AcknowledgedAt: tx.Now(),
		}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: ledger.EventPracticesAcknowledged, Actor: r.Actor, Subject: ledger.PracticesFindingSubject(repository, "codeowners")})
		return err
	})
}

// seeded says the repository's result and its acknowledgement are what seedFindings recorded, so a flow that came to
// the same outcome did so with findings in the ledger.
func seeded(r *Rig, s ports.Store, repository string) error {
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		record, err := tx.Practices().Result(repository)
		if err != nil {
			return Violatedf("the repository holds no result: %v", err)
		}
		acks, err := tx.Practices().Acknowledgements(repository)
		if err != nil {
			return err
		}
		if len(record.Result.Findings) != 3 || len(acks) != 1 {
			return Violatedf("the repository holds %d findings and %d acknowledgements, want 3 and 1", len(record.Result.Findings), len(acks))
		}
		return nil
	})
}

// decided is what a flow came to: the outcomes it observed, and the events it recorded after the point the findings were
// recorded at, each by who made it, what it is about, and its type.
type decided struct {
	outcomes []string
	events   []string
}

func (d *decided) outcome(format string, args ...any) {
	d.outcomes = append(d.outcomes, fmt.Sprintf(format, args...))
}

// eventsSince lists the events recorded after the first n, by who made each, what it is about, and its type.
func (r *Rig) eventsSince(s ports.Store, n int) ([]string, error) {
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, event := range events[min(n, len(events)):] {
		out = append(out, fmt.Sprintf("%s %s %s", event.Actor.Kind, event.Subject.Kind, event.Type))
	}
	return out, nil
}

// eventCount is how many events the log holds.
func (r *Rig) eventCount(s ports.Store) (int, error) {
	events, err := r.Events(s, r.Scope)
	return len(events), err
}

// flow runs steps on a rig and says what they came to. It calls seed once the repository exists and before the steps
// that decide anything, with seed nil on the rig with no result.
type flow func(r *Rig, seed seeder) (decided, error)

// sameWithAndWithoutFindings runs a flow on a rig with findings and on a twin with none, and requires the same outcomes
// and the same events.
func sameWithAndWithoutFindings(r *Rig, run flow) error {
	with, err := run(r, seedFindings)
	if err != nil {
		return err
	}
	without, err := run(r.Twin(), nil)
	if err != nil {
		return err
	}
	if len(with.outcomes) == 0 || len(with.events) == 0 {
		return Violatedf("the flow observed %d outcomes and %d events, so it proves nothing", len(with.outcomes), len(with.events))
	}
	if !slices.Equal(with.outcomes, without.outcomes) {
		return Violatedf("with findings the outcomes are\n  %s\nwithout a result they are\n  %s", strings.Join(with.outcomes, "\n  "), strings.Join(without.outcomes, "\n  "))
	}
	if !slices.Equal(with.events, without.events) {
		return Violatedf("with findings the events are\n  %s\nwithout a result they are\n  %s", strings.Join(with.events, "\n  "), strings.Join(without.events, "\n  "))
	}
	return nil
}

// begin runs the seed of a flow, and says how many events the log holds once it did, so the flow reads the events after.
func begin(r *Rig, s ports.Store, repository string, seed seeder) (int, error) {
	if seed != nil {
		if err := seed(r, s, repository); err != nil {
			return 0, err
		}
		if err := seeded(r, s, repository); err != nil {
			return 0, err
		}
	}
	return r.eventCount(s)
}

func checkFindingsDoNotBlockAClaim(r *Rig) error {
	return sameWithAndWithoutFindings(r, func(r *Rig, seed seeder) (decided, error) {
		var d decided
		s := r.Store()
		const repository = "repo-claim"
		if _, err := r.detect(s, repository, forgeRemote, "main"); err != nil {
			return d, err
		}
		ops := r.NewOps(s)
		order, err := ops.Record(r.Ctx, r.Scope, control.LedgerWork{
			Order:     workorders.WorkOrder{ID: r.ID("wo"), Kind: workorders.KindChange, Title: "Add the retry budget", RepositoryID: repository, MaxAttempts: 2},
			Statement: "Retry a failed payment within a budget.",
			Criteria:  []planning.Criterion{{ID: "c1", Text: "A failed payment is retried"}},
		}, r.Actor)
		if err != nil {
			return d, err
		}
		base, err := begin(r, s, repository, seed)
		if err != nil {
			return d, err
		}
		if _, err := ops.Pass(r.Ctx, r.Scope, order.ID, r.Actor); err != nil {
			return d, err
		}
		holder, err := r.Running(s, order.ID, "worker-1")
		if err != nil {
			return d, Violatedf("claiming and starting the lane: %v", err)
		}
		row, err := r.WorkOrder(s, order.ID)
		if err != nil {
			return d, err
		}
		d.outcome("the work order is %s, claimed at fence %d, its lane at generation %d", row.State, holder.Fence, holder.Generation)
		d.events, err = r.eventsSince(s, base)
		return d, err
	})
}

func checkFindingsDoNotBlockAMerge(r *Rig) error {
	return sameWithAndWithoutFindings(r, func(r *Rig, seed seeder) (decided, error) {
		var d decided
		f, err := newMergeRig(r, ledger.ReviewOnPullRequest, ledger.ReviewApproves, ledger.MergeSquash)
		if err != nil {
			return d, err
		}
		base, err := begin(r, f.store, f.repository, seed)
		if err != nil {
			return d, err
		}
		if err := f.report(f.opened(1, f.head)); err != nil {
			return d, err
		}
		if err := f.read(f.pullRequestAt(f.head, requiredCheck("CI result", ledger.CheckPassed))); err != nil {
			return d, err
		}
		if _, err := f.judge(passes("c1", "ok"), passes("c2", "ok")); err != nil {
			return d, err
		}
		if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Integrating); err != nil {
			return d, err
		}
		merge, err := f.oneMerge()
		if err != nil {
			return d, err
		}
		d.outcome("the worker is asked to merge pull request %d at the reviewed head by %s on %s into %s", merge.Number, merge.Method, merge.Binding, merge.Base)
		if err := f.mergeCame(merge, ledger.MergeResult{Outcome: ledger.MergeMerged, MergeCommit: r.Commit()}); err != nil {
			return d, err
		}
		if err := r.stateIs(f.store, f.grant.WorkOrder, workorders.Done); err != nil {
			return d, err
		}
		view, err := f.standing()
		if err != nil || len(view.Integrations) != 1 {
			return d, Violatedf("the approvals read %+v: %v", view.Integrations, err)
		}
		approval := view.Integrations[0]
		d.outcome("the work order is done, approved by %s at %s, merged %v", approval.ApprovedByKind, approval.MergeAuthority, approval.Merged())
		d.events, err = r.eventsSince(f.store, base)
		return d, err
	})
}

func checkFindingsDoNotBlockAPlanApproval(r *Rig) error {
	return sameWithAndWithoutFindings(r, func(r *Rig, seed seeder) (decided, error) {
		var d decided
		s := r.Store()
		const repository = "repo-plan"
		if _, err := r.detect(s, repository, forgeRemote, "main"); err != nil {
			return d, err
		}
		files := newApprovalSource()
		if _, err := r.intake(s, files.source); err != nil {
			return d, err
		}
		base, err := begin(r, s, repository, seed)
		if err != nil {
			return d, err
		}
		pending, err := r.theOpenPlanApproval(s)
		if err != nil {
			return d, err
		}
		approved, err := r.approve(s, pending, true, theMember)
		if err != nil {
			return d, Violatedf("approving the plan version: %v", err)
		}
		order, err := r.orderOfContract(s, "budget")
		if err != nil {
			return d, err
		}
		d.outcome("the approval is %s, and its contract's work order is %s, reviewed by %s", approved.State, order.State, order.ReviewedBy)
		d.events, err = r.eventsSince(s, base)
		return d, err
	})
}

func checkFindingsDoNotBlockAnAdoption(r *Rig) error {
	return sameWithAndWithoutFindings(r, func(r *Rig, seed seeder) (decided, error) {
		var d decided
		s := r.Store()
		repositories, err := r.practicesWorker(s, "repo-api", "repo-web")
		if err != nil {
			return d, err
		}
		base, err := begin(r, s, "repo-api", seed)
		if err != nil {
			return d, err
		}
		kept := newAdoptionClones("api", "web")
		added, err := repositories.Add(r.Ctx, r.Scope, control.RepositoryAdd{Paths: []string{"/work/api", "/work/web"}}, kept, r.member())
		if err != nil {
			return d, Violatedf("registering the clones: %v", err)
		}
		for _, one := range added {
			d.outcome("%s is %s", one.Path, one.Outcome)
		}
		for _, id := range []string{"repo-api", "repo-web"} {
			record, err := r.theRepository(s, id)
			if err != nil {
				return d, err
			}
			request, open, err := r.openCheck(s, id)
			if err != nil {
				return d, err
			}
			d.outcome("%s is registered as %s, and its open check is %v of cause %s", id, record.Name, open, request.Cause)
		}
		d.events, err = r.eventsSince(s, base)
		return d, err
	})
}

// newAdoptionClones is the clones of a machine that keeps one repository for each name, at /work/<name>.
func newAdoptionClones(names ...string) *fake.Checkouts {
	kept := fake.NewCheckouts()
	for _, name := range names {
		kept.Place(ports.Clone{Path: "/work/" + name, ID: "repo-" + name, Remote: "git@github.com:acme/" + name + ".git", DefaultBranch: "main", Directory: name})
	}
	return kept
}
