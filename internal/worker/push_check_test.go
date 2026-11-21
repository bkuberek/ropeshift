package worker_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// staleLane is a running lane in repo-1 whose claim names no base commit, so the worker cannot read its commits, and
// whose last stop refused a push of a file of another's.
func staleLane(t *testing.T, decided func(*laneRig)) (*laneRig, *gate) {
	t.Helper()
	base := newRig(t)
	base.worktrees, base.repository = fake.NewWorktrees(), "repo-1"
	if _, err := control.NewRepositories(base.store).Detect(t.Context(), scope, control.DetectedRepository{ID: "repo-1", DefaultBranch: "main"}, member); err != nil {
		t.Fatal(err)
	}
	r := newLaneRigFor(t, base)
	r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", "run_mode = \"tmux\"\n"+approvalProfile[len(profileAt):])
	decided(r)
	r.dispatched()
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	r.atGate(g)
	if err := r.journal.SaveGuard(t.Context(), "lane-1", lanes.GuardResult{Refused: []string{foreignFile}}); err != nil {
		t.Fatal(err)
	}
	return r, g
}

// TestAPushTheWorkerCannotCheckSaysSoAndNamesNoBase is a refusal an earlier stop made, with no base to read the
// lane's commits from now: the push stays refused, saying why the worker cannot check and what ends that, and never
// advises a reset to a base it does not know.
func TestAPushTheWorkerCannotCheckSaysSoAndNamesNoBase(t *testing.T) {
	t.Parallel()
	r, g := staleLane(t, func(*laneRig) {})
	reply := r.look(t, `{"command":"git push origin lane/lane-1"}`, "c-push", false)
	if reply.Approval == nil || reply.Approval.Decision != lanes.ApprovalDeny {
		t.Fatalf("the push was answered %+v", reply)
	}
	reason := reply.Approval.Reason
	if !strings.Contains(reason, "cannot check") || !strings.Contains(reason, foreignFile) || !strings.Contains(reason, "holds until") ||
		strings.Contains(reason, "git reset") {
		t.Errorf("the refusal reads %q", reason)
	}
	g.free()
}

// TestAStaleRefusalOfAPathTheProjectKeepsEnds reads an earlier refusal against the paths the orchestrator decided
// since: a path it decided is part of the project no longer holds the push back.
func TestAStaleRefusalOfAPathTheProjectKeepsEnds(t *testing.T) {
	t.Parallel()
	keep := func(r *laneRig) { r.decide(t, "files-atl", ".atl/", questions.LeftFileCommitOption(".atl/")) }
	r, g := staleLane(t, keep)
	reply := r.look(t, `{"command":"git push origin lane/lane-1"}`, "c-push", false)
	if reply.Approval != nil && reply.Approval.Decision == lanes.ApprovalDeny && strings.Contains(reply.Approval.Reason, foreignFile) {
		t.Errorf("a path the project keeps still holds the push back: %+v", reply)
	}
	g.free()
}
