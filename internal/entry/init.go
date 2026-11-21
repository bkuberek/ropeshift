package entry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
)

// How long ropeshift init waits for the daemon it started to answer, and then to stop.
const (
	initDaemonReady = 60 * time.Second
	initDaemonStop  = 15 * time.Second
	// initDaemonLogTail is how much of the end of the daemon's log a failed start shows.
	initDaemonLogTail = 4096
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
		return startInitDaemon(ctx, paths, initDaemonCommand(executable))
	}
}

// startInitDaemon runs cmd as the daemon of one init, and returns how to stop it once it answers. The daemon's
// output goes to a log in the state directory, which a start that fails names and keeps, and which init removes
// when its run succeeds, so a run that was slow or failed can be read afterwards.
func startInitDaemon(ctx context.Context, paths api.Paths, cmd *exec.Cmd) (func() error, error) {
	if err := server.PrivateDir(paths.StateDir); err != nil {
		return nil, err
	}
	path := paths.InitDaemonLog()
	log, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	// The log is the daemon's own and a person's to read, whatever mode an earlier file had.
	_ = log.Chmod(0o600)
	cmd.Stdout, cmd.Stderr = log, log
	daemon, err := launchInitDaemon(cmd, initDaemonStop)
	// The daemon holds the descriptors it needs, and a file is no pipe, so nothing the daemon leaves behind
	// holds init's wait for it.
	_ = log.Close()
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	if err := awaitDaemon(ctx, paths, daemon.exited); err != nil {
		_ = daemon.stop()
		return nil, fmt.Errorf("%w\n%s\nThe daemon's log is kept at %s", err, logTail(path), path)
	}
	return daemon.stop, nil
}

// logTail is the end of what the daemon wrote, which a start that failed shows.
func logTail(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return "the daemon's log could not be read: " + err.Error()
	}
	defer file.Close()
	if end, err := file.Seek(0, io.SeekEnd); err == nil && end > initDaemonLogTail {
		_, _ = file.Seek(end-initDaemonLogTail, io.SeekStart)
	} else {
		_, _ = file.Seek(0, io.SeekStart)
	}
	raw, err := io.ReadAll(io.LimitReader(file, initDaemonLogTail))
	if err != nil {
		return "the daemon's log could not be read: " + err.Error()
	}
	return strings.TrimSpace(strings.ToValidUTF8(string(raw), ""))
}

// initDaemonCommand is the daemon an init runs, before its output is set. It starts no probe of the agent CLI:
// the probe starts real sessions that spend turns, and this daemon is stopped, and the probe cancelled, within
// seconds of its start. A process the daemon leaves behind can hold its output open after the daemon ended,
// and a wait on output that is not a file would wait for that process too, so the wait is bounded, as the
// installed-build read's is.
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
