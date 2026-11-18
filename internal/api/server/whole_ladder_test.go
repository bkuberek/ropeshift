package server_test

import (
	"context"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// ladderWorld is a world whose questions stand at each point of the ladder: one the world's orchestrator holds on
// rung 2, one a member answered, one a member merged into the held one, one dismissed, one about the work order a
// lane runs that another lane asked, and one escalated into a batch.
type ladderWorld struct {
	d                                                   *apitest.Daemon
	held, answered, merged, dismissed, about, escalated string
	decision                                            string
}

func aLadderWorld(t *testing.T) ladderWorld {
	t.Helper()
	ctx := t.Context()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	w.Running("wo-ask", "Add the retry budget")
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: apitest.WorldWorker}
	ask := func(id, text, lane string, change func(*questions.Ask)) string {
		t.Helper()
		draft := questions.Ask{ID: id, Text: text, Categories: []string{questions.CategoryProduct}, Lane: lane, Generation: 1}
		if change != nil {
			change(&draft)
		}
		asked, err := w.Questions.Ask(ctx, apitest.Scope, draft, ledger.Actor{Kind: ledger.ActorLane, ID: lane})
		if err != nil {
			t.Fatal(err)
		}
		return asked.QuestionID
	}
	world := ladderWorld{d: d, decision: "decision-answer"}
	world.held = ask("q-held", "What is the retry budget?", "lane-wo-ask", nil)
	if _, err := w.Questions.Take(ctx, apitest.Scope, world.held, apitest.LeaseTTL, orchestrator); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Questions.Climb(ctx, apitest.Scope, world.held, questions.Step{Rung: questions.RungLookup, Outcome: "no match"}, orchestrator); err != nil {
		t.Fatal(err)
	}
	world.answered = ask("q-answered", "Which log level do retries use?", "lane-other", nil)
	if _, err := w.Questions.Answer(ctx, apitest.Scope, world.answered, questions.Answer{
		DecisionID: world.decision, Statement: "Warn.", Rationale: "Retries are worth a look.",
	}, member); err != nil {
		t.Fatal(err)
	}
	world.merged = ask("q-merged", "How many retries may a call make?", "lane-wo-ask", nil)
	if err := w.Questions.MergeQuestion(ctx, apitest.Scope, world.merged, world.held, member); err != nil {
		t.Fatal(err)
	}
	world.dismissed = ask("q-dismissed", "Is the retry budget still needed?", "lane-other", nil)
	if err := w.Questions.Dismiss(ctx, apitest.Scope, world.dismissed, member); err != nil {
		t.Fatal(err)
	}
	world.about = ask("q-about", "Does the budget cover the archive?", "lane-other", func(a *questions.Ask) {
		a.SubjectKind, a.SubjectID = workorders.SubjectKind, "wo-ask"
	})
	world.escalated = w.Escalated("Should the budget hold across restarts?", []string{questions.CategoryProduct}, false)
	return world
}

func (l ladderWorld) read(t *testing.T, filter api.QuestionFilter) map[string]api.QuestionView {
	t.Helper()
	view, err := l.d.Client.ReadQuestions(context.Background(), filter)
	if err != nil {
		t.Fatalf("questions %+v: %v", filter, err)
	}
	out := map[string]api.QuestionView{}
	for _, row := range view.Questions {
		out[row.ID] = row
	}
	return out
}

