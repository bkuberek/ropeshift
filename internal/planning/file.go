package planning

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
)

// FileKind is the `kind` key of a repository file.
type FileKind string

// The kinds of repository file.
const (
	FileGoal         FileKind = "goal"
	FilePlan         FileKind = "plan"
	FileContract     FileKind = "contract"
	FileRequirements FileKind = "requirements"
)

// The sizes a contract can declare (D27).
const (
	SizeSmall   = "small"
	SizeMedium  = "medium"
	SizeLarge   = "large"
	SizeUnknown = "unknown"
)

// CurrentRepository is the `repo` value that names the repository holding the file.
const CurrentRepository = "."

// FormatExample is a whole contract file, which is what a planning lane is shown. A test parses it,
// so what an agent is told is what intake reads.
const FormatExample = `+++
kind = "contract"
id = "checkout-retry"
title = "Retry failed checkout calls"
requirements = []
decisions = []

[[criteria]]
id = "c1"
text = "A failed call retries twice with the same idempotency key"

[[work_orders]]
id = "retry-budget"
title = "Add the retry budget"
repo = "."
priority = 2
size = "medium"
depends_on = []
needs = { driver = "claude-code" }
+++

The statement of what this work must deliver.
`

// File errors. A planning agent writes these files, so each is reported and none
// stops intake (ADR-0013).
var (
	// ErrNotOurs means the file has no front matter, so it is ordinary prose.
	ErrNotOurs = errors.New("not a ropeshift file")
	// ErrUnknownKind means the `kind` key names no kind of the format.
	ErrUnknownKind = errors.New("unknown file kind")
	// ErrBadFile means the file is ours and cannot be read.
	ErrBadFile = errors.New("bad repository file")
)

// Criterion is one acceptance criterion.
type Criterion struct {
	ID   string `toml:"id" json:"id"`
	Text string `toml:"text" json:"text"`
}

// WorkOrderSpec is one `[[work_orders]]` table.
type WorkOrderSpec struct {
	ID        string            `toml:"id" json:"id"`
	Title     string            `toml:"title" json:"title"`
	Repo      string            `toml:"repo" json:"repo"`
	Priority  int64             `toml:"priority" json:"priority"`
	Size      string            `toml:"size" json:"size"`
	DependsOn []string          `toml:"depends_on" json:"depends_on,omitempty"`
	Needs     map[string]string `toml:"needs" json:"needs,omitempty"`
}

// ContractFile is the front matter of a contract file.
type ContractFile struct {
	Kind         FileKind        `toml:"kind" json:"kind"`
	ID           string          `toml:"id" json:"id"`
	Title        string          `toml:"title" json:"title"`
	Plan         string          `toml:"plan" json:"plan,omitempty"`
	Requirements []string        `toml:"requirements" json:"requirements,omitempty"`
	Decisions    []string        `toml:"decisions" json:"decisions,omitempty"`
	Criteria     []Criterion     `toml:"criteria" json:"criteria,omitempty"`
	WorkOrders   []WorkOrderSpec `toml:"work_orders" json:"work_orders,omitempty"`
}

// GoalFile is the front matter of a goal file.
type GoalFile struct {
	Kind            FileKind `toml:"kind" json:"kind"`
	ID              string   `toml:"id" json:"id"`
	Title           string   `toml:"title" json:"title"`
	SuccessMeasures []string `toml:"success_measures" json:"success_measures,omitempty"`
}

// PlanFile is the front matter of a plan file. Goal is the ID of a goal file.
type PlanFile struct {
	Kind  FileKind `toml:"kind" json:"kind"`
	ID    string   `toml:"id" json:"id"`
	Title string   `toml:"title" json:"title"`
	Goal  string   `toml:"goal" json:"goal,omitempty"`
}

// RequirementSpec is one `[[requirement]]` table. Its ID is the key a contract names.
type RequirementSpec struct {
	ID   string `toml:"id" json:"id"`
	Text string `toml:"text" json:"text"`
}

// RequirementsFile is the front matter of a requirements file. Requirements live in
// tables, so the Markdown body stays free prose.
type RequirementsFile struct {
	Kind         FileKind          `toml:"kind" json:"kind"`
	ID           string            `toml:"id" json:"id"`
	Title        string            `toml:"title" json:"title"`
	Requirements []RequirementSpec `toml:"requirement" json:"requirement,omitempty"`
}

// Document is one decoded repository file. The field of its kind is set.
type Document struct {
	Kind         FileKind
	Contract     *ContractFile
	Goal         *GoalFile
	Plan         *PlanFile
	Requirements *RequirementsFile
	Statement    string
}

