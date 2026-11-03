package contract

import (
	"slices"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/measures"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
)

// ExperienceChecks hold experience records to what FR-EXP-1 asks: written once, with the settings
// FR-EXP-2 names, and changed afterwards only by their one outcome fill.
var ExperienceChecks = []Check{
	{"X1", "an experience record changes only by its one outcome fill", checkExperienceImmutable},
	{"X2", "a decision's record holds the settings of the session that decided, and its work's size", checkDecisionSettings},
	{"X3", "every experience record and outcome agrees with its log", checkExperienceReplay},
}

// writeExperience inserts a record with the event a replay reads, as the use cases do.
func (r *Rig) writeExperience(tx ports.WriteTx, record measures.Experience) error {
	if err := tx.Measures().InsertExperience(record); err != nil {
		return err
	}
	event, err := questions.RecordEvent(questions.EventExperienceRecorded, record.SubjectKind, record.SubjectID, record, r.Actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// checkExperienceImmutable writes a record, tries to write it again, and fills its outcome twice: the
// second write and the second fill change nothing, and nothing but the fill ever does.
func checkExperienceImmutable(r *Rig) error {
	s := r.Store()
	settings := measures.Settings{Driver: "claude-code", ModelResolved: "model-a", Effort: "high", WorkOrderSize: "medium",
		Context: measures.ContextSettings{Role: "writer", HandoffTokens: 180000}}
	record := measures.Experience{ID: r.ID("x"), SubjectKind: measures.SubjectLane, SubjectID: r.ID("lane"), PolicyVersion: 3, Settings: settings, Chosen: "change"}
	if err := s.Write(r.Ctx, r.Scope, "test.experience", func(tx ports.WriteTx) error { return r.writeExperience(tx, record) }); err != nil {
		return err
	}
	again := record
	again.ID, again.Settings.ModelResolved, again.Chosen, again.PolicyVersion = r.ID("x"), "model-b", "other", 9
	if err := s.Write(r.Ctx, r.Scope, "test.experience", func(tx ports.WriteTx) error { return r.writeExperience(tx, again) }); err == nil {
		return Violatedf("a second record of one subject was written")
	}
	fills := []measures.Outcome{{Status: "completed", CostUSD: 0.5}, {Status: "failed", Reason: "a second word"}}
	for i, outcome := range fills {
		var filled bool
		err := s.Write(r.Ctx, r.Scope, "test.experience", func(tx ports.WriteTx) error {
			var err error
			if filled, err = tx.Measures().FillOutcome(record.SubjectKind, record.SubjectID, outcome, tx.Now()); err != nil || !filled {
				return err
			}
			event, err := questions.RecordEvent(questions.EventOutcomeFilled, record.SubjectKind, record.SubjectID, outcome, r.Actor)
			if err != nil {
				return err
			}
			_, err = tx.Append(event)
			return err
		})
		if err != nil {
			return err
		}
		if filled != (i == 0) {
			return Violatedf("fill %d reported %v", i+1, filled)
		}
	}
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		got, err := tx.Measures().Experience(record.SubjectKind, record.SubjectID)
		if err != nil {
			return err
		}
		if got.ID != record.ID || got.Settings != settings || got.Chosen != record.Chosen || got.PolicyVersion != record.PolicyVersion {
			return Violatedf("the record changed: %+v", got)
		}
		if got.Outcome == nil || got.Outcome.Status != "completed" || got.Outcome.CostUSD != 0.5 || got.OutcomeObservedAt == 0 {
			return Violatedf("the outcome is %+v, want the first fill", got.Outcome)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return r.CheckExperiences(s)
}

// checkDecisionSettings records a decision as an orchestrator whose run's record names its settings,
// on a question about a work order of a known size, and reads both into the decision's record.
func checkDecisionSettings(r *Rig) error {
	s := r.Store()
	worker := r.ID("worker")
	orchestrator := ledger.Orchestrator{ID: ledger.OrchestratorOf(worker), WorkerID: worker, Desired: ledger.OrchestratorRunning, Run: 1}
	run := measures.Settings{Driver: "claude-code", ModelRequested: "opus", ModelResolved: "model-o", Effort: "high",
		Context: measures.ContextSettings{Role: "orchestrator", WarnTokens: 60000, CompactTokens: 80000, HandoffTokens: 100000}, WorkOrderSize: measures.SizeUnknown}
	err := s.Write(r.Ctx, r.Scope, "test.orchestrator", func(tx ports.WriteTx) error {
		if err := tx.Workers().SaveOrchestrator(orchestrator); err != nil {
			return err
		}
		return r.writeExperience(tx, measures.Experience{ID: r.ID("x"), SubjectKind: measures.SubjectLane, SubjectID: orchestrator.Lane(), Settings: run, Chosen: "orchestrate"})
	})
	if err != nil {
		return err
	}
	decided, err := r.NewOps(s).RecordDecision(r.Ctx, r.Scope, questions.Decision{
		ID: r.ID("d"), Status: questions.DecisionAssumed, Statement: "Retry twice", Rationale: "It follows.", Citations: []string{"PAY-12"},
		Scope: "retries", Reversibility: questions.Reversible, Categories: []string{questions.CategoryProduct},
	}, ledger.Actor{Kind: ledger.ActorOrchestrator, ID: worker})
	if err != nil {
		return err
	}
	byMember, err := r.decision(s, "Keep the budget", nil)
	if err != nil {
		return err
	}
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		record, err := tx.Measures().Experience(measures.SubjectDecision, decided.ID)
		if err != nil {
			return err
		}
		want := run
		want.WorkOrderSize = measures.SizeUnknown
		if record.Settings != want {
			return Violatedf("an orchestrator's decision holds %+v, want %+v", record.Settings, want)
		}
		person, err := tx.Measures().Experience(measures.SubjectDecision, byMember.ID)
		if err != nil {
			return err
		}
		if person.Settings != (measures.Settings{WorkOrderSize: measures.SizeUnknown}) {
			return Violatedf("a person's decision holds settings %+v", person.Settings)
		}
		return nil
	})
}

// checkExperienceReplay runs a decision through its overturn, then holds every record to its log.
func checkExperienceReplay(r *Rig) error {
	s := r.Store()
	old, err := r.decision(s, "Retry with a new key", nil)
	if err != nil {
		return err
	}
	if _, err := r.NewOps(s).Overturn(r.Ctx, r.Scope, old.ID, questions.Overturn{DecisionID: r.ID("d"), Statement: "Retry with the same key"}, r.Actor); err != nil {
		return err
	}
	return r.CheckExperiences(s)
}

// CheckExperiences compares every experience record with what the log replays to: the record as it
// was written, and the outcome it gained.
func (r *Rig) CheckExperiences(s ports.Store) error {
	book, err := r.questionBook(s)
	if err != nil {
		return err
	}
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		records, err := tx.Measures().Experiences()
		if err != nil {
			return err
		}
		if len(records) != len(book.Experiences) {
			return Violatedf("%d experience records, the log replays to %d", len(records), len(book.Experiences))
		}
		for _, record := range records {
			logged, ok := book.Experiences[questions.ExperienceKey(record.SubjectKind, record.SubjectID)]
			if !ok || logged.ID != record.ID || logged.Settings != record.Settings || logged.Chosen != record.Chosen ||
				logged.PolicyVersion != record.PolicyVersion || !slices.Equal(logged.OptionsConsidered, record.OptionsConsidered) {
				return Violatedf("experience %+v disagrees with its log %+v", record, logged)
			}
			outcome, filled := book.Outcomes[record.SubjectID]
			switch {
			case filled != (record.Outcome != nil):
				return Violatedf("experience %s has outcome %+v, the log %v", record.SubjectID, record.Outcome, filled)
			case filled && (outcome.Status != record.Outcome.Status || outcome.CostUSD != record.Outcome.CostUSD ||
				!slices.Equal(outcome.ReworkWorkOrders, record.Outcome.ReworkWorkOrders)):
				return Violatedf("experience %s has outcome %+v, the log %+v", record.SubjectID, record.Outcome, outcome)
			}
		}
		return nil
	})
}
