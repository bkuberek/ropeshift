package apitest

import (
	"context"
	"encoding/json"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// The names a populated world uses, so a test can find what it reads.
const (
	WorldWorker     = "worker-a"
	WorldHostileBit = `<img src=x onerror=alert(1)><script>alert(2)</script>`
)

// World is a daemon with a worker that runs lanes, so a test can lay out any state a person can read
// through the paths that make it, and never by writing rows.
type World struct {
	t      *testing.T
	Daemon *Daemon
	ctx    context.Context
	plane  *control.Workers
	auth   ledger.WorkerAuth
	epoch  int64
	seq    int64
	// Questions is what a lane's question reaches a person through.
	Questions *control.Questions
	questions int
}

var worldMember = ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}

// worldStart is where a Clock starts, in epoch milliseconds.
const worldStart = 1_790_000_000_000

// Clock is database time a test moves by hand, so a read model's times repeat exactly.
type Clock struct {
	at atomic.Int64
}

// NewClock returns a clock at a fixed time.
func NewClock() *Clock {
	c := &Clock{}
	c.at.Store(worldStart)
	return c
}

// Advance moves database time.
func (c *Clock) Advance(by time.Duration) { c.at.Add(by.Milliseconds()) }

// Dialect is the wrapper Options.Clock takes.
func (c *Clock) Dialect(inner sqlstore.Dialect) sqlstore.Dialect { return clockedDialect{inner, c} }

type clockedDialect struct {
	sqlstore.Dialect
	clock *Clock
}

// ClockSQL reads the hand-moved clock.
func (d clockedDialect) ClockSQL() string {
	return "SELECT " + strconv.FormatInt(d.clock.at.Load(), 10)
}

