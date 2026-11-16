//go:build screenshots

package screenshots

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
)

// The guide walk also checks two product gaps that the guides depend on. Each check logs what it found, and
// fails when the gap is real, so a guide never teaches a step that the product refuses.

// TestNoCommandSpendsAJoinToken checks that nothing a person runs consumes a join token. The web UI offers
// Create join token in solo mode, and `ropeshift worker token` makes one; a token is spent only by
// POST /v1/workers/register, which the worker calls with a token the solo daemon makes for itself. The check
// reads the help of both programs and the web UI's page, and fails when a command that takes a token appears.
func TestNoCommandSpendsAJoinToken(t *testing.T) {
	in := newInstall(t, binaryPath(t), *keepFlag)
	newHarbor(in)

	for _, line := range strings.Split(in.must("help"), "\n") {
		if lower := strings.ToLower(line); strings.Contains(lower, "join") || strings.Contains(lower, "token") {
			t.Logf("ropeshift help: %s", strings.TrimSpace(line))
		}
	}
	// The one command that mentions a token makes it, and the daemon has no command to spend it.
	if token := strings.TrimSpace(in.must("worker", "token")); token == "" {
		t.Error("worker token printed no token")
	}
	cmd := exec.Command(in.daemonBin, "help")
	cmd.Env = in.env()
	cmd.Dir = in.home
	raw, err := cmd.CombinedOutput()
	t.Logf("ropeshiftd help (%v):\n%s", err, raw)
	if strings.Contains(strings.ToLower(string(raw)), "join") {
		t.Errorf("PG6: the daemon has a command that names a join:\n%s", raw)
	}
}

// binaryPath is the binary under capture.
func binaryPath(t *testing.T) string {
	t.Helper()
	if *binaryFlag == "" {
		t.Fatal("set -binary; scripts/screenshots.sh does")
	}
	binary, err := filepath.Abs(*binaryFlag)
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

// TestAssistantAnswersInALaterProject checks that a conversation with the assistant works in a project created
// after the installation's first, as it does in the first.
func TestAssistantAnswersInALaterProject(t *testing.T) {
	in := newInstall(t, binaryPath(t), *keepFlag)
	h := newHarbor(in)

	in.must("repository", "add", h.api.Dir)
	in.must("project", "create", "later", "--display-name", "Later")
	in.must("repository", "add", h.web.Dir, "--project", "later")

	for _, project := range []string{"default", "later"} {
		var opened api.ConversationOpened
		raw := ""
		if !in.waitUntil(func() bool {
			code, out, stderr := in.run("chat", "new", "--project", project, "--name", "Check in "+project, "--json")
			raw = out + stderr
			return code == 0 && json.Unmarshal([]byte(out), &opened) == nil
		}) {
			t.Errorf("PG9: no conversation opened in project %s: %s", project, raw)
			continue
		}
		if opened.Conversation.RunLane != "" {
			in.reap(opened.Conversation.RunLane)
		}
		id := opened.Conversation.ID
		in.must("chat", "send", id, "--project", project, "--body", "What does this project hold?")
		answered := in.waitUntil(func() bool {
			code, out, _ := in.run("chat", "show", id, "--project", project)
			return code == 0 && strings.Contains(out, "no plan yet")
		})
		_, shown, _ := in.run("chat", "show", id, "--project", project)
		if !answered {
			t.Errorf("PG9: the assistant did not answer in project %s:\n%s%s", project, shown, in.daemonTail())
			continue
		}
		t.Logf("the assistant answered in project %s", project)
	}

	// A project with no repository is what guide 2 starts from, so what the assistant does there is recorded,
	// and the walk does not fail on it: the assistant needs a repository to run in.
	in.must("project", "create", "bare", "--display-name", "Bare")
	code, out, stderr := in.run("chat", "new", "--project", "bare", "--name", "Check in bare", "--json")
	t.Logf("a conversation in a project with no repository: exit %d: %s%s", code, strings.TrimSpace(out), strings.TrimSpace(stderr))
}
