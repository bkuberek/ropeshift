package cli_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

const capsWorker = "worker-caps"

// capsDaemon is a daemon whose worker has registered and reported a recommendation of seven lanes, served
// a second time with that worker's user's layer, as the daemon of its machine serves it. It returns the
// second socket.
func capsDaemon(t *testing.T) (*apitest.Daemon, *fake.ProfileSource, string) {
	t.Helper()
	return capsDaemonWith(t, "8 cores less 1 reserved fit 7 lanes", []string{"state"})
}

// capsDaemonWith is capsDaemon with the reason and the volume roles the worker reports.
func capsDaemonWith(t *testing.T, reason string, roles []string) (*apitest.Daemon, *fake.ProfileSource, string) {
	t.Helper()
	d := apitest.Start(t)
	ctx := t.Context()
	token, err := d.Client.CreateJoinToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := d.Client.Register(ctx, ledger.Registration{JoinToken: token, WorkerID: capsWorker, Name: "caps"})
	if err != nil {
		t.Fatal(err)
	}
	worker := api.NewUnixClient(d.Socket).Project(apitest.ProjectName).WithCredential(credential.Credential)
	t.Cleanup(worker.Close)
	session, err := worker.OpenSession(ctx, capsWorker, ledger.SessionRequest{Protocol: ledger.ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	report := ledger.ResourceReport{
		IntervalMs: 15_000, Machine: ledger.MachineResources{Cores: 8, Volumes: []ledger.VolumeResources{}}, Sessions: []ledger.SessionResources{},
		Recommendation: &ledger.LaneRecommendation{MaxLanes: 7, Basis: ledger.BasisEstimates, Cores: 8, Reason: reason},
		Caps:           ledger.LaneCaps{MaxLanes: 7},
	}
	if len(roles) > 1 || roles[0] != "state" {
		report.Machine.Volumes = []ledger.VolumeResources{{Roles: roles, FreeBytes: 1 << 30, TotalBytes: 1 << 40}}
	}
	if _, err := worker.Heartbeat(ctx, capsWorker, ledger.Heartbeat{Epoch: session.Epoch, Protocol: ledger.ProtocolVersion, Nonce: "n", Report: &report}); err != nil {
		t.Fatal(err)
	}
	profile := fake.NewProfileSource()
	config := d.Config
	config.Profile = profile
	config.LocalWorker = func(context.Context) (string, error) { return capsWorker, nil }
	socket := filepath.Join(apitest.ShortDir(t), "caps.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: server.New(config), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = httpServer.Serve(listener) }()
	t.Cleanup(func() { _ = httpServer.Close() })
	return d, profile, socket
}

// TestWorkerCapsSetsAnyNumberAndWarnsAboveTheRecommendation holds FR-PROJ-7's command: it shows the caps
// beside the recommendation, saves any number with no upper bound, and warns, with the recommendation and
// why, when the per-worker cap is above it.
func TestWorkerCapsSetsAnyNumberAndWarnsAboveTheRecommendation(t *testing.T) {
	d, profile, socket := capsDaemon(t)
	run := func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := cli.Run(append([]string{"worker", "caps", capsWorker}, args...), &stdout, &stderr, cli.Options{Socket: socket, Session: d.Session})
		return code, stdout.String(), stderr.String()
	}
	code, stdout, stderr := run()
	if code != cli.ExitOK || !strings.Contains(stdout, "max_lanes\tthe recommendation, 7\n") || !strings.Contains(stdout, "recommended\t7: 8 cores") || stderr != "" {
		t.Errorf("show: exit %d\n%s%s", code, stdout, stderr)
	}
	code, stdout, stderr = run("--max-lanes", "1000", "--per-sign-in", "3")
	if code != cli.ExitOK || !strings.Contains(stdout, "max_lanes\t1000\n") || !strings.Contains(stdout, "max_lanes_per_sign_in\t3\n") {
		t.Errorf("set: exit %d\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "warning: 1000 lanes is above this worker's recommendation of 7, and is saved: 8 cores less 1 reserved fit 7 lanes.") {
		t.Errorf("warning %q", stderr)
	}
	if user := profile.User(); !strings.Contains(user, "max_lanes = 1000") || !strings.Contains(user, "max_lanes_per_sign_in = 3") {
		t.Errorf("the user's layer holds %q", user)
	}
	code, stdout, stderr = run("--max-lanes", "none")
	if code != cli.ExitOK || !strings.Contains(stdout, "max_lanes\tno cap\n") || !strings.Contains(stdout, "max_lanes_per_sign_in\t3\n") || stderr != "" {
		t.Errorf("no cap, keeping the sign-in's: exit %d\n%s%s", code, stdout, stderr)
	}
	code, stdout, _ = run("--max-lanes", "recommended", "--per-sign-in", "none")
	if code != cli.ExitOK || !strings.Contains(stdout, "max_lanes\tthe recommendation, 7\n") || !strings.Contains(stdout, "max_lanes_per_sign_in\tno cap\n") {
		t.Errorf("back to the recommendation: exit %d\n%s", code, stdout)
	}
	for _, bad := range [][]string{{"--max-lanes", "-1"}, {"--max-lanes", "lots"}, {"--per-sign-in", "recommended"}} {
		if code, _, _ := run(bad...); code != cli.ExitUsage {
			t.Errorf("%v: exit %d", bad, code)
		}
	}
}

// TestWorkerListShowsWhatEachWorkerMeasured holds FR-UI-10 for the worker list: the machine as the worker
// last measured it, its cap beside the recommendation, and how long ago, with a measure the machine could
// not provide said as unavailable.
func TestWorkerListShowsWhatEachWorkerMeasured(t *testing.T) {
	d, _, _ := capsDaemon(t)
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"worker", "list"}, &stdout, &stderr, cli.Options{Socket: d.Socket, Session: d.Session}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"worker-caps\tcaps\tepoch 1\t", "cpu unavailable  load unavailable  memory unavailable  disk unavailable  cap 7, the recommendation", "measured "} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
}

// TestWorkerTextPrintsNoControlCharacters prints what a worker wrote with its control characters replaced,
// as top does, because a worker's report reaches a person's terminal.
func TestWorkerTextPrintsNoControlCharacters(t *testing.T) {
	d, _, socket := capsDaemonWith(t, "seven fit\x1b]0;owned\x07\nnext line", []string{"state\x1b[31m"})
	for _, run := range []struct {
		socket string
		args   []string
	}{{socket, []string{"worker", "caps", capsWorker}}, {d.Socket, []string{"worker", "list"}}} {
		var stdout, stderr bytes.Buffer
		if code := cli.Run(run.args, &stdout, &stderr, cli.Options{Socket: run.socket, Session: d.Session}); code != cli.ExitOK {
			t.Fatalf("%v: exit %d: %s", run.args, code, stderr.String())
		}
		lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
		if strings.ContainsAny(stdout.String(), "\x1b\x07") || slices.ContainsFunc(lines, func(line string) bool { return strings.HasPrefix(line, "next line") }) {
			t.Errorf("%v printed %q", run.args, stdout.String())
		}
	}
}
