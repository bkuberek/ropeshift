package entry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"ropeshift.com/ropeshift/internal/api"
)

// How long ropeshift init waits for the daemon it started to answer, and then to stop.
const (
	initDaemonReady = 60 * time.Second
	initDaemonStop  = 15 * time.Second
	// initDaemonOutputGrace is how long the wait for the daemon goes on after it ended, for a process it left
	// holding its output.
	initDaemonOutputGrace = 2 * time.Second
)

// startDaemonForInit runs `ropeshiftd solo` for the length of one ropeshift init. Its first start
// registers the worker, which is solo mode's one registration path, and it binds neither the web
// port nor the telemetry port while it runs. It stays in the caller's process group, so an
// interrupt at the terminal ends it with init.
func startDaemonForInit(paths api.Paths) func(context.Context) (func() error, error) {
	return func(ctx context.Context) (func() error, error) {
		executable, err := os.Executable()
		if err != nil {
			return nil, err
		}
		output := &lockedOutput{}
		cmd := initDaemonCommand(executable)
		cmd.Stdout, cmd.Stderr = output, output
		daemon, err := launchInitDaemon(cmd, initDaemonStop)
		if err != nil {
			return nil, err
		}
		if err := awaitDaemon(ctx, paths, daemon.exited); err != nil {
			_ = daemon.stop()
			return nil, fmt.Errorf("%w\n%s", err, output.String())
		}
		return daemon.stop, nil
	}
}

// initDaemonCommand is the daemon an init runs, before its output is set. It starts no probe of the agent CLI:
// the probe starts real sessions that spend turns, and this daemon is stopped, and the probe cancelled, within
// seconds of its start. A process the daemon leaves behind can hold its output open after the daemon ended,
// and the wait for the daemon would wait for that process too, so the wait is bounded, as the installed-build
// read's is.
func initDaemonCommand(executable string) *exec.Cmd {
	return &exec.Cmd{
		Path:      executable,
		Args:      []string{commandDaemon, "solo", "--web", api.WebOff, "--telemetry", api.WebOff, "--no-probe"},
		Env:       os.Environ(),
		WaitDelay: initDaemonOutputGrace,
	}
}

// initDaemon is the daemon one ropeshift init started, and the channel its end arrives on.
type initDaemon struct {
	cmd    *exec.Cmd
	exited chan error
	// limit is how long stop waits for the daemon to end on SIGTERM before it kills it.
	limit time.Duration
}

// launchInitDaemon starts cmd and watches for its end. Init hands the daemon no descriptor it was started with:
// the daemon starts the terminal server, which outlives it and holds every descriptor it was given.
func launchInitDaemon(cmd *exec.Cmd, limit time.Duration) (*initDaemon, error) {
	keepDescriptorsFromChildren()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	daemon := &initDaemon{cmd: cmd, exited: make(chan error, 1), limit: limit}
	go func() { daemon.exited <- cmd.Wait() }()
	return daemon, nil
}

// stop ends the daemon with SIGTERM, and with SIGKILL when it has not ended within the limit. The daemon is
// crash-only, so how it exited says nothing a run needs: only that it did.
func (d *initDaemon) stop() error {
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-d.exited:
		_ = err
		return nil
	case <-time.After(d.limit):
		_ = d.cmd.Process.Kill()
		killed := <-d.exited
		return fmt.Errorf("the daemon did not stop within %v, so it was killed: %v", d.limit, killed)
	}
}

// awaitDaemon waits for the daemon's own answer. A connection proves only that something is bound.
func awaitDaemon(ctx context.Context, paths api.Paths, exited chan error) error {
	client := api.NewUnixClient(paths.ControlSocket())
	defer client.Close()
	deadline := time.After(initDaemonReady)
	for {
		_, err := client.Health(ctx)
		var refused *api.Error
		if err == nil || errors.As(err, &refused) {
			return nil
		}
		select {
		case err := <-exited:
			exited <- err
			return fmt.Errorf("the daemon exited before it answered: %v", err)
		case <-deadline:
			return fmt.Errorf("the daemon did not answer within %v", initDaemonReady)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// lockedOutput keeps what the daemon printed, for the error that explains a start that failed.
type lockedOutput struct {
	mu  sync.Mutex
	raw bytes.Buffer
}

func (o *lockedOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.raw.Write(p)
}

func (o *lockedOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.raw.String()
}
