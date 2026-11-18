package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// SpendStopChecks are what a member sees and does at a spend stop besides raising the limit (ADR-0037, FR-SPEND-7):
// block the work or send it to replanning, and what Needs you reads of the stop. Each stop is one item for its work
// order and one interruption.
var SpendStopChecks = []Check{
	{"SS1", "blocking a spend stop ends its lane and wake, blocks the work with the reason spend_limit, and is a member's act on work a hard limit parked", checkBlockASpendStop},
	{"SS2", "sending a spend stop to replanning ends its lane and wake, returns the work to planning with its review void, and is a member's act", checkReplanASpendStop},
	{"SS3", "a spend stop reads once for its work order however many lanes it parked, with its limits, allowance, and what each lane left", checkSpendStopRead},
	{"SS4", "each hard limit a work order reaches is one interruption, and no act that ends the stop counts another", checkSpendStopInterrupts},
}

// interruptions counts the interruptions of one kind the product measures hold, over every window the ledger has.
func (r *Rig) interruptions(store ports.Store, kind string) (int64, error) {
	report, err := control.NewMeasures(store, nil).Report(r.Ctx, r.Scope, "all", 100*365*24*time.Hour)
	return report.InterruptionsByKind[kind], err
}

// parkedAtTheLimit is a work order whose lane handed off at its hard limit and parked.
func (r *Rig) parkedAtTheLimit(store ports.Store, charged []ledger.GenerationUsage) (string, ParkRequest, error) {
	id, request, err := r.stopped(store, charged)
	if err != nil {
		return "", ParkRequest{}, err
	}
	fired, err := r.NewOps(store).CommitParked(r.Ctx, r.Scope, request.Holder, PushedCommit(request.Wake), r.worker("a"))
	if err != nil || fired != "" {
		return "", ParkRequest{}, Violatedf("a lane parked at its hard limit woke at its parked commit: %q, %v", fired, err)
	}
	return id, request, nil
}

// laneEnded says the lane's row reads ended.
func (r *Rig) laneEnded(store ports.Store, lane string) error {
	return store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		row, err := tx.Lanes().Row(lane)
		if err != nil {
			return err
		}
		if row.Status.ActualState != ledger.StateEnded {
			return Violatedf("lane %s is %s, want ended", lane, row.Status.ActualState)
		}
		return nil
	})
}

// onlyAMemberEndsAStop holds the refusals every act on a stop shares: it is a member's, it names a work order that
// exists, and it takes work a hard limit parked.
func (r *Rig) onlyAMemberEndsAStop(store ports.Store, act func(id string, actor ledger.Actor) error, id string) error {
	for _, actor := range []ledger.Actor{
		{Kind: ledger.ActorOrchestrator, ID: "orchestrator-1"}, {Kind: ledger.ActorLane, ID: "lane-1"}, {Kind: ledger.ActorWorker, ID: "worker-1"},
		{Kind: ledger.ActorSystem, ID: "tick"}, authority.DelegateActor("member-1", "dlg_1"),
	} {
		if err := act(id, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("a %s's act on a spend stop was answered %v, want a refusal as forbidden", actor.Kind, err)
		}
	}
	if err := act("no-such-work-order", theMember); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("an act on a work order nobody made was answered %v", err)
	}
	ready, err := r.Ready(store)
	if err != nil {
		return err
	}
	if err := act(ready, theMember); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("an act on work no limit parked was answered %v, want a refusal as invalid", err)
	}
	return nil
}

