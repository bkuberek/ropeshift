package control

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// MeasureWindows are the windows the measures tick reports, and the operations view shows the first.
var MeasureWindows = []struct {
	Name   string
	Length time.Duration
}{{"7d", 7 * 24 * time.Hour}, {"24h", 24 * time.Hour}, {"30d", 30 * 24 * time.Hour}}

// Measures computes the product measures from the ledger alone, so they need no telemetry pipeline
// (FR-OBS-6, ADR-0004). It reads every page of the event log.
type Measures struct {
	store     ports.Store
	telemetry ports.Telemetry
}

// NewMeasures returns the use case. A nil telemetry exports nothing.
func NewMeasures(store ports.Store, telemetry ports.Telemetry) *Measures {
	return &Measures{store: store, telemetry: telemetry}
}

// Report computes one window's measures, the window ending at database time now.
func (m *Measures) Report(ctx context.Context, scope ledger.WorkspaceID, name string, length time.Duration) (measures.Report, error) {
	var report measures.Report
	err := m.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		report, err = ReportIn(tx, name, length)
		return err
	})
	return report, err
}

// ReportIn computes one window inside a read the caller holds.
func ReportIn(tx ports.ReadTx, name string, length time.Duration) (measures.Report, error) {
	now, err := tx.Now()
	if err != nil {
		return measures.Report{}, err
	}
	history, err := HistoryIn(tx)
	if err != nil {
		return measures.Report{}, err
	}
	return measures.Compute(history, measures.Window{Name: name, From: now - length.Milliseconds(), To: now + 1}), nil
}

// historian folds the event log into what the measures read, in revision order. Every fact comes
// from an event, and every time is the database time the event was recorded at.
type historian struct {
	tx        ports.ReadTx
	history   measures.History
	orders    map[string]int
	questions map[string]int
	asked     map[string]questions.Question
	decisions map[string]int
	recorded  map[string]int64
	citations map[string][]string
	parking   map[string]int64
	// reviews are the work orders that judge other work. A review is a lane's cost and no outcome of its own, so
	// it is no completed work order and no flow time.
	reviews   map[string]bool
	approvals map[string]authority.ApprovalState
	// orchestrators are the last projection each orchestrator's events held.
	orchestrators map[string]ledger.Orchestrator
}

// HistoryIn reads the whole event log, every page of it, into a history.
func HistoryIn(tx ports.ReadTx) (measures.History, error) {
	events, err := ReadAllEvents(tx.Events)
	if err != nil {
		return measures.History{}, err
	}
	h := &historian{
		tx: tx, orders: map[string]int{}, questions: map[string]int{}, asked: map[string]questions.Question{},
		decisions: map[string]int{}, recorded: map[string]int64{}, citations: map[string][]string{}, parking: map[string]int64{},
		reviews: map[string]bool{}, approvals: map[string]authority.ApprovalState{}, orchestrators: map[string]ledger.Orchestrator{},
	}
	for _, e := range events {
		h.read(e)
	}
	h.history.Repositories, h.history.Calibrations, err = agreementFacts(tx)
	return h.history, err
}

// agreementFacts reads what review agreement needs from the records themselves: each calibration a member's
// decision left, and the merge authority each repository holds now.
func agreementFacts(tx ports.ReadTx) ([]measures.RepositoryFacts, []measures.CalibrationFacts, error) {
	calibrations, err := tx.Evidence().Calibrations()
	if err != nil {
		return nil, nil, err
	}
	var facts []measures.CalibrationFacts
	for _, c := range calibrations {
		facts = append(facts, measures.CalibrationFacts{
			At: c.CreatedAt, Repository: c.Repository, Outcome: string(c.Outcome), Decision: string(c.Decision),
			Agreement: string(c.Agreement), Source: string(c.Source),
		})
	}
	records, err := tx.Repositories().Repositories()
	if err != nil {
		return nil, nil, err
	}
	var repositories []measures.RepositoryFacts
	for _, repo := range records {
		repositories = append(repositories, measures.RepositoryFacts{ID: repo.ID, MergeAuthority: string(repo.Level())})
	}
	return repositories, facts, nil
}

