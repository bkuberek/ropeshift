package exit

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// waitBounds say how long a test waits for the daemon to get somewhere. Every step a lane takes writes
// to the disk: each ledger and journal commit flushes a file, and git and every process it starts write
// files too. While another program holds the disk up with full flushes of its own, a commit that takes
// tens of microseconds on an idle disk takes hundreds of milliseconds, and a lane's start goes from
// seconds to minutes. CPU load slows a lane as well: on a runner with three cores a resumed session of a
// fleet took six to eight seconds, and a wait over many sessions takes the sum. So a wait counts only its time on a disk that flushes as an
// idle one does, and goes on while the disk is held up, to its longest. A wait over a scenario of many
// steps also gives a progress reading, and counts only the time since the reading last changed.
type waitBounds struct {
	// ceiling is the time on an idle disk that flushes in a millisecond or less after which a wait gives up.
	// A slower idle disk lengthens it, up to longest.
	ceiling time.Duration
	// longest bounds a wait whatever the disk does.
	longest time.Duration
	// poll is the time between two looks at the condition, and look between two readings of the disk.
	poll, look time.Duration
	// idle is how long this machine's disk takes to flush a block when nothing holds it up, and flush how
	// long it takes now.
	idle, flush func() time.Duration
}

// exitWaits are the bounds of the waits in these tests.
var exitWaits = waitBounds{
	ceiling: 30 * time.Second, longest: 5 * time.Minute, poll: 20 * time.Millisecond, look: time.Second,
	idle: idleFlush, flush: flushLatency,
}

// heldUpFactor and heldUpFloor say when a disk is held up: when a flush takes this many times what it
// takes on the idle disk, and at least the floor, so a reading that is merely noisy does not count.
const (
	heldUpFactor = 20
	heldUpFloor  = time.Millisecond
)

// heldUp reports a disk reading as one held up by another program's flushes.
func (b waitBounds) heldUp(took time.Duration) bool {
	return took >= max(heldUpFactor*b.idle(), heldUpFloor)
}

// scaleFrom is the idle flush up to which a disk counts as fast, and the ceiling is as it is written.
const scaleFrom = time.Millisecond

// idleCeiling is the time on an idle disk after which a wait gives up, on this machine's disk. Every step
// of a lane is a run of commits, so on a disk that flushes slowly even when nothing holds it up, the same
// steps take proportionally longer, and the ceiling grows in step with the idle flush. A disk that
// flushes in a millisecond or less keeps the ceiling as written, and no disk lengthens it past the longest.
func (b waitBounds) idleCeiling() time.Duration {
	scale := max(1, float64(b.idle())/float64(scaleFrom))
	return min(time.Duration(float64(b.ceiling)*scale), b.longest)
}

// await looks at done until it holds, and reports whether it did. A wait that gives up says why, with
// what the disk took to flush a block at its last reading and on an idle disk.
func (b waitBounds) await(done func() bool) (bool, string) {
	return b.awaitProgress(done, nil)
}

// awaitProgress is await for a thing that takes many steps, such as a fleet of lanes going through their
// sessions one after another. Its progress reading says how far the thing has got, as a string that
// differs whenever it has got further, and is read every look. The time a wait counts toward its ceiling
// starts over whenever the reading changes, so a wait goes on while the thing moves, however slowly a
// busy machine moves it, and gives up a ceiling after the last move. The longest still bounds the whole
// wait. With a nil progress it is await.
func (b waitBounds) awaitProgress(done func() bool, progress func() string) (bool, string) {
	began := time.Now()
	ceiling := b.idleCeiling()
	counted, last, nextLook, nextRead := time.Duration(0), began, began, began
	lastMove := began
	var reading string
	var took time.Duration
	held := false
	for {
		if done() {
			return true, ""
		}
		now := time.Now()
		if !held {
			counted += now.Sub(last)
		}
		last = now
		if progress != nil && !now.Before(nextRead) {
			if seen := progress(); seen != reading {
				counted, lastMove, reading = 0, now, seen
			}
			nextRead = time.Now().Add(b.look)
		}
		if counted >= ceiling || now.Sub(began) >= b.longest {
			return false, b.gaveUp(counted, now.Sub(began), now.Sub(lastMove), reading, progress != nil, took)
		}
		if !now.Before(nextLook) {
			took = b.flush()
			held = b.heldUp(took)
			nextLook = time.Now().Add(b.look)
		}
		time.Sleep(b.poll)
	}
}

