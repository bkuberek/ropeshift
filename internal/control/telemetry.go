package control

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Spans turns what the ledger records into ropeshift's spans and metrics (ADR-0004). It follows the
// event log from a cursor, after each change has committed, so a span describes only what happened,
// and every start it needs is read from the subject's own history, so a restart loses nothing it
// has not yet read. Spans join their work order's trace through IDs derived from what they are
// about, which every plane computes alike. What it emits carries IDs, states, and times, and never a
// question's text, a message's body, or an approval's summary.
type Spans struct {
	store     ports.Store
	telemetry ports.Telemetry
	cursor    int64
}

// NewSpans returns a projection that reads from the start of the log.
func NewSpans(store ports.Store, telemetry ports.Telemetry) *Spans {
	return &Spans{store: store, telemetry: telemetry}
}

// StartAtHead moves the cursor past everything the log holds now, so a daemon that starts emits what
// happens from then on and not its whole history again.
func (s *Spans) StartAtHead(ctx context.Context, scope ledger.WorkspaceID) error {
	return s.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		workspace, err := tx.Workspace()
		if err != nil {
			return err
		}
		s.cursor = workspace.Rev
		return nil
	})
}

// Project emits for every event past the cursor and moves the cursor past them. It returns how many
// events it read.
func (s *Spans) Project(ctx context.Context, scope ledger.WorkspaceID) (int, error) {
	read := 0
	err := s.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		for {
			page, err := tx.Events(s.cursor, eventPage)
			if err != nil {
				return err
			}
			for _, event := range page {
				if err := s.project(tx, event); err != nil {
					return err
				}
				s.cursor = event.Rev
				read++
			}
			if len(page) < eventPage {
				return nil
			}
		}
	})
	return read, err
}

func (s *Spans) project(tx ports.ReadTx, e ledger.Event) error {
	switch e.Subject.Kind {
	case workorders.SubjectKind:
		return s.workOrderEvent(tx, e)
	case ledger.SubjectLane:
		return s.laneEvent(tx, e)
	case authority.SubjectApproval:
		return s.approvalEvent(tx, e)
	case questions.SubjectQuestion:
		return s.questionEvent(tx, e)
	case messages.SubjectMessage:
		return s.messageEvent(tx, e)
	}
	return nil
}

func attr(key string, value any) ports.Attribute { return ports.Attribute{Key: key, Value: value} }

func laneAttrs(workOrder, lane string, generation int64) []ports.Attribute {
	out := []ports.Attribute{attr(lanes.AttrWorkOrder, workOrder)}
	if lane != "" {
		out = append(out, attr(lanes.AttrLane, lane))
	}
	if generation > 0 {
		out = append(out, attr(lanes.AttrGeneration, generation))
	}
	return out
}

// laneEndTriggers end a lane with an outcome.
var laneEndTriggers = []workorders.Trigger{
	workorders.TriggerLaneCompleted, workorders.TriggerLaneBlocked, workorders.TriggerLaneFailedRetry, workorders.TriggerAttemptsExhausted,
}

// terminalTriggers end a work order.
var terminalTriggers = map[workorders.Trigger]workorders.State{
	workorders.TriggerAcceptancePassed: workorders.Done, workorders.TriggerAttemptsExhausted: workorders.Failed,
	workorders.TriggerCanceled: workorders.Canceled,
}

