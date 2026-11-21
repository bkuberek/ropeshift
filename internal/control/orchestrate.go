package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ladderHold is how long an orchestrator holds a question it took. A holder takes again to keep its
// hold, a turn's end lets go of what it left unanswered, and so this bounds what a session that died
// leaves held.
const ladderHoldMs = 10 * 60 * 1000

// candidateLimit bounds what one rung's lookup reads.
const candidateLimit = 5

// LadderCandidate is a record that could answer a question.
type LadderCandidate struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
	Text string `json:"text"`
}

// LadderRead is a question to work and the candidates the core found for it. A score means nothing
// here: the orchestrator judges, and the core stores and searches.
type LadderRead struct {
	Question questions.Question `json:"question"`
	Lookup   []LadderCandidate  `json:"lookup"`
	Open     []LadderCandidate  `json:"open"`
	// Yours says what this orchestrator settles of the question, from the decision policy and its worker's autonomy,
	// so a session reads the rule its calls are held to before it makes one.
	Yours string `json:"yours"`
}

// orchestrateActor checks a call came from the run a member asked for, before anything else reads
// it. A worker's credential alone is not enough, because a worker with no orchestrator has no agent
// to act for, and a start that was replaced speaks for nobody. A run a member stopped keeps one act,
// letting go of what its last turn held, because a stop ends that turn.
func (w *Workers) orchestrateActor(ctx context.Context, verb string, call ledger.OrchestratorCall) (ledger.Actor, error) {
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
		if (row.Desired != ledger.OrchestratorRunning && verb != ledger.VerbLetGo) || row.Lane() != call.Lane {
			return workorders.ErrForbidden
		}
		return nil
	})
	return actor, err
}

// Orchestrate carries one act of this worker's orchestrator through the use case a person's act takes,
// so every rule a person is held to holds here, and the rest are in authority's table.
func (w *Workers) Orchestrate(ctx context.Context, verb string, call ledger.OrchestratorCall) (ledger.OrchestratorReply, error) {
	actor, err := w.orchestrateActor(ctx, verb, call)
	if err != nil {
		return ledger.OrchestratorReply{}, err
	}
	result, err := w.orchestrate(ctx, verb, call, actor)
	if err != nil {
		return ledger.OrchestratorReply{}, err
	}
	if result, err = w.markDelegateText(ctx, result); err != nil {
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
	case ledger.VerbLetGo:
		return w.questions.LetGo(ctx, w.scope, actor)
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
		answered, err := w.questions.Resolve(ctx, w.scope, call.Question, call.Target, actor)
		return AnsweredQuestion{Question: answered}, err
	case ledger.VerbMerge:
		return nil, w.questions.Merge(ctx, w.scope, call.Question, call.Target, actor)
	case ledger.VerbRule:
		var ruling questions.Ruling
		if err := readBody(call, &ruling); err != nil {
			return nil, err
		}
		ruled, err := w.questions.Rule(ctx, w.scope, call.Question, ruling, actor)
		return RecordedDecision{Decision: ruled}, err
	case ledger.VerbRestart, ledger.VerbReset, ledger.VerbRetry, ledger.VerbReplan:
		return w.recover(ctx, recoveryActs[verb], call, actor)
	}
	return nil, fmt.Errorf("%w: no such act: %q", workorders.ErrInvalid, verb)
}

// markDelegateText states what a delegate wrote, the assistant included, in a reply to this worker's
// orchestrator, between the tags its role names, as its wake does: the title of work it may claim, the text
// of a question it takes or settles, and each record found that could answer one. An orchestrator holds
// tools such as approval_decide and lane_start, and none of that text is its instruction.
func (w *Workers) markDelegateText(ctx context.Context, result any) (any, error) {
	var out any
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		var err error
		out, err = markedReply(tx, result)
		return err
	})
	return out, err
}

