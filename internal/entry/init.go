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

	"ropeshift.dev/ropeshift/internal/api"
)

// How long ropeshift init waits for the daemon it started to answer, and then to stop.
const (
	initDaemonReady = 60 * time.Second
	initDaemonStop  = 15 * time.Second
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
		cmd := &exec.Cmd{
			Path:   executable,
			Args:   []string{commandDaemon, "solo", "--web", api.WebOff, "--telemetry", api.WebOff},
			Env:    os.Environ(),
			Stdout: output,
			Stderr: output,
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		// The daemon is crash-only, so how it exited says nothing a run needs: only that it did.
		stop := func() error {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case err := <-exited:
				_ = err
				return nil
			case <-time.After(initDaemonStop):
				_ = cmd.Process.Kill()
				killed := <-exited
				return fmt.Errorf("the daemon did not stop within %v, so it was killed: %v", initDaemonStop, killed)
			}
		}
		if err := awaitDaemon(ctx, paths, exited); err != nil {
			_ = stop()
			return nil, fmt.Errorf("%w\n%s", err, output.String())
		}
		return stop, nil
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
