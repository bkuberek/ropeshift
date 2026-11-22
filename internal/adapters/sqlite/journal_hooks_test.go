package sqlite_test

import (
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/lanes"
)

func openedJournal(t *testing.T) *sqlite.Journal {
	t.Helper()
	journal, err := sqlite.OpenJournal(filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	if err := journal.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return journal
}

// A repeat of a hook widens its times, and its detail is the newest one's, whichever order the repeats
// arrive in, because a spool is read at least once and not in the order its hooks fired.
func TestAMergedHookKeepsTheNewestDetail(t *testing.T) {
	t.Parallel()
	for name, order := range map[string][]int64{"oldest first": {10, 20}, "newest first": {20, 10}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			journal := openedJournal(t)
			for _, at := range order {
				detail := map[int64]string{10: lanes.FailureSignIn, 20: lanes.FailureUsageLimit}[at]
				err := journal.MergeHook(t.Context(), lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: "StopFailure", Subject: "s", Detail: detail, AtMs: at})
				if err != nil {
					t.Fatal(err)
				}
			}
			hooks, err := journal.HookEvents(t.Context(), "lane-1")
			if err != nil || len(hooks) != 1 {
				t.Fatalf("hooks %+v (%v)", hooks, err)
			}
			if got := hooks[0]; got.Detail != lanes.FailureUsageLimit || got.FirstMs != 10 || got.LastMs != 20 || got.Count != 2 {
				t.Errorf("the merged hook reads %+v", got)
			}
		})
	}
}

// A clear begins a generation, and what the cleared session started on later does not undo it.
func TestAMergedSessionStartKeepsItsClear(t *testing.T) {
	t.Parallel()
	for name, order := range map[string][]int64{"oldest first": {10, 20}, "newest first": {20, 10}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			journal := openedJournal(t)
			for _, at := range order {
				detail := map[int64]string{10: lanes.SourceClear, 20: "compact"}[at]
				err := journal.MergeHook(t.Context(), lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: "SessionStart", Subject: "s", Detail: detail, AtMs: at})
				if err != nil {
					t.Fatal(err)
				}
			}
			hooks, err := journal.HookEvents(t.Context(), "lane-1")
			if err != nil || len(hooks) != 1 || hooks[0].Detail != lanes.SourceClear {
				t.Errorf("the merged hook reads %+v (%v)", hooks, err)
			}
		})
	}
}