var frontMatterFence = []byte("+++")

// SplitFrontMatter cuts a Markdown file at its `+++` lines. A file whose first line
// is anything else is ErrNotOurs.
func SplitFrontMatter(raw []byte) (header, body []byte, err error) {
	first, rest, found := bytes.Cut(raw, []byte("\n"))
	if !found || !bytes.Equal(bytes.TrimRight(first, "\r"), frontMatterFence) {
		return nil, nil, ErrNotOurs
	}
	offset := 0
	for offset <= len(rest) {
		line, after, more := bytes.Cut(rest[offset:], []byte("\n"))
		if bytes.Equal(bytes.TrimRight(line, "\r"), frontMatterFence) {
			return rest[:offset], bytes.TrimLeft(after, "\r\n"), nil
		}
		if !more {
			break
		}
		offset += len(line) + 1
	}
	return nil, nil, fmt.Errorf("%w: the front matter has no closing line", ErrBadFile)
}

// CheckKind accepts the kinds of the format.
func CheckKind(kind FileKind) error {
	switch kind {
	case FileGoal, FilePlan, FileContract, FileRequirements:
		return nil
	}
	return fmt.Errorf("%w: %q", ErrUnknownKind, kind)
}

// CheckSize accepts a declared size, and an absent one records unknown (D27).
func CheckSize(size string) (string, error) {
	switch size {
	case SizeSmall, SizeMedium, SizeLarge, SizeUnknown:
		return size, nil
	case "":
		return SizeUnknown, nil
	}
	return "", fmt.Errorf("size %q", size)
}

// Normalize refuses a goal file that cannot become a record.
func (f GoalFile) Normalize() (GoalFile, error) {
	return f, needsIdentity(f.ID, f.Title)
}

// Normalize refuses a plan file that cannot become a record.
func (f PlanFile) Normalize() (PlanFile, error) {
	return f, needsIdentity(f.ID, f.Title)
}

// Normalize refuses a requirements file that cannot become records. An ID appears once,
// because it is the key a contract names.
func (f RequirementsFile) Normalize() (RequirementsFile, error) {
	if f.ID == "" {
		return f, fmt.Errorf("%w: no id", ErrBadFile)
	}
	seen := map[string]bool{}
	for _, requirement := range f.Requirements {
		if requirement.ID == "" || requirement.Text == "" || seen[requirement.ID] {
			return f, fmt.Errorf("%w: requirement %q needs a unique id and text", ErrBadFile, requirement.ID)
		}
		seen[requirement.ID] = true
	}
	return f, nil
}

func needsIdentity(id, title string) error {
	if id == "" {
		return fmt.Errorf("%w: no id", ErrBadFile)
	}
	if title == "" {
		return fmt.Errorf("%w: %s has no title", ErrBadFile, id)
	}
	return nil
}

// Normalize fills the defaults of a contract file and refuses one that cannot
// become records. A contract with no criteria is valid here, because the
// definition of ready is what refuses it.
func (f ContractFile) Normalize() (ContractFile, error) {
	if f.ID == "" {
		return f, fmt.Errorf("%w: no id", ErrBadFile)
	}
	if f.Title == "" {
		return f, fmt.Errorf("%w: %s has no title", ErrBadFile, f.ID)
	}
	seen := map[string]bool{}
	for _, c := range f.Criteria {
		if c.ID == "" || c.Text == "" || seen["c:"+c.ID] {
			return f, fmt.Errorf("%w: criterion %q needs a unique id and text", ErrBadFile, c.ID)
		}
		seen["c:"+c.ID] = true
	}
	orders := slices.Clone(f.WorkOrders)
	for i, order := range orders {
		if order.ID == "" || order.Title == "" {
			return f, fmt.Errorf("%w: a work order needs an id and a title", ErrBadFile)
		}
		if seen["w:"+order.ID] {
			return f, fmt.Errorf("%w: work order %q appears twice", ErrBadFile, order.ID)
		}
		seen["w:"+order.ID] = true
		if slices.Contains(order.DependsOn, order.ID) {
			return f, fmt.Errorf("%w: work order %q depends on itself", ErrBadFile, order.ID)
		}
		size, err := CheckSize(order.Size)
		if err != nil {
			return f, fmt.Errorf("%w: work order %q: %v", ErrBadFile, order.ID, err)
		}
		orders[i].Size = size
		if order.Repo == "" {
			orders[i].Repo = CurrentRepository
		}
	}
	f.WorkOrders = orders
	return f, nil
}