func markedReply(tx ports.ReadTx, result any) (any, error) {
	switch reply := result.(type) {
	case []workorders.WorkOrder:
		out := make([]workorders.WorkOrder, len(reply))
		for i, order := range reply {
			title, err := wakeTitle(tx, order)
			if err != nil {
				return nil, err
			}
			order.Title = title
			out[i] = order
		}
		return out, nil
	case *LadderRead:
		if reply == nil {
			return reply, nil
		}
		read := *reply
		var err error
		if read.Question, err = markedQuestion(tx, read.Question); err != nil {
			return nil, err
		}
		if read.Lookup, err = markedCandidates(tx, read.Lookup); err != nil {
			return nil, err
		}
		if read.Open, err = markedCandidates(tx, read.Open); err != nil {
			return nil, err
		}
		return &read, nil
	case questions.Question:
		return markedQuestion(tx, reply)
	case AnsweredQuestion:
		question, err := markedQuestion(tx, reply.Question)
		if err != nil {
			return nil, err
		}
		reply.Question, reply.Scope = question, questions.ScopeOf(question.SubjectKind, question.SubjectID)
		reply.ScopeTitle, err = replyTitle(tx, reply.Scope)
		return reply, err
	case RecordedDecision:
		var err error
		reply.ScopeTitle, err = replyTitle(tx, reply.Scope)
		return reply, err
	}
	return result, nil
}

// AnsweredQuestion is the question decision_record answers with a decision the record holds, with the scope an
// answer to it takes (questions.ScopeOf) and the title of the record that scope names.
type AnsweredQuestion struct {
	questions.Question
	Scope      string `json:"scope,omitempty"`
	ScopeTitle string `json:"scope_title,omitempty"`
}

// RecordedDecision is the decision decision_record records, with the title of the record its scope names.
type RecordedDecision struct {
	questions.Decision
	ScopeTitle string `json:"scope_title,omitempty"`
}

// replyTitle is the title of the record a scope names, as a reply to an orchestrator states it field by field: a
// title a delegate wrote between the tags a wake puts around it.
func replyTitle(tx ports.ReadTx, scope string) (string, error) {
	titles, err := scopeTitler{tx: tx, orchestrator: true}.titles([]scopedRecord{{scope: scope}})
	if err != nil {
		return "", err
	}
	return titles[0], nil
}

func markedQuestion(tx ports.ReadTx, question questions.Question) (questions.Question, error) {
	by, err := recordByDelegate(tx, questions.KindQuestion, question.ID)
	if by {
		question.Text = lanes.DelegateText(question.Text)
	}
	return question, err
}

func markedCandidates(tx ports.ReadTx, found []LadderCandidate) ([]LadderCandidate, error) {
	out := make([]LadderCandidate, len(found))
	for i, candidate := range found {
		by, err := recordByDelegate(tx, candidate.Kind, candidate.Ref)
		if err != nil {
			return nil, err
		}
		if by {
			candidate.Text = lanes.DelegateText(candidate.Text)
		}
		out[i] = candidate
	}
	return out, nil
}

// recordByDelegate says a delegate made a record an orchestrator reads by its kind and ID: a question, a
// decision, or a contract, whose text is its maker's. The other kinds come from a repository's files.
func recordByDelegate(tx ports.ReadTx, kind, id string) (bool, error) {
	switch kind {
	case questions.KindQuestion:
		return madeByDelegate(tx, ledger.Subject{Kind: questions.SubjectQuestion, ID: id}, questions.EventQuestionChanged)
	case questions.KindDecision:
		return madeByDelegate(tx, ledger.Subject{Kind: questions.SubjectDecision, ID: id}, questions.EventDecisionRecorded)
	case questions.KindContract:
		return madeByDelegate(tx, ledger.Subject{Kind: planning.SubjectContract, ID: id}, planning.EventContractVersion)
	}
	return false, nil
}

