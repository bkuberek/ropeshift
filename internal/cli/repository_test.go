package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// FR-PROF-3: `repository add` registers a clone whose worktree root would lie inside it, and says so on
// the line it prints, so the person fixes the root before a lane fails.
func TestRepositoryAddSaysWhenTheWorktreeRootWouldLieInsideTheClone(t *testing.T) {
	machine := fake.NewCheckouts()
	machine.Place(ports.Clone{Path: "/work/api", ID: aRepositoryID, DefaultBranch: "main"})
	profiles := fake.NewProfileSource()
	profiles.PutUser("[worktree]\nroot = \"lanes\"\n")
	s := &session{t: t, daemon: apitest.StartWith(t, apitest.Options{Checkouts: machine, Profiles: profiles})}

	code, stdout, _ := s.run("repository", "add", "/work/api")
	for _, want := range []string{"registered\tapi\t", "worktree root /work/api/lanes", "set by the user's profile layer", "worktree_inside_clone", "or remove it to use the built-in root"} {
		if code != cli.ExitOK || !strings.Contains(stdout, want) {
			t.Errorf("the registration lacks %q (exit %d):\n%s", want, code, stdout)
		}
	}
	if list := s.must("repository", "list"); !strings.Contains(list, "api") {
		t.Errorf("the clone was not registered:\n%s", list)
	}
	profiles.PutUser("")
	if _, stdout, _ := s.run("repository", "add", "/work/api"); strings.Contains(stdout, "worktree root") {
		t.Errorf("a root outside the clone is reported:\n%s", stdout)
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

// ADR-0022: a member names the project's planning repository, which a project of one repository needs no
// name for, and a project has one.
func TestRepositorySetNamesThePlanningRepository(t *testing.T) {
	s := newSession(t)
	other := "9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d"
	s.detect(aRepositoryID, "main")
	if text := s.must("repository", "show", aRepositoryID); !strings.Contains(text, "planning repository\tyes\t(the project's only repository)") {
		t.Errorf("a project's only repository:\n%s", text)
	}
	s.detect(other, "main")
	if text := s.must("repository", "show", aRepositoryID); !strings.Contains(text, "planning repository\tno") {
		t.Errorf("one of two repositories with none named:\n%s", text)
	}
	if code, _, stderr := s.run("repository", "set", aRepositoryID, other, "--planning"); code != cli.ExitUsage || !strings.Contains(stderr, "one planning repository") {
		t.Errorf("naming two planning repositories: exit %d, %q", code, stderr)
	}
	if text := s.must("repository", "set", other[:8], "--planning"); !strings.Contains(text, "planning repository\tyes\n") {
		t.Errorf("set printed:\n%s", text)
	}
	if text := s.must("repository", "list"); !strings.Contains(text, " (planning)\t"+other[:12]) || strings.Contains(text, " (planning)\t"+aRepositoryID[:12]) {
		t.Errorf("list does not mark the planning repository:\n%s", text)
	}
	s.must("repository", "set", aRepositoryID, "--planning")
	if text := s.must("repository", "show", other); !strings.Contains(text, "planning repository\tno") {
		t.Errorf("naming another left the first:\n%s", text)
	}
	if text := s.must("repository", "set", aRepositoryID, "--planning=false"); !strings.Contains(text, "planning repository\tno") {
		t.Errorf("--planning=false printed:\n%s", text)
	}
}

// TestRepositorySetWarnsWhenNoWorkerHasGH is the choice ADR-0020 leaves at the setting: the control plane
// cannot see a worker's gh when a member changes the review target, so the change stands with a warning,
// and repository show says what each worker found. A remote on no forge needs a binding named.
func TestRepositorySetWarnsWhenNoWorkerHasGH(t *testing.T) {
	s := newSession(t)
	s.detect(aRepositoryID, "main")
	code, stdout, stderr := s.run("repository", "set", aRepositoryID[:8], "--review-target", "pull_request")
	if code != cli.ExitOK || !strings.Contains(stderr, "warning: no worker has reported a working gh sign-in for example.invalid/acme/app") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{"forge binding\texample.invalid/acme/app", "forge\t-\tno worker has reported gh for this repository yet"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("set printed no %q:\n%s", want, stdout)
		}
	}
	if text := s.must("repository", "set", aRepositoryID[:8], "--forge-binding", "github.com/acme/app"); !strings.Contains(text, "forge binding\tgithub.com/acme/app") {
		t.Errorf("a named binding:\n%s", text)
	}
	if code, _, stderr := s.run("repository", "set", aRepositoryID[:8], "--forge-binding", "acme/app"); code == cli.ExitOK || !strings.Contains(stderr, "HOST/OWNER/REPO") {
		t.Errorf("a binding with no host: exit %d, stderr %q", code, stderr)
	}
	if _, _, stderr := s.run("repository", "set", aRepositoryID[:8], "--review-target", "branch"); stderr != "" {
		t.Errorf("a branch review warned %q", stderr)
	}
}

// platformManifest is a manifest of the planning repository and the billing and ghost repositories beside
// its clone, under the plan named.
func platformManifest(plan string) string {
	out := "+++\nkind = \"repositories\"\nid = \"platform\"\nplan = \"" + plan + "\"\n"
	for _, entry := range [][2]string{{"plan", "."}, {"billing", "../billing"}, {"ghost", "../ghost"}} {
		out += "\n[[repository]]\nname = \"" + entry[0] + "\"\npath = \"" + entry[1] + "\"\n"
	}
	return out + "+++\n\nThe platform's repositories.\n"
}

// platformMachine is a worker's machine with the planning repository's clone at /work/plan, registered, and a
// clone of billing beside it under another directory name. Nothing is cloned at /work/ghost.
func platformMachine(t *testing.T) *session {
	t.Helper()
	machine := fake.NewCheckouts()
	machine.Place(ports.Clone{Path: "/work/plan", ID: aRepositoryID, DefaultBranch: "main"})
	machine.Place(ports.Clone{Path: "/work/billing", ID: "0a1b2c3d4e5f60718293a4b5c6d7e8f901a2b3c4", DefaultBranch: "main", Directory: "billing-clone"})
	s := &session{t: t, daemon: apitest.StartWith(t, apitest.Options{Checkouts: machine})}
	s.must("repository", "add", "/work/plan")
	return s
}

// ADR-0022: a member registers what a manifest lists by hand, from the clones beside the planning
// repository's, each under the manifest's name. One with no clone is refused with why, and exits 4.
func TestRepositoryAddRegistersWhatAManifestLists(t *testing.T) {
	s := platformMachine(t)
	file := filepath.Join(t.TempDir(), "repositories.md")
	if err := os.WriteFile(file, []byte(platformManifest("platform-v1")), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := s.run("repository", "add", "--manifest", file, "--dry-run")
	if code != cli.ExitConflict || !strings.Contains(stdout, "would register\tbilling\t") || !strings.Contains(stdout, "already registered\tplan\t") {
		t.Errorf("a dry run: exit %d\n%s", code, stdout)
	}
	code, stdout, _ = s.run("repository", "add", "--manifest", file)
	for _, want := range []string{"registered\tbilling\t../billing\t/work/billing\t-\n", "refused\tghost\t../ghost\t/work/ghost\t-\tghost is not registered: no git clone is at /work/ghost"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the run lacks %q:\n%s", want, stdout)
		}
	}
	if code != cli.ExitConflict {
		t.Errorf("a manifest with a repository it could not register: exit %d", code)
	}
	if text := s.must("repository", "show", "billing"); !strings.Contains(text, "review target\tbranch") || !strings.Contains(text, "merge authority\tperson_merges") {
		t.Errorf("billing:\n%s", text)
	}
	if code, _, _ := s.run("repository", "add", "/work/plan", "--manifest", file); code != cli.ExitUsage {
		t.Errorf("paths and a manifest: exit %d", code)
	}
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("+++\nkind = \"spec\"\nid = \"x\"\ntitle = \"X\"\n+++\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := s.run("repository", "add", "--manifest", notes); code != cli.ExitUsage || !strings.Contains(stderr, "repositories") {
		t.Errorf("a file that is no manifest: exit %d: %s", code, stderr)
	}
}

// platformPlan is a platform machine whose plan lists the platform manifest, read once, with the plan's ID.
func platformPlan(t *testing.T) (*session, string) {
	t.Helper()
	s := platformMachine(t)
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte("+++\nkind = \"plan\"\nid = \"platform-v1\"\ntitle = \"The platform\"\ndocuments = [\"repositories:platform\"]\n+++\n\nHow.\n"))
	source.Write("repositories.md", []byte(platformManifest("platform-v1")))
	s.poll(source)
	plans, err := s.daemon.Client.Plans(t.Context())
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans %+v: %v", plans, err)
	}
	return s, plans[0].ID
}

