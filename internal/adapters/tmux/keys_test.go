package tmux_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/doubles"
)

// The keys the web pane sends, by tmux's names, and the bytes a terminal sends a program for each
// (https://man.openbsd.org/tmux.1#KEY_BINDINGS). Shift-Tab is BTab. A key that ends in a semicolon is
// one tmux's command line reads as the end of a command, so it is the case that most needs a real server.
var keyBytes = []struct {
	name  string
	bytes string
}{
	{"BTab", "\x1b[Z"},
	{"C-o", "\x0f"},
	{"C-x", "\x18"},
	{"C-s", "\x13"},
	{";", ";"},
	{"M-;", "\x1b;"},
	{"Tab", "\t"},
	{"Escape", "\x1b"},
	{"C-c", "\x03"},
	{"Up", "\x1b[A"},
	{"F1", "\x1bOP"},
	{"BSpace", "\x7f"},
	{"DC", "\x1b[3~"},
	{"Space", " "},
	{"-", "-"},
	{"C-]", "\x1d"},
	{"Enter", "\r"},
}

// Every key reaches the program as the bytes a terminal sends for it, in order, a semicolon included.
func TestKeysReachTheProgramAsATerminalSendsThem(t *testing.T) {
	doubles.RequireTmux(t)
	terminal := doubles.PrivateTmux(t)
	ctx := t.Context()
	dir := t.TempDir()
	out := filepath.Join(dir, "read")
	script := fmt.Sprintf("stty raw -echo -ixon && echo ready && exec cat > %s", out)
	spec := ports.PaneSpec{Name: "keys-g1", Dir: dir, Width: 80, Height: 10, Command: []string{"sh", "-c", script}}
	if _, err := terminal.Open(ctx, spec); err != nil {
		t.Fatal(err)
	}
	doubles.KillTmux(t, terminal, spec.Name)
	waitFor(t, func() (bool, string) {
		screen, err := terminal.Capture(ctx, spec.Name)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Contains(screen, "ready"), fmt.Sprintf("no ready on the screen: %q", screen)
	})
	var names []string
	var want strings.Builder
	for _, key := range keyBytes {
		names = append(names, key.name)
		want.WriteString(key.bytes)
	}
	if err := terminal.Keys(ctx, spec.Name, names...); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() (bool, string) {
		got, _ := os.ReadFile(out)
		return string(got) == want.String(), fmt.Sprintf("the program read %q, want %q", got, want.String())
	})
}
