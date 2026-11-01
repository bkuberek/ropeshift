package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"syscall"
	"time"

	"ropeshift.dev/ropeshift/internal/api/shimlink"
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

func liveRows(rows []listedSession) []listedSession {
	return slices.DeleteFunc(slices.Clone(rows), func(row listedSession) bool { return !processExists(row.PID) })
}

// Find looks for a session an earlier process started, and changes nothing. Each piece
// of evidence proves less than it appears to: a shim that answers a ping is alive, an
// exit in the journal is dead, and two processes that are both gone are dead. Anything
// less is unknown, because unknown restarts nothing. The listing is a second opinion,
// and it can only hold a session back from reading dead.
func (d *Driver) Find(ctx context.Context, spec ports.SessionSpec) (ports.Located, error) {
	handle := ports.SessionHandle{ID: sessionHandleID(spec)}
	if state, err := readPaneState(spec); err == nil {
		return d.findPane(ctx, spec, state)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ports.Located{}, err
	}
	rows, listing := d.listing(ctx, spec)
	state, err := shimlink.ReadState(shimDir(spec))
	switch {
	case errors.Is(err, os.ErrNotExist):
		if listing != nil {
			return ports.Located{}, listing
		}
		live := liveRows(rows)
		if len(live) == 0 {
			return ports.Located{}, nil
		}
		handle.PID = live[0].PID
		return ports.Located{Session: lanes.Session{Found: true, Liveness: lanes.LivenessUnknown}, Handle: handle}, nil
	case err != nil:
		return ports.Located{}, err
	}
	handle.PID = state.ShimPID
	if answersPing(ctx, shimSocket(spec)) {
		return ports.Located{Session: lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true}, Handle: handle}, nil
	}
	ended, err := shimlink.Ended(shimDir(spec))
	if err != nil {
		return ports.Located{}, err
	}
	gone := ended || (!processExists(state.ShimPID) && !processExists(state.ChildPID) && len(liveRows(rows)) == 0)
	if gone {
		return ports.Located{Session: lanes.Session{Found: true, Liveness: lanes.LivenessDead, Readable: true}, Handle: handle}, nil
	}
	return ports.Located{Session: lanes.Session{Found: true, Liveness: lanes.LivenessUnknown}, Handle: handle}, nil
}

func answersPing(ctx context.Context, socket string) bool {
	attempt, cancel := context.WithTimeout(ctx, shimDialDeadline)
	defer cancel()
	client, err := shimlink.Dial(attempt, socket)
	return err == nil && client.Ping(attempt) == nil
}

// Adopt takes over a session from where a worker stopped. A shim that answers carries on
// the stream, and a shim that has gone left its journal, so a session that ended while
// the worker was away still ends in order, with nothing of its last turn lost.
func (d *Driver) Adopt(ctx context.Context, h ports.SessionHandle, from ports.AdoptFrom) error {
	spec := from.Spec
	if spec.ConfigDir == "" {
		return fmt.Errorf("%s: %w", h.ID, ports.ErrNoSession)
	}
	if state, err := readPaneState(spec); err == nil {
		return d.adoptPane(ctx, h, from, state)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := shimlink.ReadState(shimDir(spec)); err != nil {
		return fmt.Errorf("%s: %w: %v", h.ID, ports.ErrNoSession, err)
	}
	attempt, cancel := context.WithTimeout(ctx, shimDialDeadline)
	defer cancel()
	if client, err := shimlink.Dial(attempt, shimSocket(spec)); err == nil {
		_, err := d.follow(ctx, spec, h, client, from.Cursor, from.Cost)
		return err
	}
	log, err := openStreamLog(spec)
	if err != nil {
		return err
	}
	s := d.newSession(spec, h, nil, from.Cost)
	s.last = from.Cursor
	d.keep(h.ID, s)
	go func() {
		if log != nil {
			defer log.Close()
		}
		s.drain(log)
	}()
	return nil
}
