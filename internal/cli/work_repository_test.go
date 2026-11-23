package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/workorders"
)

// `work create --repository` takes a repository's ID, its name, or a prefix of its ID, and the work order keeps the ID.
func TestWorkCreateTakesARepositoryByName(t *testing.T) {
	s := newSession(t)
	s.detect("repo-0001-storefront", "main")
	for _, named := range []string{"repo-0001-storefront", "app", "repo-0001"} {
		id := "wo-" + strings.ReplaceAll(named, "-", "")
		out := s.must("work", "create", "--id", id, "--title", "T", "--criterion", "It works", "--repository", named, "--json")
		var order workorders.WorkOrder
		if err := json.Unmarshal([]byte(out), &order); err != nil {
			t.Fatal(err)
		}
		if order.RepositoryID != "repo-0001-storefront" {
			t.Errorf("--repository %s: the order names %q", named, order.RepositoryID)
		}
	}
}

// A name no repository answers to is refused before anything is recorded, and the error says how a repository is named.
func TestWorkCreateRefusesARepositoryNoneAnswersTo(t *testing.T) {
	s := newSession(t)
	s.detect("repo-0001-storefront", "main")
	code, _, stderr := s.run("work", "create", "--id", "wo-nowhere", "--title", "T", "--repository", "nowhere")
	if code != cli.ExitUsage {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{`"nowhere"`, "ID, name", "repository list"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the error %q does not say %q", stderr, want)
		}
	}
	if code, _, _ := s.run("work", "show", "wo-nowhere"); code == cli.ExitOK {
		t.Error("a work order was recorded")
	}
}