// gaveUp is the reason a wait ran out: the time it counted on an idle disk, the time in all, and what the
// disk took to flush a block. A wait with a progress reading also says how long since the reading last
// changed, and what it was.
func (b waitBounds) gaveUp(counted, total, sinceMove time.Duration, reading string, measured bool, took time.Duration) string {
	var progress string
	if measured {
		progress = fmt.Sprintf("; no progress for %v, and the last reading was %q", sinceMove.Round(time.Millisecond), reading)
	}
	return fmt.Sprintf("gave up after %v on an idle disk, %v in all%s; the disk last flushed a block in %v, and in %v when idle\n",
		counted.Round(time.Millisecond), total.Round(time.Millisecond), progress, took, b.idle())
}

// flushLatency is how long the disk under the temporary directory takes now to write one block through
// to the device, as a commit does: the median of three. It calls fsync itself, because File.Sync on macOS
// asks the device to flush its whole cache, which is the very flush that holds a disk up for everyone.
func flushLatency() time.Duration {
	return medianFlush(3)
}

// idleFlush is the flush latency this process measured first, which stands for the idle disk.
var idleFlush = sync.OnceValue(func() time.Duration { return medianFlush(9) })

func medianFlush(n int) time.Duration {
	file, err := os.CreateTemp("", "rsdisk")
	if err != nil {
		return 0
	}
	defer os.Remove(file.Name())
	defer file.Close()
	block := make([]byte, 4096)
	var took []time.Duration
	for range n {
		if _, err := file.WriteAt(block, 0); err != nil {
			return 0
		}
		began := time.Now()
		if err := syscall.Fsync(int(file.Fd())); err != nil {
			return 0
		}
		took = append(took, time.Since(began))
	}
	slices.Sort(took)
	return took[len(took)/2]
}

// heldBounds are short bounds on a disk whose reading a test chooses.
func heldBounds(flush func() time.Duration) waitBounds {
	return waitBounds{
		ceiling: 200 * time.Millisecond, longest: time.Second, poll: time.Millisecond, look: 10 * time.Millisecond,
		idle: func() time.Duration { return 50 * time.Microsecond }, flush: flush,
	}
}

func TestAWaitOnAnIdleDiskGivesUpAtItsCeiling(t *testing.T) {
	bounds := heldBounds(func() time.Duration { return 60 * time.Microsecond })
	began := time.Now()
	held, said := bounds.await(func() bool { return false })
	took := time.Since(began)
	if held || took < bounds.ceiling || took > bounds.ceiling+300*time.Millisecond {
		t.Fatalf("held %t after %v", held, took)
	}
	if !strings.Contains(said, "60µs") {
		t.Errorf("the reason leaves out the disk: %q", said)
	}
}

// ticking is a progress reading that changes every step, from a start the test chose.
func ticking(began time.Time, step time.Duration) func() string {
	return func() string { return fmt.Sprintf("step %d", time.Since(began)/step) }
}

func TestAWaitThatMakesProgressGoesOnPastItsCeiling(t *testing.T) {
	bounds := heldBounds(func() time.Duration { return 60 * time.Microsecond })
	began := time.Now()
	held, said := bounds.awaitProgress(func() bool { return time.Since(began) > 3*bounds.ceiling }, ticking(began, bounds.ceiling/4))
	if !held {
		t.Fatalf("gave up after %v: %s", time.Since(began), said)
	}
}

func TestAWaitThatMakesProgressStillEndsAtTheLongest(t *testing.T) {
	bounds := heldBounds(func() time.Duration { return 60 * time.Microsecond })
	began := time.Now()
	held, said := bounds.awaitProgress(func() bool { return false }, ticking(began, bounds.ceiling/4))
	took := time.Since(began)
	if held || took < bounds.longest || took > bounds.longest+300*time.Millisecond {
		t.Fatalf("held %t after %v", held, took)
	}
	if !strings.Contains(said, "step ") {
		t.Errorf("the reason leaves out the last reading: %q", said)
	}
}

func TestAWaitWithNoProgressGivesUpAtItsCeiling(t *testing.T) {
	bounds := heldBounds(func() time.Duration { return 60 * time.Microsecond })
	began := time.Now()
	held, said := bounds.awaitProgress(func() bool { return false }, func() string { return "stuck at lane-3" })
	took := time.Since(began)
	if held || took < bounds.ceiling || took > bounds.ceiling+300*time.Millisecond {
		t.Fatalf("held %t after %v", held, took)
	}
	if !strings.Contains(said, "stuck at lane-3") || !strings.Contains(said, "progress") {
		t.Errorf("the reason leaves out the last progress: %q", said)
	}
}

