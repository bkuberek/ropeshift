package control

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
)

// opPlanEstimatesTold is the transaction that sends a planning lane the estimates and forecasts of a version it
// submitted.
const opPlanEstimatesTold = "plan.estimates_told"

// estimateNotice is the kind of notice a planning lane is sent, and the ID of one derives from it with the version
// and the lane, so a retry or a later tick sends it once.
const estimateNotice = "plan-estimates"

// plannerActor is who tells a planning lane: the control plane, as the forecast tick.
var plannerActor = ledger.Actor{Kind: ledger.ActorSystem, ID: "forecast"}

// Telling sets how long the notices this sends stay useful, as intake's notices are set. A kind nobody set takes the
// default.
func (f *Forecasts) Telling(windows messages.Windows) *Forecasts {
	f.windows = windows
	return f
}

// Reporting sets where a notice that failed to send is told of, which is all a failed notice gets: the next tick sends
// it again, and no other notice waits for it.
func (f *Forecasts) Reporting(report func(error)) *Forecasts {
	f.report = report
	return f
}

// owedNotice is a planning lane that has not been told of the plan version it submitted: the plan, its version, and
// the lane.
type owedNotice struct {
	plan    string
	version int64
	lane    string
}

// tell sends each planning lane whose session is open the estimates and forecasts of the newest version of a plan
// it submitted, once the forecast tick has forecast that version (FR-EST-12). A version is the planning lane's when
// intake recorded it from that lane's accepted work, so a version read from files is no lane's. It sends through the
// injection path, as a notice, and only to a lane whose generation is open: a lane that ended has nobody to
// read it, and its work order's estimates stay on the ledger for any surface to show. The lane's own work order
// decides who it is, never the lane's word, so no change, rework, or review lane and no orchestrator is ever told one.
func (f *Forecasts) tell(ctx context.Context, scope ledger.WorkspaceID) error {
	var owed []owedNotice
	err := f.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		owed, err = owedNotices(tx)
		return err
	})
	if err != nil {
		return f.failed(ctx, fmt.Errorf("the notices owed to planning lanes could not be read: %w", err))
	}
	for _, notice := range owed {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := f.store.Write(ctx, scope, opPlanEstimatesTold, func(tx ports.WriteTx) error { return tellPlanner(tx, notice, f.windows) })
		if err != nil {
			// One lane's notice that cannot be sent holds back no other, and the next tick sends it again.
			if err := f.failed(ctx, fmt.Errorf("the notice of plan %s version %d to lane %s was not sent: %w", notice.plan, notice.version, notice.lane, err)); err != nil {
				return err
			}
		}
	}
	return nil
}

// failed reports a notice that could not be sent and returns nil, so the tick that forecast the plans goes on, unless
// the context ended, which stops it.
func (f *Forecasts) failed(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if f.report != nil {
		f.report(err)
	}
	return nil
}

// owedNotices reads each plan's newest version a planning lane submitted that the tick has forecast, and the lanes of
// that work whose session is open and that were not told of it.
func owedNotices(tx ports.ReadTx) ([]owedNotice, error) {
	plans, err := tx.Planning().Plans()
	if err != nil {
		return nil, err
	}
	rows, err := tx.Lanes().Rows()
	if err != nil {
		return nil, err
	}
	var owed []owedNotice
	for _, plan := range plans {
		version, err := tx.Planning().LatestPlanVersion(plan.ID)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if version.WorkOrder == "" {
			continue
		}
		order, err := tx.WorkOrders().Get(version.WorkOrder)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !seesEstimates(order.Kind) {
			continue
		}
		forecast, err := tx.Forecasts().Latest(plan.ID, version.Version)
		if err != nil || len(forecast) == 0 {
			if err != nil {
				return nil, err
			}
			continue
		}
		for _, row := range rows {
			if row.WorkOrder != order.ID {
				continue
			}
			told, err := estimatesTold(tx.Messages(), plan.ID, version.Version, row.ID)
			if err != nil || told {
				if err != nil {
					return nil, err
				}
				continue
			}
			generations, err := tx.Lanes().Generations(row.ID)
			if err != nil {
				return nil, err
			}
			if slices.ContainsFunc(generations, func(g ledger.Generation) bool { return g.Number == row.Generation && g.EndedAt == 0 }) {
				owed = append(owed, owedNotice{plan: plan.ID, version: version.Version, lane: row.ID})
			}
		}
	}
	return owed, nil
}

// estimatesTold says a lane was sent the notice of a plan version.
func estimatesTold(reader ports.MessageReader, plan string, version int64, lane string) (bool, error) {
	_, err := reader.Message(estimateNoticeID(plan, version, lane))
	if errors.Is(err, ports.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func estimateNoticeID(plan string, version int64, lane string) string {
	return messages.DerivedID(estimateNotice, plan, fmt.Sprint(version), lane)
}

// tellPlanner sends one planning lane the version's estimates and forecasts, as the approval of the version shows
// them: the latest forecasts the tick kept, with the estimate set they were made from, each with the label and the
// explanation stored with it. It re-reads everything in its own transaction, because the lane's session may have
// ended and a sibling tick may have told it since the read that found it owed, and it sends nothing then.
func tellPlanner(tx ports.WriteTx, owed owedNotice, windows messages.Windows) error {
	if told, err := estimatesTold(tx.Messages(), owed.plan, owed.version, owed.lane); err != nil || told {
		return err
	}
	row, err := tx.Lanes().Row(owed.lane)
	if err != nil {
		return err
	}
	if open, err := generationOpen(tx, messages.Target{Lane: owed.lane, Generation: row.Generation}); err != nil || !open {
		return err
	}
	set, forecasts, err := ApprovalShows(tx.Estimates(), tx.Forecasts(), owed.plan, owed.version)
	if err != nil || len(forecasts) == 0 {
		return err
	}
	record, err := tx.Planning().Plan(owed.plan)
	if err != nil {
		return err
	}
	titles := map[string]string{}
	if set != nil {
		for _, e := range set.Estimates {
			order, err := tx.WorkOrders().Get(e.WorkOrder)
			if err != nil {
				return err
			}
			titles[e.WorkOrder] = order.Title
		}
	}
	draft := messages.Draft{
		ID: estimateNoticeID(owed.plan, owed.version, owed.lane), To: messages.Target{Lane: owed.lane}, Kind: messages.KindNotice,
		Body: messages.Clip(delivery.VersionNotice(record.Title, owed.version, set, forecasts, titles)),
	}
	_, err = sendIn(tx, draft, windows, plannerActor)
	return err
}
