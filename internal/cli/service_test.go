package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/cli"
)

// services stands for this machine's service manager, and keeps what each command asked of it.
type services struct {
	installed   []string
	daemonArgs  [][]string
	uninstalled []string
	err         error
}

func (s *services) options() cli.Options {
	return cli.Options{
		InstallService: func(_ context.Context, name string, daemonArgs []string) (cli.Service, error) {
			s.installed = append(s.installed, name)
			s.daemonArgs = append(s.daemonArgs, daemonArgs)
			return cli.Service{Name: "dev.ropeshift." + name, Path: "/agents/" + name}, s.err
		},
		UninstallService: func(_ context.Context, name string) (cli.Service, error) {
			s.uninstalled = append(s.uninstalled, name)
			return cli.Service{Name: "dev.ropeshift." + name, Path: "/agents/" + name}, s.err
		},
	}
}

func runService(opts cli.Options, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, &stdout, &stderr, opts)
	return code, stdout.String(), stderr.String()
}

// Everything after -- belongs to the daemon, so a flag of its own reaches it whole.
func TestServiceInstallHandsTheDaemonItsFlags(t *testing.T) {
	s := &services{}
	code, stdout, stderr := runService(s.options(), "service", "install", "--name", "solo", "--", "--source", "/repo", "--json")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !slices.Equal(s.installed, []string{"solo"}) || !slices.Equal(s.daemonArgs[0], []string{"--source", "/repo", "--json"}) {
		t.Errorf("installed %q with %q", s.installed, s.daemonArgs)
	}
	if !strings.Contains(stdout, "dev.ropeshift.solo") || !strings.Contains(stdout, "/agents/solo") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestServiceInstallTakesThePlatformsNameByDefault(t *testing.T) {
	s := &services{}
	code, stdout, _ := runService(s.options(), "service", "install", "--json")
	if code != cli.ExitOK || !slices.Equal(s.installed, []string{""}) || len(s.daemonArgs[0]) != 0 {
		t.Fatalf("exit %d, installed %q with %q", code, s.installed, s.daemonArgs)
	}
	var got cli.Service
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || got.Path != "/agents/" {
		t.Errorf("stdout %q: %v", stdout, err)
	}
}

func TestServiceInstallRefusesAWordBeforeTheDaemonsFlags(t *testing.T) {
	s := &services{}
	if code, _, _ := runService(s.options(), "service", "install", "--source", "/repo"); code != cli.ExitUsage {
		t.Errorf("exit %d", code)
	}
	if code, _, _ := runService(s.options(), "service", "install", "solo"); code != cli.ExitUsage {
		t.Errorf("exit %d", code)
	}
	if len(s.installed) != 0 {
		t.Errorf("installed %q", s.installed)
	}
}

func TestServiceUninstallStopsNoLane(t *testing.T) {
	s := &services{}
	code, stdout, stderr := runService(s.options(), "service", "uninstall", "--name", "solo")
	if code != cli.ExitOK || !slices.Equal(s.uninstalled, []string{"solo"}) {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "/agents/solo") || !strings.Contains(stdout, "no lane") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestServiceFailuresMapToTheirExitCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		args []string
		want int
	}{
		{"a name or a flag the daemon refuses", fmt.Errorf("%w: bad name", cli.ErrServiceUsage), []string{"service", "install", "--name", "a/b"}, cli.ExitUsage},
		{"no such service", fmt.Errorf("remove: %w", os.ErrNotExist), []string{"service", "uninstall"}, cli.ExitNotFound},
		{"a manager that failed", errors.New("launchctl bootstrap: exit 5"), []string{"service", "install"}, cli.ExitError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &services{err: tt.err}
			code, _, stderr := runService(s.options(), tt.args...)
			if code != tt.want || !strings.Contains(stderr, tt.err.Error()) {
				t.Errorf("exit %d: %s", code, stderr)
			}
		})
	}
}

func TestAPlatformWithNoServiceManagerSaysSo(t *testing.T) {
	code, _, stderr := runService(cli.Options{}, "service", "install")
	if code != cli.ExitError || !strings.Contains(stderr, "no service manager") {
		t.Errorf("exit %d: %s", code, stderr)
	}
}