func checkBlockASpendStop(r *Rig) error {
	s := r.Store()
	spend := control.NewSpend(s)
	id, request, err := r.parkedAtTheLimit(s, []ledger.GenerationUsage{{CostUSD: 30, KnownTurns: 3}})
	if err != nil {
		return err
	}
	block := func(id string, actor ledger.Actor) error {
		_, err := spend.Block(r.Ctx, r.Scope, id, actor)
		return err
	}
	if err := r.onlyAMemberEndsAStop(s, block, id); err != nil {
		return err
	}
	if err := r.state(s, id, workorders.Parked); err != nil {
		return err
	}
	state, err := spend.Block(r.Ctx, r.Scope, id, theMember)
	if err != nil || state != workorders.Blocked {
		return Violatedf("a member's block left the work order %q: %v", state, err)
	}
	if err := r.state(s, id, workorders.Blocked); err != nil {
		return err
	}
	if err := r.laneEnded(s, request.Lane); err != nil {
		return err
	}
	wake, err := r.readWake(s, request.Wake)
	if err != nil || wake.Open() || wake.FiredBy != parking.RetiredSpendStop {
		return Violatedf("the stop's wake reads %+v, want it retired by the stop: %v", wake, err)
	}
	if n, err := r.firings(s, request.Wake); err != nil || n != 0 {
		return Violatedf("a block fired the lane's wake %d times: %v", n, err)
	}
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	blocked := 0
	for _, e := range events {
		if e.Type != workorders.EventType(workorders.TriggerSpendBlocked) {
			continue
		}
		blocked++
		var change workorders.Change
		if err := json.Unmarshal(e.Data, &change); err != nil {
			return err
		}
		if change.Reason != workorders.ReasonSpendLimit || e.Actor != theMember {
			return Violatedf("the block recorded %+v by %+v, want the reason spend_limit by the member", change, e.Actor)
		}
	}
	if blocked != 1 {
		return Violatedf("%d blocks on record, want one", blocked)
	}
	if n, err := r.interruptions(s, measures.InterruptionForced); err != nil || n != 0 {
		return Violatedf("a block counted %d interruptions, and the stop it ends counted one when it was reached: %v", n, err)
	}
	// A blocked work order leaves as blocked work does, and its stop is over: a second block and a resume find nothing
	// parked.
	if err := block(id, theMember); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a second block was answered %v", err)
	}
	if _, err := spend.Resume(r.Ctx, r.Scope, id, theMember); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a resume of blocked work was answered %v", err)
	}
	if err := control.NewReviews(s).Unblock(r.Ctx, r.Scope, id, workorders.TriggerResolved, "the budget was agreed", theMember); err != nil {
		return err
	}
	return r.state(s, id, workorders.Ready)
}

func checkReplanASpendStop(r *Rig) error {
	s := r.Store()
	spend := control.NewSpend(s)
	id, request, err := r.parkedAtTheLimit(s, []ledger.GenerationUsage{{CostUSD: 30, KnownTurns: 3}})
	if err != nil {
		return err
	}
	replan := func(id string, actor ledger.Actor) error {
		_, err := spend.Replan(r.Ctx, r.Scope, id, actor)
		return err
	}
	if err := r.onlyAMemberEndsAStop(s, replan, id); err != nil {
		return err
	}
	state, err := spend.Replan(r.Ctx, r.Scope, id, theMember)
	if err != nil || state != workorders.Planning {
		return Violatedf("a member's replan left the work order %q: %v", state, err)
	}
	if err := r.state(s, id, workorders.Planning); err != nil {
		return err
	}
	order, err := r.WorkOrder(s, id)
	if err != nil || order.ReviewedBy != "" {
		return Violatedf("a replan left the review of the old contract standing: %+v, %v", order, err)
	}
	if err := r.laneEnded(s, request.Lane); err != nil {
		return err
	}
	if wake, err := r.readWake(s, request.Wake); err != nil || wake.Open() || wake.FiredBy != parking.RetiredSpendStop {
		return Violatedf("the stop's wake reads %+v, want it retired by the stop: %v", wake, err)
	}
	if n, err := r.interruptions(s, measures.InterruptionForced); err != nil || n != 0 {
		return Violatedf("a replan counted %d interruptions: %v", n, err)
	}
	if err := replan(id, theMember); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a second replan was answered %v", err)
	}
	// No other route takes these edges: the plain move of a work order refuses them.
	other, _, err := r.parkedAtTheLimit(s, []ledger.GenerationUsage{{CostUSD: 30, KnownTurns: 3}})
	if err != nil {
		return err
	}
	for _, trigger := range []workorders.Trigger{workorders.TriggerSpendBlocked, workorders.TriggerSpendReplan} {
		if err := r.NewOps(s).Move(r.Ctx, r.Scope, other, trigger, theMember); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("a plain move by %s was answered %v, want a refusal as forbidden", trigger, err)
		}
	}
	return r.state(s, other, workorders.Parked)
}

