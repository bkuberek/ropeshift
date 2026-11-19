package planning

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/practices"
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

// Restructures says `--restructure` offers something in every plan of the shape. The single shape offers it
// only for the files its plan lists, which AdoptionPlan.Offers reads.
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
	// Undecided says nothing in the layout settles where the plan lives, so a member names it with --planning:
	// the clone adopt runs in holds no plan content and has no folder of clones, and no design repository was
	// found, or two tied for it.
	Undecided bool `json:"undecided,omitempty"`
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
	// Practices is the practices check of the clone at the base branch a registration would record, as a dry run
	// reads it: no forge binding, the whole history, and nothing recorded (FR-PRAC-6). It is nil for a dry run
	// that asked for none and for a clone the check could not read, and PracticesNote then says why. Neither
	// is part of the plan's digest, so a finding never refuses a restructuring.
	Practices     *practices.Result `json:"practices,omitempty"`
	PracticesNote string            `json:"practices_note,omitempty"`
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
	// Lines are the lines an append adds to the file, each only when the file holds no line like it.
	Lines []string `json:"lines,omitempty"`
	// Template names the template a file the layout lacks is written from, and is empty for a file the worker
	// builds from the plan.
	Template string `json:"template,omitempty"`
}

// What a collision keeps.
const (
	KeptWorkspace = "workspace"
	KeptUnion     = "union"
)

// Collision is a path both repositories hold, and the version a consolidation keeps. Path is the workspace's
// spelling. Two repositories hold one path when their spellings differ only in case, and then DesignPath is the
// design repository's spelling, the one its version is read under at the second parent.
type Collision struct {
	Path       string `json:"path"`
	DesignPath string `json:"design_path,omitempty"`
	Kept       string `json:"kept"`
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

// Step is a change ropeshift leaves to the person, in a clone it never writes to (FR-ADOPT-3): the repository it
// concerns, its clone, and the commands that make the change.
type Step struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Commands []string `json:"commands"`
}

// Omission is a repository a file the restructuring writes leaves out, and why. The manifest still lists it.
type Omission struct {
	Name string `json:"name"`
	// Path is where its clone sits relative to the planning repository's clone, once any move is made.
	Path   string `json:"path"`
	Reason string `json:"reason"`
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
	// Steps are what the person does in each code repository that holds no instruction file.
	Steps []Step `json:"steps,omitempty"`
	// MakefileLeftOut are the repositories the Makefile the plan writes leaves out, because its name or its path holds
	// what make or a shell reads as syntax. They follow from the repositories and the moves, so the digest leaves them
	// out, as it leaves out the notes.
	MakefileLeftOut []Omission `json:"makefile_left_out,omitempty"`
	// Notes say what the detection could not settle, with the flag that settles it.
	Notes []string `json:"notes,omitempty"`
	// Digest is the SHA-256 of the plan's JSON with no digest, no notes, and nothing a registration said of each
	// repository, which the registration itself changes, and none of its practices. A restructuring sends it back, so a layout that changed
	// in between is found.
	Digest string `json:"digest,omitempty"`
}

// Offers says `--restructure` has something to do in the plan: always in a shape that restructures, and in the single
// shape when the plan lists a file the clone lacks.
func (p AdoptionPlan) Offers() bool {
	return p.Shape.Restructures() || p.Shape == ShapeSingle && len(p.Files) != 0
}

// MaxLayoutDepth is the most levels below a folder a plan searches for clones. A folder the clone ignores may
// hold a dependency tree or a build output, so a request cannot ask for a walk as deep as it likes.
const MaxLayoutDepth = 4

// Restructure is the restructuring a member chose from an adoption plan, as an adopt work order keeps it: the
// shape, the digest of the plan it was chosen from, for a consolidation the design repository and the code
// trees to leave out, and the request the plan was read with, so a worker reads the layout again the way the
// plan did and names what differs when it reads another.
type Restructure struct {
	Shape  LayoutShape `json:"shape"`
	Digest string      `json:"digest"`
	// DesignPath is the clone of the design repository, and DesignRoot its first-parent root, which the history
	// a consolidation imports is checked against.
	DesignPath string `json:"design_path,omitempty"`
	DesignRoot string `json:"design_root,omitempty"`
	// LeaveOut names the design repository's code trees a consolidation removes, relative to its top.
	LeaveOut []string `json:"leave_out,omitempty"`
	// Root, Paths, Depth, Planning, and Design are the request the plan was read with: the clone adopt ran in
	// as an absolute path, the paths named beside it, how many levels were searched for clones, and the clone
	// folder names --planning and --design named. A restructuring kept before they existed has none of them, and
	// Missing says what that leaves a worker without.
	Root     string   `json:"root,omitempty"`
	Paths    []string `json:"paths,omitempty"`
	Depth    int      `json:"depth,omitempty"`
	Planning string   `json:"planning,omitempty"`
	Design   string   `json:"design,omitempty"`
	// Parts are the digests of the plan's parts, by part name, which say what differs when the digest does.
	Parts map[string]string `json:"parts,omitempty"`
}

