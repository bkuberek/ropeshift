package contract

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// AcceptanceChecks hold the step from a lane's end to `done`, or back to `ready`.
var AcceptanceChecks = []Check{
	{"A1", "a lane cannot accept its own work, whatever its evidence says", checkLaneCannotAccept},
	{"A2", "a passed forge result counts by itself", checkForgeCounts},
	{"A3", "a failed forge result sends the work back", checkForgeFailure},
	{"A4", "a check only the lane reports needs a second principal", checkSecondPrincipal},
	{"A5", "a criterion no check maps to needs a member", checkMemberNeeded},
	{"A6", "a failed check cannot be accepted, and the tick sends it back", checkFailedCheck},
	{"A7", "an earlier attempt's evidence does not count, and stays on record", checkEarlierAttempt},
	{"A8", "artifacts and check results stay linked to the work order", checkEvidenceLinked},
	{"A9", "the guarded statement moves work out of acceptance once", checkAcceptanceMovesOnce},
	{"A10", "a repeated acceptance changes nothing", checkAcceptanceRetry},
	{"A11", "a rework runs the definition of ready", checkReworkReady},
	{"A12", "a forge that cannot be reached decides nothing", checkForgeUnreachable},
	{"A13", "a tick adds no event while nothing changed, and reads a check run again", checkForgeRerun},
	{"A14", "accepted and reworked work replays from the log", checkAcceptanceReplays},
	{"A15", "what a forge said about one attempt does not land on the next", checkForgeKeepsItsAttempt},
}

const (
	criterionOne = "c1"
	criterionTwo = "c2"
	branchName   = "lane/accepted"
	reworkReason = "the criteria are not met"
	// AcceptanceRepository is where Awaiting's work lives, for a forge to answer about.
	AcceptanceRepository = "repo-a"
)

// LaneCheck is a check a lane reports for a criterion.
func LaneCheck(criterion string, passed bool) workorders.CheckResult {
	return workorders.CheckResult{Name: "check-" + criterion, Criterion: criterion, Passed: passed}
}

// LaneFinished is a lane's end at a commit, with what its worker read and its session reported.
func LaneFinished(head string, checks ...workorders.CheckResult) workorders.LaneResult {
	result := laneResult(workorders.OutcomeCompleted, "")
	result.Branch, result.Head, result.Artifacts, result.Checks = branchName, head, []string{"notes.md"}, checks
	return result
}

// Awaiting returns a work order whose lane ended with result, under a contract of these
// criteria.
func (r *Rig) Awaiting(s ports.Store, criteria []string, result workorders.LaneResult) (string, error) {
	ops := r.NewOps(s)
	work := control.LedgerWork{
		Order:     workorders.WorkOrder{ID: r.ID("wo"), Kind: workorders.KindChange, Title: "Accept it", RepositoryID: AcceptanceRepository, MaxAttempts: 1},
		Statement: "Do the work.",
	}
	for _, id := range criteria {
		work.Criteria = append(work.Criteria, planning.Criterion{ID: id, Text: "It holds: " + id})
	}
	order, err := ops.Record(r.Ctx, r.Scope, work, r.Actor)
	if err != nil {
		return "", err
	}
	if _, err := ops.Pass(r.Ctx, r.Scope, order.ID, r.Actor); err != nil {
		return "", err
	}
	return order.ID, r.anotherAttempt(s, order.ID, result)
}

func (r *Rig) anotherAttempt(s ports.Store, id string, result workorders.LaneResult) error {
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	return r.NewOps(s).EndLane(r.Ctx, r.Scope, holder, result, r.worker("a"))
}

func (r *Rig) forgeSays(forge *fake.Forge, head string, checks ...ports.ForgeCheck) {
	forge.Set(r.Scope, ports.ForgeRequest{Repository: AcceptanceRepository, Commit: head}, checks...)
}

func forgeCheck(criterion string, status workorders.CheckStatus) ports.ForgeCheck {
	return ports.ForgeCheck{Name: "ci-" + criterion, Criterion: criterion, Status: status}
}

func (r *Rig) tick() ledger.Actor {
	return ledger.Actor{Kind: ledger.ActorSystem, ID: "acceptance-tick"}
}

func (r *Rig) stateIs(s ports.Store, id string, want workorders.State) error {
	row, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if row.State != want {
		return Violatedf("%s is in %s, want %s", id, row.State, want)
	}
	return nil
}

