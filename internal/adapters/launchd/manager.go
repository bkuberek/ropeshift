package launchd

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ropeshift.dev/ropeshift/internal/ports"
)

// Manager loads and removes agents in one launchd domain, through launchctl.
type Manager struct {
	Runner ports.ProcessRunner
	// Tool is launchctl's absolute path.
	Tool string
	// Domain is the user's domain, such as gui/501.
	Domain string
	// Dir is where the user's agents live.
	Dir string
	// Env is launchctl's whole environment.
	Env []string
	// Patience bounds the wait for launchd to let an unloaded label go.
	Patience time.Duration
}

// Install writes an agent's file and loads it, which starts it. An agent already loaded under the
// label goes first, because launchd refuses to load a label twice, and its lanes keep running.
func (m Manager) Install(ctx context.Context, label string, plist []byte) (string, error) {
	file := m.file(label)
	if err := writeFile(file, plist); err != nil {
		return "", err
	}
	if err := m.unload(ctx, label); err != nil {
		return "", err
	}
	if err := m.run(ctx, "bootstrap", m.Domain, file); err != nil {
		return "", err
	}
	return file, nil
}

// Uninstall stops an agent, unloads it, and removes its file. Its lanes keep running.
func (m Manager) Uninstall(ctx context.Context, label string) (string, error) {
	file := m.file(label)
	if _, err := os.Stat(file); err != nil {
		return "", err
	}
	if err := m.unload(ctx, label); err != nil {
		return "", err
	}
	return file, os.Remove(file)
}

func (m Manager) file(label string) string { return filepath.Join(m.Dir, label+".plist") }

// defaultPatience is how long launchd may hold a label after bootout returns. It ends the job first,
// and the job's own stop has a few seconds.
const defaultPatience = 30 * time.Second

// unload removes the label from the domain, when the domain holds it. bootout returns before launchd
// lets the label go, and a bootstrap until then fails, so this waits for the domain to drop it.
func (m Manager) unload(ctx context.Context, label string) error {
	target := m.Domain + "/" + label
	if m.run(ctx, "print", target) != nil {
		return nil
	}
	if err := m.run(ctx, "bootout", target); err != nil {
		return err
	}
	deadline := time.Now().Add(cmp.Or(m.Patience, defaultPatience))
	for m.run(ctx, "print", target) == nil {
		if time.Now().After(deadline) {
			return fmt.Errorf("launchd still holds %s after its bootout", target)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil
}

func (m Manager) run(ctx context.Context, args ...string) error {
	var stderr bytes.Buffer
	process, err := m.Runner.Start(ctx, ports.ProcessSpec{Path: m.Tool, Args: args, Env: m.Env, Stdout: &bytes.Buffer{}, Stderr: &stderr})
	if err != nil {
		return err
	}
	status, err := process.Wait()
	if err != nil {
		return err
	}
	if status.Code != 0 || status.Signal != "" {
		return fmt.Errorf("launchctl %s: exit %d %s: %s", args[0], status.Code, status.Signal, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// writeFile replaces a file whole, so launchd never reads half of one. An agent's file is readable
// by everyone and writable only by its owner, as launchd requires.
func writeFile(file string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	next := file + ".new"
	if err := os.WriteFile(next, content, 0o644); err != nil {
		return err
	}
	if err := os.Chmod(next, 0o644); err != nil {
		return errors.Join(err, os.Remove(next))
	}
	return os.Rename(next, file)
}