func (s *Spans) workOrderEvent(tx ports.ReadTx, e ledger.Event) error {
	var change workorders.Change
	if e.Type != workorders.EventCreated && json.Unmarshal(e.Data, &change) != nil {
		return nil
	}
	id := e.Subject.ID
	trace := ledger.WorkOrderTrace(id)
	switch {
	case change.Trigger == workorders.TriggerClaimGranted:
		s.telemetry.Span(ports.Span{
			Name: ledger.SpanClaim, Kind: ports.SpanInternal, TraceID: trace, SpanID: ledger.SpanID("claim", change.ClaimID),
			ParentID: ledger.WorkOrderSpan(id), Start: e.RecordedAt, End: e.RecordedAt,
			Attributes: append(laneAttrs(id, "", 0), attr(ledger.AttrClaimResult, "granted"), attr(ledger.AttrFence, change.Fence)),
		})
	case change.Trigger == workorders.TriggerLeaseExpired:
		s.telemetry.Measure(ports.Measurement{Name: ledger.MetricLeaseLapsed, Kind: ports.MetricCounter, Unit: "{lease}", Value: 1, At: e.RecordedAt,
			Attributes: laneAttrs(id, "", 0)})
	case e.Type == workorders.EventLaneRestarted || e.Type == workorders.EventLaneHandoff || e.Type == workorders.EventLaneResumed:
		if err := s.nextGeneration(tx, e, change); err != nil {
			return err
		}
	case change.Trigger == workorders.TriggerLaneParked:
		// The park span ends when its wake fires.
	case change.Trigger == workorders.TriggerWakeFired:
		if err := s.parked(tx, e, change); err != nil {
			return err
		}
	}
	if slices.Contains(laneEndTriggers, change.Trigger) && change.Lane != "" {
		if err := s.laneEnded(tx, id, change.Lane, change.Generation, string(change.Outcome), e.RecordedAt); err != nil {
			return err
		}
	}
	if state, ends := terminalTriggers[change.Trigger]; ends {
		return s.workOrderEnded(tx, id, state, e.RecordedAt)
	}
	return nil
}

// historyOf reads one work order's events, in order, with each one's change.
func historyOf(tx ports.ReadTx, workOrder string) ([]ledger.Event, []workorders.Change, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: workorders.SubjectKind, ID: workOrder})
	if err != nil {
		return nil, nil, err
	}
	changes := make([]workorders.Change, len(events))
	for i, event := range events {
		if event.Type != workorders.EventCreated {
			_ = json.Unmarshal(event.Data, &changes[i])
		}
	}
	return events, changes, nil
}

// generationStart is when a lane's generation began: its lane's start for the first, and the move
// that started it for any other.
func generationStart(events []ledger.Event, changes []workorders.Change, lane string, generation int64) int64 {
	for i, event := range events {
		change := changes[i]
		if change.Lane != lane {
			continue
		}
		starts := change.Trigger == workorders.TriggerLaneStarted && generation == 1 ||
			(event.Type == workorders.EventLaneRestarted || event.Type == workorders.EventLaneHandoff || event.Type == workorders.EventLaneResumed) &&
				change.Generation == generation
		if starts {
			return event.RecordedAt
		}
	}
	return 0
}

func startReasonOf(tx ports.ReadTx, lane string, generation int64) string {
	generations, err := tx.Lanes().Generations(lane)
	if err != nil {
		return ""
	}
	for _, g := range generations {
		if g.Number == generation {
			return string(g.StartReason)
		}
	}
	return ""
}

func (s *Spans) generationSpan(tx ports.ReadTx, workOrder, lane string, generation, start, end int64, endReason string) {
	attributes := append(laneAttrs(workOrder, lane, generation), attr(ledger.AttrStartReason, startReasonOf(tx, lane, generation)))
	if endReason != "" {
		attributes = append(attributes, attr(ledger.AttrEndReason, endReason))
	}
	s.telemetry.Span(ports.Span{
		Name: ledger.SpanGeneration, Kind: ports.SpanInternal, TraceID: ledger.WorkOrderTrace(workOrder),
		SpanID: ledger.GenerationSpan(lane, generation), ParentID: ledger.LaneSpan(lane), Start: start, End: end, Attributes: attributes,
	})
}

// nextGeneration closes the generation a restart, a handoff, or a resume replaced, and counts the new
// one by why it began.
func (s *Spans) nextGeneration(tx ports.ReadTx, e ledger.Event, change workorders.Change) error {
	events, changes, err := historyOf(tx, e.Subject.ID)
	if err != nil {
		return err
	}
	previous := change.Generation - 1
	reason := startReasonOf(tx, change.Lane, change.Generation)
	if start := generationStart(events, changes, change.Lane, previous); start != 0 && previous > 0 {
		s.generationSpan(tx, e.Subject.ID, change.Lane, previous, start, e.RecordedAt, reason)
	}
	s.telemetry.Measure(ports.Measurement{
		Name: ledger.MetricRespawns, Kind: ports.MetricCounter, Unit: "{respawn}", Value: 1, At: e.RecordedAt,
		Attributes: append(laneAttrs(e.Subject.ID, change.Lane, change.Generation), attr(ledger.AttrStartReason, reason)),
	})
	if e.Type == workorders.EventLaneHandoff {
		s.telemetry.Span(ports.Span{
			Name: ledger.SpanHandoff, Kind: ports.SpanInternal, TraceID: ledger.WorkOrderTrace(e.Subject.ID),
			SpanID: ledger.SpanID("handoff", change.Lane, strconv.FormatInt(previous, 10)), ParentID: ledger.GenerationSpan(change.Lane, previous),
			Start: e.RecordedAt, End: e.RecordedAt,
			Attributes: append(laneAttrs(e.Subject.ID, change.Lane, previous), attr(ledger.AttrHandoffResult, "handed_off")),
		})
	}
	return nil
}

