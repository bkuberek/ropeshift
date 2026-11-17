package entry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"ropeshift.com/ropeshift/internal/control"
	buildversion "ropeshift.com/ropeshift/internal/version"
)

// installedEvery is how often the daemon reads the build installed at its own path. A package manager
// replaces that build while the daemon runs, so the answer changes rarely and a slow read is enough.
const installedEvery = time.Minute

// installedTimeout bounds one read of the installed build, which starts that build once.
const installedTimeout = 5 * time.Second

// installedOutputMax is the most of the binary's output that is read. Its version line is under 300 bytes.
const installedOutputMax = 64 << 10

// installedBuild is the last answer to what version the binary at the daemon's own path reports. The
// daemon's health reads it from here, so asking for health never touches the disk.
type installedBuild struct {
	path string
	// read starts the binary at path and returns the version it reports.
	read func(ctx context.Context, path string) (string, error)
	// note says what a read found when the outcome changed. It is nil when nothing listens.
	note func(format string, args ...any)

	mu    sync.Mutex
	found buildversion.Installed
	// failure is the failure the last read ended with, or empty when it succeeded.
	failure string
}

// liveInstalledBuild reads the binary this daemon was started from, and notes a change in what it finds on
// the daemon's log, which is stderr.
func liveInstalledBuild(log io.Writer) *installedBuild {
	note := func(format string, args ...any) { fmt.Fprintf(log, "ropeshiftd: "+format+"\n", args...) }
	path, err := daemonPath()
	if err != nil {
		// With no path to read, the answer stays empty, and the first read says why.
		return &installedBuild{read: func(context.Context, string) (string, error) { return "", err }, note: note}
	}
	return &installedBuild{path: path, read: readInstalledVersion, note: note}
}

// daemonPath is where this daemon's binary is installed: the absolute path the service manager started it
// by, which is the path a package manager's upgrade replaces, or else its executable's own path. A path
// inside a Homebrew Cellar names a version, so the opt link of its keg stands for it, and an upgrade shows.
func daemonPath() (string, error) {
	if len(os.Args) > 0 && filepath.IsAbs(os.Args[0]) {
		return preferOptLink(os.Args[0]), nil
	}
	return executablePath()
}

// Installed is what the last read found. It is empty until the first read ends, and after a read that
// failed.
func (b *installedBuild) Installed() buildversion.Installed {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.found
}

// tick reads the installed build again every installedEvery. A read that fails leaves nothing to say, so
// the answer is cleared instead of kept, and the failure is noted once, until the outcome changes.
func (b *installedBuild) tick() control.Tick {
	return control.Tick{Name: "installed-build", Every: installedEvery, Run: func(ctx context.Context) error {
		read := buildversion.Installed{Path: b.path}
		var err error
		read.Version, err = b.read(ctx, b.path)
		if err != nil {
			read.Version = ""
		}
		b.mu.Lock()
		b.found = read
		b.mu.Unlock()
		b.noteChange(read, err)
		return nil
	}}
}

// noteChange says what a read found, when it found something other than the read before it did: a failure
// with a reason the last read did not have, or a success after a failure.
func (b *installedBuild) noteChange(read buildversion.Installed, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	failure := ""
	if err != nil {
		failure = err.Error()
	}
	if failure == b.failure {
		return
	}
	b.failure = failure
	if b.note == nil {
		return
	}
	if err != nil {
		b.note("installed build: cannot read %s, so no stale daemon is reported: %v", read.Path, err)
		return
	}
	b.note("installed build: reads %s at %s again", read.Version, read.Path)
}

// readInstalledVersion starts the binary at path as ropeshiftd, which answers with its own build and asks
// no daemon, and returns the version it reports. It runs with an empty environment, so it reads no
// configuration and sees no credential.
func readInstalledVersion(ctx context.Context, path string) (string, error) {
	if base := filepath.Base(path); base != commandCLI && base != commandDaemon {
		return "", fmt.Errorf("%s is no %s binary", path, commandCLI)
	}
	ctx, cancel := context.WithTimeout(ctx, installedTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version", "--json")
	cmd.Args[0] = commandDaemon
	cmd.Env = []string{}
	cmd.WaitDelay = time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	// The binary is not trusted to stop writing, so one byte past the limit is read, and a binary that
	// writes it is stopped.
	out, readErr := io.ReadAll(io.LimitReader(stdout, installedOutputMax+1))
	over := len(out) > installedOutputMax
	if over {
		cancel()
	}
	waitErr := cmd.Wait()
	switch {
	case over:
		return "", fmt.Errorf("%s wrote more than %d bytes", path, installedOutputMax)
	case readErr != nil:
		return "", readErr
	case waitErr != nil:
		return "", waitErr
	}
	var reported buildversion.Info
	if err := json.Unmarshal(out, &reported); err != nil {
		return "", err
	}
	if !buildversion.IsRelease(reported.Version) {
		return "", fmt.Errorf("the binary reports %q, which is no release", reported.Version)
	}
	return reported.Version, nil
}