// eventsAbout counts a work order's events of a type, and returns the last.
func (r *Rig) eventsAbout(s ports.Store, id, eventType string) (int, ledger.Event, error) {
	var last ledger.Event
	count := 0
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		if err != nil {
			return err
		}
		for _, event := range events {
			if event.Subject.ID == id && event.Type == eventType {
				count++
				last = event
			}
		}
		return nil
	})
	return count, last, err
}

// checkLaneCannotAccept tries every path a lane or its worker could take, on every row of the
// evidence table, including the one where the evidence alone would pass.
func checkLaneCannotAccept(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	lane, worker := r.laneActor("lane-a"), r.worker("a")
	evidence := []struct {
		name   string
		checks []workorders.CheckResult
		forge  bool
	}{
		{name: "a forge result", forge: true},
		{name: "the lane's own check", checks: []workorders.CheckResult{LaneCheck(criterionOne, true)}},
		{name: "no check"},
		{name: "a failed check", checks: []workorders.CheckResult{LaneCheck(criterionOne, false)}},
	}
	for _, row := range evidence {
		head := r.ID("commit")
		id, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(head, row.checks...))
		if err != nil {
			return err
		}
		if row.forge {
			order, err := r.WorkOrder(s, id)
			if err != nil {
				return err
			}
			err = s.Write(r.Ctx, r.Scope, "note", func(tx ports.WriteTx) error {
				if err := tx.Evidence().Record(workorders.Evidence{
					ID: r.ID("check"), WorkOrder: id, Fence: order.Fence, Criterion: criterionOne, Name: "ci",
					Status: workorders.CheckPassed, Source: workorders.SourceForge,
				}); err != nil {
					return err
				}
				_, err := tx.Append(r.noteDraft(id))
				return err
			})
			if err != nil {
				return err
			}
		}
		for _, actor := range []ledger.Actor{lane, worker} {
			if _, err := ops.Accept(r.Ctx, r.Scope, id, actor); !errors.Is(err, workorders.ErrForbidden) {
				return Violatedf("%s: a %s accepted: %v", row.name, actor.Kind, err)
			}
			if _, err := ops.Rework(r.Ctx, r.Scope, id, reworkReason, actor); !errors.Is(err, workorders.ErrForbidden) {
				return Violatedf("%s: a %s sent the work back: %v", row.name, actor.Kind, err)
			}
			for _, trigger := range []workorders.Trigger{workorders.TriggerAcceptancePassed, workorders.TriggerRework} {
				if err := ops.Move(r.Ctx, r.Scope, id, trigger, actor); !errors.Is(err, workorders.ErrForbidden) {
					return Violatedf("%s: a %s took %s through a plain move: %v", row.name, actor.Kind, trigger, err)
				}
			}
		}
		if err := r.stateIs(s, id, workorders.Acceptance); err != nil {
			return err
		}
	}
	return nil
}

func checkForgeCounts(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	head := r.ID("commit")
	id, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(head))
	if err != nil {
		return err
	}
	forge := &fake.Forge{}
	r.forgeSays(forge, head, forgeCheck(criterionOne, workorders.CheckPassed))
	moved, err := ops.Settle(r.Ctx, r.Scope, forge, r.tick())
	if err != nil {
		return err
	}
	if !slices.Equal(moved, []string{id}) {
		return Violatedf("a tick moved %v", moved)
	}
	if err := r.stateIs(s, id, workorders.Done); err != nil {
		return err
	}
	count, event, err := r.eventsAbout(s, id, workorders.EventType(workorders.TriggerAcceptancePassed))
	if err != nil {
		return err
	}
	if count != 1 || event.Actor.Kind != ledger.ActorSystem {
		return Violatedf("%d acceptances, the last by %+v", count, event.Actor)
	}
	view, err := ops.Standing(r.Ctx, r.Scope, id)
	if err != nil {
		return err
	}
	if len(view.Checks) != 1 || view.Checks[0].Source != workorders.SourceForge || view.Checks[0].ArtifactID == "" {
		return Violatedf("the forge's result on record: %+v", view.Checks)
	}
	return nil
}

func checkForgeFailure(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	head := r.ID("commit")
	id, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(head))
	if err != nil {
		return err
	}
	forge := &fake.Forge{}
	r.forgeSays(forge, head, forgeCheck(criterionOne, workorders.CheckFailed))
	if _, err := ops.Settle(r.Ctx, r.Scope, forge, r.tick()); err != nil {
		return err
	}
	if err := r.stateIs(s, id, workorders.Ready); err != nil {
		return err
	}
	count, event, err := r.eventsAbout(s, id, workorders.EventType(workorders.TriggerRework))
	if err != nil {
		return err
	}
	if count != 1 || !strings.Contains(string(event.Data), "ci-"+criterionOne) {
		return Violatedf("%d reworks, the last says %s", count, event.Data)
	}
	return nil
}