func (s *Spans) laneEnded(tx ports.ReadTx, workOrder, lane string, generation int64, outcome string, at int64) error {
	events, changes, err := historyOf(tx, workOrder)
	if err != nil {
		return err
	}
	if start := generationStart(events, changes, lane, generation); start != 0 {
		s.generationSpan(tx, workOrder, lane, generation, start, at, outcome)
	}
	start := generationStart(events, changes, lane, 1)
	if start == 0 {
		start = at
	}
	s.telemetry.Span(ports.Span{
		Name: ledger.SpanLane, Kind: ports.SpanInternal, TraceID: ledger.WorkOrderTrace(workOrder), SpanID: ledger.LaneSpan(lane),
		Links: []ports.SpanLink{{TraceID: ledger.WorkOrderTrace(workOrder), SpanID: ledger.WorkOrderSpan(workOrder)}},
		Start: start, End: at, Failed: outcome == string(workorders.OutcomeFailed),
		Attributes: append(laneAttrs(workOrder, lane, generation), attr(ledger.AttrOutcome, outcome)),
	})
	return nil
}

func (s *Spans) workOrderEnded(tx ports.ReadTx, workOrder string, state workorders.State, at int64) error {
	events, _, err := historyOf(tx, workOrder)
	if err != nil || len(events) == 0 {
		return err
	}
	kind := ""
	if order, err := tx.WorkOrders().Get(workOrder); err == nil {
		kind = string(order.Kind)
	}
	s.telemetry.Span(ports.Span{
		Name: ledger.SpanWorkOrder, Kind: ports.SpanInternal, TraceID: ledger.WorkOrderTrace(workOrder), SpanID: ledger.WorkOrderSpan(workOrder),
		Start: events[0].RecordedAt, End: at, Failed: state == workorders.Failed,
		Attributes: append(laneAttrs(workOrder, "", 0), attr(ledger.AttrWorkOrderState, string(state)), attr(ledger.AttrWorkOrderKind, kind)),
	})
	return nil
}

// parked closes a park at the wake that ended it, from the moment the lane began to park.
func (s *Spans) parked(tx ports.ReadTx, e ledger.Event, change workorders.Change) error {
	events, changes, err := historyOf(tx, e.Subject.ID)
	if err != nil {
		return err
	}
	var start int64
	var lane string
	var generation int64
	for i, event := range events {
		if event.Rev >= e.Rev {
			break
		}
		if event.Type == workorders.EventLaneParking {
			start, lane, generation = event.RecordedAt, changes[i].Lane, changes[i].Generation
		}
	}
	if start == 0 {
		return nil
	}
	s.telemetry.Span(ports.Span{
		Name: ledger.SpanPark, Kind: ports.SpanInternal, TraceID: ledger.WorkOrderTrace(e.Subject.ID),
		SpanID: ledger.SpanID("park", lane, strconv.FormatInt(generation, 10)), ParentID: ledger.LaneSpan(lane), Start: start, End: e.RecordedAt,
		Attributes: append(laneAttrs(e.Subject.ID, lane, generation), attr(ledger.AttrParkTrigger, WakeTrigger(change.Reason))),
	})
	return nil
}

