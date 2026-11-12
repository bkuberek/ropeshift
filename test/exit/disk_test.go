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
// seconds to minutes, where CPU load alone, even from two full suites at once, slows it by less than
// twice. So a wait counts only its time on a disk that flushes as an idle one does, and goes on while the
// disk is held up, to its longest.
type waitBounds struct {
	// ceiling is the time on an idle disk after which a wait gives up.
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

// await looks at done until it holds, and reports whether it did. A wait that gives up says why, with
// what the disk took to flush a block at its last reading and on an idle disk.
func (b waitBounds) await(done func() bool) (bool, string) {
	began := time.Now()
	counted, last, nextLook := time.Duration(0), began, began
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
		if counted >= b.ceiling || now.Sub(began) >= b.longest {
			return false, fmt.Sprintf("gave up after %v on an idle disk, %v in all; the disk last flushed a block in %v, and in %v when idle\n",
				counted.Round(time.Millisecond), now.Sub(began).Round(time.Millisecond), took, b.idle())
		}
		if !now.Before(nextLook) {
			took = b.flush()
			held = b.heldUp(took)
			nextLook = time.Now().Add(b.look)
		}
		time.Sleep(b.poll)
	}
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

func TestTheDiskFlushesABlock(t *testing.T) {
	if took := flushLatency(); took <= 0 || took > time.Minute {
		t.Fatalf("a flush took %v", took)
	}
}
