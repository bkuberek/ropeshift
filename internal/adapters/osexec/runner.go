// Package osexec is the ProcessRunner on the operating system.
package osexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// Runner spawns real processes. It runs the binary itself, with no shell, so a
// user's login files cannot put back what the allowlist took out.
type Runner struct{}

// Start spawns a process. The environment is always the spec's, because a nil
// environment would otherwise inherit this process's.
func (Runner) Start(_ context.Context, spec ports.ProcessSpec) (ports.Process, error) {
	if err := lanes.CheckSpawn(spec.Path); err != nil {
		return nil, err
	}
	cmd := &exec.Cmd{
		Path:   spec.Path,
		Args:   append([]string{spec.Path}, spec.Args...),
		Dir:    spec.Dir,
		Env:    append([]string{}, spec.Env...),
		Stdin:  spec.Stdin,
		Stdout: spec.Stdout,
		Stderr: spec.Stderr,
	}
	// A process that must outlive the service leaves the group a service manager signals, which is all
	// this runner can do. On Linux, the systemd adapter takes it out of the control group too.
	if spec.Detached || spec.Outlive {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &process{cmd: cmd}, nil
}

type process struct {
	cmd    *exec.Cmd
	once   sync.Once
	status ports.ExitStatus
	err    error
}

func (p *process) PID() int { return p.cmd.Process.Pid }

var signals = map[ports.Signal]syscall.Signal{
	ports.SignalInterrupt: syscall.SIGINT,
	ports.SignalTerminate: syscall.SIGTERM,
	ports.SignalKill:      syscall.SIGKILL,
}

func (p *process) Signal(signal ports.Signal) error {
	value, ok := signals[signal]
	if !ok {
		return fmt.Errorf("no such signal: %s", signal)
	}
	err := p.cmd.Process.Signal(value)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

// Wait reaps the process once and remembers how it ended.
func (p *process) Wait() (ports.ExitStatus, error) {
	p.once.Do(func() {
		err := p.cmd.Wait()
		var exit *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &exit):
			p.status = statusOf(exit)
		default:
			p.err = err
		}
	})
	return p.status, p.err
}

func statusOf(exit *exec.ExitError) ports.ExitStatus {
	status, ok := exit.Sys().(syscall.WaitStatus)
	if ok && status.Signaled() {
		for name, value := range signals {
			if value == status.Signal() {
				return ports.ExitStatus{Signal: name}
			}
		}
		return ports.ExitStatus{Signal: ports.Signal(status.Signal().String())}
	}
	return ports.ExitStatus{Code: exit.ExitCode()}
}

var _ ports.ProcessRunner = Runner{}
