package server

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// defaultLongParkedMs is how long a lane stays parked before it reads as stuck, for a deployment that
// states nothing. The design names the state and no length, so this is a proposed value.
const defaultLongParkedMs int64 = 24 * 60 * 60 * 1000

func (h *handler) viewRoutes(mux *http.ServeMux) {
	views := map[string]func(ports.ReadTx, *http.Request, api.Stamp) (any, error){
		api.ViewNeedsYou:   h.readNeedsYou,
		api.ViewRoadmap:    h.readRoadmap,
		api.ViewPlans:      h.readPlans,
		api.ViewDecisions:  h.readDecisions,
		api.ViewQuestions:  h.readQuestions,
		api.ViewInProgress: h.readInProgress,
		api.ViewOnHold:     h.readOnHold,
		api.ViewStuck:      h.readStuck,
		api.ViewCompleted:  h.readCompleted,
		api.ViewShopFloor:  h.readShopFloor,
		api.ViewOperations: h.readOperations,
	}
	for name, fill := range views {
		mux.HandleFunc("GET "+api.ViewPath(name), func(w http.ResponseWriter, r *http.Request) { h.serveView(w, r, fill) })
	}
	mux.HandleFunc("GET "+api.ViewPath(api.ViewDocuments), h.serveDocuments)
	mux.HandleFunc("GET "+api.ViewPath(api.ViewSearch), h.serveSearch)
}

// serveView reads one view in one read, so everything in it stands at one revision.
func (h *handler) serveView(w http.ResponseWriter, r *http.Request, fill func(ports.ReadTx, *http.Request, api.Stamp) (any, error)) {
	var out any
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		workspace, err := tx.Workspace()
		if err != nil {
			return err
		}
		now, err := tx.Now()
		if err != nil {
			return err
		}
		out, err = fill(tx, r, api.Stamp{AsOf: now, Revision: workspace.Rev})
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, out)
}

// floor is the moment a read model works from: every work order and lane, once.
type floor struct {
	orders []workorders.WorkOrder
	lanes  []ledger.LaneView
	rows   map[string]ledger.LaneRow
	// ofWork is the newest lane of each work order.
	ofWork map[string]*ledger.LaneView
}

func (h *handler) readFloor(tx ports.ReadTx) (floor, error) {
	var f floor
	var err error
	if f.orders, err = tx.WorkOrders().List(); err != nil {
		return f, err
	}
	if f.lanes, err = h.workers.LanesIn(tx); err != nil {
		return f, err
	}
	rows, err := tx.Lanes().Rows()
	if err != nil {
		return f, err
	}
	f.rows, f.ofWork = map[string]ledger.LaneRow{}, map[string]*ledger.LaneView{}
	for _, row := range rows {
		f.rows[row.ID] = row
	}
	for i := range f.lanes {
		lane := &f.lanes[i]
		if current, ok := f.ofWork[lane.WorkOrder]; !ok || f.rows[current.ID].StartedFence <= f.rows[lane.ID].StartedFence {
			f.ofWork[lane.WorkOrder] = lane
		}
	}
	return f, nil
}

// enteredAt reads when a work order reached the state it stands in, which only the log knows. A note
// on a work order changes no state, so only the edges into that state count.
func enteredAt(tx ports.ReadTx, order workorders.WorkOrder) (int64, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: workorders.SubjectKind, ID: order.ID})
	if err != nil {
		return 0, err
	}
	into := edgesInto(order.State)
	for i := len(events) - 1; i >= 0; i-- {
		if into[events[i].Type] {
			return events[i].RecordedAt, nil
		}
	}
	if len(events) != 0 {
		return events[len(events)-1].RecordedAt, nil
	}
	return 0, nil
}

// edgesInto names the events that bring a work order to a state.
func edgesInto(state workorders.State) map[string]bool {
	into := map[string]bool{}
	for _, edge := range workorders.Transitions() {
		if edge.To == state {
			into[workorders.EventType(edge.Trigger)] = true
		}
	}
	return into
}

// arrivalReason reads what was said when a work order reached the state it stands in, which is where
// a lane says why it stopped, and what its outcome said of that reason.
func arrivalReason(tx ports.ReadTx, order workorders.WorkOrder) (string, string, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: workorders.SubjectKind, ID: order.ID})
	if err != nil {
		return "", "", err
	}
	into := edgesInto(order.State)
	for i := len(events) - 1; i >= 0; i-- {
		if !into[events[i].Type] {
			continue
		}
		var change workorders.Change
		if err := json.Unmarshal(events[i].Data, &change); err != nil {
			return "", "", err
		}
		return change.Reason, change.Detail, nil
	}
	return "", "", nil
}

// wakesOf groups the open wakes by lane.
func wakesOf(tx ports.ReadTx) (map[string][]parking.Wake, error) {
	open, err := tx.Wakes().Open()
	if err != nil {
		return nil, err
	}
	out := map[string][]parking.Wake{}
	for _, wake := range open {
		out[wake.Lane] = append(out[wake.Lane], wake)
	}
	return out, nil
}

// waitsOnAPerson says a member's word can move a lane, which a `human_resume` term stands for.
func waitsOnAPerson(wakes []parking.Wake) bool {
	return slices.ContainsFunc(wakes, func(wake parking.Wake) bool { return strings.Contains(wake.Expr, string(parking.HumanResume)) })
}

// wakeViews states a lane's wakes as a read model holds them.
func wakeViews(wakes []parking.Wake) []api.WakeView {
	out := make([]api.WakeView, 0, len(wakes))
	for _, wake := range wakes {
		out = append(out, api.WakeView{ID: wake.ID, Expression: wake.Expr, NextEvalAt: wake.NextEvalAt})
	}
	return out
}

func (h *handler) longParked() int64 {
	if h.LongParkedMs > 0 {
		return h.LongParkedMs
	}
	return defaultLongParkedMs
}

