package control

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// opSpendRaiseProject is the operation of raising a project's daily or weekly limit.
const opSpendRaiseProject = "spend.raise_project"

// projectReadsOf and projectWritesOf are the part of a transaction a project's hold reads: the tally, the policy, the
// events of the project's limits, and the clock. They hold no handle on the estimates, so a claim, an offer, and a wake
// read the hold in a transaction that cannot reach one (FR-EST-12), which the contract suite proves with a store that
// panics at an estimate.
func projectReadsOf(tx ports.ReadTx) spendReads {
	return spendReads{lanes: tx.Lanes(), policies: tx.Policies(), events: tx.EventsAbout, now: tx.Now}
}

func projectWritesOf(tx ports.WriteTx) spendReads {
	return spendReads{lanes: tx.Lanes(), policies: tx.Policies(), events: tx.EventsAbout, now: func() (int64, error) { return tx.Now(), nil }}
}

// projectLimitsOf reads the project keys a daily or weekly limit reads.
func projectLimitsOf(policy authority.SpendPolicy) ledger.ProjectLimits {
	return ledger.ProjectLimits{Daily: policy.ProjectDailyLimit, Weekly: policy.ProjectWeeklyLimit, Warn: policy.ProjectWarnShare}
}

// projectSpendUnder reads where a project's spend stands in the day and the week the ledger's clock is in, under a
// policy: what its lanes were charged since each window began, against the limits, with the highest limit a member
// raised each window to. A project with no limit holds nothing back, so a read that wants only the hold passes false
// and reads no spend.
func (r spendReads) projectSpendUnder(policy authority.SpendPolicy, whole bool) (ledger.ProjectSpend, error) {
	now, err := r.now()
	if err != nil {
		return ledger.ProjectSpend{}, err
	}
	limits := projectLimitsOf(policy)
	if !whole && limits.Daily <= 0 && limits.Weekly <= 0 {
		return limits.Spend(now, 0, 0, 0, 0), nil
	}
	day, err := r.lanes.SpendSince(ledger.SpendScopeWork, ledger.WindowStart(ledger.WindowDay, now))
	if err != nil {
		return ledger.ProjectSpend{}, err
	}
	week, err := r.lanes.SpendSince(ledger.SpendScopeWork, ledger.WindowStart(ledger.WindowWeek, now))
	if err != nil {
		return ledger.ProjectSpend{}, err
	}
	events, err := r.events(ledger.Subject{Kind: ledger.SubjectProjectSpend, ID: projectLimitsSubject})
	if err != nil {
		return ledger.ProjectSpend{}, err
	}
	return limits.Spend(now, day, week,
		ledger.RaisedProjectLimit(events, ledger.WindowDay, now), ledger.RaisedProjectLimit(events, ledger.WindowWeek, now)), nil
}

// projectLimitsSubject is the one subject the events about a project's limits are about.
const projectLimitsSubject = "limits"

// projectHold reads whether the project is past a daily or weekly limit now, and where its spend stands. It reads no
// estimate, band, or work order: a hold is about what the project spent, and no claim or admission decision may read
// an estimate (FR-EST-12).
func (r spendReads) projectHold() (*ledger.ProjectHold, ledger.ProjectSpend, error) {
	policy, err := spendPolicyInForce(r.policies)
	if err != nil {
		return nil, ledger.ProjectSpend{}, err
	}
	spend, err := r.projectSpendUnder(policy, false)
	if err != nil {
		return nil, ledger.ProjectSpend{}, err
	}
	return spend.Hold(), spend, nil
}

// projectHeld is the refusal a claim meets while the project is past a limit, and nil while it is not. It is one the
// member can act on: it names the limit, how far the project came, when work goes on by itself, and the raise.
func (r spendReads) projectHeld() error {
	hold, spend, err := r.projectHold()
	if err != nil || hold == nil {
		return err
	}
	return fmt.Errorf("%w: %w", workorders.ErrIneligible, ledger.ProjectLimitErrorOf(spend, *hold))
}

// ProjectSpend reads where a project's spend stands against its daily and weekly limits, for a member (FR-SPEND-6).
func (s *Spend) ProjectSpend(ctx context.Context, scope ledger.WorkspaceID) (ledger.ProjectSpend, error) {
	var out ledger.ProjectSpend
	err := s.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		reads := spendReadsOf(tx)
		policy, err := spendPolicyInForce(reads.policies)
		if err != nil {
			return err
		}
		out, err = reads.projectSpendUnder(policy, true)
		return err
	})
	return out, err
}

