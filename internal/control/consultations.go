package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// opConsult is the operation that begins a consultation.
const opConsult = "question.consult"

// consultHoldMs is how long a consultation holds its question. It outlasts the longest session a worker lets a
// consultation run, so a consultation that ends lets the question go itself, and one whose worker went away leaves the
// question held no longer than this (FR-ORCH-9).
const consultHoldMs = 45 * 60 * 1000

// consult begins a consultation of a question this worker's orchestrator could not settle (FR-ORCH-9). The ledger
// chooses the agent, a read-only one of a class above the orchestrator's, sets the budget from `consultation_limit` and
// what the project's daily and weekly limits leave, and holds the question for the consultation until it ends, so
// nobody decides it on half the evidence. A question is consulted on once. Nothing here starts a session: the worker
// does, with what this grants.
func (w *Workers) consult(ctx context.Context, call ledger.OrchestratorCall, actor ledger.Actor) (ledger.ConsultGrant, error) {
	if !authority.Allowed(actor, authority.ActionConsult) {
		return ledger.ConsultGrant{}, workorders.ErrForbidden
	}
	var request ledger.ConsultRequest
	if err := readBody(call, &request); err != nil {
		return ledger.ConsultGrant{}, err
	}
	if err := request.Check(); err != nil {
		return ledger.ConsultGrant{}, fmt.Errorf("%w: %w", workorders.ErrInvalid, err)
	}
	if !ledger.PlainID(call.Target) {
		return ledger.ConsultGrant{}, fmt.Errorf("%w: a consultation needs an ID", workorders.ErrInvalid)
	}
	installation, err := NewSpend(w.store).InstallationSpendPolicyInForce(ctx, w.scope)
	if err != nil {
		return ledger.ConsultGrant{}, err
	}
	var reads consultFacts
	err = w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		question, err := tx.Questions().Question(call.Question)
		if err != nil {
			return err
		}
		if reads.question, err = markedQuestion(tx, question); err != nil {
			return err
		}
		reads.repository, err = consultRepository(tx, question)
		return err
	})
	if err != nil {
		return ledger.ConsultGrant{}, err
	}
	var grant ledger.ConsultGrant
	err = w.store.Write(ctx, w.scope, opConsult, func(tx ports.WriteTx) error {
		var err error
		grant, err = grantConsultation(tx, call, request, installation.OrchestratorClass, reads, actor)
		return err
	})
	return grant, err
}

// consultFacts are what a consultation reads of its question before the transaction that holds it: the question's text
// as an orchestrator reads it, and the repository it names.
type consultFacts struct {
	question   questions.Question
	repository *ledger.ConsultRepository
}

// grantConsultation holds a question for a consultation and records its start, inside one transaction.
func grantConsultation(tx ports.WriteTx, call ledger.OrchestratorCall, request ledger.ConsultRequest, above string, reads consultFacts,
	actor ledger.Actor) (ledger.ConsultGrant, error) {
	question, err := tx.Questions().Question(call.Question)
	if err != nil {
		return ledger.ConsultGrant{}, err
	}
	switch {
	case !question.Status.Unresolved():
		return ledger.ConsultGrant{}, closedToHolder(question)
	case question.Direct():
		return ledger.ConsultGrant{}, refuseDirect(question.ID)
	}
	started, ended, err := consultationOf(tx.EventsAbout, question.ID)
	if err != nil {
		return ledger.ConsultGrant{}, err
	}
	if started != nil {
		next := "decide it from what that consultation found, or escalate it"
		if ended == nil {
			next = "it holds the question until it ends"
		}
		return ledger.ConsultGrant{}, fmt.Errorf("%w: %s was consulted on as %s, so %s", questions.ErrTaken, question.ID,
			ledger.ConsultationCitation(started.ID), next)
	}
	now := tx.Now()
	if question.ClaimedBy != "" && question.ClaimedBy != actor.ID && question.ClaimUntil > now {
		return ledger.ConsultGrant{}, fmt.Errorf("%w: %s holds %s", questions.ErrTaken, question.ClaimedBy, question.ID)
	}
	policy, err := spendPolicyInForce(tx.Policies())
	if err != nil {
		return ledger.ConsultGrant{}, err
	}
	budget, err := consultBudget(projectWritesOf(tx), policy)
	if err != nil {
		return ledger.ConsultGrant{}, err
	}
	view, err := ResolveIn(tx.Roster())
	if err != nil {
		return ledger.ConsultGrant{}, err
	}
	agent, err := view.Resolved.ConsultAgent(request.Agent, above)
	if err != nil {
		return ledger.ConsultGrant{}, fmt.Errorf("%w: %w", workorders.ErrInvalid, err)
	}
	grant := ledger.ConsultGrant{
		ID: call.Target, Question: question.ID, QuestionText: reads.question.Text, Ask: request.Ask, Agent: agent.Agent,
		BudgetUSD: budget, Repository: reads.repository, HoldUntil: now + consultHoldMs,
	}
	question.ClaimedBy, question.ClaimUntil = ledger.ConsultationHolder(grant.ID), grant.HoldUntil
	if err := saveQuestion(tx, question, actor); err != nil {
		return ledger.ConsultGrant{}, err
	}
	data, err := json.Marshal(grant)
	if err != nil {
		return ledger.ConsultGrant{}, err
	}
	_, err = tx.Append(ledger.EventDraft{
		Type: ledger.EventConsultationStarted, Actor: actor, Data: data,
		Subject: ledger.Subject{Kind: ledger.SubjectConsultation, ID: question.ID},
	})
	return grant, err
}

