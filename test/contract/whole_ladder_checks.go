package contract

import (
	"errors"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// WholeLadderChecks hold FR-QD-13: each rung of a question's trace names who worked it and the judgment it made, and a
// member takes over a question an orchestrator holds, on any rung, so the orchestrator never judges it again.
var WholeLadderChecks = []Check{
	{"WL1", "a rung an orchestrator climbs names it, whatever its step says of who worked it or what it decided", checkLadderStepNamesHolder},
	{"WL2", "a rung that decides or assumes names its decision, with the decision's rationale and citations", checkLadderStepNamesRuling},
	{"WL3", "a rung 1 answer from the record names the decision it found", checkLadderStepNamesResolution},
	{"WL4", "a merge names who merged the question, an orchestrator or a member", checkLadderStepNamesMerger},
	{"WL5", "a member's answer to a question an orchestrator holds, on any rung, closes it, ends the hold, and refuses the orchestrator's next act as resolved", checkTakeoverByAnswer},
	{"WL6", "a member's assumption of a question an orchestrator holds, on any rung, closes it, ends the hold, and refuses the orchestrator's next act as resolved", checkTakeoverByAssumption},
	{"WL7", "a member's merge of a question an orchestrator holds, on any rung, closes it, ends the hold, and refuses the orchestrator's next act as resolved", checkTakeoverByMerge},
}

// heldRungs are the rungs an orchestrator holds a question on while it works it. Rung 5 is a person's.
var heldRungs = []int64{questions.RungLookup, questions.RungOpen, questions.RungDecide, questions.RungAssume}

// heldOn asks a question and has one orchestrator take it and climb it to a rung, so it holds the question there.
func (r *Rig) heldOn(s ports.Store, text string, rung int64, holder ledger.Actor) (string, error) {
	ops := r.NewOps(s)
	asked, err := r.ask(s, text, r.ID("lane"), nil)
	if err != nil {
		return "", err
	}
	if _, err := ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, holder); err != nil {
		return "", err
	}
	for step := questions.RungLookup; step < rung; step++ {
		if _, err := ops.Climb(r.Ctx, r.Scope, asked.QuestionID, questions.Step{Rung: step, Outcome: "no match"}, holder); err != nil {
			return "", err
		}
	}
	row, err := r.readQuestion(s, asked.QuestionID)
	if err != nil {
		return "", err
	}
	if row.Rung != rung || row.ClaimedBy != holder.ID {
		return "", fmt.Errorf("a question climbed to rung %d reads rung %d held by %q", rung, row.Rung, row.ClaimedBy)
	}
	return asked.QuestionID, nil
}

// lastStep reads the newest rung of a question's trace.
func (r *Rig) lastStep(s ports.Store, id string) (questions.Step, error) {
	row, err := r.readQuestion(s, id)
	if err != nil {
		return questions.Step{}, err
	}
	if len(row.LadderTrace) == 0 {
		return questions.Step{}, Violatedf("question %s has no ladder trace", id)
	}
	return row.LadderTrace[len(row.LadderTrace)-1], nil
}

func checkLadderStepNamesHolder(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	holder := r.orchestrator(RungHolder)
	asked, err := r.ask(s, "What is the retry budget?", "lane-a", nil)
	if err != nil {
		return err
	}
	if _, err := ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, holder); err != nil {
		return err
	}
	forged := questions.Step{Rung: questions.RungLookup, Candidates: []string{"decision:d1"}, Outcome: "no match", By: "someone-else", Decision: "d-forged", Rationale: "It says so."}
	if _, err := ops.Climb(r.Ctx, r.Scope, asked.QuestionID, forged, holder); err != nil {
		return err
	}
	step, err := r.lastStep(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if step.By != holder.ID || step.Decision != "" || step.Rationale != "" || !slices.Equal(step.Candidates, forged.Candidates) || step.Outcome != forged.Outcome {
		return Violatedf("a climbed rung reads %+v, want it worked by %s with what it considered and nothing decided", step, holder.ID)
	}
	return nil
}