// stuck finds what stopped: the design's five, and failed work, the longest stop first. A lane on work the ledger still
// counts as moving stands here too, because its work order says nothing of a breaker or a stall.
func (h *handler) stuck(tx ports.ReadTx, f floor, wakes map[string][]parking.Wake, now int64) ([]api.StuckItem, error) {
	byDefault, err := control.DefaultAttempts(tx.Policies())
	if err != nil {
		return nil, err
	}
	items := []api.StuckItem{}
	add := func(id string, since int64, need api.WorkNeed) {
		if need.Actions == nil {
			need.Actions = []string{}
		}
		items = append(items, api.StuckItem{ID: id, Since: since, WorkNeed: need})
	}
	for i := range f.orders {
		order := &f.orders[i]
		switch order.State {
		case workorders.Lapsed, workorders.Blocked, workorders.Parked, workorders.Failed:
		case workorders.Claimed:
			since, detail, held, err := claimHeldForUpgrade(tx, *order)
			if err != nil {
				return nil, err
			}
			if held {
				add(order.ID, since, api.WorkNeed{
					WorkOrder: order, Lane: f.ofWork[order.ID], Reason: api.ReasonWorkerBehind, Detail: detail, Actions: []string{api.ActCancel},
				})
			}
			continue
		default:
			continue
		}
		since, err := enteredAt(tx, *order)
		if err != nil {
			return nil, err
		}
		need := api.WorkNeed{WorkOrder: order, Lane: f.ofWork[order.ID]}
		switch order.State {
		case workorders.Lapsed:
			need.Reason, need.Actions = api.ReasonLapsed, []string{api.ActReassign, api.ActCancel}
		case workorders.Blocked:
			need.Reason, need.Actions = api.ReasonBlocked, []string{api.ActResolve, api.ActReplan, api.ActCancel}
			if need.Detail, _, err = arrivalReason(tx, *order); err != nil {
				return nil, err
			}
		case workorders.Failed:
			// Nothing retries failed work on its own, so it waits here from the time it failed, with what the state
			// machine allows of it. Only a retry leaves the state, and work that is out of attempts needs a member
			// to add one.
			need.Reason, need.ExtraAttempt = api.ReasonFailed, !order.AttemptsLeft(byDefault)
			for _, action := range workorders.Allowed(order.State, workorders.Facts{}) {
				need.Actions = append(need.Actions, string(action))
			}
			reason, said, err := arrivalReason(tx, *order)
			if err != nil {
				return nil, err
			}
			need.Detail = failureWords(reason, said)
		default:
			if now-since < h.longParked() {
				continue
			}
			need.Reason, need.Actions = api.ReasonLongParked, []string{api.ActCancel}
			if need.Lane != nil {
				need.Wakes = wakeViews(wakes[need.Lane.ID])
			}
			if need.Lane != nil && waitsOnAPerson(wakes[need.Lane.ID]) {
				need.Actions = []string{api.ActResume, api.ActCancel}
			}
		}
		add(order.ID, since, need)
	}
	for i := range f.lanes {
		lane := &f.lanes[i]
		if lane.ActualState == ledger.StateEnded {
			continue
		}
		var order *workorders.WorkOrder
		if at := slices.IndexFunc(f.orders, func(row workorders.WorkOrder) bool { return row.ID == lane.WorkOrder }); at >= 0 {
			order = &f.orders[at]
		}
		if order == nil || workorders.Terminal(order.State) {
			continue
		}
		need := api.WorkNeed{WorkOrder: order, Lane: lane}
		switch {
		case lane.ActualState == "tripped":
			need.Reason, need.Actions = api.ReasonTripped, []string{api.ActReset, api.ActCancel}
		case slices.Contains(lane.Conditions, "stalled"):
			need.Reason, need.Actions = api.ReasonStalled, []string{api.ActRespawn, api.ActCancel}
		default:
			continue
		}
		add(lane.WorkOrder, f.rows[lane.ID].ReportedAt, need)
	}
	if err := stuckOrchestrators(tx, add); err != nil {
		return nil, err
	}
	slices.SortStableFunc(items, func(a, b api.StuckItem) int { return cmp.Or(cmp.Compare(a.Since, b.Since), cmp.Compare(a.ID, b.ID)) })
	return items, nil
}

// failureWords says why a lane failed as a person reads it: the reason its outcome gave, and what the worker
// said of it.
func failureWords(reason, said string) string {
	switch {
	case reason != "" && said != "":
		return reason + ": " + said
	case said != "":
		return said
	default:
		return reason
	}
}

// claimHeldForUpgrade says a claimed work order's worker is too old to read the lane a delegate started under a
// delegate policy that restricts it (FR-AUTH-16). The plane offers such a worker the bare claim, which it renews
// and starts nothing for, so no lease lapses and no lane reports anything. The detail names the worker and what
// it needs, and since is when the lane was asked for.
func claimHeldForUpgrade(tx ports.ReadTx, order workorders.WorkOrder) (since int64, detail string, held bool, err error) {
	claim, err := tx.Claims().Find(order.ClaimID)
	if isNotFound(err) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	if claim.Lane == "" || claim.EndedAt != 0 || claim.Delegated == nil || !claim.Delegated.Restricted {
		return 0, "", false, nil
	}
	worker, err := tx.Workers().Find(claim.Worker)
	if isNotFound(err) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	if !claim.Delegated.WithheldFrom(worker.Protocol) {
		return 0, "", false, nil
	}
	if since, err = dispatchedAt(tx, order, claim); err != nil {
		return 0, "", false, err
	}
	name := worker.ID
	if worker.Name != "" && worker.Name != worker.ID {
		name = fmt.Sprintf("%q (%s)", worker.Name, worker.ID)
	}
	detail = fmt.Sprintf("the worker %s must be upgraded to start this lane: it runs protocol %d, and this lane needs protocol %d", name, worker.Protocol, ledger.ProtocolDelegatedLayers)
	return since, detail, true, nil
}

// dispatchedAt reads when the claim was asked to run its lane, which only the log knows. A claim that brings a
// parked lane back was never dispatched, so it is held since the work order was last claimed.
func dispatchedAt(tx ports.ReadTx, order workorders.WorkOrder, claim workorders.Claim) (int64, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: workorders.SubjectKind, ID: order.ID})
	if err != nil {
		return 0, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != workorders.EventLaneDispatched {
			continue
		}
		var change workorders.Change
		if err := json.Unmarshal(events[i].Data, &change); err != nil {
			return 0, err
		}
		if change.ClaimID == claim.ID {
			return events[i].RecordedAt, nil
		}
	}
	return enteredAt(tx, order)
}

// stuckOrchestrators finds the sessions a member asked for that stopped working. An orchestrator has no
// work order and no lane row, and nothing supervises it but a person, so one that stopped would
// otherwise go unseen while the ladder it works waits.
func stuckOrchestrators(tx ports.ReadTx, add func(string, int64, api.WorkNeed)) error {
	workers, err := tx.Workers().List()
	if err != nil {
		return err
	}
	for _, worker := range workers {
		row, err := tx.Workers().Orchestrator(worker.ID)
		if isNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		if row.Desired != ledger.OrchestratorRunning {
			continue
		}
		need := api.WorkNeed{Orchestrator: &row, Actions: []string{api.ActStartOrchestrator}}
		switch {
		case row.Tripped():
			need.Reason = api.ReasonTripped
		case row.Stalled():
			need.Reason = api.ReasonStalled
		default:
			continue
		}
		add(row.ID, row.ReportedAt, need)
	}
	return nil
}

