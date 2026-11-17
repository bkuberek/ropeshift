package ledger

import (
	"errors"
	"slices"
	"strings"
)

// TemplateWriteTTLMs is how long a template write request is offered: two minutes after a member made it. A
// worker that reconnects later writes nothing nobody waits for.
const TemplateWriteTTLMs int64 = 2 * 60 * 1000

// TemplateWriteState is where a template write request stands.
type TemplateWriteState string

// The states of a template write request. A request is offered until its worker reports it; one nobody reported
// by its expiry reads as expired, and a report that comes later is still recorded, because the files exist.
const (
	TemplateWriteOffered TemplateWriteState = "offered"
	TemplateWriteWritten TemplateWriteState = "written"
	TemplateWriteRefused TemplateWriteState = "refused"
	TemplateWriteExpired TemplateWriteState = "expired"
)

// The outcomes of a worker's template write.
const (
	TemplateOutcomeWritten = "written"
	TemplateOutcomeRefused = "refused"
)

// The reasons a worker refuses a template write, in the order it checks them.
const (
	TemplateRefusedNotAbsolute   = "path_not_absolute"
	TemplateRefusedParentElement = "path_has_parent_element"
	// TemplateRefusedNotClean is a path that is not its own clean form: a trailing slash, `//`, or `/./` reaches a
	// symbolic link through a spelling that a check of the link does not see.
	TemplateRefusedNotClean = "path_not_clean"
	// TemplateRefusedReserved is a folder at or inside one ropeshift keeps for itself: its state, run, or worktree folder.
	TemplateRefusedReserved = "path_reserved"
	// TemplateRefusedChanged is a folder that became another, by a link swapped in for it, after the worker checked it.
	TemplateRefusedChanged      = "folder_changed"
	TemplateRefusedNoParent     = "parent_missing"
	TemplateRefusedLink         = "path_is_link"
	TemplateRefusedNotDirectory = "path_not_a_directory"
	TemplateRefusedNotEmpty     = "path_not_empty"
	TemplateRefusedInsideClone  = "path_inside_clone"
	// TemplateRefusedInvalid is a request the control plane should have refused: a shape or repositories no
	// folder can be written for.
	TemplateRefusedInvalid = "request_invalid"
)

// ErrInvalidTemplateWrite is the error of a template write no worker could be asked for.
var ErrInvalidTemplateWrite = errors.New("invalid template write")

// TemplateWriteRepository is a code repository a workspace names: its name, and the remote its clone at
// repos/<name> comes from.
type TemplateWriteRepository struct {
	Name   string `json:"name"`
	Remote string `json:"remote"`
}

// TemplateWriteRequest is what a member asks a worker to write: a shape's starting files into a folder on the
// worker's machine (FR-TPL-5). Project names the project the printed commands register the files in, and only
// a repository takes one. Repositories are the code repositories of a workspace, and only a workspace takes
// them.
type TemplateWriteRequest struct {
	Shape        string                    `json:"shape"`
	Path         string                    `json:"path"`
	Repositories []TemplateWriteRepository `json:"repositories,omitempty"`
	Project      string                    `json:"project,omitempty"`
	FirstCommit  bool                      `json:"first_commit,omitempty"`
}

// HasParentElement says a path, as it was sent and before any cleaning, holds a `..` element.
func HasParentElement(path string) bool {
	return slices.Contains(strings.Split(path, "/"), "..")
}

// DesiredTemplateWrite is a template write request a worker has not reported. The control plane offers it to
// the worker it names, until the worker reports it or it expires.
type DesiredTemplateWrite struct {
	ID           string                    `json:"id"`
	Shape        string                    `json:"shape"`
	Path         string                    `json:"path"`
	Project      string                    `json:"project,omitempty"`
	Repositories []TemplateWriteRepository `json:"repositories,omitempty"`
	FirstCommit  bool                      `json:"first_commit,omitempty"`
	ExpiresAt    int64                     `json:"expires_at"`
}

