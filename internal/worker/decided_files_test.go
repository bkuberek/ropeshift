package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

const foreignFile = ".atl/registry.md"

// decidedRig is a lane in repo-1, which the ledger holds, whose claim names a base commit.
func decidedRig(t *testing.T, profile string) (*laneRig, *fake.Worktrees) {
	t.Helper()
	return decidedRigOn(t, profile, false)
}

// decidedRigOn is the same lane, in a repository that reviews through pull requests when asked.
func decidedRigOn(t *testing.T, profile string, pullRequests bool) (*laneRig, *fake.Worktrees) {
	t.Helper()
	base := newRig(t)
	worktrees := fake.NewWorktrees()
	worktrees.Grow("/repository", "main")
	base.worktrees, base.repository = worktrees, "repo-1"
	base.starts = &control.Starts{Bases: worktrees, Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) { return "/repository", nil }}
	repositories := control.NewRepositories(base.store)
	detected := control.DetectedRepository{ID: "repo-1", DefaultBranch: "main"}
	if pullRequests {
		detected.Remote = "git@github.com:acme/widgets.git"
	}
	if _, err := repositories.Detect(t.Context(), scope, detected, member); err != nil {
		t.Fatal(err)
	}
	if pullRequests {
		target := ledger.ReviewOnPullRequest
		if _, err := repositories.Change(t.Context(), scope, "repo-1", control.RepositoryChange{ReviewTarget: &target}, member); err != nil {
			t.Fatal(err)
		}
		worktrees.UseRemote("origin")
	}
	r := newLaneRigFor(t, base)
	if pullRequests {
		r.config.Forge = &fake.Forge{}
	}
	if profile != "" {
		r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", profile)
	}
	return r, worktrees
}

