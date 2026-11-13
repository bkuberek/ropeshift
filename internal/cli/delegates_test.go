package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
)

// asDelegate runs a command with a delegate credential in place of a session.
func (s *session) asDelegate(token string, args ...string) (int, string, string) {
	s.t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, &stdout, &stderr, cli.Options{Socket: s.daemon.Socket, Delegate: token})
	return code, stdout.String(), stderr.String()
}

// delegate makes a credential in the harness's project and returns its ID and token.
func (s *session) delegate() (string, string) {
	s.t.Helper()
	text := s.must("delegate", "create", "--project", "default", "--label", "planning assistant", "--expires", "1d")
	if !strings.Contains(text, "This credential is shown once.") {
		s.t.Errorf("create printed %q", text)
	}
	var token string
	for line := range strings.Lines(text) {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "export "+authority.EnvDelegateToken+"="); ok {
			token = value
		}
	}
	var created api.DelegateList
	if err := json.Unmarshal([]byte(s.must("delegate", "list", "--json")), &created); err != nil || len(created.Delegates) == 0 || token == "" {
		s.t.Fatalf("create printed %q, and the list reads %+v: %v", text, created, err)
	}
	return created.Delegates[len(created.Delegates)-1].ID, token
}

func TestADelegateIsMadeListedRevokedAndRemoved(t *testing.T) {
	s := newSession(t)
	id, token := s.delegate()
	list := s.must("delegate", "list")
	if !strings.Contains(list, id) || !strings.Contains(list, "active") || !strings.Contains(list, "never used") || strings.Contains(list, token) {
		t.Errorf("list %q", list)
	}
	if code, _, stderr := s.run("delegate", "remove", id); code != cli.ExitConflict || !strings.Contains(stderr, "active") {
		t.Errorf("removing an active credential: %d %q", code, stderr)
	}
	if text := s.must("delegate", "revoke", id); !strings.Contains(text, "revoked "+id) {
		t.Errorf("revoke %q", text)
	}
	if code, _, stderr := s.asDelegate(token, "work", "list", "--project", "default"); code != cli.ExitForbidden || !strings.Contains(stderr, "revoked") {
		t.Errorf("a revoked credential: %d %q", code, stderr)
	}
	if text := s.must("delegate", "remove", id); !strings.Contains(text, "removed "+id) {
		t.Errorf("remove %q", text)
	}
	for _, args := range [][]string{
		{"delegate", "create", "--project", "default", "--label", "x"},
		{"delegate", "create", "--project", "default", "--label", "x", "--expires", "soon"},
		{"delegate", "create", "--project", "default", "--label", "x", "--expires", "31d"},
	} {
		if code, _, _ := s.run(args...); code != cli.ExitUsage {
			t.Errorf("%v exited %d", args, code)
		}
	}
}

func TestACommandWithBothCredentialsIsRefused(t *testing.T) {
	s := newSession(t)
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"work", "list", "--project", "default"}, &stdout, &stderr, cli.Options{Socket: s.daemon.Socket, Session: s.daemon.Session, Delegate: "a token"})
	if code != cli.ExitUsage || !strings.Contains(stderr.String(), authority.EnvDelegateToken) {
		t.Errorf("exit %d: %q", code, stderr.String())
	}
}

func TestADelegatesConsentCommandPreparesACardAMemberConfirms(t *testing.T) {
	s := newSession(t)
	s.ready("wo")
	_, token := s.delegate()
	code, stdout, stderr := s.asDelegate(token, "work", "cancel", "wo", "--reason", "The plan dropped it.", "--project", "default")
	if code != cli.ExitUsage || !strings.Contains(stderr, "A card needs the reason") {
		t.Errorf("no reason: %d %q %q", code, stdout, stderr)
	}
	code, stdout, stderr = s.asDelegate(token, "work", "cancel", "wo", "--reason", "The plan dropped it.", "--project", "default", "--why", "The goal changed.")
	if code != cli.ExitPrepared || !strings.HasPrefix(stdout, "Prepared card ") ||
		!strings.Contains(stdout, ": Cancel work: wo. It runs only when a member confirms it in Needs you or with ropeshift confirm ") {
		t.Fatalf("a prepared card: %d %q %q", code, stdout, stderr)
	}
	card := strings.Fields(stdout)[2]
	card = strings.TrimSuffix(card, ":")
	code, stdout, _ = s.asDelegate(token, "work", "cancel", "wo", "--reason", "Again.", "--project", "default", "--why", "Still.", "--json")
	var asJSON authority.Approval
	if code != cli.ExitPrepared || json.Unmarshal([]byte(stdout), &asJSON) != nil || asJSON.Kind != authority.KindConfirm {
		t.Errorf("a prepared card as JSON: %d %q", code, stdout)
	}
	if code, _, stderr := s.asDelegate(token, "confirm", card, "--project", "default"); code != cli.ExitForbidden || !strings.Contains(stderr, "only a member") {
		t.Errorf("a delegate's confirmation: %d %q", code, stderr)
	}
	if text := s.must("confirm", card); !strings.Contains(text, "confirmed "+card+": Cancel work: wo") {
		t.Errorf("confirm %q", text)
	}
	if show := s.must("work", "show", "wo"); !strings.Contains(show, "canceled") {
		t.Errorf("the confirmed card left %q", show)
	}
	if text := s.must("decline", asJSON.ID, "--reason", "Already done."); !strings.Contains(text, "declined "+asJSON.ID) {
		t.Errorf("decline %q", text)
	}
	if code, _, _ := s.run("confirm", "a-request-that-is-no-card"); code != cli.ExitNotFound {
		t.Errorf("confirming nothing exited %d", code)
	}
}

func TestOperationListPrintsTheClassTable(t *testing.T) {
	s := newSession(t)
	text := s.must("operation", "list")
	for _, want := range []string{"repository.raise_merge_authority\tconsent\tnever exempt\tRaise merge authority", "work_order.create\toperational\t-\tCreate work", "card.confirm\tmember_only"} {
		if !strings.Contains(text, want) {
			t.Errorf("the list lacks %q", want)
		}
	}
	var list api.OperationList
	if err := json.Unmarshal([]byte(s.must("operation", "list", "--json")), &list); err != nil || len(list.Operations) != len(authority.Operations()) {
		t.Errorf("as JSON %d: %v", len(list.Operations), err)
	}
}

func TestSignOutAllEndsEverySessionAndDelegate(t *testing.T) {
	s := newSession(t)
	_, token := s.delegate()
	if text := s.must("sign-out", "--all"); !strings.Contains(text, "signed out of 1 sessions and revoked 1 delegates") {
		t.Errorf("sign-out --all %q", text)
	}
	if code, _, _ := s.asDelegate(token, "work", "list", "--project", "default"); code != cli.ExitForbidden {
		t.Errorf("a revoked delegate exited %d", code)
	}
	if code, _, _ := s.run("work", "create", "--id", "w2", "--title", "After"); code != cli.ExitForbidden {
		t.Errorf("an ended session exited %d", code)
	}
}
