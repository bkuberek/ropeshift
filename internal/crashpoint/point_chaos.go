//go:build chaos

package crashpoint

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// EnvPause holds a process at one named step, as "op/step=duration", so a test can
// order a race between two loops of a daemon that runs as its own process.
const EnvPause = "ROPESHIFT_CHAOS_PAUSE"

// EnvAwait holds a process at one named step until another named step is hit, or for
// at most a bound, as "op/step=op/step,duration". Only a hit after the wait began
// releases it. A test uses it to make one loop of a daemon that runs as its own process
// wait for a step of another.
const EnvAwait = "ROPESHIFT_CHAOS_AWAIT"

// EnvKill ends a process with SIGKILL at one named step, as "op/step", so a test can crash
// a daemon that runs as its own process exactly there.
const EnvKill = "ROPESHIFT_CHAOS_KILL"

var handler atomic.Pointer[func(op, step string)]

func init() {
	if target := os.Getenv(EnvKill); target != "" {
		Arm(func(op, step string) {
			if op+"/"+step == target {
				_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
				select {}
			}
		})
		return
	}
	if spec := os.Getenv(EnvPause); spec != "" {
		name, value, _ := strings.Cut(spec, "=")
		pause, err := time.ParseDuration(value)
		if err != nil {
			panic(fmt.Sprintf("crashpoint: %s: %v", EnvPause, err))
		}
		Arm(func(op, step string) {
			if op+"/"+step == name {
				time.Sleep(pause)
			}
		})
		return
	}
	if spec := os.Getenv(EnvAwait); spec != "" {
		fn, err := awaiting(spec)
		if err != nil {
			panic(fmt.Sprintf("crashpoint: %s: %v", EnvAwait, err))
		}
		Arm(fn)
	}
}

// awaiting returns the function an EnvAwait spec arms.
func awaiting(spec string) (func(op, step string), error) {
	name, rest, named := strings.Cut(spec, "=")
	release, value, bounded := strings.Cut(rest, ",")
	if !named || !bounded {
		return nil, fmt.Errorf("%q is not op/step=op/step,duration", spec)
	}
	bound, err := time.ParseDuration(value)
	if err != nil {
		return nil, err
	}
	g := &gate{next: make(chan struct{})}
	return func(op, step string) {
		switch op + "/" + step {
		case release:
			g.open()
		case name:
			g.wait(bound)
		}
	}, nil
}

// gate releases each waiter at the first open after the waiter arrived.
type gate struct {
	mu   sync.Mutex
	next chan struct{}
}

func (g *gate) open() {
	g.mu.Lock()
	defer g.mu.Unlock()
	close(g.next)
	g.next = make(chan struct{})
}

func (g *gate) wait(bound time.Duration) {
	g.mu.Lock()
	next := g.next
	g.mu.Unlock()
	timer := time.NewTimer(bound)
	defer timer.Stop()
	select {
	case <-next:
	case <-timer.C:
	}
}

// Arm installs the function a chaos child runs at every named step.
func Arm(fn func(op, step string)) {
	handler.Store(&fn)
}

// Hit calls the armed function, if there is one.
func Hit(op, step string) {
	if fn := handler.Load(); fn != nil {
		(*fn)(op, step)
	}
}
