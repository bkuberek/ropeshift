package planning

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// LayoutShape is the shape `ropeshift adopt` proposes for what it detects around a clone (FR-ADOPT-12).
type LayoutShape string

// The shapes a layout is proposed as.
const (
	// ShapeSingle is a clone with no other clone found.
	ShapeSingle LayoutShape = "single"
	// ShapePlanned is a clone that holds plan content, with clones found.
	ShapePlanned LayoutShape = "planned"
	// ShapeConsolidate is a workspace with no plan content whose ignored folder holds clones, one of them the
	// design repository.
	ShapeConsolidate LayoutShape = "consolidate"
	// ShapeWorkspaceFiles is any other layout with clones.
	ShapeWorkspaceFiles LayoutShape = "workspace_files"
)

// Valid says a shape is one of the four.
func (s LayoutShape) Valid() bool {
	switch s {
	case ShapeSingle, ShapePlanned, ShapeConsolidate, ShapeWorkspaceFiles:
		return true
	}
	return false
}

// Restructures says `--restructure` offers something in the shape.
func (s LayoutShape) Restructures() bool { return s.Valid() && s != ShapeSingle }

// Where a clone was found, relative to the clone adopt runs in.
const (
	WhereHere    = "here"
	WhereIgnored = "ignored"
	WherePath    = "path"
)

// LayoutClone is one clone the detection found, with what it holds.
type LayoutClone struct {
	// Name is the clone's folder name, which `--design` and `--planning` name it by.
	Name string `json:"name"`
	Path string `json:"path"`
	// Identity is the clone's first-parent root, which names its repository (FR-REPO-14).
	Identity string `json:"identity"`
	// Where says how it was found: WhereHere for the clone adopt runs in, WhereIgnored for one in a folder that
	// clone ignores, and WherePath for one a path named.
	Where string `json:"where"`
	// Plan says the clone holds plan content.
	Plan bool `json:"plan,omitempty"`
	// Documents counts the Markdown documents it commits.
	Documents int `json:"documents"`
	// Instructions says it holds an AGENTS.md or CLAUDE.md at its top.
	Instructions bool `json:"instructions,omitempty"`
}

// DetectedLayout is what the detection read.
type DetectedLayout struct {
	// Root is the top of the clone adopt runs in.
	Root string `json:"root"`
	// Folders are the directories the root ignores that hold clones, as absolute paths in order.
	Folders []string `json:"folders"`
	// Clones are the clones found, the root among them, in order of path.
	Clones []LayoutClone `json:"clones"`
}

// PlanningRepository is where the plan lives, and why.
type PlanningRepository struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
	Why  string `json:"why"`
}

// DesignRepository is the repository that holds the project's design, and why it is the one.
type DesignRepository struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Root is its first-parent root, which a consolidation checks the history it imports against.
	Root string `json:"root"`
	Why  string `json:"why"`
}

// OutcomeNotRegistered is the outcome of a repository the plan leaves out of the registration, as the design
// repository is when its history is imported. The other outcomes are those a registration gives.
const OutcomeNotRegistered = "not_registered"

// PlannedRepository is a clone or a path the adoption would register, with the outcome a registration's dry run
// gives it.
type PlannedRepository struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path"`
	// ID is the record's identity, when the registration gives one.
	ID      string `json:"id,omitempty"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

// What a planned file does to a file of the planning repository.
const (
	ActionAdd    = "add"
	ActionAppend = "append"
)

// PlannedFile is a file a restructuring would add, or a line it would append, in the planning repository.
type PlannedFile struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
}

// What a collision keeps.
const (
	KeptWorkspace = "workspace"
	KeptUnion     = "union"
)

// Collision is a path both repositories hold, and the version a consolidation keeps.
type Collision struct {
	Path string `json:"path"`
	Kept string `json:"kept"`
}

// PlannedCodeTree is a code tree of the design repository, with the build file that makes it one.
type PlannedCodeTree struct {
	Path      string `json:"path"`
	BuildFile string `json:"build_file"`
	// Whole says the build file is at the top, so the whole repository is the tree and none of it can be left out.
	Whole bool `json:"whole,omitempty"`
	// LeftOut says a consolidation removes the tree in its merge commit.
	LeftOut bool `json:"left_out,omitempty"`
}

// Move is a clone the person moves into the planning repository's folder of clones, as the commands that do it.
// Ropeshift never runs them.
type Move struct {
	Name     string   `json:"name"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Commands []string `json:"commands"`
}

// AdoptionPlan is what `ropeshift adopt --dry-run` reports: the layout detected around a clone, the shape
// proposed for it, and every change and move the adoption would make. Building one records nothing.
type AdoptionPlan struct {
	Layout   DetectedLayout     `json:"layout"`
	Shape    LayoutShape        `json:"shape"`
	Planning PlanningRepository `json:"planning"`
	// Design is the design repository, when the layout has one.
	Design       *DesignRepository   `json:"design,omitempty"`
	Repositories []PlannedRepository `json:"repositories"`
	// Files are the files a restructuring adds, each only when the planning repository lacks it.
	Files []PlannedFile `json:"files"`
	// Takes are the paths a consolidation takes from the design repository.
	Takes      []string          `json:"takes"`
	Collisions []Collision       `json:"collisions"`
	CodeTrees  []PlannedCodeTree `json:"code_trees"`
	Moves      []Move            `json:"moves"`
	// Notes say what the detection could not settle, with the flag that settles it.
	Notes []string `json:"notes,omitempty"`
	// Digest is the SHA-256 of the plan's JSON with no digest, and of what a registration said of each
	// repository, which the registration itself changes. A restructuring sends it back, so a layout that changed
	// in between is found.
	Digest string `json:"digest,omitempty"`
}

// ComputeDigest is the digest of the plan as it stands.
func (p AdoptionPlan) ComputeDigest() string {
	p.Digest = ""
	repositories := make([]PlannedRepository, 0, len(p.Repositories))
	for _, repository := range p.Repositories {
		kept := PlannedRepository{Name: repository.Name, Path: repository.Path}
		if repository.Outcome == OutcomeNotRegistered {
			kept.Outcome = OutcomeNotRegistered
		}
		repositories = append(repositories, kept)
	}
	p.Repositories = repositories
	raw, err := json.Marshal(p)
	if err != nil {
		// A plan holds only strings, numbers, and lists, which always encode.
		panic(err)
	}
	sum := sha256.Sum256(raw)
	return DigestSHA256 + hex.EncodeToString(sum[:])
}

// WithDigest returns the plan with its digest.
func (p AdoptionPlan) WithDigest() AdoptionPlan {
	p.Digest = p.ComputeDigest()
	return p
}
