package server_test

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// anEvidenceWorld is the detailed world with two changes whose lanes report when their work happened and measured
// what their branches changed. One ran, was reviewed, waited for a member, and was accepted. The other ran, and its
// review ruled, and it waits for a member. Both come before the repository says a member approves each merge, so an
// accepted one ends at once.
func anEvidenceWorld(t *testing.T) *apitest.Daemon {
	t.Helper()
	clock := apitest.NewClock()
	knowledge := fake.NewKnowledgeSource()
	apitest.Documents(knowledge)
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect, Knowledge: []ports.KnowledgeSource{knowledge}})
	w := apitest.NewWorld(t, d)
	project := apitest.Factory(w, clock)
	apitest.Populate(w, clock)
	w.At = clock.Now

	shipped := func(id, title string, head string, change ledger.ChangeSize, worked time.Duration) {
		clock.Advance(time.Minute)
		lane := w.Running(id, title)
		clock.Advance(worked)
		lane.Finish(workorders.LaneResult{
			Report: workorders.Report{
				Outcome: workorders.OutcomeCompleted,
				Checks:  []workorders.CheckResult{{Name: "go test", Passed: true, Criterion: "c1"}},
			},
			Branch: "lane/lane-" + id, Head: head, Change: &change,
		})
		clock.Advance(3 * time.Minute)
		w.Reviewed(id, workorders.CriterionVerdict{
			Criterion: "c1", Verdict: workorders.VerdictPass, Reasons: "The budget stops the retries after the third.", Citations: []string{"retry.go:21"},
		})
	}
	shipped("wo-shipped", "Add the retry budget", "fed987", ledger.ChangeSize{Files: 3, Added: 42, Removed: 7}, 8*time.Minute)
	clock.Advance(10 * time.Minute)
	if _, err := d.Client.Accept(t.Context(), "wo-shipped"); err != nil {
		t.Fatal(err)
	}
	shipped("wo-ruled", "Trim the retry log", "cab321", ledger.ChangeSize{Files: 1, Added: 4, Removed: 19}, 6*time.Minute)

	apitest.Detail(w, clock)
	project.Change()
	return d
}

// TestGoldenEvidence writes what the work order's Changes and Activity tabs read, for work that is running, work
// whose review ruled and waits for a member, work that ended with a delivery record, and work that never reached
// `ready`, from a real ledger.
func TestGoldenEvidence(t *testing.T) {
	t.Parallel()
	d := anEvidenceWorld(t)
	for name, id := range map[string]string{"running": "wo-running", "reviewed": "wo-ruled", "ended": "wo-shipped", "empty": "wo-loose"} {
		standing, err := d.Client.Acceptance(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		holdsGolden(t, "acceptance-"+name, steady(pretty(t, standing)))
		timeline, err := d.Client.WorkTimeline(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		holdsGolden(t, "timeline-"+name, steady(pretty(t, timeline)))
		switch name {
		case "ended":
			if timeline.Record == nil || timeline.Record.PersonMs == 0 || timeline.Timeline.Terminal != workorders.Done {
				t.Errorf("the ended work's timeline reads %+v with record %+v", timeline.Timeline, timeline.Record)
			}
		default:
			if timeline.Record != nil || timeline.Timeline.Terminal != "" {
				t.Errorf("work under way has terminal %q and record %+v", timeline.Timeline.Terminal, timeline.Record)
			}
		}
	}
	ended, err := d.Client.WorkTimeline(t.Context(), "wo-shipped")
	if err != nil {
		t.Fatal(err)
	}
	var person bool
	for _, s := range ended.Timeline.Segments {
		person = person || s.Kind == delivery.Person
	}
	if !person {
		t.Errorf("work a member accepted has no person segment: %+v", ended.Timeline.Segments)
	}
}