// FR-REPO-16: an entry that asks for its repository to be created, which no work order of the version names in
// `repo`, is planned before and after approval, and approving registers nothing for it.
func TestPlanShowSaysAnEntryNoWorkOrderTargetsIsPlanned(t *testing.T) {
	s := platformMachine(t)
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte("+++\nkind = \"plan\"\nid = \"platform-v1\"\ntitle = \"The platform\"\ndocuments = [\"repositories:platform\"]\n+++\n\nHow.\n"))
	source.Write("repositories.md", []byte(strings.Replace(platformManifest("platform-v1"), "+++\n\nThe platform's", "\n[[repository]]\nname = \"web\"\npath = \"../web\"\ncreate = \"github:acme\"\n+++\n\nThe platform's", 1)))
	s.poll(source)
	plans, err := s.daemon.Client.Plans(t.Context())
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans %+v: %v", plans, err)
	}
	planned := "planned\tweb\t../web\t-\t-\tno work order of this version targets web\n"
	preview := strings.TrimSuffix(planned, "\n") + ", so approving creates nothing; the approval of a version that gives it work creates it\n"
	if shown := s.must("plan", "show", plans[0].ID); !strings.Contains(shown, "  repositories approving would register, read now on the daemon's worker's machine:\n") || !strings.Contains(shown, "    "+preview) {
		t.Errorf("plan show lacks the planned line %q:\n%s", preview, shown)
	}
	if text := s.must("plan", "approve", plans[0].ID); !strings.Contains(text, "\n"+planned) {
		t.Errorf("approve lacks the planned line %q:\n%s", planned, text)
	}
	if shown := s.must("plan", "show", plans[0].ID); !strings.Contains(shown, "  repositories this approval registered:\n") || !strings.Contains(shown, "    "+planned) {
		t.Errorf("plan show after approval lacks the planned line %q:\n%s", planned, shown)
	}
	if list := s.must("repository", "list"); strings.Contains(list, "web") {
		t.Errorf("approving registered web, which no work order names:\n%s", list)
	}
}