// laneIDs lists the lanes of a work order.
func (r *Rig) laneIDs(store ports.Store, workOrder string) ([]string, error) {
	var out []string
	err := store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.Lanes().Rows()
		for _, row := range rows {
			if row.WorkOrder == workOrder {
				out = append(out, row.ID)
			}
		}
		return err
	})
	return out, err
}

// stopRead reads the spend stops of the project.
func (r *Rig) stopRead(store ports.Store) ([]control.SpendStop, error) {
	var out []control.SpendStop
	err := store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		out, err = control.SpendStops(tx)
		return err
	})
	return out, err
}

// leftBy keeps what a generation of a lane left: its digest and its handoff, the way a worker uploads them.
func (r *Rig) leftBy(store ports.Store, lane, workOrder string, generation, turns int64, narrative *lanes.Narrative) error {
	digest, err := digestBody(turns)
	if err != nil {
		return err
	}
	handoff, hash, err := (lanes.Handoff{
		Schema: lanes.HandoffSchema, Lane: lane, Generation: generation, Reason: "spend_limit", Narrative: narrative,
		Deterministic: lanes.HandoffFacts{WorkOrder: workOrder},
	}).Seal()
	if err != nil {
		return err
	}
	return store.Write(r.Ctx, r.Scope, "spend.stop.fixture", func(tx ports.WriteTx) error {
		if _, err := tx.Append(r.noteDraft(r.ID("note"))); err != nil {
			return err
		}
		if err := tx.Lanes().SaveDigest(ledger.GenerationDigest{OwnerKind: ledger.DigestOwnerLane, OwnerID: lane, Generation: generation, Body: digest}); err != nil {
			return err
		}
		artifact := ledger.Artifact{ID: r.ID("artifact"), Lane: lane, Kind: ledger.ArtifactHandoff, URI: ledger.HandoffURI(lane, generation), Digest: hash}
		if err := tx.Lanes().AddArtifact(artifact); err != nil {
			return err
		}
		return tx.Lanes().SaveHandoff(ledger.HandoffRecord{
			ArtifactID: artifact.ID, Lane: lane, Generation: generation, Digest: hash, Degraded: narrative == nil, Body: handoff,
		})
	})
}

