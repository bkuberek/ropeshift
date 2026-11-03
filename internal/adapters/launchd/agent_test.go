package launchd_test

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/launchd"
)

var update = flag.Bool("update", false, "rewrite the golden service definitions")

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("definition\n got %s\nwant %s", got, want)
	}
}

func goldenAgent() launchd.Agent {
	return launchd.Agent{
		Label:   "dev.ropeshift.ropeshiftd",
		Program: []string{"/opt/ropeshift/bin/ropeshiftd", "solo", "--source", "/home/a person/R&D <tools>"},
		Environment: []string{
			"LANG=en_US.UTF-8", "PATH=/opt/ropeshift/bin:/usr/bin:/bin", "ROPESHIFT_STATE_DIR=/home/a person/state",
		},
		Log: "/home/a person/state/ropeshiftd.log",
	}
}

// The agent runs as interactive work, because an unset process class throttles CPU and I/O, and
// launchd restarts it after a crash and leaves it stopped after a clean exit.
func TestAgentDefinition(t *testing.T) {
	got, err := goldenAgent().Render()
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "agent.plist", got)
	if lint, err := exec.LookPath("plutil"); err == nil {
		path := filepath.Join(t.TempDir(), "agent.plist")
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(lint, "-lint", path).CombinedOutput(); err != nil {
			t.Errorf("plutil: %v: %s", err, out)
		}
	}
}

func TestAnAgentRefusesWhatAPlistCannotHold(t *testing.T) {
	tests := []struct {
		name   string
		change func(*launchd.Agent)
	}{
		{"no label", func(a *launchd.Agent) { a.Label = "" }},
		{"a relative program", func(a *launchd.Agent) { a.Program[0] = "ropeshiftd" }},
		{"no program", func(a *launchd.Agent) { a.Program = nil }},
		{"a control character", func(a *launchd.Agent) { a.Program[2] = "--source\x01" }},
		{"a variable with no value", func(a *launchd.Agent) { a.Environment = []string{"PATH"} }},
		{"a relative log", func(a *launchd.Agent) { a.Log = "ropeshiftd.log" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := goldenAgent()
			tt.change(&agent)
			if got, err := agent.Render(); err == nil {
				t.Errorf("rendered %s", strings.TrimSpace(string(got)))
			}
		})
	}
}
