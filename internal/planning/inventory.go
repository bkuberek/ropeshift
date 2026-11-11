package planning

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"
)

// InventoryVersion is the version of the inventory's shape, which a reader checks before it reads one.
const InventoryVersion = 1

// The front matter a Markdown file opens with.
const (
	FrontMatterTOML = "toml"
	FrontMatterYAML = "yaml"
	FrontMatterNone = "none"
)

// Inventory is what an adopt lane is given of each repository of its project before it starts (ADR-0023):
// what each one is, the Markdown it holds, the files already in the repository file format, and the work in
// progress on its branches. No model writes it, so two inventories taken at the same commits are the same
// bytes. Omitted says what it does not read yet, and why.
type Inventory struct {
	Version      int                   `json:"version"`
	Repositories []RepositoryInventory `json:"repositories"`
	Omitted      []InventoryOmission   `json:"omitted"`
}

// RepositoryInventory is one repository at one commit of its base branch. Detail says why one could not be
// read, with nothing else known of it.
type RepositoryInventory struct {
	Name       string `json:"name"`
	Repository string `json:"repository"`
	Remote     string `json:"remote,omitempty"`
	// DefaultBranch is the branch the repository's work lands on, and DefaultTip where it stands.
	DefaultBranch string `json:"default_branch,omitempty"`
	DefaultTip    string `json:"default_tip,omitempty"`
	// BaseBranch and BaseCommit are the branch ropeshift starts work from and the commit everything here
	// was read at.
	BaseBranch string              `json:"base_branch"`
	BaseCommit string              `json:"base_commit"`
	Documents  []InventoryDocument `json:"documents"`
	// Files are the documents whose front matter intake reads as the repository file format, and whether it
	// reads each one cleanly.
	Files []InventoryFile `json:"ropeshift_files"`
	// Branches are the branches whose tip the base does not hold: work that has not merged.
	Branches []InventoryBranch `json:"branches"`
	Detail   string            `json:"detail,omitempty"`
}

// InventoryDocument is one Markdown file at the base commit, with the digest of its content as written.
type InventoryDocument struct {
	Path        string    `json:"path"`
	Digest      string    `json:"digest"`
	FrontMatter string    `json:"front_matter"`
	Headings    []Heading `json:"headings"`
}

// Heading is one heading of a Markdown file, with its level.
type Heading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
}

// InventoryFile is one file intake reads as the repository file format. Clean says intake reads it with no
// problem; otherwise Problem says what intake would report.
type InventoryFile struct {
	Path    string   `json:"path"`
	Kind    FileKind `json:"kind,omitempty"`
	ID      string   `json:"id,omitempty"`
	Clean   bool     `json:"clean"`
	Problem string   `json:"problem,omitempty"`
}

// InventoryBranch is one branch that has not merged into the base: where it stands, where it left the base,
// how many commits it holds that the base lacks and the base holds that it lacks, and when its last commit
// was made, in UTC.
type InventoryBranch struct {
	Name       string `json:"name"`
	Ref        string `json:"ref"`
	Tip        string `json:"tip"`
	MergeBase  string `json:"merge_base,omitempty"`
	Ahead      int    `json:"ahead"`
	Behind     int    `json:"behind"`
	LastCommit string `json:"last_commit"`
}

// InventoryOmission is a part of the inventory ADR-0023 names that this one does not hold.
type InventoryOmission struct {
	Part   string `json:"part"`
	Reason string `json:"reason"`
}

// inventoryOmitted are the parts no adapter reads yet: pull requests need a forge, and issues a tracker.
var inventoryOmitted = []InventoryOmission{
	{Part: "pull_requests", Reason: "no forge adapter is built yet, so open pull requests are not read; the branches they come from are listed"},
	{Part: "issues", Reason: "no tracker adapter is built yet, so open issues are not read"},
}

// RenderInventory writes an inventory as indented JSON in one order: repositories by name, then documents and
// files by path, and branches by ref. Every list is present, even empty, so the same facts always give the
// same bytes.
func RenderInventory(inventory Inventory) ([]byte, error) {
	out := Inventory{Version: InventoryVersion, Omitted: slices.Clone(inventoryOmitted), Repositories: []RepositoryInventory{}}
	for _, repository := range inventory.Repositories {
		repository.Documents = sortedBy(repository.Documents, func(d InventoryDocument) string { return d.Path })
		for i := range repository.Documents {
			if repository.Documents[i].Headings == nil {
				repository.Documents[i].Headings = []Heading{}
			}
		}
		repository.Files = sortedBy(repository.Files, func(f InventoryFile) string { return f.Path })
		repository.Branches = sortedBy(repository.Branches, func(b InventoryBranch) string { return b.Ref })
		out.Repositories = append(out.Repositories, repository)
	}
	slices.SortStableFunc(out.Repositories, func(a, b RepositoryInventory) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.Repository, b.Repository))
	})
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// sortedBy copies a list in the order of a key, and gives an empty list for none.
func sortedBy[T any](items []T, key func(T) string) []T {
	out := slices.Clone(items)
	if out == nil {
		out = []T{}
	}
	slices.SortStableFunc(out, func(a, b T) int { return strings.Compare(key(a), key(b)) })
	return out
}

// frontMatters are the fences a Markdown file's front matter opens and closes with.
var frontMatters = []struct{ fence, format string }{{"+++", FrontMatterTOML}, {"---", FrontMatterYAML}}

// ReadMarkdown says what front matter a Markdown file opens with, and lists its headings in order: ATX
// headings of one to six marks, and setext headings underlined with = or -. Lines in the front matter and
// in fenced code are not headings.
func ReadMarkdown(raw []byte) (string, []Heading) {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	format, body := FrontMatterNone, lines
	for _, opens := range frontMatters {
		if strings.TrimSpace(lines[0]) != opens.fence {
			continue
		}
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == opens.fence {
				format, body = opens.format, lines[i+1:]
				break
			}
		}
	}
	var headings []Heading
	var fence string
	previous := ""
	for _, line := range body {
		trimmed := strings.TrimSpace(line)
		switch {
		case fence != "":
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			previous = ""
			continue
		case strings.HasPrefix(trimmed, "```"), strings.HasPrefix(trimmed, "~~~"):
			fence = trimmed[:3]
			previous = ""
			continue
		}
		if heading, ok := atxHeading(trimmed); ok {
			headings = append(headings, heading)
			previous = ""
			continue
		}
		if level := setextLevel(trimmed); level != 0 && previous != "" {
			headings = append(headings, Heading{Level: level, Text: previous})
			previous = ""
			continue
		}
		previous = trimmed
	}
	return format, headings
}

// atxHeading reads a line that opens with one to six marks and a space, with any closing marks dropped.
func atxHeading(line string) (Heading, bool) {
	marks := len(line) - len(strings.TrimLeft(line, "#"))
	if marks < 1 || marks > 6 {
		return Heading{}, false
	}
	rest := line[marks:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return Heading{}, false
	}
	text := strings.TrimSpace(rest)
	if trimmed := strings.TrimRight(text, "#"); trimmed != text && (trimmed == "" || strings.HasSuffix(trimmed, " ")) {
		text = strings.TrimSpace(trimmed)
	}
	return Heading{Level: marks, Text: text}, text != ""
}

// setextLevel reads an underline: = makes a first level heading of the line above, and - a second.
func setextLevel(line string) int {
	switch {
	case line == "":
		return 0
	case strings.Trim(line, "=") == "":
		return 1
	case strings.Trim(line, "-") == "" && len(line) >= 2:
		return 2
	}
	return 0
}
