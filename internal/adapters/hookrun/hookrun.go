// Package hookrun is the HookRunner on real processes. It runs what a profile names with
// the environment it is given, and no shell reads the command.
package hookrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ropeshift.com/ropeshift/internal/ports"
)

const (
	// maxOutput bounds what a run keeps, because a chatty hook must not fill a journal.
	maxOutput = 8 << 10
	// stopGrace is how long a command gets to end after SIGTERM.
	stopGrace = 5 * time.Second
)

// Runner spawns through a ProcessRunner, so a spawn takes the same checks as any other.
type Runner struct {
	Processes ports.ProcessRunner
}

// tail keeps the end of what a command prints. A process writes from one goroutine, and a
// result reads after it ends, but a killed process can still be flushing.
type tail struct {
	mu   sync.Mutex
	kept []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.kept = append(t.kept, p...)
	if len(t.kept) > maxOutput {
		t.kept = t.kept[len(t.kept)-maxOutput:]
	}
	return len(p), nil
}

func (t *tail) bytes() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return bytes.Clone(t.kept)
}

// Run runs one command to its end.
func (r Runner) Run(ctx context.Context, command ports.HookCommand) (ports.HookResult, error) {
	if len(command.Argv) == 0 {
		return ports.HookResult{}, errors.New("a hook names no command")
	}
	binary, err := resolve(command.Argv[0], command.Dir, command.Env)
	if err != nil {
		return ports.HookResult{}, err
	}
	output := &tail{}
	process, err := r.Processes.Start(ctx, ports.ProcessSpec{
		Path: binary, Args: command.Argv[1:], Dir: command.Dir, Env: command.Env, Stdout: output, Stderr: output,
	})
	if err != nil {
		return ports.HookResult{}, err
	}
	finished := make(chan ports.ExitStatus, 1)
	go func() {
		status, _ := process.Wait()
		finished <- status
	}()
	var expired <-chan time.Time
	if command.Timeout > 0 {
		expired = time.After(command.Timeout)
	}
	select {
	case status := <-finished:
		return ports.HookResult{Exit: status, Output: output.bytes()}, nil
	case <-expired:
	case <-ctx.Done():
	}
	_ = process.Signal(ports.SignalTerminate)
	select {
	case status := <-finished:
		return ports.HookResult{Exit: status, Output: output.bytes()}, nil
	case <-time.After(stopGrace):
	}
	_ = process.Signal(ports.SignalKill)
	return ports.HookResult{Exit: <-finished, Output: output.bytes()}, nil
}

// resolve turns a command's first word into an absolute path, because a spawn names one.
func resolve(word, dir string, env []string) (string, error) {
	if filepath.IsAbs(word) {
		return word, nil
	}
	if strings.ContainsRune(word, filepath.Separator) {
		return filepath.Join(dir, word), nil
	}
	for _, entry := range env {
		list, ok := strings.CutPrefix(entry, "PATH=")
		if !ok {
			continue
		}
		for _, candidate := range filepath.SplitList(list) {
			path := filepath.Join(candidate, word)
			if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 && filepath.IsAbs(path) {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("%s is not on the hook's PATH", word)
}

var _ ports.HookRunner = Runner{}