// NewWorld registers one worker on a daemon.
func NewWorld(t *testing.T, d *Daemon) *World {
	t.Helper()
	ctx := context.Background()
	w := &World{
		t: t, Daemon: d, ctx: ctx,
		plane:     control.NewWorkers(d.Store, Scope, control.WorkerOptions{HeartbeatMs: HeartbeatMs, LeaseTTLMs: LeaseTTL}),
		Questions: control.NewQuestions(d.Store, questions.BatchPolicy{WindowMs: 1, MaxWaitMs: 1}),
	}
	token, err := w.plane.CreateJoinToken(ctx, worldMember)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := w.plane.Register(ctx, ledger.Registration{JoinToken: token, WorkerID: WorldWorker, Name: "solo"})
	if err != nil {
		t.Fatal(err)
	}
	w.auth = ledger.WorkerAuth{WorkerID: WorldWorker, Credential: credential.Credential}
	session, err := w.plane.OpenSession(ctx, ledger.SessionRequest{Auth: w.auth, Protocol: ledger.ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	w.epoch = session.Epoch
	return w
}

// Credential is what this world's worker proves itself with, for a test that looks for it where it
// must not be.
func (w *World) Credential() string { return w.auth.Credential }

// Lane is a lane a world runs, and the claim it runs under.
type Lane struct {
	w     *World
	ID    string
	Grant workorders.Grant
}

// Created records a work order that stays in `intake`, with no plan and no contract.
func (w *World) Created(id, title string) {
	w.t.Helper()
	draft := workorders.WorkOrder{ID: id, Kind: workorders.KindChange, Title: title, MaxAttempts: 1, RepositoryID: "repository-1"}
	if _, err := control.NewWorkOrders(w.Daemon.Store).Create(w.ctx, Scope, draft, worldMember); err != nil {
		w.t.Fatal(err)
	}
}

// Recorded records a work order with a plan and a contract, which waits in `contract_review`.
func (w *World) Recorded(id, title string) {
	w.t.Helper()
	work := control.LedgerWork{
		Order:     workorders.WorkOrder{ID: id, Kind: workorders.KindChange, Title: title, MaxAttempts: 1, RepositoryID: "repository-1"},
		Statement: title + ".", Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
	}
	if _, err := control.NewWorkOrders(w.Daemon.Store).Record(w.ctx, Scope, work, worldMember); err != nil {
		w.t.Fatal(err)
	}
}

// Ready records a work order and passes its review.
func (w *World) Ready(id, title string) {
	w.t.Helper()
	w.Recorded(id, title)
	if _, err := control.NewReviews(w.Daemon.Store).Pass(w.ctx, Scope, id, worldMember); err != nil {
		w.t.Fatal(err)
	}
}

// Cancel takes a work order to `canceled`, an edge no route of the API offers yet.
func (w *World) Cancel(id string) {
	w.t.Helper()
	if err := control.NewWorkOrders(w.Daemon.Store).Move(w.ctx, Scope, id, workorders.TriggerCanceled, worldMember); err != nil {
		w.t.Fatal(err)
	}
}

// Running takes a work order to `running` with a lane reporting that it works.
func (w *World) Running(id, title string) *Lane {
	w.t.Helper()
	w.Ready(id, title)
	lane := "lane-" + id
	leases := control.NewLeases(w.Daemon.Store)
	grant, err := leases.Claim(w.ctx, Scope, workorders.ClaimRequest{WorkOrder: id, ClaimID: "claim-" + id, Worker: WorldWorker, TTLMs: LeaseTTL}, worldMember)
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := leases.Dispatch(w.ctx, Scope, id, lane, worldMember); err != nil {
		w.t.Fatal(err)
	}
	l := &Lane{w: w, ID: lane, Grant: grant}
	l.Upload(ledger.UploadLaneStarted, nil)
	l.Status(ledger.LaneStatus{ActualState: "working", Liveness: "alive", ModelReach: "reachable", LedgerReach: "reachable"})
	return l
}

// Upload sends one event of this lane, and the ledger must take it.
func (l *Lane) Upload(eventType string, payload any) {
	l.w.t.Helper()
	var raw []byte
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			l.w.t.Fatal(err)
		}
		raw = encoded
	}
	l.w.seq++
	reply, err := l.w.plane.Upload(l.w.ctx, ledger.UploadBatch{Auth: l.w.auth, Epoch: l.w.epoch, Events: []ledger.UploadEvent{{
		Epoch: l.w.epoch, Seq: l.w.seq, Type: eventType, WorkOrder: l.Grant.WorkOrder, ClaimID: l.Grant.ClaimID,
		Fence: l.Grant.Fence, Lane: l.ID, Generation: 1, Payload: raw, OccurredAt: 1,
	}}})
	if err != nil {
		l.w.t.Fatal(err)
	}
	if len(reply.Rejected) != 0 {
		l.w.t.Fatalf("%s was refused: %+v", eventType, reply.Rejected)
	}
}

// Status reports a lane's state.
func (l *Lane) Status(status ledger.LaneStatus) {
	l.w.t.Helper()
	l.Upload(ledger.UploadLaneStatus, status)
}

// End reports a lane's outcome.
func (l *Lane) End(outcome workorders.Outcome, reason string) {
	l.w.t.Helper()
	result := workorders.LaneResult{Report: workorders.Report{Outcome: outcome, Reason: reason}, Branch: "lane/" + l.ID, Head: "abc123"}
	l.Upload(ledger.UploadLaneEnded, result)
}

// Park parks a lane on a wake.
func (l *Lane) Park(expr string) {
	l.w.t.Helper()
	wake := "wake-" + l.ID
	l.Upload(ledger.UploadLaneParking, ledger.ParkingStart{Wake: wake, Expr: expr})
	l.Upload(ledger.UploadLaneParked, ledger.ParkedCommit{Wake: wake, Checkpoint: ledger.Checkpoint{
		Lane: l.ID, Generation: 1, Branch: "lane/" + l.ID, Head: "abc123", Pushed: true, PendingInstruction: "Finish the retry",
	}})
}

