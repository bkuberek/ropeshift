package exit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/cli"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/doubles"
)

// headlessWakes runs an orchestrator on a subscription as a test can read it. A subscription's
// sessions otherwise start in a terminal.
const headlessWakes = "run_mode = \"headless\"\n" + wakesNow

// aFewPasses is many times what a heartbeat and a wake take here, so a wake that was coming has come.
const aFewPasses = 600 * time.Millisecond

func (in *install) autonomy(worker string) authority.AutonomyView {
	in.t.Helper()
	var view authority.AutonomyView
	if err := json.Unmarshal([]byte(in.must("worker", "autonomy", worker, "--json")), &view); err != nil {
		in.t.Fatal(err)
	}
	return view
}

// TestAnAttendedWorkerClaimsOnlyInsideAWindow runs FR-ORCH-6 through the real daemon, driver, and
// `ropeshift mcp`: a worker on a subscription starts at attended, its orchestrator sleeps on ready
// work, and a member's window is what wakes it and lets the claim through.
func TestAnAttendedWorkerClaimsOnlyInsideAWindow(t *testing.T) {
	in := newInstall(t)
	in.path = doubles.InstallFakeClaude(t, "orchestrator-claims")
	if err := os.MkdirAll(in.paths().StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.paths().StateDir, "profile.toml"), []byte(headlessWakes), 0o600); err != nil {
		t.Fatal(err)
	}
	d := in.start("--heartbeat", "30ms", "--lease-tick", "20ms")
	defer func() {
		d.kill()
		if t.Failed() {
			t.Logf("the daemon said: %s", d.stderr)
		}
	}()
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	worker := in.workers()[0].ID
	if view := in.autonomy(worker); view.Level != authority.LevelAttended || view.MayClaim || view.SignIn != ledger.SignInSubscription {
		t.Fatalf("a subscription starts at %+v", view)
	}
	in.must("work", "create", "--id", "wo-1", "--title", "Add the retry budget", "--criterion", "It retries twice.")
	in.must("work", "review", "wo-1")
	in.must("orchestrator", "start", worker)
	doubles.ReapShims(t, filepath.Join(in.paths().StateDir, "lanes", ledger.Orchestrator{ID: ledger.OrchestratorOf(worker), Run: 1}.Lane()))
	seen := filepath.Join(doubles.BinDir(in.path), "seen")
	time.Sleep(aFewPasses)
	if _, err := os.Stat(seen); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a wake with no window: %v", in.sessionsOf(t))
	}

	in.must("worker", "window-open", worker, "--for", "1h")
	if view := in.autonomy(worker); !view.MayClaim || view.Window == nil {
		t.Fatalf("after a window: %+v", view)
	}
	in.waitFor("the claim", func() bool { return in.show("wo-1").State == workorders.Claimed })
	if got := in.show("wo-1").LeaseWorker; got != worker {
		t.Errorf("claimed by %q", got)
	}
	in.must("worker", "window-close", worker)
	if view := in.autonomy(worker); view.MayClaim {
		t.Errorf("a closed window: %+v", view)
	}
}

// TestACommandWithNoSessionMovesNothing is the same attack a lane would make from a shell: it has
// the binary and the socket, it has no session, and nothing here asks a terminal it does not have.
func TestACommandWithNoSessionMovesNothing(t *testing.T) {
	in := newInstall(t)
	in.path = doubles.InstallFakeClaude(t, "orchestrator-claims")
	d := in.start("--heartbeat", "30ms")
	defer d.kill()
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	worker := in.workers()[0].ID
	digest := authority.RiskDigest()
	session := in.session
	in.session = ""
	for _, args := range [][]string{
		{"worker", "window-open", worker},
		{"worker", "window-close", worker},
		{"worker", "acknowledge-risk", worker, "--digest", digest},
		{"worker", "level", worker, "unattended"},
	} {
		if code, _, stderr := in.run(args...); code != cli.ExitForbidden {
			t.Errorf("%s: exit %d: %s", strings.Join(args, " "), code, stderr)
		}
	}
	in.session = session
	view := in.autonomy(worker)
	if view.Window != nil || view.Acknowledged || view.Chosen != "" {
		t.Fatalf("something moved: %+v", view)
	}
	events := 0
	err := in.ledgerOf().Read(t.Context(), soloScope, func(tx ports.ReadTx) error {
		rows, err := tx.Events(0, 10_000)
		for _, row := range rows {
			if row.Subject.Kind == authority.SubjectAutonomy {
				events++
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("%d events with no session behind them", events)
	}
}

// TestAMemberTakesAWorkerToUnattended walks the only way there: the level is refused, the statement
// reads, its digest acknowledges it, and the level takes.
func TestAMemberTakesAWorkerToUnattended(t *testing.T) {
	in := newInstall(t)
	in.path = doubles.InstallFakeClaude(t, "orchestrator-claims")
	d := in.start("--heartbeat", "30ms")
	defer d.kill()
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	worker := in.workers()[0].ID
	if code, _, stderr := in.run("worker", "level", worker, "unattended"); code != cli.ExitForbidden || !strings.Contains(stderr, "acknowledge") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	text := in.must("worker", "risk")
	if !strings.Contains(text, authority.RiskStatement) || !strings.Contains(text, authority.RiskDigest()) {
		t.Fatalf("the statement reads %q", text)
	}
	if code, _, _ := in.run("worker", "acknowledge-risk", worker); code != cli.ExitUsage {
		t.Fatalf("an acknowledgement with no digest: exit %d", code)
	}
	if code, _, _ := in.run("worker", "acknowledge-risk", worker, "--digest", "sha256:older"); code != cli.ExitConflict {
		t.Fatalf("an acknowledgement of other text: exit %d", code)
	}
	in.must("worker", "acknowledge-risk", worker, "--digest", authority.RiskDigest())
	in.must("worker", "level", worker, "unattended")
	if view := in.autonomy(worker); view.Level != authority.LevelUnattended || !view.MayClaim {
		t.Fatalf("view %+v", view)
	}
}
