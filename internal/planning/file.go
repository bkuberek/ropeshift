package planning

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
)

// FileKind is the `kind` key of a repository file.
type FileKind string

// The kinds of repository file.
const (
	FileGoal             FileKind = "goal"
	FilePlan             FileKind = "plan"
	FileContract         FileKind = "contract"
	FileRequirements     FileKind = "requirements"
	FileSpec             FileKind = "spec"
	FileArchitecture     FileKind = "architecture"
	FileExperienceDesign FileKind = "experience-design"
	FileMilestone        FileKind = "milestone"
)

// documentKinds are the kinds of file that are documents: each has a stable ID, a version the ledger
// numbers for every committed change, and a reference a contract or a plan can cite (ADR-0013).
var documentKinds = []FileKind{FileRequirements, FileSpec, FileArchitecture, FileExperienceDesign, FileMilestone}

// IsDocumentKind says a file of this kind is a document.
func IsDocumentKind(kind FileKind) bool { return slices.Contains(documentKinds, kind) }

// DocumentRef is how a document is cited: its kind and its own ID, as `spec:checkout-api`. It is also
// the document's ID in the ledger.
func DocumentRef(kind FileKind, id string) string { return string(kind) + ":" + id }

// ParseDocumentRef reads a citation back, and reports false for one that names no document kind.
func ParseDocumentRef(ref string) (FileKind, string, bool) {
	kind, id, found := strings.Cut(ref, ":")
	if !found || id == "" || !IsDocumentKind(FileKind(kind)) {
		return "", "", false
	}
	return FileKind(kind), id, true
}

// The sizes a contract can declare (D27).
const (
	SizeSmall   = "small"
	SizeMedium  = "medium"
	SizeLarge   = "large"
	SizeUnknown = "unknown"
)

// CurrentRepository is the `repo` value that names the repository holding the file.
const CurrentRepository = "."

// File errors. A planning agent writes these files, so each is reported and none
// stops intake (ADR-0013).
var (
	// ErrNotOurs means the file has no front matter, so it is ordinary prose.
	ErrNotOurs = errors.New("not a ropeshift file")
	// ErrUnknownKind means the `kind` key names no kind of the format.
	ErrUnknownKind = errors.New("unknown file kind")
	// ErrBadFile means the file is ours and cannot be read.
	ErrBadFile = errors.New("bad repository file")
	// ErrBaseMoved means a file and the ledger's version of it each changed a base the other never
	// saw, so neither can stand as the next version of the other.
	ErrBaseMoved = errors.New("the base moved")
	// ErrNotPlanningRepository means a plan file sits in a repository of a project that plans in another
	// (ADR-0022), so intake reads nothing from it.
	ErrNotPlanningRepository = errors.New("not the planning repository")
)

// Criterion is one acceptance criterion. Requirements lists the requirement IDs it serves.
type Criterion struct {
	ID           string   `toml:"id" json:"id"`
	Text         string   `toml:"text" json:"text"`
	Requirements []string `toml:"requirements" json:"requirements,omitempty"`
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
	// Base is the branch this work starts from. It overrides the repository's base branch (FR-BASE-2).
	Base string `toml:"base" json:"base,omitempty"`
	// Continues names an in-flight branch this work finishes (ADR-0023). Its lane starts from that branch's
	// tip on a branch of its own, and the branch itself never moves.
	Continues string `toml:"continues" json:"continues,omitempty"`
	// Agent names the roster agent this work's lane runs as, which the planning session proposes from the
	// catalog in its context pack and plan approval covers (ADR-0025). Empty leaves it to the roster's
	// default mapping.
	Agent string `toml:"agent" json:"agent,omitempty"`
}