// consultBudget is the most a consultation may spend: `consultation_limit`, and no more than what the project's daily
// and weekly limits leave. A project past either limit starts none, and neither does a limit of 0 (FR-SPEND-6).
func consultBudget(reads spendReads, policy authority.SpendPolicy) (float64, error) {
	if policy.ConsultationLimit <= 0 {
		return 0, fmt.Errorf("%w: consultation_limit is 0, so the project starts no consultation", workorders.ErrIneligible)
	}
	spend, err := reads.projectSpendUnder(policy, false)
	if err != nil {
		return 0, err
	}
	if hold := spend.Hold(); hold != nil {
		return 0, fmt.Errorf("%w: %w", workorders.ErrIneligible, ledger.ProjectLimitErrorOf(spend, *hold))
	}
	budget := policy.ConsultationLimit
	for _, window := range []ledger.ProjectWindowSpend{spend.Day, spend.Week} {
		if window.HardLimit > 0 {
			budget = min(budget, window.Remaining)
		}
	}
	return budget, nil
}

// consultRepository is the repository a consultation reads: the one the question's work order targets, or the one the
// work order of a lane waiting on it targets. A question that names none, or names a repository the project removed,
// gives a consultation the records alone.
func consultRepository(tx ports.ReadTx, question questions.Question) (*ledger.ConsultRepository, error) {
	orders := []string{}
	if question.SubjectKind == workorders.SubjectKind {
		orders = append(orders, question.SubjectID)
	}
	attachments, err := tx.Questions().Attachments(question.ID)
	if err != nil {
		return nil, err
	}
	for _, attachment := range attachments {
		row, err := tx.Lanes().Row(attachment.Lane)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		orders = append(orders, row.WorkOrder)
	}
	for _, id := range orders {
		order, err := tx.WorkOrders().Get(id)
		if errors.Is(err, ports.ErrNotFound) || (err == nil && order.RepositoryID == "") {
			continue
		}
		if err != nil {
			return nil, err
		}
		repository, err := tx.Repositories().Repository(order.RepositoryID)
		if errors.Is(err, ports.ErrNotFound) || (err == nil && repository.Removed()) {
			continue
		}
		if err != nil {
			return nil, err
		}
		base := repository.DefaultBranch
		if base == "" {
			base = repository.Base()
		}
		return &ledger.ConsultRepository{ID: repository.ID, Name: repository.Name, Base: base}, nil
	}
	return nil, nil
}

// startedConsultation is a consultation's start as the ledger recorded it, with the worker that began it.
type startedConsultation struct {
	ledger.ConsultGrant
	worker string
}

// endedConsultation is a consultation's end as the ledger recorded it, with when it did.
type endedConsultation struct {
	ledger.ConsultationEnd
	worker     string
	recordedAt int64
}

// consultationOf reads the consultation of a question, if any: its start, and its end once it ended.
func consultationOf(eventsAbout func(ledger.Subject) ([]ledger.Event, error), question string) (*startedConsultation, *endedConsultation, error) {
	events, err := eventsAbout(ledger.Subject{Kind: ledger.SubjectConsultation, ID: question})
	if err != nil {
		return nil, nil, err
	}
	var started *startedConsultation
	var ended *endedConsultation
	for _, event := range events {
		switch event.Type {
		case ledger.EventConsultationStarted:
			var grant ledger.ConsultGrant
			if err := json.Unmarshal(event.Data, &grant); err != nil {
				return nil, nil, err
			}
			started = &startedConsultation{ConsultGrant: grant, worker: event.Actor.ID}
		case ledger.EventConsulted:
			var end ledger.ConsultationEnd
			if err := json.Unmarshal(event.Data, &end); err != nil {
				return nil, nil, err
			}
			ended = &endedConsultation{ConsultationEnd: end, worker: event.Actor.ID, recordedAt: event.RecordedAt}
		}
	}
	return started, ended, nil
}