func checkSpendStopRead(r *Rig) error {
	s := r.Store()
	spend := control.NewSpend(s)
	if stops, err := r.stopRead(s); err != nil || len(stops) != 0 {
		return Violatedf("a project that stopped nothing reads %d spend stops: %v", len(stops), err)
	}
	// One lane's two generations spent 20 and 12, and another lane's two spent 5 and 4, so the work order is charged 41
	// against a limit of 25, and a generation costs 10.25 on average.
	id, request, err := r.parkedAtTheLimit(s, []ledger.GenerationUsage{{CostUSD: 20, KnownTurns: 2}, {CostUSD: 12, KnownTurns: 3}})
	if err != nil {
		return err
	}
	known, err := r.laneIDs(s, id)
	if err != nil {
		return err
	}
	if err := r.lanesWithUsage(s, id, []ledger.GenerationUsage{{CostUSD: 5, KnownTurns: 1}, {CostUSD: 4, KnownTurns: 1}}); err != nil {
		return err
	}
	all, err := r.laneIDs(s, id)
	if err != nil {
		return err
	}
	var extra string
	for _, lane := range all {
		if !slices.Contains(known, lane) {
			extra = lane
		}
	}
	first := lanes.Narrative{Done: []string{"the parser"}, InProgress: []string{"the writer"}, NextSteps: []string{"wire it in"}, Risks: []string{"none"}, CarriesNextStep: true}
	last := lanes.Narrative{Done: []string{"the writer"}, InProgress: []string{}, NextSteps: []string{"review it"}, Risks: []string{"a slow test"}, CarriesNextStep: true}
	parked := request.Lane
	for generation, left := range map[int64]*lanes.Narrative{1: &first, 2: &last} {
		if err := r.leftBy(s, extra, id, generation, 3*generation+1, left); err != nil {
			return err
		}
	}
	if err := r.leftBy(s, parked, id, 1, 2, nil); err != nil {
		return err
	}
	stops, err := r.stopRead(s)
	if err != nil || len(stops) != 1 {
		return Violatedf("a work order parked at its hard limit reads %d spend stops, want one: %v", len(stops), err)
	}
	stop := stops[0]
	if stop.WorkOrder.ID != id || !near(stop.Spend.HardLimit, 25) || !near(stop.Spend.SoftLimit, 12.5) || !near(stop.Spend.Remaining, 0) || !near(stop.Spend.Charged.ChargedUSD, 41) {
		return Violatedf("the stop reads %+v of work order %s, want a hard limit of 25, a soft limit of 12.5, nothing remaining, and 41 charged", stop.Spend, stop.WorkOrder.ID)
	}
	if stop.Resume == nil || stop.Resume.Basis != ledger.AllowanceMeanGeneration || !near(stop.Resume.AllowanceUSD, 10.25) || !near(stop.Resume.LimitUSD, 51.25) {
		return Violatedf("Resume once is %+v, want the mean generation of 10.25 lifting the limit to 51.25", stop.Resume)
	}
	byLane := map[string]control.SpendStopLane{}
	for _, read := range stop.Lanes {
		byLane[read.Lane] = read
	}
	if len(stop.Lanes) != len(all) || len(byLane) != len(all) {
		return Violatedf("the stop names %d lanes, want the %d of the work order, each once", len(stop.Lanes), len(all))
	}
	// The last generation that left a digest and the last handoff are read, the earlier ones passed.
	two := byLane[extra]
	if two.Digest == nil || two.Generation != 2 || two.Digest.Turns != 7 {
		return Violatedf("the lane of two generations reads generation %d with digest %+v, want the last generation's, of 7 turns", two.Generation, two.Digest)
	}
	if two.Handoff == nil || two.Handoff.Generation != 2 || two.Handoff.Degraded || two.Handoff.Narrative == nil ||
		len(two.Handoff.Narrative.NextSteps) != 1 || two.Handoff.Narrative.NextSteps[0] != "review it" {
		return Violatedf("the lane of two generations reads the handoff %+v, want generation 2's narrative", two.Handoff)
	}
	// A handoff with no narrative is said to be degraded, and a lane that left nothing reads nothing.
	one := byLane[parked]
	if one.Handoff == nil || !one.Handoff.Degraded || one.Handoff.Narrative != nil || one.Digest == nil || one.Digest.Turns != 2 {
		return Violatedf("the parked lane reads the handoff %+v and digest %+v, want a degraded handoff and a digest of 2 turns", one.Handoff, one.Digest)
	}
	for lane, read := range byLane {
		if lane != extra && lane != parked && (read.Digest != nil || read.Handoff != nil) {
			return Violatedf("lane %s left nothing and reads %+v", lane, read)
		}
	}
	// A limit a member raised past what was charged ends the stop, and the item leaves with it.
	if _, err := spend.Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: id, ToUSD: 80}, theMember); err != nil {
		return err
	}
	if stops, err := r.stopRead(s); err != nil || len(stops) != 0 {
		return Violatedf("a raised limit leaves %d spend stops: %v", len(stops), err)
	}
	return nil
}

func checkSpendStopInterrupts(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	id := s.grant.WorkOrder
	count := func(want int64, when string) error {
		n, err := r.interruptions(s.store, measures.InterruptionForced)
		if err != nil || n != want {
			return Violatedf("%s the product measures hold %d interruptions, want %d: %v", when, n, want, err)
		}
		return nil
	}
	report := func(cost float64) error {
		reply, err := s.usage(ledger.GenerationUsage{CostUSD: cost, KnownTurns: 4})
		if err == nil && len(reply.Rejected) != 0 {
			return fmt.Errorf("a usage report was refused: %+v", reply.Rejected)
		}
		return err
	}
	if err := report(24); err != nil {
		return err
	}
	if err := count(0, "under the hard limit"); err != nil {
		return err
	}
	for _, cost := range []float64{25, 26, 27} {
		if err := report(cost); err != nil {
			return err
		}
	}
	if err := count(1, "after the stop, across three reports past it,"); err != nil {
		return err
	}
	if _, err := control.NewSpend(s.store).Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: id, ToUSD: 40}, theMember); err != nil {
		return err
	}
	if err := count(1, "after a member raised the limit"); err != nil {
		return err
	}
	if err := report(41); err != nil {
		return err
	}
	return count(2, "after spend passed the raised limit")
}