// ContractFile is the front matter of a contract file. Requirements at this level is the key list of
// the files written before a criterion could name its own; a pack reads it together with the
// criteria's. Milestone, Shared, and Uses are kept as written, and Documents cites the documents the
// contract depends on, each as `<kind>:<id>`. Existing says the code already delivers the contract, so it
// has no work orders and every dependency on it is resolved from the start (ADR-0023).
type ContractFile struct {
	Kind         FileKind        `toml:"kind" json:"kind"`
	ID           string          `toml:"id" json:"id"`
	Title        string          `toml:"title" json:"title"`
	Plan         string          `toml:"plan" json:"plan,omitempty"`
	Milestone    string          `toml:"milestone" json:"milestone,omitempty"`
	Shared       bool            `toml:"shared" json:"shared,omitempty"`
	Uses         []string        `toml:"uses" json:"uses,omitempty"`
	Documents    []string        `toml:"documents" json:"documents,omitempty"`
	Requirements []string        `toml:"requirements" json:"requirements,omitempty"`
	Decisions    []string        `toml:"decisions" json:"decisions,omitempty"`
	Existing     bool            `toml:"existing" json:"existing,omitempty"`
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

// DraftDecision is one `[[decision]]` table of a plan: an answer the person gave in conversation. It
// binds nothing until the plan is approved.
type DraftDecision struct {
	ID        string `toml:"id" json:"id"`
	Question  string `toml:"question" json:"question"`
	Answer    string `toml:"answer" json:"answer"`
	Rationale string `toml:"rationale" json:"rationale,omitempty"`
	Blocking  bool   `toml:"blocking" json:"blocking,omitempty"`
}

// LeftOut is one `[[left_out]]` table of a plan: a branch or an issue an adoption chose not to carry, and
// why (ADR-0023). Plan approval shows each one.
type LeftOut struct {
	Ref    string `toml:"ref" json:"ref"`
	Reason string `toml:"reason" json:"reason"`
}

// PlanFile is the front matter of a plan file. Goal is the ID of a goal file, and Documents lists every
// document the plan holds, each as `<kind>:<id>`.
type PlanFile struct {
	Kind      FileKind        `toml:"kind" json:"kind"`
	ID        string          `toml:"id" json:"id"`
	Title     string          `toml:"title" json:"title"`
	Goal      string          `toml:"goal" json:"goal,omitempty"`
	Documents []string        `toml:"documents" json:"documents,omitempty"`
	Decisions []DraftDecision `toml:"decision" json:"decision,omitempty"`
	LeftOut   []LeftOut       `toml:"left_out" json:"left_out,omitempty"`
}

// The statuses of a requirement. An empty status is open.
const (
	RequirementOpen = "open"
	// RequirementMet says the code already meets the requirement, which its evidence shows (ADR-0023).
	RequirementMet = "met"
)

// RequirementSpec is one `[[requirement]]` table. Its ID is the key a contract names. A met requirement
// lists its Evidence as `<repository>:<path>` references, and needs no criterion.
type RequirementSpec struct {
	ID       string   `toml:"id" json:"id"`
	Text     string   `toml:"text" json:"text"`
	Status   string   `toml:"status" json:"status,omitempty"`
	Evidence []string `toml:"evidence" json:"evidence,omitempty"`
}

// Met says the code already meets the requirement.
func (s RequirementSpec) Met() bool { return s.Status == RequirementMet }

// Digest identifies the requirement's content. An open one keeps the digest it had before the format could
// say met, and a met one's covers its evidence too.
func (s RequirementSpec) Digest() string {
	if !s.Met() {
		return RequirementDigest(s.ID, s.Text)
	}
	return ContentDigest(append([]string{"requirement", s.ID, s.Text, RequirementMet}, s.Evidence...)...)
}

// check refuses a status nobody offers, a met requirement with no evidence, and evidence that names no
// repository and a path inside it.
func (s RequirementSpec) check() error {
	switch s.Status {
	case "", RequirementOpen:
		if len(s.Evidence) != 0 {
			return fmt.Errorf("%w: requirement %s is not met, so it cites no evidence", ErrBadFile, s.ID)
		}
		return nil
	case RequirementMet:
	default:
		return fmt.Errorf("%w: requirement %s has status %q, and a status is open or met", ErrBadFile, s.ID, s.Status)
	}
	if len(s.Evidence) == 0 {
		return fmt.Errorf("%w: requirement %s is met and cites no evidence", ErrBadFile, s.ID)
	}
	for _, cited := range s.Evidence {
		repository, file, found := strings.Cut(cited, ":")
		if !found || repository == "" || !insideRepository(file) {
			return fmt.Errorf("%w: requirement %s cites %q, and evidence is written <repository>:<path> with a path inside the repository", ErrBadFile, s.ID, cited)
		}
	}
	return nil
}

// RequirementsFile is the front matter of a requirements file. Requirements live in
// tables, so the Markdown body stays free prose.
type RequirementsFile struct {
	Kind         FileKind          `toml:"kind" json:"kind"`
	ID           string            `toml:"id" json:"id"`
	Title        string            `toml:"title" json:"title"`
	Plan         string            `toml:"plan" json:"plan,omitempty"`
	Requirements []RequirementSpec `toml:"requirement" json:"requirement,omitempty"`
}

// SpecFile is the front matter of a technical spec. Requirements lists the requirement IDs it addresses.
type SpecFile struct {
	Kind         FileKind `toml:"kind" json:"kind"`
	ID           string   `toml:"id" json:"id"`
	Title        string   `toml:"title" json:"title"`
	Plan         string   `toml:"plan" json:"plan,omitempty"`
	Requirements []string `toml:"requirements" json:"requirements,omitempty"`
}

// ArchitectureFile is the front matter of an architecture document.
type ArchitectureFile struct {
	Kind         FileKind `toml:"kind" json:"kind"`
	ID           string   `toml:"id" json:"id"`
	Title        string   `toml:"title" json:"title"`
	Plan         string   `toml:"plan" json:"plan,omitempty"`
	Requirements []string `toml:"requirements" json:"requirements,omitempty"`
}

// ExperienceDesignFile is the front matter of an experience design. Assets lists repository paths to
// the images or other files the design shows.
type ExperienceDesignFile struct {
	Kind         FileKind `toml:"kind" json:"kind"`
	ID           string   `toml:"id" json:"id"`
	Title        string   `toml:"title" json:"title"`
	Plan         string   `toml:"plan" json:"plan,omitempty"`
	Requirements []string `toml:"requirements" json:"requirements,omitempty"`
	Assets       []string `toml:"assets" json:"assets,omitempty"`
}

// ExitCriterion is one `[[exit_criteria]]` table of a milestone.
type ExitCriterion struct {
	ID   string `toml:"id" json:"id"`
	Text string `toml:"text" json:"text"`
}

// MilestoneFile is the front matter of a milestone. A lower Priority comes first, and DependsOn lists
// milestone IDs. Existing says the code already delivers the milestone, so every dependency on it is
// resolved from the start (ADR-0023).
type MilestoneFile struct {
	Kind         FileKind        `toml:"kind" json:"kind"`
	ID           string          `toml:"id" json:"id"`
	Title        string          `toml:"title" json:"title"`
	Plan         string          `toml:"plan" json:"plan,omitempty"`
	Priority     int64           `toml:"priority" json:"priority"`
	DependsOn    []string        `toml:"depends_on" json:"depends_on,omitempty"`
	Existing     bool            `toml:"existing" json:"existing,omitempty"`
	ExitCriteria []ExitCriterion `toml:"exit_criteria" json:"exit_criteria,omitempty"`
}

// Document is one decoded repository file. The field of its kind is set.
type Document struct {
	Kind         FileKind
	Contract     *ContractFile
	Goal         *GoalFile
	Plan         *PlanFile
	Requirements *RequirementsFile
	Spec         *SpecFile
	Architecture *ArchitectureFile
	Experience   *ExperienceDesignFile
	Milestone    *MilestoneFile
	Statement    string
}

// DocumentHead is what the ledger keeps of a file that is a document, whatever its kind. Fields is
// the front matter as JSON, so a version holds what the kind says beyond a title and a plan.
type DocumentHead struct {
	Kind   FileKind
	ID     string
	Title  string
	Plan   string
	Fields json.RawMessage
}

// Ref is the document's reference, which is also its ID in the ledger.
func (h DocumentHead) Ref() string { return DocumentRef(h.Kind, h.ID) }

// Name is what a person calls the document: its title, or else its ID, because a requirements file
// has never needed a title.
func (h DocumentHead) Name() string {
	if h.Title == "" {
		return h.ID
	}
	return h.Title
}

// Head reads the part of a document file every document kind shares, and reports false for a file that
// is not a document.
func (d Document) Head() (DocumentHead, bool) {
	var head DocumentHead
	var file any
	switch {
	case d.Requirements != nil:
		f := d.Requirements
		head, file = DocumentHead{Kind: FileRequirements, ID: f.ID, Title: f.Title, Plan: f.Plan}, f
	case d.Spec != nil:
		f := d.Spec
		head, file = DocumentHead{Kind: FileSpec, ID: f.ID, Title: f.Title, Plan: f.Plan}, f
	case d.Architecture != nil:
		f := d.Architecture
		head, file = DocumentHead{Kind: FileArchitecture, ID: f.ID, Title: f.Title, Plan: f.Plan}, f
	case d.Experience != nil:
		f := d.Experience
		head, file = DocumentHead{Kind: FileExperienceDesign, ID: f.ID, Title: f.Title, Plan: f.Plan}, f
	case d.Milestone != nil:
		f := d.Milestone
		head, file = DocumentHead{Kind: FileMilestone, ID: f.ID, Title: f.Title, Plan: f.Plan}, f
	default:
		return DocumentHead{}, false
	}
	fields, err := json.Marshal(file)
	if err != nil {
		return DocumentHead{}, false
	}
	head.Fields = fields
	return head, true
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
	case FileGoal, FilePlan, FileContract, FileRequirements, FileSpec, FileArchitecture, FileExperienceDesign, FileMilestone:
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

// Normalize refuses a plan file that cannot become a record. Each document it lists is cited by kind
// and ID, and each draft decision carries the question and the answer the person gave.
func (f PlanFile) Normalize() (PlanFile, error) {
	if err := needsIdentity(f.ID, f.Title); err != nil {
		return f, err
	}
	documents, err := cleanRefs(f.ID, "documents", f.Documents)
	if err != nil {
		return f, err
	}
	f.Documents = documents
	seen := map[string]bool{}
	for _, d := range f.Decisions {
		if d.ID == "" || d.Question == "" || d.Answer == "" || seen[d.ID] {
			return f, fmt.Errorf("%w: draft decision %q needs a unique id, a question, and an answer", ErrBadFile, d.ID)
		}
		seen[d.ID] = true
	}
	left := map[string]bool{}
	for _, out := range f.LeftOut {
		if out.Ref == "" || out.Reason == "" || left[out.Ref] {
			return f, fmt.Errorf("%w: what the plan left out, %q, needs a unique ref and a reason", ErrBadFile, out.Ref)
		}
		left[out.Ref] = true
	}
	return f, nil
}

// Normalize refuses a spec that cannot become a document.
func (f SpecFile) Normalize() (SpecFile, error) {
	requirements, err := documentIdentity(f.ID, f.Title, f.Requirements)
	f.Requirements = requirements
	return f, err
}

// Normalize refuses an architecture document that cannot become a document.
func (f ArchitectureFile) Normalize() (ArchitectureFile, error) {
	requirements, err := documentIdentity(f.ID, f.Title, f.Requirements)
	f.Requirements = requirements
	return f, err
}

// Normalize refuses an experience design that cannot become a document. An asset is a path inside the
// repository, because the design shows what the repository holds.
func (f ExperienceDesignFile) Normalize() (ExperienceDesignFile, error) {
	requirements, err := documentIdentity(f.ID, f.Title, f.Requirements)
	f.Requirements = requirements
	if err != nil {
		return f, err
	}
	for _, asset := range f.Assets {
		if !insideRepository(asset) {
			return f, fmt.Errorf("%w: %s: asset %q is not a path inside the repository", ErrBadFile, f.ID, asset)
		}
	}
	f.Assets = cleanList(f.Assets)
	return f, nil
}

// Normalize refuses a milestone that cannot become a document. It cannot depend on itself, and a
// cycle across milestones is for the plan to report.
func (f MilestoneFile) Normalize() (MilestoneFile, error) {
	if err := needsIdentity(f.ID, f.Title); err != nil {
		return f, err
	}
	dependsOn, err := cleanIDs(f.ID, "depends_on", f.DependsOn)
	if err != nil {
		return f, err
	}
	if slices.Contains(dependsOn, f.ID) {
		return f, fmt.Errorf("%w: milestone %s depends on itself", ErrBadFile, f.ID)
	}
	f.DependsOn = dependsOn
	seen := map[string]bool{}
	for _, c := range f.ExitCriteria {
		if c.ID == "" || c.Text == "" || seen[c.ID] {
			return f, fmt.Errorf("%w: exit criterion %q needs a unique id and text", ErrBadFile, c.ID)
		}
		seen[c.ID] = true
	}
	return f, nil
}

// insideRepository says a path is relative and never leaves the repository.
func insideRepository(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\\x00") || path.IsAbs(name) {
		return false
	}
	return !slices.Contains(strings.Split(path.Clean(name), "/"), "..") && path.Clean(name) != "."
}

// documentIdentity checks what every document that addresses requirements shares.
func documentIdentity(id, title string, requirements []string) ([]string, error) {
	if err := needsIdentity(id, title); err != nil {
		return requirements, err
	}
	return cleanIDs(id, "requirements", requirements)
}

// cleanIDs refuses an empty entry of a list of IDs and drops a repeated one, keeping the order.
func cleanIDs(owner, key string, ids []string) ([]string, error) {
	if slices.Contains(ids, "") {
		return nil, fmt.Errorf("%w: %s: %s holds an empty entry", ErrBadFile, owner, key)
	}
	return cleanList(ids), nil
}

// cleanRefs does the same for citations, which also name a document kind.
func cleanRefs(owner, key string, refs []string) ([]string, error) {
	for _, ref := range refs {
		if _, _, ok := ParseDocumentRef(ref); !ok {
			return nil, fmt.Errorf("%w: %s: %s entry %q is not a document, written <kind>:<id> with a kind of %s",
				ErrBadFile, owner, key, ref, documentKindNames())
		}
	}
	return cleanList(refs), nil
}

func documentKindNames() string {
	names := make([]string, len(documentKinds))
	for i, kind := range documentKinds {
		names[i] = string(kind)
	}
	return strings.Join(names, ", ")
}

// cleanList drops a repeated entry and keeps the order. A list with nothing in it is nil.
func cleanList(items []string) []string {
	var out []string
	for _, item := range items {
		if !slices.Contains(out, item) {
			out = append(out, item)
		}
	}
	return out
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
		if err := requirement.check(); err != nil {
			return f, err
		}
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
	criteria := slices.Clone(f.Criteria)
	for i, c := range criteria {
		if c.ID == "" || c.Text == "" || seen["c:"+c.ID] {
			return f, fmt.Errorf("%w: criterion %q needs a unique id and text", ErrBadFile, c.ID)
		}
		seen["c:"+c.ID] = true
		requirements, err := cleanIDs(f.ID, "criterion "+c.ID+" requirements", c.Requirements)
		if err != nil {
			return f, err
		}
		criteria[i].Requirements = requirements
	}
	f.Criteria = criteria
	documents, err := cleanRefs(f.ID, "documents", f.Documents)
	if err != nil {
		return f, err
	}
	f.Documents = documents
	uses, err := cleanIDs(f.ID, "uses", f.Uses)
	if err != nil {
		return f, err
	}
	if slices.Contains(uses, f.ID) {
		return f, fmt.Errorf("%w: contract %s uses itself", ErrBadFile, f.ID)
	}
	f.Uses = uses
	if f.Existing && len(f.WorkOrders) > 0 {
		return f, fmt.Errorf("%w: contract %s exists already and lists work orders; an existing contract records work the code delivers, and has none", ErrBadFile, f.ID)
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
		if order.Base != "" {
			if err := ledger.CheckBranch(order.Base); err != nil {
				return f, fmt.Errorf("%w: work order %q: %v", ErrBadFile, order.ID, err)
			}
		}
		if order.Continues != "" {
			if err := ledger.CheckBranch(order.Continues); err != nil {
				return f, fmt.Errorf("%w: work order %q continues: %v", ErrBadFile, order.ID, err)
			}
			if order.Continues == order.Base {
				return f, fmt.Errorf("%w: work order %q continues %s, which is its own base; it continues a branch that merges into its base", ErrBadFile, order.ID, order.Base)
			}
		}
		if order.Repo == "" {
			orders[i].Repo = CurrentRepository
		}
	}
	f.WorkOrders = orders
	return f, nil
}