// Effect raises an effect a crash left unknown, and returns what the ledger holds.
func (l *Lane) Effect(key, kind string, attempt int64) ledger.UnknownEffect {
	l.w.t.Helper()
	l.Upload(ledger.UploadEffectUnknown, ledger.EffectReport{Key: key, Kind: kind, Target: "the session", Attempt: attempt})
	rows, err := control.NewEffects(l.w.Daemon.Store).List(l.w.ctx, Scope)
	if err != nil {
		l.w.t.Fatal(err)
	}
	for _, row := range rows {
		if row.Key == key && row.Attempt == attempt {
			return row
		}
	}
	l.w.t.Fatalf("no effect %s", key)
	return ledger.UnknownEffect{}
}

// Approval raises a request of this lane, and returns what the ledger holds.
func (l *Lane) Approval(id, input string) authority.Approval {
	l.w.t.Helper()
	call, err := authority.ReadToolCall("Bash", []byte(input))
	if err != nil {
		l.w.t.Fatal(err)
	}
	l.Upload(ledger.UploadApprovalRaised, authority.Approval{
		ID: id, Kind: authority.KindToolCall, Subject: call.Tool, SubjectDigest: call.Digest, Input: call.Input,
		Command: call.Command, Path: call.Path, Summary: call.Summary, RequestedBy: "lane:" + l.ID,
	})
	got, err := control.NewApprovals(l.w.Daemon.Store).Get(l.w.ctx, Scope, id)
	if err != nil {
		l.w.t.Fatal(err)
	}
	return got
}

// Lapse lets a lease run out, with database time moved by the caller past its deadline.
func (w *World) Lapse() {
	w.t.Helper()
	if _, err := control.NewLeases(w.Daemon.Store).Sweep(w.ctx, Scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "lease-tick"}); err != nil {
		w.t.Fatal(err)
	}
}

// Escalated walks one question up the ladder to a person.
func (w *World) Escalated(text string, categories []string, blocking bool) string {
	w.t.Helper()
	w.questions++
	asked, err := w.Questions.Ask(w.ctx, Scope, questions.Ask{
		ID: "question-" + strconv.Itoa(w.questions), Lane: "lane-asking", Generation: 1, Text: text, Categories: categories, Blocking: blocking,
	}, ledger.Actor{Kind: ledger.ActorLane, ID: "lane-asking"})
	if err != nil {
		w.t.Fatal(err)
	}
	holder := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: WorldWorker}
	if _, err := w.Questions.Take(w.ctx, Scope, asked.QuestionID, LeaseTTL, holder); err != nil {
		w.t.Fatal(err)
	}
	for _, rung := range []int64{questions.RungLookup, questions.RungOpen} {
		step := questions.Step{Rung: rung, Candidates: []string{"decision:decision-1"}, Outcome: "no match"}
		if _, err := w.Questions.Climb(w.ctx, Scope, asked.QuestionID, step, holder); err != nil {
			w.t.Fatal(err)
		}
	}
	_, err = w.Questions.Escalate(w.ctx, Scope, asked.QuestionID, questions.Escalation{
		Reason: questions.ReasonScopeChange, Options: []string{"Keep the budget", "Drop it"}, Recommendation: "Keep the budget",
	}, holder)
	if err != nil {
		w.t.Fatal(err)
	}
	return asked.QuestionID
}

// Deliver closes every batch that is due.
func (w *World) Deliver() {
	w.t.Helper()
	if _, err := w.Questions.DeliverBatches(w.ctx, Scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "batch-tick"}); err != nil {
		w.t.Fatal(err)
	}
}

// Contract adds a version of the contract a work order pins, as intake would.
func (w *World) Contract(id string) {
	w.t.Helper()
	err := w.Daemon.Store.Write(w.ctx, Scope, "test.contract", func(tx ports.WriteTx) error {
		order, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		current, err := tx.Planning().Contract(order.ContractID, order.ContractVersion)
		if err != nil {
			return err
		}
		next := current
		next.Version++
		next.Statement += " Now with a budget."
		if err := tx.Planning().InsertContract(next); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: "test.contract", Actor: worldMember, Subject: ledger.Subject{Kind: "contract", ID: next.ID},
		})
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
}