// ReviewAgreementIn reads review agreement as of now inside a read the caller holds, without the event log.
// It is evidence about the review agent, and it never feeds a merge authority level (FR-CAL-3).
func ReviewAgreementIn(tx ports.ReadTx) (measures.ReviewAgreement, error) {
	now, err := tx.Now()
	if err != nil {
		return measures.ReviewAgreement{}, err
	}
	repositories, calibrations, err := agreementFacts(tx)
	if err != nil {
		return measures.ReviewAgreement{}, err
	}
	return measures.ReviewAgreementAt(repositories, calibrations, now+1), nil
}

func (h *historian) interrupted(e ledger.Event, kind, subject string) {
	h.history.Interruptions = append(h.history.Interruptions, measures.Occasion{At: e.RecordedAt, Kind: kind, Subject: subject})
}

func (h *historian) read(e ledger.Event) {
	member := authority.PartyOf(e.Actor) == authority.PartyPerson
	switch e.Subject.Kind {
	case workorders.SubjectKind:
		h.workOrder(e, member)
	case questions.SubjectQuestion:
		h.question(e)
	case questions.SubjectDecision:
		h.decision(e)
	case authority.SubjectApproval:
		h.approval(e)
	case ledger.SubjectOrchestrator:
		h.orchestrator(e)
	}
	switch {
	case e.Type == questions.EventBatchDelivered:
		h.interrupted(e, measures.InterruptionBatch, e.Subject.ID)
	case e.Type == ledger.EventLaneReset && member:
		h.interrupted(e, measures.InterruptionForced, e.ID)
	case e.Type == ledger.EventEffectDecided && member:
		h.interrupted(e, measures.InterruptionForced, e.ID)
	case e.Type == parking.EventSignal && member:
		var signal parking.Signal
		if json.Unmarshal(e.Data, &signal) == nil && signal.Kind == parking.HumanResume {
			h.interrupted(e, measures.InterruptionForced, e.ID)
		}
	case e.Type == ledger.EventLaneEnded:
		h.laneEnded(e)
	}
}

// forcedTriggers are the moves only a person can make, each one an interruption: ending a lapsed
// holder's ownership, and taking work out of `blocked`.
var forcedTriggers = []workorders.Trigger{workorders.TriggerTakeover, workorders.TriggerResolved, workorders.TriggerReplan}

func (h *historian) workOrder(e ledger.Event, member bool) {
	id := e.Subject.ID
	if e.Type == workorders.EventCreated {
		var created workorders.WorkOrder
		if json.Unmarshal(e.Data, &created) != nil {
			return
		}
		if created.Kind == workorders.KindReview {
			h.reviews[id] = true
			return
		}
		h.orders[id] = len(h.history.WorkOrders)
		h.history.WorkOrders = append(h.history.WorkOrders, measures.WorkOrderFacts{
			ID: id, Kind: string(created.Kind), State: string(created.State), CreatedAt: e.RecordedAt, ReworkOf: created.ReworkOf,
		})
		return
	}
	var change workorders.Change
	if json.Unmarshal(e.Data, &change) != nil {
		return
	}
	if h.reviews[id] {
		// What a review's lane spent counts, by the kind that ran it, and so does a person taking it out of a
		// stop. Nothing else of a review does.
		if member && slices.Contains(forcedTriggers, change.Trigger) {
			h.interrupted(e, measures.InterruptionForced, e.ID)
		}
		switch change.Trigger {
		case workorders.TriggerLaneCompleted, workorders.TriggerLaneBlocked, workorders.TriggerLaneFailedRetry, workorders.TriggerAttemptsExhausted:
			h.laneResult(string(workorders.KindReview), e.RecordedAt, change.Payload)
		}
		return
	}
	at, known := h.orders[id]
	if !known {
		return
	}
	order := &h.history.WorkOrders[at]
	if e.Type == workorders.EventLaneParking {
		h.parking[id] = e.RecordedAt
	}
	if change.Trigger == "" {
		return
	}
	if member && slices.Contains(forcedTriggers, change.Trigger) {
		h.interrupted(e, measures.InterruptionForced, e.ID)
	}
	edge, ok := workorders.Next(workorders.State(order.State), change.Trigger)
	if !ok {
		return
	}
	order.State = string(edge.To)
	switch {
	case edge.To == workorders.Ready && order.ReadyAt == 0:
		order.ReadyAt = e.RecordedAt
	case edge.To == workorders.Done:
		order.DoneAt = e.RecordedAt
	}
	switch change.Trigger {
	case workorders.TriggerWakeFired:
		if parked := h.parking[id]; parked != 0 {
			h.history.Parks = append(h.history.Parks, measures.Park{Trigger: WakeTrigger(change.Reason), ParkedAt: parked, WokenAt: e.RecordedAt})
			delete(h.parking, id)
		}
	case workorders.TriggerLaneCompleted, workorders.TriggerLaneBlocked, workorders.TriggerLaneFailedRetry, workorders.TriggerAttemptsExhausted:
		h.laneResult(order.Kind, e.RecordedAt, change.Payload)
	case workorders.TriggerTakeover:
		// A holder whose ownership a person ended never reports what its lane spent.
		h.history.Lanes = append(h.history.Lanes, measures.LaneCost{WorkOrderKind: order.Kind, EndedAt: e.RecordedAt, LowerBound: true})
	}
}

