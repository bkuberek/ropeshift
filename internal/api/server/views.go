package server

import (
	"cmp"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
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
			need.Reason, need.Actions = api.ReasonLapsed, []string{api.ActReassign}
		case workorders.Blocked:
			need.Reason, need.Actions = api.ReasonBlocked, []string{api.ActResolve, api.ActReplan}
			if need.Detail, err = arrivalReason(tx, *order); err != nil {
				return nil, err
			}
		default:
			if now-since < h.longParked() {
				continue
			}
			need.Reason = api.ReasonLongParked
			if need.Lane != nil && waitsOnAPerson(wakes[need.Lane.ID]) {
				need.Actions = []string{api.ActResume}
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
		id := lane.WorkOrder
		if id == "" {
			id = lane.ID
		}
		need := api.WorkNeed{WorkOrder: order, Lane: lane}
		switch {
		case lane.ActualState == "tripped":
			need.Reason = api.ReasonTripped
			if order != nil {
				need.Actions = []string{api.ActReset}
			}
		case slices.Contains(lane.Conditions, "stalled"):
			need.Reason = api.ReasonStalled
			if order != nil {
				need.Actions = []string{api.ActRespawn}
			}
		default:
			continue
		}
		add(id, f.rows[lane.ID].ReportedAt, need)
	}
	slices.SortStableFunc(items, func(a, b api.StuckItem) int { return cmp.Or(cmp.Compare(a.Since, b.Since), cmp.Compare(a.ID, b.ID)) })
	return items, nil
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
		if approvals[i].Open() {
			items = append(items, api.NeedItem{Kind: api.NeedApproval, ID: approvals[i].ID, Since: approvals[i].RaisedAt, Approval: &approvals[i]})
		}
	}
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
			need.Reason, need.Actions = api.ReasonResume, []string{api.ActResume}
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

// plansByTitle lists plans the way a person looks one up. Most plan IDs are generated, so an order by
// ID would read as no order at all.
func plansByTitle(tx ports.ReadTx) ([]planning.Plan, error) {
	plans, err := tx.Planning().Plans()
	slices.SortStableFunc(plans, func(a, b planning.Plan) int { return cmp.Or(cmp.Compare(a.Title, b.Title), cmp.Compare(a.ID, b.ID)) })
	return plans, err
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
		progress[plan.ID] = &api.PlanProgress{Plan: plan, States: map[workorders.State]int64{}}
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
			for _, wake := range wakes[item.Lane.ID] {
				item.Wakes = append(item.Wakes, api.WakeView{ID: wake.ID, Expression: wake.Expr, NextEvalAt: wake.NextEvalAt})
			}
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
	return api.ShopFloor{Stamp: stamp, Workers: lines, Lanes: lanes}, nil
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
	if err == nil && bindings != nil {
		view.Sources = bindings
	}
	return view, err
}
