package control_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

const leakFinding = "secrets:github_token:config/dev.yaml:1a2b3c4d5e6f"

func (r *practicesRig) acknowledged(repository string) []ledger.PracticesAcknowledgement {
	r.t.Helper()
	var out []ledger.PracticesAcknowledgement
	err := r.store.Read(r.t.Context(), "pa", func(tx ports.ReadTx) (err error) {
		out, err = tx.Practices().Acknowledgements(repository)
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return out
}

func TestAMemberAcknowledgesAFindingWithAReasonAndTheEventSaysSo(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolPracticesChecks)
	repo := r.register("repo-a", "alpha", "worker-1")
	got, err := r.practices.Acknowledge(t.Context(), "pa", repo.ID, leakFinding, "  a fixture the tests read  ", projectMember)
	if err != nil {
		t.Fatal(err)
	}
	if got.Repository != repo.ID || got.Finding != leakFinding || got.Reason != "a fixture the tests read" || got.Member != projectMember.ID || got.AcknowledgedAt == 0 {
		t.Errorf("the acknowledgement reads %+v, want the finding, the reason without its padding, the member, and the time", got)
	}
	if kept := r.acknowledged(repo.ID); len(kept) != 1 || kept[0] != got {
		t.Errorf("the repository keeps %+v, want %+v", kept, got)
	}
	events := r.events(ledger.EventPracticesAcknowledged)
	if len(events) != 1 || events[0].Subject != ledger.PracticesFindingSubject(repo.ID, leakFinding) || events[0].Actor != projectMember {
		t.Fatalf("the events read %+v, want one about the repository's finding by the member", events)
	}
	var data map[string]any
	if err := json.Unmarshal(events[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["finding"] != leakFinding || data["repository"] != repo.ID || data["reason"] != got.Reason || data["member"] != projectMember.ID || int64(data["acknowledged_at"].(float64)) != got.AcknowledgedAt {
		t.Errorf("the event holds %v, want the finding, the repository, the reason, the member, and the time", data)
	}
}

func TestAFindingNoCheckHasReportedMayBeAcknowledged(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolPracticesChecks)
	repo := r.register("repo-a", "alpha", "worker-1")
	if _, err := r.record(repo.ID); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("the repository has a result: %v", err)
	}
	if _, err := r.practices.Acknowledge(t.Context(), "pa", repo.ID, "codeowners", "owned by the platform team", projectMember); err != nil {
		t.Errorf("acknowledging a finding no result holds: %v", err)
	}
}

func TestAcknowledgingRefusesWhatIsNotAReasonedFindingOfARepository(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolPracticesChecks)
	repo := r.register("repo-a", "alpha", "worker-1")
	if _, err := r.practices.Acknowledge(t.Context(), "pa", repo.ID, "codeowners", "owned by the platform team", projectMember); err != nil {
		t.Fatal(err)
	}
	delegate := authority.DelegateActor(projectMember.ID, authority.DelegateIDPrefix+"x")
	for _, tc := range []struct {
		name                       string
		repository, finding, why   string
		actor                      ledger.Actor
		wantInvalid, wantForbidden bool
		want                       error
	}{
		{name: "an empty reason", repository: repo.ID, finding: "security-policy", why: "", wantInvalid: true},
		{name: "a reason of spaces", repository: repo.ID, finding: "security-policy", why: " \n\t ", wantInvalid: true},
		{name: "a reason over 2 KiB", repository: repo.ID, finding: "security-policy", why: strings.Repeat("a", ledger.MaxPracticesReason+1), wantInvalid: true},
		{name: "a finding of no practice", repository: repo.ID, finding: "no-such-practice", why: "x", wantInvalid: true},
		{name: "a malformed subject", repository: repo.ID, finding: "secrets:github_token", why: "x", wantInvalid: true},
		{name: "a finding already acknowledged", repository: repo.ID, finding: "codeowners", why: "again", want: ledger.ErrAcknowledged},
		{name: "a repository the project does not hold", repository: "repo-zzz", finding: "security-policy", why: "x", want: ports.ErrNotFound},
		{name: "a delegate", repository: repo.ID, finding: "security-policy", why: "x", actor: delegate, wantForbidden: true},
	} {
		actor := projectMember
		if tc.actor.ID != "" {
			actor = tc.actor
		}
		_, err := r.practices.Acknowledge(t.Context(), "pa", tc.repository, tc.finding, tc.why, actor)
		switch {
		case tc.wantInvalid && !errors.Is(err, workorders.ErrInvalid):
			t.Errorf("%s: acknowledging answered %v, want an invalid request", tc.name, err)
		case tc.wantForbidden && !errors.Is(err, workorders.ErrForbidden):
			t.Errorf("%s: acknowledging answered %v, want forbidden", tc.name, err)
		case tc.want != nil && !errors.Is(err, tc.want):
			t.Errorf("%s: acknowledging answered %v, want %v", tc.name, err, tc.want)
		}
	}
	if kept := r.acknowledged(repo.ID); len(kept) != 1 || kept[0].Reason != "owned by the platform team" {
		t.Errorf("the repository keeps %+v after the refusals, want only the first acknowledgement as it was", kept)
	}
	if events := r.events(ledger.EventPracticesAcknowledged); len(events) != 1 {
		t.Errorf("%d acknowledgement events, want only the first one's: a refusal records nothing", len(events))
	}
}

