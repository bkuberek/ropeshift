package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// ladderHold is how long an orchestrator holds a question it took. A holder takes again to keep its
// hold, so this bounds what a session that died leaves held.
const ladderHoldMs = 10 * 60 * 1000

// candidateLimit bounds what one rung's lookup reads.
const candidateLimit = 5

// LadderRead is a question to work and the candidates the core found for it. A score means nothing
// here: the orchestrator judges, and the core stores and searches.
type LadderRead struct {
	Question questions.Question `json:"question"`
	Lookup   []ports.Candidate  `json:"lookup"`
	Open     []ports.Candidate  `json:"open"`
}

// orchestrateActor checks a call came from the run a member asked for, before anything else reads
// it. A worker's credential alone is not enough, because a worker with no orchestrator has no agent
// to act for, and a start that was replaced speaks for nobody.
func (w *Workers) orchestrateActor(ctx context.Context, call ledger.OrchestratorCall) (ledger.Actor, error) {
	actor := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: call.Auth.WorkerID}
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		if err := w.checkSession(tx, call.Auth, call.Epoch); err != nil {
			return err
		}
		row, err := tx.Workers().Orchestrator(call.Auth.WorkerID)
		if errors.Is(err, ports.ErrNotFound) {
			return workorders.ErrForbidden
		}
		if err != nil {
			return err
		}
		if row.Desired != ledger.OrchestratorRunning || row.Lane() != call.Lane {
			return workorders.ErrForbidden
		}
		return nil
	})
	return actor, err
}

// Orchestrate carries one act of this worker's orchestrator through the use case a person's act takes,
// so every rule a person is held to holds here, and the rest are in authority's table.
func (w *Workers) Orchestrate(ctx context.Context, verb string, call ledger.OrchestratorCall) (ledger.OrchestratorReply, error) {
	actor, err := w.orchestrateActor(ctx, call)
	if err != nil {
		return ledger.OrchestratorReply{}, err
	}
	result, err := w.orchestrate(ctx, verb, call, actor)
	if err != nil {
		return ledger.OrchestratorReply{}, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return ledger.OrchestratorReply{}, err
	}
	return ledger.OrchestratorReply{Result: raw}, nil
}

func (w *Workers) orchestrate(ctx context.Context, verb string, call ledger.OrchestratorCall, actor ledger.Actor) (any, error) {
	switch verb {
	case ledger.VerbCandidates:
		return w.candidates(ctx, actor.ID)
	case ledger.VerbClaim:
		return w.Claim(ctx, call.Auth, workorders.ClaimRequest{WorkOrder: call.WorkOrder, ClaimID: call.Target})
	case ledger.VerbDecline:
		return nil, w.decline(ctx, call, actor)
	case ledger.VerbStart:
		if err := w.holdsWork(ctx, actor.ID, call.WorkOrder); err != nil {
			return nil, err
		}
		if err := w.heldToStanding(ctx, actor.ID, authority.AutonomyStanding.MayClaim); err != nil {
			return nil, err
		}
		return w.leases.Dispatch(ctx, w.scope, call.WorkOrder, call.Target, actor)
	case ledger.VerbStop:
		if err := w.holdsWork(ctx, actor.ID, call.WorkOrder); err != nil {
			return nil, err
		}
		if err := w.heldToStanding(ctx, actor.ID, authority.AutonomyStanding.MayWork); err != nil {
			return nil, err
		}
		return nil, w.leases.RespawnLane(ctx, w.scope, call.WorkOrder, actor)
	case ledger.VerbNext:
		return w.nextQuestion(ctx, actor)
	case ledger.VerbTake:
		return w.questions.Take(ctx, w.scope, call.Question, ladderHoldMs, actor)
	case ledger.VerbClimb:
		var step questions.Step
		if err := readBody(call, &step); err != nil {
			return nil, err
		}
		return w.questions.Climb(ctx, w.scope, call.Question, step, actor)
	case ledger.VerbEscalate:
		var escalation questions.Escalation
		if err := readBody(call, &escalation); err != nil {
			return nil, err
		}
		return w.questions.Escalate(ctx, w.scope, call.Question, escalation, actor)
	case ledger.VerbResolve:
		return w.questions.Resolve(ctx, w.scope, call.Question, call.Target, actor)
	case ledger.VerbMerge:
		return nil, w.questions.Merge(ctx, w.scope, call.Question, call.Target, actor)
	case ledger.VerbRule:
		var ruling questions.Ruling
		if err := readBody(call, &ruling); err != nil {
			return nil, err
		}
		return w.questions.Rule(ctx, w.scope, call.Question, ruling, actor)
	case ledger.VerbAccept:
		if err := w.heldToStanding(ctx, actor.ID, authority.AutonomyStanding.MayWork); err != nil {
			return nil, err
		}
		return w.accepting.Accept(ctx, w.scope, call.WorkOrder, actor)
	}
	return nil, fmt.Errorf("%w: no such act: %q", workorders.ErrInvalid, verb)
}

