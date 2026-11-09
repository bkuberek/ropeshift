package systemd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ropeshift.com/ropeshift/internal/ports"
)

// Manager writes, starts, and removes user units, through systemctl.
type Manager struct {
	Runner ports.ProcessRunner
	// Tool is systemctl's absolute path.
	Tool string
	// Dir is the user manager's directory of units.
	Dir string
	// Env is systemctl's whole environment, which reaches the user manager.
	Env []string
}

// Install writes a unit's file, makes the user manager read it, enables it for every later start
// of that manager, and starts it, or restarts one already running, so the daemon runs the unit
// just written. Its lanes keep running through a restart.
func (m Manager) Install(ctx context.Context, name string, unit []byte) (string, error) {
	file := m.file(name)
	if err := writeFile(file, unit); err != nil {
		return "", err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", name + ".service"}, {"restart", name + ".service"}} {
		if err := m.run(ctx, args...); err != nil {
			return "", err
		}
	}
	return file, nil
}

// Uninstall stops a unit, disables it, and removes its file. Its lanes keep running.
func (m Manager) Uninstall(ctx context.Context, name string) (string, error) {
	file := m.file(name)
	if _, err := os.Stat(file); err != nil {
		return "", err
	}
	if err := m.run(ctx, "disable", "--now", name+".service"); err != nil {
		return "", err
	}
	if err := os.Remove(file); err != nil {
		return "", err
	}
	return file, m.run(ctx, "daemon-reload")
}

func (m Manager) file(name string) string { return filepath.Join(m.Dir, name+".service") }

func (m Manager) run(ctx context.Context, args ...string) error {
	var stderr bytes.Buffer
	process, err := m.Runner.Start(ctx, ports.ProcessSpec{
		Path: m.Tool, Args: append([]string{"--user"}, args...), Env: m.Env, Stdout: &bytes.Buffer{}, Stderr: &stderr,
	})
	if err != nil {
		return err
	}
	status, err := process.Wait()
	if err != nil {
		return err
	}
	if status.Code != 0 || status.Signal != "" {
		return fmt.Errorf("systemctl %s: exit %d %s: %s", args[0], status.Code, status.Signal, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// writeFile replaces a file whole, so the manager never reads half of one.
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