func TestWithdrawingRemovesTheAcknowledgementAndTheEventSaysSo(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolPracticesChecks)
	repo := r.register("repo-a", "alpha", "worker-1")
	made, err := r.practices.Acknowledge(t.Context(), "pa", repo.ID, "codeowners", "owned by the platform team", projectMember)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.practices.Withdraw(t.Context(), "pa", repo.ID, "codeowners", projectMember)
	if err != nil || got != made {
		t.Fatalf("withdrawing answered %+v, %v; want the acknowledgement it removed, %+v", got, err, made)
	}
	if kept := r.acknowledged(repo.ID); len(kept) != 0 {
		t.Errorf("the repository keeps %+v after the withdrawal", kept)
	}
	events := r.events(ledger.EventPracticesWithdrawn)
	if len(events) != 1 || events[0].Subject != ledger.PracticesFindingSubject(repo.ID, "codeowners") || events[0].Actor != projectMember {
		t.Fatalf("the events read %+v, want one about the repository's finding by the member", events)
	}
	var data map[string]any
	if err := json.Unmarshal(events[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["finding"] != "codeowners" || data["repository"] != repo.ID || data["reason"] != made.Reason || data["member"] != projectMember.ID || data["acknowledged_by"] != made.Member {
		t.Errorf("the event holds %v, want the finding, the repository, the reason withdrawn, and the member who withdrew it", data)
	}
	if _, err := r.practices.Withdraw(t.Context(), "pa", repo.ID, "codeowners", projectMember); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("withdrawing again answered %v, want not found", err)
	}
	if _, err := r.practices.Withdraw(t.Context(), "pa", repo.ID, "no-such-practice", projectMember); !errors.Is(err, workorders.ErrInvalid) {
		t.Errorf("withdrawing a finding no practice names answered %v, want an invalid request", err)
	}
	delegate := authority.DelegateActor(projectMember.ID, authority.DelegateIDPrefix+"x")
	if _, err := r.practices.Acknowledge(t.Context(), "pa", repo.ID, "codeowners", "again", projectMember); err != nil {
		t.Fatal(err)
	}
	if _, err := r.practices.Withdraw(t.Context(), "pa", repo.ID, "codeowners", delegate); !errors.Is(err, workorders.ErrForbidden) {
		t.Errorf("a delegate's withdrawal answered %v, want forbidden", err)
	}
	if events := r.events(ledger.EventPracticesWithdrawn); len(events) != 1 {
		t.Errorf("%d withdrawal events, want one: a refusal records nothing", len(events))
	}
}

func TestReadingAResultCarriesEachAcknowledgementOfItsRepository(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolPracticesChecks)
	a := r.register("repo-a", "alpha", "worker-1")
	b := r.register("repo-b", "beta", "worker-1")
	for _, ack := range []struct{ repo, finding string }{{a.ID, "security-policy"}, {a.ID, "codeowners"}, {b.ID, "codeowners"}} {
		if _, err := r.practices.Acknowledge(t.Context(), "pa", ack.repo, ack.finding, "reason", projectMember); err != nil {
			t.Fatal(err)
		}
	}
	reads, err := r.practices.Read(t.Context(), "pa")
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string][]string{}
	for _, read := range reads {
		for _, ack := range read.Acknowledgements {
			counts[read.Repository.ID] = append(counts[read.Repository.ID], ack.Finding)
		}
	}
	if len(counts[a.ID]) != 2 || counts[a.ID][0] != "codeowners" || counts[a.ID][1] != "security-policy" || len(counts[b.ID]) != 1 {
		t.Errorf("the reads carry %v, want each repository's own acknowledgements by finding", counts)
	}
}

// A reason is model text when the assistant writes it, so what is kept, shown, and recorded is one line with the shapes
// of credentials taken out, whether the credential is on one line or spans two.
func TestAnAcknowledgementsReasonIsKeptOnOneLineWithNoCredential(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolPracticesChecks)
	repo := r.register("repo-a", "alpha", "worker-1")
	token := "ghp_" + strings.Repeat("a", 30)
	key := "-----BEGIN PRIVATE KEY-----\nbbbbbbbbbbbbbbbbbbbbbbbb\n-----END PRIVATE KEY-----"
	got, err := r.practices.Acknowledge(t.Context(), "pa", repo.ID, "codeowners", "a fixture,\r\nnot real: "+token+"\n\tnor "+key+"\x00 end", projectMember)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{got.Reason, r.acknowledged(repo.ID)[0].Reason, string(r.events(ledger.EventPracticesAcknowledged)[0].Data)} {
		if strings.ContainsAny(text, "\r\n\t") || strings.Contains(text, token) || strings.Contains(text, "bbbbbbbbbbbb") {
			t.Errorf("the reason is kept as %q, want one line with no credential", text)
		}
	}
	if !strings.HasPrefix(got.Reason, "a fixture, not real: ") || !strings.HasSuffix(got.Reason, " end") {
		t.Errorf("the reason reads %q, want the text around the credentials kept", got.Reason)
	}
	long := strings.Repeat("ghp_"+strings.Repeat("c", 20)+" ", 200)[:ledger.MaxPracticesReason]
	if _, err := r.practices.Acknowledge(t.Context(), "pa", repo.ID, "security-policy", long, projectMember); err != nil {
		t.Fatal(err)
	}
	for _, ack := range r.acknowledged(repo.ID) {
		if len(ack.Reason) > ledger.MaxPracticesReason {
			t.Errorf("a redacted reason is %d bytes, over the bound of %d", len(ack.Reason), ledger.MaxPracticesReason)
		}
	}
}