// ProjectRaise asks to raise a project's daily or weekly limit, by a percentage of the limit in force or to an amount. It
// names one of the two, as a work order's raise does.
type ProjectRaise struct {
	Window    string
	ByPercent float64
	ToUSD     float64
	// WindowStart is the start of the window the raise was asked for, which a card a delegate raised carries so that a
	// confirmation that comes in a later day or week does not lift that one with an amount read in the earlier. Zero asks for
	// the window the raise is made in.
	WindowStart int64
}

// RaiseProject lifts a project's daily or weekly limit for the window it is made in, records the limit before and
// after, and wakes the lanes the project's limits parked, in one transaction (FR-SPEND-8, FR-PARK-2). It is a work
// order's raise on a project: a member's decision, held to the same action, a card for a delegate that no delegate
// policy exempts, and never an orchestrator's. It ends with the window, so the next day or week is back to the policy's
// limit, because a limit that stays raised is a setting, and `spend set` changes a setting.
func (s *Spend) RaiseProject(ctx context.Context, scope ledger.WorkspaceID, in ProjectRaise, actor ledger.Actor) (ledger.ProjectLimitChange, error) {
	if !authority.Allowed(actor, authority.ActionRaiseSpendLimit) {
		return ledger.ProjectLimitChange{}, workorders.ErrForbidden
	}
	raise := SpendRaise{ByPercent: in.ByPercent, ToUSD: in.ToUSD}
	if !ledger.ValidWindow(in.Window) || in.ByPercent < 0 || in.ToUSD < 0 || (in.ByPercent > 0) == (in.ToUSD > 0) {
		return ledger.ProjectLimitChange{}, fmt.Errorf("%w: a raise names the day or the week and either a percentage or an amount", workorders.ErrInvalid)
	}
	var change ledger.ProjectLimitChange
	err := s.store.Write(ctx, scope, opSpendRaiseProject, func(tx ports.WriteTx) error {
		reads := spendWritesOf(tx)
		policy, err := spendPolicyInForce(reads.policies)
		if err != nil {
			return err
		}
		spend, err := reads.projectSpendUnder(policy, true)
		if err != nil {
			return err
		}
		window := spend.Day
		if in.Window == ledger.WindowWeek {
			window = spend.Week
		}
		name := "daily"
		if in.Window == ledger.WindowWeek {
			name = "weekly"
		}
		if in.WindowStart != 0 && in.WindowStart != window.StartsAt {
			return fmt.Errorf("%w: the %s that began at %s is over, and this raise was asked for it: the %s that began at %s is the one in force, so ask again",
				workorders.ErrInvalid, in.Window, time.UnixMilli(in.WindowStart).UTC().Format(time.RFC3339), in.Window,
				time.UnixMilli(window.StartsAt).UTC().Format(time.RFC3339))
		}
		if window.HardLimit <= 0 {
			return fmt.Errorf("%w: the project has no %s spend limit to raise", workorders.ErrInvalid, name)
		}
		after := raise.LimitFrom(window.HardLimit)
		if after <= window.SpentUSD {
			return fmt.Errorf("%w: a %s limit of %s would stay below the %s charged this %s, so raise it to more than that",
				workorders.ErrInvalid, name, usd(after), usd(window.SpentUSD), in.Window)
		}
		if after <= window.HardLimit || after > maxSpendAmount {
			return fmt.Errorf("%w: a %s limit of %s cannot be raised to %s, which must be higher and at most %s",
				workorders.ErrInvalid, name, usd(window.HardLimit), usd(after), usd(maxSpendAmount))
		}
		change = ledger.ProjectLimitChange{
			Window: in.Window, WindowStart: window.StartsAt, BeforeUSD: window.HardLimit, AfterUSD: after, SpentUSD: window.SpentUSD,
			ByPercent: in.ByPercent, ToUSD: in.ToUSD, PolicyVersion: policy.Version,
		}
		data, err := json.Marshal(change)
		if err != nil {
			return err
		}
		if _, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventSpendProjectLimitRaised, Actor: actor, Data: data,
			Subject: ledger.Subject{Kind: ledger.SubjectProjectSpend, ID: projectLimitsSubject},
		}); err != nil {
			return err
		}
		_, err = fireWakes(tx, "limit:project:"+in.Window, actor, parking.LimitRaised)
		return err
	})
	if err != nil {
		return ledger.ProjectLimitChange{}, err
	}
	return change, nil
}
