package server

import (
	"cmp"
	"encoding/json"
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
// a lane says why it stopped.
func arrivalReason(tx ports.ReadTx, order workorders.WorkOrder) (string, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: workorders.SubjectKind, ID: order.ID})
	if err != nil {
		return "", err
	}
	into := edgesInto(order.State)
	for i := len(events) - 1; i >= 0; i-- {
		if !into[events[i].Type] {
			continue
		}
		var change workorders.Change
		if err := json.Unmarshal(events[i].Data, &change); err != nil {
			return "", err
		}
		return change.Reason, nil
	}
	return "", nil
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

// stuck finds what stopped: the design's five, the longest stop first. A lane on work the ledger still
// counts as moving stands here too, because its work order says nothing of a breaker or a stall.
func (h *handler) stuck(tx ports.ReadTx, f floor, wakes map[string][]parking.Wake, now int64) ([]api.StuckItem, error) {
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
		case workorders.Lapsed, workorders.Blocked, workorders.Parked:
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
			if need.Detail, err = arrivalReason(tx, *order); err != nil {
				return nil, err
			}
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

func waitingBatches(tx ports.ReadTx) ([]questions.Presented, error) {
	batches, err := tx.Questions().WaitingBatches()
	if err != nil {
		return nil, err
	}
	out := []questions.Presented{}
	for _, batch := range batches {
		rows, err := tx.Questions().BatchQuestions(batch.ID)
		if err != nil {
			return nil, err
		}
		if presented := questions.Present(batch, rows, ""); len(presented.Items) != 0 {
			out = append(out, presented)
		}
	}
	return out, nil
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
	for i := range approvals {
		switch {
		case !approvals[i].Open():
		case approvals[i].Kind == authority.KindPlan:
			items = append(items, api.NeedItem{Kind: api.NeedPlanApproval, ID: approvals[i].ID, Since: approvals[i].RaisedAt, PlanApproval: planApprovalNeed(approvals[i])})
		default:
			items = append(items, api.NeedItem{Kind: api.NeedApproval, ID: approvals[i].ID, Since: approvals[i].RaisedAt, Approval: &approvals[i]})
		}
	}
	reviews, err := contractReviewNeeds(tx, f.orders)
	if err != nil {
		return nil, err
	}
	items = append(items, reviews...)
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
		switch {
		case order.State == workorders.Acceptance:
			need.Reason, need.Actions = api.ReasonAcceptance, []string{api.ActAccept, api.ActRework}
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

// planApprovalNeed is what a member reads of a plan version they are asked to approve.
func planApprovalNeed(a authority.Approval) *api.PlanApprovalNeed {
	detail := api.PlanDetailOf(a)
	counts := detail.Counts
	if counts == nil {
		counts = map[string]int{}
	}
	return &api.PlanApprovalNeed{
		Approval: a, Title: detail.Title, Version: a.PlanVersion, RequestedBy: a.RequestedBy, Counts: counts, Decisions: detail.Decisions,
		Difference: detail.Difference, Summary: detail.Difference.Summary(),
	}
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
			ContractReview: &api.ContractReviewNeed{WorkOrder: orders[i], Contract: contract.Title, Version: contract.Version},
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
	planOf := map[string]string{}
	for _, contract := range contracts {
		planOf[contract.ID] = contract.PlanID
	}
	progress := map[string]*api.PlanProgress{}
	for _, plan := range plans {
		progress[plan.ID] = &api.PlanProgress{
			Plan: plan, States: map[workorders.State]int64{}, Milestones: []api.MilestoneProgress{}, Layers: [][]string{},
			CriticalPath: api.CriticalPath{WorkOrders: []string{}},
		}
	}
	if err := layOutPlans(tx.Planning(), progress, contracts, orders); err != nil {
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
func layOutPlans(planned ports.PlanningReader, progress map[string]*api.PlanProgress, contracts []planning.Contract, orders []workorders.WorkOrder) error {
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
		row.Milestones, err = milestonesOf(planned, id, held, func(order workorders.WorkOrder) string { return placed[order.ContractID].milestone })
		if err != nil {
			return err
		}
		row.Layers, row.CriticalPath = layersOf(held, waits)
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

// milestonesOf lists a plan's milestones by priority, with how far each one's work got. A milestone is
// a document, so its newest version says what it depends on and how it exits.
func milestonesOf(planned ports.PlanningReader, plan string, held []workorders.WorkOrder, milestone func(workorders.WorkOrder) string) ([]api.MilestoneProgress, error) {
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
		}
		started := false
		for _, order := range held {
			if milestone(order) != file.ID {
				continue
			}
			row.Total++
			if order.State == workorders.Done {
				row.Done++
			}
			started = started || beyondReady(order.State)
		}
		switch {
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
		finished[row.ID] = row.State == api.MilestoneDone
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
	open, err := unresolved(tx, ports.RecordFilter{})
	if err != nil {
		return nil, err
	}
	view := api.Plans{Stamp: stamp, Plans: []api.PlanDetail{}, Unplanned: []workorders.WorkOrder{}}
	held := map[string]bool{}
	for _, plan := range plans {
		detail := api.PlanDetail{Plan: plan, Contracts: []api.ContractHistory{}, Reviews: []api.PlanReview{}, WorkOrders: []workorders.WorkOrder{}}
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
			detail.Contracts = append(detail.Contracts, api.ContractHistory{ID: contract.ID, Versions: []planning.Contract{contract}})
		}
		for _, order := range orders {
			held[order.ID] = held[order.ID] || order.ReviewsPlan == plan.ID || ofPlan[order.ContractID]
			switch {
			case order.ReviewsPlan == plan.ID:
				review := api.PlanReview{WorkOrder: order, Open: []questions.Question{}}
				for _, question := range open {
					if question.SubjectKind == planning.SubjectPlan && question.SubjectID == plan.ID {
						review.Open = append(review.Open, question)
					}
				}
				detail.Reviews = append(detail.Reviews, review)
			case ofPlan[order.ContractID]:
				detail.WorkOrders = append(detail.WorkOrders, order)
			}
		}
		view.Plans = append(view.Plans, detail)
	}
	for _, order := range orders {
		if !held[order.ID] {
			view.Unplanned = append(view.Unplanned, order)
		}
	}
	return view, nil
}

// serveDocuments lists the sources before it opens a read, because a source runs a subprocess for each
// file, and nothing that slow belongs inside a read of the ledger.
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
		view := api.Documents{Stamp: stamp, Documents: listed, Requirements: []planning.Requirement{}}
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
		view.Decisions = append(view.Decisions, api.DecisionLine{Decision: row, Lineage: lineage, SupersededBy: replacedBy[row.ID]})
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
	return api.Questions{Stamp: stamp, Categories: categories, Questions: open}, nil
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
		reason, err := arrivalReason(tx, order)
		if err != nil {
			return nil, err
		}
		view.Items = append(view.Items, api.CompletedWork{WorkOrder: order, Standing: standing, Since: since, Reason: reason})
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
		line := api.WorkerLine{Worker: worker}
		if line.Freshness, err = h.workers.WorkerFreshness(tx, worker); err != nil {
			return nil, err
		}
		row, err := tx.Workers().Orchestrator(worker.ID)
		switch {
		case err == nil:
			line.Orchestrator = &row
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
	titles := map[string]string{}
	for _, order := range orders {
		titles[order.ID] = order.Title
	}
	view := api.ShopFloor{Stamp: stamp, Workers: lines, Lanes: make([]api.FloorLane, 0, len(lanes))}
	for _, lane := range lanes {
		view.Lanes = append(view.Lanes, api.FloorLane{LaneView: lane, WorkOrderTitle: titles[lane.WorkOrder]})
	}
	return view, nil
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
	length, err := api.ParseWindow(api.DefaultMeasureWindow)
	if err != nil {
		return nil, err
	}
	view.Measures, err = control.ReportIn(tx, api.DefaultMeasureWindow, length)
	return view, err
}
