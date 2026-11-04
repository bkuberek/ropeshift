//go:build chaos

// Package chaos kills real processes at named steps and checks that nothing is
// half-applied. Every check here has a broken variant that it must catch.
//
// The harness refuses anything but its own run directory (NFR-TEST-3). A target
// needs a sentinel file with a matching nonce, its database must sit inside the
// directory, and no live installation may be running on the machine.
package chaos

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const sentinelName = ".ropeshift-chaos-sentinel"

// ErrRefused marks every refusal, so a test can tell one from an I/O failure.
var ErrRefused = errors.New("refused: not a disposable target")

func refuse(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, args...))
}

// Target is one disposable ledger.
type Target struct {
	Dir   string `json:"dir"`
	Path  string `json:"path"`
	Nonce string `json:"nonce"`
}

// Installation says where a live ropeshift would show.
type Installation struct {
	// SocketDirs are the directories that hold live sockets (D19).
	SocketDirs []string
	// Service names a live service, or returns "".
	Service func() (string, error)
}

// shortTempDir makes a directory whose path leaves room for a unix socket.
func shortTempDir(prefix string) (string, error) {
	base := os.TempDir()
	if len(base) > 60 {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, prefix)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(dir)
}

// NewTarget creates a run directory and writes its sentinel.
func NewTarget() (Target, error) {
	dir, err := shortTempDir("rschaos")
	if err != nil {
		return Target{}, err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return Target{}, err
	}
	nonce := hex.EncodeToString(raw)
	if err := os.WriteFile(filepath.Join(dir, sentinelName), []byte(nonce), 0o600); err != nil {
		return Target{}, err
	}
	return Target{Dir: dir, Path: filepath.Join(dir, "ledger.db"), Nonce: nonce}, nil
}

// LiveInstallation looks where solo mode really runs on this machine.
func LiveInstallation() Installation {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".ropeshift", "run"))
	}
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		dirs = append(dirs, filepath.Join(runtimeDir, "ropeshift"))
	}
	return Installation{SocketDirs: dirs, Service: liveService}
}

// liveService asks the service manager and the process table. A manager that is
// not on this machine has nothing to report.
func liveService() (string, error) {
	if out, err := exec.Command("pgrep", "-x", "ropeshiftd").Output(); err == nil && len(out) > 0 {
		return "a ropeshiftd process", nil
	}
	var probe *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		probe = exec.Command("launchctl", "list")
	default:
		probe = exec.Command("systemctl", "--user", "list-units", "--no-legend", "--state=active", "ropeshift*")
	}
	out, err := probe.Output()
	if err != nil {
		var notFound *exec.Error
		if errors.As(err, &notFound) {
			return "", nil
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", nil
		}
		return "", err
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.Contains(strings.ToLower(line), "ropeshift") {
			return strings.TrimSpace(line), nil
		}
	}
	return "", nil
}

// Verify refuses anything not provably disposable. It runs in the parent and in
// every child, so an environment variable alone cannot aim a child at a real
// ledger.
func Verify(target Target, live Installation) error {
	if target.Dir == "" || !filepath.IsAbs(target.Dir) || target.Nonce == "" {
		return refuse("no run directory or nonce")
	}
	got, err := os.ReadFile(filepath.Join(target.Dir, sentinelName))
	if err != nil {
		return refuse("no sentinel in the run directory")
	}
	if string(got) != target.Nonce {
		return refuse("the sentinel holds another nonce")
	}
	if !inside(target.Dir, target.Path) {
		return refuse("the database is outside the run directory")
	}
	for _, dir := range live.SocketDirs {
		if socket, err := liveSocket(dir); err != nil {
			return err
		} else if socket != "" {
			return refuse("a live socket answered: %s", filepath.Base(socket))
		}
	}
	if live.Service != nil {
		service, err := live.Service()
		if err != nil {
			return fmt.Errorf("probe services: %w", err)
		}
		if service != "" {
			return refuse("a live service is running: %s", service)
		}
	}
	return nil
}

func inside(dir, path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	rel, err := filepath.Rel(dir, filepath.Clean(path))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, "../")
}

// liveSocket finds a socket that accepts a connection. A stale file left by a
// crash answers nothing and refuses nothing.
func liveSocket(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.Type()&fs.ModeSocket == 0 {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return path, nil
		}
	}
	return "", nil
}
