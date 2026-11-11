package exit

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/gittest"
)

// continuingContract is a contract whose one work order finishes an in-flight branch.
func continuingContract(id, title, branch string) string {
	text := contract.ContractText(id, title, "Finish what was started.")
	return strings.Replace(text, "size = \"small\"", "size = \"small\"\ncontinues = \""+branch+"\"", 1)
}

// TestALaneContinuesAnInFlightBranch is FR-ADOPT-7 on the real binary, with real git. Someone started work
// on a branch, and the base moved on after it. A work order that continues the branch starts its lane at the
// branch's tip on a branch of its own, records where the branch left the base as its base commit, and the
// branch itself never moves. A work order that continues a branch nobody holds is an intake problem, and
// its claim is held with the reason.
func TestALaneContinuesAnInFlightBranch(t *testing.T) {
	in := newInstall(t)
	in.aLaneThatCommits()
	repo := gittest.New(t)
	fork := repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	repo.Git("switch", "--quiet", "-c", "feature/retry")
	started := repo.Commit("retry.txt", "started\n")
	repo.Git("switch", "--quiet", "main")
	repo.Commit("finish.md", continuingContract("finish", "Finish", "feature/retry"))
	repo.Commit("gone.md", continuingContract("gone", "Gone", "feature/gone"))
	laneRoot := filepath.Join(filepath.Dir(repo.Dir), filepath.Base(repo.Dir)+"-lanes")

	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	t.Cleanup(d.stop)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool { return len(in.workers()) == 1 })
	ids := in.workOrders(2)
	finish, gone := ids["The work order of finish"], ids["The work order of gone"]

	// The branch that is gone is an intake problem for its work order.
	in.waitFor("the problem of the branch that is gone", func() bool {
		for _, source := range in.sources() {
			if slices.ContainsFunc(source.Problems, func(p planning.SourceProblem) bool {
				return p.Path == "gone.md" && strings.Contains(p.Detail, "feature/gone")
			}) {
				return true
			}
		}
		return false
	})
	in.must("work", "review", gone)
	code, _, stderr := in.run("work", "claim", gone, "--worker", in.workers()[0].ID, "--claim-id", "c0")
	if code != cli.ExitConflict || !strings.Contains(stderr, string(workorders.HoldContinuedBranchMissing)) || !strings.Contains(stderr, "feature/gone") {
		t.Errorf("a claim on work whose branch is gone: exit %d: %s", code, stderr)
	}

	in.must("work", "review", finish)
	in.startLane(finish, "c1", "lane-1")
	in.waitFor("the lane's outcome", func() bool { return in.show(finish).State == workorders.Acceptance })

	lane := in.lane("lane-1")
	if lane.ContinuesRef != "feature/retry" || lane.ContinuesCommit != started || lane.BaseRef != "main" || lane.BaseCommit != fork {
		t.Errorf("the lane continues %s at %s from %s at %s; want feature/retry at %s from main at %s",
			lane.ContinuesRef, lane.ContinuesCommit, lane.BaseRef, lane.BaseCommit, started, fork)
	}
	if text := in.must("lane", "show", "lane-1"); !strings.Contains(text, "continues\tfeature/retry\t"+started) || !strings.Contains(text, "base\tmain\t"+fork) {
		t.Errorf("lane show does not say what the lane continues:\n%s", text)
	}
	if contents, err := os.ReadFile(filepath.Join(laneRoot, "lane-1", "retry.txt")); err != nil || strings.TrimSpace(string(contents)) != "started" {
		t.Errorf("the lane's worktree does not hold the branch's work: %q, %v", contents, err)
	}
	head := branchesOf(in.acceptance(finish))[0]
	if head.URI == "feature/retry" || repo.Git("merge-base", "--is-ancestor", started, head.Digest) != "" {
		t.Errorf("the lane's branch %s at %s does not grow from the branch it continues", head.URI, head.Digest)
	}
	if now := repo.Git("rev-parse", "refs/heads/feature/retry"); now != started {
		t.Errorf("the continued branch moved from %s to %s", started, now)
	}
	// What review reads begins where the branch left main, so it holds the branch's work and the lane's.
	if diff := repo.Git("diff", "--name-only", lane.BaseCommit, head.Digest); !strings.Contains(diff, "retry.txt") || !strings.Contains(diff, "work.txt") {
		t.Errorf("the change from the base commit is:\n%s", diff)
	}
}