// WakeTrigger names what fired a wake by the kind of its cause, such as `answer` or `tick`, and never
// by the record it names, so park times group by trigger.
func WakeTrigger(cause string) string {
	kind, _, _ := strings.Cut(cause, ":")
	if kind == "" {
		return "unknown"
	}
	return kind
}

// laneResult reads a lane's reported cost, which is a difference between the session's running totals
// summed over its generations, and a lower bound when a turn reported nothing (observability.md).
func (h *historian) laneResult(kind string, at int64, payload []byte) {
	var result workorders.LaneResult
	if len(payload) == 0 || json.Unmarshal(payload, &result) != nil {
		h.history.Lanes = append(h.history.Lanes, measures.LaneCost{WorkOrderKind: kind, EndedAt: at, LowerBound: true})
		return
	}
	h.history.Lanes = append(h.history.Lanes, measures.LaneCost{
		WorkOrderKind: kind, EndedAt: at, CostUSD: result.CostUSD, LowerBound: result.UnknownCostTurns > 0,
	})
}

// laneEnded is a canceled lane's end, which moves no work order.
func (h *historian) laneEnded(e ledger.Event) {
	row, err := h.tx.Lanes().Row(e.Subject.ID)
	if err != nil {
		return
	}
	kind := ""
	if at, ok := h.orders[row.WorkOrder]; ok {
		kind = h.history.WorkOrders[at].Kind
	}
	h.laneResult(kind, e.RecordedAt, e.Data)
}

func (h *historian) question(e ledger.Event) {
	switch e.Type {
	case questions.EventQuestionAttached:
		var attachment questions.Attachment
		if json.Unmarshal(e.Data, &attachment) != nil {
			return
		}
		if asked, ok := h.asked[attachment.QuestionID]; ok && asked.AskedBy != attachment.Lane {
			h.history.Attachments = append(h.history.Attachments, e.RecordedAt)
		}
		return
	case questions.EventQuestionChanged:
	default:
		return
	}
	var question questions.Question
	if json.Unmarshal(e.Data, &question) != nil {
		return
	}
	at, known := h.questions[question.ID]
	if !known {
		at = len(h.history.Questions)
		h.questions[question.ID] = at
		h.history.Questions = append(h.history.Questions, measures.QuestionFacts{ID: question.ID, AskedAt: e.RecordedAt})
	}
	facts := &h.history.Questions[at]
	h.asked[question.ID] = question
	if question.Status == questions.StatusNeedsHuman && facts.ReachedHumanAt == 0 {
		facts.ReachedHumanAt = e.RecordedAt
	}
	if question.Status.Unresolved() || facts.ClosedAt != 0 {
		return
	}
	facts.ClosedAt, facts.ClosedRung = e.RecordedAt, question.Rung
	facts.ClosedByMember = authority.PartyOf(e.Actor) == authority.PartyPerson
	facts.Repeat = facts.ReachedHumanAt != 0 && h.repeats(question, facts.AskedAt)
}

// repeats says a question was asked or decided before this one was asked: it merged into an earlier
// question, its answer is a decision recorded before it, or its answer cites one, as a person does
// who answers that this was settled already.
func (h *historian) repeats(question questions.Question, asked int64) bool {
	if into, merged := question.MergedInto(); merged {
		return h.askedBefore(into, asked)
	}
	if question.Status != questions.StatusAnswered || question.DecisionID == "" {
		return false
	}
	if h.decidedBefore(question.DecisionID, asked) {
		return true
	}
	for _, cited := range h.citations[question.DecisionID] {
		if cited != question.ID && (h.decidedBefore(cited, asked) || h.askedBefore(cited, asked)) {
			return true
		}
	}
	return false
}

