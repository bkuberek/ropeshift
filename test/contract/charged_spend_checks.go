package contract

import (
	"encoding/json"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ChargedSpendChecks are what a work order has spent and where its limits sit (ADR-0037): each generation's usage
// kept as its worker last reported it, charged spend folded across every lane that served the work order, soft and
// hard limits from its band, and a soft notice once per limit.
var ChargedSpendChecks = []Check{
	{"SP6", "a generation's usage report replaces its last, is fenced to the writer's generation, and is never negative", checkGenerationUsageReports},
	{"SP7", "charged spend folds every lane of a work order, charges stopped turns at the mean, and a retry keeps the lanes before it", checkChargedSpendFolds},
	{"SP8", "limits come from the baselined band or else the size, and a review's from the work it judges", checkSpendLimitsByTier},
	{"SP9", "a soft limit raises over_estimate and one notice, stops nothing, and follows a changed policy at once", checkSoftLimit},
	{"SP10", "a delivery record whose work a hard limit stopped is marked spend_limited", checkSpendLimitedRecord},
	{"SP11", "no lane pack, lane read, lease, or orchestrator wake holds a band, a limit, or what remains to spend", checkNoBandOrLimitReachesALaneOrTheOrchestrator},
	{"SP12", "the spend state holds the policies in force and the spend of each open work order against its limits", checkSpendState},
}

func checkSpendState(r *Rig) error {
	s := r.Store()
	spend := control.NewSpend(s)
	spent, err := r.Ready(s)
	if err != nil {
		return err
	}
	idle, err := r.Ready(s)
	if err != nil {
		return err
	}
	if err := r.lanesWithUsage(s, spent, []ledger.GenerationUsage{{CostUSD: 3, KnownTurns: 2, UnknownTurns: 1}}); err != nil {
		return err
	}
	state, err := spend.State(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if state.ProjectVersion.Version != 1 || state.InstallationVersion.Version != 1 || state.Project.HardMultiplier != 2 || state.Installation.OrchestratorClass != "standard" {
		return Violatedf("a workspace on the built-in policies reads %+v", state)
	}
	byID := map[string]ledger.WorkOrderSpend{}
	for _, order := range state.WorkOrders {
		byID[order.WorkOrder] = order
	}
	if got, ok := byID[spent]; !ok || !near(got.Charged.ChargedUSD, 4.5) || got.HardLimit == 0 {
		return Violatedf("the work order that spent reads %+v among %+v", got, state.WorkOrders)
	}
	if got, ok := byID[idle]; !ok || got.Charged.ChargedUSD != 0 {
		return Violatedf("the work order that spent nothing reads %+v", got)
	}
	policy, err := authority.DefaultSpendPolicy().Set("hard_multiplier", "3")
	if err != nil {
		return err
	}
	policy.Version = 2
	if _, err := spend.PublishSpendPolicy(r.Ctx, r.Scope, authority.SpendPolicyDraft{Body: policy.Body(), Policy: policy}, theMember); err != nil {
		return err
	}
	if state, err = spend.State(r.Ctx, r.Scope); err != nil || state.ProjectVersion.Version != 2 || state.Project.HardMultiplier != 3 || state.ProjectVersion.AuthoredBy != theMember.ID {
		return Violatedf("after version 2 the state reads %+v: %v", state, err)
	}
	return nil
}

// forbiddenReads are the words and the figures a lane and an orchestrator never read about a work order's spend, and
// the numbers the check puts into its charged spend and limits so that any copy of one is found.
var forbiddenReads = []string{
	"soft_limit", "hard_limit", "over_estimate", "spend_limit", "remaining", "charged_usd", "recorded_usd", "estimated_usd",
	"cost_p50_usd", "cost_p85_usd", "estimated by average", "70.25", "77.77", "38.885", "61.9", "123.8",
}

func checkNoBandOrLimitReachesALaneOrTheOrchestrator(r *Rig) error {
	policy, err := authority.DefaultSpendPolicy().Set("no_history_limit.medium", "77.77")
	if err != nil {
		return err
	}
	policy.Version = 2
	if _, err := control.NewSpend(r.Store()).PublishSpendPolicy(r.Ctx, r.Scope, authority.SpendPolicyDraft{Body: policy.Body(), Policy: policy}, theMember); err != nil {
		return err
	}
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	id := s.grant.WorkOrder
	if err := r.bandFor(s.store, id, 9, 12.34, 61.9, true); err != nil {
		return err
	}
	if reply, err := s.usage(ledger.GenerationUsage{CostUSD: 70.25, KnownTurns: 4}); err != nil || len(reply.Rejected) != 0 {
		return Violatedf("a usage report was refused: %+v, %v", reply.Rejected, err)
	}
	spend, err := r.spendOf(s.store, id)
	if err != nil || !near(spend.Charged.ChargedUSD, 70.25) || spend.SoftLimit == 0 || spend.HardLimit == 0 {
		return Violatedf("the member's own read of the work order is %+v: %v", spend, err)
	}
	reads := map[string]any{}
	if reads["desired state"], err = s.plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: s.auth}); err != nil {
		return err
	}
	if reads["wake digest"], err = s.plane.WakeDigest(r.Ctx, ledger.WakeRequest{Auth: s.auth, Epoch: s.epoch}); err != nil {
		return err
	}
	for _, tool := range []string{lanes.ToolContextGet, lanes.ToolDecisionSearch, lanes.ToolRequirementGet, lanes.ToolQuestionSearch} {
		read, err := s.laneRead(func(request *ledger.LaneContextRequest) {
			request.Tool, request.Kind, request.Ref, request.Query = tool, "contract", id, "spend limit band"
		})
		if err != nil {
			continue
		}
		reads["lane read "+tool] = read
	}
	pack, err := r.packOf(s.store, s.grant.ClaimID)
	if err != nil {
		return err
	}
	reads["lane pack"] = pack
	events, err := r.Events(s.store, r.Scope)
	if err != nil {
		return err
	}
	if len(reads) < 3 {
		return Violatedf("only %d reads were made", len(reads))
	}
	for name, read := range reads {
		raw, err := json.Marshal(read)
		if err != nil {
			return err
		}
		text := strings.ToLower(string(raw))
		for _, word := range forbiddenReads {
			if strings.Contains(text, word) {
				return Violatedf("the %s holds %q:\n%s", name, word, raw)
			}
		}
	}
	// The ledger's own events about the work order hold the notice, and only a member's read of events sees them:
	// none of them is in a lane's or an orchestrator's read above.
	if !slices.ContainsFunc(events, func(e ledger.Event) bool { return e.Type == ledger.EventSpendSoftLimit }) {
		return Violatedf("the soft limit left no notice, so the check read nothing it should have kept out")
	}
	return nil
}

