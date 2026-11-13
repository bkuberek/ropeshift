package server_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
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
