package control

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// consolidationActor is who the ledger names as the source of a check of a merged consolidation's base.
var consolidationActor = ledger.Actor{Kind: ledger.ActorSystem, ID: "consolidation"}

const (
	// consolidationRecheck is how long a check that did not find the design tip in the base waits before the next,
	// doubled for each check made since, up to consolidationRecheckMax, so a base nobody fixes costs a fetch a day.
	consolidationRecheck    = 10 * time.Minute
	consolidationRecheckMax = 24 * time.Hour
)

// mergedConsolidation is an adopt work order whose branch imported a design repository's history and merged, as
// the ledger holds it: the branch and the base it merged into, the design tip the plan approval listed, and the
// checks of the base made so far.
type mergedConsolidation struct {
	order  workorders.WorkOrder
	branch string
	base   string
	tip    string
	checks []ledger.Event
}

// last is the newest check of the base, and false when there is none.
func (m mergedConsolidation) last() (ledger.ConsolidationCheck, int64, bool) {
	if len(m.checks) == 0 {
		return ledger.ConsolidationCheck{}, 0, false
	}
	event := m.checks[len(m.checks)-1]
	var check ledger.ConsolidationCheck
	_ = json.Unmarshal(event.Data, &check)
	return check, event.RecordedAt, true
}

// due says the base is to be checked now: it never was, or the last check did not find the design tip and the wait
// after it has passed. A base found to contain the tip is never checked again.
func (m mergedConsolidation) due(now int64) bool {
	check, at, checked := m.last()
	if !checked {
		return true
	}
	if check.Contained {
		return false
	}
	wait := consolidationRecheck << min(len(m.checks)-1, 8)
	return now-at >= min(wait, consolidationRecheckMax).Milliseconds()
}

// mergedConsolidations reads every adopt work order that consolidated a design repository and whose merge the
// ledger records, by ropeshift or read from the forge. A branch at person_merges with review target `branch` merges
// with no record, and is not among them.
func mergedConsolidations(tx ports.ReadTx) ([]mergedConsolidation, error) {
	orders, err := tx.WorkOrders().List()
	if err != nil {
		return nil, err
	}
	var out []mergedConsolidation
	for _, order := range orders {
		if order.Kind != workorders.KindPlan || !order.Adopt || !order.Restructure.Consolidates() || order.State != workorders.Done {
			continue
		}
		integration, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectIntegration, ID: order.ID})
		if err != nil {
			return nil, err
		}
		merged := false
		for _, event := range integration {
			merged = merged || event.Type == ledger.EventIntegrationMerged
		}
		if !merged {
			continue
		}
		m := mergedConsolidation{order: order}
		artifacts, err := tx.Evidence().Artifacts(order.ID)
		if err != nil {
			return nil, err
		}
		m.branch = attemptBranch(artifacts, order.Fence).URI
		record, err := tx.Repositories().Repository(order.RepositoryID)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return nil, err
		}
		m.base = cmp.Or(order.Base, record.Base())
		if m.tip, err = approvedDesignTip(tx.Approvals(), order.ID); err != nil {
			return nil, err
		}
		if m.checks, err = tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectConsolidation, ID: order.ID}); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// approvedDesignTip is the design tip the approved plan approval of an adopt work order listed, from its newest
// approval that names one.
func approvedDesignTip(approvals ports.ApprovalReader, workOrder string) (string, error) {
	rows, err := approvals.OfWorkOrder(workOrder)
	if err != nil {
		return "", err
	}
	tip := ""
	for _, row := range rows {
		if row.Kind != authority.KindPlan || (row.State != authority.ApprovalApproved && row.State != authority.ApprovalUsed) {
			continue
		}
		if adopted := planDetailOf(row).Adoption; adopted != nil && adopted.Consolidation != nil && adopted.Consolidation.DesignTip != "" {
			tip = adopted.Consolidation.DesignTip
		}
	}
	return tip, nil
}

// CheckConsolidations checks, for each consolidation whose merge the ledger records, that the base branch contains
// the design repository's tip the adoption imported (FR-ADOPT-13): a squash or a rebase merge made outside ropeshift
// keeps the files and drops that history. It reads the base on this machine, through the checkout the worker keeps,
// fetched first, and records each check, so a base without the tip waits in Needs you until a later check finds it.
// A control plane with no repository reads checks nothing.
func (i *Intake) CheckConsolidations(ctx context.Context, scope ledger.WorkspaceID) error {
	if i.starts == nil {
		return nil
	}
	var due []mergedConsolidation
	err := i.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		merged, err := mergedConsolidations(tx)
		for _, m := range merged {
			if m.due(now) {
				due = append(due, m)
			}
		}
		return err
	})
	if err != nil {
		return err
	}
	var failed []error
	for _, m := range due {
		check := i.checkBase(ctx, scope, m)
		data, err := json.Marshal(check)
		if err == nil {
			err = i.store.Write(ctx, scope, "consolidation.check", func(tx ports.WriteTx) error {
				_, err := tx.Append(ledger.EventDraft{
					Type: ledger.EventConsolidationChecked, Actor: consolidationActor,
					Subject: ledger.Subject{Kind: ledger.SubjectConsolidation, ID: m.order.ID}, Data: data,
				})
				return err
			})
		}
		if err != nil {
			failed = append(failed, fmt.Errorf("consolidation %s: %w", m.order.ID, err))
		}
	}
	return errors.Join(failed...)
}

