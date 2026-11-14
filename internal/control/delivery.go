package control

import (
	"cmp"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// DeliveryOptions are what a read of delivery knows beyond a work order's own records: when it reads, the
// spans in which the project's work in progress was full, and since when capacity intervals were recorded,
// which is zero when that is not known.
type DeliveryOptions struct {
	Now           int64
	WIPFull       []delivery.Span
	CapacitySince int64
}

// DeliveryOf derives where one work order's time went from the records about it, and, once a `change` or a
// `rework` has ended, its delivery record (ADR-0026). It reads the work order's own events, its lanes'
// approval requests and the blocking questions they attached to, its review work orders, and the capacity
// intervals of the installation's workers, and never the whole log.
func DeliveryOf(tx ports.ReadTx, id string, opts DeliveryOptions) (delivery.Timeline, *delivery.Record, error) {
	order, err := tx.WorkOrders().Get(id)
	if err != nil {
		return delivery.Timeline{}, nil, err
	}
	in, events, err := deliveryInput(tx, id, opts)
	if err != nil {
		return delivery.Timeline{}, nil, err
	}
	reviews, err := tx.WorkOrders().ReviewsOf(id)
	if err != nil {
		return delivery.Timeline{}, nil, err
	}
	facts := delivery.Facts{}
	for _, review := range reviews {
		reviewIn, reviewEvents, err := deliveryInput(tx, review.ID, opts)
		if err != nil {
			return delivery.Timeline{}, nil, err
		}
		timeline, err := delivery.Derive(reviewIn)
		if err != nil {
			return delivery.Timeline{}, nil, err
		}
		span := delivery.ReviewSpan{WorkOrder: review.ID}
		if len(reviewEvents) > 0 {
			span.Opened = reviewEvents[0].RecordedAt
		}
		if n := len(timeline.Segments); timeline.Terminal != "" && n > 0 && timeline.Segments[n-1].Ended != nil {
			span.Closed, span.ClosedBy = timeline.End, timeline.Segments[n-1].Ended.Actor
		}
		in.Reviews = append(in.Reviews, span)
		facts.Reviews = append(facts.Reviews, timeline)
		usage, err := usageOf(tx, reviewEvents)
		if err != nil {
			return delivery.Timeline{}, nil, err
		}
		facts.Usage = addUsage(facts.Usage, usage)
	}
	timeline, err := delivery.Derive(in)
	if err != nil {
		return delivery.Timeline{}, nil, err
	}
	kind := order.Kind
	if timeline.Terminal == "" || (kind != workorders.KindChange && kind != workorders.KindRework) {
		return timeline, nil, nil
	}
	if err := recordFacts(tx, order, events, &facts); err != nil {
		return delivery.Timeline{}, nil, err
	}
	usage, err := usageOf(tx, events)
	if err != nil {
		return delivery.Timeline{}, nil, err
	}
	facts.Usage = addUsage(facts.Usage, usage)
	facts.LaneCapKnown = opts.CapacitySince > 0 && timeline.Start >= opts.CapacitySince
	record, _ := delivery.RecordOf(timeline, facts)
	return timeline, &record, nil
}

// deliveryInput reads what one work order's timeline is derived from, and its own events.
func deliveryInput(tx ports.ReadTx, id string, opts DeliveryOptions) (delivery.Input, []ledger.Event, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: workorders.SubjectKind, ID: id})
	if err != nil {
		return delivery.Input{}, nil, err
	}
	in := delivery.Input{WorkOrder: id, Events: events, Now: opts.Now}
	if in.Waits, err = waitsOf(tx, id); err != nil {
		return delivery.Input{}, nil, err
	}
	var since int64
	if len(events) > 0 {
		since = events[0].RecordedAt
	}
	capped, err := lanesCapped(tx, since)
	if err != nil {
		return delivery.Input{}, nil, err
	}
	in.LaneCapped = delivery.Union(capped, opts.WIPFull)
	return in, events, nil
}

// waitsOf reads the blocking waits of a work order's lanes: each approval request, which holds its lane
// until a principal decides it, and each blocking question a lane attached to, until it was answered or
// dismissed. Who closed it is the actor of the decision or of the event that closed the question.
func waitsOf(tx ports.ReadTx, id string) ([]delivery.Wait, error) {
	approvals, err := tx.Approvals().OfWorkOrder(id)
	if err != nil {
		return nil, err
	}
	var out []delivery.Wait
	for _, a := range approvals {
		if a.RaisedAt == 0 {
			continue
		}
		wait := delivery.Wait{Lane: a.Lane, Record: a.ID, Opened: a.RaisedAt, Closed: a.DecidedAt}
		if a.DecidedAt != 0 {
			// DecidedBy is a principal: the deciding actor's kind and ID, as authority.Principal writes it.
			wait.ClosedBy = ledger.Actor{Kind: ledger.ActorKind(a.DecidedKind), ID: strings.TrimPrefix(a.DecidedBy, a.DecidedKind+":")}
		}
		out = append(out, wait)
	}
	asked, err := tx.Questions().Questions(ports.RecordFilter{Scope: id})
	if err != nil {
		return nil, err
	}
	for _, q := range asked {
		attachments, err := tx.Questions().Attachments(q.ID)
		if err != nil {
			return nil, err
		}
		closed, by, err := questionClosed(tx, q.ID)
		if err != nil {
			return nil, err
		}
		for _, a := range attachments {
			if a.Blocking {
				out = append(out, delivery.Wait{Lane: a.Lane, Record: q.ID, Opened: a.AttachedAt, Closed: closed, ClosedBy: by})
			}
		}
	}
	return out, nil
}