// ErrBadRestructure marks a restructuring that cannot be kept.
var ErrBadRestructure = errors.New("invalid restructuring")

// Missing names what a restructuring lacks for a worker to read the layout again as the plan did: the root the
// plan was read from, and the digests of its parts. The other fields of the request may be empty.
func (r Restructure) Missing() []string {
	var missing []string
	if r.Root == "" {
		missing = append(missing, "root")
	}
	if len(r.Parts) == 0 {
		missing = append(missing, "parts")
	}
	return missing
}

// ErrIncompleteRestructure marks a restructuring kept before the request its plan was read with and the digests of
// the plan's parts were, which a worker cannot read the layout against again.
var ErrIncompleteRestructure = errors.New("the restructuring is incomplete")

// Incomplete says why a worker cannot read the layout again as the plan did, naming what the restructuring lacks, or
// is nil when it lacks nothing. The error is ErrIncompleteRestructure.
func (r Restructure) Incomplete() error {
	missing := r.Missing()
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%w: it has no %s, because it was kept before the plan's request and the digests of its parts were, so the layout cannot be read again as the plan read it",
		ErrIncompleteRestructure, strings.Join(missing, " and no "))
}

// isDigest says a string is the SHA-256 digest of something, as DigestSHA256 spells one.
func isDigest(value string) bool {
	hash, ok := strings.CutPrefix(value, DigestSHA256)
	return ok && len(hash) == sha256.Size*2 && strings.Trim(hash, "0123456789abcdef") == ""
}

// validateRequest refuses a request with no clean absolute root, a path that is not absolute, a depth beyond what a
// plan searches, a clone name that holds a slash, or the digest of a part nothing digests.
func (r Restructure) validateRequest() error {
	if r.Root == "" {
		if len(r.Paths) != 0 || r.Depth != 0 || r.Planning != "" || r.Design != "" {
			return fmt.Errorf("%w: the request the plan was read with needs the root it was read from", ErrBadRestructure)
		}
	} else if !path.IsAbs(r.Root) || path.Clean(r.Root) != r.Root {
		return fmt.Errorf("%w: the root %q is no clean absolute path", ErrBadRestructure, r.Root)
	}
	for _, named := range r.Paths {
		if !path.IsAbs(named) {
			return fmt.Errorf("%w: the path %q is not absolute", ErrBadRestructure, named)
		}
	}
	if r.Depth < 0 || r.Depth > MaxLayoutDepth {
		return fmt.Errorf("%w: the depth %d is not between 0 and %d", ErrBadRestructure, r.Depth, MaxLayoutDepth)
	}
	for flag, name := range map[string]string{"planning": r.Planning, "design": r.Design} {
		if strings.Contains(name, "/") {
			return fmt.Errorf("%w: the %s name %q is a clone's folder name, which holds no slash", ErrBadRestructure, flag, name)
		}
	}
	for part, digest := range r.Parts {
		if !slices.Contains(PlanParts, part) {
			return fmt.Errorf("%w: %q is no part of a plan", ErrBadRestructure, part)
		}
		if !isDigest(digest) {
			return fmt.Errorf("%w: the digest %q of part %s is no SHA-256 digest", ErrBadRestructure, digest, part)
		}
	}
	return nil
}

