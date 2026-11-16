package entry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// installedBuild is the last answer to what version the binary at the daemon's own path reports. The
// daemon's health reads it from here, so asking for health never touches the disk.
type installedBuild struct {
	path string
	// read starts the binary at path and returns the version it reports.
	read func(ctx context.Context, path string) (string, error)

	mu    sync.Mutex
	found buildversion.Installed
}

// liveInstalledBuild reads the binary this daemon was started from.
func liveInstalledBuild() *installedBuild {
	path, err := daemonPath()
	if err != nil {
		// With no path to read, the answer stays empty and nothing is said.
		return &installedBuild{read: func(context.Context, string) (string, error) { return "", err }}
	}
	return &installedBuild{path: path, read: readInstalledVersion}
}

// daemonPath is where this daemon's binary is installed: the absolute path the service manager started it
// by, which is the path a package manager's upgrade replaces, or else its executable's own path.
func daemonPath() (string, error) {
	if len(os.Args) > 0 && filepath.IsAbs(os.Args[0]) {
		return os.Args[0], nil
	}
	return os.Executable()
}

// Installed is what the last read found. It is empty until the first read ends, and after a read that
// failed.
func (b *installedBuild) Installed() buildversion.Installed {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.found
}

// tick reads the installed build again every installedEvery. A read that fails leaves nothing to say, so
// the answer is cleared instead of kept.
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
		return nil
	}}
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
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, path, "version", "--json")
	cmd.Args[0] = commandDaemon
	cmd.Env = []string{}
	cmd.Stdout = &stdout
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return "", err
	}
	var reported buildversion.Info
	if err := json.NewDecoder(&stdout).Decode(&reported); err != nil {
		return "", err
	}
	if reported.Version == "" {
		return "", errors.New("the binary reports no version")
	}
	return reported.Version, nil
}
