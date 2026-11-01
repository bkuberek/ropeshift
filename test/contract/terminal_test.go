package contract

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/osexec"
	"ropeshift.dev/ropeshift/internal/adapters/tmux"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/test/apitest"
	"ropeshift.dev/ropeshift/test/doubles"
)

func TestTmuxTerminalContract(t *testing.T) {
	doubles.RequireTmux(t)
	RunTerminal(t, func(t *testing.T) TerminalRig {
		terminal := doubles.PrivateTmux(t)
		terminalPath := doubles.InstallFakeClaude(t, "pane-terminal")
		exitPath := doubles.InstallFakeClaude(t, "pane-exit")
		spec := func(path string) func(string) ports.PaneSpec {
			return func(name string) ports.PaneSpec {
				return ports.PaneSpec{
					Name: name, Dir: t.TempDir(), Width: 130, Height: 45,
					Command: []string{filepath.Join(doubles.BinDir(path), "claude"), "--tui"},
				}
			}
		}
		return TerminalRig{
			Terminal: terminal, Spec: spec(terminalPath), Exits: spec(exitPath),
			Waits: func(t *testing.T, name, text string) {
				t.Helper()
				deadline := time.Now().Add(20 * time.Second)
				last := ""
				for time.Now().Before(deadline) {
					screen, err := terminal.Capture(context.Background(), name)
					if err == nil && strings.Contains(screen, text) {
						return
					}
					last = screen
					time.Sleep(5 * time.Millisecond)
				}
				t.Fatalf("%q never showed: %q", text, last)
			},
		}
	})
}

// A worker's server must not read a person's configuration or leave their server alone
// by luck, so this reads the one socket the adapter was given.
func TestTmuxStaysOnItsOwnSocket(t *testing.T) {
	doubles.RequireTmux(t)
	dir := apitest.ShortDir(t)
	socket := filepath.Join(dir, "tmux.sock")
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir}
	terminal, err := tmux.New(osexec.Runner{}, tmux.Options{Socket: socket, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	path := doubles.InstallFakeClaude(t, "pane-terminal")
	doubles.KillTmux(t, terminal, "pane")
	spec := ports.PaneSpec{
		Name: "pane", Dir: dir, Width: 130, Height: 45, Command: []string{filepath.Join(doubles.BinDir(path), "claude"), "--tui"},
	}
	if _, err := terminal.Open(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(socket); err != nil {
		t.Errorf("no server on the given socket: %v", err)
	}
}

func TestTmuxNameMatchesExactly(t *testing.T) {
	doubles.RequireTmux(t)
	terminal := doubles.PrivateTmux(t)
	path := doubles.InstallFakeClaude(t, "pane-terminal")
	spec := ports.PaneSpec{
		Name: "lane-10", Dir: t.TempDir(), Width: 130, Height: 45, Command: []string{filepath.Join(doubles.BinDir(path), "claude"), "--tui"},
	}
	if _, err := terminal.Open(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.Pane(t.Context(), "lane-1"); err == nil {
		t.Error("a prefix of a name read another lane's pane")
	}
}

func TestFakeTerminalContract(t *testing.T) {
	RunTerminal(t, func(t *testing.T) TerminalRig {
		terminal := fake.NewTerminal()
		var opened atomic.Int64
		exits := map[string]bool{}
		cancel := "   \n ❯ No, exit\n   Yes, I trust this folder\n"
		accept := "   \n   No, exit\n ❯ Yes, I trust this folder\n"
		terminal.Play(fake.PaneScript{Screen: cancel, Moves: map[string]map[string]string{cancel: {"Down": accept}}})
		return TerminalRig{
			Terminal: terminal,
			Spec: func(name string) ports.PaneSpec {
				opened.Add(1)
				return ports.PaneSpec{Name: name, Command: []string{"claude", strconv.FormatInt(opened.Load(), 10)}}
			},
			Exits: func(name string) ports.PaneSpec {
				exits[name] = true
				go func() {
					for range 1000 {
						if _, err := terminal.Pane(context.Background(), name); err == nil {
							terminal.Exit(name, ports.ExitStatus{Code: 7})
							return
						}
						time.Sleep(time.Millisecond)
					}
				}()
				return ports.PaneSpec{Name: name, Command: []string{"claude", "exits"}}
			},
			Waits: func(t *testing.T, name, text string) {
				t.Helper()
				screen, err := terminal.Capture(context.Background(), name)
				if err != nil || !strings.Contains(screen, text) {
					t.Fatalf("%q is not on the screen: %q, %v", text, screen, err)
				}
			},
		}
	})
}