// Validate refuses a restructuring that names no shape, carries no digest, or holds a design repository or code
// trees outside a consolidation, or a request that cannot be kept.
func (r Restructure) Validate() error {
	if !r.Shape.Valid() {
		return fmt.Errorf("%w: the shape %q is no shape of a layout", ErrBadRestructure, r.Shape)
	}
	if !isDigest(r.Digest) {
		return fmt.Errorf("%w: the digest %q is no SHA-256 digest of a plan", ErrBadRestructure, r.Digest)
	}
	if err := r.validateRequest(); err != nil {
		return err
	}
	if r.Shape != ShapeConsolidate {
		if r.DesignPath != "" || r.DesignRoot != "" || len(r.LeaveOut) != 0 {
			return fmt.Errorf("%w: a design repository and code trees to leave out belong to a consolidation, and this is the %s shape", ErrBadRestructure, r.Shape)
		}
		return nil
	}
	if !path.IsAbs(r.DesignPath) || r.DesignRoot == "" {
		return fmt.Errorf("%w: a consolidation names the design repository by its absolute path and its first-parent root", ErrBadRestructure)
	}
	for _, tree := range r.LeaveOut {
		if tree == "" || tree == "." || path.IsAbs(tree) || path.Clean(tree) != tree || tree == ".." || strings.HasPrefix(tree, "../") {
			return fmt.Errorf("%w: %q is no code tree below the design repository's top", ErrBadRestructure, tree)
		}
	}
	return nil
}

// ComputeDigest is the digest of the plan as it stands.
func (p AdoptionPlan) ComputeDigest() string {
	p.Digest, p.Notes, p.MakefileLeftOut = "", nil, nil
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

// The parts of a plan that Parts digests, in the order a refusal names them.
const (
	PartLayout       = "layout"
	PartShape        = "shape"
	PartPlanning     = "planning"
	PartDesign       = "design"
	PartRepositories = "repositories"
	PartFiles        = "files"
	PartTakes        = "takes"
	PartCollisions   = "collisions"
	PartCodeTrees    = "code_trees"
	PartMoves        = "moves"
	PartSteps        = "steps"
)

// PlanParts lists the parts Parts digests, in order.
var PlanParts = []string{
	PartLayout, PartShape, PartPlanning, PartDesign, PartRepositories, PartFiles, PartTakes, PartCollisions, PartCodeTrees, PartMoves, PartSteps,
}

// orEmpty spells a list with nothing in it one way, whether it is nil or empty, so a plan that crossed the
// wire digests as the plan it was.
func orEmpty[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

// Parts digests each part of the plan alone, with what ComputeDigest leaves out left out of each: no notes, and
// of each repository its name and path and whether it is left unregistered, and no more. They exist so a
// refusal can name what differs, and they never stand for the plan: its Digest does, as it always did.
func (p AdoptionPlan) Parts() map[string]string {
	layout := p.Layout
	layout.Folders, layout.Clones = orEmpty(layout.Folders), orEmpty(layout.Clones)
	repositories := make([]PlannedRepository, 0, len(p.Repositories))
	for _, repository := range p.Repositories {
		kept := PlannedRepository{Name: repository.Name, Path: repository.Path}
		if repository.Outcome == OutcomeNotRegistered {
			kept.Outcome = OutcomeNotRegistered
		}
		repositories = append(repositories, kept)
	}
	moves := make([]Move, 0, len(p.Moves))
	for _, move := range p.Moves {
		move.Commands = orEmpty(move.Commands)
		moves = append(moves, move)
	}
	byPart := map[string]any{
		PartLayout: layout, PartShape: p.Shape, PartPlanning: p.Planning, PartDesign: p.Design, PartRepositories: repositories,
		PartFiles: orEmpty(p.Files), PartTakes: orEmpty(p.Takes), PartCollisions: orEmpty(p.Collisions),
		PartCodeTrees: orEmpty(p.CodeTrees), PartMoves: moves, PartSteps: orEmpty(p.Steps),
	}
	parts := make(map[string]string, len(byPart))
	for name, part := range byPart {
		raw, err := json.Marshal(part)
		if err != nil {
			// A plan holds only strings, numbers, and lists, which always encode.
			panic(err)
		}
		sum := sha256.Sum256(raw)
		parts[name] = DigestSHA256 + hex.EncodeToString(sum[:])
	}
	return parts
}

// DiffersIn names the parts of the plan whose digest is not the one stored, in the order of PlanParts. A part the
// stored digests do not hold differs.
func (p AdoptionPlan) DiffersIn(stored map[string]string) []string {
	var differs []string
	for name, now := range p.Parts() {
		if stored[name] != now {
			differs = append(differs, name)
		}
	}
	slices.SortFunc(differs, func(a, b string) int { return slices.Index(PlanParts, a) - slices.Index(PlanParts, b) })
	return differs
}
