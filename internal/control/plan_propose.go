package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// proposablePlans lists the `plan` work orders whose lane ended with a branch nobody has accepted, and
// that no request already asks about at that commit.
func (i *Intake) proposablePlans(ctx context.Context, scope ledger.WorkspaceID) ([]acceptedPlan, error) {
	var out []acceptedPlan
	err := i.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Kind != workorders.KindPlan || row.State != workorders.Acceptance || row.PlanCommit != "" {
				continue
			}
			artifacts, err := tx.Evidence().Artifacts(row.ID)
			if err != nil {
				return err
			}
			branch := attemptBranch(artifacts, row.Fence)
			if branch.Digest == "" {
				continue
			}
			asked, err := askedAbout(tx.Approvals(), row.ID, branch.Digest)
			if err != nil {
				return err
			}
			if !asked {
				out = append(out, acceptedPlan{order: row, commit: branch.Digest, lane: branch.Lane})
			}
		}
		return nil
	})
	return out, err
}

// askedAbout says a request already stands for a work order's branch at this commit, in any state, so a
// branch is asked about once and a request a member denied is not asked again.
func askedAbout(approvals ports.ApprovalReader, workOrder, commit string) (bool, error) {
	rows, err := approvals.OfWorkOrder(workOrder)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.Kind == authority.KindPlan && planDetailOf(row).Source == commit {
			return true, nil
		}
	}
	return false, nil
}

// ProposePlans reads the branch of each `plan` work order that waits for acceptance, and asks a member to
// approve the plan it holds. Nothing it reads is a record: the request is bound to what the branch holds,
// and approving it accepts the work order, after which intake reads the commit as it reads any accepted
// plan (D17). A plan work order and a commit are asked about once.
func (i *Intake) ProposePlans(ctx context.Context, scope ledger.WorkspaceID, open PlanSource) ([]string, error) {
	waiting, err := i.proposablePlans(ctx, scope)
	if err != nil {
		return nil, err
	}
	var proposed []string
	var failed []error
	for _, plan := range waiting {
		source, err := open(ctx, plan.order.RepositoryID, plan.commit)
		if err != nil {
			failed = append(failed, fmt.Errorf("plan %s: %w", plan.order.ID, err))
			continue
		}
		batch, err := source.Poll(ctx, "")
		if err == nil {
			batch, err = i.offPlanOf(ctx, scope, source, batch)
		}
		if err != nil {
			failed = append(failed, fmt.Errorf("plan %s: %w", plan.order.ID, err))
			continue
		}
		batch, _ = refuseCycles(batch)
		var changes branchChanges
		if plan.order.Adopt {
			changes = i.adoptionChanges(ctx, scope, plan)
		}
		err = i.store.Write(ctx, scope, "intake.propose", func(tx ports.WriteTx) error {
			return askAboutBranch(tx, plan, batch, changes)
		})
		if err != nil {
			failed = append(failed, fmt.Errorf("plan %s: %w", plan.order.ID, err))
			continue
		}
		proposed = append(proposed, plan.order.ID)
	}
	return proposed, errors.Join(failed...)
}

// branchChanges is what an adoption branch changed since its lane began, or why that could not be read.
type branchChanges struct {
	files  []planning.FileChange
	unread string
}

// adoptionChanges reads every file an adopt lane's branch added, changed, moved, or deleted since the commit
// its claim began the branch at, so the person reads the branch as one change (ADR-0023). What cannot be read
// is said, and the request is raised all the same.
func (i *Intake) adoptionChanges(ctx context.Context, scope ledger.WorkspaceID, plan acceptedPlan) branchChanges {
	if i.starts == nil {
		return branchChanges{unread: "this control plane reads no repository's history"}
	}
	var claim workorders.Claim
	err := i.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		claim, err = tx.Claims().OfLane(plan.lane)
		return err
	})
	switch {
	case err != nil:
		return branchChanges{unread: fmt.Sprintf("the lane's claim could not be read: %v", err)}
	case claim.BaseCommit == "":
		return branchChanges{unread: "the lane recorded no commit its branch began at"}
	}
	path, err := i.starts.Checkout(ctx, scope, plan.order.RepositoryID)
	if err != nil {
		return branchChanges{unread: err.Error()}
	}
	files, err := i.starts.Bases.Changes(ctx, path, claim.BaseCommit, plan.commit)
	if err != nil {
		return branchChanges{unread: fmt.Sprintf("git could not read the branch from %s to %s: %v", claim.BaseCommit, plan.commit, err)}
	}
	return branchChanges{files: files}
}

