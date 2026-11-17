package entry

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
)

// withDaemon makes the daemon the version command asks the one a test describes, or none.
func withDaemon(t *testing.T, health *api.Health) {
	t.Helper()
	isolate(t)
	was := daemonHealth
	t.Cleanup(func() { daemonHealth = was })
	daemonHealth = func(context.Context) (api.Health, bool) {
		if health == nil {
			return api.Health{}, false
		}
		return *health, true
	}
}

func runVersionCommand(t *testing.T, command string, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := Main(append([]string{command, "version"}, args...), &stdout, &stderr); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	return stdout.String()
}

func TestVersionNamesTheDaemonItAsks(t *testing.T) {
	withDaemon(t, &api.Health{Status: "ok", Version: "v0.1.0-alpha.16"})
	lines := strings.Split(strings.TrimSpace(runVersionCommand(t, commandCLI)), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "ropeshift ") || lines[1] != "daemon v0.1.0-alpha.16" {
		t.Errorf("version printed %q", lines)
	}
}

func TestVersionWarnsWhenTheDaemonSaysItIsStale(t *testing.T) {
	const warning = "ropeshift v0.1.0-alpha.17 is installed, but the daemon is still running v0.1.0-alpha.16. Run brew services restart ropeshift to run it."
	withDaemon(t, &api.Health{Status: "ok", Version: "v0.1.0-alpha.16", Installed: "v0.1.0-alpha.17", Warning: warning})
	out := runVersionCommand(t, commandCLI)
	for _, want := range []string{"daemon v0.1.0-alpha.16\n", "warning: " + warning + "\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("version printed %q, which lacks %q", out, want)
		}
	}
	var report versionReport
	if err := json.Unmarshal([]byte(runVersionCommand(t, commandCLI, "--json")), &report); err != nil {
		t.Fatal(err)
	}
	if report.Command != commandCLI || report.Warning != warning || report.Daemon == nil || report.Daemon.Version != "v0.1.0-alpha.16" || report.Daemon.Installed != "v0.1.0-alpha.17" {
		t.Errorf("version --json reported %+v", report)
	}
}

func TestVersionWarnsOfADaemonOlderThanThisBinary(t *testing.T) {
	cases := []struct {
		name      string
		daemon    string
		installed string
		warns     bool
	}{
		{"a daemon older than this binary", "v0.1.0-alpha.16", "", true},
		{"a daemon that reports no version", "", "", true},
		{"a daemon as new as this binary", "v0.1.0-alpha.17", "", false},
		{"a daemon newer than this binary", "v0.1.0-alpha.18", "", false},
		// The daemon read the build installed at its own path and found it current, so a binary run from
		// somewhere else does not overrule it.
		{"a daemon that found its installed build current", "v0.1.0-alpha.16", "v0.1.0-alpha.16", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			withDaemon(t, &api.Health{Status: "ok", Version: tt.daemon, Installed: tt.installed})
			built := readBuild
			t.Cleanup(func() { readBuild = built })
			readBuild = func(command string) versionInfo {
				info := built(command)
				info.Version = "v0.1.0-alpha.17"
				return info
			}
			out := runVersionCommand(t, commandCLI)
			if got := strings.Contains(out, "warning: "); got != tt.warns {
				t.Errorf("version printed %q, warns is %v", out, got)
			}
			if tt.warns && !strings.Contains(out, "ropeshift v0.1.0-alpha.17 is installed") {
				t.Errorf("the warning does not name the installed version: %q", out)
			}
		})
	}
}

// With no daemon to answer, `version` says only what this binary is, in the form it always had.
func TestVersionSaysNothingOfADaemonThatDoesNotAnswer(t *testing.T) {
	withDaemon(t, nil)
	out := runVersionCommand(t, commandCLI)
	if strings.Count(out, "\n") != 1 || strings.Contains(out, "daemon") || strings.Contains(out, "warning") {
		t.Errorf("version printed %q", out)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(runVersionCommand(t, commandCLI, "--json")), &keys); err != nil {
		t.Fatal(err)
	}
	for _, extra := range []string{"daemon", "warning"} {
		if _, ok := keys[extra]; ok {
			t.Errorf("version --json holds %q with no daemon", extra)
		}
	}
}

// The daemon's own name reports the binary and asks no daemon, which is how the daemon reads an installed
// build without asking itself.
func TestTheDaemonNameAsksNoDaemon(t *testing.T) {
	withDaemon(t, &api.Health{Status: "ok", Version: "v0.0.1"})
	asked := false
	daemonHealth = func(context.Context) (api.Health, bool) { asked = true; return api.Health{}, false }
	if out := runVersionCommand(t, commandDaemon); !strings.HasPrefix(out, "ropeshiftd ") || strings.Contains(out, "daemon ") {
		t.Errorf("version printed %q", out)
	}
	if asked {
		t.Error("ropeshiftd version asked a daemon")
	}
}