// laneEvent reads a lane's reported status: the time it spent flagged `stalled`, a liveness read that
// came back unknown, and a breaker that tripped. A canceled lane's end is a lane event too.
func (s *Spans) laneEvent(tx ports.ReadTx, e ledger.Event) error {
	switch e.Type {
	case ledger.EventLaneEnded:
		row, err := tx.Lanes().Row(e.Subject.ID)
		if err != nil {
			return nil
		}
		return s.laneEnded(tx, row.WorkOrder, e.Subject.ID, row.Generation, string(workorders.Canceled), e.RecordedAt)
	case ledger.EventLaneStatus:
	default:
		return nil
	}
	var status ledger.LaneStatus
	if json.Unmarshal(e.Data, &status) != nil {
		return nil
	}
	history, err := tx.EventsAbout(e.Subject)
	if err != nil {
		return err
	}
	previous, stalledSince := ledger.LaneStatus{}, int64(0)
	for _, event := range history {
		if event.Rev >= e.Rev {
			break
		}
		if event.Type != ledger.EventLaneStatus {
			continue
		}
		var earlier ledger.LaneStatus
		if json.Unmarshal(event.Data, &earlier) != nil {
			continue
		}
		stalled := slices.Contains(earlier.Conditions, string(lanes.Stalled))
		switch {
		case stalled && stalledSince == 0:
			stalledSince = event.RecordedAt
		case !stalled:
			stalledSince = 0
		}
		previous = earlier
	}
	row, _ := tx.Lanes().Row(e.Subject.ID)
	attributes := laneAttrs(row.WorkOrder, e.Subject.ID, row.Generation)
	if stalledSince != 0 && !slices.Contains(status.Conditions, string(lanes.Stalled)) {
		s.telemetry.Measure(ports.Measurement{
			Name: ledger.MetricStall, Kind: ports.MetricHistogram, Unit: "s", Value: float64(e.RecordedAt-stalledSince) / 1000, At: e.RecordedAt,
			Attributes: attributes,
		})
	}
	if status.Liveness == string(lanes.LivenessUnknown) && previous.Liveness != status.Liveness {
		s.telemetry.Measure(ports.Measurement{Name: ledger.MetricLivenessUnknown, Kind: ports.MetricCounter, Unit: "{read}", Value: 1, At: e.RecordedAt, Attributes: attributes})
	}
	if status.ActualState == string(lanes.Tripped) && previous.ActualState != status.ActualState {
		s.telemetry.Measure(ports.Measurement{Name: ledger.MetricBreakerTrips, Kind: ports.MetricCounter, Unit: "{trip}", Value: 1, At: e.RecordedAt, Attributes: attributes})
	}
	return nil
}

// approvalEvent counts pending approvals and records each wait, by who decided it.
func (s *Spans) approvalEvent(tx ports.ReadTx, e ledger.Event) error {
	var approval authority.Approval
	if json.Unmarshal(e.Data, &approval) != nil {
		return nil
	}
	history, err := tx.EventsAbout(e.Subject)
	if err != nil {
		return err
	}
	var before authority.ApprovalState
	var raised int64
	for _, event := range history {
		if event.Rev >= e.Rev {
			break
		}
		var earlier authority.Approval
		if json.Unmarshal(event.Data, &earlier) == nil {
			before = earlier.State
			if raised == 0 {
				raised = event.RecordedAt
			}
		}
	}
	attributes := append(laneAttrs(approval.WorkOrder, approval.Lane, approval.Generation), attr(ledger.AttrApprovalKind, approval.Kind))
	pending := func(delta float64) {
		s.telemetry.Measure(ports.Measurement{Name: ledger.MetricApprovalPending, Kind: ports.MetricUpDown, Unit: "{request}", Value: delta, At: e.RecordedAt, Attributes: attributes})
	}
	switch {
	case approval.State == authority.ApprovalPending && before != authority.ApprovalPending:
		pending(1)
	case before == authority.ApprovalPending && approval.State != authority.ApprovalPending:
		pending(-1)
		decided := append(slices.Clone(attributes), attr(ledger.AttrApprovalResult, string(approval.State)), attr(ledger.AttrApprovalDecidedBy, approval.DecidedKind))
		s.telemetry.Span(ports.Span{
			Name: ledger.SpanApprovalWait, Kind: ports.SpanInternal, TraceID: ledger.WorkOrderTrace(approval.WorkOrder),
			SpanID: ledger.SpanID("approval", approval.ID), ParentID: ledger.GenerationSpan(approval.Lane, approval.Generation),
			Start: raised, End: e.RecordedAt, Attributes: decided,
		})
		s.telemetry.Measure(ports.Measurement{
			Name: ledger.MetricApprovalWait, Kind: ports.MetricHistogram, Unit: "s", Value: float64(e.RecordedAt-raised) / 1000, At: e.RecordedAt,
			Attributes: decided,
		})
	}
	return nil
}

