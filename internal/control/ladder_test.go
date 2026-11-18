package control_test

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// TestEscalationStoresTheDecisionsInForceItsScopeOverlaps holds that a question escalated to a person keeps the
// decisions in force whose scope overlaps the question's, by ID, and none for a ledger with no decision in force,
// a decision about other work, or a decision that ended.
func TestEscalationStoresTheDecisionsInForceItsScopeOverlaps(t *testing.T) {
	type decision struct {
		id, scope string
		status    questions.DecisionStatus
	}
	decided := questions.DecisionDecided
	tests := []struct {
		name      string
		decisions []decision
		want      []string
	}{
		{"no decision in force", nil, nil},
		{"a decision about other work", []decision{{"d-other", "plan:other", decided}}, nil},
		{"a decision about the same plan", []decision{{"d-same", "plan:checkout", decided}, {"d-other", "plan:other", decided}}, []string{"d-same"}},
		{"two decisions about the same plan, in order of ID", []decision{{"d-b", "plan:checkout", decided}, {"d-a", "plan:checkout", decided}}, []string{"d-a", "d-b"}},
		{"a decision that a later one replaced", []decision{{"d-old", "plan:checkout", questions.DecisionReplaced}, {"d-new", "plan:checkout", decided}}, []string{"d-new"}},
		{"a decision written before the scope grammar", []decision{{"d-legacy", "checkout", decided}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			store, err := fake.NewStore()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if err := store.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
			if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: "w"}, member); err != nil {
				t.Fatal(err)
			}
			ops := control.NewQuestions(store, questions.BatchPolicy{})
			// The decisions go in through the store, as a ledger holds them, so a scope needs no record behind it.
			if err := store.Write(ctx, "w", "test.decisions", func(tx ports.WriteTx) error {
				for _, row := range tt.decisions {
					record := questions.Decision{
						ID: row.id, Status: row.status, Statement: "Statement of " + row.id, Rationale: "It follows.", Scope: row.scope,
					}
					if err := tx.Questions().InsertDecision(record); err != nil {
						return err
					}
					event, err := questions.RecordEvent(questions.EventDecisionRecorded, questions.SubjectDecision, record.ID, record, member)
					if err != nil {
						return err
					}
					if _, err := tx.Append(event); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			lane := ledger.Actor{Kind: ledger.ActorLane, ID: "lane-a"}
			holder := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "orchestrator-a"}
			asked, err := ops.Ask(ctx, "w", questions.Ask{
				ID: "q-a", Text: "Which logo set should the site use?", Categories: []string{questions.CategoryProduct},
				Lane: lane.ID, Generation: 1, SubjectKind: planning.SubjectPlan, SubjectID: "checkout",
			}, lane)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ops.Take(ctx, "w", asked.QuestionID, 60_000, holder); err != nil {
				t.Fatal(err)
			}
			for _, rung := range []int64{questions.RungLookup, questions.RungOpen} {
				if _, err := ops.Climb(ctx, "w", asked.QuestionID, questions.Step{Rung: rung, Outcome: "no match"}, holder); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ops.Escalate(ctx, "w", asked.QuestionID, questions.Escalation{
				Reason: questions.ReasonRequirementConflict, Options: []string{"Monogram", "Wordmark"}, Recommendation: "Wordmark",
				RecommendationRationale: "It reads at small sizes.",
			}, holder); err != nil {
				t.Fatal(err)
			}

			var read questions.Question
			if err := store.Read(ctx, "w", func(tx ports.ReadTx) error {
				var err error
				read, err = tx.Questions().Question(asked.QuestionID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(read.Overlaps, tt.want) {
				t.Errorf("the escalated question keeps the overlaps %q, want %q", read.Overlaps, tt.want)
			}
		})
	}
}
