package contract

import (
	"errors"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ProjectHoldChecks are what a project past its daily or weekly limit does (FR-SPEND-6): it grants no claim, its lanes
// read a hold, a member raises the window's limit with the authority a work order's raise has, and a lane the limit
// parked wakes once when the window ends or the limit is raised.
var ProjectHoldChecks = []Check{
	{"PJ3", "a project past its daily limit grants no claim until the day ends or a member raises the day's limit, and the raise ends with the day", checkADailyLimitHoldsClaims},
	{"PJ4", "a project past its weekly limit grants no claim until the week ends", checkAWeeklyLimitHoldsClaims},
	{"PJ5", "a member raises a project window's limit with the limit before and after recorded, and nobody else may", checkRaiseAProjectLimit},
	{"PJ6", "a lane a project's limit parked wakes once when the day ends, and once when a member raises the limit, and never twice", checkAProjectLimitWakesTheLaneOnce},
	{"PJ7", "a worker's allowance carries the project's hold and says the project is limited, with no limit on the work order itself", checkTheAllowanceCarriesTheHold},
}

const dayMs = int64(24 * time.Hour / time.Millisecond)

// projectPolicy publishes a spend policy that sets the keys given, as a new version.
func (r *Rig) projectPolicy(store ports.Store, version int64, pairs ...string) error {
	policy := authority.DefaultSpendPolicy()
	for i := 0; i+1 < len(pairs); i += 2 {
		var err error
		if policy, err = policy.Set(pairs[i], pairs[i+1]); err != nil {
			return err
		}
	}
	policy.Version = version
	_, err := control.NewSpend(store).PublishSpendPolicy(r.Ctx, r.Scope, authority.SpendPolicyDraft{Body: policy.Body(), Policy: policy}, theMember)
	return err
}

// projectSpent adds to what the project's lanes were charged, at the moment the clock is at, as a usage report does.
func (r *Rig) projectSpent(store ports.Store, usd float64) error {
	return store.Write(r.Ctx, r.Scope, "spend.tally", func(tx ports.WriteTx) error {
		if _, err := tx.Append(r.noteDraft(r.ID("note"))); err != nil {
			return err
		}
		return tx.Lanes().AddSpend(ledger.SpendScopeWork, usd)
	})
}

// freezeAtNoon stops the clock at midday of the UTC day the database is in, so the day and the week a check reads
// cannot end under it, and says when that is.
func (r *Rig) freezeAtNoon(store ports.Store) (int64, error) {
	now, err := r.Clock(store)
	if err != nil {
		return 0, err
	}
	noon := ledger.WindowStart(ledger.WindowDay, now) + dayMs/2
	r.DB.FreezeClock(noon)
	return noon, nil
}

// claimRefusal claims a ready work order on a new worker and returns the refusal, nil for a grant.
func (r *Rig) claimRefusal(store ports.Store) (*ledger.ProjectLimitError, error) {
	id, err := r.Ready(store)
	if err != nil {
		return nil, err
	}
	worker := r.ID("w")
	_, err = r.NewOps(store).Claim(r.Ctx, r.Scope, r.Request(id, worker), r.worker(worker))
	if err == nil {
		return nil, nil
	}
	var limit *ledger.ProjectLimitError
	if !errors.Is(err, workorders.ErrIneligible) || !errors.As(err, &limit) {
		return nil, Violatedf("a claim was refused for another reason than the project's limit: %v", err)
	}
	return limit, nil
}

func checkADailyLimitHoldsClaims(r *Rig) error {
	if r.DB.FreezeClock == nil {
		return nil
	}
	defer r.DB.FreezeClock(0)
	s := r.Store()
	noon, err := r.freezeAtNoon(s)
	if err != nil {
		return err
	}
	// A project with no limit set holds nobody, whatever it spent.
	if err := r.projectSpent(s, 1e6); err != nil {
		return err
	}
	if refusal, err := r.claimRefusal(s); err != nil || refusal != nil {
		return Violatedf("a project with no limit refused a claim: %v, %v", refusal, err)
	}
	// A new day: a limit of 10 over what the day has spent so far.
	r.DB.FreezeClock(noon + dayMs)
	if err := r.projectPolicy(s, 2, "project_daily_limit", "10"); err != nil {
		return err
	}
	if err := r.projectSpent(s, 9.99); err != nil {
		return err
	}
	if refusal, err := r.claimRefusal(s); err != nil || refusal != nil {
		return Violatedf("a claim at $9.99 of a $10 day was refused: %v, %v", refusal, err)
	}
	if err := r.projectSpent(s, 0.01); err != nil {
		return err
	}
	refusal, err := r.claimRefusal(s)
	if err != nil {
		return err
	}
	if refusal == nil || refusal.Window != ledger.WindowDay || !near(refusal.HardUSD, 10) || !near(refusal.SpentUSD, 10) || refusal.UntilMs != noon+dayMs+dayMs/2 {
		return Violatedf("a claim at the daily limit was answered %+v, want a refusal that names the day, the limit, and when it ends", refusal)
	}
	if !strings.Contains(refusal.Error(), "ropeshift spend raise --project day") {
		return Violatedf("the refusal %q does not say how to go on", refusal.Error())
	}
	// A member raises the day's limit, and claims go on.
	spend := control.NewSpend(s)
	if _, err := spend.RaiseProject(r.Ctx, r.Scope, control.ProjectRaise{Window: ledger.WindowDay, ToUSD: 20}, theMember); err != nil {
		return err
	}
	if refusal, err := r.claimRefusal(s); err != nil || refusal != nil {
		return Violatedf("a claim under a limit raised to $20 was refused: %v, %v", refusal, err)
	}
	if err := r.projectSpent(s, 10); err != nil {
		return err
	}
	if refusal, err := r.claimRefusal(s); err != nil || refusal == nil || !near(refusal.HardUSD, 20) {
		return Violatedf("a claim at the raised limit was answered %v, %v", refusal, err)
	}
	// The next day starts from nothing, and the raise ended with the day it was made in.
	r.DB.FreezeClock(noon + 2*dayMs)
	if refusal, err := r.claimRefusal(s); err != nil || refusal != nil {
		return Violatedf("a claim the day after was refused: %v, %v", refusal, err)
	}
	if err := r.projectSpent(s, 10); err != nil {
		return err
	}
	if refusal, err := r.claimRefusal(s); err != nil || refusal == nil || !near(refusal.HardUSD, 10) {
		return Violatedf("the day after a raise the limit is %v, want the policy's $10: %v", refusal, err)
	}
	return nil
}

func checkAWeeklyLimitHoldsClaims(r *Rig) error {
	if r.DB.FreezeClock == nil {
		return nil
	}
	defer r.DB.FreezeClock(0)
	s := r.Store()
	noon, err := r.freezeAtNoon(s)
	if err != nil {
		return err
	}
	if err := r.projectPolicy(s, 2, "project_weekly_limit", "40"); err != nil {
		return err
	}
	if err := r.projectSpent(s, 40); err != nil {
		return err
	}
	until := ledger.WindowEnd(ledger.WindowWeek, noon)
	refusal, err := r.claimRefusal(s)
	if err != nil {
		return err
	}
	if refusal == nil || refusal.Window != ledger.WindowWeek || !near(refusal.HardUSD, 40) || refusal.UntilMs != until {
		return Violatedf("a claim at the weekly limit was answered %+v, want a refusal that names the week and its end %d", refusal, until)
	}
	// Every later day of the week is held too, because the week's spend does not leave with the day.
	for _, day := range []int64{1, 3, 6} {
		now := noon + day*dayMs
		if now >= until {
			break
		}
		r.DB.FreezeClock(now)
		if refusal, err := r.claimRefusal(s); err != nil || refusal == nil || refusal.Window != ledger.WindowWeek {
			return Violatedf("a claim %d days on, in the same week, was answered %v, %v", day, refusal, err)
		}
	}
	r.DB.FreezeClock(until)
	if refusal, err := r.claimRefusal(s); err != nil || refusal != nil {
		return Violatedf("a claim in the first millisecond of the next week was refused: %v, %v", refusal, err)
	}
	return nil
}

func checkRaiseAProjectLimit(r *Rig) error {
	if r.DB.FreezeClock == nil {
		return nil
	}
	defer r.DB.FreezeClock(0)
	s := r.Store()
	spend := control.NewSpend(s)
	noon, err := r.freezeAtNoon(s)
	if err != nil {
		return err
	}
	invalid := func(why string, in control.ProjectRaise) error {
		if _, err := spend.RaiseProject(r.Ctx, r.Scope, in, theMember); !errors.Is(err, workorders.ErrInvalid) {
			return Violatedf("%s: the raise %+v was answered %v, want a refusal as invalid", why, in, err)
		}
		return nil
	}
	// A limit the policy leaves unset has nothing to raise.
	if err := invalid("no limit is set", control.ProjectRaise{Window: ledger.WindowDay, ToUSD: 50}); err != nil {
		return err
	}
	if err := r.projectPolicy(s, 2, "project_daily_limit", "10", "project_weekly_limit", "50"); err != nil {
		return err
	}
	if err := r.projectSpent(s, 12); err != nil {
		return err
	}
	for why, in := range map[string]control.ProjectRaise{
		"below what was spent": {Window: ledger.WindowDay, ToUSD: 12},
		"not above the limit":  {Window: ledger.WindowDay, ToUSD: 10},
		"both ways":            {Window: ledger.WindowDay, ToUSD: 30, ByPercent: 50},
		"neither way":          {Window: ledger.WindowDay},
		"a negative share":     {Window: ledger.WindowDay, ByPercent: -5},
		"a window of no name":  {ToUSD: 30},
		"a month":              {Window: "month", ToUSD: 30},
		"past the largest":     {Window: ledger.WindowDay, ToUSD: 2e6},
	} {
		if err := invalid(why, in); err != nil {
			return err
		}
	}
	change, err := spend.RaiseProject(r.Ctx, r.Scope, control.ProjectRaise{Window: ledger.WindowDay, ByPercent: 100}, theMember)
	if err != nil {
		return err
	}
	if change.Window != ledger.WindowDay || change.WindowStart != ledger.WindowStart(ledger.WindowDay, noon) || !near(change.BeforeUSD, 10) ||
		!near(change.AfterUSD, 20) || !near(change.SpentUSD, 12) || change.ByPercent != 100 || change.PolicyVersion != 2 {
		return Violatedf("raising a daily limit of 10 by 100 percent recorded %+v", change)
	}
	state, err := spend.ProjectSpend(r.Ctx, r.Scope)
	if err != nil || !near(state.Day.HardLimit, 20) || !near(state.Day.RaisedTo, 20) || !near(state.Day.SpentUSD, 12) || !near(state.Week.HardLimit, 50) || state.Week.RaisedTo != 0 {
		return Violatedf("after the raise the project reads %+v: %v", state, err)
	}
	// A second raise starts from the limit in force, and a new version of the policy never takes a raise back.
	if err := r.projectPolicy(s, 3, "project_daily_limit", "5", "project_weekly_limit", "50"); err != nil {
		return err
	}
	second, err := spend.RaiseProject(r.Ctx, r.Scope, control.ProjectRaise{Window: ledger.WindowDay, ByPercent: 50}, theMember)
	if err != nil || !near(second.BeforeUSD, 20) || !near(second.AfterUSD, 30) {
		return Violatedf("raising a limit of 20 by half recorded %+v: %v", second, err)
	}
	week, err := spend.RaiseProject(r.Ctx, r.Scope, control.ProjectRaise{Window: ledger.WindowWeek, ToUSD: 90}, theMember)
	if err != nil || week.Window != ledger.WindowWeek || week.WindowStart != ledger.WindowStart(ledger.WindowWeek, noon) || !near(week.BeforeUSD, 50) {
		return Violatedf("raising the weekly limit to 90 recorded %+v: %v", week, err)
	}
	// Only a member raises a limit, and the refusals changed nothing.
	for _, actor := range []ledger.Actor{
		{Kind: ledger.ActorOrchestrator, ID: "orchestrator-1"}, {Kind: ledger.ActorLane, ID: "lane-1"}, {Kind: ledger.ActorWorker, ID: "worker-1"},
		{Kind: ledger.ActorSystem, ID: "tick"}, authority.DelegateActor("member-1", "dlg_1"),
	} {
		if _, err := spend.RaiseProject(r.Ctx, r.Scope, control.ProjectRaise{Window: ledger.WindowDay, ToUSD: 200}, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("a %s's raise was answered %v, want a refusal as forbidden", actor.Kind, err)
		}
	}
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	raises := 0
	for _, e := range events {
		if e.Type == ledger.EventSpendProjectLimitRaised {
			raises++
		}
	}
	if raises != 3 {
		return Violatedf("the ledger holds %d project raises, want 3", raises)
	}
	return nil
}

// parkedByTheProject parks a lane the way a worker does once a project's limit holds it, before the project is past
// the limit, so the claim is granted, and spends the project to its limit afterwards.
func (r *Rig) parkedByTheProject(store ports.Store, until int64, spend float64) (ParkRequest, error) {
	id, err := r.Ready(store)
	if err != nil {
		return ParkRequest{}, err
	}
	request, err := r.parkingOn(store, id, lanes.ProjectLimitWake(until))
	if err != nil {
		return ParkRequest{}, err
	}
	if err := r.projectSpent(store, spend); err != nil {
		return ParkRequest{}, err
	}
	fired, err := r.NewOps(store).CommitParked(r.Ctx, r.Scope, request.Holder, PushedCommit(request.Wake), r.worker("a"))
	if err != nil || fired != "" {
		return ParkRequest{}, Violatedf("a lane a project limit parked woke at its parked commit: %q, %v", fired, err)
	}
	return request, nil
}

func checkAProjectLimitWakesTheLaneOnce(r *Rig) error {
	if r.DB.FreezeClock == nil {
		return nil
	}
	defer r.DB.FreezeClock(0)
	s := r.Store()
	wakes := control.NewWakes(s)
	noon, err := r.freezeAtNoon(s)
	if err != nil {
		return err
	}
	if err := r.projectPolicy(s, 2, "project_daily_limit", "10"); err != nil {
		return err
	}
	midnight := ledger.WindowEnd(ledger.WindowDay, noon)
	tick := func() error {
		_, err := wakes.WakeTick(r.Ctx, r.Scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "tick"})
		return err
	}
	// The day ends: the lane wakes once, and ticks after it wake nothing again.
	byDay, err := r.parkedByTheProject(s, midnight, 10)
	if err != nil {
		return err
	}
	if err := tick(); err != nil {
		return err
	}
	if n, err := r.firings(s, byDay.Wake); err != nil || n != 0 {
		return Violatedf("the wake fired %d times at noon, with the day still to end: %v", n, err)
	}
	r.DB.FreezeClock(midnight)
	for range 3 {
		if err := tick(); err != nil {
			return err
		}
	}
	if n, err := r.firings(s, byDay.Wake); err != nil || n != 1 {
		return Violatedf("the wake fired %d times when the day ended, want once: %v", n, err)
	}
	if err := r.state(s, byDay.Holder.Lease.WorkOrder, workorders.Ready); err != nil {
		return err
	}
	// A raise: the lane of the new day wakes once, at the raise, and the day's end finds it woken already.
	nextNoon := midnight + dayMs/2
	r.DB.FreezeClock(nextNoon)
	byRaise, err := r.parkedByTheProject(s, midnight+dayMs, 10)
	if err != nil {
		return err
	}
	if n, err := r.firings(s, byRaise.Wake); err != nil || n != 0 {
		return Violatedf("the wake of a lane parked behind a held project fired %d times before anything changed: %v", n, err)
	}
	if _, err := control.NewSpend(s).RaiseProject(r.Ctx, r.Scope, control.ProjectRaise{Window: ledger.WindowDay, ToUSD: 20}, theMember); err != nil {
		return err
	}
	if n, err := r.firings(s, byRaise.Wake); err != nil || n != 1 {
		return Violatedf("a raise to $20 over a day of $10 woke the lane %d times, want once: %v", n, err)
	}
	r.DB.FreezeClock(midnight + dayMs)
	for range 2 {
		if err := tick(); err != nil {
			return err
		}
	}
	if n, err := r.firings(s, byRaise.Wake); err != nil || n != 1 {
		return Violatedf("the lane a raise woke was woken %d times in all, want once: %v", n, err)
	}
	// A raise of one window leaves the project held by the other, and wakes nothing, because a lane that woke could
	// not be claimed. The raise of the other lets it go on.
	if err := r.projectPolicy(s, 3, "project_daily_limit", "10", "project_weekly_limit", "30"); err != nil {
		return err
	}
	r.DB.FreezeClock(midnight + dayMs + dayMs/2)
	stillHeld, err := r.parkedByTheProject(s, midnight+2*dayMs, 35)
	if err != nil {
		return err
	}
	spend := control.NewSpend(s)
	if _, err := spend.RaiseProject(r.Ctx, r.Scope, control.ProjectRaise{Window: ledger.WindowDay, ToUSD: 36}, theMember); err != nil {
		return err
	}
	if n, err := r.firings(s, stillHeld.Wake); err != nil || n != 0 {
		return Violatedf("a daily limit raised above the day's spend woke the lane %d times, with the week still past its limit: %v", n, err)
	}
	if _, err := spend.RaiseProject(r.Ctx, r.Scope, control.ProjectRaise{Window: ledger.WindowWeek, ToUSD: 100}, theMember); err != nil {
		return err
	}
	if n, err := r.firings(s, stillHeld.Wake); err != nil || n != 1 {
		return Violatedf("raising the week's limit as well woke the lane %d times, want once: %v", n, err)
	}
	return nil
}