func checkLadderStepNamesRuling(r *Rig) error {
	for _, status := range []questions.DecisionStatus{questions.DecisionDecided, questions.DecisionAssumed} {
		s := r.Store()
		rung := questions.RungDecide
		if status == questions.DecisionAssumed {
			rung = questions.RungAssume
		}
		id, holder, err := r.OnRung(s, fmt.Sprintf("What is the retry budget once it is %s?", status), rung, nil)
		if err != nil {
			return err
		}
		draft := ruling(r.ID("d"), status)
		// Each ruling binds a scope of its own, so the second overlaps nothing the first recorded.
		draft.Scope = "repo:" + r.ID("s")
		if err := r.holdScopedRepository(s, draft.Scope); err != nil {
			return err
		}
		recorded, err := r.NewOps(s).Rule(r.Ctx, r.Scope, id, draft, holder)
		if err != nil {
			return err
		}
		step, err := r.lastStep(s, id)
		if err != nil {
			return err
		}
		if step.Rung != rung || step.By != holder.ID || step.Decision != recorded.ID || step.Rationale != draft.Rationale || !slices.Equal(step.Candidates, draft.Citations) || step.Outcome != string(status) {
			return Violatedf("a %s rung reads %+v, want rung %d by %s naming %s with its rationale and citations", status, step, rung, holder.ID, recorded.ID)
		}
	}
	return nil
}