// heldToStanding refuses an act this worker's level does not leave its orchestrator. A start reads
// the same rule as a claim, because the session is what runs with nobody there: work claimed in a
// window that closed waits for the next one, or for a member.
func (w *Workers) heldToStanding(ctx context.Context, worker string, may func(authority.AutonomyStanding) bool) error {
	return w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		facts, err := autonomyOf(tx, worker)
		if err != nil {
			return err
		}
		if !may(facts.standing) {
			return fmt.Errorf("%w: worker %s is %s, with no run window open", workorders.ErrIneligible, worker, facts.standing.Level)
		}
		return nil
	})
}

// candidates is what an orchestrator may take now, which is nothing when its level leaves claims to a
// person, so a session does not spend a turn on work it could only be refused.
func (w *Workers) candidates(ctx context.Context, worker string) ([]workorders.WorkOrder, error) {
	offered, err := w.Offered(ctx, worker)
	if err != nil {
		return nil, err
	}
	err = w.heldToStanding(ctx, worker, authority.AutonomyStanding.MayClaim)
	if errors.Is(err, workorders.ErrIneligible) {
		return []workorders.WorkOrder{}, nil
	}
	return offered, err
}

func readBody(call ledger.OrchestratorCall, into any) error {
	if err := json.Unmarshal(call.Body, into); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	return nil
}

// holdsWork keeps an orchestrator to its own worker's claims: any can work any question, and each
// runs only what its machine holds.
func (w *Workers) holdsWork(ctx context.Context, worker, id string) error {
	return w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		row, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		if row.LeaseWorker != worker {
			return &workorders.FencedError{Current: row.Fence, State: row.State}
		}
		return nil
	})
}

// decline keeps why an orchestrator passed on an item, so a person can read it. It changes nothing
// about the work, which any other worker may still claim.
func (w *Workers) decline(ctx context.Context, call ledger.OrchestratorCall, actor ledger.Actor) error {
	if call.Reason == "" {
		return fmt.Errorf("%w: declining work needs a reason", workorders.ErrInvalid)
	}
	data, err := json.Marshal(map[string]string{"work_order": call.WorkOrder, "reason": call.Reason})
	if err != nil {
		return err
	}
	return w.store.Write(ctx, w.scope, "orchestrator.decline", func(tx ports.WriteTx) error {
		if _, err := tx.WorkOrders().Get(call.WorkOrder); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventOrchestratorDeclined, Actor: actor, Data: data,
			Subject: ledger.Subject{Kind: ledger.SubjectOrchestrator, ID: ledger.OrchestratorOf(actor.ID)},
		})
		return err
	})
}

// nextQuestion takes what comes next: what a lane is blocked on, then the oldest. One another took
// first is skipped, with the compare-and-set that keeps two from working one question. Nothing left
// answers nothing, because a session that reads that ends its turn.
func (w *Workers) nextQuestion(ctx context.Context, actor ledger.Actor) (*LadderRead, error) {
	var free []questions.Question
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		var err error
		free, err = freeQuestions(tx, actor.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(free, func(a, b questions.Question) int {
		switch {
		case a.Blocking && !b.Blocking:
			return -1
		case b.Blocking && !a.Blocking:
			return 1
		}
		return 0
	})
	for _, row := range free {
		taken, err := w.questions.Take(ctx, w.scope, row.ID, ladderHoldMs, actor)
		if errors.Is(err, questions.ErrTaken) || errors.Is(err, questions.ErrResolved) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return w.withCandidates(ctx, taken)
	}
	return nil, nil
}

func (w *Workers) withCandidates(ctx context.Context, question questions.Question) (*LadderRead, error) {
	read := &LadderRead{Question: question, Lookup: []ports.Candidate{}, Open: []ports.Candidate{}}
	if w.options.Matcher == nil {
		return read, nil
	}
	var err error
	read.Lookup, err = w.options.Matcher.Candidates(ctx, w.scope, ports.MatchRequest{
		Text: question.Text, Kinds: []string{questions.KindDecision, questions.KindRequirement, questions.KindContract}, Limit: candidateLimit,
	})
	if err != nil {
		return nil, err
	}
	found, err := w.options.Matcher.Candidates(ctx, w.scope, ports.MatchRequest{Text: question.Text, Kinds: []string{questions.KindQuestion}, Limit: candidateLimit + 1})
	if err != nil {
		return nil, err
	}
	read.Open = slices.DeleteFunc(found, func(candidate ports.Candidate) bool { return candidate.RecordID == question.ID })
	return read, nil
}