// Goal adds a goal and puts a work order's plan under it, as intake would.
func (w *World) Goal(id, title, workOrder string) {
	w.t.Helper()
	err := w.Daemon.Store.Write(w.ctx, Scope, "test.goal", func(tx ports.WriteTx) error {
		if err := tx.Planning().SaveGoal(planning.Goal{ID: id, Title: title, Status: planning.StatusActive}); err != nil {
			return err
		}
		order, err := tx.WorkOrders().Get(workOrder)
		if err != nil {
			return err
		}
		contract, err := tx.Planning().Contract(order.ContractID, order.ContractVersion)
		if err != nil {
			return err
		}
		plan, err := tx.Planning().Plan(contract.PlanID)
		if err != nil {
			return err
		}
		plan.GoalID = id
		if err := tx.Planning().UpdatePlan(plan); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: "test.goal", Actor: worldMember, Subject: ledger.Subject{Kind: "goal", ID: id}})
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
}

// Populate lays out one of everything a person can read, each through the path that makes it, with
// time between them, so that wait time has an order to hold.
func Populate(w *World, clock *Clock) {
	w.t.Helper()
	w.Decisions()

	w.Running("wo-lapsed", "Rotate the keys")
	clock.Advance(time.Duration(LeaseTTL+1) * time.Millisecond)
	w.Lapse()
	clock.Advance(time.Minute)

	w.Running("wo-blocked", "Move the archive").End(workorders.OutcomeBlocked, "no_outcome")
	clock.Advance(time.Minute)

	w.Running("wo-effect", "Add the retry budget").Effect("lane-wo-effect/1/input/nudge", "input", 1)
	clock.Advance(time.Minute)

	w.Running("wo-accept", "Trim the log").End(workorders.OutcomeCompleted, "")
	clock.Advance(time.Minute)

	w.Running("wo-approval", "Tag the release").Approval("approval-1", `{"command":"git tag v1"}`)
	clock.Advance(time.Minute)

	w.Escalated("Should the budget hold across restarts?", []string{questions.CategoryProduct}, true)
	w.Escalated("May the "+WorldHostileBit+" stay?", []string{questions.CategoryProduct}, false)
	clock.Advance(time.Minute)
	w.Deliver()
	clock.Advance(time.Minute)

	w.Running("wo-parked", "Wait for the check").Park("human_resume")
	clock.Advance(time.Minute)
	w.Running("wo-tripped", "Fix the flaky test").Status(ledger.LaneStatus{
		ActualState: "tripped", Liveness: "dead", ModelReach: "reachable", LedgerReach: "reachable",
	})
	w.Running("wo-stalled", "Read the logs").Status(ledger.LaneStatus{
		ActualState: "working", Conditions: []string{"stalled"}, Liveness: "alive", ModelReach: "reachable", LedgerReach: "reachable",
	})
	w.Running("wo-running", "Write the docs")
	w.Contract("wo-running")
	w.Goal("goal-1", "Ship solo mode", "wo-running")
}

// Decisions records a decision, and what replaced it.
func (w *World) Decisions() {
	w.t.Helper()
	client := w.Daemon.Client
	first, err := client.RecordDecision(w.ctx, api.RecordDecision{
		ID: "decision-1", Status: questions.DecisionDecided, Statement: "Retry twice.", Rationale: "It covers the outage.",
		Scope: "retries", Categories: []string{questions.CategoryProduct}, Reversibility: questions.Reversible,
	})
	if err != nil {
		w.t.Fatal(err)
	}
	_, err = client.RecordDecision(w.ctx, api.RecordDecision{
		ID: "decision-2", Status: questions.DecisionDecided, Statement: "Retry three times.", Rationale: "The outage ran longer.",
		Scope: "retries", Categories: []string{questions.CategoryProduct}, Reversibility: questions.Reversible, Supersedes: first.ID,
	})
	if err != nil {
		w.t.Fatal(err)
	}
}
