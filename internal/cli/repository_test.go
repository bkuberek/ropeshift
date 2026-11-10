package cli_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

const aRepositoryID = "3f2a1b0c4d5e6f708192a3b4c5d6e7f8091a2b3c"

func (s *session) detect(id, branch string) {
	s.t.Helper()
	_, err := control.NewRepositories(s.daemon.Store).Detect(s.t.Context(), apitest.Scope,
		control.DetectedRepository{ID: id, Remote: "https://example.invalid/acme/app.git", DefaultBranch: branch}, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
	if err != nil {
		s.t.Fatal(err)
	}
}

func TestRepositoryListSaysWhatEachOneStartsFrom(t *testing.T) {
	s := newSession(t)
	s.detect(aRepositoryID, "main")
	text := s.must("repository", "list")
	for _, want := range []string{aRepositoryID[:12], "base main", "merge merge", "start merged", "integration off"} {
		if !strings.Contains(text, want) {
			t.Errorf("list lacks %q:\n%s", want, text)
		}
	}
	var list api.RepositoryList
	if err := json.Unmarshal([]byte(s.must("repository", "list", "--json")), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Repositories) != 1 || list.Repositories[0].ID != aRepositoryID || list.Repositories[0].Base != "main" {
		t.Errorf("json %+v", list)
	}
}

func TestRepositoryShowNamesEverySetting(t *testing.T) {
	s := newSession(t)
	s.detect(aRepositoryID, "main")
	text := s.must("repository", "show", aRepositoryID[:8])
	for _, want := range []string{
		"repository\t" + aRepositoryID, "remote\thttps://example.invalid/acme/app.git", "default branch\tmain", "base\tmain\t(the default branch)",
		"forge binding\t-", "merge method\tmerge", "integration\toff", "start from\tmerged", "merge authority\tperson_merges",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("show lacks %q:\n%s", want, text)
		}
	}
	if code, _, _ := s.run("repository", "show", "0000000"); code != cli.ExitNotFound {
		t.Errorf("a repository nothing read: exit %d", code)
	}
}

func TestRepositorySetChangesWhatAMemberNames(t *testing.T) {
	s := newSession(t)
	s.detect(aRepositoryID, "main")
	text := s.must("repository", "set", aRepositoryID[:8], "--base", "release/1.2", "--merge-method", "squash", "--integration", "on")
	for _, want := range []string{"base\trelease/1.2", "merge method\tsquash", "integration\ton", "start from\tmerged"} {
		if !strings.Contains(text, want) {
			t.Errorf("set printed no %q:\n%s", want, text)
		}
	}
	if text := s.must("repository", "set", aRepositoryID, "--base", "", "--integration", "off"); !strings.Contains(text, "base\tmain\t(the default branch)") || !strings.Contains(text, "integration\toff") {
		t.Errorf("clearing the base:\n%s", text)
	}
}

func TestRepositorySetChangesTheMergeAuthority(t *testing.T) {
	s := newSession(t)
	s.detect(aRepositoryID, "main")
	if text := s.must("repository", "list"); !strings.Contains(text, "authority person_merges") {
		t.Errorf("list does not say the level:\n%s", text)
	}
	if text := s.must("repository", "set", aRepositoryID[:8], "--merge-authority", "review_approves"); !strings.Contains(text, "merge authority\treview_approves") {
		t.Errorf("set printed:\n%s", text)
	}
	if code, _, stderr := s.run("repository", "set", aRepositoryID, "--merge-authority", "whenever"); code == cli.ExitOK || stderr == "" {
		t.Errorf("a level nobody offers: exit %d, stderr %q", code, stderr)
	}
	if got := s.must("repository", "show", aRepositoryID); !strings.Contains(got, "merge authority\treview_approves") {
		t.Errorf("a refused level changed the record:\n%s", got)
	}
}

func TestRepositorySetRefusesWhatCannotHold(t *testing.T) {
	s := newSession(t)
	s.detect(aRepositoryID, "main")
	s.must("repository", "set", aRepositoryID, "--merge-method", "rebase")
	for name, args := range map[string][]string{
		"accepted work on a rebase repository": {"--start-from", "accepted"},
		"a method nobody offers":               {"--merge-method", "fast-forward"},
		"a start nobody offers":                {"--start-from", "whenever"},
		"a switch that is neither":             {"--integration", "maybe"},
		"a base git would read as an option":   {"--base", "-rf"},
	} {
		code, _, stderr := s.run(append([]string{"repository", "set", aRepositoryID}, args...)...)
		if code == cli.ExitOK || stderr == "" {
			t.Errorf("%s: exit %d, stderr %q", name, code, stderr)
		}
	}
	if code, _, stderr := s.run("repository", "set", aRepositoryID); code != cli.ExitUsage || !strings.Contains(stderr, "name a setting") {
		t.Errorf("a set that names nothing: exit %d, %q", code, stderr)
	}
	if code, _, _ := s.run("repository", "set"); code != cli.ExitUsage {
		t.Errorf("a set with no repository: exit %d", code)
	}
	if got := s.must("repository", "show", aRepositoryID); !strings.Contains(got, "merge method\trebase") || !strings.Contains(got, "start from\tmerged") {
		t.Errorf("a refused change left:\n%s", got)
	}
}

