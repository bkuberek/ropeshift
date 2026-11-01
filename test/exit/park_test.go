package exit

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/api/shimlink"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
	"ropeshift.dev/ropeshift/test/doubles"
	"ropeshift.dev/ropeshift/test/gittest"
)

// parkProfile asks a lane to wait a moment on a person, and gives it a moment to hand off.
const parkProfile = "[lifecycle]\nask_wait = \"100ms\"\nhandoff_timeout = \"10s\"\nmax_restarts = 0\n"

// parkedLane takes a fresh daemon to a lane parked on a question, and returns its work order.
func (in *install) parkedLane(t *testing.T, flags []string, start func(...string) *daemon) (string, *daemon) {
	t.Helper()
	first := start(flags...)
	in.waitReady(first)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	config := filepath.Join(in.paths().StateDir, "lanes", "lane-1")
	doubles.ReapShims(t, config)
	in.waitFor("the lane to park", func() bool { return in.show(id).State == workorders.Parked })
	return id, first
}

// sessionsOf lists the `-p` sessions the scripted CLI saw.
func (in *install) sessionsOf(t *testing.T) [][]string {
	t.Helper()
	var sessions [][]string
	for _, seen := range doubles.SeenBy(t, doubles.BinDir(in.path)) {
		if len(seen.Args) != 0 && seen.Args[0] == "-p" {
			sessions = append(sessions, seen.Args)
		}
	}
	return sessions
}

func parkFlags(repo string) []string {
	return []string{"--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--wake-tick", "20ms", "--source", repo}
}

// TestAParkedLaneHoldsNoProcessAndResumes is the sequence on the real binary, the real driver,
// and the real shim: the lane stops, nothing of it runs, a member's word wakes it, its worker
// claims it with nobody dispatching it, and the session continues with every flag stated again.
func TestAParkedLaneHoldsNoProcessAndResumes(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", parkProfile)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(t, "lane-parks")
	id, running := in.parkedLane(t, parkFlags(repo.Dir), in.start)

	state, err := shimlink.ReadState(filepath.Join(in.paths().StateDir, "lanes", "lane-1", "shim-1"))
	if err != nil {
		t.Fatal(err)
	}
	in.waitFor("the session's end", func() bool { return !exists(state.ChildPID) && !exists(state.ShimPID) })
	row := in.show(id)
	if row.ClaimID != "" || row.LeaseWorker != "" {
		t.Errorf("a parked work order holds a lease: %+v", row)
	}

	in.must("lane", "resume", id)
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	var view ledger.LaneView
	if err := json.Unmarshal([]byte(in.must("lane", "show", "lane-1", "--json")), &view); err != nil {
		t.Fatal(err)
	}
	running.kill()
	if len(view.Generations) != 2 || view.Generations[1].StartReason != ledger.StartResume {
		t.Errorf("generations %+v", view.Generations)
	}

	sessions := in.sessionsOf(t)
	if len(sessions) != 2 {
		t.Fatalf("%d sessions, want the first and its resume", len(sessions))
	}
	first, second := sessions[0], sessions[1]
	at := slices.Index(second, "--resume")
	if at < 0 {
		t.Fatalf("no resume in %v", second)
	}
	flags := slices.Delete(slices.Clone(second), at, at+2)
	if len(flags) != len(first) {
		t.Fatalf("a resume dropped a flag:\nfirst  %v\nsecond %v", first, second)
	}
	for i := range flags {
		if flags[i] != first[i] && !strings.HasSuffix(first[i], ".json") {
			t.Errorf("flag %d: %q and %q", i, first[i], flags[i])
		}
	}
}

// laterBinary builds this source with one migration more, as a later release would.
func laterBinary(t *testing.T) string {
	t.Helper()
	dir := apiShortDir(t)
	migration := filepath.Join(dir, "later.sql")
	const text = "CREATE TABLE later_release (workspace_id TEXT NOT NULL, id TEXT NOT NULL, PRIMARY KEY (workspace_id, id));\n"
	if err := os.WriteFile(migration, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := json.Marshal(map[string]map[string]string{"Replace": {
		filepath.Join(root, "internal", "adapters", "sqlite", "migrations", "9000_later_release.sql"): migration,
	}})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0o600); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(dir, "ropeshift")
	build := exec.Command("go", "build", "-overlay", overlayPath, "-o", cli, "ropeshift.dev/ropeshift/cmd/ropeshift")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	daemon := filepath.Join(dir, "ropeshiftd")
	if err := os.Symlink(cli, daemon); err != nil {
		t.Fatal(err)
	}
	return daemon
}

func apiShortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rslater")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func (in *install) startBinary(path string, args ...string) *daemon {
	in.t.Helper()
	cmd := exec.Command(path, append([]string{"solo"}, args...)...)
	cmd.Env = append(in.env(), in.daemonEnv...)
	d := &daemon{cmd: cmd, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, exited: make(chan error, 1)}
	cmd.Stdout, cmd.Stderr = d.stdout, d.stderr
	if err := cmd.Start(); err != nil {
		in.t.Fatal(err)
	}
	go func() { d.exited <- cmd.Wait() }()
	in.t.Cleanup(func() { d.kill() })
	return d
}

func hasTable(t *testing.T, ledgerPath, table string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", ledgerPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?1`, table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count == 1
}

// TestParkedLaneSurvivesUpgrade is FR-PARK-7 on two compiled binaries: parked state is data, so a
// lane one build parked wakes under a later build with a later migration.
func TestParkedLaneSurvivesUpgrade(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", parkProfile)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(t, "lane-parks")
	later := laterBinary(t)
	id, earlier := in.parkedLane(t, parkFlags(repo.Dir), in.start)
	ledgerPath := filepath.Join(in.paths().StateDir, api.LedgerFileName)
	if hasTable(t, ledgerPath, "later_release") {
		t.Fatal("the earlier build ran the later migration")
	}
	earlier.kill()

	running := in.startBinary(later, parkFlags(repo.Dir)...)
	in.waitReady(running)
	if !hasTable(t, ledgerPath, "later_release") {
		t.Fatal("the later build did not run its migration")
	}
	if row := in.show(id); row.State != workorders.Parked {
		t.Fatalf("an upgrade moved a parked lane to %s", row.State)
	}
	in.must("lane", "resume", id)
	in.waitFor("the lane's outcome under the later build", func() bool { return in.show(id).State == workorders.Acceptance })
	running.kill()
	sessions := in.sessionsOf(t)
	if len(sessions) != 2 || !slices.Contains(sessions[1], "--resume") {
		t.Errorf("sessions %v", sessions)
	}
}
