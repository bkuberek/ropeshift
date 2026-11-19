package cli_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// hasLine reports a line of output that begins with a label.
func hasLine(text, label string) bool {
	for line := range strings.Lines(text) {
		if strings.HasPrefix(line, label) {
			return true
		}
	}
	return false
}

// reviewedWork is work that waited for acceptance until its review gave a verdict it cannot judge.
func reviewedWork(t *testing.T, s *session) {
	t.Helper()
	w := apitest.NewWorld(t, s.daemon)
	w.Running("wo", "Trim the log").End(workorders.OutcomeCompleted, "")
	w.Reviewed("wo", workorders.CriterionVerdict{
		Criterion: "c1", Verdict: workorders.VerdictCannotJudge,
		Reasons:   "The log is trimmed in trim.go,\nbut nothing I can run shows it keeps the last day.",
		Citations: []string{"trim.go:30", "FR-LOG-2"},
	})
}

// A person at a terminal reads the review's verdict for each criterion before deciding (FR-MERGE-4).
func TestWorkShowPrintsTheReviewsAndTheirVerdicts(t *testing.T) {
	s := newSession(t)
	reviewedWork(t, s)
	text := s.must("work", "show", "wo")
	for _, want := range []string{
		"\tdone\tattempt 2",
		"verdict\tc1\tcannot_judge\tThe log is trimmed in trim.go, but nothing I can run shows it keeps the last day.\ttrim.go:30, FR-LOG-2",
		"waiting\tfor a member: the review could not judge criterion c1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("show leaves out %q:\n%s", want, text)
		}
	}
	if !hasLine(text, "review\t") {
		t.Errorf("show names no review:\n%s", text)
	}
}