// TemplateWriteReport is what a worker did with a template write request. A refusal says why and where; a
// write lists the files it made, the first commit when it made one, and the commands that register the folder.
type TemplateWriteReport struct {
	Auth    WorkerAuth `json:"-"`
	Request string     `json:"request"`
	Outcome string     `json:"outcome"`
	Reason  string     `json:"reason,omitempty"`
	Detail  string     `json:"detail,omitempty"`
	// Project is the name rendered into the files.
	Project string `json:"project,omitempty"`
	// Files are the paths written, relative to the folder, sorted.
	Files []string `json:"files,omitempty"`
	// Commit is the first commit's ID when one was made, and CommitError what git said when it failed. The
	// files stay either way.
	Commit      string `json:"commit,omitempty"`
	CommitError string `json:"commit_error,omitempty"`
	// Clone says the folder is the top of a clone after the write.
	Clone    bool     `json:"clone,omitempty"`
	Commands []string `json:"commands,omitempty"`
}

// TemplateWriteRecord is a template write request as the installation holds it.
type TemplateWriteRecord struct {
	ID          string
	Worker      string
	RequestedBy string
	Request     TemplateWriteRequest
	RequestedAt int64
	ExpiresAt   int64
	// State is offered until a report lands, and then written or refused. Expiry is read from the clock, not
	// stored: StateAt says it.
	State      TemplateWriteState
	Report     *TemplateWriteReport
	ReportedAt int64
}

// StateAt is the request's state at a time: offered becomes expired once its expiry passes with no report.
func (r TemplateWriteRecord) StateAt(now int64) TemplateWriteState {
	if r.State == TemplateWriteOffered && now >= r.ExpiresAt {
		return TemplateWriteExpired
	}
	return r.State
}

// Desired is the request as its worker reads it.
func (r TemplateWriteRecord) Desired() DesiredTemplateWrite {
	return DesiredTemplateWrite{
		ID: r.ID, Shape: r.Request.Shape, Path: r.Request.Path, Project: r.Request.Project,
		Repositories: r.Request.Repositories, FirstCommit: r.Request.FirstCommit, ExpiresAt: r.ExpiresAt,
	}
}

// TemplateWriteView is a request as a member reads it: what was asked, where it stands, what the worker
// reported, and whether the folder can be registered from the web UI.
type TemplateWriteView struct {
	ID           string                    `json:"id"`
	Worker       string                    `json:"worker"`
	RequestedBy  string                    `json:"requested_by"`
	RequestedAt  int64                     `json:"requested_at"`
	ExpiresAt    int64                     `json:"expires_at"`
	Shape        string                    `json:"shape"`
	Path         string                    `json:"path"`
	Project      string                    `json:"project,omitempty"`
	Repositories []TemplateWriteRepository `json:"repositories,omitempty"`
	FirstCommit  bool                      `json:"first_commit,omitempty"`
	State        TemplateWriteState        `json:"state"`
	Report       *TemplateWriteReport      `json:"report,omitempty"`
	ReportedAt   int64                     `json:"reported_at,omitempty"`
	// Registrable says the web UI can register what was written: the report says the folder is a clone, and
	// the worker is the one this daemon runs, because the register route reads clones only through that
	// worker's checkouts.
	Registrable bool `json:"registrable"`
}

// ViewAt is the record as a member reads it at a time.
func (r TemplateWriteRecord) ViewAt(now int64, registrable bool) TemplateWriteView {
	return TemplateWriteView{
		ID: r.ID, Worker: r.Worker, RequestedBy: r.RequestedBy, RequestedAt: r.RequestedAt, ExpiresAt: r.ExpiresAt,
		Shape: r.Request.Shape, Path: r.Request.Path, Project: r.Request.Project, Repositories: r.Request.Repositories,
		FirstCommit: r.Request.FirstCommit, State: r.StateAt(now), Report: r.Report, ReportedAt: r.ReportedAt,
		Registrable: registrable && r.Report != nil && r.Report.Clone,
	}
}