// waitingEffects lists what a person can decide of effects, with what they can say.
func waitingEffects(tx ports.ReadTx) ([]api.EffectView, error) {
	rows, err := tx.Effects().Effects()
	if err != nil {
		return nil, err
	}
	out := []api.EffectView{}
	for _, row := range rows {
		if !row.Waiting() {
			continue
		}
		ended, err := control.LaneEnded(tx.Lanes(), row.Lane)
		if err != nil {
			return nil, err
		}
		out = append(out, api.EffectView{UnknownEffect: row, Offers: row.Offers(ended)})
	}
	return out, nil
}

func waitingBatches(tx ports.ReadTx) ([]api.QuestionBatch, error) {
	batches, err := tx.Questions().WaitingBatches()
	if err != nil {
		return nil, err
	}
	policy, err := control.DecisionPolicy(tx)
	if err != nil {
		return nil, err
	}
	var waiting []questions.Presented
	for _, batch := range batches {
		rows, err := tx.Questions().BatchQuestions(batch.ID)
		if err != nil {
			return nil, err
		}
		if presented := questions.Present(batch, rows, "", policy); len(presented.Items) != 0 {
			waiting = append(waiting, presented)
		}
	}
	return questionBatches(tx, waiting)
}

// needs is everything a member can act on, the longest wait first. A tie falls to the ID, so two
// reads of one revision agree.
func (h *handler) needs(tx ports.ReadTx, stamp api.Stamp) ([]api.NeedItem, error) {
	f, err := h.readFloor(tx)
	if err != nil {
		return nil, err
	}
	wakes, err := wakesOf(tx)
	if err != nil {
		return nil, err
	}
	items := []api.NeedItem{}
	batches, err := waitingBatches(tx)
	if err != nil {
		return nil, err
	}
	for i := range batches {
		items = append(items, api.NeedItem{Kind: api.NeedQuestions, ID: batches[i].Batch.ID, Since: batches[i].Batch.DeliveredAt, Batch: &batches[i]})
	}
	approvals, err := tx.Approvals().All()
	if err != nil {
		return nil, err
	}
	// planned holds the waiting plan approvals of each work order they name: the planning lane's, whose branch
	// holds the plan. Approving the plan accepts that work order (ADR-0018), so the approval is the one thing asked.
	planned := map[string][]authority.Approval{}
	for i := range approvals {
		switch {
		case !approvals[i].Open():
		case approvals[i].Kind == authority.KindPlan:
			need, err := planApprovalNeed(tx, approvals[i])
			if err != nil {
				return nil, err
			}
			if approvals[i].WorkOrder != "" {
				planned[approvals[i].WorkOrder] = append(planned[approvals[i].WorkOrder], approvals[i])
			}
			items = append(items, api.NeedItem{Kind: api.NeedPlanApproval, ID: approvals[i].ID, Since: approvals[i].RaisedAt, PlanApproval: need})
		case approvals[i].Kind == authority.KindConfirm:
			need, err := cardNeed(tx.Members(), tx.Conversations(), approvals[i])
			if err != nil {
				return nil, err
			}
			items = append(items, api.NeedItem{Kind: api.NeedConfirm, ID: approvals[i].ID, Since: approvals[i].RaisedAt, Card: need})
		default:
			items = append(items, api.NeedItem{Kind: api.NeedApproval, ID: approvals[i].ID, Since: approvals[i].RaisedAt, Approval: &approvals[i]})
		}
	}
	reviews, err := contractReviewNeeds(tx, f.orders)
	if err != nil {
		return nil, err
	}
	items = append(items, reviews...)
	revisions, err := planRevisionNeeds(tx)
	if err != nil {
		return nil, err
	}
	items = append(items, revisions...)
	effects, err := waitingEffects(tx)
	if err != nil {
		return nil, err
	}
	for i := range effects {
		items = append(items, api.NeedItem{Kind: api.NeedEffect, ID: effects[i].ID, Since: effects[i].RaisedAt, Effect: &effects[i]})
	}
	stuck, err := h.stuck(tx, f, wakes, stamp.AsOf)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i := range stuck {
		seen[stuck[i].ID] = true
		items = append(items, api.NeedItem{Kind: api.NeedWork, ID: stuck[i].ID, Since: stuck[i].Since, Work: &stuck[i].WorkNeed})
	}
	for i := range f.orders {
		order := &f.orders[i]
		need := api.WorkNeed{WorkOrder: order, Lane: f.ofWork[order.ID]}
		accepting, err := acceptedByApproving(tx.Evidence(), *order, planned[order.ID])
		if err != nil {
			return nil, err
		}
		switch {
		case order.State == workorders.Acceptance && !accepting:
			need.Reason, need.Actions = api.ReasonAcceptance, []string{api.ActAccept, api.ActRework}
			standing, err := control.StandingIn(tx, order.ID)
			if err != nil {
				return nil, err
			}
			need.Standing = &standing
		case order.State == workorders.Parked && need.Lane != nil && waitsOnAPerson(wakes[need.Lane.ID]) && !seen[order.ID]:
			need.Reason, need.Actions, need.Wakes = api.ReasonResume, []string{api.ActResume}, wakeViews(wakes[need.Lane.ID])
		default:
			continue
		}
		since, err := enteredAt(tx, *order)
		if err != nil {
			return nil, err
		}
		items = append(items, api.NeedItem{Kind: api.NeedWork, ID: order.ID, Since: since, Work: &need})
	}
	slices.SortStableFunc(items, func(a, b api.NeedItem) int { return cmp.Or(cmp.Compare(a.Since, b.Since), cmp.Compare(a.ID, b.ID)) })
	return items, nil
}

// acceptedByApproving reports whether approving one of a work order's waiting plan approvals would accept it:
// the work order waits for acceptance, and the approval was raised for the commit its branch holds now. An
// approval of an earlier commit accepts nothing, so the work order's acceptance is still asked.
func acceptedByApproving(evidence ports.EvidenceReader, order workorders.WorkOrder, approvals []authority.Approval) (bool, error) {
	if order.State != workorders.Acceptance {
		return false, nil
	}
	for _, approval := range approvals {
		current, err := control.PlanApprovalIsCurrent(evidence, order, approval)
		if err != nil || current {
			return current, err
		}
	}
	return false, nil
}

