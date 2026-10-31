package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"syscall"
	"time"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// listingTimeout bounds the one listing of an adoption. A call took up to two seconds
// in the spike, and one that passes this reads as incomplete.
const listingTimeout = 15 * time.Second

// listedSession is one row of `claude agents --json`, read by field name.
type listedSession struct {
	PID int `json:"pid"`
}

// listing runs `claude agents --json --cwd <worktree>`, the one question it answers
// well: which sessions are alive in this worktree. A hung process reads idle and a
// killed one drops out with no record, so it is never a state source, and this is the
// only place ropeshift runs it. Each generation reads it once per process.
func (d *Driver) listing(ctx context.Context, spec ports.SessionSpec) ([]listedSession, error) {
	id := sessionHandleID(spec)
	d.mu.Lock()
	rows, read := d.listed[id]
	d.mu.Unlock()
	if read {
		return rows, nil
	}
	env := spec.Env
	if env == nil {
		env = d.versionEnv()
	}
	binary, err := findBinary(env)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	process, err := d.runner.Start(ctx, ports.ProcessSpec{
		Path: binary, Args: []string{"agents", "--json", "--cwd", spec.Dir}, Env: env, Stdout: &out,
	})
	if err != nil {
		return nil, err
	}
	finished := make(chan error, 1)
	go func() {
		status, err := process.Wait()
		if err == nil && status.Code != 0 {
			err = fmt.Errorf("claude agents exited %d", status.Code)
		}
		finished <- err
	}()
	select {
	case err := <-finished:
		if err != nil {
			return nil, err
		}
	case <-time.After(listingTimeout):
		_ = process.Signal(ports.SignalKill)
		return nil, errors.New("claude agents did not answer")
	}
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		return nil, fmt.Errorf("claude agents: %w", err)
	}
	d.mu.Lock()
	d.listed[id] = rows
	d.mu.Unlock()
	return rows, nil
}

// processExists reports a process that has not exited, which proves nothing more.
func processExists(pid int) bool {
	return pid > 0 && !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// Find looks for a session an earlier process started. With no shim its pipes went with
// that process, so a session the listing shows can only end, and it reads unknown until
// it has: a restart must not start a second one beside it.
func (d *Driver) Find(ctx context.Context, spec ports.SessionSpec) (ports.Located, error) {
	rows, err := d.listing(ctx, spec)
	if err != nil {
		return ports.Located{}, err
	}
	rows = slices.DeleteFunc(slices.Clone(rows), func(row listedSession) bool { return !processExists(row.PID) })
	if len(rows) == 0 {
		return ports.Located{}, nil
	}
	return ports.Located{
		Session: lanes.Session{Found: true, Liveness: lanes.LivenessUnknown},
		Handle:  ports.SessionHandle{ID: sessionHandleID(spec), PID: rows[0].PID},
	}, nil
}

// Adopt has nothing to take over without a shim holding the session's pipes.
func (d *Driver) Adopt(_ context.Context, h ports.SessionHandle, _ ports.AdoptFrom) error {
	return fmt.Errorf("%s: %w", h.ID, ports.ErrNoSession)
}
