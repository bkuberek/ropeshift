//go:build chaos

package chaos

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newGuardTarget(t *testing.T) Target {
	t.Helper()
	target, err := NewTarget()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(target.Dir) })
	return target
}

func noInstallation() Installation { return Installation{} }

// TestChaosRefusesLiveInstallation holds every refusal in one table.
func TestChaosRefusesLiveInstallation(t *testing.T) {
	tests := []struct {
		name   string
		target func(t *testing.T) Target
		live   func(t *testing.T) Installation
		reason string
	}{
		{
			name: "no sentinel",
			target: func(t *testing.T) Target {
				target := newGuardTarget(t)
				if err := os.Remove(filepath.Join(target.Dir, sentinelName)); err != nil {
					t.Fatal(err)
				}
				return target
			},
			reason: "no sentinel",
		},
		{
			name: "a wrong nonce",
			target: func(t *testing.T) Target {
				target := newGuardTarget(t)
				target.Nonce = strings.Repeat("0", 32)
				return target
			},
			reason: "another nonce",
		},
		{
			name: "no nonce",
			target: func(t *testing.T) Target {
				target := newGuardTarget(t)
				target.Nonce = ""
				return target
			},
			reason: "no run directory",
		},
		{
			name: "a relative run directory",
			target: func(t *testing.T) Target {
				target := newGuardTarget(t)
				target.Dir = "run"
				return target
			},
			reason: "no run directory",
		},
		{
			name: "a path outside the run directory",
			target: func(t *testing.T) Target {
				target := newGuardTarget(t)
				target.Path = filepath.Join(filepath.Dir(target.Dir), "ledger.db")
				return target
			},
			reason: "outside the run directory",
		},
		{
			name: "a path that climbs out of the run directory",
			target: func(t *testing.T) Target {
				target := newGuardTarget(t)
				target.Path = target.Dir + "/../ledger.db"
				return target
			},
			reason: "outside the run directory",
		},
		{
			name: "a live socket",
			target: func(t *testing.T) Target {
				return newGuardTarget(t)
			},
			live: func(t *testing.T) Installation {
				dir, err := shortTempDir("rsdaemon")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(dir) })
				listener, err := net.Listen("unix", filepath.Join(dir, "control.sock"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = listener.Close() })
				return Installation{SocketDirs: []string{dir}}
			},
			reason: "live socket",
		},
		{
			name: "a live service",
			target: func(t *testing.T) Target {
				return newGuardTarget(t)
			},
			live: func(*testing.T) Installation {
				return Installation{Service: func() (string, error) { return "ropeshiftd", nil }}
			},
			reason: "live service",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			live := noInstallation()
			if tt.live != nil {
				live = tt.live(t)
			}
			err := Verify(tt.target(t), live)
			if !errors.Is(err, ErrRefused) {
				t.Fatalf("got %v, want a refusal", err)
			}
			if !strings.Contains(err.Error(), tt.reason) {
				t.Errorf("refused for the wrong reason: %v", err)
			}
		})
	}
}

func TestGuardAcceptsItsOwnTarget(t *testing.T) {
	target := newGuardTarget(t)
	if err := Verify(target, noInstallation()); err != nil {
		t.Fatal(err)
	}
}

func TestGuardIgnoresAStaleSocket(t *testing.T) {
	dir, err := shortTempDir("rsstale")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "control.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err := Verify(newGuardTarget(t), Installation{SocketDirs: []string{dir}}); err != nil {
		t.Fatalf("a socket nothing answers refused the run: %v", err)
	}
}

// TestChildRefusesAWrongTarget proves that a disabled guard would show: a child
// aimed at a target with a wrong nonce must exit with the refusal code and touch
// nothing.
func TestChildRefusesAWrongTarget(t *testing.T) {
	target := newGuardTarget(t)
	aimed := target
	aimed.Nonce = strings.Repeat("0", 32)
	child, err := startChild(childSpec{Role: "note", Target: aimed, Scope: "w"})
	if err != nil {
		t.Fatal(err)
	}
	child.wait()
	if code, signaled := child.exitCode(); signaled || code != exitRefused {
		t.Fatalf("exit %d signaled %v: %s", code, signaled, child.stderr.String())
	}
	if _, err := os.Stat(target.Path); !os.IsNotExist(err) {
		t.Errorf("a refused child created the database: %v", err)
	}
}
