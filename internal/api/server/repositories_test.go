package server_test

import (
	"fmt"
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

const aRepository = "3f2a1b0c4d5e6f708192a3b4c5d6e7f8091a2b3c"

func detected(t *testing.T, d *apitest.Daemon, id, branch string) {
	t.Helper()
	_, err := control.NewRepositories(d.Store).Detect(t.Context(), apitest.Scope,
		control.DetectedRepository{ID: id, Remote: "https://example.invalid/acme/app.git", DefaultBranch: branch}, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAMemberReadsAndChangesARepository(t *testing.T) {
	d := apitest.Start(t)
	detected(t, d, aRepository, "main")
	list, err := d.Client.Repositories(t.Context())
	if err != nil || len(list.Repositories) != 1 {
		t.Fatalf("list %+v: %v", list, err)
	}
	if got := list.Repositories[0]; got.ID != aRepository || got.Base != "main" || got.MergeMethod != ledger.MergeCommit || got.StartFrom != ledger.StartFromMerged || got.Integration {
		t.Errorf("the record reads %+v", got)
	}
	base, squash, on := "release/1.2", ledger.MergeSquash, true
	// A name that is a prefix of the identity is enough.
	changed, err := d.Client.SetRepository(t.Context(), aRepository[:8], api.RepositoryChange{Base: &base, MergeMethod: &squash, Integration: &on})
	if err != nil {
		t.Fatal(err)
	}
	if changed.ID != aRepository || changed.Base != "release/1.2" || changed.BaseBranch != "release/1.2" || changed.MergeMethod != ledger.MergeSquash || !changed.Integration {
		t.Errorf("the change left %+v", changed)
	}
	shown, err := d.Client.Repository(t.Context(), aRepository)
	if err != nil || shown != changed {
		t.Errorf("show reads %+v: %v", shown, err)
	}
	// Empty follows the default branch again.
	empty := ""
	if back, err := d.Client.SetRepository(t.Context(), aRepository, api.RepositoryChange{Base: &empty}); err != nil || back.Base != "main" || back.BaseBranch != "" {
		t.Errorf("clearing the base left %+v: %v", back, err)
	}
}

func TestAMemberChangesTheMergeAuthority(t *testing.T) {
	d := apitest.Start(t)
	detected(t, d, aRepository, "main")
	if record, err := d.Client.Repository(t.Context(), aRepository); err != nil || record.Level() != ledger.PersonMerges {
		t.Fatalf("a new record reads %+v: %v", record, err)
	}
	level := ledger.ReviewApproves
	changed, err := d.Client.SetRepository(t.Context(), aRepository, api.RepositoryChange{MergeAuthority: &level})
	if err != nil || changed.MergeAuthority != ledger.ReviewApproves {
		t.Fatalf("the change left %+v: %v", changed, err)
	}
	bad := ledger.MergeAuthority("whenever")
	_, err = d.Client.SetRepository(t.Context(), aRepository, api.RepositoryChange{MergeAuthority: &bad})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	if record, err := d.Client.Repository(t.Context(), aRepository); err != nil || record.MergeAuthority != ledger.ReviewApproves {
		t.Errorf("a refused level changed the record: %+v: %v", record, err)
	}
}

func TestARepositoryRefusesWhatCannotHold(t *testing.T) {
	d := apitest.Start(t)
	detected(t, d, aRepository, "main")
	accepted, squash, bad := ledger.StartFromAccepted, ledger.MergeSquash, ledger.MergeMethod("fast-forward")
	if _, err := d.Client.SetRepository(t.Context(), aRepository, api.RepositoryChange{MergeMethod: &squash}); err != nil {
		t.Fatal(err)
	}
	_, err := d.Client.SetRepository(t.Context(), aRepository, api.RepositoryChange{StartFrom: &accepted})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	_, err = d.Client.SetRepository(t.Context(), aRepository, api.RepositoryChange{MergeMethod: &bad})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	_, err = d.Client.SetRepository(t.Context(), "0000000", api.RepositoryChange{MergeMethod: &squash})
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
	if _, err := d.Client.SetRepository(t.Context(), "3f2a1b0", api.RepositoryChange{}); err != nil {
		t.Errorf("an unambiguous prefix: %v", err)
	}
	detected(t, d, "3f2a1b0ffffffffffffffffffffffffffffffffffff", "main")
	_, err = d.Client.SetRepository(t.Context(), "3f2a1b0", api.RepositoryChange{})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
}

// Reaching the socket proves nothing, because a lane runs as the same user. Only a member's session
// changes how much a repository may merge or where work starts.
func TestOnlyAMemberChangesARepository(t *testing.T) {
	d := apitest.Start(t)
	detected(t, d, aRepository, "main")
	credential, _ := joinWorker(t, d)
	on := true
	cases := []struct {
		name  string
		setup func(*api.ProjectClient) *api.ProjectClient
	}{
		{"a caller with nothing", func(c *api.ProjectClient) *api.ProjectClient { return c }},
		{"the worker's credential", func(c *api.ProjectClient) *api.ProjectClient { return c.WithCredential(credential) }},
		{"a lane's token where a session goes", withSession("a-lane-token")},
	}
	for _, tt := range cases {
		plain := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
		t.Cleanup(plain.Close)
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.setup(plain).SetRepository(t.Context(), aRepository, api.RepositoryChange{Integration: &on})
			refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
		})
	}
	err := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		record, err := tx.Repositories().Repository(aRepository)
		if err == nil && record.Integration {
			t.Errorf("a refused caller changed the record: %+v", record)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// calibrated leaves the records of a member's decisions after a review: two the member agreed with, and one
// approval the member sent back.
func calibrated(t *testing.T, d *apitest.Daemon, repository string) {
	t.Helper()
	records := []workorders.Calibration{
		{Outcome: workorders.ReviewPass, Decision: workorders.DecisionAccept, Agreement: workorders.AgreementAgree},
		{Outcome: workorders.ReviewFail, Decision: workorders.DecisionRework, Agreement: workorders.AgreementAgree},
		{Outcome: workorders.ReviewPass, Decision: workorders.DecisionRework, Agreement: workorders.AgreementDisagree},
	}
	err := d.Store.Write(t.Context(), apitest.Scope, "calibration.test", func(tx ports.WriteTx) error {
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
		t.Fatal(err)
	}
}

func TestARepositoryReadsItsReviewAgreementBesideItsLevel(t *testing.T) {
	d := apitest.Start(t)
	detected(t, d, aRepository, "main")
	detected(t, d, "9e8d7c6b5a49382716059483726150493827160a", "main")
	calibrated(t, d, aRepository)

	shown, err := d.Client.Repository(t.Context(), aRepository)
	if err != nil {
		t.Fatal(err)
	}
	all := shown.ReviewAgreement.AllTime
	if all.Decisions != 3 || all.Agreed != 2 || all.Disagreed != 1 || all.ApprovalsOverturned != 1 || all.FailuresOverridden != 0 || shown.ReviewAgreement.Last30Days.Decisions != 3 {
		t.Errorf("show reads %+v", shown.ReviewAgreement)
	}
	if shown.Level() != ledger.PersonMerges {
		t.Errorf("show reads level %s", shown.Level())
	}
	list, err := d.Client.Repositories(t.Context())
	if err != nil || len(list.Repositories) != 2 {
		t.Fatalf("list %+v: %v", list, err)
	}
	for _, view := range list.Repositories {
		want := int64(0)
		if view.ID == aRepository {
			want = 3
		}
		if view.ReviewAgreement.AllTime.Decisions != want {
			t.Errorf("list reads %d decisions for %s, want %d", view.ReviewAgreement.AllTime.Decisions, view.ID, want)
		}
	}
	level := ledger.PersonApproves
	changed, err := d.Client.SetRepository(t.Context(), aRepository, api.RepositoryChange{MergeAuthority: &level})
	if err != nil || changed.ReviewAgreement.AllTime.Decisions != 3 {
		t.Errorf("a change reads %+v: %v", changed.ReviewAgreement, err)
	}

	report, err := d.Client.Measures(t.Context(), "7d")
	if err != nil {
		t.Fatal(err)
	}
	if got := report.ReviewAgreement.Workspace.AllTime; got.Decisions != 3 || got.Agreed != 2 {
		t.Errorf("the measures read %+v", got)
	}
	var seen int
	for _, repo := range report.ReviewAgreement.Repositories {
		if repo.Repository == aRepository {
			seen++
			if repo.MergeAuthority != string(ledger.PersonApproves) || repo.AllTime.Decisions != 3 {
				t.Errorf("the measures read %+v beside the level in force", repo)
			}
		}
	}
	if seen != 1 || len(report.ReviewAgreement.Repositories) != 2 {
		t.Errorf("the measures list %+v", report.ReviewAgreement.Repositories)
	}
	// Reading agreement is not a decision about the level: the member's change stays the only one.
	if got, err := d.Client.Repository(t.Context(), aRepository); err != nil || got.Level() != ledger.PersonApproves {
		t.Errorf("the level reads %v: %v", got.Level(), err)
	}
}