func checkTheAllowanceCarriesTheHold(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	id := s.grant.WorkOrder
	read := func() (*ledger.SpendAllowance, error) {
		facts, err := s.plane.LaneFacts(r.Ctx, ledger.LaneFactsRequest{Auth: s.auth, Epoch: s.epoch, WorkOrder: id, Fence: s.grant.Fence, Lane: s.lane})
		return facts.Spend, err
	}
	// The work order has no limit of its own, and the project has none either: nothing holds the lane.
	if err := r.projectPolicy(s.store, 2, "no_history_limit.medium", "0"); err != nil {
		return err
	}
	if allowance, err := read(); err != nil || allowance != nil {
		return Violatedf("a lane with no limit anywhere reads an allowance of %+v: %v", allowance, err)
	}
	// A project limit alone gives the worker an allowance to keep, and says the project is limited, not held.
	if err := r.projectPolicy(s.store, 3, "no_history_limit.medium", "0", "project_daily_limit", "10"); err != nil {
		return err
	}
	allowance, err := read()
	if err != nil || allowance == nil || !allowance.ProjectLimited || allowance.ProjectHold != nil || allowance.HardUSD != 0 {
		return Violatedf("a project under its limit reads an allowance of %+v: %v", allowance, err)
	}
	// Past it, the allowance carries the hold and when it ends, which is a midnight.
	if err := r.projectSpent(s.store, 10); err != nil {
		return err
	}
	now, err := r.Clock(s.store)
	if err != nil {
		return err
	}
	if allowance, err = read(); err != nil || allowance == nil || allowance.ProjectHold == nil || allowance.ProjectHold.Window != ledger.WindowDay ||
		allowance.ProjectHold.UntilMs < now-60_000 || allowance.ProjectHold.UntilMs > now+dayMs+60_000 || allowance.ProjectHold.UntilMs%dayMs != 0 || !allowance.Held() {
		return Violatedf("a project at its limit reads an allowance of %+v: %v", allowance, err)
	}
	if _, err := control.NewSpend(s.store).RaiseProject(r.Ctx, r.Scope, control.ProjectRaise{Window: ledger.WindowDay, ToUSD: 30}, theMember); err != nil {
		return err
	}
	if allowance, err = read(); err != nil || allowance == nil || allowance.ProjectHold != nil || !allowance.ProjectLimited {
		return Violatedf("after a raise the allowance reads %+v: %v", allowance, err)
	}
	return nil
}
