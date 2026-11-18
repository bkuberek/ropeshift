//go:build screenshots

package screenshots

import (
	"os"
	"path/filepath"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/doubles"
)

// troubleGuide is the guide whose walk reads the sample troubled: after every guide before it has run.
const troubleGuide = "trouble"

// The records the troubleshooting guide adds to the sample.
const (
	orderRefund       = "Refund a cancelled order"
	orderRefundWork   = "refund-cancelled-order"
	laneRefund        = "lane-refund"
	orderRetryReport  = "Report the retry budget on the status page"
	orderRetryReportW = "report-retry-budget"
)

// returnsProfile lets a crashing lane restart once, so its second crash trips its breaker.
const returnsProfile = "[lifecycle]\nmax_restarts = 1\nrestart_window = \"1h\"\nrestart_backoff = \"10ms\"\n"

// returnsFiles is the repository the tripped lane works in: a small service whose profile allows one restart.
func returnsFiles() map[string]string {
	return map[string]string{
		".ropeshift/profile.toml": returnsProfile,
		"README.md":               lines("# returns-service", "", "Refunds for the orders the storefront cancels."),
		"go.mod":                  "module example.com/returns-service\n\ngo 1.24\n",
	}
}

// refundScript is a lane that crashes in its first moments, as a session does that its machine kills.
func refundScript() map[string]any {
	crash := 1
	return map[string]any{"turns": [][]step{turn(context(9100), step{"exit": crash})}}
}

// troubled lays out what the troubleshooting guide reads beyond the sample, once the guides before it have run:
// a lane whose breaker tripped, ready work whose dependency is accepted and not yet merged, and a driver whose
// sign-in ended. The sign-in comes last, because a worker advertises it when it connects and offers no
// driver that is signed out, so the daemon starts again with the fake CLI signed out, and the work above has
// run by then.
func (w *world) troubled() {
	in := w.in
	in.t.Helper()

	returns := in.newClone("src", "returns-service", returnsFiles())
	in.must("repository", "add", returns.Dir)
	var list api.RepositoryList
	in.read(&list, "repository", "list")
	var returnsID string
	for _, repository := range list.Repositories {
		if repository.Name == "returns-service" {
			returnsID = repository.ID
		}
	}
	if returnsID == "" {
		in.t.Fatalf("no returns-service among %+v", list.Repositories)
	}
	in.must("work", "create", "--id", orderRefundWork, "--title", orderRefund,
		"--criterion", "A cancelled order's payment is returned in full", "--size", "small", "--repository", returnsID)
	in.must("work", "review", orderRefundWork)
	in.scripts[laneRefund] = refundScript()
	in.writeFixture(planningTurns())
	in.reap(laneRefund)
	in.must("work", "claim", orderRefundWork, "--worker", w.worker, "--claim-id", "claim-"+laneRefund)
	in.must("lane", "start", orderRefundWork, "--lane", laneRefund)
	in.waitFor("the refund lane's breaker to trip", func() bool { return w.lane(laneRefund).ActualState == "tripped" })

	// The retry work is reviewed and accepted, and its branch is not merged, so work that builds on it is held.
	in.must("work", "accept", w.orders[orderRetries])
	in.must("work", "create", "--id", orderRetryReportW, "--title", orderRetryReport,
		"--criterion", "The status page shows the retry budget of each upstream", "--size", "small",
		"--repository", w.repository, "--depends-on", w.orders[orderRetries])
	in.must("work", "review", orderRetryReportW)

	// The sign-in ends, and the daemon starts again so its worker connects and advertises that.
	mark := filepath.Join(doubles.BinDir(in.path), "signed-out")
	if err := os.WriteFile(mark, nil, 0o600); err != nil {
		in.t.Fatal(err)
	}
	in.stop(in.daemon)
	in.start(w.flags...)
	in.waitFor("the worker to say its driver is signed out", func() bool {
		list := in.workers()
		if len(list) != 1 {
			return false
		}
		for _, driver := range list[0].Manifest.Drivers {
			if driver.SignIn.Status == ledger.SignedOut {
				return true
			}
		}
		return false
	})
}
