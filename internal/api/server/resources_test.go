package server_test

import (
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
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// reportAs sends one heartbeat that carries a report, as the worker joinWorker registered.
func reportAs(t *testing.T, d *apitest.Daemon, credential string, epoch int64, report ledger.ResourceReport) {
	t.Helper()
	worker := api.NewUnixClient(d.Socket).Project(apitest.ProjectName).WithCredential(credential)
	t.Cleanup(worker.Close)
	_, err := worker.Heartbeat(t.Context(), aSubscriptionWorker, ledger.Heartbeat{Epoch: epoch, Protocol: ledger.ProtocolVersion, Nonce: "n", Report: &report})
	if err != nil {
		t.Fatal(err)
	}
}

func aReport() ledger.ResourceReport {
	cpu, rss := 42.0, int64(512)<<20
	return ledger.ResourceReport{
		IntervalMs: 15_000, AgeMs: 1_000,
		Machine:        ledger.MachineResources{Cores: 8, CPUPercent: &cpu, Volumes: []ledger.VolumeResources{{Roles: []string{"state"}, FreeBytes: 1 << 30, TotalBytes: 1 << 40}}},
		Recommendation: &ledger.LaneRecommendation{MaxLanes: 7, Basis: ledger.BasisEstimates, Cores: 8, Reason: "seven fit"},
		Caps:           ledger.LaneCaps{MaxLanes: 7},
		Sessions:       []ledger.SessionResources{{Kind: ledger.SessionLane, Project: apitest.ProjectName, Lane: "lane-1", RSSBytes: &rss}},
		Warnings:       []ledger.ResourceWarning{{Measure: ledger.WarnDiskFree, Volume: "state", Value: 1 << 30, Threshold: 10 << 30, Message: "low"}},
	}
}

// TestTheShopFloorCarriesWhatAWorkerReported is what the web UI and `ropeshift top` read: each worker's
// last report, and whether it is fresh (FR-UI-10).
func TestTheShopFloorCarriesWhatAWorkerReported(t *testing.T) {
	d := apitest.Start(t)
	credential, epoch := joinWorker(t, d)
	floor := readView[api.ShopFloor](t, d, api.ViewShopFloor, "")
	if len(floor.Workers) != 1 || floor.Workers[0].Worker.Report != nil || floor.Workers[0].ReportFreshness != "" {
		t.Fatalf("a worker that never reported: %+v", floor.Workers)
	}
	reportAs(t, d, credential, epoch, aReport())
	floor = readView[api.ShopFloor](t, d, api.ViewShopFloor, "")
	line := floor.Workers[0]
	if line.Worker.Report == nil || line.Worker.Report.Caps.MaxLanes != 7 || len(line.Worker.Report.Warnings) != 1 || line.Worker.ReportedAt == 0 {
		t.Errorf("worker %+v", line.Worker)
	}
	if line.ReportFreshness != ledger.FreshnessObserved {
		t.Errorf("a report just made reads %q", line.ReportFreshness)
	}
	list, err := d.Client.WorkerList(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Workers) != 1 || list.Workers[0].Report == nil || list.AsOf < list.Workers[0].ReportedAt {
		t.Errorf("list %+v", list)
	}
}

// capsListener serves the API a second time on the daemon's ledger, as the daemon of the worker
// joinWorker registered, with that machine's user's layer. A session of "" calls with none.
func capsListener(t *testing.T, d *apitest.Daemon, profile ports.UserProfile, local, session string) *api.Client {
	t.Helper()
	config := d.Config
	config.Profile = profile
	config.LocalWorker = func(context.Context) (string, error) { return local, nil }
	socket := filepath.Join(apitest.ShortDir(t), "caps.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: server.New(config), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = httpServer.Serve(listener) }()
	t.Cleanup(func() { _ = httpServer.Close() })
	client := api.NewUnixClient(socket)
	t.Cleanup(client.Close)
	client.UseSession(session)
	return client
}

func lanesOf(value int64) *int64 { return &value }

// TestAMemberSetsTheLaneCaps holds FR-PROJ-7's API: any positive number, no cap, or the recommendation,
// with no upper bound, saved into the user's layer, and a warning with the recommendation and its reason
// when the per-worker cap stands above it.
func TestAMemberSetsTheLaneCaps(t *testing.T) {
	d := apitest.Start(t)
	credential, epoch := joinWorker(t, d)
	reportAs(t, d, credential, epoch, aReport())
	profile := fake.NewProfileSource()
	client := capsListener(t, d, profile, aSubscriptionWorker, d.Session)
	ctx := t.Context()
	view, err := client.LaneCaps(ctx, aSubscriptionWorker)
	if err != nil {
		t.Fatal(err)
	}
	if view.MaxLanes != nil || view.EffectiveMaxLanes != 7 || view.Recommendation == nil || view.Recommendation.MaxLanes != 7 || view.Warning != nil {
		t.Errorf("unset caps read %+v", view)
	}
	view, err = client.SetLaneCaps(ctx, aSubscriptionWorker, api.SetLaneCaps{MaxLanes: lanesOf(1000), MaxLanesPerSignIn: 2})
	if err != nil {
		t.Fatal(err)
	}
	if view.MaxLanes == nil || *view.MaxLanes != 1000 || view.EffectiveMaxLanes != 1000 || view.MaxLanesPerSignIn != 2 {
		t.Errorf("caps over the recommendation read %+v", view)
	}
	if view.Warning == nil || view.Warning.Recommended != 7 || view.Warning.Reason != "seven fit" || !strings.Contains(view.Warning.Message, "7") {
		t.Errorf("warning %+v", view.Warning)
	}
	if user := profile.User(); !strings.Contains(user, "max_lanes = 1000") || !strings.Contains(user, "max_lanes_per_sign_in = 2") {
		t.Errorf("the user's layer holds %q", user)
	}
	for _, step := range []struct {
		set       *int64
		effective int64
	}{{lanesOf(5), 5}, {lanesOf(0), 0}, {nil, 7}} {
		view, err = client.SetLaneCaps(ctx, aSubscriptionWorker, api.SetLaneCaps{MaxLanes: step.set})
		if err != nil {
			t.Fatal(err)
		}
		if view.EffectiveMaxLanes != step.effective || view.Warning != nil || view.MaxLanesPerSignIn != 0 {
			t.Errorf("set %v: %+v", step.set, view)
		}
	}
	if read, err := client.LaneCaps(ctx, aSubscriptionWorker); err != nil || read.MaxLanes != nil {
		t.Errorf("back to the recommendation reads %+v, %v", read, err)
	}
	_, err = client.SetLaneCaps(ctx, aSubscriptionWorker, api.SetLaneCaps{MaxLanes: lanesOf(-1)})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
}

func TestOnlyAMemberSetsALanesCap(t *testing.T) {
	d := apitest.Start(t)
	joinWorker(t, d)
	client := capsListener(t, d, fake.NewProfileSource(), aSubscriptionWorker, "")
	if _, err := client.LaneCaps(t.Context(), aSubscriptionWorker); err != nil {
		t.Errorf("a read needs no session: %v", err)
	}
	_, err := client.SetLaneCaps(t.Context(), aSubscriptionWorker, api.SetLaneCaps{MaxLanes: lanesOf(2)})
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
}

// TestALanesCapIsSetOnItsOwnMachine refuses the caps of a worker whose user's layer this daemon cannot
// reach: one on another machine, or any worker of a daemon that runs none.
func TestALanesCapIsSetOnItsOwnMachine(t *testing.T) {
	d := apitest.Start(t)
	joinWorker(t, d)
	elsewhere := capsListener(t, d, fake.NewProfileSource(), "another-worker", d.Session)
	_, err := elsewhere.SetLaneCaps(t.Context(), aSubscriptionWorker, api.SetLaneCaps{MaxLanes: lanesOf(2)})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	_, err = elsewhere.LaneCaps(t.Context(), "nobody")
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
	_, err = d.Client.LaneCaps(t.Context(), aSubscriptionWorker)
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
}
