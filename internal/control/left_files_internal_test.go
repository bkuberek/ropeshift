package control

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// leftFilesRig is a workspace with one change in repo-1, whose lanes report paths they left out.
type leftFilesRig struct {
	t     *testing.T
	store ports.Store
}

func newLeftFilesRig(t *testing.T) leftFilesRig {
	t.Helper()
	ctx := t.Context()
	store, err := fake.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: "w"}, member); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRepositories(store).Detect(ctx, "w", DetectedRepository{ID: "repo-1", DefaultBranch: "main", Directory: "app"}, member); err != nil {
		t.Fatal(err)
	}
	// The worker whose reports raise the questions, registered as a join registers one.
	err = store.Installation("w").Write(ctx, "test.worker", func(tx ports.SharedTx) error {
		if err := tx.Workers().Insert(ledger.WorkerRecord{Worker: ledger.Worker{ID: "worker-1", Name: "w"}}); err != nil {
			return err
		}
		_, err := tx.Append(workerEvent(eventWorkerRegistered, "worker-1", ledger.Actor{Kind: ledger.ActorWorker, ID: "worker-1"}))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewWorkOrders(store).Record(ctx, "w", LedgerWork{
		Order:    workorders.WorkOrder{ID: "wo", Kind: workorders.KindChange, Title: "T", RepositoryID: "repo-1"},
		Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
	}, member); err != nil {
		t.Fatal(err)
	}
	return leftFilesRig{t: t, store: store}
}

// report folds one lane guard report that left the paths out.
func (r leftFilesRig) report(id string, paths ...string) {
	r.t.Helper()
	r.reportUnasked(id, paths...)
}

// reportUnasked folds one report and returns the paths it could not ask about.
func (r leftFilesRig) reportUnasked(id string, paths ...string) []string {
	r.t.Helper()
	var unasked []string
	err := r.store.Write(r.t.Context(), "w", "test.guard", func(tx ports.WriteTx) error {
		var err error
		unasked, err = askLeftFiles(tx, "worker-1", id, ledger.UploadEvent{WorkOrder: "wo"}, ledger.LaneGuard{LeftOut: paths})
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return unasked
}

func (r leftFilesRig) asked() []questions.Question {
	r.t.Helper()
	var rows []questions.Question
	err := r.store.Read(r.t.Context(), "w", func(tx ports.ReadTx) error {
		var err error
		rows, err = tx.Questions().Questions(ports.RecordFilter{Limit: ports.MaxRecords})
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return rows
}

// TestALeftOutPathIsDecidedOnce is FR-LANE-20's record per path: the orchestrator decides the question on rung 3
// under the built-in policy, and a later lane that leaves the same path out in the same repository asks nothing.
func TestALeftOutPathIsDecidedOnce(t *testing.T) {
	r := newLeftFilesRig(t)
	r.report("event-1", ".atl/")
	r.report("event-2", ".atl/")
	asked := r.asked()
	if len(asked) != 1 || !slices.Equal(asked[0].Categories, []string{questions.CategoryFiles}) || asked[0].Blocking {
		t.Fatalf("questions %+v", asked)
	}
	id := asked[0].ID
	if _, err := r.rule(id, "d-atl", questions.LeftFileIgnoreOption(".atl/"), "repo:repo-1"); err != nil {
		t.Fatalf("the orchestrator may not decide a left-out path: %v", err)
	}
	r.report("event-3", ".atl/", ".idea/")
	asked = r.asked()
	if len(asked) != 2 {
		t.Fatalf("questions %+v", asked)
	}
	for _, question := range asked {
		if question.ID != id && question.Status != questions.StatusOpen {
			t.Errorf("the new path's question %+v", question)
		}
	}
}

// TestADismissedPathIsNotAskedAgain holds a dismissal as the record's answer: no later report asks again.
func TestADismissedPathIsNotAskedAgain(t *testing.T) {
	r := newLeftFilesRig(t)
	r.report("event-1", ".atl/")
	asked := r.asked()
	if len(asked) != 1 {
		t.Fatalf("questions %+v", asked)
	}
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	if err := NewQuestions(r.store, questions.BatchPolicy{}).Dismiss(t.Context(), "w", asked[0].ID, member); err != nil {
		t.Fatal(err)
	}
	r.report("event-2", ".atl/")
	if again := r.asked(); len(again) != 1 {
		t.Errorf("a dismissed path was asked again: %+v", again)
	}
}

// TestPathsThatDifferOnlyInCaseAreTwoQuestions keeps Makefile and makefile apart, which the record's hash folds
// together.
func TestPathsThatDifferOnlyInCaseAreTwoQuestions(t *testing.T) {
	r := newLeftFilesRig(t)
	r.report("event-1", "Makefile", "makefile")
	if asked := r.asked(); len(asked) != 2 {
		t.Errorf("questions %+v", asked)
	}
}

// TestAPathThatMakesNoQuestionIsNamed returns a path too long for a question, for the lane's event to name, and
// asks about the rest.
func TestAPathThatMakesNoQuestionIsNamed(t *testing.T) {
	r := newLeftFilesRig(t)
	long := strings.Repeat("a", questions.MaxFieldBytes) + "/"
	if unasked := r.reportUnasked("event-1", long, ".atl/"); !slices.Equal(unasked, []string{long}) {
		t.Errorf("unasked %d paths", len(unasked))
	}
	if asked := r.asked(); len(asked) != 1 || !strings.Contains(asked[0].Text, `".atl/"`) {
		t.Errorf("questions %+v", asked)
	}
}

// rule is the orchestrator deciding one question on rung 3 with a statement and a scope.
func (r leftFilesRig) rule(id, decision, statement, scope string) (questions.Decision, error) {
	r.t.Helper()
	ctx := r.t.Context()
	ops := NewQuestions(r.store, questions.BatchPolicy{})
	holder := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "orchestrator-a"}
	if _, err := ops.Take(ctx, "w", id, 60_000, holder); err != nil {
		r.t.Fatal(err)
	}
	for _, rung := range []int64{questions.RungLookup, questions.RungOpen} {
		if _, err := ops.Climb(ctx, "w", id, questions.Step{Rung: rung, Outcome: "no_match"}, holder); err != nil && rung == questions.RungLookup {
			r.t.Fatal(err)
		}
	}
	return ops.Rule(ctx, "w", id, questions.Ruling{
		ID: decision, Status: questions.DecisionDecided, Statement: statement, Rationale: "What the path holds.",
		Citations: []string{"the lane report that left it out"}, Scope: scope, Reversibility: questions.Reversible,
	}, holder)
}

func (r leftFilesRig) questionOf(path string) string {
	r.t.Helper()
	for _, question := range r.asked() {
		if _, named, ok := questions.ReadLeftFile(question); ok && named == path {
			return question.ID
		}
	}
	r.t.Fatalf("no question names %s", path)
	return ""
}

// TestADecidedPathReachesItsRepositorysLanes is what a lane in the repository acts on: each path decided ignored,
// for its .gitignore, and each decided part of the project, which counts as the lane's own. A ruling that states
// neither option is refused, and one with no scope takes the repository's.
func TestADecidedPathReachesItsRepositorysLanes(t *testing.T) {
	r := newLeftFilesRig(t)
	r.report("event-1", ".atl/", ".tool/", ".unsure/")
	if _, err := r.rule(r.questionOf(".atl/"), "d-atl", questions.LeftFileIgnoreOption(".atl/"), ""); err != nil {
		t.Fatal(err)
	}
	decided, err := r.rule(r.questionOf(".tool/"), "d-tool", questions.LeftFileCommitOption(".tool/"), "")
	if err != nil || decided.Scope != "repo:repo-1" {
		t.Fatalf("a ruling with no scope: %+v, %v", decided, err)
	}
	if _, err := r.rule(r.questionOf(".unsure/"), "d-unsure", "Leave it for now", "repo:repo-1"); !errors.Is(err, questions.ErrInvalid) {
		t.Errorf("a ruling that states neither option: %v", err)
	}
	var files *ledger.DesiredFiles
	err = r.store.Read(t.Context(), "w", func(tx ports.ReadTx) error {
		var err error
		files, err = desiredFiles(tx, "repo-1")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == nil || !slices.Equal(files.Ignore, []string{".atl/"}) || !slices.Equal(files.Keep, []string{".tool/"}) {
		t.Errorf("the repository's decided files %+v", files)
	}
}

// decidedFiles is what lanes in repo-1 are told the orchestrator decided.
func (r leftFilesRig) decidedFiles() *ledger.DesiredFiles {
	r.t.Helper()
	var files *ledger.DesiredFiles
	err := r.store.Read(r.t.Context(), "w", func(tx ports.ReadTx) error {
		var err error
		files, err = desiredFiles(tx, "repo-1")
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return files
}

// TestOnlyAPathAWorkerReportedIsDecidedForLanes is a lane or a member that asks a question in the files category with
// a left-out path's text, so that a decision on it would ignore or keep a path of its choosing. No asker may give it a
// left-out path's ID, and a question that reads as one under another ID, or that no worker asked, decides nothing a
// lane acts on.
func TestOnlyAPathAWorkerReportedIsDecidedForLanes(t *testing.T) {
	r := newLeftFilesRig(t)
	ctx := t.Context()
	ops := NewQuestions(r.store, questions.BatchPolicy{})
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	crafted, err := questions.LeftFile("files-crafted", "repo-1", "src/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ops.RecordQuestion(ctx, "w", crafted, member); !errors.Is(err, questions.ErrInvalid) {
		t.Errorf("a member recorded a question by a left-out path's ID: %v", err)
	}
	asked := questions.Ask{ID: "files-asked", Text: crafted.Text, Categories: crafted.Categories, Options: crafted.Options, Lane: "lane-1", Generation: 1}
	if _, err := ops.Ask(ctx, "w", asked, ledger.Actor{Kind: ledger.ActorLane, ID: "lane-1"}); !errors.Is(err, questions.ErrInvalid) {
		t.Errorf("a lane asked a question by a left-out path's ID: %v", err)
	}
	lookalike := crafted
	lookalike.ID = "q-lookalike"
	if _, err := ops.RecordQuestion(ctx, "w", lookalike, member); err != nil {
		t.Fatal(err)
	}
	forged, err := questions.LeftFile("files-forged", "repo-1", "lib/")
	if err != nil {
		t.Fatal(err)
	}
	forged.AskedBy = "member-1"
	err = r.store.Write(ctx, "w", "test.forged", func(tx ports.WriteTx) error { return insertQuestion(tx, forged, member) })
	if err != nil {
		t.Fatal(err)
	}
	r.report("event-1", ".atl/")
	for id, path := range map[string]string{"q-lookalike": "src/", "files-forged": "lib/", r.questionOf(".atl/"): ".atl/"} {
		if _, err := r.rule(id, "d-"+id, questions.LeftFileIgnoreOption(path), "repo:repo-1"); err != nil {
			t.Fatalf("ruling on %s: %v", id, err)
		}
	}
	if files := r.decidedFiles(); files == nil || !slices.Equal(files.Ignore, []string{".atl/"}) || len(files.Keep) != 0 {
		t.Errorf("the repository's decided files %+v", files)
	}
}

// TestAGitignoreIsNeverDecidedIgnored refuses the ruling, so no lane adds a line that would hide its .gitignore.
func TestAGitignoreIsNeverDecidedIgnored(t *testing.T) {
	r := newLeftFilesRig(t)
	r.report("event-1", ".gitignore")
	if _, err := r.rule(r.questionOf(".gitignore"), "d-gitignore", questions.LeftFileIgnoreOption(".gitignore"), ""); !errors.Is(err, questions.ErrInvalid) {
		t.Errorf("a .gitignore was decided ignored: %v", err)
	}
	if files := r.decidedFiles(); files != nil {
		t.Errorf("the repository's decided files %+v", files)
	}
}

// TestAPathWithALineBreakRaisesNoQuestion names the path for the lane's event, as one too long does.
func TestAPathWithALineBreakRaisesNoQuestion(t *testing.T) {
	r := newLeftFilesRig(t)
	if unasked := r.reportUnasked("event-1", "x\n*", ".atl/"); !slices.Equal(unasked, []string{"x\n*"}) {
		t.Errorf("unasked %q", unasked)
	}
	if asked := r.asked(); len(asked) != 1 {
		t.Errorf("questions %+v", asked)
	}
}