// decide has the orchestrator decide a path a lane left out in repo-1, with one of its options, on the question the
// control plane records when this lane's worker reports the path. No asker may record one by that ID.
func (r *laneRig) decide(t *testing.T, id, path, statement string) {
	t.Helper()
	ctx := t.Context()
	ops := control.NewQuestions(r.store, questions.BatchPolicy{})
	question, err := questions.LeftFile(id, "repo-1", path)
	if err != nil {
		t.Fatal(err)
	}
	question.AskedBy = r.subject.Auth().WorkerID
	asker := ledger.Actor{Kind: ledger.ActorWorker, ID: question.AskedBy}
	if err := r.store.Write(ctx, scope, "test.left_file", func(tx ports.WriteTx) error {
		question.AskedAt = tx.Now()
		if err := tx.Questions().Insert(question); err != nil {
			return err
		}
		event, err := questions.RecordEvent(questions.EventQuestionChanged, questions.SubjectQuestion, question.ID, question, asker)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	holder := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "orchestrator-a"}
	if _, err := ops.Take(ctx, scope, id, 60_000, holder); err != nil {
		t.Fatal(err)
	}
	for _, rung := range []int64{questions.RungLookup, questions.RungOpen} {
		if _, err := ops.Climb(ctx, scope, id, questions.Step{Rung: rung, Outcome: "no_match"}, holder); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ops.Rule(ctx, scope, id, questions.Ruling{
		ID: "d-" + id, Status: questions.DecisionDecided, Statement: statement, Rationale: "What the path holds.",
		Citations: []string{"a lane report"}, Reversibility: questions.Reversible,
	}, holder); err != nil {
		t.Fatal(err)
	}
}

// TestALaneIgnoresWhatTheOrchestratorDecidedBeforeItsSessionStarts is FR-LANE-20 acting on an ignore decision: the
// next lane in the repository adds the path to its .gitignore in a commit of that file alone, before its session.
func TestALaneIgnoresWhatTheOrchestratorDecidedBeforeItsSessionStarts(t *testing.T) {
	t.Parallel()
	r, worktrees := decidedRig(t, "")
	r.decide(t, "files-atl", ".atl/", questions.LeftFileIgnoreOption(".atl/"))
	r.decide(t, "files-tool", ".tool/", questions.LeftFileCommitOption(".tool/"))
	var before []ports.Commit
	r.driver.Play(script([]fake.DriverStep{
		{Bound: true}, {Do: func(ports.SessionSpec) { before, _ = worktrees.Commits(context.Background(), ownWorktree, "") }},
		r.reports(workorders.OutcomeCompleted), completed(0.01),
	}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || before[0].Subject != lanes.IgnoreCommitSubject || !slices.Equal(before[0].Files, []string{".gitignore"}) {
		t.Fatalf("the session started after the commits %+v", before)
	}
	if text, _ := worktrees.FileText(ownWorktree, ".gitignore"); text != "/.atl/\n" {
		t.Errorf("the .gitignore holds %q", text)
	}
}

// pushingLane is an interactive lane whose setup left a cache before its first turn, and whose session then
// committed a file of that cache beside one of its own.
func pushingLane(t *testing.T, profile string, decided func(*laneRig)) (*laneRig, *fake.Worktrees, *gate) {
	t.Helper()
	return pushingLaneOn(t, profile, decided, false)
}

func pushingLaneOn(t *testing.T, profile string, decided func(*laneRig), pullRequests bool) (*laneRig, *fake.Worktrees, *gate) {
	t.Helper()
	r, worktrees := decidedRigOn(t, "run_mode = \"tmux\"\n"+profile, pullRequests)
	decided(r)
	r.dispatched()
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	r.atGate(g)
	worktrees.ChangeFiles(ownWorktree, foreignFile)
	r.hook(t, lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "s", AtMs: 1000})
	r.awaitReading(t)
	if _, err := worktrees.CommitFiles(t.Context(), ownWorktree, []ports.TreeFile{{Path: foreignFile, Text: "a cache\n"}, {Path: "gen/out.go", Text: "package gen\n"}}, "work"); err != nil {
		t.Fatal(err)
	}
	worktrees.ChangeFiles(ownWorktree)
	return r, worktrees, g
}

// TestALanesPushOfAnotherFileIsRefusedSoItCanFixItsCommits answers the lane's own push before any person is asked,
// naming the path and how to take it out, while the lane still runs.
func TestALanesPushOfAnotherFileIsRefusedSoItCanFixItsCommits(t *testing.T) {
	t.Parallel()
	r, _, g := pushingLane(t, approvalProfile[len(profileAt):], func(*laneRig) {})
	reply := r.look(t, `{"command":"git push origin lane/lane-1"}`, "c-push", false)
	if reply.Approval == nil || reply.Approval.Decision != lanes.ApprovalDeny || !strings.Contains(reply.Approval.Reason, foreignFile) ||
		!strings.Contains(reply.Approval.Reason, "git reset --soft") {
		t.Fatalf("the push was answered %+v", reply)
	}
	g.free()
}

// TestAPathTheProjectKeepsMayBePushed is a commit decision: the path counts as the lane's own.
func TestAPathTheProjectKeepsMayBePushed(t *testing.T) {
	t.Parallel()
	keep := func(r *laneRig) { r.decide(t, "files-atl", ".atl/", questions.LeftFileCommitOption(".atl/")) }
	r, _, g := pushingLane(t, approvalProfile[len(profileAt):], keep)
	reply := r.look(t, `{"command":"git push origin lane/lane-1"}`, "c-push", false)
	if reply.Approval != nil && reply.Approval.Decision == lanes.ApprovalDeny && strings.Contains(reply.Approval.Reason, foreignFile) {
		t.Errorf("a path the project keeps was refused: %+v", reply)
	}
	g.free()
}

// TestTheStopGuardDoesNotPushAnotherFile is the worker's own push: the branch and its snapshot stay on this machine,
// the lane reads as holding unpushed work, and its report names the path.
func TestTheStopGuardDoesNotPushAnotherFile(t *testing.T) {
	t.Parallel()
	r, worktrees, g := pushingLane(t, "", func(r *laneRig) { r.choosesPush() })
	worktrees.UseRemote("origin")
	worktrees.Commit(ownWorktree)
	g.free()
	r.waitForTheLane(t)
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if pushed := worktrees.Pushed(); len(pushed) != 0 {
		t.Errorf("the guard pushed %v", pushed)
	}
	if !slices.Contains(r.view().Conditions, string(lanes.UnpushedWork)) {
		t.Errorf("conditions %v", r.view().Conditions)
	}
	var refused []string
	for _, event := range r.events() {
		if event.Type != ledger.EventLaneGuarded {
			continue
		}
		var guard ledger.LaneGuard
		if err := json.Unmarshal(event.Data, &guard); err != nil {
			t.Fatal(err)
		}
		refused = guard.Refused
	}
	if !slices.Equal(refused, []string{foreignFile}) {
		t.Errorf("the guard reported %q as refused", refused)
	}
}

// TestAWorkerDoesNotPublishABranchThatHoldsAnotherFile is the push for a pull request: the worker reads the lane's
// commits before it pushes, pushes nothing, opens no pull request, and its report names the path.
func TestAWorkerDoesNotPublishABranchThatHoldsAnotherFile(t *testing.T) {
	t.Parallel()
	r, worktrees, g := pushingLaneOn(t, "", func(*laneRig) {}, true)
	g.free()
	r.waitForTheLane(t)
	forge := r.config.Forge.(*fake.Forge)
	forge.Push(widgets, "lane/lane-1", fake.LaneHead)
	for range 3 {
		if err := r.subject.Converge(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if pushed := worktrees.Pushed(); slices.Contains(pushed, lanes.HeadsRef("lane/lane-1")) {
		t.Errorf("the worker pushed %v", pushed)
	}
	if prs := forge.PullRequests(); len(prs) != 0 {
		t.Errorf("the forge holds %+v", prs)
	}
	refused := false
	for _, event := range r.events() {
		refused = refused || strings.Contains(string(event.Data), foreignFile) && strings.Contains(string(event.Data), "Nothing was pushed")
	}
	if !refused {
		t.Error("no event names the refused path")
	}
}

// TestAPathTheBranchTracksIsNeverIgnored is an ignore decision on a file the branch tracks and on a folder that holds
// tracked files: a survey reads no ignored file, so a new file there would never read as a change, and a discard would
// lose it. Only the untracked path reaches the .gitignore.
func TestAPathTheBranchTracksIsNeverIgnored(t *testing.T) {
	t.Parallel()
	r, worktrees := decidedRig(t, "")
	worktrees.SeedFile(ownWorktree, "src/main.go", "package main\n")
	worktrees.SeedFile(ownWorktree, "README.md", "# Widgets\n")
	for id, path := range map[string]string{"files-src": "src/", "files-readme": "README.md", "files-atl": ".atl/"} {
		r.decide(t, id, path, questions.LeftFileIgnoreOption(path))
	}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if text, _ := worktrees.FileText(ownWorktree, ".gitignore"); text != "/.atl/\n" {
		t.Errorf("the .gitignore holds %q", text)
	}
}

// TestAWorkerThatCannotReadTheBranchItPublishesPushesNothing is the push for a pull request from a checkout the worker
// cannot read: it pushes nothing, opens no pull request, and its report says why.
func TestAWorkerThatCannotReadTheBranchItPublishesPushesNothing(t *testing.T) {
	t.Parallel()
	r, worktrees, g := pushingLaneOn(t, "", func(*laneRig) {}, true)
	worktrees.FailBranchChanges(errors.New("the checkout is locked"))
	g.free()
	r.waitForTheLane(t)
	forge := r.config.Forge.(*fake.Forge)
	forge.Push(widgets, "lane/lane-1", fake.LaneHead)
	for range 3 {
		if err := r.subject.Converge(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if pushed := worktrees.Pushed(); slices.Contains(pushed, lanes.HeadsRef("lane/lane-1")) {
		t.Errorf("the worker pushed %v", pushed)
	}
	if prs := forge.PullRequests(); len(prs) != 0 {
		t.Errorf("the forge holds %+v", prs)
	}
	said := false
	for _, event := range r.events() {
		said = said || strings.Contains(string(event.Data), "cannot check") && strings.Contains(string(event.Data), "the checkout is locked")
	}
	if !said {
		t.Error("no event says why the branch went unchecked")
	}
}
