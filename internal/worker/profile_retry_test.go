package worker_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
)

// profileReads is a profile source that cannot be read while a test says so.
type profileReads struct {
	ports.ProfileSource
	failing atomic.Bool
}

func (p *profileReads) Load(ctx context.Context, repository, base, overrides string) (lanes.Profile, error) {
	if p.failing.Load() {
		return lanes.Profile{}, errors.New("the profile source is out")
	}
	return p.ProfileSource.Load(ctx, repository, base, overrides)
}

// A lane a restarted worker adopts reads its profile again once the source answers, because the thresholds and the gates
// it runs on come from nothing else. The worker looks again every thirty seconds.
func TestAnAdoptedLaneReadsItsProfileOnceItCan(t *testing.T) {
	t.Parallel()
	w := newWorldRig(t)
	source := w.config.Provisioner.Profiles.(*fake.ProfileSource)
	source.Put("/repository", "[lifecycle]\nstall_after = \"7m\"\n[agent]\ngates = [\"Bash(git tag *)\"]\n")
	reads := &profileReads{ProfileSource: source}
	reads.failing.Store(true)
	w.config.Provisioner.Profiles = reads
	w.record("lane-1", lanes.StateRunning)
	if err := w.journal.SaveActual(t.Context(), "lane-1", lanes.Idle, ""); err != nil {
		t.Fatal(err)
	}
	w.effect(lanes.EffectSession, "lane-1", lanes.EffectCompleted)
	w.driver.Leave("lane-1", 1, sessionAlive)
	w.driver.Play(script([]fake.DriverStep{{Hold: true}}))
	if _, err := w.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if stall, gates, _ := w.subject.LaneBounds("lane-1"); stall != 0 || len(gates) != 0 {
		t.Fatalf("an adopted lane whose profile cannot be read runs on a stall bound of %v and the gates %v", stall, gates)
	}

	reads.failing.Store(false)
	w.clock.Advance(10 * time.Second)
	if _, err := w.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if stall, _, _ := w.subject.LaneBounds("lane-1"); stall != 0 {
		t.Errorf("the lane read its profile %v after it failed, before the thirty seconds were out", stall)
	}
	w.clock.Advance(21 * time.Second)
	if _, err := w.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	stall, gates, ok := w.subject.LaneBounds("lane-1")
	if !ok || stall != 7*time.Minute || !slices.Equal(gates, []string{"Bash(git tag *)"}) {
		t.Errorf("the lane runs on a stall bound of %v and the gates %v, want its profile's 7m and its gate", stall, gates)
	}
}

// An orchestrator whose profile cannot be read still has limits: with none, its first failed start would be one more
// than its breaker allows, and its run would end. It restarts on the default limits, and its next generation starts.
func TestAnOrchestratorWithNoProfileRestartsOnTheDefaultLimits(t *testing.T) {
	o := newOrchestratorRig(t, aSession([]fake.DriverStep{{Hold: true}}))
	reads := &profileReads{ProfileSource: o.config.Provisioner.Profiles.(*fake.ProfileSource)}
	reads.failing.Store(true)
	o.config.Provisioner.Profiles = reads
	run := o.start(t)
	o.tick(t)
	_, state := o.told(t, "Are you there?")
	o.awaitNoted(t, 1)
	o.clock.Advance(31 * time.Second)
	o.await(t, "backoff after the failed start", func(row ledger.Orchestrator) bool { return row.Actual == string(lanes.Backoff) })

	reads.failing.Store(false)
	o.clock.Advance(31 * time.Second)
	o.awaitSessions(t, 1)
	if spec := o.specs()[0]; spec.Lane != run.Lane() || spec.Generation != 2 {
		t.Errorf("the session after the backoff is %+v, want run %s's generation 2", spec, run.Lane())
	}
	o.awaitMessage(t, state, messages.HandedOff)
	if row := o.row(t); row.Run != run.Run || row.Ended() {
		t.Errorf("the failed start left %+v", row)
	}
}

// profileSaying is a profile source that cannot be read, and says why in the words a test sets, until it is told it can.
type profileSaying struct {
	ports.ProfileSource
	says atomic.Value
}

func (p *profileSaying) Load(ctx context.Context, repository, base, overrides string) (lanes.Profile, error) {
	if says, _ := p.says.Load().(string); says != "" {
		return lanes.Profile{}, errors.New(says)
	}
	return p.ProfileSource.Load(ctx, repository, base, overrides)
}

// A profile that stays unreadable is reported once, and not at each of the worker's looks every thirty seconds: the same
// words say nothing new. Other words are reported, and so is the same failure after a read that succeeded between.
func TestAProfileThatStaysUnreadableIsReportedOnceUntilItChanges(t *testing.T) {
	t.Parallel()
	w := newWorldRig(t)
	source := w.config.Provisioner.Profiles.(*fake.ProfileSource)
	source.Put("/repository", "[lifecycle]\nstall_after = \"7m\"\n")
	reads := &profileSaying{ProfileSource: source}
	reads.says.Store("the profile source is out")
	w.config.Provisioner.Profiles = reads
	w.record("lane-1", lanes.StateRunning)
	if err := w.journal.SaveActual(t.Context(), "lane-1", lanes.Idle, ""); err != nil {
		t.Fatal(err)
	}
	w.effect(lanes.EffectSession, "lane-1", lanes.EffectCompleted)
	w.driver.Leave("lane-1", 1, sessionAlive)
	w.driver.Play(script([]fake.DriverStep{{Hold: true}}))
	reported := func(words string) int {
		n := 0
		for _, err := range w.problems() {
			if strings.Contains(err.Error(), words) {
				n++
			}
		}
		return n
	}
	look := func() {
		t.Helper()
		w.clock.Advance(31 * time.Second)
		if _, err := w.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		look()
	}
	if n := reported("the profile source is out"); n != 1 {
		t.Errorf("a profile that stayed unreadable for five looks was reported %d times, want once", n)
	}

	reads.says.Store("the profile source is slow")
	look()
	look()
	if n := reported("the profile source is slow"); n != 1 {
		t.Errorf("a profile that stays unreadable for other reasons was reported %d times, want once", n)
	}

	reads.says.Store("")
	look()
	if stall, _, _ := w.subject.LaneBounds("lane-1"); stall != 7*time.Minute {
		t.Fatalf("the lane did not read its profile once it could: %v", stall)
	}
}
