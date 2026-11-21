package api

import (
	"context"
	"net/http"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// OutsideEditsPath reads what each registered repository's clone on the daemon's worker's machine holds that intake and
// lanes cannot read (FR-UI-14).
const OutsideEditsPath = "/v1/outside-edits"

// FreshQuery is the query of a read of OutsideEditsPath that reads the clones again, however recent the reading the
// daemon holds.
const FreshQuery = "fresh=1"

// OutsideEdits is every registered repository's clone, read at one time.
type OutsideEdits struct {
	AsOf int64 `json:"as_of"`
	// Limit is how many paths of one clone a reading lists.
	Limit  int              `json:"limit"`
	Clones []CloneEditsView `json:"clones"`
	// Conflicts are the lanes whose branch changed files a person changed too, as Needs you lists them.
	Conflicts []ConflictNeed `json:"conflicts"`
}

// CloneEditsView is one repository's clone. Every name and path git gave is plain text.
type CloneEditsView struct {
	Repository string `json:"repository"`
	Name       string `json:"name"`
	// Planning says the project plans in this repository.
	Planning bool `json:"planning,omitempty"`
	// Path is the clone's top, and empty when the machine keeps no clone of the repository.
	Path   string `json:"path,omitempty"`
	Branch string `json:"branch,omitempty"`
	Head   string `json:"head,omitempty"`
	Remote string `json:"remote,omitempty"`
	// Edits lists each path intake and lanes cannot read, in order of path, at most Limit of them; Cut says git
	// listed more.
	Edits []EditView `json:"edits"`
	Cut   bool       `json:"cut,omitempty"`
	// Steps make the paths readable, each with the exact commands that take it.
	Steps []ledger.EditStep `json:"steps"`
	// Problem says why the clone was not read.
	Problem string `json:"problem,omitempty"`
}

// EditView is one path of a clone that intake and lanes cannot read.
type EditView struct {
	Path  string           `json:"path"`
	State ledger.EditState `json:"state"`
	// Steps are the kinds of the clone's steps whose commands take the path.
	Steps []string `json:"steps"`
	// Hidden says the path holds a character a page cannot show as a shell reads it, so Path shows it without that
	// character, or a backslash, and no command names it.
	Hidden bool `json:"hidden,omitempty"`
}

// ConflictNeed is a lane whose branch changed files a person changed too: both sides, and the ways a member settles it,
// each with its exact commands. Every name and path git gave is plain text.
type ConflictNeed struct {
	WorkOrder string           `json:"work_order"`
	Title     string           `json:"title"`
	State     workorders.State `json:"state"`
	Lane      string           `json:"lane"`
	// Branch is the lane's branch, and Base the ref of the base it is read against, such as origin/main.
	Branch     string `json:"branch"`
	Base       string `json:"base"`
	Repository string `json:"repository"`
	Name       string `json:"name"`
	// Clone is the path of the person's clone on the daemon's worker's machine.
	Clone string             `json:"clone"`
	Paths []ConflictPathView `json:"paths"`
	// Cut says git listed more paths of a side than a reading keeps, so Paths may not be every one.
	Cut bool `json:"cut,omitempty"`
	// Options are the ways to settle it: keep_both, keep_person, and stop.
	Options []ledger.EditStep `json:"options"`
	// Clears says what ends the conflict (FR-UI-13).
	Clears string `json:"clears"`
}

// ConflictPathView is a path both sides changed, with how the person changed it.
type ConflictPathView struct {
	Path string `json:"path"`
	// Person is uncommitted, untracked, or unpushed for the person's clone, or pushed for the base.
	Person string `json:"person"`
	// Hidden says the path holds a character a page cannot show as a shell reads it, which no option names.
	Hidden bool `json:"hidden,omitempty"`
}

// OutsideEdits reads each registered repository's clone on the daemon's worker's machine.
func (c *ProjectClient) OutsideEdits(ctx context.Context) (OutsideEdits, error) {
	var out OutsideEdits
	err := c.do(ctx, http.MethodGet, OutsideEditsPath, nil, &out)
	return out, err
}

// FreshOutsideEdits reads the clones again, however recent the reading the daemon holds.
func (c *ProjectClient) FreshOutsideEdits(ctx context.Context) (OutsideEdits, error) {
	var out OutsideEdits
	err := c.do(ctx, http.MethodGet, OutsideEditsPath+"?"+FreshQuery, nil, &out)
	return out, err
}