// checkBase reads whether a merged consolidation's base contains the design tip, or why it cannot say.
func (i *Intake) checkBase(ctx context.Context, scope ledger.WorkspaceID, m mergedConsolidation) ledger.ConsolidationCheck {
	check := ledger.ConsolidationCheck{Branch: m.branch, Base: m.base, DesignTip: m.tip}
	if m.tip == "" {
		check.Unread = "the plan approval names no design tip, because the adoption branch could not be read when it was raised"
		return check
	}
	path, err := i.starts.Checkout(ctx, scope, m.order.RepositoryID)
	if err != nil {
		check.Unread = err.Error()
		return check
	}
	if check.Base == "" {
		if check.Base, err = i.starts.Bases.DefaultBranch(ctx, path); err != nil {
			check.Unread = fmt.Sprintf("git could not name the base branch: %v", err)
			return check
		}
	}
	tip, err := i.starts.Bases.BaseTip(ctx, path, check.Base)
	if err != nil {
		check.Unread = fmt.Sprintf("git could not read %s: %v", check.Base, err)
		return check
	}
	check.BaseCommit = tip.Commit
	contained, err := i.starts.Bases.Contains(ctx, path, m.tip, tip.Commit)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		// A repository that does not hold the tip at all cannot hold it in its base.
	case err != nil:
		check.Unread = fmt.Sprintf("git could not compare %s with %s: %v", check.Base, m.tip, err)
	default:
		check.Contained = contained
	}
	return check
}

// HistoryGap is a merged consolidation whose base was last found without the design tip the adoption imported, or
// could not be checked: the work order, the check, and when the base was first found that way.
type HistoryGap struct {
	WorkOrder workorders.WorkOrder
	Check     ledger.ConsolidationCheck
	Since     int64
}

// Unread says the base could not be checked, rather than found without the tip.
func (g HistoryGap) Unread() bool { return g.Check.Unread != "" }

// Detail says what is missing and how to bring it back, naming the branch and the commit, or why the base could not
// be checked.
func (g HistoryGap) Detail() string {
	if g.Unread() {
		return fmt.Sprintf("Branch %s merged into %s, and ropeshift could not check that %s contains the design history the adoption imported: %s. "+
			"It checks again later, after twice as long each time, up to once a day. If %s lacks that history, merge the design repository's tip into it with a merge commit.",
			g.Check.Branch, cmp.Or(g.Check.Base, "its base"), cmp.Or(g.Check.Base, "the base"), g.Check.Unread, cmp.Or(g.Check.Base, "the base"))
	}
	return fmt.Sprintf("Branch %s merged into %s, and %s at %s does not contain %s, the design repository's tip that the adoption imported: "+
		"the merge kept the files and dropped the design history, as a squash or a rebase merge does. %s "+
		"Merge %s into %s with a merge commit to bring the history back; this clears once %s contains it.",
		g.Check.Branch, g.Check.Base, g.Check.Base, g.Check.BaseCommit, g.Check.DesignTip, planning.MergeCommitKeepsHistory,
		g.Check.DesignTip, g.Check.Base, g.Check.Base)
}

// HistoryGaps lists every merged consolidation whose newest check found the base without the design tip, or could
// not check it, which Needs you lists for a member, and the notices with it. Each is dated from the first check of
// the checks since that found the base the same way.
func HistoryGaps(tx ports.ReadTx) ([]HistoryGap, error) {
	merged, err := mergedConsolidations(tx)
	if err != nil {
		return nil, err
	}
	var gaps []HistoryGap
	for _, m := range merged {
		check, _, checked := m.last()
		if !checked || check.Contained {
			continue
		}
		gap := HistoryGap{WorkOrder: m.order, Check: check}
		for _, event := range slices.Backward(m.checks) {
			var earlier ledger.ConsolidationCheck
			if json.Unmarshal(event.Data, &earlier) != nil || earlier.Missing() != check.Missing() {
				break
			}
			gap.Since = event.RecordedAt
		}
		gaps = append(gaps, gap)
	}
	return gaps, nil
}
