package contract

import (
	"context"
	"testing"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
)

// MatcherRig is one matcher and the way a check gives it records.
type MatcherRig struct {
	Matcher ports.Matcher
	Scope   ledger.WorkspaceID
	// Add holds one record of a kind.
	Add func(kind, id, text string)
	// Other holds one record in another workspace, and it is nil on a matcher with one.
	Other func(kind, id, text string)
}

// RunMatcher runs one contract on one matcher. The adapter and its fake both pass it, so a
// fake cannot drift from the matcher it stands for.
func RunMatcher(t *testing.T, fresh func(*testing.T) MatcherRig) {
	t.Helper()
	checks := []struct {
		name string
		run  func(*testing.T, context.Context, MatcherRig)
	}{
		{"the record that shares the most terms comes first", checkMatcherRanks},
		{"only the kinds asked for are candidates", checkMatcherKinds},
		{"a limit bounds the candidates", checkMatcherLimit},
		{"nothing to match returns nothing", checkMatcherNothing},
		{"another workspace's records are no candidates", checkMatcherScope},
		{"marks in a question are not operators", checkMatcherMarks},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			check.run(t, t.Context(), fresh(t))
		})
	}
}

func matched(t *testing.T, ctx context.Context, rig MatcherRig, request ports.MatchRequest) []string {
	t.Helper()
	candidates, err := rig.Matcher.Candidates(ctx, rig.Scope, request)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(candidates))
	for i, candidate := range candidates {
		if i > 0 && candidate.Score > candidates[i-1].Score {
			t.Errorf("scores rise: %+v", candidates)
		}
		ids = append(ids, candidate.RecordID)
	}
	return ids
}

func checkMatcherRanks(t *testing.T, ctx context.Context, rig MatcherRig) {
	rig.Add(questions.KindDecision, "weak", "the retry count is fixed at build time")
	rig.Add(questions.KindDecision, "strong", "the retry budget limits each checkout call")
	rig.Add(questions.KindDecision, "none", "the color of the button stays blue")
	got := matched(t, ctx, rig, ports.MatchRequest{Text: "What is the retry budget for checkout?", Kinds: questions.LookupKinds()})
	if len(got) < 2 || got[0] != "strong" {
		t.Errorf("candidates %v", got)
	}
}

func checkMatcherKinds(t *testing.T, ctx context.Context, rig MatcherRig) {
	rig.Add(questions.KindDecision, "a-decision", "retry budget")
	rig.Add(questions.KindRequirement, "a-requirement", "retry budget")
	rig.Add(questions.KindContract, "a-contract", "retry budget")
	rig.Add(questions.KindQuestion, "a-question", "retry budget")
	got := matched(t, ctx, rig, ports.MatchRequest{Text: "retry budget", Kinds: questions.OpenKinds()})
	if len(got) != 1 || got[0] != "a-question" {
		t.Errorf("rung 2 read %v", got)
	}
	if got := matched(t, ctx, rig, ports.MatchRequest{Text: "retry budget", Kinds: questions.LookupKinds()}); len(got) != 3 {
		t.Errorf("rung 1 read %v", got)
	}
}

func checkMatcherLimit(t *testing.T, ctx context.Context, rig MatcherRig) {
	for _, id := range []string{"a", "b", "c"} {
		rig.Add(questions.KindDecision, id, "retry budget")
	}
	if got := matched(t, ctx, rig, ports.MatchRequest{Text: "retry", Kinds: questions.LookupKinds(), Limit: 2}); len(got) != 2 {
		t.Errorf("candidates %v", got)
	}
}

func checkMatcherNothing(t *testing.T, ctx context.Context, rig MatcherRig) {
	rig.Add(questions.KindDecision, "a", "retry budget")
	for _, text := range []string{"", " ?! ", "unrelated words"} {
		if got := matched(t, ctx, rig, ports.MatchRequest{Text: text, Kinds: questions.LookupKinds()}); len(got) != 0 {
			t.Errorf("%q matched %v", text, got)
		}
	}
}

func checkMatcherScope(t *testing.T, ctx context.Context, rig MatcherRig) {
	if rig.Other == nil {
		t.Skip("this matcher holds one workspace")
	}
	rig.Other(questions.KindDecision, "theirs", "retry budget")
	if got := matched(t, ctx, rig, ports.MatchRequest{Text: "retry budget", Kinds: questions.LookupKinds()}); len(got) != 0 {
		t.Errorf("candidates %v", got)
	}
}

func checkMatcherMarks(t *testing.T, ctx context.Context, rig MatcherRig) {
	rig.Add(questions.KindDecision, "a", "retry budget")
	for _, text := range []string{`"retry`, `retry*`, `retry AND (budget`, `budget NOT`, `col:retry`, `retry' OR '1`} {
		if got := matched(t, ctx, rig, ports.MatchRequest{Text: text, Kinds: questions.LookupKinds()}); len(got) != 1 {
			t.Errorf("%q matched %v", text, got)
		}
	}
}
