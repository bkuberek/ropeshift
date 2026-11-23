package worker_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// endSaveHeld is a journal that holds the second save of a probe's attempt, which is the one a probe makes when it ends,
// until a test lets it go.
type endSaveHeld struct {
	ports.Journal
	saves   atomic.Int64
	reached chan struct{}
	release chan struct{}
	freed   sync.Once
}

func (j *endSaveHeld) SaveProbeAttempt(ctx context.Context, attempt lanes.ProbeAttempt) error {
	if j.saves.Add(1) == 2 {
		close(j.reached)
		select {
		case <-j.release:
		case <-time.After(20 * time.Second):
		}
	}
	return j.Journal.SaveProbeAttempt(ctx, attempt)
}

func (j *endSaveHeld) let() { j.freed.Do(func() { close(j.release) }) }

// The attempt a probe keeps when it ends is kept before the next probe may begin, because each save replaces the one
// before it: an end that was kept late would put an older probe's attempt over the newer one a restart reads.
func TestAnAttemptKeptAtAProbesEndNeverReplacesANewerOne(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	journal := &endSaveHeld{Journal: base.journal, reached: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(journal.let)
	base.workerJournal = journal
	var releaseProbes func()
	r := newLaneRigWith(t, base, func(driver *fake.AgentDriver) {
		driver.SetVersion("1.0.0", true)
		releaseProbes = driver.HoldProbes()
	})
	t.Cleanup(releaseProbes)
	deadline := time.Now().Add(10 * time.Second)
	for len(r.driver.Probes()) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("the first probe never started")
		}
		time.Sleep(time.Millisecond)
	}
	releaseProbes()
	select {
	case <-journal.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the first probe's end never kept its attempt")
	}

	// The installed version moves while that end waits to be kept, and a look at it finds the first probe still the one that runs.
	r.driver.SetVersion("2.0.0", true)
	r.clock.Advance(2 * time.Minute)
	looked := make(chan struct{})
	go func() {
		defer close(looked)
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	// Time and not a signal lets a probe that is free to begin do so.
	time.Sleep(300 * time.Millisecond)
	if n := len(r.driver.Probes()); n != 1 {
		t.Errorf("%d probes had begun while the first one's end was still to be kept, want the first alone", n)
	}
	journal.let()
	<-looked
	r.subject.Wait()
	// The look after the end was kept probes the version that moved.
	r.clock.Advance(2 * time.Minute)
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	if n := len(r.driver.Probes()); n != 2 {
		t.Fatalf("%d probes, want one of each version", n)
	}
	got, err := base.journal.ProbeAttempt(t.Context())
	if err != nil || got.Version != "2.0.0" {
		t.Errorf("the journal keeps the attempt %+v, %v, want the newer probe's, of 2.0.0", got, err)
	}
}

// A version the worker notices while a probe's end is being kept is probed once that end is kept, and not when a look a
// version-check interval later finds it: the look that noticed it was refused for the probe that still ran, and nothing
// else would start the next.
func TestAVersionNoticedWhileAProbesEndIsKeptIsProbedAtOnce(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	journal := &endSaveHeld{Journal: base.journal, reached: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(journal.let)
	base.workerJournal = journal
	var releaseProbes func()
	r := newLaneRigWith(t, base, func(driver *fake.AgentDriver) {
		driver.SetVersion("1.0.0", true)
		releaseProbes = driver.HoldProbes()
	})
	t.Cleanup(releaseProbes)
	deadline := time.Now().Add(10 * time.Second)
	for len(r.driver.Probes()) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("the first probe never started")
		}
		time.Sleep(time.Millisecond)
	}
	releaseProbes()
	select {
	case <-journal.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the first probe's end never kept its attempt")
	}

	r.driver.SetVersion("2.0.0", true)
	r.clock.Advance(2 * time.Minute)
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := len(r.driver.Probes()); n != 1 {
		t.Fatalf("%d probes had begun while the first one's end was still to be kept, want the first alone", n)
	}
	journal.let()
	r.subject.Wait()
	if n := len(r.driver.Probes()); n != 2 {
		t.Errorf("%d probes once the first one's end was kept, with no look since: want the version that moved probed too", n)
	}
}