// planApprovalNeed is what a member reads of a plan version they are asked to approve, with the answers
// the version drafts: those its record holds, or for a branch's version, which the ledger holds no record of
// until it is approved, those its request carries.
func planApprovalNeed(tx ports.ReadTx, a authority.Approval) (*api.PlanApprovalNeed, error) {
	detail := api.PlanDetailOf(a)
	counts := detail.Counts
	if counts == nil {
		counts = map[string]int{}
	}
	drafts := nonNil(detail.Drafts)
	version, err := tx.Planning().PlanVersion(a.Plan, a.PlanVersion)
	switch {
	case err == nil:
		drafts = nonNil(version.Decisions)
	case !errors.Is(err, ports.ErrNotFound):
		return nil, err
	}
	need := &api.PlanApprovalNeed{
		Approval: a, Title: detail.Title, Version: a.PlanVersion, RequestedBy: a.RequestedBy, Counts: counts, Decisions: detail.Decisions,
		Drafts: drafts, Difference: detail.Difference, Summary: detail.Difference.Summary(), Adoption: detail.Adoption,
	}
	if a.Plan == "" {
		return need, nil
	}
	plan, err := tx.Planning().Plan(a.Plan)
	if errors.Is(err, ports.ErrNotFound) {
		return need, nil
	}
	if err != nil {
		return nil, err
	}
	if need.Replaced, err = control.ReplacedDrafts(tx.Questions(), a.Plan, a.PlanVersion, drafts); err != nil {
		return nil, err
	}
	need.Revision, err = revisionOfApproval(tx, plan, a)
	return need, err
}

// contractReviewNeeds lists the work whose contract waits for a member's review and that no plan
// approval stands behind. Work a pending approval covers waits for that approval, which is the one thing
// a member is asked for, and work an approved one covers is passed.
func contractReviewNeeds(tx ports.ReadTx, orders []workorders.WorkOrder) ([]api.NeedItem, error) {
	covered, err := control.ReadCoverage(tx.Approvals(), tx.Planning(), authority.ApprovalApproved, authority.ApprovalPending)
	if err != nil {
		return nil, err
	}
	var items []api.NeedItem
	for i := range orders {
		waits, err := control.WaitsOnAMember(tx.Planning(), orders[i], covered)
		if err != nil || !waits {
			if err != nil {
				return nil, err
			}
			continue
		}
		contract, err := tx.Planning().Contract(orders[i].ContractID, orders[i].ContractVersion)
		if err != nil {
			return nil, err
		}
		since, err := enteredAt(tx, orders[i])
		if err != nil {
			return nil, err
		}
		items = append(items, api.NeedItem{
			Kind: api.NeedContractReview, ID: orders[i].ID, Since: since,
			ContractReview: &api.ContractReviewNeed{WorkOrder: orders[i], Contract: contract.Title, Version: contract.Version, Plan: contract.PlanID},
		})
	}
	return items, nil
}

func (h *handler) readNeedsYou(tx ports.ReadTx, _ *http.Request, stamp api.Stamp) (any, error) {
	items, err := h.needs(tx, stamp)
	return api.NeedsYou{Stamp: stamp, Items: items}, err
}

func (h *handler) readStuck(tx ports.ReadTx, _ *http.Request, stamp api.Stamp) (any, error) {
	f, err := h.readFloor(tx)
	if err != nil {
		return nil, err
	}
	wakes, err := wakesOf(tx)
	if err != nil {
		return nil, err
	}
	items, err := h.stuck(tx, f, wakes, stamp.AsOf)
	return api.Stuck{Stamp: stamp, Items: items}, err
}

// plansByTitle lists the plans a person made, the way a person looks one up. Most plan IDs are
// generated, so an order by ID would read as no order at all. Recording a review makes a plan to carry
// its contract, which is how the ledger holds work, and the review stands under the plan it reads.
func plansByTitle(tx ports.ReadTx) ([]planning.Plan, error) {
	plans, err := tx.Planning().Plans()
	if err != nil {
		return nil, err
	}
	contracts, err := tx.Planning().Contracts()
	if err != nil {
		return nil, err
	}
	orders, err := tx.WorkOrders().List()
	if err != nil {
		return nil, err
	}
	reviewing := map[string]bool{}
	for _, order := range orders {
		reviewing[order.ContractID] = order.ReviewsPlan != ""
	}
	carriers := map[string]bool{}
	for _, contract := range contracts {
		carriers[contract.PlanID] = carriers[contract.PlanID] || reviewing[contract.ID]
	}
	plans = slices.DeleteFunc(plans, func(plan planning.Plan) bool { return carriers[plan.ID] })
	slices.SortStableFunc(plans, func(a, b planning.Plan) int { return cmp.Or(cmp.Compare(a.Title, b.Title), cmp.Compare(a.ID, b.ID)) })
	return plans, nil
}

// plannedOnly leaves out the reviews. A review pins the contract of the work it judges, but it is no part
// of the plan that contract belongs to: it is not counted in a milestone, laid out in a layer, or on the
// critical path.
func plannedOnly(orders []workorders.WorkOrder) []workorders.WorkOrder {
	return slices.DeleteFunc(slices.Clone(orders), func(order workorders.WorkOrder) bool { return order.Kind == workorders.KindReview })
}

func (h *handler) readRoadmap(tx ports.ReadTx, _ *http.Request, stamp api.Stamp) (any, error) {
	plans, err := plansByTitle(tx)
	if err != nil {
		return nil, err
	}
	goals, err := tx.Planning().Goals()
	if err != nil {
		return nil, err
	}
	contracts, err := tx.Planning().Contracts()
	if err != nil {
		return nil, err
	}
	orders, err := tx.WorkOrders().List()
	if err != nil {
		return nil, err
	}
	orders = plannedOnly(orders)
	planOf := map[string]string{}
	for _, contract := range contracts {
		planOf[contract.ID] = contract.PlanID
	}
	progress := map[string]*api.PlanProgress{}
	for _, plan := range plans {
		progress[plan.ID] = &api.PlanProgress{
			Plan: plan, States: map[workorders.State]int64{}, Milestones: []api.MilestoneProgress{}, Layers: [][]string{},
			CriticalPath: api.CriticalPath{WorkOrders: []string{}}, Work: []api.PlannedWork{},
		}
	}
	accepted, err := acceptedOnly(tx.Repositories())
	if err != nil {
		return api.Roadmap{}, err
	}
	if err := layOutPlans(tx.Planning(), accepted, progress, contracts, orders); err != nil {
		return nil, err
	}
	for _, order := range orders {
		row, ok := progress[planOf[order.ContractID]]
		if !ok {
			continue
		}
		row.Total++
		row.States[order.State]++
		if order.State == workorders.Done {
			row.Done++
		}
	}
	view := api.Roadmap{Stamp: stamp, Goals: []api.GoalProgress{}}
	under := func(goal string) []api.PlanProgress {
		out := []api.PlanProgress{}
		for _, plan := range plans {
			if plan.GoalID == goal {
				out = append(out, *progress[plan.ID])
			}
		}
		return out
	}
	for i := range goals {
		view.Goals = append(view.Goals, api.GoalProgress{Goal: &goals[i], Plans: under(goals[i].ID)})
	}
	if loose := under(""); len(loose) != 0 {
		view.Goals = append(view.Goals, api.GoalProgress{Plans: loose})
	}
	return view, nil
}

