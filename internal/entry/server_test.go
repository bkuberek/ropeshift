package entry

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// A server's two settings come from its flags, and from the environment a container gives it, and a flag
// wins over the environment.
func TestAServerTakesItsSettingsFromFlagsAndTheEnvironment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		env      map[string]string
		database string
		listen   string
	}{
		{"the defaults", []string{"--database", "postgres://db/x"}, nil, "postgres://db/x", "127.0.0.1:7400"},
		{"the environment", nil, map[string]string{"ROPESHIFT_DATABASE": "postgres://env/x", "ROPESHIFT_LISTEN": "127.0.0.1:9000"}, "postgres://env/x", "127.0.0.1:9000"},
		{"flags over the environment", []string{"--database", "postgres://flag/x", "--listen", "[::1]:9100"}, map[string]string{"ROPESHIFT_DATABASE": "postgres://env/x", "ROPESHIFT_LISTEN": "127.0.0.1:9000"}, "postgres://flag/x", "[::1]:9100"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			opts, ok := parseServer(tt.args, env(tt.env), &stderr)
			if !ok {
				t.Fatalf("refused: %s", &stderr)
			}
			if opts.database != tt.database || opts.listen != tt.listen {
				t.Errorf("database %q, listen %q", opts.database, opts.listen)
			}
		})
	}
}

func TestAServerRefusesWhatItDoesNotTake(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--database", "x", "stray"}, {"--database", "x", "--source", "/repo"}, {"--no-such-flag"}} {
		var stderr bytes.Buffer
		if _, ok := parseServer(args, env(nil), &stderr); ok {
			t.Errorf("%v is taken", args)
		}
	}
}

// This version binds loopback only, because nothing yet encrypts what a worker or a member sends.
func TestAServerListensOnLoopbackOnly(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"127.0.0.1:7400", "127.0.0.1:0", "localhost:7400", "[::1]:7400", "127.1.2.3:80"} {
		if err := checkListen(address); err != nil {
			t.Errorf("%s is refused: %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:7400", "[::]:7400", ":7400", "192.168.1.5:7400", "10.0.0.1:7400", "server.example:7400", "127.0.0.1.example:7400"} {
		err := checkListen(address)
		if err == nil || err.Error() != "a server off loopback needs TLS, which this version does not have" {
			t.Errorf("%s answers %v", address, err)
		}
	}
	if err := checkListen("no port"); err == nil {
		t.Error("an address with no port is taken")
	}
}

// The refusal comes before the server touches a database, a lock, or a socket, so a mistaken address costs nothing.
func TestAServerOffLoopbackIsRefusedBeforeItConnects(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	code, stdout, stderr := runMain(commandDaemon, "server", "--database", "host=/nowhere dbname=none", "--listen", "0.0.0.0:7400")
	if code != exitUsage || stdout != "" {
		t.Fatalf("exit %d, stdout %q", code, stdout)
	}
	if !strings.Contains(stderr, "a server off loopback needs TLS, which this version does not have") {
		t.Errorf("stderr %q", stderr)
	}
}

func TestAServerNeedsADatabase(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	t.Setenv("ROPESHIFT_DATABASE", "")
	code, _, stderr := runMain(commandDaemon, "server")
	if code != exitUsage || !strings.Contains(stderr, "ROPESHIFT_DATABASE") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

// What the Postgres adapter goes on past reaches the server's own log, as a migration's warning reaches
// solo's, so an operator sees a schema the sweep dropped or an index it reconciled.
func TestWhatTheDatabaseWarnsOfReachesTheServersLog(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	options := serverDatabase(&stderr)
	if options.Store.Warn == nil {
		t.Fatal("the adapter's warnings go nowhere")
	}
	options.Store.Warn("dropped the schema project_gone")
	if got, want := stderr.String(), "ropeshiftd: warning: dropped the schema project_gone\n"; got != want {
		t.Errorf("the log holds %q, want %q", got, want)
	}
}

// emptyProjects is an installation with no project, which a tick that reads each project finds nothing in.
type emptyProjects struct{ ports.Projects }

func (emptyProjects) List(context.Context) ([]ledger.Project, error) { return nil, nil }

// soloTickNames is every tick solo's daemon runs, by name: the control plane's own and the ones it adds in
// serveSolo, each built as serveSolo builds it.
func soloTickNames(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, tick := range soloTicks(emptyProjects{}, soloScope, soloOptions{}, nil, questions.BatchPolicy{}, nil) {
		names = append(names, tick.Name)
	}
	spans, err := (&soloTelemetry{}).spansTick(t.Context(), emptyProjects{})
	if err != nil {
		t.Fatal(err)
	}
	extras := []string{
		noticeTick(server.Config{}, emptyProjects{}, notifyDeps{}, time.Second).Name,
		spans.Name,
		(&soloTelemetry{}).measuresTick(emptyProjects{}).Name,
		liveInstalledBuild(io.Discard).tick().Name,
	}
	return append(names, extras...)
}

// The server runs every control-plane tick solo runs, by name, except the ones that act on the machine of a
// worker beside it: the two wirings are two lists, and this is what keeps them from drifting.
func TestServerRunsEverySoloControlTick(t *testing.T) {
	t.Parallel()
	solo := soloTickNames(t)
	for _, name := range workerMachineTicks {
		if !slices.Contains(solo, name) {
			t.Errorf("%q is excluded as a tick of the worker's machine, and solo runs no such tick", name)
		}
	}
	want := slices.DeleteFunc(slices.Clone(solo), func(name string) bool { return slices.Contains(workerMachineTicks, name) })
	var got []string
	for _, tick := range serverTicks(emptyProjects{}, serverOptions{}, unavailableServerReads(), questions.BatchPolicy{}, nil) {
		got = append(got, tick.Name)
	}
	got = append(got, liveInstalledBuild(io.Discard).tick().Name)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Errorf("solo runs %v, less %v, and the server runs %v", solo, workerMachineTicks, got)
	}
	if len(want) < 12 {
		t.Errorf("only %d ticks were compared", len(want))
	}
}

// Every tick of the server runs more often than never, and none shares a name with another.
func TestServerTicksHaveNamesAndIntervals(t *testing.T) {
	t.Parallel()
	opts, ok := parseServer([]string{"--database", "x"}, env(nil), io.Discard)
	if !ok {
		t.Fatal("refused")
	}
	seen := map[string]bool{}
	for _, tick := range serverTicks(emptyProjects{}, opts, unavailableServerReads(), questions.BatchPolicy{}, nil) {
		if tick.Name == "" || tick.Every <= 0 || tick.Run == nil {
			t.Errorf("tick %+v", tick)
		}
		if seen[tick.Name] {
			t.Errorf("two ticks are named %q", tick.Name)
		}
		seen[tick.Name] = true
	}
}

// Group commands start only with the preview on: without it they exit 2 with the refusal, whatever else the
// command line holds, and the help they answer to is the same as for a command that does not exist.
func TestAGroupCommandRefusesWithoutThePreviewGate(t *testing.T) {
	isolate(t)
	for _, value := range []string{"", "yes", "1", "true", "Preview", "preview "} {
		t.Run("value "+value, func(t *testing.T) {
			t.Setenv("ROPESHIFT_GROUP_MODE", value)
			for _, args := range [][]string{
				{"server"}, {"server", "--database", "x"}, {"server", "--help"}, {"server", "--listen", "0.0.0.0:1"},
			} {
				code, stdout, stderr := runMain(append([]string{commandDaemon}, args...)...)
				if code != exitUsage || stdout != "" || !strings.Contains(stderr, groupRefusal) {
					t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, stdout, stderr)
				}
			}
			code, _, stderr := runMain(commandDaemon, "help", "server")
			if code != exitUsage || !strings.Contains(stderr, "unknown command") {
				t.Errorf("help server: exit %d, stderr %q", code, stderr)
			}
		})
	}
}