func TestWorkShowSaysWhyReadyWorkIsNotClaimed(t *testing.T) {
	held := workorders.WorkOrder{
		ID: "wo-1", State: workorders.Ready, Title: "Build on it", Base: "release/1.2",
		StartHeld: &workorders.StartHold{Reason: workorders.HoldWaitingForMerge, Dependency: "wo-0", Detail: "its work, 3f2a1b0c, is accepted and not yet in origin/main"},
	}
	text := cli.WorkOrderText(held)
	for _, want := range []string{"base\trelease/1.2", "waiting\twaiting_for_merge\two-0\tits work, 3f2a1b0c, is accepted and not yet in origin/main"} {
		if !strings.Contains(text, want) {
			t.Errorf("work show lacks %q:\n%s", want, text)
		}
	}
	plain := cli.WorkOrderText(workorders.WorkOrder{ID: "wo-2", State: workorders.Ready, Title: "Plain"})
	if strings.Contains(plain, "waiting") || strings.Contains(plain, "base\t") {
		t.Errorf("work that waits for nothing says so:\n%s", plain)
	}
}

func TestLaneShowSaysWhereTheLaneBegan(t *testing.T) {
	lane := ledger.LaneView{ID: "lane-1", BaseRef: "origin/main", BaseCommit: "3f2a1b0c4d5e6f708192a3b4c5d6e7f8091a2b3c"}
	if text := cli.LaneDetail(lane); !strings.Contains(text, "base\torigin/main\t3f2a1b0c4d5e6f708192a3b4c5d6e7f8091a2b3c\n") {
		t.Errorf("lane show lacks its base:\n%s", text)
	}
	if text := cli.LaneDetail(ledger.LaneView{ID: "lane-2"}); strings.Contains(text, "base") {
		t.Errorf("a lane that began where its checkout was says it began somewhere:\n%s", text)
	}
}

// calibrate leaves the records of a member's decisions after a review: two the member agreed with, and one
// approval the member sent back.
func (s *session) calibrate(repository string) {
	s.t.Helper()
	records := []workorders.Calibration{
		{Outcome: workorders.ReviewPass, Decision: workorders.DecisionAccept, Agreement: workorders.AgreementAgree},
		{Outcome: workorders.ReviewFail, Decision: workorders.DecisionRework, Agreement: workorders.AgreementAgree},
		{Outcome: workorders.ReviewPass, Decision: workorders.DecisionRework, Agreement: workorders.AgreementDisagree},
	}
	err := s.daemon.Store.Write(s.t.Context(), apitest.Scope, "calibration.test", func(tx ports.WriteTx) error {
		for i, c := range records {
			c.ID, c.WorkOrder, c.Review, c.Commit = fmt.Sprintf("cal-%d", i), fmt.Sprintf("wo-%d", i), fmt.Sprintf("review-%d", i), "abc123"
			c.Repository, c.Authority, c.Source, c.Member = repository, ledger.PersonMerges, workorders.SourceDecision, "member-1"
			c.Verdicts = []workorders.SeenVerdict{{Criterion: "c1", Verdict: workorders.VerdictKind(c.Outcome)}}
			if _, err := tx.Evidence().RecordCalibration(c); err != nil {
				return err
			}
		}
		_, err := tx.Append(ledger.EventDraft{
			Type: workorders.EventEvidenceRecorded, Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}, Subject: ledger.Subject{Kind: workorders.SubjectKind, ID: "wo-0"},
		})
		return err
	})
	if err != nil {
		s.t.Fatal(err)
	}
}

func TestRepositoryShowSaysHowOftenYouAgreedWithTheReview(t *testing.T) {
	s := newSession(t)
	s.detect(aRepositoryID, "main")
	text := s.must("repository", "show", aRepositoryID[:8])
	for _, want := range []string{"merge authority\tperson_merges", "review agreement, last 30 days\tno decisions after a review yet", "review agreement, all time\tno decisions after a review yet"} {
		if !strings.Contains(text, want) {
			t.Errorf("show lacks %q before any decision:\n%s", want, text)
		}
	}
	s.calibrate(aRepositoryID)
	text = s.must("repository", "show", aRepositoryID[:8])
	for _, want := range []string{
		"merge authority\tperson_merges",
		"review agreement, last 30 days\t3 decisions after a review; you agreed with 66.7% (2 of 3); approvals you overturned 1 (later defects 0); failures you overrode 0; the review could not judge 0",
		"review agreement, all time\t3 decisions after a review; you agreed with 66.7% (2 of 3)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("show lacks %q:\n%s", want, text)
		}
	}
	var view api.RepositoryView
	if err := json.Unmarshal([]byte(s.must("repository", "show", aRepositoryID, "--json")), &view); err != nil {
		t.Fatal(err)
	}
	if view.ReviewAgreement.AllTime.Decisions != 3 || view.ReviewAgreement.AllTime.Agreed != 2 {
		t.Errorf("json %+v", view.ReviewAgreement)
	}
	// Seeing the record is not choosing the level.
	if got := s.must("repository", "show", aRepositoryID); !strings.Contains(got, "merge authority\tperson_merges") {
		t.Errorf("the level moved:\n%s", got)
	}
}

func TestMeasuresPrintsReviewAgreementPerRepository(t *testing.T) {
	s := newSession(t)
	s.detect(aRepositoryID, "main")
	s.calibrate(aRepositoryID)
	text := s.must("measures")
	for _, want := range []string{
		"review agreement, all time\t3 decisions after a review; you agreed with 66.7% (2 of 3)",
		"review agreement, " + aRepositoryID[:12] + " (person_merges), all time\t3 decisions after a review",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("measures lack %q:\n%s", want, text)
		}
	}
}
