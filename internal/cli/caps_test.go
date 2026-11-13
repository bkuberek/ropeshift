package cli_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"path/filepath"
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
		Recommendation: &ledger.LaneRecommendation{MaxLanes: 7, Basis: ledger.BasisEstimates, Cores: 8, Reason: "8 cores less 1 reserved fit 7 lanes"},
		Caps:           ledger.LaneCaps{MaxLanes: 7},
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