// TestAWaitCountsFromItsLastProgress is a wait whose progress stops partway: it gives up a ceiling after
// the last change, not a ceiling after it began.
func TestAWaitCountsFromItsLastProgress(t *testing.T) {
	bounds := heldBounds(func() time.Duration { return 60 * time.Microsecond })
	began := time.Now()
	const movedFor = 400 * time.Millisecond
	reading := func() string {
		return fmt.Sprintf("step %d", min(time.Since(began), movedFor)/(movedFor/4))
	}
	held, _ := bounds.awaitProgress(func() bool { return false }, reading)
	took := time.Since(began)
	if held || took < movedFor+bounds.ceiling-50*time.Millisecond || took >= bounds.longest {
		t.Fatalf("held %t after %v", held, took)
	}
}

func TestAWaitOnAHeldUpDiskGoesOnPastItsCeiling(t *testing.T) {
	bounds := heldBounds(func() time.Duration { return 40 * time.Millisecond })
	began := time.Now()
	held, said := bounds.await(func() bool { return time.Since(began) > 3*bounds.ceiling })
	if !held {
		t.Fatalf("gave up after %v: %s", time.Since(began), said)
	}
}

func TestAWaitOnAHeldUpDiskStillEnds(t *testing.T) {
	bounds := heldBounds(func() time.Duration { return 40 * time.Millisecond })
	began := time.Now()
	held, said := bounds.await(func() bool { return false })
	took := time.Since(began)
	if held || took < bounds.longest || took > bounds.longest+300*time.Millisecond {
		t.Fatalf("held %t after %v", held, took)
	}
	if !strings.Contains(said, "40ms") {
		t.Errorf("the reason leaves out the disk: %q", said)
	}
}

// TestAWaitCountsOnlyItsTimeOnAnIdleDisk is a disk held up for a while and then idle: the wait's ceiling
// starts to run out only once the disk is idle.
func TestAWaitCountsOnlyItsTimeOnAnIdleDisk(t *testing.T) {
	began := time.Now()
	const heldFor = 400 * time.Millisecond
	bounds := heldBounds(func() time.Duration {
		if time.Since(began) < heldFor {
			return 40 * time.Millisecond
		}
		return 60 * time.Microsecond
	})
	held, _ := bounds.await(func() bool { return false })
	took := time.Since(began)
	if held || took < heldFor+bounds.ceiling-50*time.Millisecond || took >= bounds.longest {
		t.Fatalf("held %t after %v", held, took)
	}
}

// TestTheCeilingScalesWithTheIdleFlush is the rule for a disk that is slow by itself: every step of a lane
// is a run of commits, so its time grows with the idle flush, and the ceiling grows with it, from one
// millisecond of idle flush up, and never past the longest wait.
func TestTheCeilingScalesWithTheIdleFlush(t *testing.T) {
	for _, c := range []struct {
		name string
		idle time.Duration
		want time.Duration
	}{
		{"a fast disk", 50 * time.Microsecond, exitWaits.ceiling},
		{"a flush of one millisecond", time.Millisecond, exitWaits.ceiling},
		{"a flush of two milliseconds", 2 * time.Millisecond, 2 * exitWaits.ceiling},
		{"a flush of five milliseconds", 5 * time.Millisecond, 5 * exitWaits.ceiling},
		{"a flush of 60 milliseconds", 60 * time.Millisecond, exitWaits.longest},
		{"a flush of a second", time.Second, exitWaits.longest},
	} {
		bounds := exitWaits
		bounds.idle = func() time.Duration { return c.idle }
		if got := bounds.idleCeiling(); got != c.want {
			t.Errorf("%s: the ceiling is %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAWaitOnASlowDiskGivesUpAtItsScaledCeiling(t *testing.T) {
	bounds := heldBounds(func() time.Duration { return 3 * time.Millisecond })
	bounds.idle = func() time.Duration { return 3 * time.Millisecond }
	bounds.flush = bounds.idle
	began := time.Now()
	held, said := bounds.await(func() bool { return false })
	took := time.Since(began)
	want := 3 * bounds.ceiling
	if held || took < want || took > want+300*time.Millisecond {
		t.Fatalf("held %t after %v, want a give-up at %v", held, took, want)
	}
	if !strings.Contains(said, "3ms") {
		t.Errorf("the reason leaves out the disk: %q", said)
	}
}

func TestAWaitOnASlowDiskStillEndsAtTheLongest(t *testing.T) {
	bounds := heldBounds(func() time.Duration { return 60 * time.Millisecond })
	bounds.idle = func() time.Duration { return 60 * time.Millisecond }
	began := time.Now()
	held, _ := bounds.await(func() bool { return false })
	took := time.Since(began)
	if held || took < bounds.longest || took > bounds.longest+300*time.Millisecond {
		t.Fatalf("held %t after %v", held, took)
	}
}

func TestTheDiskFlushesABlock(t *testing.T) {
	if took := flushLatency(); took <= 0 || took > time.Minute {
		t.Fatalf("a flush took %v", took)
	}
}
