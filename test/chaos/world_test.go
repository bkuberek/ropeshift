//go:build chaos

package chaos

import (
	"os"
	"path/filepath"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/test/contract"
)

// world is one crash run: its target, the parent's view of the ledger, and the
// witness.
type world struct {
	t       *testing.T
	target  Target
	rig     *contract.Rig
	store   ports.Store
	logPath string
	variant string
}

// newWorld creates a disposable ledger after the guard agrees.
func newWorld(t *testing.T, variant string) *world {
	t.Helper()
	target, err := NewTarget()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(target.Dir) })
	if err := Verify(target, LiveInstallation()); err != nil {
		t.Fatal(err)
	}
	backend := contract.Backend{Name: "chaos", Fresh: func(testing.TB) contract.Database {
		return contract.Database{
			Restartable: true,
			ShiftClock:  func(int64) { t.Fatal("a crash run keeps real time") },
			Open: func(t testing.TB, opts sqlstore.Options) ports.Store {
				store, err := sqlite.Open(target.Path, opts)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				return store
			},
		}
	}}
	rig := contract.NewRig(t, backend)
	return &world{
		t:       t,
		target:  target,
		rig:     rig,
		store:   rig.Store(),
		logPath: filepath.Join(target.Dir, "effects.log"),
		variant: variant,
	}
}

// spec fills in what every child of this world shares.
func (w *world) spec(s childSpec) childSpec {
	s.Target = w.target
	s.Scope = string(w.rig.Scope)
	s.LogPath = w.logPath
	s.Variant = w.variant
	return s
}

// verify checks the ledger against itself and against the witness.
func (w *world) verify() error {
	if err := w.rig.CheckRevisions(w.store, w.rig.Scope); err != nil {
		return err
	}
	if err := w.rig.CheckProjection(w.store); err != nil {
		return err
	}
	return w.verifyLog()
}

// verifyLog holds the database to what the children saw: an operation a child
// saw commit must be in the log with the same revision.
func (w *world) verifyLog() error {
	records, err := ReadEffectLog(w.logPath)
	if err != nil {
		return contract.Violatedf("%v", err)
	}
	events, err := w.rig.Events(w.store, w.rig.Scope)
	if err != nil {
		return err
	}
	byRev := map[int64]bool{}
	for _, e := range events {
		byRev[e.Rev] = true
	}
	for _, r := range records {
		if r.Kind == Completed && r.Rev != 0 && !byRev[r.Rev] {
			return contract.Violatedf("a child saw %s %s commit at rev %d, and the log has no such event", r.Op, r.Key, r.Rev)
		}
	}
	return nil
}