func (s *statusRig) usage(u ledger.GenerationUsage) (ledger.UploadReply, error) {
	raw, err := json.Marshal(u)
	if err != nil {
		return ledger.UploadReply{}, err
	}
	return s.upload(ledger.UploadGenerationUsage, raw)
}

func (r *Rig) spendOf(store ports.Store, id string) (ledger.WorkOrderSpend, error) {
	return control.NewSpend(store).WorkOrderSpend(r.Ctx, r.Scope, id)
}

func (r *Rig) generationsOf(store ports.Store, lane string) ([]ledger.Generation, error) {
	var out []ledger.Generation
	err := store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Lanes().Generations(lane)
		return err
	})
	return out, err
}

// lanesWithUsage gives a work order lanes that each ran generations with the usage given, as if its workers had
// reported them.
func (r *Rig) lanesWithUsage(store ports.Store, workOrder string, lanes ...[]ledger.GenerationUsage) error {
	return store.Write(r.Ctx, r.Scope, "spend.fixture", func(tx ports.WriteTx) error {
		if _, err := tx.Append(r.noteDraft(r.ID("note"))); err != nil {
			return err
		}
		for _, generations := range lanes {
			lane := r.ID("lane")
			if err := tx.Lanes().Start(lane, workOrder, 1, ledger.LanePolicies{}); err != nil {
				return err
			}
			for i, usage := range generations {
				number := int64(i + 1)
				if i > 0 {
					if _, err := tx.Lanes().NextGeneration(lane); err != nil {
						return err
					}
				}
				if err := tx.Lanes().StartGeneration(ledger.Generation{Lane: lane, Number: number, StartReason: ledger.StartInitial}); err != nil {
					return err
				}
				if _, err := tx.Lanes().ReportUsage(lane, number, usage); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// bandFor keeps an estimate of a work order whose cost band is the one given, as a set, and as the baseline of an
// approval when baselined says so.
func (r *Rig) bandFor(store ports.Store, id string, outcomes int, p50, p85 float64, baselined bool) error {
	set := delivery.EstimateSet{
		ID: r.ID("set"), Plan: r.ID("plan"), PlanVersion: 1, Rev: 1, EstimatorVersion: delivery.EstimatorVersion, PolicyVersion: 1, CreatedAt: 1000,
		Estimates: []delivery.Estimate{{
			ID: r.ID("e"), WorkOrder: id, Status: delivery.Estimated, Confidence: delivery.Confident, Tier: 1, Keys: []string{"kind", "size"},
			DroppedKeys: []string{}, SampleCount: outcomes, MinSamples: 5, SampleIDs: []string{}, CostP50USD: p50, CostP85USD: p85, Rev: 1,
			EstimatorVersion: delivery.EstimatorVersion, PolicyVersion: 1, Explanation: "A band.",
		}},
	}.Seal()
	if err := r.addSet(store, set); err != nil {
		return err
	}
	if !baselined {
		return nil
	}
	return store.Write(r.Ctx, r.Scope, "baseline.add", func(tx ports.WriteTx) error {
		if _, err := tx.Append(r.noteDraft(r.ID("note"))); err != nil {
			return err
		}
		return tx.Estimates().AddBaseline(delivery.Baseline{
			ID: r.ID("baseline"), SetID: set.ID, SetDigest: set.Digest, Forecasts: []delivery.Forecast{}, Rev: 1, CreatedAt: 1000,
		})
	})
}

func checkGenerationUsageReports(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	for _, usage := range []ledger.GenerationUsage{{CostUSD: 0.1, KnownTurns: 1}, {CostUSD: 0.3, KnownTurns: 3, UnknownTurns: 1}} {
		reply, err := s.usage(usage)
		if err != nil || len(reply.Rejected) != 0 {
			return Violatedf("a usage report was refused: %+v, %v", reply.Rejected, err)
		}
	}
	generations, err := r.generationsOf(s.store, s.lane)
	if err != nil || len(generations) != 1 || reported(generations[0].Usage) != (ledger.GenerationUsage{CostUSD: 0.3, KnownTurns: 3, UnknownTurns: 1}) {
		return Violatedf("the generation reads %+v after two reports: %v", generations, err)
	}
	spend, err := r.spendOf(s.store, s.grant.WorkOrder)
	if err != nil || !near(spend.Charged.ChargedUSD, 0.4) || spend.Charged.EstimatedTurns != 1 {
		return Violatedf("the work order is charged %+v: %v", spend.Charged, err)
	}
	reply, err := s.usage(ledger.GenerationUsage{CostUSD: -1})
	if err == nil && (len(reply.Rejected) != 1 || reply.Rejected[0].Status != ledger.UploadInvalid) {
		return Violatedf("a negative usage was answered %+v", reply.Rejected)
	}
	raw, err := json.Marshal(ledger.GenerationUsage{CostUSD: 9, KnownTurns: 1})
	if err != nil {
		return err
	}
	s.seq++
	stale, err := s.plane.Upload(r.Ctx, ledger.UploadBatch{Auth: s.auth, Epoch: s.epoch, Events: []ledger.UploadEvent{{
		Epoch: s.epoch, Seq: s.seq, Type: ledger.UploadGenerationUsage, WorkOrder: s.grant.WorkOrder, ClaimID: s.grant.ClaimID,
		Fence: s.grant.Fence, Lane: s.lane, Generation: 7, Payload: raw, OccurredAt: 1,
	}}})
	if err == nil && len(stale.Rejected) == 0 {
		return Violatedf("a report for a generation the lane is not at was taken")
	}
	if after, err := r.generationsOf(s.store, s.lane); err != nil || after[0].Usage.CostUSD != 0.3 {
		return Violatedf("a refused report changed the generation to %+v: %v", after, err)
	}
	return nil
}

func checkChargedSpendFolds(r *Rig) error {
	s := r.Store()
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	if err := r.lanesWithUsage(s, id,
		[]ledger.GenerationUsage{{CostUSD: 0.30, KnownTurns: 3}},
		[]ledger.GenerationUsage{{CostUSD: 0.20, KnownTurns: 2}},
		[]ledger.GenerationUsage{{CostUSD: 0.40, KnownTurns: 4, UnknownTurns: 2}},
	); err != nil {
		return err
	}
	spend, err := r.spendOf(s, id)
	if err != nil {
		return err
	}
	if !near(spend.Charged.ChargedUSD, 1.1) || !near(spend.Charged.RecordedUSD, 0.9) || spend.Charged.EstimatedTurns != 2 ||
		spend.Label != "estimated, at list price, with 2 turns estimated by average" {
		return Violatedf("three lanes, one with two stopped turns, are charged %+v, labeled %q", spend.Charged, spend.Label)
	}
	// A retry is another lane of the same work order, and the lanes before it keep what they spent.
	if err := r.lanesWithUsage(s, id, []ledger.GenerationUsage{{CostUSD: 0.5, KnownTurns: 5}}); err != nil {
		return err
	}
	retried, err := r.spendOf(s, id)
	if err != nil || !near(retried.Charged.ChargedUSD, 1.6) || retried.Charged.EstimatedTurns != 2 {
		return Violatedf("a retried work order is charged %+v: %v", retried.Charged, err)
	}
	other, err := r.Ready(s)
	if err != nil {
		return err
	}
	if spend, err := r.spendOf(s, other); err != nil || spend.Charged.ChargedUSD != 0 {
		return Violatedf("another work order is charged %+v: %v", spend.Charged, err)
	}
	return nil
}

func checkSpendLimitsByTier(r *Rig) error {
	s := r.Store()
	limits := func(id string) (soft, hard float64, basis string, err error) {
		spend, err := r.spendOf(s, id)
		return spend.SoftLimit, spend.HardLimit, spend.Basis, err
	}
	expect := func(id string, soft, hard float64, basis string) error {
		gotSoft, gotHard, gotBasis, err := limits(id)
		if err != nil {
			return err
		}
		if !near(gotSoft, soft) || !near(gotHard, hard) || gotBasis != basis {
			return Violatedf("limits are %v and %v by %s, want %v and %v by %s", gotSoft, gotHard, gotBasis, soft, hard, basis)
		}
		return nil
	}
	baselined, err := r.Ready(s)
	if err != nil {
		return err
	}
	if err := r.bandFor(s, baselined, 8, 4, 10, true); err != nil {
		return err
	}
	if err := expect(baselined, 10, 20, string(delivery.BasisBaselinedBand)); err != nil {
		return err
	}
	// A new version of the policy moves the limit at once, and a revert, which is the earlier values published as
	// a newer version, puts it back.
	publish := func(version int64, hardMultiplier string) error {
		policy, err := authority.DefaultSpendPolicy().Set("hard_multiplier", hardMultiplier)
		if err != nil {
			return err
		}
		policy.Version = version
		_, err = control.NewSpend(s).PublishSpendPolicy(r.Ctx, r.Scope, authority.SpendPolicyDraft{Body: policy.Body(), Policy: policy}, theMember)
		return err
	}
	if err := publish(2, "3"); err != nil {
		return err
	}
	if err := expect(baselined, 10, 30, string(delivery.BasisBaselinedBand)); err != nil {
		return err
	}
	if err := publish(3, "2"); err != nil {
		return err
	}
	if err := expect(baselined, 10, 20, string(delivery.BasisBaselinedBand)); err != nil {
		return err
	}
	none, err := r.Ready(s)
	if err != nil {
		return err
	}
	if err := expect(none, 12.5, 25, string(delivery.BasisNoHistory)); err != nil {
		return err
	}
	f, err := newForgeRig(r, ledger.ReviewOnPullRequest)
	if err != nil {
		return err
	}
	review, err := f.review()
	if err != nil {
		return err
	}
	if review.Kind != workorders.KindReview || review.Reviews != f.grant.WorkOrder {
		return Violatedf("the review is %+v", review)
	}
	spend, err := r.spendOf(f.store, review.ID)
	if err != nil || !near(spend.HardLimit, 12.5) || !near(spend.SoftLimit, 6.25) || spend.Basis != string(delivery.BasisReview) {
		return Violatedf("a review of work limited at 25 has %+v: %v", spend, err)
	}
	return nil
}

// softLimitEvents counts the notices a work order's spend left.
func (r *Rig) softLimitEvents(store ports.Store, id string) (int, error) {
	count := 0
	err := store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectSpend, ID: id})
		for _, e := range events {
			if e.Type == ledger.EventSpendSoftLimit {
				count++
			}
		}
		return err
	})
	return count, err
}

func checkSoftLimit(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	id := s.grant.WorkOrder
	report := func(cost float64) error {
		reply, err := s.usage(ledger.GenerationUsage{CostUSD: cost, KnownTurns: 4})
		if err == nil && len(reply.Rejected) != 0 {
			return Violatedf("a usage report was refused: %+v", reply.Rejected)
		}
		return err
	}
	if err := report(12); err != nil {
		return err
	}
	if spend, err := r.spendOf(s.store, id); err != nil || len(spend.Conditions) != 0 {
		return Violatedf("under the soft limit of 12.5 the work order reads %+v: %v", spend, err)
	}
	if count, err := r.softLimitEvents(s.store, id); err != nil || count != 0 {
		return Violatedf("%d notices before the soft limit: %v", count, err)
	}
	for _, cost := range []float64{13, 14} {
		if err := report(cost); err != nil {
			return err
		}
	}
	spend, err := r.spendOf(s.store, id)
	if err != nil || !slices.Equal(spend.Conditions, []string{ledger.ConditionOverEstimate}) {
		return Violatedf("past the soft limit the work order reads %+v: %v", spend, err)
	}
	if count, err := r.softLimitEvents(s.store, id); err != nil || count != 1 {
		return Violatedf("%d notices past the soft limit, want one: %v", count, err)
	}
	if err := r.state(s.store, id, workorders.Running); err != nil {
		return err
	}
	// A new version of the policy moves the limits at once: a larger size limit leaves the work under its soft limit.
	policy, err := authority.DefaultSpendPolicy().Set("no_history_limit.medium", "40")
	if err != nil {
		return err
	}
	policy.Version = 2
	if _, err := control.NewSpend(s.store).PublishSpendPolicy(r.Ctx, r.Scope, authority.SpendPolicyDraft{Body: policy.Body(), Policy: policy}, theMember); err != nil {
		return err
	}
	moved, err := r.spendOf(s.store, id)
	if err != nil || !near(moved.HardLimit, 40) || !near(moved.SoftLimit, 20) || len(moved.Conditions) != 0 || moved.PolicyVersion != 2 {
		return Violatedf("under policy version 2 the work order reads %+v: %v", moved, err)
	}
	if err := report(30); err != nil {
		return err
	}
	if count, err := r.softLimitEvents(s.store, id); err != nil || count != 2 {
		return Violatedf("%d notices after the limit moved and was passed again, want two: %v", count, err)
	}
	return nil
}

// state checks a work order is in a state.
func (r *Rig) state(store ports.Store, id string, want workorders.State) error {
	order, err := r.WorkOrder(store, id)
	if err != nil {
		return err
	}
	if order.State != want {
		return Violatedf("work order %s is %s, want %s", id, order.State, want)
	}
	return nil
}

func checkSpendLimitedRecord(r *Rig) error {
	s := r.Store()
	result := LaneFinished(r.ID("commit"), LaneCheck(criterionOne, true))
	id, err := r.Awaiting(s, []string{criterionOne}, result)
	if err != nil {
		return err
	}
	err = s.Write(r.Ctx, r.Scope, "spend.limited", func(tx ports.WriteTx) error {
		_, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventSpendLimited, Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "spend"},
			Subject: ledger.Subject{Kind: ledger.SubjectSpend, ID: id}, Data: []byte(`{}`),
		})
		return err
	})
	if err != nil {
		return err
	}
	if _, err := r.NewOps(s).Accept(r.Ctx, r.Scope, id, r.Actor); err != nil {
		return err
	}
	_, record, err := r.deliveryOf(s, id)
	if err != nil || record == nil || !record.Usage.SpendLimited || !record.Usage.LowerBound() {
		return Violatedf("a work order a hard limit stopped has record %+v: %v", record, err)
	}
	clean, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(r.ID("commit"), LaneCheck(criterionOne, true)))
	if err != nil {
		return err
	}
	if _, err := r.NewOps(s).Accept(r.Ctx, r.Scope, clean, r.Actor); err != nil {
		return err
	}
	if _, record, err := r.deliveryOf(s, clean); err != nil || record == nil || record.Usage.SpendLimited {
		return Violatedf("work no limit stopped has record %+v: %v", record, err)
	}
	return nil
}