func idsOf(rows map[string]api.QuestionView) []string {
	out := make([]string, 0, len(rows))
	for id := range rows {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// sorted is a list of IDs in the order idsOf answers them.
func sorted(ids ...string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

// The questions view names who holds a question by orchestrator and machine, every lane attached to it, the batch
// that gathered it, the machine of each orchestrator its trace names, and nobody once no hold lasts (FR-QD-13).
func TestTheQuestionsViewShowsTheWholeLadder(t *testing.T) {
	t.Parallel()
	world := aLadderWorld(t)
	open := world.read(t, api.QuestionFilter{})
	if got, want := idsOf(open), sorted(world.about, world.escalated, world.held); !slices.Equal(got, want) {
		t.Fatalf("the open questions are %v, want %v", got, want)
	}
	held := open[world.held]
	want := api.QuestionHolder{ID: apitest.WorldWorker, Orchestrator: ledger.OrchestratorOf(apitest.WorldWorker), Machine: "solo"}
	if held.Holder == nil || held.Holder.Until == 0 {
		t.Fatalf("the held question names no holder: %+v", held.Holder)
	}
	if got := *held.Holder; got.ID != want.ID || got.Orchestrator != want.Orchestrator || got.Machine != want.Machine {
		t.Errorf("the holder reads %+v, want %+v", got, want)
	}
	if held.Machines[apitest.WorldWorker] != "solo" || len(held.LadderTrace) != 1 || held.LadderTrace[0].By != apitest.WorldWorker {
		t.Errorf("the trace reads %+v with machines %v, want its rung worked by the orchestrator on solo", held.LadderTrace, held.Machines)
	}
	lanes := []string{}
	for _, attachment := range held.Attached {
		lanes = append(lanes, attachment.Lane)
	}
	if !slices.Equal(lanes, []string{"lane-wo-ask"}) || held.Batch != nil {
		t.Errorf("the held question has lanes %v and batch %+v, want lane-wo-ask and no batch", lanes, held.Batch)
	}
	escalated := open[world.escalated]
	if escalated.Holder != nil || escalated.Batch == nil || escalated.Batch.ID != escalated.BatchID {
		t.Errorf("the escalated question reads holder %+v and batch %+v, want nobody holding it and its batch %s", escalated.Holder, escalated.Batch, escalated.BatchID)
	}
}

// A status filter reads the questions closed each way, each with what closed it: the decision that answered it, or
// the question that took its place, and a merged question reads as merged and not as dismissed.
func TestTheQuestionsViewReadsClosedQuestionsByStatus(t *testing.T) {
	t.Parallel()
	world := aLadderWorld(t)
	tests := []struct {
		status string
		want   []string
	}{
		{string(questions.StatusAnswered), []string{world.answered}},
		{api.QuestionStatusMerged, []string{world.merged}},
		{string(questions.StatusDismissed), []string{world.dismissed}},
		{api.QuestionStatusAll, sorted(world.about, world.answered, world.dismissed, world.escalated, world.held, world.merged)},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := world.read(t, api.QuestionFilter{Status: tt.status})
			if ids := idsOf(got); !slices.Equal(ids, tt.want) {
				t.Fatalf("status %s reads %v, want %v", tt.status, ids, tt.want)
			}
		})
	}
	every := world.read(t, api.QuestionFilter{Status: api.QuestionStatusAll})
	answered := every[world.answered]
	if answered.ClosedBy == nil || answered.ClosedBy.ID != world.decision || answered.ClosedBy.Statement != "Warn." || answered.ClosedBy.Rationale == "" {
		t.Errorf("the answered question is closed by %+v, want %s with its statement and rationale", answered.ClosedBy, world.decision)
	}
	if merged := every[world.merged]; merged.MergedInto != world.held || merged.ClosedBy != nil {
		t.Errorf("the merged question reads into %q, closed by %+v, want into %s", merged.MergedInto, merged.ClosedBy, world.held)
	}
	if dismissed := every[world.dismissed]; dismissed.MergedInto != "" || dismissed.ClosedBy != nil || dismissed.Holder != nil {
		t.Errorf("the dismissed question reads %+v", dismissed)
	}
}

// The lane filter keeps the questions a lane asked or is attached to, and the work order filter adds those about the
// work order, whatever lane asked them, so the lane's page and the work order's page each list theirs.
func TestTheQuestionsViewReadsALanesAndAWorkOrdersQuestions(t *testing.T) {
	t.Parallel()
	world := aLadderWorld(t)
	tests := []struct {
		name   string
		filter api.QuestionFilter
		want   []string
	}{
		{"a lane's open questions", api.QuestionFilter{Lane: "lane-wo-ask"}, []string{world.held}},
		{"every question of a lane", api.QuestionFilter{Lane: "lane-wo-ask", Status: api.QuestionStatusAll}, sorted(world.held, world.merged)},
		{"every question of a work order", api.QuestionFilter{WorkOrder: "wo-ask", Status: api.QuestionStatusAll}, sorted(world.about, world.held, world.merged)},
		{"a lane that asked nothing", api.QuestionFilter{Lane: "lane-none", Status: api.QuestionStatusAll}, []string{}},
		{"a work order nothing is about", api.QuestionFilter{WorkOrder: "wo-none", Status: api.QuestionStatusAll}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := idsOf(world.read(t, tt.filter)); !slices.Equal(got, tt.want) {
				t.Errorf("%+v reads %v, want %v", tt.filter, got, tt.want)
			}
		})
	}
}
