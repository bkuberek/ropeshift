package planning_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
)

func TestReadMarkdownNamesItsFrontMatterAndHeadings(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		format   string
		headings []planning.Heading
	}{
		{
			name:     "TOML front matter, whose lines are not headings",
			text:     "+++\nkind = \"spec\"\n# not a heading\n+++\n\n# Checkout API\n\n## Errors ##\n",
			format:   planning.FrontMatterTOML,
			headings: []planning.Heading{{Level: 1, Text: "Checkout API"}, {Level: 2, Text: "Errors"}},
		},
		{
			name:     "YAML front matter",
			text:     "---\ntitle: Notes\n---\n\nIntro\n=====\n\nMore\n----\n",
			format:   planning.FrontMatterYAML,
			headings: []planning.Heading{{Level: 1, Text: "Intro"}, {Level: 2, Text: "More"}},
		},
		{
			name:     "no front matter, and code fences hide what looks like a heading",
			text:     "# Readme\n\n```sh\n# a comment\n```\n\n~~~\n## also code\n~~~\n\n###### Deep\n####### too deep\n#nospace\n",
			format:   planning.FrontMatterNone,
			headings: []planning.Heading{{Level: 1, Text: "Readme"}, {Level: 6, Text: "Deep"}},
		},
		{
			name:   "an empty file",
			text:   "",
			format: planning.FrontMatterNone,
		},
		{
			name:     "carriage returns",
			text:     "+++\r\nkind = \"plan\"\r\n+++\r\n# Plan\r\n",
			format:   planning.FrontMatterTOML,
			headings: []planning.Heading{{Level: 1, Text: "Plan"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format, headings := planning.ReadMarkdown([]byte(tt.text))
			if format != tt.format || !slices.Equal(headings, tt.headings) {
				t.Errorf("read %q with %+v, want %q with %+v", format, headings, tt.format, tt.headings)
			}
		})
	}
}

// ADR-0023: the inventory is the same bytes for the same facts, whatever order a reader found them in, and
// it says what it leaves out.
func TestAnInventoryRendersTheSameBytesForTheSameFacts(t *testing.T) {
	repository := func(reversed bool) planning.RepositoryInventory {
		r := planning.RepositoryInventory{
			Name: "api", Repository: "id-api", BaseBranch: "main", BaseCommit: "c1",
			Documents: []planning.InventoryDocument{
				{Path: "b.md", Digest: "gitblob:2", FrontMatter: planning.FrontMatterNone},
				{Path: "a.md", Digest: "gitblob:1", FrontMatter: planning.FrontMatterTOML},
			},
			Files: []planning.InventoryFile{{Path: "b.md", Kind: "spec", ID: "b", Clean: true}, {Path: "a.md", Problem: "bad"}},
			Branches: []planning.InventoryBranch{
				{Name: "z", Ref: "refs/heads/z", Tip: "t2"}, {Name: "origin/a", Ref: "refs/remotes/origin/a", Tip: "t1"},
			},
		}
		if reversed {
			slices.Reverse(r.Documents)
			slices.Reverse(r.Files)
			slices.Reverse(r.Branches)
		}
		return r
	}
	first, err := planning.RenderInventory(planning.Inventory{Repositories: []planning.RepositoryInventory{repository(false), {Name: "web", Repository: "id-web"}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := planning.RenderInventory(planning.Inventory{Repositories: []planning.RepositoryInventory{{Name: "web", Repository: "id-web"}, repository(true)}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("the same facts rendered two ways:\n%s\n%s", first, second)
	}
	var read planning.Inventory
	if err := json.Unmarshal(first, &read); err != nil {
		t.Fatal(err)
	}
	if read.Repositories[0].Name != "api" || read.Repositories[0].Documents[0].Path != "a.md" || read.Repositories[0].Branches[0].Ref != "refs/heads/z" {
		t.Errorf("the inventory is not in its order: %+v", read.Repositories[0])
	}
	if !strings.HasSuffix(string(first), "}\n") || read.Version != planning.InventoryVersion {
		t.Errorf("the inventory reads version %d and ends %q", read.Version, first[len(first)-3:])
	}
	parts := map[string]bool{}
	for _, omitted := range read.Omitted {
		parts[omitted.Part] = omitted.Reason != ""
	}
	if !parts["pull_requests"] || !parts["issues"] {
		t.Errorf("the inventory does not say what it leaves out: %+v", read.Omitted)
	}
}