// layOutPlans fills in how each plan places its work: its milestones in priority order with the progress
// of each, the layers of its work orders, and its critical path. The ledger computes all of it from the
// dependencies it holds, with no model (FR-MILE-5). A work order counts in a milestone through its
// contract, at the contract's newest version.
func layOutPlans(planned ports.PlanningReader, accepted map[string]bool, progress map[string]*api.PlanProgress, contracts []planning.Contract, orders []workorders.WorkOrder) error {
	type place struct{ plan, milestone string }
	placed := map[string]place{}
	for _, contract := range contracts {
		placed[contract.ID] = place{plan: contract.PlanID, milestone: contract.Milestone}
	}
	waits, err := orderDependencies(planned)
	if err != nil {
		return err
	}
	ordersOf := map[string][]workorders.WorkOrder{}
	for _, order := range orders {
		if plan := placed[order.ContractID].plan; progress[plan] != nil {
			ordersOf[plan] = append(ordersOf[plan], order)
		}
	}
	for id, row := range progress {
		held := ordersOf[id]
		row.Milestones, err = milestonesOf(planned, accepted, id, held, func(order workorders.WorkOrder) string { return placed[order.ContractID].milestone })
		if err != nil {
			return err
		}
		row.Layers, row.CriticalPath = layersOf(held, waits)
		for _, order := range held {
			row.Work = append(row.Work, api.PlannedWork{
				ID: order.ID, Title: order.Title, State: order.State, Size: order.Size, Milestone: placed[order.ContractID].milestone,
			})
		}
	}
	return nil
}

// orderDependencies reads every dependency as the work order it names, whichever kind it is. A
// dependency a file wrote names its target by the ID the file gave it and the source, which the
// external links resolve.
func orderDependencies(planned ports.PlanningReader) (map[string][]string, error) {
	links, err := planned.Links()
	if err != nil {
		return nil, err
	}
	byKey := map[ledger.ExternalKey]string{}
	for _, link := range links {
		if link.RecordKind == workorders.SubjectKind {
			byKey[link.Key] = link.RecordID
		}
	}
	edges, err := planned.AllDependencies()
	if err != nil {
		return nil, err
	}
	waits := map[string][]string{}
	for _, edge := range edges {
		target := edge.DependsOn
		if key, external := edge.Key(); external {
			resolved, known := byKey[key]
			if !known {
				continue
			}
			target = resolved
		}
		waits[edge.WorkOrder] = append(waits[edge.WorkOrder], target)
	}
	return waits, nil
}

// layersOf groups a plan's work orders into layers and finds its critical path by remaining weight. The graph holds the
// plan's own work, so a dependency on work of another plan is not in it. A graph with a cycle has
// neither, because intake refuses a plan with one and no order of that work exists.
func layersOf(held []workorders.WorkOrder, waits map[string][]string) ([][]string, api.CriticalPath) {
	graph := planning.NewGraph()
	byID := map[string]workorders.WorkOrder{}
	for _, order := range held {
		graph.AddNode(order.ID)
		byID[order.ID] = order
	}
	for _, order := range held {
		for _, target := range waits[order.ID] {
			if _, inPlan := byID[target]; inPlan {
				graph.AddEdge(order.ID, target)
			}
		}
	}
	layers, err := graph.Layers()
	if err != nil {
		return [][]string{}, api.CriticalPath{WorkOrders: []string{}}
	}
	// Work that is done, and work that was canceled and will never run, leaves the weight. Failed work
	// keeps it, because it still has to be redone.
	remaining := func(id string) int {
		if state := byID[id].State; state == workorders.Done || state == workorders.Canceled {
			return 0
		}
		return planning.SizeWeight(byID[id].Size)
	}
	path, weight, err := graph.CriticalPath(remaining)
	if err != nil || path == nil {
		path = []string{}
	}
	if layers == nil {
		layers = [][]string{}
	}
	return layers, api.CriticalPath{WorkOrders: path, Weight: weight}
}

// acceptedOnly names the repositories where a work order's `done` means a member accepted it and not that it
// merged: those at person_merges with review target branch, where the person merges the branch and
// ropeshift does not see the merge (ADR-0020, ADR-0022).
func acceptedOnly(repositories ports.RepositoryReader) (map[string]bool, error) {
	all, err := repositories.Repositories()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, record := range all {
		if record.Level() == ledger.PersonMerges && record.Target() == ledger.ReviewOnBranch {
			out[record.ID] = true
		}
	}
	return out, nil
}

// milestonesOf lists a plan's milestones by priority, with how far each one's work got. A milestone is
// a document, so its newest version says what it depends on and how it exits. A milestone whose done work
// spans repositories reads as accepted, not merged while any of it is in a repository accepted names,
// because a milestone across repositories is complete only once its work merged in every one (FR-REPO-4).
func milestonesOf(planned ports.PlanningReader, accepted map[string]bool, plan string, held []workorders.WorkOrder, milestone func(workorders.WorkOrder) string) ([]api.MilestoneProgress, error) {
	documents, err := planned.PlanDocuments(plan)
	if err != nil {
		return nil, err
	}
	out := []api.MilestoneProgress{}
	for _, document := range documents {
		if document.Kind != planning.FileMilestone {
			continue
		}
		latest, err := planned.LatestDocumentVersion(document.ID)
		if err != nil {
			return nil, err
		}
		var file planning.MilestoneFile
		if err := latest.Decode(&file); err != nil {
			return nil, err
		}
		row := api.MilestoneProgress{
			ID: file.ID, Title: document.Title, Priority: file.Priority, DependsOn: nonNil(file.DependsOn), ExitCriteria: nonNil(file.ExitCriteria),
			Existing: file.Existing,
		}
		started, unmerged := false, false
		repositories := map[string]bool{}
		for _, order := range held {
			if milestone(order) != file.ID {
				continue
			}
			row.Total++
			if order.State == workorders.Done {
				row.Done++
			}
			started = started || beyondReady(order.State)
			repositories[order.RepositoryID] = true
			unmerged = unmerged || accepted[order.RepositoryID]
		}
		switch {
		case file.Existing:
			row.State = api.MilestoneExisting
		case row.Total > 0 && row.Done == row.Total && len(repositories) > 1 && unmerged:
			row.State = api.MilestoneAcceptedNotMerged
		case row.Total > 0 && row.Done == row.Total:
			row.State = api.MilestoneDone
		case started:
			row.State = api.MilestoneInProgress
		default:
			row.State = api.MilestoneNotStarted
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b api.MilestoneProgress) int {
		return cmp.Or(cmp.Compare(a.Priority, b.Priority), cmp.Compare(a.ID, b.ID))
	})
	finished := map[string]bool{}
	for _, row := range out {
		finished[row.ID] = row.State == api.MilestoneDone || row.State == api.MilestoneExisting || row.State == api.MilestoneAcceptedNotMerged
	}
	for i := range out {
		if out[i].State != api.MilestoneNotStarted {
			continue
		}
		for _, dependency := range out[i].DependsOn {
			if done, found := finished[dependency]; found && !done {
				out[i].State = api.MilestoneWaiting
			}
		}
	}
	return out, nil
}

