package fake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// Program stands for one binary. It writes to stdout and returns an exit code, and
// it may block on done, which a signal closes.
type Program func(spec ports.ProcessSpec, done <-chan struct{}) int

// ProcessRunner runs programs in memory. No process starts.
type ProcessRunner struct {
	mu       sync.Mutex
	programs map[string]Program
	spawned  []ports.ProcessSpec
	nextPID  atomic.Int64
}

// NewProcessRunner returns a runner with no programs.
func NewProcessRunner() *ProcessRunner {
	return &ProcessRunner{programs: map[string]Program{}}
}

// Install makes a binary exist.
func (r *ProcessRunner) Install(path string, program Program) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.programs[path] = program
}

// Spawned lists every spawn, in order.
func (r *ProcessRunner) Spawned() []ports.ProcessSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ports.ProcessSpec{}, r.spawned...)
}

// Start runs a program, under the rule the real runner holds.
func (r *ProcessRunner) Start(_ context.Context, spec ports.ProcessSpec) (ports.Process, error) {
	if err := lanes.CheckSpawn(spec.Path); err != nil {
		return nil, err
	}
	r.mu.Lock()
	program, ok := r.programs[spec.Path]
	r.spawned = append(r.spawned, spec)
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%s: %w", spec.Path, errNoBinary)
	}
	p := &fakeProcess{pid: int(r.nextPID.Add(1)), done: make(chan struct{}), exited: make(chan struct{})}
	go func() {
		code := program(spec, p.done)
		p.mu.Lock()
		p.status.Code = code
		if p.status.Signal != "" {
			p.status.Code = 0
		}
		p.mu.Unlock()
		close(p.exited)
	}()
	return p, nil
}

var errNoBinary = errors.New("no such binary")

type fakeProcess struct {
	pid    int
	mu     sync.Mutex
	status ports.ExitStatus
	once   sync.Once
	done   chan struct{}
	exited chan struct{}
}

func (p *fakeProcess) PID() int { return p.pid }

// Signal ends a program that is blocked on done.
func (p *fakeProcess) Signal(signal ports.Signal) error {
	select {
	case <-p.exited:
		return nil
	default:
	}
	p.once.Do(func() {
		p.mu.Lock()
		p.status.Signal = signal
		p.mu.Unlock()
		close(p.done)
	})
	return nil
}

func (p *fakeProcess) Wait() (ports.ExitStatus, error) {
	<-p.exited
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status, nil
}

// PrintEnv is a program that prints its environment.
func PrintEnv(spec ports.ProcessSpec, _ <-chan struct{}) int {
	if spec.Stdout != nil {
		var out bytes.Buffer
		for _, entry := range spec.Env {
			out.WriteString(entry + "\n")
		}
		_, _ = spec.Stdout.Write(out.Bytes())
	}
	return 0
}

// PrintArgs is a program that prints its arguments.
func PrintArgs(spec ports.ProcessSpec, _ <-chan struct{}) int {
	if spec.Stdout != nil {
		_, _ = spec.Stdout.Write([]byte(strings.Join(spec.Args, " ") + "\n"))
	}
	return 0
}

// PrintDir is a program that prints its directory.
func PrintDir(spec ports.ProcessSpec, _ <-chan struct{}) int {
	if spec.Stdout != nil {
		_, _ = spec.Stdout.Write([]byte(spec.Dir + "\n"))
	}
	return 0
}

// Fail is a program that exits with 1.
func Fail(ports.ProcessSpec, <-chan struct{}) int { return 1 }

// Block is a program that runs until a signal ends it.
func Block(_ ports.ProcessSpec, done <-chan struct{}) int {
	<-done
	return 0
}

var _ ports.ProcessRunner = (*ProcessRunner)(nil)
