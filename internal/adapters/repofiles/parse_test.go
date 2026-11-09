package repofiles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// What a planning lane is shown is what intake reads, so an agent that copies it writes a file.
func TestThePlanningExampleIsAFile(t *testing.T) {
	doc, err := ParseFile([]byte(planning.FormatExample))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Contract == nil || doc.Contract.ID != "checkout-retry" || len(doc.Contract.Criteria) != 1 || len(doc.Contract.WorkOrders) != 1 {
		t.Fatalf("document %+v", doc)
	}
	if !strings.Contains(doc.Statement, "must deliver") {
		t.Fatalf("statement %q", doc.Statement)
	}
}

func TestParseFile(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		wantErr error
		check   func(*testing.T, planning.Document)
	}{
		{
			name:    "a contract in the D1 format",
			fixture: "valid.md",
			check: func(t *testing.T, doc planning.Document) {
				c := doc.Contract
				if doc.Kind != planning.FileContract || c == nil {
					t.Fatalf("got %+v", doc)
				}
				if c.ID != "checkout-retry" || c.Title != "Retry failed checkout calls" {
					t.Errorf("header %+v", c)
				}
				if len(c.Requirements) != 1 || c.Requirements[0] != "PAY-12" {
					t.Errorf("requirements %v", c.Requirements)
				}
				if len(c.Criteria) != 1 || c.Criteria[0].ID != "c1" {
					t.Errorf("criteria %v", c.Criteria)
				}
				if len(c.WorkOrders) != 1 {
					t.Fatalf("work orders %v", c.WorkOrders)
				}
				order := c.WorkOrders[0]
				if order.ID != "retry-budget" || order.Repo != "." || order.Priority != 2 || order.Size != "medium" {
					t.Errorf("work order %+v", order)
				}
				if order.Needs["driver"] != "claude-code" {
					t.Errorf("needs %v", order.Needs)
				}
				if doc.Statement != "The statement of what this work must deliver.\n" {
					t.Errorf("statement %q", doc.Statement)
				}
			},
		},
		{
			name:    "no criteria parses, and the definition of ready refuses it",
			fixture: "no-criteria.md",
			check: func(t *testing.T, doc planning.Document) {
				if doc.Contract == nil || len(doc.Contract.Criteria) != 0 {
					t.Fatalf("got %+v", doc)
				}
				if got := doc.Contract.WorkOrders[0].Size; got != planning.SizeUnknown {
					t.Errorf("an absent size reads %q, want %q (D27)", got, planning.SizeUnknown)
				}
				if got := doc.Contract.WorkOrders[0].Repo; got != "." {
					t.Errorf("an absent repo reads %q", got)
				}
			},
		},
		{
			name:    "a goal",
			fixture: "goal.md",
			check: func(t *testing.T, doc planning.Document) {
				if doc.Kind != planning.FileGoal || doc.Goal == nil {
					t.Fatalf("got %+v", doc)
				}
				if doc.Goal.ID != "reliable-checkout" || len(doc.Goal.SuccessMeasures) != 1 {
					t.Errorf("goal %+v", doc.Goal)
				}
				if doc.Statement != "Why this matters.\n" {
					t.Errorf("statement %q", doc.Statement)
				}
			},
		},
		{
			name:    "a plan names its goal",
			fixture: "plan.md",
			check: func(t *testing.T, doc planning.Document) {
				if doc.Kind != planning.FilePlan || doc.Plan == nil {
					t.Fatalf("got %+v", doc)
				}
				if doc.Plan.ID != "checkout-plan" || doc.Plan.Goal != "reliable-checkout" {
					t.Errorf("plan %+v", doc.Plan)
				}
			},
		},
		{
			name:    "requirements come from tables, and the body stays prose",
			fixture: "requirements.md",
			check: func(t *testing.T, doc planning.Document) {
				if doc.Kind != planning.FileRequirements || doc.Requirements == nil {
					t.Fatalf("got %+v", doc)
				}
				tables := doc.Requirements.Requirements
				if len(tables) != 2 || tables[0].ID != "PAY-12" || tables[1].Text != "A retry budget bounds every call" {
					t.Errorf("requirements %+v", tables)
				}
			},
		},
		{name: "one requirement twice", fixture: "repeated-requirement.md", wantErr: planning.ErrBadFile},
		{name: "a goal with no title", fixture: "untitled-goal.md", wantErr: planning.ErrBadFile},
		{name: "an unknown kind", fixture: "unknown-kind.md", wantErr: planning.ErrUnknownKind},
		{name: "bad TOML", fixture: "bad-toml.md", wantErr: planning.ErrBadFile},
		{name: "a front matter with no end", fixture: "unclosed.md", wantErr: planning.ErrBadFile},
		{name: "a size the format does not name", fixture: "bad-size.md", wantErr: planning.ErrBadFile},
		{name: "one work order twice", fixture: "repeated-order.md", wantErr: planning.ErrBadFile},
		{name: "a plain document", fixture: "no-front-matter.md", wantErr: planning.ErrNotOurs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := ParseFile(readFixture(t, tt.fixture))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("got %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, doc)
		})
	}
}
