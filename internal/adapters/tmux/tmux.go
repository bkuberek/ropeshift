// Package tmux is the Terminal on a private tmux server. The server holds panes in its
// own process session, so a session survives the worker that started it (FR-LANE-17). It
// reads no user configuration, and it never touches a default server.
package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ropeshift.dev/ropeshift/internal/ports"
)

// Binary is the name the adapter looks for on PATH.
const Binary = "tmux"

// commandLimit bounds one call, and a call past it is an incomplete read.
const commandLimit = 10 * time.Second

// paneFormat is what a read asks tmux for: the process, whether it ended, and how. A dead
// pane leaves some of these empty, so the separator cannot be a space.
const paneFormat = "#{pane_pid}|#{pane_dead}|#{pane_dead_status}|#{pane_dead_signal}"

// ErrNoTmux means the worker's PATH holds no tmux.
var ErrNoTmux = errors.New("tmux is not on PATH")

// absent is how tmux says a server or a pane is not there. Anything else is a read that
// did not finish, which is never the same answer.
var absent = []string{"can't find", "no server running", "error connecting", "server exited"}

// Options configures a terminal.
type Options struct {
	// Socket is the private server's path, which only this worker uses.
	Socket string
	// Env is the server's whole environment, and every tmux client's.
	Env []string
}

// Terminal runs panes on one private server.
type Terminal struct {
	runner ports.ProcessRunner
	binary string
	socket string
	env    []string
}

// New finds tmux on the PATH of env, because the worker's own may differ.
func New(runner ports.ProcessRunner, options Options) (*Terminal, error) {
	binary, err := find(options.Env)
	if err != nil {
		return nil, err
	}
	return &Terminal{runner: runner, binary: binary, socket: options.Socket, env: options.Env}, nil
}

func find(env []string) (string, error) {
	for _, entry := range env {
		list, ok := strings.CutPrefix(entry, "PATH=")
		if !ok {
			continue
		}
		for _, dir := range filepath.SplitList(list) {
			candidate := filepath.Join(dir, Binary)
			if !filepath.IsAbs(candidate) {
				continue
			}
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return candidate, nil
			}
		}
	}
	return "", ErrNoTmux
}

// run is one client call. `-f /dev/null` keeps a user's configuration out of the server,
// and `-u` keeps a screen's glyphs whatever the locale says.
func (t *Terminal) run(ctx context.Context, args ...string) (string, error) {
	return t.call(ctx, false, args...)
}

// call is one client call. A call that can start the server outlives the worker's service, because
// the server forks from it and holds every pane (D20).
func (t *Terminal) call(ctx context.Context, starts bool, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	process, err := t.runner.Start(ctx, ports.ProcessSpec{
		Path: t.binary, Args: append([]string{"-u", "-f", os.DevNull, "-S", t.socket}, args...), Env: t.env,
		Stdout: &stdout, Stderr: &stderr, Detached: true, Outlive: starts,
	})
	if err != nil {
		return "", err
	}
	finished := make(chan error, 1)
	go func() {
		status, err := process.Wait()
		if err == nil && (status.Code != 0 || status.Signal != "") {
			err = fmt.Errorf("tmux %s: %s", args[0], strings.TrimSpace(stderr.String()))
			if isAbsent(stderr.String()) {
				err = fmt.Errorf("%w: %v", ports.ErrNotFound, err)
			}
		}
		finished <- err
	}()
	select {
	case err := <-finished:
		return stdout.String(), err
	case <-ctx.Done():
		_ = process.Signal(ports.SignalKill)
		return "", ctx.Err()
	case <-time.After(commandLimit):
		_ = process.Signal(ports.SignalKill)
		return "", fmt.Errorf("tmux %s did not answer", args[0])
	}
}