func TestWorkShowJSONKeepsTheWorkOrderAndAddsTheReview(t *testing.T) {
	s := newSession(t)
	reviewedWork(t, s)
	var detail api.WorkDetail
	if err := json.Unmarshal([]byte(s.must("work", "show", "wo", "--json")), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.ID != "wo" || detail.State != workorders.Acceptance || len(detail.JudgedBy) != 1 || len(detail.Verdicts) != 1 {
		t.Errorf("detail %+v", detail)
	}
	// What read the work order before still reads it.
	var row workorders.WorkOrder
	if err := json.Unmarshal([]byte(s.must("work", "show", "wo", "--json")), &row); err != nil || row.ID != "wo" {
		t.Errorf("row %+v: %v", row, err)
	}
}

func TestWorkShowOfWorkNobodyReviewedPrintsNoReview(t *testing.T) {
	s := newSession(t)
	s.create("plain")
	text := s.must("work", "show", "plain")
	if hasLine(text, "review\t") || hasLine(text, "verdict\t") {
		t.Errorf("show of unreviewed work:\n%s", text)
	}
	var detail api.WorkDetail
	if err := json.Unmarshal([]byte(s.must("work", "show", "plain", "--json")), &detail); err != nil || len(detail.JudgedBy) != 0 || len(detail.Verdicts) != 0 {
		t.Errorf("detail %+v: %v", detail, err)
	}
}

func TestWorkAcceptancePrintsTheVerdicts(t *testing.T) {
	s := newSession(t)
	reviewedWork(t, s)
	text := s.must("work", "acceptance", "wo")
	if !strings.Contains(text, "verdict\tc1\tcannot_judge\t") {
		t.Errorf("acceptance leaves out the verdicts:\n%s", text)
	}
}

// notedWork is reviewedWork whose review also left two security notes, the second with a control sequence in it.
func notedWork(t *testing.T, s *session) {
	t.Helper()
	w := apitest.NewWorld(t, s.daemon)
	w.Running("wo", "Trim the log").End(workorders.OutcomeCompleted, "")
	w.ReviewedWithNotes("wo", []workorders.ReviewNote{
		{Point: workorders.PointInjection, Reasons: "trim.go passes the\nname to a shell.", Citations: []string{"trim.go:44", "FR-LOG-2"}},
		{Point: workorders.PointAuthority, Reasons: "The workflow gains\x1b[31m write access."},
	}, workorders.CriterionVerdict{Criterion: "c1", Verdict: workorders.VerdictPass, Reasons: "It trims the log."})
}

// A person reads a review's security notes after its verdicts, in work show and in work acceptance.
func TestWorkShowAndAcceptancePrintTheSecurityNotesAfterTheVerdicts(t *testing.T) {
	s := newSession(t)
	notedWork(t, s)
	for _, args := range [][]string{{"work", "show", "wo"}, {"work", "acceptance", "wo"}} {
		text := s.must(args...)
		verdict := strings.Index(text, "verdict\tc1\tpass\t")
		header := strings.Index(text, "security notes\t2\n")
		first := strings.Index(text, "note\tinjection\ttrim.go passes the name to a shell.\ttrim.go:44, FR-LOG-2\n")
		second := strings.Index(text, "note\tauthority\t")
		if verdict < 0 || header < verdict || first < header || second < first {
			t.Errorf("%v prints the notes out of place:\n%s", args, text)
		}
		if strings.Contains(text, "\x1b") {
			t.Errorf("%v prints a control sequence of a note:\n%s", args, text)
		}
	}
}

func TestWorkShowJSONCarriesTheSecurityNotes(t *testing.T) {
	s := newSession(t)
	notedWork(t, s)
	var detail api.WorkDetail
	if err := json.Unmarshal([]byte(s.must("work", "show", "wo", "--json")), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Notes) != 2 || detail.Notes[0].Point != workorders.PointInjection || detail.Notes[1].Point != workorders.PointAuthority ||
		len(detail.Notes[0].Citations) != 2 || detail.Notes[0].Commit == "" {
		t.Errorf("notes %+v", detail.Notes)
	}
}

func TestWorkShowOfAReviewWithNoNotesPrintsNone(t *testing.T) {
	s := newSession(t)
	reviewedWork(t, s)
	if text := s.must("work", "show", "wo"); hasLine(text, "security notes") || hasLine(text, "note\t") {
		t.Errorf("show of a review with no note:\n%s", text)
	}
}

// A person reads how much a lane's branch changed beside the branch (FR-OBS-13).
func TestWorkAcceptancePrintsWhatTheBranchChanged(t *testing.T) {
	s := newSession(t)
	s.daemon.Completed(t, "wo", workorders.LaneResult{
		Report: workorders.Report{Outcome: workorders.OutcomeCompleted}, Branch: "lane/wo", Head: "abc1230000000000000000000000000000000000",
		Change: &ledger.ChangeSize{Files: 3, Added: 40, Removed: 12},
	})
	s.daemon.Completed(t, "unmeasured", workorders.LaneResult{
		Report: workorders.Report{Outcome: workorders.OutcomeCompleted}, Branch: "lane/unmeasured", Head: "def4560000000000000000000000000000000000",
	})
	if text := s.must("work", "acceptance", "wo"); !strings.Contains(text, "artifact\tbranch\tlane/wo\tabc1230000000000000000000000000000000000\t3 files\t+40\t-12\n") {
		t.Errorf("acceptance leaves out the change:\n%s", text)
	}
	if text := s.must("work", "acceptance", "unmeasured"); !strings.Contains(text, "artifact\tbranch\tlane/unmeasured\tdef4560000000000000000000000000000000000\n") {
		t.Errorf("a branch with no change size:\n%s", text)
	}
	var view struct {
		Artifacts []ledger.Artifact `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(s.must("work", "acceptance", "wo", "--json")), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Artifacts) != 1 || view.Artifacts[0].Change == nil || *view.Artifacts[0].Change != (ledger.ChangeSize{Files: 3, Added: 40, Removed: 12}) {
		t.Errorf("the artifacts read %+v", view.Artifacts)
	}
}

// pullRequestOn keeps a pull request for a work order as its worker would read it from the forge, and a forge
// check on record for the work's attempt that the pull request lists too.
func pullRequestOn(t *testing.T, s *session, id string, pr ledger.PullRequest) {
	t.Helper()
	err := s.daemon.Store.Write(t.Context(), apitest.Scope, "test.pull_request", func(tx ports.WriteTx) error {
		row, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		held, err := tx.Evidence().Attempt(id, row.Fence)
		if err != nil || len(held) == 0 {
			return fmt.Errorf("no evidence on record for %s: %v", id, err)
		}
		err = tx.Evidence().Record(workorders.Evidence{
			ID: "forge:" + id + ":CI result", WorkOrder: id, Lane: held[0].Lane, Fence: row.Fence, ArtifactID: held[0].ArtifactID,
			Name: "CI result", Status: workorders.CheckPassed, Source: workorders.SourceForge,
		})
		if err != nil {
			return err
		}
		if err := tx.Forge().SavePullRequest(ledger.WorkPullRequest{PullRequest: pr, WorkOrder: id, Worker: "worker-a"}); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: "test.pull_request", Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}, Subject: ledger.Subject{Kind: workorders.SubjectKind, ID: id}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// work acceptance names the pull request the work lands through and lists its checks beside the work's own, as the
// web tab does: a required check is marked so, and a forge check on record the pull request lists shows once.
func TestWorkAcceptanceNamesThePullRequestAndItsChecks(t *testing.T) {
	s := newSession(t)
	s.completed("wo", workorders.CheckResult{Name: "unit", Passed: true, Criterion: "c1"})
	pr := ledger.PullRequest{
		Number: 7, URL: "https://forge.example/acme/app/pull/7", State: ledger.PullRequestOpen, Base: "main", Branch: "lane/wo",
		HeadCommit: "abc123", Checks: []ledger.PullRequestCheck{
			{Name: "CI result", State: ledger.CheckPassed, Required: true},
			{Name: "lint", State: ledger.CheckFailed},
		},
	}
	pullRequestOn(t, s, "wo", pr)
	text := s.must("work", "acceptance", "wo")
	for _, want := range []string{
		"pull request\t#7\topen\tlane/wo into main\thead abc123\thttps://forge.example/acme/app/pull/7\n",
		"check\tunit\tpassed\tlane\tc1\n",
		"check\tCI result\tpassed\tpull request #7, required\n",
		"check\tlint\tfailed\tpull request #7\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("acceptance lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "check\tCI result\t") != 1 {
		t.Errorf("a forge check the pull request lists shows more than once:\n%s", text)
	}
	var view workorders.AcceptanceView
	if err := json.Unmarshal([]byte(s.must("work", "acceptance", "wo", "--json")), &view); err != nil {
		t.Fatal(err)
	}
	if view.PullRequest == nil || view.PullRequest.Number != 7 || len(view.PullRequest.Checks) != 2 || !view.PullRequest.Checks[0].Required {
		t.Errorf("--json reads the pull request %+v", view.PullRequest)
	}

	pr.State, pr.MergeCommit = ledger.PullRequestMerged, "0123456789abcdef0123"
	pullRequestOn(t, s, "wo", pr)
	if text := s.must("work", "acceptance", "wo"); !strings.Contains(text, "pull request\t#7\tmerged\tlane/wo into main\thead abc123\tmerged as 0123456789ab\thttps://forge.example/acme/app/pull/7\n") {
		t.Errorf("a merged pull request does not name its merge commit:\n%s", text)
	}
}

// A planning branch's approval is a member's plan approval, and work show names the plan version it approved.
func TestWorkShowNamesThePlanVersionThatApprovedAMerge(t *testing.T) {
	detail := api.WorkDetail{WorkOrder: workorders.WorkOrder{ID: "plan-1", Kind: workorders.KindPlan, State: workorders.Done}, Integrations: []ledger.IntegrationRecord{{
		ID: "i1", WorkOrder: "plan-1", ApprovedCommit: "1111aaaa2222bbbb", MergeAuthority: ledger.ReviewApproves, ApprovedByKind: ledger.ApprovedByMember,
		ApprovedByID: "dana", ApprovedAt: 1, MergedCommit: "3333cccc4444dddd", MergedAt: 2, Plan: "01PLAN", PlanVersion: 2,
	}}}
	text := cli.WorkDetailText(detail)
	if want := "approved\t1111aaaa2222\tby member dana\tat review_approves\tmerged as 3333cccc4444\tplan 01PLAN version 2\n"; !strings.Contains(text, want) {
		t.Errorf("show leaves out %q:\n%s", want, text)
	}
}