func checkSecondPrincipal(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	finished := LaneFinished(r.ID("commit"), LaneCheck(criterionOne, true))
	byOrchestrator, err := r.Awaiting(s, []string{criterionOne}, finished)
	if err != nil {
		return err
	}
	byMember, err := r.Awaiting(s, []string{criterionOne}, finished)
	if err != nil {
		return err
	}
	if moved, err := ops.Settle(r.Ctx, r.Scope, nil, r.tick()); err != nil || len(moved) != 0 {
		return Violatedf("a tick moved %v: %v", moved, err)
	}
	if _, err := ops.Accept(r.Ctx, r.Scope, byOrchestrator, r.tick()); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("nobody stood behind a lane's check: %v", err)
	}
	if _, err := ops.Accept(r.Ctx, r.Scope, byOrchestrator, r.orchestrator("o1")); err != nil {
		return Violatedf("an orchestrator is a second principal: %v", err)
	}
	if _, err := ops.Accept(r.Ctx, r.Scope, byMember, r.Actor); err != nil {
		return Violatedf("a member is a second principal: %v", err)
	}
	for _, id := range []string{byOrchestrator, byMember} {
		if err := r.stateIs(s, id, workorders.Done); err != nil {
			return err
		}
	}
	return nil
}

func checkMemberNeeded(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	head := r.ID("commit")
	id, err := r.Awaiting(s, []string{criterionOne, criterionTwo}, LaneFinished(head, LaneCheck(criterionOne, true)))
	if err != nil {
		return err
	}
	forge := &fake.Forge{}
	r.forgeSays(forge, head, forgeCheck(criterionOne, workorders.CheckPassed))
	if moved, err := ops.Settle(r.Ctx, r.Scope, forge, r.tick()); err != nil || len(moved) != 0 {
		return Violatedf("a tick moved %v: %v", moved, err)
	}
	if _, err := ops.Accept(r.Ctx, r.Scope, id, r.orchestrator("o1")); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("an orchestrator answered for a criterion no check covers: %v", err)
	}
	if err := r.stateIs(s, id, workorders.Acceptance); err != nil {
		return err
	}
	view, err := ops.Standing(r.Ctx, r.Scope, id)
	if err != nil {
		return err
	}
	if view.Assessment.Needs != workorders.NeedMember {
		return Violatedf("the standing reads %+v", view.Assessment)
	}
	if _, err := ops.Accept(r.Ctx, r.Scope, id, r.Actor); err != nil {
		return Violatedf("a member answers for it: %v", err)
	}
	return r.stateIs(s, id, workorders.Done)
}

func checkFailedCheck(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	failed := LaneFinished(r.ID("commit"), LaneCheck(criterionOne, false))
	byTick, err := r.Awaiting(s, []string{criterionOne}, failed)
	if err != nil {
		return err
	}
	if _, err := ops.Accept(r.Ctx, r.Scope, byTick, r.Actor); !errors.Is(err, workorders.ErrNotAcceptable) {
		return Violatedf("a member accepted a failed check: %v", err)
	}
	if err := r.stateIs(s, byTick, workorders.Acceptance); err != nil {
		return err
	}
	if _, err := ops.Settle(r.Ctx, r.Scope, nil, r.tick()); err != nil {
		return err
	}
	if err := r.stateIs(s, byTick, workorders.Ready); err != nil {
		return err
	}
	byMember, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(r.ID("commit")))
	if err != nil {
		return err
	}
	if state, err := ops.Rework(r.Ctx, r.Scope, byMember, reworkReason, r.Actor); err != nil || state != workorders.Ready {
		return Violatedf("a member's rework: %s: %v", state, err)
	}
	count, event, err := r.eventsAbout(s, byMember, workorders.EventType(workorders.TriggerRework))
	if err != nil {
		return err
	}
	if count != 1 || !strings.Contains(string(event.Data), reworkReason) {
		return Violatedf("%d reworks, the last says %s", count, event.Data)
	}
	return nil
}

