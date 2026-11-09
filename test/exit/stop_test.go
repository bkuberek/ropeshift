package exit

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/profiles"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

// runLaneThatLeavesWork takes one lane to its end on the real binary, with a file git has
// not seen left in its worktree.
func runLaneThatLeavesWork(t *testing.T, in *install, repo *gittest.Repo) string {
	t.Helper()
	in.path = doubles.InstallFakeClaude(t, "lane-leaves-work")
	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	t.Cleanup(d.stop)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool { return len(in.workers()) == 1 })
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	return filepath.Join(filepath.Dir(repo.Dir), filepath.Base(repo.Dir)+"-lanes", "lane-1")
}

func (in *install) lane(id string) ledger.LaneView {
	in.t.Helper()
	var view ledger.LaneView
	if err := json.Unmarshal([]byte(in.must("lane", "show", id, "--json")), &view); err != nil {
		in.t.Fatal(err)
	}
	return view
}

// repoAheadOfItsRemote is a source whose base branch holds a commit its bare remote does not,
// as a person's working branch often does. It returns the repository, the remote, and the commit
// the remote holds.
func repoAheadOfItsRemote(t *testing.T) (*gittest.Repo, string, string) {
	t.Helper()
	repo := gittest.New(t)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	bare := filepath.Join(filepath.Dir(repo.Dir), "remote.git")
	repo.Git("init", "--quiet", "--bare", bare)
	repo.Git("remote", "add", "origin", bare)
	repo.Git("push", "--quiet", "origin", "main")
	pushed := repo.Git("rev-parse", "HEAD")
	repo.Commit("notes.txt", "not pushed yet\n")
	return repo, bare, pushed
}

// gitAt runs git in a directory and says whether it worked, for a wait that polls.
func gitAt(repo *gittest.Repo, dir string, args ...string) (string, bool) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = repo.Env()
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err == nil
}

// choosePush is the user's layer turning pushing on, which nothing else can.
func (in *install) choosePush() {
	in.t.Helper()
	if err := os.MkdirAll(in.paths().StateDir, 0o700); err != nil {
		in.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.paths().StateDir, profiles.UserFile), []byte("[worktree]\npush_on_stop = true\n"), 0o600); err != nil {
		in.t.Fatal(err)
	}
}

// TestAStopSavesWorkToTheRemote walks the design's table on real git for a user who chose to
// push: the lane's branch and what a session left uncommitted reach the remote, the worktree
// stays until a member removes it, and a removal with nothing at risk needs no force.
func TestAStopSavesWorkToTheRemote(t *testing.T) {
	in := newInstall(t)
	in.choosePush()
	repo, bare, _ := repoAheadOfItsRemote(t)
	worktree := runLaneThatLeavesWork(t, in, repo)
	const ref = "refs/ropeshift/checkpoints/lane-1/1"
	in.waitFor("the snapshot on the remote", func() bool {
		out, ok := gitAt(repo, bare, "show", ref+":untracked.txt")
		return ok && out == "work"
	})
	if head, ok := gitAt(repo, bare, "rev-parse", "refs/heads/lane/lane-1"); !ok || head != repo.Git("rev-parse", "refs/heads/lane/lane-1") {
		t.Errorf("the lane's branch on the remote: %q, %v", head, ok)
	}
	salvage := func(view ledger.LaneView) []ledger.Artifact {
		var out []ledger.Artifact
		for _, artifact := range view.Artifacts {
			if artifact.Kind == ledger.ArtifactSalvage {
				out = append(out, artifact)
			}
		}
		return out
	}
	in.waitFor("the record of it", func() bool { return len(salvage(in.lane("lane-1"))) == 1 })
	view := in.lane("lane-1")
	if salvage(view)[0].URI != ref {
		t.Errorf("artifacts %+v", view.Artifacts)
	}
	if slices.Contains(view.Conditions, "unpushed_work") {
		t.Errorf("work on the remote reads as unpushed: %v", view.Conditions)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("a stop took the worktree: %v", err)
	}
	in.must("lane", "archive", "lane-1")
	in.waitFor("the removal", func() bool {
		_, err := os.Stat(worktree)
		return errors.Is(err, os.ErrNotExist) && in.lane("lane-1").Archived
	})
}