// With --json the command prints the one document of the decision and nothing before it, so a script reads it
// whole; the preview is for a terminal.
func TestPlanApproveJSONPrintsOneDocument(t *testing.T) {
	s, plan := platformPlan(t)
	var decided api.ApprovalDecided
	if err := json.Unmarshal([]byte(s.must("plan", "approve", plan, "--json")), &decided); err != nil || len(decided.Repositories) != 3 {
		t.Fatalf("approve --json printed a decision with %d repositories: %v", len(decided.Repositories), err)
	}
}

// ADR-0022: approving a plan registers what its manifest adds, as the approving member, and the command
// says what it registered and what it could not.
func TestPlanApproveRegistersWhatItsManifestAdds(t *testing.T) {
	s, plan := platformPlan(t)
	// Before approving, the member reads each repository the manifest lists, where its path leads, and
	// whether approving registers it.
	shown := s.must("plan", "show", plan)
	for _, want := range []string{
		"  repositories approving would register, read now on the daemon's worker's machine:\n",
		"    would register\tbilling\t../billing\t/work/billing\t-\n",
		"    already registered\tplan\t.\t/work/plan\t-\n",
		"    refused\tghost\t../ghost\t/work/ghost\t-\tghost is not registered: no git clone is at /work/ghost\n",
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("plan show lacks %q:\n%s", want, shown)
		}
	}
	if list := s.must("repository", "list"); strings.Contains(list, "billing") {
		t.Errorf("reading the plan registered billing:\n%s", list)
	}
	text := s.must("plan", "approve", plan)
	for _, want := range []string{"approved The platform version 1", "registered\tbilling\t../billing\t/work/billing\t-\n", "refused\tghost\t../ghost\t/work/ghost\t-\tghost is not registered"} {
		if !strings.Contains(text, want) {
			t.Errorf("approve lacks %q:\n%s", want, text)
		}
	}
	// The command prints what it would register before it approves, as plan show does, so the member's
	// terminal shows the entries the approval consented to ahead of its result.
	approved := strings.Index(text, "approved The platform version 1")
	for _, want := range []string{
		"  repositories approving would register, read now on the daemon's worker's machine:\n",
		"    would register\tbilling\t../billing\t/work/billing\t-\n",
		"    already registered\tplan\t.\t/work/plan\t-\n",
		"    refused\tghost\t../ghost\t/work/ghost\t-\tghost is not registered: no git clone is at /work/ghost\n",
	} {
		if at := strings.Index(text, want); at < 0 || at > approved {
			t.Errorf("approve printed %q at %d, want it before the result at %d:\n%s", want, at, approved, text)
		}
	}
	// Afterwards the approval says what it registered, however the member gave the word.
	shown = s.must("plan", "show", plan)
	for _, want := range []string{"  repositories this approval registered:\n", "    registered\tbilling\t../billing\t/work/billing\t-\n", "    refused\tghost\t"} {
		if !strings.Contains(shown, want) {
			t.Errorf("plan show after approval lacks %q:\n%s", want, shown)
		}
	}
	if list := s.must("repository", "list"); !strings.Contains(list, "billing") || strings.Contains(list, "ghost") {
		t.Errorf("the repositories after approval:\n%s", list)
	}
}