func checkEarlierAttempt(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	first, second := r.ID("commit"), r.ID("commit")
	id, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(first, LaneCheck(criterionOne, true)))
	if err != nil {
		return err
	}
	if _, err := ops.Rework(r.Ctx, r.Scope, id, reworkReason, r.Actor); err != nil {
		return err
	}
	if err := r.anotherAttempt(s, id, LaneFinished(second)); err != nil {
		return err
	}
	forge := &fake.Forge{}
	r.forgeSays(forge, first, forgeCheck(criterionOne, workorders.CheckPassed))
	if moved, err := ops.Settle(r.Ctx, r.Scope, forge, r.tick()); err != nil || len(moved) != 0 {
		return Violatedf("the first commit's result moved %v: %v", moved, err)
	}
	if _, err := ops.Accept(r.Ctx, r.Scope, id, r.orchestrator("o1")); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("the first attempt's check stood for the second: %v", err)
	}
	view, err := ops.Standing(r.Ctx, r.Scope, id)
	if err != nil {
		return err
	}
	if view.Assessment.Needs != workorders.NeedMember || len(view.Checks) != 1 {
		return Violatedf("the standing reads %+v", view)
	}
	branches := 0
	for _, artifact := range view.Artifacts {
		if artifact.Kind == ledger.ArtifactBranch {
			branches++
		}
	}
	if branches != 2 {
		return Violatedf("%d branches on record, want both attempts'", branches)
	}
	return nil
}

func checkEvidenceLinked(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	head := r.ID("commit")
	id, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(head, LaneCheck(criterionOne, true)))
	if err != nil {
		return err
	}
	if _, err := ops.Accept(r.Ctx, r.Scope, id, r.Actor); err != nil {
		return err
	}
	view, err := ops.Standing(r.Ctx, r.Scope, id)
	if err != nil {
		return err
	}
	if view.WorkOrder != id || view.State != workorders.Done {
		return Violatedf("the standing names %s in %s", view.WorkOrder, view.State)
	}
	var branch, report ledger.Artifact
	for _, artifact := range view.Artifacts {
		switch artifact.Kind {
		case ledger.ArtifactBranch:
			branch = artifact
		case ledger.ArtifactReport:
			report = artifact
		}
	}
	if branch.URI != branchName || branch.Digest != head || report.URI != "notes.md" {
		return Violatedf("the artifacts on record: %+v", view.Artifacts)
	}
	if len(view.Checks) != 1 {
		return Violatedf("the checks on record: %+v", view.Checks)
	}
	row := view.Checks[0]
	if row.WorkOrder != id || row.ArtifactID != branch.ID || row.Criterion != criterionOne || row.Source != workorders.SourceLane || row.Status != workorders.CheckPassed {
		return Violatedf("the check on record: %+v", row)
	}
	return nil
}

var errRolledBack = errors.New("rolled back on purpose")

// checkAcceptanceMovesOnce calls the guarded statement twice, because a racing test does not
// reliably catch a compare-and-set that went missing.
func checkAcceptanceMovesOnce(r *Rig) error {
	s := r.Store()
	id, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(r.ID("commit")))
	if err != nil {
		return err
	}
	var first, second, third bool
	err = s.Write(r.Ctx, r.Scope, "note", func(tx ports.WriteTx) error {
		var err error
		if first, err = tx.WorkOrders().Move(id, workorders.Acceptance, workorders.Done); err != nil {
			return err
		}
		if second, err = tx.WorkOrders().Move(id, workorders.Acceptance, workorders.Done); err != nil {
			return err
		}
		if third, err = tx.WorkOrders().Move(id, workorders.Acceptance, workorders.Ready); err != nil {
			return err
		}
		return errRolledBack
	})
	if !errors.Is(err, errRolledBack) {
		return err
	}
	if !first || second || third {
		return Violatedf("the statement matched %v, %v, %v", first, second, third)
	}
	return r.stateIs(s, id, workorders.Acceptance)
}

func checkAcceptanceRetry(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(r.ID("commit")))
	if err != nil {
		return err
	}
	for range 2 {
		state, err := ops.Accept(r.Ctx, r.Scope, id, r.Actor)
		if err != nil || state != workorders.Done {
			return Violatedf("an acceptance: %s: %v", state, err)
		}
	}
	count, _, err := r.eventsAbout(s, id, workorders.EventType(workorders.TriggerAcceptancePassed))
	if err != nil {
		return err
	}
	if count != 1 {
		return Violatedf("%d acceptances on record", count)
	}
	return nil
}