// beyondReady says work went past the states that come before it can be claimed.
func beyondReady(state workorders.State) bool {
	switch state {
	case workorders.Intake, workorders.Planning, workorders.ContractReview, workorders.Ready:
		return false
	}
	return true
}

// nonNil keeps an empty list a list in JSON.
func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

// unresolved walks past the bound of one read, which a filter stops at.
func unresolved(tx ports.ReadTx, filter ports.RecordFilter) ([]questions.Question, error) {
	out := []questions.Question{}
	for {
		page, err := tx.Questions().Questions(filter)
		if err != nil {
			return nil, err
		}
		for _, question := range page {
			if question.Status.Unresolved() {
				out = append(out, question)
			}
		}
		if filter.Text != "" || len(page) < ports.MaxRecords {
			return out, nil
		}
		filter.After = page[len(page)-1].ID
	}
}

// planVersionStandings names, for each plan, the newest of its versions whose approval waits for a member and
// the newest a member approved.
func planVersionStandings(tx ports.ReadTx) (waiting, approved map[string]int64, err error) {
	approvals, err := tx.Approvals().All()
	if err != nil {
		return nil, nil, err
	}
	waiting, approved = map[string]int64{}, map[string]int64{}
	for _, approval := range approvals {
		if approval.Kind != authority.KindPlan {
			continue
		}
		if approval.Open() && approval.PlanVersion > waiting[approval.Plan] {
			waiting[approval.Plan] = approval.PlanVersion
		}
		if approval.State == authority.ApprovalApproved && approval.PlanVersion > approved[approval.Plan] {
			approved[approval.Plan] = approval.PlanVersion
		}
	}
	return waiting, approved, nil
}

func (h *handler) readPlans(tx ports.ReadTx, _ *http.Request, stamp api.Stamp) (any, error) {
	plans, err := plansByTitle(tx)
	if err != nil {
		return nil, err
	}
	contracts, err := tx.Planning().Contracts()
	if err != nil {
		return nil, err
	}
	orders, err := tx.WorkOrders().List()
	if err != nil {
		return nil, err
	}
	unanswered, err := unresolved(tx, ports.RecordFilter{})
	if err != nil {
		return nil, err
	}
	// What a plan's review left open is the questions about the plan, each named with its scope and the plan's
	// title, read once for them all.
	open, err := questionViews(tx, slices.DeleteFunc(unanswered, func(question questions.Question) bool {
		return question.SubjectKind != planning.SubjectPlan
	}))
	if err != nil {
		return nil, err
	}
	waiting, approved, err := planVersionStandings(tx)
	if err != nil {
		return nil, err
	}
	links, err := tx.Planning().Links()
	if err != nil {
		return nil, err
	}
	keyOf := map[string]string{}
	for _, link := range links {
		if link.RecordKind == planning.SubjectContract {
			keyOf[link.RecordID] = link.Key.NativeID
		}
	}
	// A work order is in the milestone of its contract's newest version.
	milestoneOf := map[string]string{}
	for _, contract := range contracts {
		milestoneOf[contract.ID] = contract.Milestone
	}
	accepted, err := acceptedOnly(tx.Repositories())
	if err != nil {
		return nil, err
	}
	view := api.Plans{Stamp: stamp, Plans: []api.PlanDetail{}, Unplanned: []workorders.WorkOrder{}, Reviews: []workorders.WorkOrder{}}
	if view.WorkActions, err = allowances(tx, orders); err != nil {
		return nil, err
	}
	held := map[string]bool{}
	for _, plan := range plans {
		detail := api.PlanDetail{
			Plan: plan, Contracts: []api.ContractHistory{}, Reviews: []api.PlanReview{}, WorkOrders: []workorders.WorkOrder{},
			PendingVersion: waiting[plan.ID], ApprovedVersion: approved[plan.ID],
		}
		if plan.GoalID != "" {
			goal, err := tx.Planning().Goal(plan.GoalID)
			if err != nil {
				return nil, err
			}
			detail.Goal = &goal
		}
		ofPlan := map[string]bool{}
		for _, contract := range contracts {
			if contract.PlanID != plan.ID {
				continue
			}
			ofPlan[contract.ID] = true
			if last := len(detail.Contracts) - 1; last >= 0 && detail.Contracts[last].ID == contract.ID {
				detail.Contracts[last].Versions = append(detail.Contracts[last].Versions, contract)
				continue
			}
			detail.Contracts = append(detail.Contracts, api.ContractHistory{ID: contract.ID, Key: keyOf[contract.ID], Versions: []planning.Contract{contract}})
		}
		for _, order := range orders {
			held[order.ID] = held[order.ID] || order.ReviewsPlan == plan.ID || ofPlan[order.ContractID]
			switch {
			case order.ReviewsPlan == plan.ID:
				review := api.PlanReview{WorkOrder: order, Open: []api.QuestionView{}}
				for _, question := range open {
					if question.SubjectID == plan.ID {
						review.Open = append(review.Open, question)
					}
				}
				detail.Reviews = append(detail.Reviews, review)
			case order.Kind == workorders.KindReview:
				// It pins this plan's contract, and is held by the plan so it is not unplanned, but the
				// plan's work is what the plan planned.
			case ofPlan[order.ContractID]:
				detail.WorkOrders = append(detail.WorkOrders, order)
			}
		}
		detail.Milestones, err = milestonesOf(tx.Planning(), accepted, plan.ID, detail.WorkOrders, func(order workorders.WorkOrder) string { return milestoneOf[order.ContractID] })
		if err != nil {
			return nil, err
		}
		view.Plans = append(view.Plans, detail)
	}
	for _, order := range orders {
		// A review is listed on its own while it waits or runs, so a member can start it, and once it ends the
		// work it judged lists it. It is never unplanned work, whether or not its contract is in a plan.
		if order.Kind == workorders.KindReview {
			if order.OpenReview() {
				view.Reviews = append(view.Reviews, order)
			}
			continue
		}
		if !held[order.ID] {
			view.Unplanned = append(view.Unplanned, order)
		}
	}
	return view, nil
}

