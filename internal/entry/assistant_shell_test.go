package entry

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
)

// A delegate's credential starts no session process: lane-shim would run any program it names, and
// lane-pane any session its file names, so each is refused before it reads anything when a delegate
// token is set. The worker starts both with none.
func TestADelegateStartsNoSessionProcess(t *testing.T) {
	t.Setenv(authority.EnvDelegateToken, "a-delegate-token")
	ran := filepath.Join(t.TempDir(), "ran")
	launch := filepath.Join(t.TempDir(), "launch.json")
	if err := os.WriteFile(launch, []byte(`{"path":"/usr/bin/touch","args":["`+ran+`"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"ropeshift", "lane-shim", "--dir", filepath.Join(dir, "g"), "--socket", filepath.Join(dir, "s.sock"), "--", "/usr/bin/touch", ran},
		{"ropeshift", "lane-pane", launch},
	} {
		var stdout, stderr bytes.Buffer
		if code := Main(args, &stdout, &stderr); code == exitOK || !strings.Contains(stderr.String(), "delegate") {
			t.Errorf("%v: exit %d %q", args[1], code, stderr.String())
		}
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("a session process ran for a delegate")
	}
	if _, err := os.Stat(launch); err != nil {
		t.Errorf("lane-pane read its file for a delegate: %v", err)
	}
}

// What an assistant's shell may run is every command of the CLI that acts through the API, and none of
// the routes the binary answers itself, which its rules deny by name.
func TestAnAssistantsShellHoldsTheCLIsCommandsAndNoRoute(t *testing.T) {
	commands, routes := assistantShell()
	for _, route := range []string{"hook", "mcp", "statusline", "lane-pane", "lane-shim", "help", "version"} {
		if !slices.Contains(routes, route) {
			t.Errorf("routes lack %s: %v", route, routes)
		}
		if slices.Contains(commands, route) {
			t.Errorf("an assistant may run the route %s", route)
		}
	}
	for _, command := range authority.MachineLocalCommands {
		if slices.Contains(commands, command) {
			t.Errorf("an assistant may run the machine-local %s", command)
		}
	}
	for _, command := range []string{"lane list", "work show", "message send", "chat show", "search"} {
		if !slices.Contains(commands, command) {
			t.Errorf("an assistant may not run %s", command)
		}
	}
	for _, command := range commands {
		if !cli.HasCommand(command) || len(strings.Fields(command)) > 3 {
			t.Errorf("%q is no command of the CLI an assistant's policy can name", command)
		}
	}
}

// A daemon under a user service listens in the run directory XDG_RUNTIME_DIR names, which a session's
// environment does not carry: what the worker names to an assistant's session leads its ropeshift there.
func TestAnAssistantsRopeshiftFindsTheDaemonUnderAUserService(t *testing.T) {
	service := map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"}
	daemon, err := api.ResolvePaths("linux", func(name string) string { return service[name] }, "/home/sam")
	if err != nil {
		t.Fatal(err)
	}
	session := map[string]string{}
	for _, entry := range controlEnv(daemon) {
		name, value, _ := strings.Cut(entry, "=")
		session[name] = value
	}
	seen, err := api.ResolvePaths("linux", func(name string) string { return session[name] }, "/home/sam")
	if err != nil {
		t.Fatal(err)
	}
	if seen.ControlSocket() != daemon.ControlSocket() {
		t.Errorf("the assistant's ropeshift dials %s, and the daemon listens at %s", seen.ControlSocket(), daemon.ControlSocket())
	}
}
