package api

import (
	"context"
	"net/http"

	"ropeshift.com/ropeshift/internal/ledger"
)

// OutsideEditsPath reads what each registered repository's clone on the daemon's worker's machine holds that intake and
// lanes cannot read (FR-UI-14).
const OutsideEditsPath = "/v1/outside-edits"

// OutsideEdits is every registered repository's clone, read at one time.
type OutsideEdits struct {
	AsOf int64 `json:"as_of"`
	// Limit is how many paths of one clone a reading lists.
	Limit  int              `json:"limit"`
	Clones []CloneEditsView `json:"clones"`
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

// OutsideEdits reads each registered repository's clone on the daemon's worker's machine.
func (c *ProjectClient) OutsideEdits(ctx context.Context) (OutsideEdits, error) {
	var out OutsideEdits
	err := c.do(ctx, http.MethodGet, OutsideEditsPath, nil, &out)
	return out, err
}