func isAbsent(text string) bool {
	for _, phrase := range absent {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// target names a pane by exact session name, so a prefix never matches another lane.
func target(name string) string { return "=" + name + ":" }

// Open starts a pane. The options go in the same call, so a process that exits at once
// still leaves a dead pane to read. Two or more words after `new-session` run directly,
// with no shell, and a dead pane stays until Close, so an end reads after a restart.
func (t *Terminal) Open(ctx context.Context, spec ports.PaneSpec) (ports.Pane, error) {
	if len(spec.Command) < 2 {
		return ports.Pane{}, errors.New("a pane's command needs a path and an argument, or tmux hands it to a shell")
	}
	args := []string{
		"start-server", ";",
		"set-option", "-wg", "remain-on-exit", "on", ";",
		"set-option", "-g", "status", "off", ";",
		"new-session", "-d", "-P", "-F", "#{pane_pid}", "-s", spec.Name,
		"-x", strconv.Itoa(spec.Width), "-y", strconv.Itoa(spec.Height), "-c" + spec.Dir, "--",
	}
	out, err := t.call(ctx, true, append(args, spec.Command...)...)
	if err != nil {
		return ports.Pane{}, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return ports.Pane{}, fmt.Errorf("tmux named no process: %q", out)
	}
	return ports.Pane{Name: spec.Name, PID: pid}, nil
}

// signalNames reads `pane_dead_signal`, which tmux prints as a lowercase short name where the C
// library names signals, as on macOS, and as a number where it does not, as on Linux.
var signalNames = map[string]ports.Signal{
	"int": ports.SignalInterrupt, "term": ports.SignalTerminate, "kill": ports.SignalKill,
	strconv.Itoa(int(syscall.SIGINT)): ports.SignalInterrupt, strconv.Itoa(int(syscall.SIGTERM)): ports.SignalTerminate,
	strconv.Itoa(int(syscall.SIGKILL)): ports.SignalKill,
}

// Pane reads one pane. A dead pane has no process ID any more, which is no loss, because
// nothing may be signaled through one.
func (t *Terminal) Pane(ctx context.Context, name string) (ports.Pane, error) {
	out, err := t.run(ctx, "list-panes", "-t", target(name), "-F", paneFormat)
	if err != nil {
		return ports.Pane{}, err
	}
	fields := strings.Split(strings.TrimRight(out, "\n"), "|")
	if len(fields) != 4 {
		return ports.Pane{}, fmt.Errorf("tmux answered %q", out)
	}
	pane := ports.Pane{Name: name, Dead: fields[1] == "1"}
	if !pane.Dead {
		if pane.PID, err = strconv.Atoi(fields[0]); err != nil {
			return ports.Pane{}, fmt.Errorf("tmux answered %q", out)
		}
		return pane, nil
	}
	pane.Exit.Code, _ = strconv.Atoi(fields[2])
	if fields[3] != "" && fields[3] != "0" {
		pane.Exit = ports.ExitStatus{Signal: signalNames[fields[3]]}
		if pane.Exit.Signal == "" {
			pane.Exit.Signal = ports.Signal(fields[3])
		}
	}
	return pane, nil
}

// Capture returns the visible screen.
func (t *Terminal) Capture(ctx context.Context, name string) (string, error) {
	return t.run(ctx, "capture-pane", "-p", "-t", target(name))
}

// Keys sends named keys.
func (t *Terminal) Keys(ctx context.Context, name string, keys ...string) error {
	_, err := t.run(ctx, append([]string{"send-keys", "-t", target(name)}, keys...)...)
	return err
}

// Type sends literal text.
func (t *Terminal) Type(ctx context.Context, name, text string) error {
	_, err := t.run(ctx, "send-keys", "-t", target(name), "-l", "--", text)
	return err
}

var signalNumbers = map[ports.Signal]syscall.Signal{
	ports.SignalInterrupt: syscall.SIGINT, ports.SignalTerminate: syscall.SIGTERM, ports.SignalKill: syscall.SIGKILL,
}

// Signal sends a signal to the exact process a pane runs now. A pane whose process ended
// holds nothing to signal, because its ID may already name another process.
func (t *Terminal) Signal(ctx context.Context, name string, signal ports.Signal) error {
	number, ok := signalNumbers[signal]
	if !ok {
		return fmt.Errorf("no such signal: %s", signal)
	}
	pane, err := t.Pane(ctx, name)
	if err != nil {
		return err
	}
	if pane.Dead {
		return nil
	}
	if err := syscall.Kill(pane.PID, number); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// Close removes a pane. A last session's end takes the server with it, which leaves no
// process behind a worker that has nothing left to run.
func (t *Terminal) Close(ctx context.Context, name string) error {
	_, err := t.run(ctx, "kill-session", "-t", "="+name)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	return err
}

var _ ports.Terminal = (*Terminal)(nil)
