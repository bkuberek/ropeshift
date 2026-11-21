package api

import (
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
	// InitDaemonLogName is the log of the daemon that ropeshift init runs for its own length, inside the state
	// directory. A run that fails keeps it.
	InitDaemonLogName = "init-daemon.log"
)

// Environment names that move the directories, for a second installation on one
// machine and for tests.
const (
	EnvStateDir = "ROPESHIFT_STATE_DIR"
	EnvRunDir   = "ROPESHIFT_RUN_DIR"
	// EnvWorktreeDir replaces the directory that holds lane worktrees. With a state directory set and this
	// unset, the worktrees go under the state directory.
	EnvWorktreeDir = "ROPESHIFT_WORKTREE_DIR"
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

// EnvServiceManager names what runs the daemon as a service: "brew" in the Homebrew formula's service, and
// "ropeshift" in a unit that `ropeshift service install` writes. The daemon reads it once, to name the
// right command when its installed build is newer than the one it runs. Unset, the daemon guesses from
// the path of its binary.
const EnvServiceManager = "ROPESHIFT_SERVICE_MANAGER"

// Paths says where one installation keeps its files (D19).
type Paths struct {
	StateDir string
	RunDir   string
	// WorktreeDir holds the built-in root of every lane's worktree, outside every clone (FR-PROF-3).
	WorktreeDir string
}

// TerminalSocket is the private terminal server's socket (D20).
func (p Paths) TerminalSocket() string { return filepath.Join(p.RunDir, TerminalSocketName) }

// ControlSocket is the control plane's socket.
func (p Paths) ControlSocket() string { return filepath.Join(p.RunDir, ControlSocketName) }

// InitDaemonLog is where the daemon ropeshift init starts writes its output.
func (p Paths) InitDaemonLog() string { return filepath.Join(p.StateDir, InitDaemonLogName) }

// ResolvePaths applies D19. It takes the operating system, the environment, and the
// home directory as inputs, so a test can check every platform on any machine.
func ResolvePaths(goos string, getenv func(string) string, home string) (Paths, error) {
	var p Paths
	switch goos {
	case "darwin":
		p.StateDir = filepath.Join(home, "Library", "Application Support", "ropeshift")
		p.RunDir = filepath.Join(home, ".ropeshift", "run")
		p.WorktreeDir = filepath.Join(home, ".ropeshift", "worktrees")
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
		// A relative XDG_DATA_HOME is invalid, and the specification has it ignored.
		data := getenv("XDG_DATA_HOME")
		if !filepath.IsAbs(data) {
			data = filepath.Join(home, ".local", "share")
		}
		p.WorktreeDir = filepath.Join(data, "ropeshift", "worktrees")
	default:
		return Paths{}, fmt.Errorf("no paths for %s", goos)
	}
	if dir := getenv(EnvStateDir); dir != "" {
		p.StateDir = dir
		// A second installation and every test keep their worktrees apart from the person's own.
		p.WorktreeDir = filepath.Join(dir, "worktrees")
	}
	if dir := getenv(EnvRunDir); dir != "" {
		p.RunDir = dir
	}
	if dir := getenv(EnvWorktreeDir); dir != "" {
		if !filepath.IsAbs(dir) {
			return Paths{}, fmt.Errorf("%s must be an absolute path, not %q", EnvWorktreeDir, dir)
		}
		p.WorktreeDir = dir
	}
	if len(p.ControlSocket()) > MaxSocketPath {
		return Paths{}, SocketPathTooLong(p.ControlSocket())
	}
	return p, nil
}

// SocketPathTooLong is the error for a socket path over MaxSocketPath. It names the
// limit, the path, and the way out.
func SocketPathTooLong(socket string) error {
	return fmt.Errorf("socket path is over the limit (%d bytes): %s; set %s to a shorter directory", MaxSocketPath, socket, EnvRunDir)
}