// With the gate off, help is what it was before group mode: the list a binary prints is its public routes
// and nothing more. With it on, the group commands are listed.
func TestGroupCommandsAreListedOnlyWithThePreviewGate(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "")
	_, off, _ := runMain(commandDaemon, "help")
	var public bytes.Buffer
	var plain []cli.Group
	for _, group := range routes(commandDaemon) {
		listed := cli.Group{Title: group.title}
		for _, r := range group.routes {
			listed.Commands = append(listed.Commands, r.Command)
		}
		plain = append(plain, listed)
	}
	cli.WriteList(&public, commandDaemon, plain)
	if off != public.String() {
		t.Errorf("help with the gate off is\n%s\nand the public routes list\n%s", off, public.String())
	}
	if listed(off, "server") || listed(off, "worker") {
		t.Errorf("help lists a group command with the gate off:\n%s", off)
	}
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	_, on, _ := runMain(commandDaemon, "help")
	if !listed(on, "server") {
		t.Errorf("help lists no server with the gate on:\n%s", on)
	}
	for _, command := range []string{"solo", "help", "version"} {
		if !listed(on, command) {
			t.Errorf("help with the gate on lists no %s:\n%s", command, on)
		}
	}
	// The CLI's help is the CLI's, with or without the gate.
	t.Setenv("ROPESHIFT_GROUP_MODE", "")
	_, cliOff, _ := runMain(commandCLI, "help")
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	_, cliOn, _ := runMain(commandCLI, "help")
	if cliOff != cliOn {
		t.Errorf("the CLI's help changes with the gate")
	}
}

func TestAGroupCommandAnswersHelpWithTheGateOn(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	_, usage, _ := runMain(commandDaemon, "server", "--help")
	for _, want := range []string{"usage: ropeshiftd server", "-database", "-listen", "ROPESHIFT_DATABASE", "ROPESHIFT_LISTEN", "ROPESHIFT_GROUP_MODE=preview"} {
		if !strings.Contains(usage, want) {
			t.Errorf("the usage of server lacks %q:\n%s", want, usage)
		}
	}
	code, viaHelp, _ := runMain(commandDaemon, "help", "server")
	if code != exitOK || viaHelp != usage {
		t.Errorf("help server prints exit %d\n%s", code, viaHelp)
	}
}
