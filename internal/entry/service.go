package entry

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"ropeshift.dev/ropeshift/internal/adapters/launchd"
	"ropeshift.dev/ropeshift/internal/adapters/osexec"
	"ropeshift.dev/ropeshift/internal/adapters/systemd"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/api/server"
	"ropeshift.dev/ropeshift/internal/cli"
	"ropeshift.dev/ropeshift/internal/ports"
)

// The service's name when a person names none: a launchd label on macOS, and a unit's name on Linux.
const (
	launchdLabel = "dev.ropeshift.ropeshiftd"
	systemdUnit  = "ropeshiftd"
)

// serviceName is a name both managers accept, which is also one file's name.
var serviceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// serviceVariables are what a service keeps of the shell that installs it. A service manager starts
// the daemon with almost no environment, so the daemon would find no agent CLI, tmux, or git on its
// PATH, and an installation moved by a variable would be another installation. Nothing else is kept,
// so no credential reaches a service's file (NFR-SEC-1).
var serviceVariables = []string{
	"LANG", "LC_ALL", "LC_CTYPE", "PATH", api.EnvRunDir, api.EnvStateDir, api.EnvTelemetry, api.EnvWeb, "TZ", "XDG_STATE_HOME",
}

// services writes the daemon's service on this machine, through its service manager.
type services struct {
	goos string
	// lookup reads the installing shell's environment, and environ is all of it, which the manager's
	// own command gets, as it would from that shell.
	lookup  func(string) (string, bool)
	environ []string
	// executable is this command's path, and ropeshiftd is beside it (D5).
	executable string
	home       string
	uid        int
	runner     ports.ProcessRunner
	// tool finds launchctl or systemctl.
	tool func(name string) (string, error)
}

// liveServices is this machine.
func liveServices() (services, error) {
	executable, err := os.Executable()
	if err != nil {
		return services{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return services{}, err
	}
	return services{
		goos: runtime.GOOS, lookup: os.LookupEnv, environ: os.Environ(), executable: executable, home: home,
		uid: os.Getuid(), runner: osexec.Runner{}, tool: exec.LookPath,
	}, nil
}

// manager is one platform's way to write and start a service.
type manager interface {
	Install(ctx context.Context, name string, definition []byte) (string, error)
	Uninstall(ctx context.Context, name string) (string, error)
}

// install writes the daemon's service and starts it. Nothing is written for a service the daemon
// could not run: a bad name, a flag `ropeshiftd solo` refuses, a source read from no directory, or
// no ropeshiftd to run.
func (s services) install(ctx context.Context, name string, daemonArgs []string) (cli.Service, error) {
	name, err := s.name(name)
	if err != nil {
		return cli.Service{}, err
	}
	var refused bytes.Buffer
	opts, ok := parseSolo(daemonArgs, &refused)
	if !ok {
		return cli.Service{}, fmt.Errorf("%w: ropeshiftd solo refuses %q: %s", cli.ErrServiceUsage, daemonArgs, strings.TrimSpace(refused.String()))
	}
	for _, source := range opts.sources {
		if !filepath.IsAbs(source) {
			return cli.Service{}, fmt.Errorf("%w: a service reads --source %q from no directory, so name its absolute path", cli.ErrServiceUsage, source)
		}
	}
	daemon := filepath.Join(filepath.Dir(s.executable), commandDaemon)
	if info, err := os.Stat(daemon); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return cli.Service{}, fmt.Errorf("no %s beside %s, where packaging links it: %v", commandDaemon, s.executable, err)
	}
	paths, err := api.ResolvePaths(s.goos, s.getenv, s.home)
	if err != nil {
		return cli.Service{}, err
	}
	program := append([]string{daemon, "solo"}, daemonArgs...)
	var env []string
	for _, variable := range serviceVariables {
		if value, ok := s.lookup(variable); ok {
			env = append(env, variable+"="+value)
		}
	}
	var definition []byte
	switch s.goos {
	case "darwin":
		definition, err = launchd.Agent{Label: name, Program: program, Environment: env, Log: filepath.Join(paths.StateDir, name+".log")}.Render()
	case "linux":
		definition, err = systemd.Unit{Description: "ropeshift daemon", Command: program, Environment: env}.Render()
	}
	if err != nil {
		return cli.Service{}, fmt.Errorf("%w: %v", cli.ErrServiceUsage, err)
	}
	m, err := s.manager()
	if err != nil {
		return cli.Service{}, err
	}
	// launchd makes no directory for a log, so the installation's goes first.
	if s.goos == "darwin" {
		if err := server.PrivateDir(paths.StateDir); err != nil {
			return cli.Service{}, err
		}
	}
	file, err := m.Install(ctx, name, definition)
	return cli.Service{Name: name, Path: file}, err
}

// uninstall stops the daemon's service and removes its file. A lane runs outside the service, so
// none stops.
func (s services) uninstall(ctx context.Context, name string) (cli.Service, error) {
	name, err := s.name(name)
	if err != nil {
		return cli.Service{}, err
	}
	m, err := s.manager()
	if err != nil {
		return cli.Service{}, err
	}
	file, err := m.Uninstall(ctx, name)
	return cli.Service{Name: name, Path: file}, err
}

func (s services) name(name string) (string, error) {
	if name == "" {
		name = systemdUnit
		if s.goos == "darwin" {
			name = launchdLabel
		}
	}
	if !serviceName.MatchString(name) {
		return "", fmt.Errorf("%w: a service's name is letters, digits, dots, dashes, and underscores: %q", cli.ErrServiceUsage, name)
	}
	return name, nil
}

func (s services) getenv(name string) string {
	value, _ := s.lookup(name)
	return value
}

// manager is the platform's service manager. A user unit goes where the user manager looks first.
func (s services) manager() (manager, error) {
	switch s.goos {
	case "darwin":
		tool, err := s.tool("launchctl")
		if err != nil {
			return nil, err
		}
		return launchd.Manager{
			Runner: s.runner, Tool: tool, Domain: "gui/" + strconv.Itoa(s.uid),
			Dir: filepath.Join(s.home, "Library", "LaunchAgents"), Env: s.environ,
		}, nil
	case "linux":
		tool, err := s.tool("systemctl")
		if err != nil {
			return nil, err
		}
		config := cmp.Or(s.getenv("XDG_CONFIG_HOME"), filepath.Join(s.home, ".config"))
		return systemd.Manager{Runner: s.runner, Tool: tool, Dir: filepath.Join(config, "systemd", "user"), Env: s.environ}, nil
	}
	return nil, errors.New("no service manager on " + s.goos)
}
