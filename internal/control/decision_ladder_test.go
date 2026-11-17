package control_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/questions"
)

// decisionRecord is a decision record file with no plan. An empty status leaves the key out.
func decisionRecord(status string) string {
	head := "+++\nkind = \"decision\"\nid = \"adr-0007\"\ntitle = \"Retry budget\"\n"
	if status != "" {
		head += "status = \"" + status + "\"\n"
	}
	return head + "+++\n\nEvery call has a retry budget of two attempts.\n"
}

// decisionLadder commits each given version of one decision record in turn, asks a question the record
// answers, and returns what the orchestrator's first rung reads for it.
func decisionLadder(t *testing.T, statuses ...string) []control.LadderCandidate {
	t.Helper()
	r := newPlaneRig(t, ledger.ProtocolVersion)
	ctx := t.Context()
	source := fake.NewWorkSource()
	for _, status := range statuses {
		source.Write("docs/decisions/0007-retry-budget.md", []byte(decisionRecord(status)))
		report, err := control.NewIntake(r.stores["pa"]).Poll(ctx, "pa", source)
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Problems) != 0 {
			t.Fatalf("intake: %+v", report.Problems)
		}
	}
	if _, err := control.NewQuestions(r.stores["pa"], questions.BatchPolicy{}).RecordQuestion(ctx, "pa", questions.Question{ID: "q-a", Text: "What is the retry budget of a call?"}, projectMember); err != nil {
		t.Fatal(err)
	}
	call := r.orchestrating()
	call.Project = "pa"
	reply, err := r.plane.Orchestrate(ctx, ledger.VerbNext, call)
	if err != nil {
		t.Fatal(err)
	}
	var read control.LadderRead
	if err := json.Unmarshal(reply.Result, &read); err != nil {
		t.Fatal(err)
	}
	if read.Question.ID != "q-a" {
		t.Fatalf("the next question %s", reply.Result)
	}
	return read.Lookup
}

// TestAnAcceptedDecisionDocumentAnswersAQuestion reads a question's first rung over an accepted decision
// record: the record is a candidate, named by its reference, with its title and body as the text.
func TestAnAcceptedDecisionDocumentAnswersAQuestion(t *testing.T) {
	found := decisionLadder(t, "accepted")
	if len(found) != 1 || found[0].Kind != questions.KindDecisionDocument || found[0].Ref != "decision:adr-0007" {
		t.Fatalf("the lookup read %+v", found)
	}
	for _, want := range []string{"Retry budget", "Every call has a retry budget of two attempts."} {
		if !strings.Contains(found[0].Text, want) {
			t.Errorf("the candidate's text %q does not say %q", found[0].Text, want)
		}
	}
}

// TestADecisionDocumentThatIsNotAcceptedIsNoCandidate holds that a record that states no status, which is
// proposed, and one that was rejected answer nothing.
func TestADecisionDocumentThatIsNotAcceptedIsNoCandidate(t *testing.T) {
	for _, status := range []string{"", "proposed", "rejected"} {
		t.Run("status "+status, func(t *testing.T) {
			if found := decisionLadder(t, status); len(found) != 0 {
				t.Errorf("the lookup read %+v", found)
			}
		})
	}
}

// TestADecisionDocumentSupersededByANewerVersionIsNoCandidate holds that a record leaves the lookup when
// its newest version leaves `accepted`, and returns to it when a later version is accepted again.
func TestADecisionDocumentSupersededByANewerVersionIsNoCandidate(t *testing.T) {
	if found := decisionLadder(t, "accepted", "superseded"); len(found) != 0 {
		t.Errorf("a superseded record: the lookup read %+v", found)
	}
	if found := decisionLadder(t, "accepted", "superseded", "accepted"); len(found) != 1 {
		t.Errorf("a record accepted again: the lookup read %+v", found)
	}
}