// serveDocuments lists the sources before it opens a read, because a source runs a subprocess for each
// file, and nothing that slow belongs inside a read of the ledger. A file a plan recorded names its
// record, which the view lists with every version beside how the plan's requirements trace to work.
func (h *handler) serveDocuments(w http.ResponseWriter, r *http.Request) {
	listed := []api.Document{}
	for _, source := range h.Knowledge {
		documents, err := source.List(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		for _, document := range documents {
			listed = append(listed, documentBody(document))
		}
	}
	h.serveView(w, r, func(tx ports.ReadTx, _ *http.Request, stamp api.Stamp) (any, error) {
		plans, err := recordedPlans(tx)
		if err != nil {
			return nil, err
		}
		marked, err := markRecorded(tx, listed, plans)
		if err != nil {
			return nil, err
		}
		view := api.Documents{Stamp: stamp, Documents: marked, Requirements: []planning.Requirement{}, Plans: plans}
		versions, err := tx.Planning().Requirements()
		if err != nil {
			return nil, err
		}
		for _, requirement := range versions {
			if last := len(view.Requirements) - 1; last >= 0 && view.Requirements[last].Key == requirement.Key {
				view.Requirements[last] = requirement
				continue
			}
			view.Requirements = append(view.Requirements, requirement)
		}
		return view, nil
	})
}

// allDecisions walks past the bound of one read.
func allDecisions(tx ports.ReadTx, filter ports.RecordFilter) ([]questions.Decision, error) {
	out := []questions.Decision{}
	for {
		page, err := tx.Questions().Decisions(filter)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if filter.Text != "" || len(page) < ports.MaxRecords {
			return out, nil
		}
		filter.After = page[len(page)-1].ID
	}
}

func (h *handler) readDecisions(tx ports.ReadTx, r *http.Request, stamp api.Stamp) (any, error) {
	rows, err := allDecisions(tx, filterOf(r))
	if err != nil {
		return nil, err
	}
	every, err := allDecisions(tx, ports.RecordFilter{})
	if err != nil {
		return nil, err
	}
	replacedBy, replaces := map[string]string{}, map[string]string{}
	for _, row := range every {
		if row.Supersedes != "" {
			replacedBy[row.Supersedes], replaces[row.ID] = row.ID, row.Supersedes
		}
	}
	// An orchestrator decides as its worker, so a decider that is a worker reads by the worker's name.
	workers, err := tx.Workers().List()
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(workers))
	for _, worker := range workers {
		names[worker.ID] = worker.Name
	}
	titles, err := decisionScopeTitles(tx, rows)
	if err != nil {
		return nil, err
	}
	view := api.Decisions{Stamp: stamp, Decisions: make([]api.DecisionLine, 0, len(rows))}
	for _, row := range rows {
		root := row.ID
		for seen := map[string]bool{root: true}; replaces[root] != "" && !seen[replaces[root]]; seen[root] = true {
			root = replaces[root]
		}
		lineage := []string{root}
		for at := root; replacedBy[at] != "" && !slices.Contains(lineage, replacedBy[at]); at = replacedBy[at] {
			lineage = append(lineage, replacedBy[at])
		}
		view.Decisions = append(view.Decisions, api.DecisionLine{
			Decision: row, Lineage: lineage, SupersededBy: replacedBy[row.ID], DecidedByName: names[row.DecidedBy], ScopeTitle: titles[row.Scope],
		})
	}
	return view, nil
}

func (h *handler) readQuestions(tx ports.ReadTx, r *http.Request, stamp api.Stamp) (any, error) {
	open, err := unresolved(tx, filterOf(r))
	if err != nil {
		return nil, err
	}
	added, err := tx.Questions().Categories()
	if err != nil {
		return nil, err
	}
	categories := append(questions.BuiltIn(), added...)
	slices.Sort(categories)
	views, err := questionViews(tx, open)
	if err != nil {
		return nil, err
	}
	return api.Questions{Stamp: stamp, Categories: categories, Questions: views}, nil
}

func (h *handler) readInProgress(tx ports.ReadTx, _ *http.Request, stamp api.Stamp) (any, error) {
	f, err := h.readFloor(tx)
	if err != nil {
		return nil, err
	}
	view := api.InProgress{Stamp: stamp, Items: []api.WorkLine{}}
	for _, order := range f.orders {
		if order.State != workorders.Claimed && order.State != workorders.Running {
			continue
		}
		since, err := enteredAt(tx, order)
		if err != nil {
			return nil, err
		}
		view.Items = append(view.Items, api.WorkLine{WorkOrder: order, Lane: f.ofWork[order.ID], Since: since})
	}
	slices.SortStableFunc(view.Items, func(a, b api.WorkLine) int {
		return cmp.Or(cmp.Compare(a.Since, b.Since), cmp.Compare(a.WorkOrder.ID, b.WorkOrder.ID))
	})
	return view, nil
}

func (h *handler) readOnHold(tx ports.ReadTx, _ *http.Request, stamp api.Stamp) (any, error) {
	f, err := h.readFloor(tx)
	if err != nil {
		return nil, err
	}
	wakes, err := wakesOf(tx)
	if err != nil {
		return nil, err
	}
	view := api.OnHold{Stamp: stamp, Items: []api.HeldLane{}}
	for _, order := range f.orders {
		if order.State != workorders.Parked {
			continue
		}
		since, err := enteredAt(tx, order)
		if err != nil {
			return nil, err
		}
		item := api.HeldLane{WorkOrder: order, Lane: f.ofWork[order.ID], Since: since, Wakes: []api.WakeView{}}
		if item.Lane != nil {
			item.Wakes = wakeViews(wakes[item.Lane.ID])
		}
		view.Items = append(view.Items, item)
	}
	slices.SortStableFunc(view.Items, func(a, b api.HeldLane) int {
		return cmp.Or(cmp.Compare(a.Since, b.Since), cmp.Compare(a.WorkOrder.ID, b.WorkOrder.ID))
	})
	return view, nil
}

func (h *handler) readCompleted(tx ports.ReadTx, _ *http.Request, stamp api.Stamp) (any, error) {
	orders, err := tx.WorkOrders().List()
	if err != nil {
		return nil, err
	}
	view := api.Completed{Stamp: stamp, Items: []api.CompletedWork{}}
	for _, order := range orders {
		// A review is read with the work it judged, whose standing lists it and its verdicts. Listed on its own,
		// every piece of work would come twice.
		if order.Kind == workorders.KindReview {
			continue
		}
		if !order.State.Completed() && !workorders.Terminal(order.State) {
			continue
		}
		standing, err := control.StandingIn(tx, order.ID)
		if err != nil {
			return nil, err
		}
		since, err := enteredAt(tx, order)
		if err != nil {
			return nil, err
		}
		reason, detail, err := arrivalReason(tx, order)
		if err != nil {
			return nil, err
		}
		view.Items = append(view.Items, api.CompletedWork{WorkOrder: order, Standing: standing, Since: since, Reason: reason, Detail: detail})
	}
	slices.SortStableFunc(view.Items, func(a, b api.CompletedWork) int {
		return cmp.Or(cmp.Compare(b.Since, a.Since), cmp.Compare(a.WorkOrder.ID, b.WorkOrder.ID))
	})
	return view, nil
}

