package cli_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
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
	for _, want := range []string{"app\t" + aRepositoryID[:12], "base main", "merge merge", "start merged", "review branch"} {
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
		"repository\t" + aRepositoryID, "name\tapp", "remote\thttps://example.invalid/acme/app.git", "default branch\tmain", "base\tmain\t(the default branch)",
		"forge binding\t-", "merge method\tmerge", "review target\tbranch", "start from\tmerged", "merge authority\tperson_merges",
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
	text := s.must("repository", "set", aRepositoryID[:8], "--base", "release/1.2", "--merge-method", "squash", "--review-target", "pull_request")
	for _, want := range []string{"base\trelease/1.2", "merge method\tsquash", "review target\tpull_request", "start from\tmerged"} {
		if !strings.Contains(text, want) {
			t.Errorf("set printed no %q:\n%s", want, text)
		}
	}
	if text := s.must("repository", "set", aRepositoryID, "--base", "", "--review-target", "branch"); !strings.Contains(text, "base\tmain\t(the default branch)") || !strings.Contains(text, "review target\tbranch") {
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
		"a target nobody offers":               {"--review-target", "merge_queue"},
		"the switch the target replaced":       {"--integration", "on"},
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

// A repository is named in its project, and every repository command takes the name as well as the
// identity (ADR-0022).
func TestRepositoryCommandsTakeAName(t *testing.T) {
	s := newSession(t)
	s.detect(aRepositoryID, "main")
	if text := s.must("repository", "show", "app"); !strings.Contains(text, "repository\t"+aRepositoryID) {
		t.Errorf("show by name:\n%s", text)
	}
	if text := s.must("repository", "set", "app", "--name", "payments"); !strings.Contains(text, "name\tpayments") {
		t.Errorf("a rename printed:\n%s", text)
	}
	if code, _, _ := s.run("repository", "show", "app"); code != cli.ExitNotFound {
		t.Errorf("the old name: exit %d", code)
	}
	if text := s.must("repository", "set", "payments", "--merge-method", "squash"); !strings.Contains(text, "merge method\tsquash") {
		t.Errorf("a change by the new name:\n%s", text)
	}
	const other = "0a1b2c3d4e5f60718293a4b5c6d7e8f901a2b3c4"
	s.detect(other, "main")
	if code, _, stderr := s.run("repository", "set", other, "--name", "payments"); code != cli.ExitConflict || !strings.Contains(stderr, "payments") {
		t.Errorf("a name another repository holds: exit %d, %q", code, stderr)
	}
	if code, _, _ := s.run("repository", "set", "payments", "--name", "a b"); code != cli.ExitUsage {
		t.Errorf("a name no command can type: exit %d", code)
	}
}

// TestRepositoryAddRegistersClonesAndSaysWhatItRefused is ADR-0022's bulk registration on the command line:
// a dry run says what would happen and records nothing, a run registers each clone with its name, and a
// path that holds no clone is refused with the reason and exit 4 while the others are still registered.
func TestRepositoryAddRegistersClonesAndSaysWhatItRefused(t *testing.T) {
	machine := fake.NewCheckouts()
	machine.Place(ports.Clone{Path: "/work/api", ID: aRepositoryID, Remote: "https://example.invalid/acme/api.git", DefaultBranch: "main"})
	machine.Place(ports.Clone{Path: "/work/group/web", ID: "0a1b2c3d4e5f60718293a4b5c6d7e8f901a2b3c4", DefaultBranch: "main"})
	s := &session{t: t, daemon: apitest.StartWith(t, apitest.Options{Checkouts: machine})}

	code, stdout, _ := s.run("repository", "add", "/work", "--dry-run")
	if code != cli.ExitOK || !strings.Contains(stdout, "would register\tapi\t"+aRepositoryID[:12]+"\t/work/api") || strings.Contains(stdout, "web") {
		t.Errorf("a dry run one level down: exit %d\n%s", code, stdout)
	}
	if list := s.must("repository", "list"); list != "" {
		t.Errorf("a dry run registered:\n%s", list)
	}
	code, stdout, _ = s.run("repository", "add", "/work", "--depth", "2", "/nowhere")
	for _, want := range []string{"registered\tapi\t", "registered\tweb\t", "refused\t/nowhere\t"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the run lacks %q:\n%s", want, stdout)
		}
	}
	if code != cli.ExitConflict {
		t.Errorf("a run with a refusal: exit %d", code)
	}
	if text := s.must("repository", "show", "web"); !strings.Contains(text, "review target\tbranch") || !strings.Contains(text, "merge authority\tperson_merges") {
		t.Errorf("a registered repository:\n%s", text)
	}
	if code, stdout, _ := s.run("repository", "add", "/work/api"); code != cli.ExitOK || !strings.Contains(stdout, "already registered\tapi\t") {
		t.Errorf("a repository the project holds: exit %d\n%s", code, stdout)
	}
	var report api.RepositoryAddReport
	if err := json.Unmarshal([]byte(s.must("repository", "add", "/work/api", "--json")), &report); err != nil || len(report.Repositories) != 1 {
		t.Errorf("json %+v: %v", report, err)
	}
	for name, args := range map[string][]string{
		"no path":           {"repository", "add"},
		"a depth below one": {"repository", "add", "/work", "--depth", "0"},
	} {
		if code, _, _ := s.run(args...); code != cli.ExitUsage {
			t.Errorf("%s: exit %d", name, code)
		}
	}
}

// FR-REPO-12: `repository set` takes several repositories and applies the same change to each, one event
// per repository. A name the project does not hold changes none of them.
func TestRepositorySetChangesSeveralRepositories(t *testing.T) {
	s := newSession(t)
	const other = "0a1b2c3d4e5f60718293a4b5c6d7e8f901a2b3c4"
	s.detect(aRepositoryID, "main")
	s.detect(other, "main")
	s.must("repository", "set", other, "--name", "web")
	if code, _, _ := s.run("repository", "set", "app", "nowhere", "--merge-method", "squash"); code != cli.ExitNotFound {
		t.Errorf("a name the project does not hold: exit %d", code)
	}
	if text := s.must("repository", "show", "app"); !strings.Contains(text, "merge method\tmerge") {
		t.Errorf("a refused set changed a repository:\n%s", text)
	}
	text := s.must("repository", "set", "app", "web", "--review-target", "pull_request")
	if strings.Count(text, "review target\tpull_request") != 2 || !strings.Contains(text, "name\tapp") || !strings.Contains(text, "name\tweb") {
		t.Errorf("a set of two printed:\n%s", text)
	}
	var list api.RepositoryList
	if err := json.Unmarshal([]byte(s.must("repository", "set", "app", "web", "--merge-method", "squash", "--json")), &list); err != nil || len(list.Repositories) != 2 {
		t.Errorf("json %+v: %v", list, err)
	}
	for _, id := range []string{aRepositoryID, other} {
		var changes int
		err := s.daemon.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
			events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectRepository, ID: id})
			for _, event := range events {
				if event.Type == ledger.EventRepositoryChanged {
					changes++
				}
			}
			return err
		})
		if err != nil || (id == aRepositoryID && changes != 2) || (id == other && changes != 3) {
			t.Errorf("%s changed %d times: %v", id[:7], changes, err)
		}
	}
	if code, _, _ := s.run("repository", "set", "app", "web", "--name", "one"); code != cli.ExitUsage {
		t.Errorf("one name for two repositories: exit %d", code)
	}
}

