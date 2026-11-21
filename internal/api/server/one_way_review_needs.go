package server

import (
	"errors"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// oneWayReviewNeeds lists, as decisions that wait for a member's review, every one-way decision in force that an
// orchestrator recorded, which it does only on an unattended worker whose one-way acknowledgement a member accepted
// (FR-QD-8). The event that recorded a decision names who recorded it, so a decision whose worker the ledger no longer
// holds still reads as an orchestrator's, and the event's time is how long the review has waited. The events that
// recorded the decisions are read together, once, however many decisions wait. A member's Keep or Overturn ends the
// decision, which ends its review.
func oneWayReviewNeeds(tx ports.ReadTx) ([]api.NeedItem, error) {
	decided, err := allDecisions(tx, ports.RecordFilter{Status: string(questions.DecisionDecided)})
	if err != nil {
		return nil, err
	}
	oneWay := make([]questions.Decision, 0, len(decided))
	for _, decision := range decided {
		if decision.Reversibility == questions.OneWay && decision.DecidedBy != "" {
			oneWay = append(oneWay, decision)
		}
	}
	items := []api.NeedItem{}
	if len(oneWay) == 0 {
		return items, nil
	}
	recorded, err := recordingEvents(tx, oneWay)
	if err != nil {
		return nil, err
	}
	var names map[string]string
	for _, decision := range oneWay {
		event, ok := recorded[decision.ID]
		if !ok || authority.PartyOf(event.Actor) != authority.PartyOrchestrator || event.Actor.ID != decision.DecidedBy {
			continue
		}
		if names == nil {
			if names, err = workerNames(tx); err != nil {
				return nil, err
			}
		}
		need := &api.OneWayReviewNeed{Decision: decision, DecidedByName: names[decision.DecidedBy], Actions: []string{api.ActDecisionKeep, api.ActDecisionOverturn}}
		if need.Question, err = questionText(tx, decision.QuestionID); err != nil {
			return nil, err
		}
		items = append(items, api.NeedItem{Kind: api.NeedOneWayReview, ID: decision.ID, Since: event.RecordedAt, OneWayReview: need})
	}
	return items, nil
}

// recordingEvents reads the event that recorded each of some decisions, by decision, in one read. A decision no event
// recorded has none.
func recordingEvents(tx ports.ReadTx, decisions []questions.Decision) (map[string]ledger.Event, error) {
	ids := make([]string, len(decisions))
	for i, decision := range decisions {
		ids[i] = decision.ID
	}
	events, err := tx.EventsOfTypeAbout(questions.EventDecisionRecorded, questions.SubjectDecision, ids)
	if err != nil {
		return nil, err
	}
	recorded := make(map[string]ledger.Event, len(events))
	for _, event := range events {
		if _, first := recorded[event.Subject.ID]; !first {
			recorded[event.Subject.ID] = event
		}
	}
	return recorded, nil
}

// workerNames names each worker the ledger holds by its ID, because an orchestrator decides as its worker.
func workerNames(tx ports.ReadTx) (map[string]string, error) {
	workers, err := tx.Workers().List()
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(workers))
	for _, worker := range workers {
		names[worker.ID] = worker.Name
	}
	return names, nil
}

// questionText is the text of the question a decision answered, or nothing when it answered none the ledger holds.
func questionText(tx ports.ReadTx, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	question, err := tx.Questions().Question(id)
	if errors.Is(err, ports.ErrNotFound) {
		return "", nil
	}
	return question.Text, err
}