// adoptionOf groups the records of one plan a branch holds as its approval shows them, with what the branch
// changed.
func adoptionOf(batch ports.ChangeBatch, plan planning.PlanFile, changes branchChanges) *planning.Adoption {
	files := planning.AdoptedFiles{Plan: plan}
	for _, record := range batch.Records {
		switch {
		case record.Requirements != nil && record.Requirements.Plan == plan.ID:
			files.Requirements = append(files.Requirements, *record.Requirements)
		case record.File.Milestone != nil && record.File.Milestone.Plan == plan.ID:
			files.Milestones = append(files.Milestones, *record.File.Milestone)
		}
	}
	for _, item := range batch.Items {
		if item.Contract.Plan == plan.ID {
			files.Contracts = append(files.Contracts, item.Contract)
		}
	}
	adoption := planning.GroupAdoption(files)
	if changes.files != nil {
		adoption.Changes = changes.files
	}
	adoption.ChangesUnread = changes.unread
	return &adoption
}

// askAboutBranch raises one request for each plan file the branch holds whose digest the ledger does not. A
// branch that adopts a project groups each plan it holds, with what the branch changed.
func askAboutBranch(tx ports.WriteTx, branch acceptedPlan, batch ports.ChangeBatch, changes branchChanges) error {
	asked, err := askedAbout(tx.Approvals(), branch.order.ID, branch.commit)
	if err != nil || asked {
		return err
	}
	for _, item := range batch.Records {
		if item.Plan == nil {
			continue
		}
		entries, err := candidateEntries(batch, item)
		if err != nil {
			return err
		}
		ask := planAsk{
			ref: item.Plan.ID, title: item.Plan.Title, version: 1, digest: planning.PlanDigest(entries), entries: entries,
			decisions: len(item.Plan.Decisions), source: branch.commit, workOrder: branch.order.ID,
		}
		if branch.order.Adopt {
			ask.adoption = adoptionOf(batch, *item.Plan, changes)
		}
		link, err := tx.Planning().Link(planning.SubjectPlan, item.Key)
		switch {
		case errors.Is(err, ports.ErrNotFound):
		case err != nil:
			return err
		default:
			ask.plan = link.RecordID
			latest, err := tx.Planning().LatestPlanVersion(ask.plan)
			if err == nil && latest.Digest == ask.digest {
				// The ledger already holds exactly this plan, so there is nothing to approve.
				continue
			}
			if err == nil {
				ask.version = latest.Version + 1
			} else if !errors.Is(err, ports.ErrNotFound) {
				return err
			}
		}
		if err := askForPlanApproval(tx, ask); err != nil {
			return err
		}
	}
	return nil
}

// candidateEntries gathers the records a batch holds that name a plan file, with the digests intake
// would give them, so a branch has the digest its plan will have once the ledger holds it.
func candidateEntries(batch ports.ChangeBatch, plan ports.RecordItem) ([]planning.Entry, error) {
	fields, err := json.Marshal(plan.Plan)
	if err != nil {
		return nil, err
	}
	digest := func(item ports.RecordItem, parts ...string) string {
		if item.Digest != "" {
			return item.Digest
		}
		return planning.ContentDigest(append(parts, item.Statement)...)
	}
	entries := []planning.Entry{{Ref: planning.PlanRef(plan.Plan.ID), Digest: digest(plan, string(fields))}}
	for _, item := range batch.Records {
		if head, document := item.Head(); document && head.Plan == plan.Plan.ID {
			entries = append(entries, planning.Entry{Ref: head.Ref(), Digest: digest(item, string(head.Fields))})
		}
	}
	for _, item := range batch.Items {
		if item.Contract.Plan != plan.Plan.ID {
			continue
		}
		entry := planning.Entry{Ref: planning.ContractRef(item.Contract.ID), Digest: item.Digest}
		for _, criterion := range item.Contract.Criteria {
			entry.Parts = append(entry.Parts, planning.CriterionPart(criterion))
		}
		entry.Parts = append(entry.Parts, orderParts(item.Contract)...)
		entries = append(entries, entry)
	}
	return entries, nil
}