// ADR-0021: `repository remove` takes a repository out of its project once no open work targets it, and
// the record stays as history.
func TestRepositoryRemoveKeepsTheRecordAsHistory(t *testing.T) {
	s := newSession(t)
	s.detect(aRepositoryID, "main")
	s.must("work", "create", "--id", "wo-1", "--title", "Change the app", "--criterion", "It changes", "--repository", aRepositoryID)
	if code, _, stderr := s.run("repository", "remove", "app"); code != cli.ExitConflict || !strings.Contains(stderr, "wo-1") {
		t.Errorf("open work targets it: exit %d, %q", code, stderr)
	}
	s.must("work", "cancel", "wo-1", "--reason", "not needed")
	if text := s.must("repository", "remove", "app"); !strings.Contains(text, "state\tremoved") {
		t.Errorf("remove printed:\n%s", text)
	}
	if text := s.must("repository", "list"); !strings.Contains(text, "app (removed)\t") {
		t.Errorf("list:\n%s", text)
	}
	if code, _, _ := s.run("repository", "remove", "app"); code != cli.ExitConflict {
		t.Errorf("removing it again: exit %d", code)
	}
	if code, _, _ := s.run("repository", "remove"); code != cli.ExitUsage {
		t.Errorf("no repository: exit %d", code)
	}
}