func (h *handler) workerLines(tx ports.ReadTx) ([]api.WorkerLine, error) {
	workers, err := tx.Workers().List()
	if err != nil {
		return nil, err
	}
	out := make([]api.WorkerLine, 0, len(workers))
	for _, worker := range workers {
		line := api.WorkerLine{Worker: worker, Drivers: worker.Manifest.Results()}
		if line.Freshness, err = h.workers.WorkerFreshness(tx, worker); err != nil {
			return nil, err
		}
		if line.ReportFreshness, err = h.workers.ReportFreshness(tx, worker); err != nil {
			return nil, err
		}
		row, err := tx.Workers().Orchestrator(worker.ID)
		switch {
		case err == nil:
			wait, waitErr := h.workers.OrchestratorWait(tx, row)
			if waitErr != nil {
				return nil, waitErr
			}
			line.Orchestrator = &api.Orchestrator{Orchestrator: row, WaitingFor: waitingFor(wait)}
		case !isNotFound(err):
			return nil, err
		}
		out = append(out, line)
	}
	return out, nil
}

func (h *handler) readShopFloor(tx ports.ReadTx, _ *http.Request, stamp api.Stamp) (any, error) {
	lines, err := h.workerLines(tx)
	if err != nil {
		return nil, err
	}
	lanes, err := h.workers.LanesIn(tx)
	if err != nil {
		return nil, err
	}
	orders, err := tx.WorkOrders().List()
	if err != nil {
		return nil, err
	}
	rows, err := tx.Lanes().Rows()
	if err != nil {
		return nil, err
	}
	titles, byLane := map[string]string{}, map[string]ledger.LaneRow{}
	for _, order := range orders {
		titles[order.ID] = order.Title
	}
	for _, row := range rows {
		byLane[row.ID] = row
	}
	view := api.ShopFloor{Stamp: stamp, Workers: lines, Lanes: make([]api.FloorLane, 0, len(lanes))}
	for _, lane := range lanes {
		view.Lanes = append(view.Lanes, api.FloorLane{LaneView: lane, WorkOrderTitle: titles[lane.WorkOrder], Archivable: archivable(byLane[lane.ID])})
	}
	return view, nil
}

// forgeLines reads each worker's forge sign-in for each repository, with the repository's name.
func forgeLines(tx ports.ReadTx) ([]api.ForgeLine, error) {
	health, err := control.ForgeHealthIn(tx)
	if err != nil {
		return nil, err
	}
	out := make([]api.ForgeLine, 0, len(health))
	for _, h := range health {
		line := api.ForgeLine{ForgeHealth: h}
		if record, err := tx.Repositories().Repository(h.Repository); err == nil {
			line.RepositoryName = record.Name
		}
		out = append(out, line)
	}
	return out, nil
}

func (h *handler) readOperations(tx ports.ReadTx, _ *http.Request, stamp api.Stamp) (any, error) {
	workspace, err := tx.Workspace()
	if err != nil {
		return nil, err
	}
	view := api.Operations{
		Stamp: stamp, Fleet: api.Fleet{Mode: workspace.FleetMode, Caps: workspace.Caps()},
		WorkOrders: map[workorders.State]int64{}, Lanes: map[string]int64{}, Sources: []planning.SourceBinding{},
	}
	orders, err := tx.WorkOrders().List()
	if err != nil {
		return nil, err
	}
	for _, order := range orders {
		view.WorkOrders[order.State]++
	}
	lanes, err := h.workers.LanesIn(tx)
	if err != nil {
		return nil, err
	}
	for _, lane := range lanes {
		view.Lanes[lane.ActualState]++
	}
	lines, err := h.workerLines(tx)
	if err != nil {
		return nil, err
	}
	view.Workers = int64(len(lines))
	for _, line := range lines {
		if line.Freshness == ledger.FreshnessObserved {
			view.Fresh++
		}
	}
	waiting, err := h.needs(tx, stamp)
	if err != nil {
		return nil, err
	}
	view.Waiting = int64(len(waiting))
	bindings, err := tx.Planning().Bindings()
	if err != nil {
		return nil, err
	}
	if bindings != nil {
		view.Sources = bindings
	}
	if view.Forges, err = forgeLines(tx); err != nil {
		return nil, err
	}
	length, err := api.ParseWindow(api.DefaultMeasureWindow)
	if err != nil {
		return nil, err
	}
	view.Measures, err = control.ReportIn(tx, api.DefaultMeasureWindow, length)
	return view, err
}

// cardNeed is what a member reads of a card: the operation it runs, and the delegate that prepared it by
// its credential's label and the member it acts for. A credential whose record was removed reads as its ID.
func cardNeed(members ports.MemberReader, threads ports.ConversationReader, card authority.Approval) (*api.CardNeed, error) {
	detail, err := authority.CardDetailOf(card)
	if err != nil {
		return nil, err
	}
	need := &api.CardNeed{Approval: card, Detail: detail}
	if need.Conversation, err = cardConversation(members, threads, card); err != nil {
		return nil, err
	}
	kind, id, _ := strings.Cut(card.RequestedBy, ":")
	member, credential, ok := authority.ActingFor(ledger.Actor{Kind: ledger.ActorKind(kind), ID: id})
	if !ok {
		need.Label = card.RequestedBy
		return need, nil
	}
	need.Member, need.Label = member, credential
	record, err := members.Delegate(credential)
	switch {
	case errors.Is(err, ports.ErrNotFound):
	case err != nil:
		return nil, err
	default:
		need.Label, need.DelegateKind = record.Label, record.Kind
	}
	return need, nil
}

// cardConversation names the conversation a card came from, when an assistant's credential prepared it,
// and nothing for any other card. A conversation since deleted reads with no name, and so does one that is
// not the credential's member's.
func cardConversation(members ports.MemberReader, threads ports.ConversationReader, card authority.Approval) (*api.CardConversation, error) {
	kind, id, _ := strings.Cut(card.RequestedBy, ":")
	_, credential, ok := authority.ActingFor(ledger.Actor{Kind: ledger.ActorKind(kind), ID: id})
	if !ok {
		return nil, nil
	}
	record, err := members.Delegate(credential)
	if errors.Is(err, ports.ErrNotFound) {
		return nil, nil
	}
	if err != nil || !record.Assistant() {
		return nil, err
	}
	thread, err := threads.Conversation(record.Conversation)
	if errors.Is(err, ports.ErrNotFound) || (err == nil && thread.Member != record.Member) {
		return &api.CardConversation{ID: record.Conversation, Deleted: true}, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.CardConversation{ID: thread.ID, Name: thread.Name}, nil
}
