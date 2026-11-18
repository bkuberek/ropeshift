package planning

import (
	"cmp"
	"path"
)

// MergeCommitKeepsHistory says why an adoption branch that imports a design repository's history merges only
// with a merge commit (FR-ADOPT-13).
const MergeCommitKeepsHistory = "Only a merge commit keeps the design history: a squash or a rebase merge keeps the files " +
	"and drops the design repository's commits, because it drops the merge's second parent."

// Consolidation is what an adoption that consolidated a design repository into the planning repository brings to
// its approval (FR-ADOPT-13): the merge commit that imported the history and its second parent, the design
// repository's tip; every path both repositories held, with the version the merge kept; the code trees the merge
// removed; and the old design repository when the project holds it, which approving removes from the project with
// its record kept as history.
type Consolidation struct {
	// Design is the design repository's clone folder name.
	Design string `json:"design"`
	// Merge is the merge commit on the adoption branch, and DesignTip its second parent.
	Merge     string `json:"merge,omitempty"`
	DesignTip string `json:"design_tip,omitempty"`
	// Collisions are the paths both repositories held, each with the version the merge kept, at most MaxCollisions
	// of them. CollisionsTruncated says the list is cut short: there were more, or the trees were too large to read
	// whole.
	Collisions          []Collision `json:"collisions"`
	CollisionsTruncated bool        `json:"collisions_truncated,omitempty"`
	// LeftOut are the design repository's code trees the merge commit removed.
	LeftOut []string `json:"left_out"`
	// Removes is the old design repository's record, when the project holds it and it is the clone the adoption
	// imported: approving removes it. RemovalUnread says why a record the project holds under the design
	// repository's first commit is not removed, because nothing shows it is that clone.
	Removes       *RemovedRepository `json:"removes,omitempty"`
	RemovalUnread string             `json:"removal_unread,omitempty"`
	// PersonMerges says the person merges the adoption branch, at person_merges, so the approval tells them
	// that only a merge commit keeps the design history.
	PersonMerges bool `json:"person_merges,omitempty"`
	// Unread says why the merge could not be read from the adoption branch, when it could not, and the
	// collisions and the design tip are then unknown.
	Unread string `json:"unread,omitempty"`
}

// MaxCollisions is the most colliding paths a consolidation's approval lists.
const MaxCollisions = 200

// RemovedRepository is a repository an approval removes from the project.
type RemovedRepository struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Consolidates says a restructuring imports a design repository's history.
func (r *Restructure) Consolidates() bool { return r != nil && r.Shape == ShapeConsolidate }

// DesignName is the design repository's clone folder name: the one --design named, or else the last element of
// the clone's path.
func (r Restructure) DesignName() string {
	return cmp.Or(r.Design, path.Base(r.DesignPath))
}