// questionClosed reads when a question was answered or dismissed, and who did it, from its events. A
// question still open has neither.
func questionClosed(tx ports.ReadTx, id string) (int64, ledger.Actor, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: questions.SubjectQuestion, ID: id})
	if err != nil {
		return 0, ledger.Actor{}, err
	}
	for _, e := range events {
		if e.Type != questions.EventQuestionChanged {
			continue
		}
		var record questions.Question
		if json.Unmarshal(e.Data, &record) != nil {
			continue
		}
		if record.Status == questions.StatusAnswered || record.Status == questions.StatusDismissed {
			return e.RecordedAt, e.Actor, nil
		}
	}
	return 0, ledger.Actor{}, nil
}

// lanesCapped is the spans since a time in which every worker of the installation stood at one of its lane
// caps, from their capacity intervals (FR-OBS-14).
func lanesCapped(tx ports.ReadTx, since int64) ([]delivery.Span, error) {
	intervals, err := tx.Workers().CapacityIntervals(since)
	if err != nil {
		return nil, err
	}
	workers, err := tx.Workers().List()
	if err != nil {
		return nil, err
	}
	full := delivery.Full(intervals)
	sets := make([][]delivery.Span, 0, len(workers))
	for _, worker := range workers {
		sets = append(sets, full[worker.ID])
	}
	return delivery.Intersect(sets...), nil
}

// usageOf sums what the lanes of a work order reported, from its events: each lane end's result, the end of
// a lane canceled under it, and, as a lower bound, a holder a person replaced, which never reports.
func usageOf(tx ports.ReadTx, events []ledger.Event) (delivery.Usage, error) {
	var out delivery.Usage
	add := func(payload []byte) {
		var result workorders.LaneResult
		if len(payload) == 0 || json.Unmarshal(payload, &result) != nil {
			out.CostLowerBound = true
			return
		}
		out.InputTokens += result.InputTokens
		out.OutputTokens += result.OutputTokens
		out.CostUSD += result.CostUSD
		out.CostLowerBound = out.CostLowerBound || result.UnknownCostTurns > 0
	}
	var lanes []string
	ended := map[string]bool{}
	for _, e := range events {
		var change workorders.Change
		if json.Unmarshal(e.Data, &change) != nil {
			continue
		}
		if change.Lane != "" && !slices.Contains(lanes, change.Lane) {
			lanes = append(lanes, change.Lane)
		}
		switch {
		case slices.Contains(laneEnds, change.Trigger):
			add(change.Payload)
			ended[change.Lane] = true
		case change.Trigger == workorders.TriggerTakeover:
			out.CostLowerBound = true
		}
	}
	for _, lane := range lanes {
		if ended[lane] {
			continue
		}
		laneEvents, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectLane, ID: lane})
		if err != nil {
			return delivery.Usage{}, err
		}
		for _, e := range laneEvents {
			if e.Type == ledger.EventLaneEnded {
				add(e.Data)
			}
		}
	}
	return out, nil
}

func addUsage(a, b delivery.Usage) delivery.Usage {
	return delivery.Usage{
		InputTokens: a.InputTokens + b.InputTokens, OutputTokens: a.OutputTokens + b.OutputTokens, CostUSD: a.CostUSD + b.CostUSD,
		CostLowerBound: a.CostLowerBound || b.CostLowerBound,
	}
}

// recordFacts reads the keys and the rest of a finished work order's delivery record: the agent its last
// claim pinned and that agent's capability class in the roster now, its repository and that repository's
// merge authority level now, its pinned contract's criteria, milestone, and plan, the model its last
// generation resolved to, and the change size of its last branch.
func recordFacts(tx ports.ReadTx, order workorders.WorkOrder, events []ledger.Event, facts *delivery.Facts) error {
	facts.Kind, facts.Size, facts.Repository = string(order.Kind), cmp.Or(order.Size, measures.SizeUnknown), order.RepositoryID
	var claim string
	for _, e := range events {
		var change workorders.Change
		if json.Unmarshal(e.Data, &change) != nil {
			continue
		}
		switch {
		case change.Trigger == workorders.TriggerClaimGranted:
			claim = change.ClaimID
		case e.Type == workorders.EventGenerationBound:
			var binding workorders.GenerationBinding
			if json.Unmarshal(change.Payload, &binding) == nil && binding.ModelResolved != "" {
				facts.Model = binding.ModelResolved
			}
		}
	}
	if claim != "" {
		pinned, err := tx.Claims().Find(claim)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		facts.Agent = pinned.Agent
	}
	if facts.Agent != "" {
		view, err := ResolveIn(tx.Roster())
		if err != nil {
			return err
		}
		if agent, ok := view.Resolved.Agent(facts.Agent); ok {
			facts.CapabilityClass = agent.Class
		}
	}
	if order.RepositoryID != "" {
		repository, err := tx.Repositories().Repository(order.RepositoryID)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		facts.MergeAuthority = string(cmp.Or(repository.MergeAuthority, ledger.PersonMerges))
	}
	if order.ContractID != "" {
		contract, err := tx.Planning().Contract(order.ContractID, order.ContractVersion)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		facts.Criteria, facts.Milestone, facts.Plan = int64(len(contract.Criteria)), contract.Milestone, contract.PlanID
	}
	artifacts, err := tx.Evidence().Artifacts(order.ID)
	if err != nil {
		return err
	}
	var last *ledger.Artifact
	for i, artifact := range artifacts {
		if artifact.Kind == ledger.ArtifactBranch && (last == nil || artifact.Fence >= last.Fence) {
			last = &artifacts[i]
		}
	}
	if last != nil {
		facts.Change = last.Change
	}
	return nil
}