func checkReworkReady(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(r.ID("commit")))
	if err != nil {
		return err
	}
	question, err := ops.RecordQuestion(r.Ctx, r.Scope, questions.Question{
		ID: r.ID("q"), Text: "Which budget holds?", Blocking: true, SubjectKind: workorders.SubjectKind, SubjectID: id,
	}, r.Actor)
	if err != nil {
		return err
	}
	if state, err := ops.Rework(r.Ctx, r.Scope, id, reworkReason, r.Actor); err != nil || state != workorders.ContractReview {
		return Violatedf("a rework past an open blocking question: %s: %v", state, err)
	}
	if err := r.stateIs(s, id, workorders.ContractReview); err != nil {
		return err
	}
	if err := ops.Dismiss(r.Ctx, r.Scope, question.ID, r.Actor); err != nil {
		return err
	}
	if _, err := ops.Promote(r.Ctx, r.Scope, r.tick()); err != nil {
		return err
	}
	return r.stateIs(s, id, workorders.Ready)
}

func checkForgeUnreachable(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(r.ID("commit"), LaneCheck(criterionOne, false)))
	if err != nil {
		return err
	}
	away := errors.New("the forge is away")
	if _, err := ops.Settle(r.Ctx, r.Scope, &fake.Forge{Fail: away}, r.tick()); !errors.Is(err, away) {
		return Violatedf("a tick hid the failure: %v", err)
	}
	return r.stateIs(s, id, workorders.Acceptance)
}

func checkForgeRerun(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	head := r.ID("commit")
	id, err := r.Awaiting(s, []string{criterionOne, criterionTwo}, LaneFinished(head))
	if err != nil {
		return err
	}
	forge := &fake.Forge{}
	r.forgeSays(forge, head, forgeCheck(criterionOne, workorders.CheckPassed))
	for range 3 {
		if moved, err := ops.Settle(r.Ctx, r.Scope, forge, r.tick()); err != nil || len(moved) != 0 {
			return Violatedf("a tick moved %v: %v", moved, err)
		}
	}
	count, _, err := r.eventsAbout(s, id, workorders.EventEvidenceRecorded)
	if err != nil {
		return err
	}
	if count != 1 {
		return Violatedf("%d events for one result", count)
	}
	if err := ops.Rebuild(r.Ctx, r.Scope, r.Actor); err != nil {
		return Violatedf("a recorded result stopped the log replaying: %v", err)
	}
	r.forgeSays(forge, head, forgeCheck(criterionOne, workorders.CheckFailed))
	if _, err := ops.Settle(r.Ctx, r.Scope, forge, r.tick()); err != nil {
		return err
	}
	return r.stateIs(s, id, workorders.Ready)
}

// racingForge runs a step while a tick waits for its answer, which is the one moment a later
// attempt can start between the tick's read and its write.
type racingForge struct {
	ports.Forge
	during func() error
}

func (f racingForge) Checks(ctx context.Context, scope ledger.WorkspaceID, request ports.ForgeRequest) ([]ports.ForgeCheck, error) {
	if err := f.during(); err != nil {
		return nil, err
	}
	return f.Forge.Checks(ctx, scope, request)
}

func checkForgeKeepsItsAttempt(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	first := r.ID("commit")
	id, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(first))
	if err != nil {
		return err
	}
	forge := &fake.Forge{}
	r.forgeSays(forge, first, forgeCheck(criterionOne, workorders.CheckPassed))
	once := false
	racing := racingForge{Forge: forge, during: func() error {
		if once {
			return nil
		}
		once = true
		if _, err := ops.Rework(r.Ctx, r.Scope, id, reworkReason, r.Actor); err != nil {
			return err
		}
		return r.anotherAttempt(s, id, LaneFinished(r.ID("commit")))
	}}
	if moved, err := ops.Settle(r.Ctx, r.Scope, racing, r.tick()); err != nil || len(moved) != 0 {
		return Violatedf("a tick moved %v: %v", moved, err)
	}
	if err := r.stateIs(s, id, workorders.Acceptance); err != nil {
		return err
	}
	view, err := ops.Standing(r.Ctx, r.Scope, id)
	if err != nil {
		return err
	}
	if len(view.Checks) != 0 {
		return Violatedf("the first commit's result reached the second attempt: %+v", view.Checks)
	}
	return nil
}

func checkAcceptanceReplays(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	accepted, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(r.ID("commit")))
	if err != nil {
		return err
	}
	reworked, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(r.ID("commit")))
	if err != nil {
		return err
	}
	if _, err := ops.Accept(r.Ctx, r.Scope, accepted, r.Actor); err != nil {
		return err
	}
	if _, err := ops.Rework(r.Ctx, r.Scope, reworked, reworkReason, r.Actor); err != nil {
		return err
	}
	if err := ops.Rebuild(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	for id, want := range map[string]workorders.State{accepted: workorders.Done, reworked: workorders.Ready} {
		if err := r.stateIs(s, id, want); err != nil {
			return fmt.Errorf("after a rebuild: %w", err)
		}
	}
	return nil
}
