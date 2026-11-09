package systemd_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/systemd"
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

func goldenUnit() systemd.Unit {
	return systemd.Unit{
		Description: "ropeshift daemon",
		Command:     []string{"/opt/ropeshift/bin/ropeshiftd", "solo", "--source", `/home/a person/50% "R&D" $HOME\x`},
		Environment: []string{"LANG=en_US.UTF-8", "PATH=/opt/ropeshift/bin:/usr/bin:/bin", "ROPESHIFT_STATE_DIR=/home/a person/100%"},
	}
}

// The unit restarts the daemon after a crash and never after a usage error, which no restart
// mends. Every word is quoted, so a space, a specifier, a variable, or a quote reaches the daemon
// as it was written.
func TestUnitDefinition(t *testing.T) {
	got, err := goldenUnit().Render()
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "ropeshiftd.service", got)
}

func TestAUnitRefusesWhatItCannotHold(t *testing.T) {
	tests := []struct {
		name   string
		change func(*systemd.Unit)
	}{
		{"a relative program", func(u *systemd.Unit) { u.Command[0] = "ropeshiftd" }},
		{"no program", func(u *systemd.Unit) { u.Command = nil }},
		{"a line break", func(u *systemd.Unit) { u.Command[2] = "--source\n" }},
		{"a control character", func(u *systemd.Unit) { u.Environment = []string{"PATH=/bin\x01"} }},
		{"a variable with no value", func(u *systemd.Unit) { u.Environment = []string{"PATH"} }},
		{"a line break in the description", func(u *systemd.Unit) { u.Description = "two\nlines" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unit := goldenUnit()
			tt.change(&unit)
			if got, err := unit.Render(); err == nil {
				t.Errorf("rendered %s", strings.TrimSpace(string(got)))
			}
		})
	}
}
