package worker_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// TestAnEffectOfAnotherProjectWithNoLaneRecordReachesItsOwnProject has a crash leave an input of a lane of
// the installation's second project unknown, with no record of that lane in the journal. The ledger hears
// of it in that project, not in the first one's, because the effect carries the project of the lane that
// produced it.
func TestAnEffectOfAnotherProjectWithNoLaneRecordReachesItsOwnProject(t *testing.T) {
	t.Parallel()
	r := newInstallationRig(t)
	ctx := t.Context()
	effect := lanes.NewEffect(lanes.EffectInput, "lane-b", 1, "", "nudge")
	effect.Project = "pb"
	if _, err := r.journal.BeginEffect(ctx, effect); err != nil {
		t.Fatal(err)
	}
	if _, err := r.journal.MarkUnknown(ctx); err != nil {
		t.Fatal(err)
	}
	r.pass()
	if err := r.subject.Upload(ctx); err != nil {
		t.Fatal(err)
	}
	r.noRefusals()
	for project, want := range map[ledger.WorkspaceID]int{"pb": 1, "pa": 0} {
		rows, err := control.NewEffects(r.stores[project]).List(ctx, project)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != want {
			t.Errorf("project %s holds %d unknown effects, want %d: %+v", project, len(rows), want, rows)
		}
	}
	stored, err := r.journal.Effect(ctx, effect.Key)
	if err != nil || stored.Project != "pb" {
		t.Errorf("the journal keeps the effect as %+v: %v", stored, err)
	}
}

// TestAnEffectTheWorkerRecordsTakesItsLanesProject runs a lane of the second project through a park and
// a wake, and finds every effect the worker recorded for it under that project.
func TestAnEffectTheWorkerRecordsTakesItsLanesProject(t *testing.T) {
	t.Parallel()
	r := newInstallationRig(t)
	r.parkedInProject("pb", "lane-b")
	r.pass()
	r.subject.Wait()
	effects, err := r.journal.Effects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, effect := range effects {
		if effect.Lane != "lane-b" {
			continue
		}
		seen++
		if effect.Project != "pb" {
			t.Errorf("effect %s of lane-b names project %q", effect.Key, effect.Project)
		}
	}
	if seen == 0 {
		t.Fatal("the lane recorded no effect")
	}
}
