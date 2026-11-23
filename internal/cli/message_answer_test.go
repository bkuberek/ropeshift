package cli_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
)

// A message of kind answer is made by the answer to a question, so `message send` refuses it and names the command that
// answers.
func TestMessageSendRefusesTheAnswerKind(t *testing.T) {
	s := newSession(t)
	code, _, stderr := s.run("message", "send", "lane-1", "--kind", "answer", "--reply-to", "q-1", "--body", "Use two retries.")
	if code == cli.ExitOK || !strings.Contains(stderr, "question answer") {
		t.Errorf("exit %d: %s", code, stderr)
	}
}
