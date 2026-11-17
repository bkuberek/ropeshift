package repofiles

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/test/gittest"
)

// idFile is a plan or a contract whose file names it by id.
func idFile(kind, id string) string {
	switch kind {
	case "plan":
		return "+++\nkind = \"plan\"\nid = \"" + id + "\"\ntitle = \"A plan\"\n+++\n\nHow.\n"
	default:
		return "+++\nkind = \"contract\"\nid = \"" + id + "\"\ntitle = \"A contract\"\n\n[[work_orders]]\nid = \"order\"\ntitle = \"Do it\"\n+++\n\nWhat.\n"
	}
}

// A plan and a contract are named by a plain ID, because the ledger and every scope that names them use it as
// it is.
func TestAPlanOrContractFileNamesItselfByAPlainID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		refused string
	}{
		{name: "a plain ID", id: "payments-v2"},
		{name: "a plain ID with an underscore and capitals", id: "Q4_2026"},
		{name: "a plain ID of the longest length", id: strings.Repeat("a", 64)},
		{name: "a dotted ID", id: "2026.q4", refused: "must be a plain ID"},
		{name: "an ID with a space", id: "payments v2", refused: "must be a plain ID"},
		{name: "an ID with a path separator", id: "plans/q4", refused: "must be a plain ID"},
		{name: "an ID past the longest length", id: strings.Repeat("a", 65), refused: "must be a plain ID"},
		{name: "an empty ID", id: "", refused: "no id"},
	}
	for _, kind := range []string{"plan", "contract"} {
		for _, tt := range tests {
			t.Run(kind+": "+tt.name, func(t *testing.T) {
				doc, err := ParseFile([]byte(idFile(kind, tt.id)))
				if tt.refused == "" {
					if err != nil {
						t.Fatal(err)
					}
					if got := idOf(doc); got != tt.id {
						t.Errorf("the file reads as %q, want %q", got, tt.id)
					}
					return
				}
				if !errors.Is(err, planning.ErrBadFile) || !strings.Contains(err.Error(), tt.refused) {
					t.Fatalf("got %v, want a bad file that says %q", err, tt.refused)
				}
				if tt.id != "" && !strings.Contains(err.Error(), tt.id) {
					t.Errorf("%q does not name the ID %q", err, tt.id)
				}
			})
		}
	}
}

func idOf(doc planning.Document) string {
	if doc.Plan != nil {
		return doc.Plan.ID
	}
	return doc.Contract.ID
}

// A repository whose plan or contract carries an ID that is not plain reports that file as one it cannot
// read, and reads every other file.
func TestTheGitSourceReportsAFileWhoseIDIsNotPlain(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("plan.md", idFile("plan", "2026.q4"))
	repo.Commit("contract.md", idFile("contract", "api v2"))
	repo.Commit("fine.md", idFile("contract", "api-v3"))
	batch, err := New(Config{Repository: repo.Dir, Env: repo.Env()}).Poll(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Items) != 1 || batch.Items[0].Contract.ID != "api-v3" {
		t.Errorf("items %+v, want the contract api-v3 alone", batch.Items)
	}
	problems := map[string]string{}
	for _, problem := range batch.Problems {
		problems[problem.Path] = problem.Detail
	}
	for path, id := range map[string]string{"plan.md": "2026.q4", "contract.md": "api v2"} {
		if detail := problems[path]; !strings.Contains(detail, id) || !strings.Contains(detail, "must be a plain ID") {
			t.Errorf("the problem of %s reads %q, want it to name %q and the rule", path, detail, id)
		}
	}
	if len(problems) != 2 {
		t.Errorf("problems %v, want the two files", problems)
	}
}