// consultationUpload records how a consultation ended, once: the event that holds its agent, class, model, cost, and
// what its agent found, the cost in the project's work tally under the consultation's own name, which the daily and
// weekly limits count, and the end of its hold on the question, so its orchestrator's next wake names both.
func consultationUpload(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	var end ledger.ConsultationEnd
	if err := json.Unmarshal(e.Payload, &end); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	if err := end.Check(); err != nil {
		return fmt.Errorf("%w: %w", workorders.ErrInvalid, err)
	}
	started, ended, err := consultationOf(tx.EventsAbout, end.Question)
	if err != nil {
		return err
	}
	if started == nil || started.ID != end.ID || started.worker != worker {
		return fmt.Errorf("%w: no consultation %s of %s is this worker's", workorders.ErrInvalid, end.ID, end.Question)
	}
	if ended != nil {
		return nil
	}
	end.Agent, end.Class, end.Approval = started.Agent.Name, started.Agent.Class, started.Agent.Approval
	data, err := json.Marshal(end)
	if err != nil {
		return err
	}
	actor := ledger.Actor{Kind: ledger.ActorWorker, ID: worker}
	if _, err := tx.Append(ledger.EventDraft{
		ID: id, Type: ledger.EventConsulted, OccurredAt: e.OccurredAt, Actor: actor, Data: data,
		Subject: ledger.Subject{Kind: ledger.SubjectConsultation, ID: end.Question},
	}); err != nil {
		return err
	}
	usage := ledger.GenerationUsage{CostUSD: end.CostUSD, KnownTurns: 1}
	if err := tx.Lanes().NoteSpend(ledger.SpendScopeWork, ledger.ConsultationCitation(end.ID), 1, usage); err != nil {
		return err
	}
	if err := noteProjectLimits(tx); err != nil {
		return err
	}
	question, err := tx.Questions().Question(end.Question)
	if err != nil || question.ClaimedBy != ledger.ConsultationHolder(end.ID) {
		return err
	}
	question.ClaimedBy = ""
	if !keepsHoldTime() {
		question.ClaimUntil = tx.Now()
	}
	return saveQuestion(tx, question, actor)
}

// wakeConsultations lists each consultation this worker ran that ended on a question still waiting on the ladder, as
// one item of the next wake: what its agent found, and how the decision cites it. Its stamp is when it ended, so it
// counts once, and a question decided, attached, or escalated takes its item with it.
func wakeConsultations(tx ports.ReadTx, worker string) ([]ledger.WakeItem, error) {
	rows, err := freeQuestions(tx, "")
	if err != nil {
		return nil, err
	}
	var out []ledger.WakeItem
	for _, row := range rows {
		_, ended, err := consultationOf(tx.EventsAbout, row.ID)
		if err != nil {
			return nil, err
		}
		if ended == nil || ended.worker != worker {
			continue
		}
		out = append(out, ledger.WakeItem{Kind: ledger.WakeConsultation, ID: ended.ID, Stamp: ended.recordedAt, Text: consultationText(ended.ConsultationEnd)})
	}
	return out, nil
}

// consultationText states what a consultation found for its orchestrator. What its agent wrote stands between the tags
// the role names, because the agent read records a delegate may have written, and its text grants nothing.
func consultationText(end ledger.ConsultationEnd) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Consultation %s of question %s, by %s (%s, approval %s, %s, $%.2f), ended %s.", end.ID, end.Question, end.Agent, end.Class,
		end.Approval, end.Model, end.CostUSD, end.Outcome)
	if end.Outcome == ledger.ConsultationReported {
		for i, option := range end.Options {
			fmt.Fprintf(&out, " Option %d: %s", i+1, lanes.DelegateText(option))
		}
		fmt.Fprintf(&out, " Recommendation: %s Rationale: %s", lanes.DelegateText(end.Recommendation), lanes.DelegateText(end.Rationale))
	} else if end.Detail != "" {
		fmt.Fprintf(&out, " %s", end.Detail)
	}
	fmt.Fprintf(&out, " Take %s and decide it, citing %s, or escalate it with your recommendation.", end.Question, ledger.ConsultationCitation(end.ID))
	return out.String()
}

// withConsultation adds the citation of a question's consultation to the citations of a decision on it that does not cite
// it already, because the decision rests on what the consultation found, and its event holds the agent and the cost
// (FR-ORCH-9). A question no consultation ended on keeps its citations.
func withConsultation(eventsAbout func(ledger.Subject) ([]ledger.Event, error), question string, citations []string) ([]string, error) {
	_, ended, err := consultationOf(eventsAbout, question)
	if err != nil || ended == nil {
		return citations, err
	}
	cited := ledger.ConsultationCitation(ended.ID)
	if slices.Contains(citations, cited) {
		return citations, nil
	}
	return append(slices.Clone(citations), cited), nil
}