func (h *historian) askedBefore(question string, asked int64) bool {
	at, ok := h.questions[question]
	return ok && h.history.Questions[at].AskedAt < asked
}

func (h *historian) decidedBefore(decision string, asked int64) bool {
	recorded, ok := h.recorded[decision]
	return ok && recorded < asked
}

func (h *historian) decision(e ledger.Event) {
	var decision questions.Decision
	if (e.Type != questions.EventDecisionRecorded && e.Type != questions.EventDecisionEnded) || json.Unmarshal(e.Data, &decision) != nil {
		return
	}
	at, known := h.decisions[decision.ID]
	if !known {
		h.recorded[decision.ID], h.citations[decision.ID] = e.RecordedAt, decision.Citations
		at = len(h.history.Decisions)
		h.decisions[decision.ID] = at
		autonomous := authority.PartyOf(e.Actor).Agent() || decision.Status == questions.DecisionAssumed
		h.history.Decisions = append(h.history.Decisions, measures.DecisionFacts{ID: decision.ID, RecordedAt: e.RecordedAt, Autonomous: autonomous})
	}
	if decision.Status == questions.DecisionOverturned {
		h.history.Decisions[at].Overturned = true
	}
}

// approval counts one interruption for each request a person decided: at the web UI or the command
// line, or at a pane. One an orchestrator decided under delegation reached no person.
func (h *historian) approval(e ledger.Event) {
	var approval authority.Approval
	if json.Unmarshal(e.Data, &approval) != nil {
		return
	}
	before := h.approvals[approval.ID]
	h.approvals[approval.ID] = approval.State
	if before == approval.State {
		return
	}
	decider := authority.PartyOf(ledger.Actor{Kind: ledger.ActorKind(approval.DecidedKind)})
	byPerson := decider == authority.PartyPerson && (approval.State == authority.ApprovalApproved || approval.State == authority.ApprovalDenied)
	if byPerson || approval.State == authority.ApprovalAnswered {
		h.interrupted(e, measures.InterruptionApproval, approval.ID)
	}
}

// orchestrator counts a person resetting an orchestrator's breaker, or replacing a run that stalled
// while it was meant to be running. Starting one that was stopped is a choice, and no interruption.
func (h *historian) orchestrator(e ledger.Event) {
	if e.Type != ledger.EventOrchestratorDesired {
		return
	}
	var row ledger.Orchestrator
	if json.Unmarshal(e.Data, &row) != nil {
		return
	}
	before, known := h.orchestrators[e.Subject.ID]
	replaced := known && before.Desired == ledger.OrchestratorRunning && row.Run > before.Run
	if (known && row.Resets > before.Resets) || replaced {
		h.interrupted(e, measures.InterruptionForced, e.ID)
	}
	h.orchestrators[e.Subject.ID] = row
}

// Export emits every window's measures as `ropeshift.measure.*` metrics, so a backend charts the north
// star beside its counterweights (ADR-0004). The measures need no pipeline: this only copies them out.
func (m *Measures) Export(ctx context.Context, scope ledger.WorkspaceID) error {
	if m.telemetry == nil {
		return nil
	}
	return m.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		history, err := HistoryIn(tx)
		if err != nil {
			return err
		}
		for _, window := range MeasureWindows {
			report := measures.Compute(history, measures.Window{Name: window.Name, From: now - window.Length.Milliseconds(), To: now + 1})
			for _, point := range measures.Points(report) {
				attributes := []ports.Attribute{{Key: AttrMeasureWindow, Value: window.Name}}
				for _, key := range slices.Sorted(maps.Keys(point.Labels)) {
					attributes = append(attributes, ports.Attribute{Key: "ropeshift.measure." + key, Value: point.Labels[key]})
				}
				m.telemetry.Measure(ports.Measurement{
					Name: MetricMeasurePrefix + point.Name, Kind: ports.MetricGauge, Unit: point.Unit, Value: point.Value, At: now, Attributes: attributes,
				})
			}
		}
		return nil
	})
}

// The measure metrics' prefix, and the attribute that names a window.
const (
	MetricMeasurePrefix = "ropeshift.measure."
	AttrMeasureWindow   = "ropeshift.measure.window"
)