// TestAStopKeepsWorkOnThisMachineByDefault is the same lane with nobody's choice to push: the
// remote holds what it held before, the work stays on its local branch and a local ref, the
// outcome reaches acceptance, and the ledger says the work exists only here.
func TestAStopKeepsWorkOnThisMachineByDefault(t *testing.T) {
	in := newInstall(t)
	repo, bare, pushed := repoAheadOfItsRemote(t)
	worktree := runLaneThatLeavesWork(t, in, repo)
	in.waitFor("the condition", func() bool { return slices.Contains(in.lane("lane-1").Conditions, "unpushed_work") })
	if refs, _ := gitAt(repo, bare, "for-each-ref", "--format=%(refname) %(objectname)"); refs != "refs/heads/main "+pushed {
		t.Errorf("the remote holds %q", refs)
	}
	if got := repo.Git("show", "refs/ropeshift/checkpoints/lane-1/1:untracked.txt"); got != "work" {
		t.Errorf("the local ref holds %q", got)
	}
	if got := repo.Git("rev-parse", "refs/heads/lane/lane-1"); got != repo.Git("rev-parse", "main") {
		t.Errorf("the lane's branch is at %s", got)
	}
	code, _, stderr := in.run("lane", "archive", "lane-1")
	if code != cli.ExitConflict || !strings.Contains(stderr, "no remote") {
		t.Errorf("a plain removal: exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(worktree, "untracked.txt")); err != nil {
		t.Fatalf("a refused removal touched the worktree: %v", err)
	}
}

// TestRemovingUnpushedWorkTakesAForcedCommand is the case with no remote: the work has one
// copy, so the ledger shows it, a plain removal is refused, and only --force goes on.
func TestRemovingUnpushedWorkTakesAForcedCommand(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	worktree := runLaneThatLeavesWork(t, in, repo)
	in.waitFor("the condition", func() bool { return slices.Contains(in.lane("lane-1").Conditions, "unpushed_work") })
	if !strings.Contains(in.must("lane", "show", "lane-1"), "unpushed_work") {
		t.Error("the condition does not reach a person")
	}
	code, _, stderr := in.run("lane", "archive", "lane-1")
	if code != cli.ExitConflict || !strings.Contains(stderr, "no remote") {
		t.Errorf("a plain removal: exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(worktree, "untracked.txt")); err != nil {
		t.Fatalf("a refused removal touched the worktree: %v", err)
	}
	in.must("lane", "archive", "lane-1", "--force")
	in.waitFor("the forced removal", func() bool {
		_, err := os.Stat(worktree)
		return errors.Is(err, os.ErrNotExist)
	})
	if got := repo.Git("show", "refs/ropeshift/checkpoints/lane-1/1:untracked.txt"); got != "work" {
		t.Errorf("a forced removal lost the snapshot: %q", got)
	}
}

// TestAPausedFleetHoldsWhatItCan walks one control on the real binary: a pause refuses a
// claim, a lane dispatched before it waits, and run lets it go.
func TestAPausedFleetHoldsWhatItCan(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(t, "lane-completed")
	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool { return len(in.workers()) == 1 })
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	if out := in.must("fleet", "pause"); !strings.Contains(out, "pause") {
		t.Errorf("fleet pause says %q", out)
	}
	worker := in.workers()[0].ID
	if code, _, stderr := in.run("work", "claim", id, "--worker", worker, "--claim-id", "c1"); code != cli.ExitConflict {
		t.Fatalf("a claim passed a paused fleet: exit %d: %s", code, stderr)
	}
	in.must("fleet", "run")
	in.must("work", "claim", id, "--worker", worker, "--claim-id", "c1")
	in.must("fleet", "pause")
	in.must("lane", "start", id, "--lane", "lane-1")
	const settle = 300 * time.Millisecond
	time.Sleep(settle)
	if state := in.show(id).State; state != workorders.Claimed {
		t.Fatalf("a paused fleet ran a lane: %s", state)
	}
	in.must("fleet", "run")
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	var fleet api.Fleet
	if err := json.Unmarshal([]byte(in.must("fleet", "caps", "--wip", "3", "--json")), &fleet); err != nil {
		t.Fatal(err)
	}
	if fleet.WIPLimit != 3 || fleet.WorkerLimit != 0 || fleet.Mode != ledger.FleetRun {
		t.Errorf("fleet %+v", fleet)
	}
	if err := json.Unmarshal([]byte(in.must("fleet", "caps", "--per-worker", "2", "--json")), &fleet); err != nil {
		t.Fatal(err)
	}
	if fleet.WIPLimit != 3 || fleet.WorkerLimit != 2 {
		t.Errorf("a flag left out lost its value: %+v", fleet)
	}
}

// TestTheDoctorAndTheManifestAgree runs the doctor by hand and through the daemon, on a
// sign-in the fixture states: only a kind is printed, and only a kind reaches the ledger.
func TestTheDoctorAndTheManifestAgree(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	out := in.must("worker", "doctor", "--json")
	var manifest ledger.Manifest
	if err := json.Unmarshal([]byte(out), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Drivers) != 1 || manifest.Drivers[0].SignIn.Kind != ledger.SignInAPIKey || !manifest.Drivers[0].Present {
		t.Fatalf("the doctor says %s", out)
	}
	if strings.Contains(out, "example.invalid") {
		t.Errorf("the doctor printed an identity: %s", out)
	}
	if text := in.must("worker", "doctor"); !strings.Contains(text, "sign-in api_key") {
		t.Errorf("the doctor says %q", text)
	}
	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		workers := in.workers()
		return len(workers) == 1 && len(workers[0].Manifest.Drivers) == 1
	})
	listed := in.workers()[0].Manifest
	if listed.Drivers[0].SignIn != manifest.Drivers[0].SignIn || listed.Labels["os"] == "" || listed.Capacity.MaxLanes == 0 {
		t.Errorf("the ledger holds %+v", listed)
	}
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	var offered api.WorkOrderList
	if err := json.Unmarshal([]byte(in.must("work", "list", "--worker", in.workers()[0].ID, "--json")), &offered); err != nil {
		t.Fatal(err)
	}
	if len(offered.WorkOrders) != 1 || offered.WorkOrders[0].ID != id {
		t.Errorf("offered %+v", offered)
	}
}

func TestTheDoctorWithNoCLIExitsOne(t *testing.T) {
	in := newInstall(t)
	in.path = doubles.CanaryPathWithout(t)
	code, stdout, _ := in.run("worker", "doctor")
	if code != cli.ExitError || !strings.Contains(stdout, "absent") {
		t.Errorf("exit %d: %s", code, stdout)
	}
}
