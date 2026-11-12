package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
)

// audit reads the project's log newest first, with the filters the web UI's Audit takes, and the
// installation's with --installation.
func TestAuditReadsALogNewestFirst(t *testing.T) {
	s := newSession(t)
	s.ready("wo-1")
	s.ready("wo-2")
	text := s.must("audit", "--kind", "work_order.created")
	first, second := strings.Index(text, "wo-2"), strings.Index(text, "wo-1")
	if first < 0 || second < 0 || first > second {
		t.Errorf("audit text %q", text)
	}
	var page api.AuditPage
	if err := json.Unmarshal([]byte(s.must("audit", "--kind", "work_order.created", "--limit", "1", "--json")), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].SubjectID != "wo-2" || page.Next == 0 {
		t.Errorf("page %+v", page)
	}
	if !strings.Contains(s.must("audit", "--installation"), "\n") {
		t.Error("the installation's audit printed nothing")
	}
	if code, _, stderr := s.run("audit", "--limit", "500"); code == cli.ExitOK || !strings.Contains(stderr, "limit") {
		t.Errorf("--limit 500: exit %d, stderr %q", code, stderr)
	}
}

// member list names each member with a live session and never a token.
func TestMemberListNamesWhoHoldsASession(t *testing.T) {
	s := newSession(t)
	text := s.must("member", "list")
	if !strings.Contains(text, "live sessions") || strings.Contains(text, s.daemon.Session) {
		t.Errorf("member list %q", text)
	}
}
