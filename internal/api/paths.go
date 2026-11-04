package api

import (
	"errors"
	"fmt"
	"path/filepath"
)

// MaxSocketPath is the longest socket path. A sockaddr_un holds 104 bytes on macOS
// with its terminating NUL, and more on Linux, so one limit serves both.
const MaxSocketPath = 103

// ControlSocketName is the control plane's socket inside the run directory.
const ControlSocketName = "control.sock"

// LedgerFileName and JournalFileName are the stores inside the state directory.
const (
	LedgerFileName  = "ledger.db"
	JournalFileName = "journal.db"
	// TerminalSocketName is short, because a socket path has a 104-byte limit.
	TerminalSocketName = "tmux"
)

// Environment names that move the directories, for a second installation on one
// machine and for tests.
const (
	EnvStateDir = "ROPESHIFT_STATE_DIR"
	EnvRunDir   = "ROPESHIFT_RUN_DIR"
)

// The web listener's address. EnvWeb replaces the default, for a second installation on one machine and
// for tests, and WebOff turns the listener off.
const (
	DefaultWebAddress = "127.0.0.1:7317"
	EnvWeb            = "ROPESHIFT_WEB"
	WebOff            = "off"
)

// The OTLP receiver's address. EnvTelemetry replaces the default, and WebOff turns the receiver off,
// which leaves every lane's telemetry unset. A fixed port keeps a lane that outlived its worker
// pointed at the receiver that came back.
const (
	DefaultTelemetryAddress = "127.0.0.1:7318"
	EnvTelemetry            = "ROPESHIFT_TELEMETRY"
)

// Paths says where one installation keeps its files (D19).
type Paths struct {
	StateDir string
	RunDir   string
}

// TerminalSocket is the private terminal server's socket (D20).
func (p Paths) TerminalSocket() string { return filepath.Join(p.RunDir, TerminalSocketName) }

// ControlSocket is the control plane's socket.
func (p Paths) ControlSocket() string { return filepath.Join(p.RunDir, ControlSocketName) }

// ResolvePaths applies D19. It takes the operating system, the environment, and the
// home directory as inputs, so a test can check every platform on any machine.
func ResolvePaths(goos string, getenv func(string) string, home string) (Paths, error) {
	var p Paths
	switch goos {
	case "darwin":
		p.StateDir = filepath.Join(home, "Library", "Application Support", "ropeshift")
		p.RunDir = filepath.Join(home, ".ropeshift", "run")
	case "linux":
		state := getenv("XDG_STATE_HOME")
		if state == "" {
			state = filepath.Join(home, ".local", "state")
		}
		p.StateDir = filepath.Join(state, "ropeshift")
		p.RunDir = filepath.Join(home, ".ropeshift", "run")
		if runtime := getenv("XDG_RUNTIME_DIR"); runtime != "" {
			p.RunDir = filepath.Join(runtime, "ropeshift")
		}
	default:
		return Paths{}, fmt.Errorf("no paths for %s", goos)
	}
	if dir := getenv(EnvStateDir); dir != "" {
		p.StateDir = dir
	}
	if dir := getenv(EnvRunDir); dir != "" {
		p.RunDir = dir
	}
	if len(p.ControlSocket()) > MaxSocketPath {
		return Paths{}, errors.New("socket path is over the limit: " + p.ControlSocket())
	}
	return p, nil
}