// repository show prints, beside the forge line of each worker that reported the repository's checkout as a workspace
// that does not ignore its folder for lane worktrees, the workspace and the line to add. A worker whose report names
// none gets no such line.
func TestRepositoryShowSaysWhichWorkspaceDoesNotIgnoreItsWorktrees(t *testing.T) {
	s := newSession(t)
	s.detect(aRepositoryID, "main")
	err := s.daemon.Store.Write(t.Context(), apitest.Scope, "test.forge_health", func(tx ports.WriteTx) error {
		for _, health := range []ledger.ForgeHealth{
			{Worker: "worker-a", Repository: aRepositoryID, State: ledger.ForgeHealthy, UnignoredWorkspace: "/work/ws", CheckedAt: 1},
			{Worker: "worker-b", Repository: aRepositoryID, State: ledger.ForgeHealthy, CheckedAt: 1},
		} {
			if _, err := tx.Forge().SaveHealth(health); err != nil {
				return err
			}
			_, err := tx.Append(ledger.EventDraft{
				Type: ledger.EventForgeHealth, Actor: ledger.Actor{Kind: ledger.ActorWorker, ID: health.Worker}, Subject: ledger.Subject{Kind: ledger.SubjectRepository, ID: aRepositoryID},
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	text := s.must("repository", "show", aRepositoryID)
	if want := "workspace\tworker-a\t/work/ws\t" + ledger.UnignoredWorkspaceNote + "\n"; !strings.Contains(text, want) {
		t.Errorf("repository show printed no %q:\n%s", want, text)
	}
	if strings.Count(text, ledger.UnignoredWorkspaceNote) != 1 {
		t.Errorf("repository show names the workspace for a worker whose report names none:\n%s", text)
	}
}

// A repository stored before names were checked can hold a base branch, a default branch, or a remote with a hidden
// character in it. Each prints with that character dropped, so what a person reads is what they would type.
func TestRepositoryShowAndListDropHiddenCharactersFromStoredNames(t *testing.T) {
	s := newSession(t)
	err := s.daemon.Store.Write(s.t.Context(), apitest.Scope, "test.register", func(tx ports.WriteTx) error {
		record := ledger.NewRepository(aRepositoryID)
		record.BaseBranch = "rel\u202eease"
		record.DefaultBranch = "ma\u200bin"
		record.Remote = "https://example.invalid/ac\u200bme/app.git"
		if _, err := tx.Repositories().Insert(record); err != nil {
			return err
		}
		if err := tx.Repositories().Settings(record); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventRepositoryRegistered, Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "test"},
			Subject: ledger.Subject{Kind: ledger.SubjectRepository, ID: aRepositoryID}, Data: []byte(`{}`),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"show": s.must("repository", "show", aRepositoryID[:8]),
		"list": s.must("repository", "list"),
	} {
		if strings.ContainsAny(text, "\u202e\u200b") {
			t.Errorf("%s prints a hidden character:\n%q", name, text)
		}
		if !strings.Contains(text, "release") {
			t.Errorf("%s lacks the base branch with the character dropped:\n%q", name, text)
		}
	}
}
