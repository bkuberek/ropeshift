//go:build screenshots && postgres

package screenshots

import (
	stdcontext "context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/pgtest"
	"ropeshift.com/ropeshift/test/procs"
)

// The group scenario is a group server and two workers, each an installation of its own: a home, a state directory,
// a run directory, and the fake agent CLI, under a root of its own, with every port one the kernel chose and a
// Postgres of the run's own for the server. Nothing in it reads or writes the person's installation, the Homebrew
// daemon, or port 7317. It is what the guides of a group server will start from, and for now the kit holds it and
// checks it: a member makes a join string with `ropeshift worker token` on the server, each worker joins with it,
// and the server lists both and hears them report (docs/development/screenshots.md). It needs initdb and postgres
// on PATH or in ROPESHIFT_TEST_PG_BIN, as `make check-postgres` does, and runs with `scripts/screenshots.sh -s`.

// groupGate is what the group commands of the daemon need to run in this version.
const groupGate = "ROPESHIFT_GROUP_MODE=preview"

// group is the server and the workers that joined it.
type group struct {
	t      *testing.T
	binary string
	keep   bool
	// server is the installation of the control plane, which a member drives with the CLI, and workers are the
	// installations of the machines that joined it.
	server  *install
	workers []*install
}

// newGroup starts a Postgres of this run's own and a server on it, serving TLS in a certificate its own authority
// issues, on a loopback port the kernel chooses. The server stops after the workers, and the database after both.
func newGroup(t *testing.T, binary string, keep bool) *group {
	t.Helper()
	ctx, cancel := stdcontext.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	database, err := pgtest.Start(ctx)
	if err != nil {
		t.Fatalf("the group scenario needs initdb and postgres on PATH or in ROPESHIFT_TEST_PG_BIN: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Stop(); err != nil {
			t.Error(err)
		}
	})
	name, err := database.NewDatabase(ctx)
	if err != nil {
		t.Fatal(err)
	}
	g := &group{t: t, binary: binary, keep: keep, server: newInstall(t, binary, keep)}
	// The connection string holds a password, so it reaches the server through the environment and no argument.
	g.server.daemon = g.server.startGroup([]string{"ROPESHIFT_DATABASE=" + database.DSN(name)}, "server",
		"--listen", "127.0.0.1:0", "--name", "127.0.0.1", "--heartbeat", "500ms", "--lease-ttl", "5s")
	g.server.waitReady()
	return g
}

// startGroup runs a group command of the daemon with the preview gate set and the install's own environment, on
// the ports its flags and environment leave to the kernel. What it says also goes to a file in the installation.
func (in *install) startGroup(env []string, args ...string) *process {
	in.t.Helper()
	cmd := exec.Command(in.daemonBin, args...)
	cmd.Env = append(append(in.env(), groupGate), env...)
	cmd.Dir = in.home
	d := &process{cmd: cmd, stdout: &lockedBuffer{}, stderr: &lockedBuffer{}, exited: make(chan error, 1)}
	log, err := os.Create(filepath.Join(in.root, "daemon-"+args[0]+".log"))
	if err != nil {
		in.t.Fatal(err)
	}
	in.t.Cleanup(func() { _ = log.Close() })
	cmd.Stdout, cmd.Stderr = io.MultiWriter(d.stdout, log), io.MultiWriter(d.stderr, log)
	if err := procs.Start(in.t, cmd); err != nil {
		in.t.Fatal(err)
	}
	in.t.Logf("ropeshiftd %s is process %d", args[0], cmd.Process.Pid)
	go func() { d.exited <- cmd.Wait() }()
	in.t.Cleanup(func() { in.stop(d) })
	return d
}

// join makes a machine of the group: a member makes a join string on the server, and a worker of an installation of
// its own joins with it and runs. The join string reaches the worker through the environment, which keeps it out of
// the process list, and nothing the worker prints holds it.
func (g *group) join(name string) *install {
	g.t.Helper()
	text := strings.TrimSpace(g.server.must("worker", "token"))
	machine := newInstall(g.t, g.binary, g.keep)
	cmd := exec.Command(machine.daemonBin, "worker", "join", "--name", name)
	cmd.Env = append(machine.env(), groupGate, "ROPESHIFT_JOIN="+text)
	cmd.Dir = machine.home
	out, err := cmd.CombinedOutput()
	if err != nil {
		g.t.Fatalf("ropeshiftd worker join: %v\n%s", err, out)
	}
	if strings.Contains(string(out), text) {
		g.t.Errorf("ropeshiftd worker join printed its join string")
	}
	machine.daemon = machine.startGroup(nil, "worker")
	g.workers = append(g.workers, machine)
	return machine
}

// listed reads the workers the server lists by name.
func (g *group) listed() map[string]ledger.Worker {
	g.t.Helper()
	found := map[string]ledger.Worker{}
	for _, worker := range g.server.workers() {
		found[worker.Name] = worker
	}
	return found
}

// TestGroupScenario starts the server and two workers, and waits until the server lists both and each has reported
// what it measured of its machine twice, which a worker does on the heartbeats it sends the server over the link its
// join string made.
func TestGroupScenario(t *testing.T) {
	g := newGroup(t, binaryPath(t), *keepFlag)
	t.Logf("the server said:\n%s", g.server.daemon.stdout.String())
	names := []string{"first-worker", "second-worker"}
	for _, name := range names {
		g.join(name)
	}
	var first map[string]ledger.Worker
	g.server.waitFor("both workers listed and reporting", func() bool {
		first = g.listed()
		for _, name := range names {
			if first[name].ReportedAt == 0 {
				return false
			}
		}
		return true
	})
	g.server.waitFor("both workers to report again", func() bool {
		later := g.listed()
		for _, name := range names {
			if later[name].ReportedAt <= first[name].ReportedAt {
				return false
			}
		}
		return true
	})
	if first[names[0]].ID == first[names[1]].ID {
		t.Errorf("the two workers share the ID %s", first[names[0]].ID)
	}
}
