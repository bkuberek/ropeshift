package contract

import (
	"reflect"
	"strings"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
)

// designRoot is the first-parent root of the design repository a consolidation imports, which is also the ID of
// its record when the project holds it.
const designRoot = "design-root"

// consolidating is the restructuring a member chose for a consolidation of the design repository, with the code
// tree poc left out.
func consolidating() *planning.Restructure {
	digest := planning.DigestSHA256 + strings.Repeat("c", 64)
	return &planning.Restructure{
		Shape: planning.ShapeConsolidate, Digest: digest, Root: "/work/ws", DesignPath: "/work/ws/repos/design", DesignRoot: designRoot,
		LeaveOut: []string{"poc"}, Parts: map[string]string{planning.PartCollisions: digest},
	}
}

// consolidatedBranch grows, in the start world's repository, an adoption branch that begins where main stands
// and holds the merge that imports the design repository's history, with a commit after it. It returns the
// merge, the design tip, and the branch's head.
func (w *startWorld) consolidatedBranch() (merge, tip, head string) {
	path := w.path(startRepository)
	base := w.git.Touch(path, "main", "README.md")
	w.git.Hold(base, ".gitignore", "README.md", "workspace.md")
	tip = w.git.Add()
	w.git.Hold(tip, ".gitignore", "README.md", "docs/vision.md", "poc/go.mod", "poc/main.go")
	merge = w.git.Add(base, tip)
	w.git.Name(merge, planning.ImportCommitSubject("design"))
	w.git.Point(path, "lane/adopt", merge)
	head = w.git.Touch(path, "lane/adopt", "plan.md")
	return merge, tip, head
}

// consolidationAsked runs a consolidating adoption's lane to the end of its branch and reads what its request says
// of the consolidation.
func (w *startWorld) consolidationAsked(head string) (*planning.Consolidation, error) {
	id, err := w.adoptOrder(startRepository, consolidating())
	if err != nil {
		return nil, err
	}
	if err := w.adoptLaneEnds(id, head); err != nil {
		return nil, err
	}
	_, adoption, err := w.pendingAdoption(control.NewIntake(w.s).Checking(w.starts))
	if err != nil {
		return nil, err
	}
	if adoption == nil {
		return nil, Violatedf("the consolidating adoption's request groups nothing")
	}
	return adoption.Consolidation, nil
}

// heldDesign registers the old design repository in the project, under the ID a consolidation names it by.
func (w *startWorld) heldDesign() (ledger.Repository, error) {
	return w.r.repositories(w.s).Detect(w.r.Ctx, w.r.Scope, control.DetectedRepository{ID: designRoot, DefaultBranch: "main", Directory: "design"},
		ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
}

func checkAConsolidationListsWhatItImported(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	design, err := w.heldDesign()
	if err != nil {
		return err
	}
	merge, tip, head := w.consolidatedBranch()
	consolidation, err := w.consolidationAsked(head)
	if err != nil {
		return err
	}
	want := planning.Consolidation{
		Design: "design", Merge: merge, DesignTip: tip,
		Collisions: []planning.Collision{{Path: ".gitignore", Kept: planning.KeptUnion}, {Path: "README.md", Kept: planning.KeptWorkspace}},
		LeftOut:    []string{"poc"}, Removes: &planning.RemovedRepository{ID: designRoot, Name: design.Name}, PersonMerges: true,
	}
	if consolidation == nil || !reflect.DeepEqual(*consolidation, want) {
		return Violatedf("the consolidation reads\n%+v\nwant\n%+v", consolidation, want)
	}
	return nil
}

func checkAConsolidationTheProjectDoesNotHoldRemovesNothing(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	// The repository merges at person_approves, where ropeshift merges it, so the approval says nothing of the
	// person's merge.
	level := ledger.PersonApproves
	if _, err := r.change(s, startRepository, control.RepositoryChange{MergeAuthority: &level}, r.Actor); err != nil {
		return err
	}
	_, _, head := w.consolidatedBranch()
	consolidation, err := w.consolidationAsked(head)
	if err != nil {
		return err
	}
	if consolidation == nil || consolidation.Removes != nil || consolidation.PersonMerges || len(consolidation.Collisions) != 2 {
		return Violatedf("a consolidation of a design repository the project does not hold reads %+v", consolidation)
	}
	return nil
}

func checkAConsolidationWhoseMergeIsUnreadSaysWhy(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	if _, err := w.heldDesign(); err != nil {
		return err
	}
	// The branch holds no merge that imports a history.
	path := w.path(startRepository)
	base := w.git.Touch(path, "main", "README.md")
	w.git.Point(path, "lane/adopt", base)
	head := w.git.Touch(path, "lane/adopt", "plan.md")
	consolidation, err := w.consolidationAsked(head)
	if err != nil {
		return err
	}
	if consolidation == nil || consolidation.Unread == "" || consolidation.Merge != "" || consolidation.DesignTip != "" ||
		consolidation.Design != "design" || !reflect.DeepEqual(consolidation.LeftOut, []string{"poc"}) || consolidation.Removes == nil {
		return Violatedf("a consolidation whose merge cannot be read reads %+v", consolidation)
	}
	return nil
}
