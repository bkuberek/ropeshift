package cli_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/planning"
)

func outlined() api.PlanOutline {
	return api.PlanOutline{
		Commit:     "abc1234",
		Documents:  []api.OutlineDocument{{Path: "docs/search.md", Kind: "requirements", Title: "Search", Status: "changed"}, {Path: "specs/saved.md", Kind: "spec", Status: "new"}},
		Milestones: []api.OutlineMilestone{{ID: "m4", Title: "Saved searches v2", Priority: 4}},
		Contracts:  []api.OutlineContract{{ID: "c16", Title: "Saved search storage", Criteria: 3, WorkOrders: 2}},
		Decisions:  []planning.DraftDecision{{ID: "dd-10", Question: "Whose?", Answer: "One member"}},
		Questions:  []api.OutlineQuestion{{ID: "q1", Text: "Hourly or daily?", Blocking: true}},
		Approval:   &api.OutlineApproval{ID: "a1", State: authority.ApprovalPending, Version: 5},
	}
}

func TestLaneOutlinePrintsWhatTheBranchHolds(t *testing.T) {
	p := newPlane(t)
	p.on("GET /v1/lanes/{id}/outline", func(*http.Request, []byte) (any, *api.Error) { return outlined(), nil })
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"lane", "outline", "lane-1"}, &stdout, &stderr, cli.Options{Socket: p.Socket}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for _, want := range []string{
		"commit\tabc1234\n", "changed\tdocs/search.md\trequirements\n", "new\tspecs/saved.md\tspec\n", "milestone\tm4\t4\tSaved searches v2\n",
		"contract\tc16\t3 criteria\t2 work orders\tSaved search storage\n", "decision\tdd-10\tWhose?\n",
		"question\tq1\tblocking\tHourly or daily?\n", "approval\ta1\tpending\tversion 5\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("lane outline lacks %q:\n%s", want, stdout.String())
		}
	}
}

func TestLaneOutlineSaysWhyABranchCannotBeRead(t *testing.T) {
	p := newPlane(t)
	p.on("GET /v1/lanes/{id}/outline", func(*http.Request, []byte) (any, *api.Error) {
		return api.PlanOutline{Unread: "the lane recorded no commit its branch began at"}, nil
	})
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"lane", "outline", "lane-1"}, &stdout, &stderr, cli.Options{Socket: p.Socket}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if got := stdout.String(); got != "unread\tthe lane recorded no commit its branch began at\n" {
		t.Errorf("printed %q", got)
	}
}

func TestLaneOutlineReadsAsJSON(t *testing.T) {
	p := newPlane(t)
	p.on("GET /v1/lanes/{id}/outline", func(*http.Request, []byte) (any, *api.Error) { return outlined(), nil })
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"lane", "outline", "lane-1", "--json"}, &stdout, &stderr, cli.Options{Socket: p.Socket}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var got api.PlanOutline
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got.Commit != "abc1234" || len(got.Contracts) != 1 {
		t.Errorf("printed %q: %v", stdout.String(), err)
	}
}
