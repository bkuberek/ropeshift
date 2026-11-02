//go:build chaos

package chaos

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/claudecode"
	"ropeshift.dev/ropeshift/internal/adapters/gitcli"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/messages"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
)

// variant is one deliberately broken build of an invariant. Sabotage changes the
// adapter, and Ops replaces a use case. Neither exists in a normal build.
type variant struct {
	name string
	// sabotage breaks the adapter for this process.
	sabotage sqlstore.Sabotage
	// control breaks parking and waking for this process.
	control control.Sabotage
	// gitSabotage breaks the git adapter for this process.
	gitSabotage gitcli.Sabotage
	// ops replaces the use cases.
	ops func(ports.Store) contract.Ops
	// wrap decorates every Store handle.
	wrap func(ports.Store) ports.Store
	// check is a contract check ID, "crash" for the crash case below, "random"
	// for a run of random kills, "paused" for the stopped writer, "holder" for a run
	// that stops a holder on purpose, or "shim" for a worker reading a shim.
	check string
	// crash is the case and step a crash variant runs.
	crash func() (crashCase, string)
	// pane names a broken driver for a pane run, and paneStep the step it crashes at.
	pane     string
	paneStep string
}

// replace returns a Rewrite that swaps one piece of one statement. It panics when
// the text is gone, so a variant cannot quietly become a no-op.
func replace(statement, from, to string) func(name, text string) string {
	return func(name, text string) string {
		if name != statement {
			return text
		}
		if !strings.Contains(text, from) {
			panic(fmt.Sprintf("variant: %s no longer holds %q", statement, from))
		}
		return strings.Replace(text, from, to, 1)
	}
}

// chain applies several rewrites in order.
func chain(rewrites ...func(name, text string) string) func(name, text string) string {
	return func(name, text string) string {
		for _, rewrite := range rewrites {
			text = rewrite(name, text)
		}
		return text
	}
}

// noteEvent keeps a split transaction's revisions gapless, so that only a crash
// between the two halves can expose it.
func noteEvent(actor ledger.Actor) ledger.EventDraft {
	return ledger.EventDraft{Type: "note.added", Actor: actor, Subject: ledger.Subject{Kind: "note", ID: "split"}}
}

// splitCreate records a work order in one transaction and its event in another.
type splitCreate struct {
	contract.Ops
	store ports.Store
}

