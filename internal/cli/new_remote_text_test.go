package cli

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

// The text of a result is the one place a step's detail is printed, so it drops what a tool printed that a terminal would
// act on, whatever built the result.
func TestAResultsTextPrintsWhatGitAndGHSaidAsPlainText(t *testing.T) {
	hostile := "\x1b[31mdenied\x1b[0m\x1b]0;title\x07\u202e\r"
	result := newResult{
		Path: "/srv/a", Project: "a", Commit: "abc", CommitError: hostile,
		Repositories: []ledger.RepositoryReport{{
			Name: "a" + hostile, Path: ".", CommitError: hostile, Remote: "git@example.com:a/a.git" + hostile,
			Steps: []ledger.RemoteStep{{Step: "push", Outcome: "failed", Detail: hostile}},
		}},
		Remaining: []string{"git push" + hostile},
	}
	text := result.text()
	if strings.ContainsAny(text, "\x1b\x07\u202e\r") || strings.Contains(text, "title") {
		t.Errorf("the text holds what a terminal would act on: %q", text)
	}
	if !strings.Contains(text, "push\tfailed\tdenied\n") {
		t.Errorf("the plain detail is gone: %q", text)
	}
}