func checkLadderStepNamesResolution(r *Rig) error {
	s := r.Store()
	asked, err := r.ask(s, "What is the retry budget?", "lane-a", nil)
	if err != nil {
		return err
	}
	scope := "repo:" + r.ID("s")
	if err := r.holdScopedRepository(s, scope); err != nil {
		return err
	}
	decided, err := r.NewOps(s).RecordDecision(r.Ctx, r.Scope, questions.Decision{
		ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Three calls.", Rationale: "It follows.",
		Citations: []string{"PAY-12"}, Scope: scope, Reversibility: questions.Reversible,
	}, r.Actor)
	if err != nil {
		return err
	}
	if err := r.resolvedBy(s, asked.QuestionID, decided.ID); err != nil {
		return err
	}
	step, err := r.lastStep(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if step.Rung != questions.RungLookup || step.By != "resolver" || step.Decision != decided.ID || step.Outcome != questions.OutcomeAnswered {
		return Violatedf("a rung 1 answer from the record reads %+v, want rung 1 by resolver naming %s", step, decided.ID)
	}
	return nil
}

func checkLadderStepNamesMerger(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	holder := r.orchestrator(RungHolder)
	stays, err := r.ask(s, "What is the retry budget?", "lane-a", nil)
	if err != nil {
		return err
	}
	for name, merger := range map[string]ledger.Actor{"an orchestrator": holder, "a member": r.Actor} {
		id, err := r.heldOn(s, "How many retries may a call make, by "+name+"?", questions.RungOpen, holder)
		if err != nil {
			return err
		}
		if merger.Kind == ledger.ActorOrchestrator {
			err = ops.Merge(r.Ctx, r.Scope, id, stays.QuestionID, merger)
		} else {
			err = ops.MergeQuestion(r.Ctx, r.Scope, id, stays.QuestionID, merger)
		}
		if err != nil {
			return err
		}
		step, err := r.lastStep(s, id)
		if err != nil {
			return err
		}
		if step.Outcome != questions.OutcomeMerged || step.By != merger.ID || !slices.Equal(step.Candidates, []string{stays.QuestionID}) {
			return Violatedf("a question %s merged reads %+v, want it merged into %s by %s", name, step, stays.QuestionID, merger.ID)
		}
	}
	return nil
}

func checkTakeoverByAnswer(r *Rig) error {
	return r.takenOver(func(s ports.Store, id string) error {
		_, err := r.answer(s, id, "Three calls.")
		return err
	})
}

func checkTakeoverByAssumption(r *Rig) error {
	return r.takenOver(func(s ports.Store, id string) error {
		_, err := r.NewOps(s).Answer(r.Ctx, r.Scope, id, questions.Answer{
			DecisionID: r.ID("d"), Statement: "Three calls.", Rationale: "Nobody objected.", Assumed: true,
		}, r.Actor)
		return err
	})
}

func checkTakeoverByMerge(r *Rig) error {
	return r.takenOver(func(s ports.Store, id string) error {
		stays, err := r.ask(s, "Which retry budget does the gateway take?", r.ID("lane"), nil)
		if err != nil {
			return err
		}
		return r.NewOps(s).MergeQuestion(r.Ctx, r.Scope, id, stays.QuestionID, r.Actor)
	})
}

// takenOver has a member take over a question an orchestrator holds on each rung it can hold one, then holds that the
// question closed, that the hold ended, and that every act the orchestrator could take next on it is refused as
// resolved and changes nothing, so it drops the question rather than judging it again.
func (r *Rig) takenOver(takeOver func(s ports.Store, id string) error) error {
	for _, rung := range heldRungs {
		s := r.Store()
		holder := r.orchestrator(RungHolder)
		// Each rung asks its own question, because an answered one would answer the next ask of the same text.
		id, err := r.heldOn(s, fmt.Sprintf("What is the retry budget of tier %d?", rung), rung, holder)
		if err != nil {
			return err
		}
		if err := takeOver(s, id); err != nil {
			return Violatedf("a member could not take over a question held on rung %d: %v", rung, err)
		}
		closed, err := r.readQuestion(s, id)
		if err != nil {
			return err
		}
		if closed.Status.Unresolved() || closed.ClaimedBy != "" || closed.ClaimUntil != 0 {
			return Violatedf("a question a member took over on rung %d reads %+v, want it closed with no hold", rung, closed)
		}
		released, err := r.NewOps(s).LetGo(r.Ctx, r.Scope, holder)
		if err != nil {
			return err
		}
		if len(released) != 0 {
			return Violatedf("the orchestrator still held %v after a member took the question over on rung %d", released, rung)
		}
		for act, err := range r.nextActs(s, id, rung, holder) {
			if !errors.Is(err, questions.ErrResolved) {
				return Violatedf("the orchestrator's %s on a question a member took over on rung %d: %v, want it refused as resolved", act, rung, err)
			}
		}
		after, err := r.readQuestion(s, id)
		if err != nil {
			return err
		}
		if after.Status != closed.Status || after.DecisionID != closed.DecisionID || len(after.LadderTrace) != len(closed.LadderTrace) || after.ClaimedBy != "" {
			return Violatedf("the orchestrator's refused acts changed the question: %+v, was %+v", after, closed)
		}
	}
	return nil
}

// nextActs runs every act an orchestrator could take next on a question it held on a rung, and names each one's error.
func (r *Rig) nextActs(s ports.Store, id string, rung int64, holder ledger.Actor) map[string]error {
	ops := r.NewOps(s)
	acts := map[string]error{}
	_, acts["take"] = ops.Take(r.Ctx, r.Scope, id, TTLMs, holder)
	_, acts["climb"] = ops.Climb(r.Ctx, r.Scope, id, questions.Step{Rung: rung, Outcome: "no match"}, holder)
	_, acts["escalation"] = ops.Escalate(r.Ctx, r.Scope, id, questions.Escalation{
		Reason: questions.ReasonScopeChange, Options: []string{"Three", "Five"}, Recommendation: "Three", RecommendationRationale: "It follows.",
	}, holder)
	_, acts["ruling"] = ops.Rule(r.Ctx, r.Scope, id, ruling(r.ID("d"), questions.DecisionDecided), holder)
	_, acts["answer from the record"] = ops.Resolve(r.Ctx, r.Scope, id, r.ID("d"), holder)
	other, err := r.ask(s, "Which timeout does a retry take?", r.ID("lane"), nil)
	if err != nil {
		acts["merge"] = err
		return acts
	}
	acts["merge"] = ops.Merge(r.Ctx, r.Scope, id, other.QuestionID, holder)
	return acts
}
