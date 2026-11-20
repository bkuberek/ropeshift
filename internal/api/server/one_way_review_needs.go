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
// holds still reads as an orchestrator's, and the event's time is how long the review has waited. A member's Keep or
// Overturn ends the decision, which ends its review.
func oneWayReviewNeeds(tx ports.ReadTx) ([]api.NeedItem, error) {
	decided, err := allDecisions(tx, ports.RecordFilter{Status: string(questions.DecisionDecided)})
	if err != nil {
		return nil, err
	}
	var names map[string]string
	items := []api.NeedItem{}
	for _, decision := range decided {
		if decision.Reversibility != questions.OneWay || decision.DecidedBy == "" {
			continue
		}
		since, ok, err := recordedByOrchestrator(tx, decision)
		if err != nil {
			return nil, err
		}
		if !ok {
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
		items = append(items, api.NeedItem{Kind: api.NeedOneWayReview, ID: decision.ID, Since: since, OneWayReview: need})
	}
	return items, nil
}

// recordedByOrchestrator reads the event that recorded a decision: whether an orchestrator recorded it, and when.
func recordedByOrchestrator(tx ports.ReadTx, decision questions.Decision) (int64, bool, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: questions.SubjectDecision, ID: decision.ID})
	if err != nil {
		return 0, false, err
	}
	for _, event := range events {
		if event.Type == questions.EventDecisionRecorded {
			return event.RecordedAt, authority.PartyOf(event.Actor) == authority.PartyOrchestrator && event.Actor.ID == decision.DecidedBy, nil
		}
	}
	return 0, false, nil
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