// recoveryActs are the recovery act each verb takes.
var recoveryActs = map[string]authority.RecoveryAct{
	ledger.VerbRestart: authority.RecoverRestart, ledger.VerbReset: authority.RecoverReset,
	ledger.VerbRetry: authority.RecoverRetry, ledger.VerbReplan: authority.RecoverReplan,
}

// recover takes one recovery act for this worker's orchestrator, which is held to its level as a stop is,
// and to the recovery policy in force. The record names the generation its session is in.
func (w *Workers) recover(ctx context.Context, act authority.RecoveryAct, call ledger.OrchestratorCall, actor ledger.Actor) (RecoveryResult, error) {
	if !reclaimsNothing(act) {
		if err := w.heldToStanding(ctx, actor.ID, authority.AutonomyStanding.MayWork); err != nil {
			return RecoveryResult{}, err
		}
	}
	var generation int64
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		row, err := tx.Workers().Orchestrator(actor.ID)
		generation = row.Generation
		return err
	})
	if err != nil {
		return RecoveryResult{}, err
	}
	return w.recoveries.Recover(ctx, w.scope, RecoveryCall{Act: act, WorkOrder: call.WorkOrder, Reason: call.Reason}, actor, generation)
}

// reclaimsNothing says a recovery act starts no lane: a retry returns failed work to ready, and a replan sends blocked
// work back to planning, where a claim or a member takes it next. Either is open to an orchestrator at every level,
// and a restart or a reset, which runs a lane's next generation, is held to the level (ADR-0014).
func reclaimsNothing(act authority.RecoveryAct) bool {
	return act == authority.RecoverRetry || act == authority.RecoverReplan
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
		read, err := w.withCandidates(ctx, taken)
		if err != nil {
			return nil, err
		}
		read.Yours, err = w.yours(ctx, taken, actor)
		return read, err
	}
	return nil, nil
}

// yours says what an orchestrator settles of a question it took, by the rule its calls are held to: the decision
// policy keeps whole categories for a member, and an unattended worker's orchestrator, once a member accepted its
// one-way acknowledgement, may decide every other choice that names a category, a one-way one too, or take it to a
// member (FR-QD-8).
func (w *Workers) yours(ctx context.Context, question questions.Question, actor ledger.Actor) (string, error) {
	by, err := readParty(ctx, w.store, w.scope, actor)
	if err != nil {
		return "", err
	}
	var policy questions.Policy
	var unattended bool
	err = w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		var err error
		if policy, err = readPolicy(tx); err != nil {
			return err
		}
		now, err := tx.Now()
		if err != nil {
			return err
		}
		unattended, err = standsUnattended(tx.Policies(), tx.Workers(), actor, now)
		return err
	})
	if err != nil {
		return "", err
	}
	switch {
	case !policy.MayDecide(question):
		return fmt.Sprintf("The decision policy keeps %v for a member. Answer it from a decision on record, attach it to an open "+
			"question that asks the same, or escalate it with your recommendation.", keptCategories(policy, question)), nil
	case unattended && !by.oneWayAccepted:
		return "This worker is unattended, but no member has accepted its one-way acknowledgement, so a one-way choice still " +
			"goes to a member. Decide or assume a choice that can be undone, and record why. Escalate a one-way choice, a conflict " +
			"between requirements, or a change of scope.", nil
	case unattended && len(question.Categories) == 0:
		return "This worker is unattended, but this question names no category, so a one-way choice in it still goes to a " +
			"member. Decide or assume a choice that can be undone, and record why. Escalate a one-way choice with the categories " +
			"it touches, a conflict between requirements, or a change of scope.", nil
	case unattended:
		return "This worker is unattended, so a member left this question to you. Decide it from the goals, requirements, and " +
			"contracts, or assume a choice that can be undone, and record why. A one-way choice is yours too: record it decided, with " +
			"reversibility one_way, or escalate it when a member should make it. Escalate a conflict between requirements or a change of scope.", nil
	}
	return "A member attends this worker. Decide or assume a choice that can be undone, and record why. Escalate a one-way " +
		"choice, a conflict between requirements, or a change of scope.", nil
}

