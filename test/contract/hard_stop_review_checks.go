package contract

import (
	"errors"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// reported is a generation's usage as a worker reports it, without the floor the store keeps beside it.
func reported(u ledger.GenerationUsage) ledger.GenerationUsage {
	u.FloorUSD = 0
	return u
}

// checkChargedSpendNeverFalls holds the charge of a generation to the highest it reached. A turn of unknown cost is charged
// at the mean of the known ones, so a known turn that costs little, such as the handoff that lands just before a lane's
// parked commit, lowers the mean and the charge with it, though no figure of what the generation recorded has gone down. A
// limit that stopped the work order would read as lifted, which wakes the lane it parked with no raise and no member's word.
// No act corrects a charge down, and so none does here.
func checkChargedSpendNeverFalls(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	id := s.grant.WorkOrder
	report := func(u ledger.GenerationUsage) error {
		reply, err := s.usage(u)
		if err == nil && len(reply.Rejected) != 0 {
			return Violatedf("a usage report was refused: %+v", reply.Rejected)
		}
		return err
	}
	charged := func() (float64, error) {
		spend, err := r.spendOf(s.store, id)
		return spend.Charged.ChargedUSD, err
	}
	// The stop: 30 over 3 known turns, and a stopped turn charged at their mean of 10.
	if err := report(ledger.GenerationUsage{CostUSD: 30, KnownTurns: 3, UnknownTurns: 1}); err != nil {
		return err
	}
	if got, err := charged(); err != nil || !near(got, 40) {
		return Violatedf("a stop recorded at 30 over 3 turns and 1 unknown charged %v: %v", got, err)
	}
	// A cheap known turn lands: 30.5 over 4 turns, which would charge the stopped turn at 7.625 and the work order 38.125.
	if err := report(ledger.GenerationUsage{CostUSD: 30.5, KnownTurns: 4, UnknownTurns: 1}); err != nil {
		return err
	}
	if got, err := charged(); err != nil || got < 40-1e-9 {
		return Violatedf("a cheap known turn after the stop took the charge from 40 to %v with no raise: %v", got, err)
	}
	generations, err := r.generationsOf(s.store, s.lane)
	if err != nil || len(generations) != 1 || reported(generations[0].Usage) != (ledger.GenerationUsage{CostUSD: 30.5, KnownTurns: 4, UnknownTurns: 1}) {
		return Violatedf("the cheap turn's own figures were not kept: %+v: %v", generations, err)
	}
	// The floor is the store's, never the reporter's: a report that names one raises nothing.
	if err := report(ledger.GenerationUsage{CostUSD: 30.5, KnownTurns: 4, UnknownTurns: 1, FloorUSD: 500}); err != nil {
		return err
	}
	if got, err := charged(); err != nil || got > 40+1e-9 {
		return Violatedf("a report that named a floor of 500 charged %v, and a floor is only ever what the store saw reported: %v", got, err)
	}
	spend, err := r.spendOf(s.store, id)
	if err != nil || !slices.Contains(spend.Conditions, ledger.ConditionSpendLimit) {
		return Violatedf("the work order reads %+v after the cheap turn, and a limit that stopped it is not lifted by one: %v", spend, err)
	}
	return nil
}

// checkUsageNeverLowers holds a generation's recorded spend to what it already was: a report that is smaller, from a
// worker that lost its meter or a retry that arrived late, would take a work order below its limit, which is the fact
// that wakes a lane parked on it, and a lane would go on past its limit with no member's word.
func checkUsageNeverLowers(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	id := s.grant.WorkOrder
	report := func(u ledger.GenerationUsage) error {
		reply, err := s.usage(u)
		if err == nil && len(reply.Rejected) != 0 {
			return Violatedf("a usage report was refused: %+v", reply.Rejected)
		}
		return err
	}
	if err := report(ledger.GenerationUsage{CostUSD: 30, KnownTurns: 3, UnknownTurns: 1}); err != nil {
		return err
	}
	for _, lower := range []ledger.GenerationUsage{{CostUSD: 10, KnownTurns: 1}, {CostUSD: 30, KnownTurns: 1, UnknownTurns: 0}, {}} {
		if err := report(lower); err != nil {
			return err
		}
		generations, err := r.generationsOf(s.store, s.lane)
		if err != nil || len(generations) != 1 || reported(generations[0].Usage) != (ledger.GenerationUsage{CostUSD: 30, KnownTurns: 3, UnknownTurns: 1}) {
			return Violatedf("after a report of %+v the generation reads %+v, and a report never lowers what it spent: %v", lower, generations, err)
		}
	}
	if spend, err := r.spendOf(s.store, id); err != nil || !near(spend.Charged.RecordedUSD, 30) || spend.HardLimit == 0 || spend.Charged.ChargedUSD < spend.HardLimit {
		return Violatedf("after the lower reports the work order reads %+v: %v", spend, err)
	}
	// A higher figure in any one field is taken, field by field.
	if err := report(ledger.GenerationUsage{CostUSD: 31, KnownTurns: 2, UnknownTurns: 4}); err != nil {
		return err
	}
	if generations, err := r.generationsOf(s.store, s.lane); err != nil || reported(generations[0].Usage) != (ledger.GenerationUsage{CostUSD: 31, KnownTurns: 3, UnknownTurns: 4}) {
		return Violatedf("a report that raised two fields left the generation at %+v: %v", generations, err)
	}
	return nil
}

func checkTheAllowanceHoldsTheLanesEarlierGenerations(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	id := s.grant.WorkOrder
	if err := r.lanesWithUsage(s.store, id, []ledger.GenerationUsage{{CostUSD: 7, KnownTurns: 2}}); err != nil {
		return err
	}
	// The lane's first generation spent 20, and it runs its second, which has spent 1: a worker that never ran the first
	// holds nothing of it, and the ledger is where it reads it.
	err = s.store.Write(r.Ctx, r.Scope, "spend.fixture", func(tx ports.WriteTx) error {
		if _, err := tx.Append(r.noteDraft(r.ID("note"))); err != nil {
			return err
		}
		if _, err := tx.Lanes().ReportUsage(s.lane, 1, ledger.GenerationUsage{CostUSD: 20, KnownTurns: 2}); err != nil {
			return err
		}
		number, err := tx.Lanes().NextGeneration(s.lane)
		if err != nil {
			return err
		}
		if err := tx.Lanes().StartGeneration(ledger.Generation{Lane: s.lane, Number: number, StartReason: ledger.StartHandoff}); err != nil {
			return err
		}
		_, err = tx.Lanes().ReportUsage(s.lane, number, ledger.GenerationUsage{CostUSD: 1, KnownTurns: 1})
		return err
	})
	if err != nil {
		return err
	}
	facts, err := s.plane.LaneFacts(r.Ctx, ledger.LaneFactsRequest{Auth: s.auth, Epoch: s.epoch, WorkOrder: id, Fence: s.grant.Fence, Lane: s.lane})
	if err != nil {
		return err
	}
	allowance := facts.Spend
	if allowance == nil || !near(allowance.HardUSD, 25) || !near(allowance.OthersUSD, 7) || !near(allowance.LaneUSD, 20) {
		return Violatedf("the worker reads %+v, want a limit of 25, 7 spent by the other lanes, and 20 by this lane's earlier generations", allowance)
	}
	if !allowance.Crossed(allowance.LaneBefore(0) + 0) {
		return Violatedf("a lane that spent 20 over 7 of others' reads as under a limit of 25, and a worker that never ran it would not stop it")
	}
	return nil
}

func checkOnlyAMemberResumesALaneAtItsLimit(r *Rig) error {
	s := r.Store()
	wakes := control.NewWakes(s)
	delegate := authority.DelegateActor("member-1", "dlg_1")
	id, request, err := r.stopped(s, []ledger.GenerationUsage{{CostUSD: 30, KnownTurns: 3}})
	if err != nil {
		return err
	}
	if fired, err := r.NewOps(s).CommitParked(r.Ctx, r.Scope, request.Holder, PushedCommit(request.Wake), r.worker("a")); err != nil || fired != "" {
		return Violatedf("a lane parked at its hard limit woke at its parked commit: %q, %v", fired, err)
	}
	// A delegate holds the action that resumes a lane, and the wake that waits on a person would let it end a spend stop
	// with no card and no member: the limit is what the stop waits on, so only a member's word moves a lane past it.
	if fired, err := wakes.ResumeWork(r.Ctx, r.Scope, id, delegate); !errors.Is(err, workorders.ErrForbidden) || len(fired) != 0 {
		return Violatedf("a delegate's resume of work parked at its limit fired %v and was answered %v", fired, err)
	}
	if fired, err := wakes.ResumeLane(r.Ctx, r.Scope, request.Lane, delegate); !errors.Is(err, workorders.ErrForbidden) || len(fired) != 0 {
		return Violatedf("a delegate's resume of a lane parked at its limit fired %v and was answered %v", fired, err)
	}
	if err := r.state(s, id, workorders.Parked); err != nil {
		return err
	}
	if n, err := r.firings(s, request.Wake); err != nil || n != 0 {
		return Violatedf("a refused resume woke the lane %d times: %v", n, err)
	}
	// Once the limit is above what was charged the stop has ended, and a lane that waits on the wake is no longer at a
	// limit: the same delegate resumes it as any other.
	other, err := r.Parked(s, fmt.Sprintf(`{%q:true}`, parking.HumanResume))
	if err != nil {
		return err
	}
	if fired, err := wakes.ResumeWork(r.Ctx, r.Scope, other.WorkOrder, delegate); err != nil || len(fired) != 1 {
		return Violatedf("a delegate's resume of a lane parked for a person fired %v and was answered %v", fired, err)
	}
	fired, err := wakes.ResumeWork(r.Ctx, r.Scope, id, theMember)
	if err != nil || len(fired) != 1 {
		return Violatedf("a member's resume of work parked at its limit fired %v and was answered %v", fired, err)
	}
	return r.state(s, id, workorders.Ready)
}