// questionEvent records a question's climb once it closes, from when it was asked.
func (s *Spans) questionEvent(tx ports.ReadTx, e ledger.Event) error {
	if e.Type != questions.EventQuestionChanged {
		return nil
	}
	var question questions.Question
	if json.Unmarshal(e.Data, &question) != nil || question.Status.Unresolved() {
		return nil
	}
	history, err := tx.EventsAbout(e.Subject)
	if err != nil {
		return err
	}
	asked := e.RecordedAt
	for _, event := range history {
		if event.Rev >= e.Rev {
			break
		}
		var earlier questions.Question
		if event.Type != questions.EventQuestionChanged || json.Unmarshal(event.Data, &earlier) != nil {
			continue
		}
		if !earlier.Status.Unresolved() {
			return nil
		}
		asked = min(asked, event.RecordedAt)
	}
	trace := ledger.TraceID("question", question.ID)
	if question.SubjectKind == workorders.SubjectKind {
		trace = ledger.WorkOrderTrace(question.SubjectID)
	}
	s.telemetry.Span(ports.Span{
		Name: ledger.SpanQuestion, Kind: ports.SpanInternal, TraceID: trace, SpanID: ledger.SpanID("question", question.ID),
		Start: asked, End: e.RecordedAt,
		Attributes: []ports.Attribute{attr(ledger.AttrQuestionRung, question.Rung), attr(ledger.AttrQuestionResult, string(question.Status))},
	})
	return nil
}

// messageEvent records a send, an injection, each message's final state, and how long a reply took
// from the hand-off it answers. Nothing about a message's body leaves the ledger.
func (s *Spans) messageEvent(tx ports.ReadTx, e ledger.Event) error {
	var message messages.Message
	if json.Unmarshal(e.Data, &message) != nil {
		return nil
	}
	history, err := tx.EventsAbout(e.Subject)
	if err != nil {
		return err
	}
	var before messages.State
	var handedOff int64
	for _, event := range history {
		if event.Rev >= e.Rev {
			break
		}
		var earlier messages.Message
		if json.Unmarshal(event.Data, &earlier) == nil {
			before = earlier.State
			if earlier.State == messages.HandedOff && handedOff == 0 {
				handedOff = event.RecordedAt
			}
		}
	}
	if message.State == before {
		return nil
	}
	lane, generation := message.To.Lane, message.To.Generation
	attributes := []ports.Attribute{
		attr(lanes.AttrLane, lane), attr(lanes.AttrGeneration, generation), attr(ledger.AttrMessageID, message.ID),
		attr(ledger.AttrMessageKind, string(message.Kind)), attr(ledger.AttrMessageState, string(message.State)),
	}
	trace := ledger.TraceID("message", message.ID)
	send := ledger.SpanID("message.send", message.ID)
	switch message.State {
	case messages.Queued:
		s.telemetry.Span(ports.Span{Name: ledger.SpanMessageSend, Kind: ports.SpanProducer, TraceID: trace, SpanID: send, Start: e.RecordedAt, End: e.RecordedAt, Attributes: attributes})
	case messages.Admitted:
		s.telemetry.Span(ports.Span{
			Name: ledger.SpanMessageInject, Kind: ports.SpanConsumer, TraceID: trace, SpanID: ledger.SpanID("message.inject", message.ID),
			Links: []ports.SpanLink{{TraceID: trace, SpanID: send}}, Start: max(handedOff, message.CreatedAt), End: e.RecordedAt, Attributes: attributes,
		})
	case messages.Replied, messages.Refused, messages.Expired:
		s.telemetry.Measure(ports.Measurement{Name: ledger.MetricMessageState, Kind: ports.MetricCounter, Unit: "{message}", Value: 1, At: e.RecordedAt, Attributes: attributes})
		if message.State == messages.Replied && handedOff != 0 {
			s.telemetry.Measure(ports.Measurement{
				Name: ledger.MetricReplyLatency, Kind: ports.MetricHistogram, Unit: "s", Value: float64(e.RecordedAt-handedOff) / 1000, At: e.RecordedAt,
				Attributes: attributes,
			})
		}
	}
	return nil
}
