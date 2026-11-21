package worker_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// keptLaneStop bounds how long a stopping worker may take to let go of a lane it keeps. A service manager
// waits a fixed time for a stop before it kills the process, so a lane that holds its worker past the stop
// costs the journal its clean close.
const keptLaneStop = 5 * time.Second

// journalFile opens a journal on a file, as a daemon does, so a test can close it and open it again.
func journalFile(t *testing.T) (*sqlite.Journal, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "journal.db")
	journal, err := sqlite.OpenJournal(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	if err := journal.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return journal, file
}

// runStoppable runs a worker on a journal file, under a context the test ends the way a daemon's stop does.
// The profile picks the lane's run mode, and setup readies the driver and says what the session does.
func runStoppable(t *testing.T, profile string, setup func(*laneRig) []fake.DriverStep) (*laneRig, *sqlite.Journal, string, func()) {
	t.Helper()
	base := newRig(t)
	journal, file := journalFile(t)
	base.journal = &fake.Journal{Journal: journal}
	r := newLaneRigFor(t, base)
	if profile != "" {
		r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", profile)
	}
	r.dispatched()
	r.driver.Play(script(setup(r)))
	// The probe the worker started with ends, and its result goes up, before Run opens the next epoch, so a read
	// while that epoch opens has nothing of the first one's to send.
	r.subject.Wait()
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	ran := make(chan error, 1)
	go func() { ran <- r.subject.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		r.driver.StopEverySession()
		r.waitForTheLane(t)
	})
	stop := func() {
		t.Helper()
		cancel()
		if err := <-ran; !errors.Is(err, context.Canceled) {
			t.Fatalf("run ended with %v", err)
		}
	}
	return r, journal, file, stop
}

// waitedWithin reports whether the worker's wait returned within limit, and the goroutines that held it
// when it did not.
func (r *laneRig) waitedWithin(limit time.Duration) (bool, string) {
	waited := make(chan struct{})
	go func() {
		defer close(waited)
		r.subject.Wait()
	}()
	select {
	case <-waited:
		return true, ""
	case <-time.After(limit):
		stacks := make([]byte, 1<<20)
		return false, string(stacks[:runtime.Stack(stacks, true)])
	}
}

// TestAStoppingWorkerLetsGoOfTheLaneItKeeps stops a worker while its lane's session is mid-turn, the way a
// daemon stops: it ends the worker's context, waits for what the worker started, and closes the journal.
// The stream of a session that goes on never closes, in a terminal pane or headless, and the session
// outlives the worker, as a service stop keeps lanes. So the worker stops no session and leaves the lane
// running for the worker after it to adopt, and the lane's goroutines still end with the worker's context.
// The journal then closes before a service manager's deadline, and cleanly: SQLite removes the write-ahead
// log when the last connection closes (https://www.sqlite.org/wal.html), and only a clean close does that.
func TestAStoppingWorkerLetsGoOfTheLaneItKeeps(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		profile string
		mode    string
	}{
		{name: "pane", profile: "run_mode = \"tmux\"\n", mode: ports.RunModeInteractive},
		{name: "headless", profile: "run_mode = \"headless\"\n", mode: ports.RunModeHeadless},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stops atomic.Int64
			r, journal, file, stop := runStoppable(t, tt.profile, func(r *laneRig) []fake.DriverStep {
				r.driver.OnStop(func() { stops.Add(1) })
				return []fake.DriverStep{{Bound: true}, {Hold: true}}
			})
			r.eventually("a turn in progress", func(view ledger.LaneView) bool {
				return view.ActualState == string(lanes.Working)
			})
			if got := r.driver.Specs()[0].RunMode; got != tt.mode {
				t.Fatalf("the session runs %q, and the test means %q", got, tt.mode)
			}

			stop()
			if waited, stacks := r.waitedWithin(keptLaneStop); !waited {
				t.Fatalf("the worker still waits on its lane %s after its context ended; goroutines:\n%s", keptLaneStop, stacks)
			}
			if stopped := stops.Load(); stopped != 0 {
				t.Errorf("the worker stopped its lane's session %d times on its way out", stopped)
			}
			if err := journal.Close(); err != nil {
				t.Fatalf("closing the journal: %v", err)
			}
			if _, err := os.Stat(file + "-wal"); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the journal kept its write-ahead log after it closed: %v", err)
			}
			reopened, err := sqlite.OpenJournal(file)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			record, err := reopened.Lane(t.Context(), "lane-1")
			if err != nil {
				t.Fatal(err)
			}
			if record.State != lanes.StateRunning {
				t.Errorf("the worker left its lane %q, and the worker after it adopts only a running one", record.State)
			}
		})
	}
}

// TestAStoppingWorkerWaitsForTheStopItSent stops a worker while a stop it sent its lane's session is still
// on its way. The stop keeps its effect in the journal when it ends, so the worker's wait covers it, and
// the journal closes after the stop's end is on record.
func TestAStoppingWorkerWaitsForTheStopItSent(t *testing.T) {
	t.Parallel()
	stopping, release := make(chan struct{}), make(chan struct{})
	var reached, freed sync.Once
	r, journal, _, stop := runStoppable(t, "", func(r *laneRig) []fake.DriverStep {
		r.driver.OnStop(func() {
			reached.Do(func() { close(stopping) })
			<-release
		})
		return []fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.1)}
	})
	t.Cleanup(func() { freed.Do(func() { close(release) }) })
	select {
	case <-stopping:
	case <-time.After(20 * time.Second):
		t.Fatalf("the worker never stopped the session that reported: the lane reads %+v, problems %v", r.read(), r.problems())
	}

	stop()
	if waited, _ := r.waitedWithin(300 * time.Millisecond); waited {
		t.Fatal("the worker's wait returned while the stop it sent was still on its way")
	}
	freed.Do(func() { close(release) })
	if waited, stacks := r.waitedWithin(keptLaneStop); !waited {
		t.Fatalf("the worker still waits %s after its stop ended; goroutines:\n%s", keptLaneStop, stacks)
	}
	effect, err := journal.Effect(t.Context(), lanes.NewEffect(lanes.EffectStop, "lane-1", 1, "", "").Key)
	if err != nil {
		t.Fatal(err)
	}
	if effect.State != lanes.EffectCompleted {
		t.Errorf("the stop reads %q in the journal the worker leaves", effect.State)
	}
}