// keptCategories lists the categories of a question the decision policy keeps for a member.
func keptCategories(policy questions.Policy, question questions.Question) []string {
	var out []string
	for _, category := range question.Categories {
		if !policy.Open([]string{category}) {
			out = append(out, category)
		}
	}
	return out
}

func (w *Workers) withCandidates(ctx context.Context, question questions.Question) (*LadderRead, error) {
	read := &LadderRead{Question: question, Lookup: []LadderCandidate{}, Open: []LadderCandidate{}}
	if w.options.Matcher == nil {
		return read, nil
	}
	lookup, err := w.options.Matcher.Candidates(ctx, w.scope, ports.MatchRequest{
		Text: question.Text, Kinds: questions.LookupKinds(), Limit: candidateLimit,
	})
	if err != nil {
		return nil, err
	}
	found, err := w.options.Matcher.Candidates(ctx, w.scope, ports.MatchRequest{Text: question.Text, Kinds: []string{questions.KindQuestion}, Limit: candidateLimit + 1})
	if err != nil {
		return nil, err
	}
	open := slices.DeleteFunc(found, func(candidate ports.Candidate) bool { return candidate.RecordID == question.ID })
	err = w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		var err error
		if read.Lookup, err = readCandidates(tx, lookup); err != nil {
			return err
		}
		read.Open, err = readCandidates(tx, open)
		return err
	})
	if err != nil {
		return nil, err
	}
	return read, nil
}

// readCandidates states what each record says, under the name a citation and the read tools take. A
// search knows a row's ID, which names nothing a session can read, and a session answers from what it
// reads.
func readCandidates(tx ports.ReadTx, found []ports.Candidate) ([]LadderCandidate, error) {
	out := make([]LadderCandidate, 0, len(found))
	for _, candidate := range found {
		read, err := readCandidate(tx, candidate)
		if err != nil {
			return nil, err
		}
		out = append(out, read)
	}
	return out, nil
}

func readCandidate(tx ports.ReadTx, candidate ports.Candidate) (LadderCandidate, error) {
	read := LadderCandidate{Kind: candidate.Kind, Ref: candidate.RecordID}
	switch candidate.Kind {
	case questions.KindDecision:
		row, err := tx.Questions().Decision(candidate.RecordID)
		if err != nil {
			return read, err
		}
		read.Text = decisionSource(row).Text
	case questions.KindRequirement:
		return readRequirement(tx, read)
	case questions.KindContract:
		row, err := tx.Planning().LatestContract(candidate.RecordID)
		if err != nil {
			return read, err
		}
		read.Text = contractSource(row).Text
	case questions.KindDecisionDocument:
		row, err := tx.Planning().LatestDocumentVersion(candidate.RecordID)
		if err != nil {
			return read, err
		}
		read.Text = row.Title + "\n\n" + row.Body
	case questions.KindQuestion:
		row, err := tx.Questions().Question(candidate.RecordID)
		if err != nil {
			return read, err
		}
		read.Text = row.Text
	}
	return read, nil
}

// readRequirement finds a requirement by the ID search holds, at its newest version. A session reads
// one by its key, so that is the name it gets.
func readRequirement(tx ports.ReadTx, read LadderCandidate) (LadderCandidate, error) {
	versions, err := tx.Planning().Requirements()
	if err != nil {
		return read, err
	}
	at := -1
	for i, version := range versions {
		if version.ID == read.Ref {
			at = i
		}
	}
	if at < 0 {
		return read, fmt.Errorf("requirement %s: %w", read.Ref, ports.ErrNotFound)
	}
	read.Ref, read.Text = versions[at].Key, versions[at].Text
	return read, nil
}