func (s splitCreate) Create(ctx context.Context, scope ledger.WorkspaceID, draft workorders.WorkOrder, actor ledger.Actor) (workorders.WorkOrder, error) {
	record, err := workorders.New(draft)
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	err = s.store.Write(ctx, scope, "work_order.create", func(tx ports.WriteTx) error {
		if _, err := tx.WorkOrders().Get(record.ID); err == nil {
			return nil
		}
		if err := tx.WorkOrders().Insert(record); err != nil {
			return err
		}
		_, err := tx.Append(noteEvent(actor))
		return err
	})
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	crashpoint.Hit("work_order.create", "split")
	return record, s.store.Write(ctx, scope, "work_order.create", func(tx ports.WriteTx) error {
		event, err := workorders.CreatedEvent(record, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}

// splitClaim moves the fence in one transaction and records the grant in another.
type splitClaim struct {
	contract.Ops
	store ports.Store
}

func (s splitClaim) Claim(ctx context.Context, scope ledger.WorkspaceID, request workorders.ClaimRequest, actor ledger.Actor) (workorders.Grant, error) {
	var current workorders.WorkOrder
	err := s.store.Write(ctx, scope, "claim", func(tx ports.WriteTx) error {
		won, err := tx.Leases().Grant(request)
		if err != nil {
			return err
		}
		if !won {
			return workorders.ErrLost
		}
		if current, err = tx.WorkOrders().Get(request.WorkOrder); err != nil {
			return err
		}
		_, err = tx.Append(noteEvent(actor))
		return err
	})
	if err != nil {
		return workorders.Grant{}, err
	}
	crashpoint.Hit("claim", "split")
	grant := workorders.Grant{Lease: workorders.Lease{WorkOrder: request.WorkOrder, ClaimID: request.ClaimID, Fence: current.Fence}}
	err = s.store.Write(ctx, scope, "claim", func(tx ports.WriteTx) error {
		err := tx.Claims().Record(workorders.Claim{
			ID: request.ClaimID, WorkOrder: request.WorkOrder, Worker: request.Worker,
			Fence: current.Fence, GrantedAt: tx.Now(),
		})
		if err != nil {
			return err
		}
		event, err := workorders.ChangeEvent(grantEventType(), request.WorkOrder, workorders.Change{
			Trigger: workorders.TriggerClaimGranted, Fence: current.Fence, ClaimID: request.ClaimID,
			Worker: request.Worker, LeaseUntil: current.LeaseUntil,
		}, actor)
		if err != nil {
			return err
		}
		appended, err := tx.Append(event)
		grant.Rev = appended.Rev
		return err
	})
	return grant, err
}

// splitIntake records a contract version in one transaction and its work orders in
// another, so a kill between them leaves a digest that says the file is done.
type splitIntake struct {
	contract.Ops
	store ports.Store
}

func (s splitIntake) Poll(ctx context.Context, scope ledger.WorkspaceID, source ports.WorkSource) (control.IntakeReport, error) {
	batch, err := source.Poll(ctx, "")
	if err != nil {
		return control.IntakeReport{}, err
	}
	item := batch.Items[0]
	version := planning.Contract{
		ID: item.Key.NativeID, Version: 1, PlanID: "plan", Title: item.Contract.Title, Digest: item.Digest,
		Criteria: item.Contract.Criteria, Status: planning.StatusActive,
	}
	recorded := false
	err = s.store.Write(ctx, scope, "intake.item", func(tx ports.WriteTx) error {
		if _, err := tx.Planning().Contract(version.ID, 1); err == nil {
			recorded = true
			return nil
		}
		if err := tx.Planning().InsertContract(version); err != nil {
			return err
		}
		event, err := planning.RecordEvent(planning.EventContractVersion, planning.SubjectContract, version.ID, version, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil || recorded {
		return control.IntakeReport{}, err
	}
	crashpoint.Hit("intake.item", "split")
	return control.IntakeReport{}, s.store.Write(ctx, scope, "intake.item", func(tx ports.WriteTx) error {
		order, err := workorders.New(workorders.WorkOrder{ID: item.Contract.WorkOrders[0].ID, Kind: workorders.KindChange})
		if err != nil {
			return err
		}
		if err := tx.WorkOrders().Insert(order); err != nil {
			return err
		}
		event, err := workorders.CreatedEvent(order, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}

// splitRepin records a contract's new version in one transaction and pins its work in
// another, so a kill between them leaves a digest that says the file is done and ready work
// on the last version.
type splitRepin struct {
	contract.Ops
	store ports.Store
}

func (s splitRepin) Poll(ctx context.Context, scope ledger.WorkspaceID, source ports.WorkSource) (control.IntakeReport, error) {
	batch, err := source.Poll(ctx, "")
	if err != nil {
		return control.IntakeReport{}, err
	}
	item := batch.Items[0]
	var order workorders.WorkOrder
	recorded := false
	err = s.store.Write(ctx, scope, "intake.item", func(tx ports.WriteTx) error {
		link, err := tx.Planning().Link(planning.SubjectContract, item.Key)
		if err != nil {
			return err
		}
		latest, err := tx.Planning().LatestContract(link.RecordID)
		if err != nil {
			return err
		}
		if latest.Digest == item.Digest {
			recorded = true
			return nil
		}
		rows, err := tx.WorkOrders().ByContract(latest.ID)
		if err != nil {
			return err
		}
		order = rows[0]
		next := latest
		next.Version, next.Digest = latest.Version+1, item.Digest
		if err := tx.Planning().InsertContract(next); err != nil {
			return err
		}
		event, err := planning.RecordEvent(planning.EventContractVersion, planning.SubjectContract, next.ID, next, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil || recorded {
		return control.IntakeReport{}, err
	}
	crashpoint.Hit("intake.item", "split")
	return control.IntakeReport{}, s.store.Write(ctx, scope, "intake.item", func(tx ports.WriteTx) error {
		if err := tx.WorkOrders().Pin(order.ID, order.ContractVersion+1); err != nil {
			return err
		}
		event, err := workorders.PinEvent(order.ID, order.ContractVersion+1, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
		if err != nil {
			return err
		}
		if _, err := tx.Append(event); err != nil {
			return err
		}
		return tx.WorkOrders().Reviewed(order.ID, "")
	})
}

// pinsStarted moves a pin the way a poll must not: under a lane that already started.
type pinsStarted struct {
	contract.Ops
	store ports.Store
}

func (p pinsStarted) Poll(ctx context.Context, scope ledger.WorkspaceID, source ports.WorkSource) (control.IntakeReport, error) {
	report, err := p.Ops.Poll(ctx, scope, source)
	if err != nil || report.Versions == 0 {
		return report, err
	}
	return report, p.store.Write(ctx, scope, "intake.item", func(tx ports.WriteTx) error {
		rows, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.State != workorders.Running {
				continue
			}
			if err := tx.WorkOrders().Pin(row.ID, row.ContractVersion+1); err != nil {
				return err
			}
			event, err := workorders.PinEvent(row.ID, row.ContractVersion+1, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
			if err != nil {
				return err
			}
			if _, err := tx.Append(event); err != nil {
				return err
			}
		}
		return nil
	})
}

// splitIndex records a question in one transaction and its search text in another, so a
// kill between them leaves a question no search can find.
type splitIndex struct {
	contract.Ops
	store ports.Store
}

func (s splitIndex) RecordQuestion(ctx context.Context, scope ledger.WorkspaceID, draft questions.Question, actor ledger.Actor) (questions.Question, error) {
	record, err := questions.New(draft)
	if err != nil {
		return questions.Question{}, err
	}
	recorded := false
	err = s.store.Write(ctx, scope, "question.record", func(tx ports.WriteTx) error {
		if _, err := tx.Questions().Question(record.ID); err == nil {
			recorded = true
			return nil
		}
		if err := tx.Questions().Insert(record); err != nil {
			return err
		}
		event, err := questions.RecordEvent(questions.EventQuestionChanged, questions.SubjectQuestion, record.ID, record, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil || recorded {
		return record, err
	}
	crashpoint.Hit("question.record", "split")
	return record, s.store.Write(ctx, scope, "question.record", func(tx ports.WriteTx) error {
		if err := tx.Index(questions.KindQuestion, record.ID, record.Text); err != nil {
			return err
		}
		_, err := tx.Append(noteEvent(actor))
		return err
	})
}

// splitAsk records a question in one transaction and attaches its lane in another, so a
// kill between them leaves a lane that asked and waits on nothing.
type splitAsk struct {
	contract.Ops
	store ports.Store
}

func (s splitAsk) Ask(ctx context.Context, scope ledger.WorkspaceID, ask questions.Ask, actor ledger.Actor) (questions.AskResult, error) {
	record, err := questions.New(questions.Question{ID: ask.ID, Text: ask.Text, Blocking: ask.Blocking, SubjectKind: ask.SubjectKind, SubjectID: ask.SubjectID})
	if err != nil {
		return questions.AskResult{}, err
	}
	recorded := false
	err = s.store.Write(ctx, scope, "question.ask", func(tx ports.WriteTx) error {
		if _, err := tx.Questions().Question(record.ID); err == nil {
			recorded = true
			return nil
		}
		if err := tx.Questions().Insert(record); err != nil {
			return err
		}
		if err := tx.Index(questions.KindQuestion, record.ID, record.Text); err != nil {
			return err
		}
		event, err := questions.RecordEvent(questions.EventQuestionChanged, questions.SubjectQuestion, record.ID, record, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil || recorded {
		return questions.AskResult{Status: questions.AskPending, QuestionID: record.ID}, err
	}
	crashpoint.Hit("question.ask", "split")
	return questions.AskResult{Status: questions.AskPending, QuestionID: record.ID}, s.store.Write(ctx, scope, "question.ask", func(tx ports.WriteTx) error {
		attachment := questions.Attachment{QuestionID: record.ID, Lane: ask.Lane, Generation: ask.Generation, Blocking: ask.Blocking, AttachedAt: tx.Now()}
		if _, err := tx.Questions().Attach(attachment); err != nil {
			return err
		}
		event, err := questions.RecordEvent(questions.EventQuestionAttached, questions.SubjectQuestion, record.ID, attachment, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}

// splitSignIn issues a session in one transaction and records it in another, so a kill
// between them leaves a token that signs in with no record.
type splitSignIn struct {
	contract.Ops
	store ports.Store
}

func (s splitSignIn) SignIn(ctx context.Context, scope ledger.WorkspaceID, _ ports.Proof, _ int64) (control.SessionGrant, error) {
	err := s.store.Write(ctx, scope, "member.sign_in", func(tx ports.WriteTx) error {
		if err := tx.Members().InsertSession(authority.Session{Digest: "split", Member: "member-1", ExpiresAt: tx.Now() + longTTL}); err != nil {
			return err
		}
		_, err := tx.Append(noteEvent(ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}))
		return err
	})
	if err != nil {
		return control.SessionGrant{}, err
	}
	crashpoint.Hit("member.sign_in", "split")
	return control.SessionGrant{}, s.store.Write(ctx, scope, "member.sign_in", func(tx ports.WriteTx) error {
		_, err := tx.Append(ledger.EventDraft{
			Type: control.EventSignedIn, Actor: ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"},
			Subject: ledger.Subject{Kind: "member", ID: "member-1"},
		})
		return err
	})
}

// splitAnswer closes a question in one transaction and records its decision in another,
// so a kill between them leaves an answer nobody can read.
type splitAnswer struct {
	contract.Ops
	store ports.Store
}

func (s splitAnswer) Answer(ctx context.Context, scope ledger.WorkspaceID, id string, answer questions.Answer, actor ledger.Actor) (questions.Decision, error) {
	err := s.store.Write(ctx, scope, "question.answer", func(tx ports.WriteTx) error {
		record, err := tx.Questions().Question(id)
		if err != nil {
			return err
		}
		record.Status, record.DecisionID = questions.StatusAnswered, answer.DecisionID
		if err := tx.Questions().Save(record); err != nil {
			return err
		}
		event, err := questions.RecordEvent(questions.EventQuestionChanged, questions.SubjectQuestion, id, record, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		return questions.Decision{}, err
	}
	crashpoint.Hit("question.answer", "split")
	return s.Ops.RecordDecision(ctx, scope, questions.Decision{
		ID: answer.DecisionID, Status: questions.DecisionDecided, Statement: answer.Statement, QuestionID: id,
	}, actor)
}

// splitTell closes a question in one transaction and tells its lanes in another, so a kill between
// them leaves an answer that no lane will ever hear. A retry finds the question closed.
type splitTell struct {
	contract.Ops
	store ports.Store
}

func (s splitTell) Answer(ctx context.Context, scope ledger.WorkspaceID, id string, answer questions.Answer, actor ledger.Actor) (questions.Decision, error) {
	control.Sabotaged(control.Sabotage{LeavesAnswers: true})
	var closed bool
	err := s.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		record, err := tx.Questions().Question(id)
		closed = err == nil && record.Status == questions.StatusAnswered
		return err
	})
	if err != nil {
		return questions.Decision{}, err
	}
	decided, err := s.Ops.Answer(ctx, scope, id, answer, actor)
	if err != nil || closed {
		return decided, err
	}
	crashpoint.Hit("question.answer", "split")
	control.Sabotaged(control.Sabotage{})
	return decided, s.tell(ctx, scope, id, actor)
}

func (s splitTell) tell(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) error {
	var attached []questions.Attachment
	err := s.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		attached, err = tx.Questions().Attachments(id)
		return err
	})
	if err != nil {
		return err
	}
	for _, attachment := range attached {
		draft := messages.Draft{
			ID: messages.DerivedID(string(messages.KindAnswer), id, attachment.Lane), To: messages.Target{Lane: attachment.Lane},
			ReplyTo: id, Kind: messages.KindAnswer, Body: "The answer.",
		}
		if _, err := s.Ops.Send(ctx, scope, draft, actor); err != nil {
			return err
		}
	}
	return nil
}

// splitAccept moves accepted work in one transaction and records who accepted it in another,
// so a kill between them leaves work `done` with nobody standing behind it.
type splitAccept struct {
	contract.Ops
	store ports.Store
}

func (s splitAccept) Accept(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (workorders.State, error) {
	err := s.store.Write(ctx, scope, "acceptance.accept", func(tx ports.WriteTx) error {
		won, err := tx.WorkOrders().Move(id, workorders.Acceptance, workorders.Done)
		if err != nil || !won {
			return err
		}
		_, err = tx.Append(noteEvent(actor))
		return err
	})
	if err != nil {
		return "", err
	}
	crashpoint.Hit("acceptance.accept", "split")
	err = s.store.Write(ctx, scope, "acceptance.accept", func(tx ports.WriteTx) error {
		event, err := workorders.ChangeEvent(workorders.EventType(workorders.TriggerAcceptancePassed), id,
			workorders.Change{Trigger: workorders.TriggerAcceptancePassed}, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	return workorders.Done, err
}

// splitEvidence ends a lane in one transaction and keeps what it left in another, so a kill
// between them leaves an outcome with no evidence behind it.
type splitEvidence struct {
	contract.Ops
	store ports.Store
}

func (s splitEvidence) EndLane(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, result workorders.LaneResult, actor ledger.Actor) error {
	bare := result
	bare.Branch, bare.Head, bare.Artifacts, bare.Checks = "", "", nil, nil
	if err := s.Ops.EndLane(ctx, scope, holder, bare, actor); err != nil {
		return err
	}
	crashpoint.Hit("lane.end", "split")
	return s.store.Write(ctx, scope, "lane.end", func(tx ports.WriteTx) error {
		artifact := ledger.Artifact{ID: holder.Lane + "-branch", Lane: holder.Lane, Kind: ledger.ArtifactBranch, URI: result.Branch, Digest: result.Head}
		if err := tx.Lanes().AddArtifact(artifact); err != nil {
			return err
		}
		artifact.ID, artifact.Kind = holder.Lane+"-report", ledger.ArtifactReport
		if err := tx.Lanes().AddArtifact(artifact); err != nil {
			return err
		}
		err := tx.Evidence().Record(workorders.Evidence{
			ID: holder.Lane + "-check", WorkOrder: holder.WorkOrder, Lane: holder.Lane, Fence: holder.Fence + 1, Name: "unit",
			Status: workorders.CheckPassed, Source: workorders.SourceLane,
		})
		if err != nil {
			return err
		}
		_, err = tx.Append(noteEvent(actor))
		return err
	})
}

// anyoneAccepts answers as a member for whoever asked, so a lane accepts its own work.
type anyoneAccepts struct {
	contract.Ops
}

func (a anyoneAccepts) Accept(ctx context.Context, scope ledger.WorkspaceID, id string, _ ledger.Actor) (workorders.State, error) {
	return a.Ops.Accept(ctx, scope, id, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})
}

// reworkWithNoCheck moves work to `ready` and never asks the definition of ready.
type reworkWithNoCheck struct {
	contract.Ops
	store ports.Store
}

func (r reworkWithNoCheck) Rework(ctx context.Context, scope ledger.WorkspaceID, id, reason string, actor ledger.Actor) (workorders.State, error) {
	err := r.store.Write(ctx, scope, "acceptance.rework", func(tx ports.WriteTx) error {
		won, err := tx.WorkOrders().Move(id, workorders.Acceptance, workorders.Ready)
		if err != nil || !won {
			return err
		}
		event, err := workorders.ChangeEvent(workorders.EventType(workorders.TriggerRework), id,
			workorders.Change{Trigger: workorders.TriggerRework, Reason: reason}, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	return workorders.ContractReview, err
}

// splitOverturn ends a decision in one transaction and writes the rest in another, so a kill
// between them leaves a reversal with no decision in its place and no rework.
type splitOverturn struct {
	contract.Ops
	store ports.Store
}

func (s splitOverturn) Overturn(ctx context.Context, scope ledger.WorkspaceID, id string, overturn questions.Overturn, actor ledger.Actor) (questions.Overturned, error) {
	err := s.store.Write(ctx, scope, "decision.overturn", func(tx ports.WriteTx) error {
		if _, err := tx.Questions().EndDecision(id, questions.DecisionOverturned); err != nil {
			return err
		}
		record, err := tx.Questions().Decision(id)
		if err != nil {
			return err
		}
		event, err := questions.RecordEvent(questions.EventDecisionEnded, questions.SubjectDecision, id, record, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		return questions.Overturned{}, err
	}
	crashpoint.Hit("decision.overturn", "split")
	return questions.Overturned{}, nil
}

// freeRuling writes a ruling with no policy behind it, which is the path FR-ORCH-5 forbids.
type freeRuling struct {
	contract.Ops
	store ports.Store
}

func (f freeRuling) Rule(ctx context.Context, scope ledger.WorkspaceID, id string, ruling questions.Ruling, actor ledger.Actor) (questions.Decision, error) {
	record := questions.Decision{ID: ruling.ID, Status: ruling.Status, Statement: ruling.Statement, QuestionID: id, PolicyVersion: 1}
	return record, f.store.Write(ctx, scope, "question.rule", func(tx ports.WriteTx) error {
		if err := tx.Questions().InsertDecision(record); err != nil {
			return err
		}
		event, err := questions.RecordEvent(questions.EventDecisionRecorded, questions.SubjectDecision, record.ID, record, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}

// freeDecision records what it is given, with no policy behind it.
type freeDecision struct {
	contract.Ops
	store ports.Store
}

func (f freeDecision) RecordDecision(ctx context.Context, scope ledger.WorkspaceID, draft questions.Decision, actor ledger.Actor) (questions.Decision, error) {
	draft.PolicyVersion = 1
	return draft, f.store.Write(ctx, scope, "decision.record", func(tx ports.WriteTx) error {
		if err := tx.Questions().InsertDecision(draft); err != nil {
			return err
		}
		event, err := questions.RecordEvent(questions.EventDecisionRecorded, questions.SubjectDecision, draft.ID, draft, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}

// splitDelivery closes a batch in one transaction and writes its interruption in another,
// so a kill between them loses the one record that counts it.
type splitDelivery struct {
	contract.Ops
	store ports.Store
}

func (s splitDelivery) DeliverBatches(ctx context.Context, scope ledger.WorkspaceID, _ ledger.Actor) ([]string, error) {
	var due []string
	err := s.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		due, err = tx.Questions().DueBatches(now)
		return err
	})
	if err != nil {
		return nil, err
	}
	for _, id := range due {
		err := s.store.Write(ctx, scope, "question.deliver", func(tx ports.WriteTx) error {
			if _, err := tx.Questions().CloseBatch(id, questions.BatchDelivered, tx.Now()); err != nil {
				return err
			}
			_, err := tx.Append(noteEvent(ledger.Actor{Kind: ledger.ActorSystem, ID: "batch-tick"}))
			return err
		})
		if err != nil {
			return nil, err
		}
		crashpoint.Hit("question.deliver", "split")
	}
	return due, nil
}

// openGate escalates with no gate, which is the one way past a member's attention.
type openGate struct {
	contract.Ops
	store ports.Store
}

func (o openGate) Escalate(ctx context.Context, scope ledger.WorkspaceID, id string, escalation questions.Escalation, actor ledger.Actor) (questions.Question, error) {
	var record questions.Question
	err := o.store.Write(ctx, scope, "question.escalate", func(tx ports.WriteTx) error {
		var err error
		if record, err = tx.Questions().Question(id); err != nil {
			return err
		}
		if record.ClaimedBy != actor.ID {
			return questions.ErrNotHeld
		}
		record.Status, record.EscalationReason, record.Options, record.Recommendation = questions.StatusNeedsHuman, escalation.Reason, escalation.Options, escalation.Recommendation
		if err := tx.Questions().Save(record); err != nil {
			return err
		}
		event, err := questions.RecordEvent(questions.EventQuestionChanged, questions.SubjectQuestion, id, record, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	return record, err
}

// outsideCheck compares the fence before its transaction, so a handover can pass
// between the check and the write.
type outsideCheck struct {
	contract.Ops
	store ports.Store
}

func (o outsideCheck) Write(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, _ string, payload []byte, actor ledger.Actor) (ledger.Event, error) {
	var current workorders.WorkOrder
	err := o.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		current, err = tx.WorkOrders().Get(holder.WorkOrder)
		return err
	})
	if err != nil {
		return ledger.Event{}, err
	}
	if current.Fence != holder.Fence {
		return ledger.Event{}, &workorders.FencedError{Current: current.Fence, State: current.State}
	}
	if stepper, ok := o.store.(contract.Stepper); ok {
		stepper.Step("fenced_write", contract.StepGuarded)
	}
	return appendWrite(ctx, o.store, scope, holder, payload, actor)
}

func appendWrite(ctx context.Context, store ports.Store, scope ledger.WorkspaceID, holder workorders.Holder, payload []byte, actor ledger.Actor) (ledger.Event, error) {
	var written ledger.Event
	err := store.Write(ctx, scope, "fenced_write.unguarded", func(tx ports.WriteTx) error {
		event, err := workorders.ChangeEvent(workorders.EventFencedWrite, holder.WorkOrder, workorders.Change{
			Guarded: true, Fence: holder.Fence, ClaimID: holder.ClaimID, Lane: holder.Lane,
			Generation: holder.Generation, Payload: payload,
		}, actor)
		if err != nil {
			return err
		}
		written, err = tx.Append(event)
		return err
	})
	return written, err
}

// renewOnly gives a returning worker only the renewal statement, which excludes
// `lapsed`.
type renewOnly struct {
	contract.Ops
}

func (r renewOnly) Return(ctx context.Context, scope ledger.WorkspaceID, lease workorders.Lease, ttlMs int64, _ ledger.Actor) error {
	return r.Renew(ctx, scope, lease, ttlMs)
}

var memoryFence atomic.Int64

// fenceInMemory takes the fence from a process counter, which a restart resets.
func fenceInMemory(name, text string) string {
	if name != "lease.grant" {
		return text
	}
	if !strings.Contains(text, "fence = fence + 1") {
		panic("variant: lease.grant no longer moves the fence")
	}
	return strings.Replace(text, "fence = fence + 1", "fence = "+strconv.FormatInt(memoryFence.Add(1), 10), 1)
}

func forgetFence(store ports.Store) ports.Store {
	memoryFence.Store(0)
	return store
}

const (
	stateGuard = "\n   AND state = 'ready'"
	fenceGuard = "\n   AND fence = :fence"
	leaseGuard = "\n   AND fence = :fence\n   AND claim_id = :claim_id"

	deadlineGuard = "\n   AND lease_until > :now"
)

// allVariants lists every broken variant. The name is also how a child finds one.
func allVariants() []variant {
	noStateGuard := sqlstore.Sabotage{Rewrite: replace("lease.grant", stateGuard, "")}
	noFencePredicate := sqlstore.Sabotage{Rewrite: replace("lease.guard", fenceGuard, "")}
	memoryFences := sqlstore.Sabotage{Rewrite: fenceInMemory}
	return append(slices.Concat(handoffVariants(), wakeVariants(), parkVariants(), orchestratorVariants(), resumeVariants(), messageVariants(), limitVariants(), packVariants()), []variant{
		{
			name:     "revision taken last",
			sabotage: sqlstore.Sabotage{RevisionLast: true},
			check:    "S10",
		},
		{
			name: "a revision is skipped",
			sabotage: sqlstore.Sabotage{Rewrite: replace(sqlstore.StatementTakeRevision,
				"rev = rev + 1", "rev = rev + 2")},
			check: "S1",
		},
		{
			name: "the event list has no workspace scope",
			sabotage: sqlstore.Sabotage{Rewrite: replace("event.list",
				"workspace_id = :workspace_id AND rev > :after", "(:workspace_id = :workspace_id) AND rev > :after")},
			check: "S5",
		},
		{
			name: "a projection differs from its event",
			sabotage: sqlstore.Sabotage{Rewrite: replace("work_order.insert",
				":title, :repository_id", "'tampered', :repository_id")},
			check: "S8",
		},
		{
			name:     "a transaction with no event commits",
			sabotage: sqlstore.Sabotage{CommitEmpty: true},
			check:    "S4",
		},
		{
			name: "a work order and its event in two transactions",
			ops: func(s ports.Store) contract.Ops {
				return splitCreate{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) { return createCase(), "split" },
		},
		{name: "claim without the state guard", sabotage: noStateGuard, check: "L1"},
		{name: "claim without the state guard", sabotage: noStateGuard, check: "random"},
		{name: "fence kept in process memory", sabotage: memoryFences, wrap: forgetFence, check: "L2"},
		{name: "fence kept in process memory", sabotage: memoryFences, wrap: forgetFence, check: "random"},
		{name: "write without the fencing predicate", sabotage: noFencePredicate, check: "L3"},
		{name: "write without the fencing predicate", sabotage: noFencePredicate, check: "L5"},
		{name: "write without the fencing predicate", sabotage: noFencePredicate, check: "paused"},
		{name: "write without the fencing predicate", sabotage: noFencePredicate, check: "holder"},
		{
			name: "fence checked outside the transaction",
			ops: func(s ports.Store) contract.Ops {
				return outsideCheck{Ops: contract.RealOps(s), store: s}
			},
			check: "L4",
		},
		{
			name: "fence moves only on a grant",
			sabotage: sqlstore.Sabotage{Rewrite: chain(
				replace("lease.revoke", "\n       fence = fence + 1,", ""),
				replace("lease.release", "\n       fence = fence + 1,", ""),
			)},
			check: "L5",
		},
		{
			name:     "lapse transfers ownership",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.lapse", "SET state = 'lapsed'", "SET state = 'ready'")},
			check:    "L6",
		},
		{
			name:     "renewal without the fence predicate",
			sabotage: sqlstore.Sabotage{Rewrite: replace(sqlstore.OpRenew, leaseGuard, "")},
			check:    "L8",
		},
		{
			name:     "renewal revives an expired lease",
			sabotage: sqlstore.Sabotage{Rewrite: replace(sqlstore.OpRenew, deadlineGuard, "")},
			check:    "L24",
		},
		{name: "renewal revives an expired lease", check: "solo-sleep"},
		{
			name:     "a lane's end keeps the fence",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.end", "\n       fence = fence + 1,", "")},
			check:    "L26",
		},
		{
			name:     "a lane's end without the fence predicate",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.end", leaseGuard, "")},
			check:    "L27",
		},
		{
			name: "a lane's end without the generation check",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lane.generation",
				"SELECT current_generation FROM lanes", "SELECT 1 FROM lanes")},
			check: "L28",
		},
		{
			name:     "a lane's end keeps the claim",
			sabotage: sqlstore.Sabotage{Rewrite: replace("claim.end", "ended_at IS NULL", "1 = 0")},
			check:    "L25",
		},
		{
			name:     "a dispatch leaves no record on the claim",
			sabotage: sqlstore.Sabotage{Rewrite: replace("claim.dispatch", "ended_at IS NULL", "1 = 0")},
			check:    "L31",
		},
		{
			name:  splitLaneEnd,
			check: "crash",
			crash: func() (crashCase, string) { return workerLaneCase(), laneSplitStep },
		},
		{name: sabotageTrackedOnly, check: "crash", crash: func() (crashCase, string) { return stopCase("worker.lane", "allocated"), "allocated" }},
		{name: sabotageOwnIndex, check: "crash", crash: func() (crashCase, string) { return stopCase("worker.lane", "allocated"), "allocated" }},
		{name: sabotageNoGuard, check: "crash", crash: func() (crashCase, string) { return stopCase("worker.lane", "allocated"), "allocated" }},
		{name: sabotageFenced, check: "crash", crash: func() (crashCase, string) { return fencedCase("worker.lane", "allocated"), "allocated" }},
		{name: sabotageKeeps, check: "crash", crash: func() (crashCase, string) { return stopCase("worker.lane", "allocated"), "allocated" }},
		{name: "a removal that reads nothing first", check: "removal"},
		{name: "a guard's push that forces", check: "force", gitSabotage: gitcli.Sabotage{ForcePush: true}},
		{name: "a reader that skips what it missed", check: "shim"},
		{name: "a trust dialog confirmed where its focus stands", check: "pane", pane: claudecode.SabotageBlindConfirm, paneStep: "checked"},
		{name: "a brief sent again after a crash", check: "pane", pane: claudecode.SabotageRepeatBrief, paneStep: "briefed"},
		{name: "a crash charged again by the next worker", check: "restart"},
		{
			name:  retriesBlind,
			check: "crash",
			crash: func() (crashCase, string) { return effectCase("effect.session"), "performed" },
		},
		{
			name:     "lapse trusts the sweep's earlier read",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.lapse", "\n   AND lease_until <= :now", "")},
			check:    "L9",
		},
		{name: "time read on the client", sabotage: sqlstore.Sabotage{ClientClock: true}, check: "L10"},
		{
			name:     "row count 0 means lost",
			sabotage: sqlstore.Sabotage{Rewrite: replace("claim.find", "AND id = :id", "AND id = :id AND 1 = 0")},
			check:    "L11",
		},
		{
			name: "only running can lapse",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.lapse",
				"state IN ('claimed', 'running')", "state IN ('running')")},
			check: "L12",
		},
		{
			name:  "only renewal exists for a return",
			ops:   func(s ports.Store) contract.Ops { return renewOnly{Ops: contract.RealOps(s)} },
			check: "L13",
		},
		{
			name: "return without a guard",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.return",
				"\n   AND fence = :fence\n   AND claim_id = :claim_id\n   AND state = 'lapsed'", "")},
			check: "L14",
		},
		{
			name: "claim skips eligibility",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.work_in_progress",
				"state IN ('claimed', 'running', 'lapsed')", "state IN ('nothing')")},
			check: "L15",
		},
		{
			name: "claim skips the repository cap",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.in_repository",
				"state IN ('claimed', 'running', 'lapsed')", "state IN ('nothing')")},
			check: "L32",
		},
		{
			name: "claim skips the worker cap",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.held_by_worker",
				"state IN ('claimed', 'running', 'lapsed')", "state IN ('nothing')")},
			check: "L32",
		},
		{
			name: "a lapsed lease leaves the worker's count",
			sabotage: sqlstore.Sabotage{Rewrite: chain(
				replace("lease.held_by_worker", "state IN ('claimed', 'running', 'lapsed')", "state IN ('claimed', 'running')"),
				replace("lease.work_in_progress", "state IN ('claimed', 'running', 'lapsed')", "state IN ('claimed', 'running')"),
			)},
			check: "L33",
		},
		{
			name: "write without the generation check",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lane.generation",
				"SELECT current_generation FROM lanes", "SELECT 1 FROM lanes")},
			check: "L16",
		},
		{
			name: "a contract version and its work orders in two transactions",
			ops: func(s ports.Store) contract.Ops {
				return splitIntake{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) { return intakeCase("intake.item", withSteps()), "split" },
		},
		{
			name: "an uploaded event loses its identity",
			sabotage: sqlstore.Sabotage{Rewrite: replace("event.append",
				":rev, :id, :type", ":rev, :id || CAST(:rev AS TEXT), :type")},
			check: "plane",
			crash: func() (crashCase, string) {
				return uploadCase("worker.upload", []string{"answered"}), "answered"
			},
		},
		{
			name: "a lapsed lease cannot return",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.return",
				"state = 'lapsed'", "state = 'nothing'")},
			check: "sleep",
		},
		{
			name:        "a create keeps a dead registration",
			gitSabotage: gitcli.Sabotage{SkipPrune: true},
			check:       "worktree",
		},
		{
			name:     "a write retried after a lost answer is not found",
			sabotage: sqlstore.Sabotage{Rewrite: replace("event.find", "AND id = :id", "AND id = :id AND 1 = 0")},
			check:    "L23",
		},
		{
			name: "intake does not compare the digest",
			sabotage: sqlstore.Sabotage{Rewrite: replace("contract.latest",
				"plan_id, title, digest,", "plan_id, title, 'stale',")},
			check: "I4",
		},
		{
			name: "a new version leaves ready work where it was",
			sabotage: sqlstore.Sabotage{Rewrite: replace("work_order.by_contract",
				"AND contract_id = :contract_id", "AND contract_id = :contract_id AND 1 = 0")},
			check: "I16",
		},
		{
			name: "a review carries over to a new version",
			sabotage: sqlstore.Sabotage{Rewrite: replace("work_order.pin",
				", reviewed_by = NULL", "")},
			check: "I17",
		},
		{
			name: "a new version moves work that started",
			ops: func(s ports.Store) contract.Ops {
				return pinsStarted{Ops: contract.RealOps(s), store: s}
			},
			check: "I18",
		},
		{
			name: "a new version and its pin in two transactions",
			ops: func(s ports.Store) contract.Ops {
				return splitRepin{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) { return repinCase("split"), "split" },
		},
		{
			name: "a requirement read at its first version",
			sabotage: sqlstore.Sabotage{Rewrite: replace("requirement.latest",
				"ORDER BY version DESC", "ORDER BY version ASC")},
			check: "I21",
		},
		{
			name: "an unchanged requirement versions again",
			sabotage: sqlstore.Sabotage{Rewrite: replace("requirement.latest",
				"SELECT id, version, requirement_key, text, source, digest", "SELECT id, version, requirement_key, text, source, 'stale'")},
			check: "I20",
		},
		{
			name: "search has no workspace scope",
			sabotage: sqlstore.Sabotage{Rewrite: replace("search.ranked",
				"m.workspace_id = :workspace_id", "(:workspace_id = :workspace_id)")},
			check: "Q4",
		},
		{
			name: "a question search has no workspace scope",
			sabotage: sqlstore.Sabotage{Rewrite: replace("question.search",
				"q.workspace_id = :workspace_id", "(:workspace_id = :workspace_id)")},
			check: "Q4",
		},
		{
			name: "search ranks the worse match first",
			sabotage: sqlstore.Sabotage{Rewrite: replace("search.ranked",
				"ORDER BY m.score DESC", "ORDER BY m.score ASC")},
			check: "Q2",
		},
		{
			name: "search reads a kind nobody asked for",
			sabotage: sqlstore.Sabotage{Rewrite: replace("search.ranked",
				"CAST(:kinds AS TEXT) LIKE '%,' || m.kind || ',%'", "CAST(:kinds AS TEXT) <> ''")},
			check: "Q3",
		},
		{
			name: "an overturned decision stays a candidate",
			sabotage: sqlstore.Sabotage{Rewrite: replace("search.ranked",
				"d.status IN ('overturned', 'replaced')", "d.status IN ('nothing')")},
			check: "Q5",
		},
		{
			name: "a resolved question stays a candidate",
			sabotage: sqlstore.Sabotage{Rewrite: replace("search.ranked",
				"q.status IN ('answered', 'dismissed')", "q.status IN ('nothing')")},
			check: "Q5",
		},
		{
			name: "a question and its search text in two transactions",
			ops: func(s ports.Store) contract.Ops {
				return splitIndex{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) { return recordQuestionCase("split"), "split" },
		},
		{
			name: "one text asks twice",
			sabotage: sqlstore.Sabotage{Rewrite: replace("question.by_hash",
				"AND normalized_hash = :normalized_hash", "AND normalized_hash = :normalized_hash AND 1 = 0")},
			check: "Q12",
		},
		{
			name: "racing asks make two questions",
			sabotage: sqlstore.Sabotage{Rewrite: replace("question.by_hash",
				"AND normalized_hash = :normalized_hash", "AND normalized_hash = :normalized_hash AND 1 = 0")},
			check: "Q15",
		},
		{
			name: "a take with no compare-and-set",
			sabotage: sqlstore.Sabotage{Rewrite: replace("question.take",
				"\n   AND (claimed_by IS NULL OR claim_until <= :now OR claimed_by = :holder)", "")},
			check: "Q16",
		},
		{
			name: "a hold never lapses",
			sabotage: sqlstore.Sabotage{Rewrite: replace("question.take",
				"claim_until <= :now OR ", "")},
			check: "Q17",
		},
		{
			name: "the definition of ready sees no question",
			sabotage: sqlstore.Sabotage{Rewrite: replace("question.open_blocking",
				"AND blocking = 1", "AND blocking = 2")},
			check: "Q21",
		},
		{
			name: "a closed question still holds work",
			sabotage: sqlstore.Sabotage{Rewrite: replace("question.open_blocking",
				"AND status IN ('open', 'triage', 'needs_human')", "")},
			check: "Q21",
		},
		{
			name: "the gate lets anything through",
			ops: func(s ports.Store) contract.Ops {
				return openGate{Ops: contract.RealOps(s), store: s}
			},
			check: "Q19",
		},
		{
			name: "an ask and its attachment in two transactions",
			ops: func(s ports.Store) contract.Ops {
				return splitAsk{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) { return askCase("question.ask", nil), "split" },
		},
		{
			name:     "a batch delivered before its window closes",
			sabotage: sqlstore.Sabotage{Rewrite: replace("question_batch.due", "\n   AND closes_at <= :now", "")},
			check:    "B1",
		},
		{
			name:     "a blocking question waits out the window",
			sabotage: sqlstore.Sabotage{Rewrite: replace("question_batch.hasten", "AND closes_at > :closes_at", "AND 1 = 0")},
			check:    "B2",
		},
		{
			name:     "a delivery with no compare-and-set",
			sabotage: sqlstore.Sabotage{Rewrite: replace("question_batch.close", "\n   AND status = 'open'", "")},
			check:    "B5",
		},
		{
			name: "an answer and its decision in two transactions",
			ops: func(s ports.Store) contract.Ops {
				return splitAnswer{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) { return answerCase(), "split" },
		},
		{
			name: "an answer and its messages in two transactions",
			ops: func(s ports.Store) contract.Ops {
				return splitTell{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) {
				return answerTellsCase("an answer that may tell nobody", "split"), "split"
			},
		},
		{name: "an answer that tells no lane", control: control.Sabotage{LeavesAnswers: true}, check: "AN1"},
		{
			name:     "an answer that reaches its first lane",
			sabotage: sqlstore.Sabotage{Rewrite: replace("question.attachments", " ORDER BY question_id, lane_id", " ORDER BY question_id, lane_id LIMIT 1")}, check: "AN1",
		},
		{
			name: "an acceptance in two transactions",
			ops: func(s ports.Store) contract.Ops {
				return splitAccept{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) { return acceptCase(), "split" },
		},
		{
			name: "a lane's evidence in a transaction of its own",
			ops: func(s ports.Store) contract.Ops {
				return splitEvidence{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) { return laneEndCase(), "split" },
		},
		{
			name:     "a move that does not compare the state",
			sabotage: sqlstore.Sabotage{Rewrite: replace("work_order.move", " AND state = :from_state", "")},
			check:    "A9",
		},
		{
			name: "acceptance that ignores who asks",
			ops: func(s ports.Store) contract.Ops {
				return anyoneAccepts{Ops: contract.RealOps(s)}
			},
			check: "A1",
		},
		{
			name:     "a lane's own check reads as independent",
			sabotage: sqlstore.Sabotage{Rewrite: replace("evidence.record", ":status, :source,", ":status, 'forge',")},
			check:    "A4",
		},
		{
			name:     "an earlier attempt's evidence still counts",
			sabotage: sqlstore.Sabotage{Rewrite: replace("evidence.attempt", " AND fence = :fence", "")},
			check:    "A7",
		},
		{
			name: "a rework that skips the definition of ready",
			ops: func(s ports.Store) contract.Ops {
				return reworkWithNoCheck{Ops: contract.RealOps(s), store: s}
			},
			check: "A11",
		},
		{
			name: "a check run again keeps its first answer",
			sabotage: sqlstore.Sabotage{Rewrite: replace("evidence.record",
				"DO UPDATE SET status = :status, details_uri = :details_uri", "DO NOTHING")},
			check: "A13",
		},
		{
			name: "an overturn in two transactions",
			ops: func(s ports.Store) contract.Ops {
				return splitOverturn{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) { return overturnCase(), "split" },
		},
		{
			name: "a ruling with no policy behind it",
			ops: func(s ports.Store) contract.Ops {
				return freeRuling{Ops: contract.RealOps(s), store: s}
			},
			check: "D1",
		},
		{
			name: "a decision with no policy behind it",
			ops: func(s ports.Store) contract.Ops {
				return freeDecision{Ops: contract.RealOps(s), store: s}
			},
			check: "D2",
		},
		{
			name:     "a decision ends twice",
			sabotage: sqlstore.Sabotage{Rewrite: replace("decision.end", "\n   AND status IN ('assumed', 'decided')", "")},
			check:    "D19",
		},
		{
			name:     "an outcome fills twice",
			sabotage: sqlstore.Sabotage{Rewrite: replace("experience.outcome", "\n   AND outcome IS NULL", "")},
			check:    "D19",
		},
		{
			name: "a policy version is written over",
			sabotage: sqlstore.Sabotage{Rewrite: replace("policy.insert",
				"DO NOTHING", "DO UPDATE SET body = EXCLUDED.body")},
			check: "D14",
		},
		{
			name: "a decision keeps no policy version",
			sabotage: sqlstore.Sabotage{Rewrite: replace("decision.insert",
				":question_id, :policy_version,", ":question_id, 0 * :policy_version,")},
			check: "D4",
		},
		{
			name:     "a replaced decision stays in force",
			sabotage: sqlstore.Sabotage{Rewrite: replace("decision.end", "SET status = :status", "SET status = status")},
			check:    "D11",
		},
		{
			name: "an overlap misses a wider scope",
			sabotage: sqlstore.Sabotage{Rewrite: replace("decision.overlapping",
				"\n     OR substr(CAST(:scope AS TEXT), 1, length(d.scope) + 1) = d.scope || '/'", "")},
			check: "D10",
		},
		{
			name: "a delivery and its interruption in two transactions",
			ops: func(s ports.Store) contract.Ops {
				return splitDelivery{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) { return deliverCase(), "split" },
		},
		{
			name: "a sign-in and its record in two transactions",
			ops: func(s ports.Store) contract.Ops {
				return splitSignIn{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) { return signInCase(), "split" },
		},
		{
			name: "the definition of ready sees no dependency",
			sabotage: sqlstore.Sabotage{Rewrite: replace("dependency.of",
				"work_order_id = :work_order_id", "work_order_id = :work_order_id AND 1 = 0")},
			check: "I8",
		},
		{
			name: "claim split into two transactions",
			ops: func(s ports.Store) contract.Ops {
				return splitClaim{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) { return claimCase(), "split" },
		},
	}...)
}

func findVariant(name string) (variant, bool) {
	for _, v := range allVariants() {
		if v.name == name {
			return v, true
		}
	}
	return variant{}, false
}

// variantOps wraps a child's store for its variant.
func variantOps(name string, store ports.Store) (ports.Store, contract.Ops, error) {
	if name == "" {
		return store, contract.RealOps(store), nil
	}
	v, ok := findVariant(name)
	if !ok {
		return nil, nil, fmt.Errorf("unknown variant %q", name)
	}
	sqlstore.Sabotaged(v.sabotage)
	control.Sabotaged(v.control)
	if v.wrap != nil {
		store = v.wrap(store)
	}
	if v.ops != nil {
		return store, v.ops(store), nil
	}
	return store, contract.RealOps(store), nil
}

func findCheck(id string) (contract.Check, bool) {
	for _, c := range contract.AllChecks() {
		if c.ID == id {
			return c, true
		}
	}
	return contract.Check{}, false
}

// runVariant returns the check's verdict on one variant.
func runVariant(t *testing.T, v variant) error {
	switch v.check {
	case "crash":
		c, step := v.crash()
		return runCrashCase(newWorld(t, v.name), c, step)
	case "paused":
		return checkPausedWorker(newWorld(t, v.name))
	case "plane":
		sqlstore.Sabotaged(v.sabotage)
		defer sqlstore.Sabotaged(sqlstore.Sabotage{})
		c, step := v.crash()
		return runCrashCase(newWorld(t, ""), c, step)
	case "plane-control":
		control.Sabotaged(v.control)
		defer control.Sabotaged(control.Sabotage{})
		c, step := v.crash()
		return runCrashCase(newWorld(t, ""), c, step)
	case "worktree":
		gitcli.Sabotaged(v.gitSabotage)
		defer gitcli.Sabotaged(gitcli.Sabotage{})
		return checkDeadRegistration(newWorld(t, ""))
	case "force":
		gitcli.Sabotaged(v.gitSabotage)
		defer gitcli.Sabotaged(gitcli.Sabotage{})
		return checkNoForce(newWorld(t, ""))
	case "removal":
		return checkRemovalReads(newWorld(t, sabotageBlindRemoval))
	case "sleep":
		sqlstore.Sabotaged(v.sabotage)
		defer sqlstore.Sabotaged(sqlstore.Sabotage{})
		return checkSleep(newWorld(t, ""), false)
	case "solo-sleep":
		return checkSoloSleep(newWorld(t, ""), sqlstore.EnvCut+"="+sqlstore.OpRenew+"|"+deadlineGuard)
	case "lifecycle":
		return checkLostWake(newWorld(t, v.name))
	case "random":
		return runRandomVariant(t, v)
	case "shim":
		return checkShimGroundTruth(t)
	case "pane":
		return checkPaneVariant(t, v.pane, v.paneStep)
	case "restart":
		return runRestartCrash(t, "charged", worker.EnvSabotage+"="+worker.SabotageRecharge)
	case "holder":
		run := newRandomRun(newWorld(t, v.name), holderVariantKills)
		run.holders = 1
		if err := run.seedWork(3); err != nil {
			return err
		}
		return run.execute()
	}
	check, ok := findCheck(v.check)
	if !ok {
		t.Fatalf("unknown check %q", v.check)
	}
	sqlstore.Sabotaged(v.sabotage)
	defer sqlstore.Sabotaged(sqlstore.Sabotage{})
	control.Sabotaged(v.control)
	defer control.Sabotaged(control.Sabotage{})
	rig := contract.NewRig(t, contract.SQLiteFile)
	rig.Wrap = v.wrap
	if v.ops != nil {
		rig.NewOps = v.ops
	}
	return check.Run(rig)
}

// holderVariantKills is small, because the stopped holder decides this variant.
const holderVariantKills = 4

// runRandomVariant repeats a run up to a bound, because a defect that needs one
// interleaving is not found in every schedule.
func runRandomVariant(t *testing.T, v variant) error {
	var err error
	for attempt := range 4 {
		run := newRandomRun(newWorld(t, v.name), crashKills(40))
		run.seed += uint64(attempt)
		if err := run.seedWork(3); err != nil {
			return err
		}
		if err = run.execute(); err != nil {
			return err
		}
	}
	return err
}

// TestBrokenVariants is the proof that the checks can fail. A variant counts as
// caught only through a typed violation, so an unrelated error cannot pass.
func TestBrokenVariants(t *testing.T) {
	caught := 0
	for _, v := range allVariants() {
		t.Run(v.check+"-"+v.name, func(t *testing.T) {
			err := runVariant(t, v)
			var violation *contract.Violation
			switch {
			case errors.As(err, &violation):
				t.Logf("caught by %s: %s", v.check, violation.Message)
				caught++
			case err != nil:
				t.Errorf("a harness error is not a catch: %v", err)
			default:
				t.Errorf("NOT CAUGHT: %s passed %s", v.name, v.check)
			}
		})
	}
	t.Logf("broken variants caught: %d of %d", caught, len(allVariants()))
}
