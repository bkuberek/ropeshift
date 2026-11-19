package ledger

import (
	"errors"
	"fmt"
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

// TemplateWriteRepository is a code repository a workspace names: its name, and either the remote its clone at
// repos/<name> comes from, for an existing repository, or, for a new one, the files and first commit the worker
// writes at repos/<name> and the optional remote it gives them: a URL to add as origin, or a repository to create.
type TemplateWriteRepository struct {
	Name string `json:"name"`
	// Remote is an existing repository's remote, which `make setup` clones from. A new repository has none.
	Remote string `json:"remote,omitempty"`
	// New says the worker writes the repository's files at repos/<name>, and gives it its own first commit when
	// the request asks for one.
	New bool `json:"new,omitempty"`
	// URL and Create are a new repository's remote, at most one of them.
	URL    string        `json:"url,omitempty"`
	Create *RemoteCreate `json:"create,omitempty"`
}

// ProjectRemote is the remote a new repository asks for, absent when it asks for none.
func (r TemplateWriteRepository) ProjectRemote() *ProjectRemote {
	if r.URL == "" && r.Create == nil {
		return nil
	}
	return &ProjectRemote{URL: r.URL, Create: r.Create}
}

// CheckRemote refuses the remote fields of a repository: a remote on an existing repository, which keeps only its
// required remote, both a URL and a repository to create, and a remote CheckURL, CheckOwner, or the forge refuses.
func (r TemplateWriteRepository) CheckRemote() error {
	if !r.New {
		if r.URL != "" || r.Create != nil {
			return fmt.Errorf("%w: repository %s is existing, so it names its remote in remote and creates nothing", ErrInvalidProjectRemote, r.Name)
		}
		return nil
	}
	if r.Remote != "" {
		return fmt.Errorf("%w: repository %s is new, so it names a url or a repository to create and no remote", ErrInvalidProjectRemote, r.Name)
	}
	if r.URL != "" && r.Create != nil {
		return fmt.Errorf("%w: repository %s names a url or a repository to create, not both", ErrInvalidProjectRemote, r.Name)
	}
	if remote := r.ProjectRemote(); remote != nil {
		return remote.Check()
	}
	return nil
}

// TemplateWriteRequest is what a member asks a worker to write: a shape's starting files into a folder on the
// worker's machine (FR-TPL-5). Name is the project's name rendered into the files and into the commands that
// register them, and the folder's name makes one when it is empty. Project names the project the printed commands
// register a repository in, and only a repository takes one. Repositories are the code repositories of a workspace, and only a workspace takes
// them.
type TemplateWriteRequest struct {
	Shape        string                    `json:"shape"`
	Path         string                    `json:"path"`
	Repositories []TemplateWriteRepository `json:"repositories,omitempty"`
	Name         string                    `json:"name,omitempty"`
	Project      string                    `json:"project,omitempty"`
	FirstCommit  bool                      `json:"first_commit,omitempty"`
	// Remote is the remote of the folder's own repository: the monorepo, the repository, or the workspace's
	// planning repository. It needs FirstCommit, because a remote needs a commit to push.
	Remote *ProjectRemote `json:"remote,omitempty"`
}

// CheckRemotes refuses the remote fields of a request: a remote of the folder or a repository the first commit
// does not back, `new` on a shape other than a workspace, and a remote CheckURL, CheckOwner, or the forge refuses.
func (r TemplateWriteRequest) CheckRemotes() error {
	if r.Remote != nil {
		if err := r.Remote.Check(); err != nil {
			return err
		}
	}
	asked := r.Remote != nil
	for _, repository := range r.Repositories {
		if repository.New && r.Shape != "workspace" {
			return fmt.Errorf("%w: a %s names no new code repositories; a workspace does", ErrInvalidProjectRemote, r.Shape)
		}
		if err := repository.CheckRemote(); err != nil {
			return err
		}
		asked = asked || repository.ProjectRemote() != nil
	}
	if asked && !r.FirstCommit {
		return fmt.Errorf("%w: a remote needs the first commit, which ropeshift pushes", ErrInvalidProjectRemote)
	}
	return nil
}

// ProtocolTemplateName is the oldest protocol a worker is offered a request that names the project. An older worker
// drops the name and would write the files for the folder's name.
const ProtocolTemplateName = 7

// HasRemotes says the request holds a field only a worker on ProtocolProjectRemotes reads.
func (r TemplateWriteRequest) HasRemotes() bool {
	if r.Remote != nil {
		return true
	}
	for _, repository := range r.Repositories {
		if repository.New || repository.URL != "" || repository.Create != nil {
			return true
		}
	}
	return false
}

// Creates says some repository of the request is created on a forge.
func (r TemplateWriteRequest) Creates() bool {
	if r.Remote != nil && r.Remote.Create != nil {
		return true
	}
	for _, repository := range r.Repositories {
		if repository.Create != nil {
			return true
		}
	}
	return false
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
	Name         string                    `json:"name,omitempty"`
	Project      string                    `json:"project,omitempty"`
	Repositories []TemplateWriteRepository `json:"repositories,omitempty"`
	FirstCommit  bool                      `json:"first_commit,omitempty"`
	Remote       *ProjectRemote            `json:"remote,omitempty"`
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
	// Remedy is the command a person runs on the worker to fix what a refusal names, when one does.
	Remedy string `json:"remedy,omitempty"`
	// Repositories are the repositories the worker made: the folder's own, at path ".", and each new code
	// repository, at repos/<name>, with its commit, its remote, and the outcome of each step.
	Repositories []RepositoryReport `json:"repositories,omitempty"`
	// Remaining are the commands that finish what did not run: a repository to create, an origin to add, a push.
	Remaining []string `json:"remaining,omitempty"`
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
		ID: r.ID, Shape: r.Request.Shape, Path: r.Request.Path, Name: r.Request.Name, Project: r.Request.Project,
		Repositories: r.Request.Repositories, FirstCommit: r.Request.FirstCommit, Remote: r.Request.Remote, ExpiresAt: r.ExpiresAt,
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
	Name         string                    `json:"name,omitempty"`
	Project      string                    `json:"project,omitempty"`
	Repositories []TemplateWriteRepository `json:"repositories,omitempty"`
	FirstCommit  bool                      `json:"first_commit,omitempty"`
	Remote       *ProjectRemote            `json:"remote,omitempty"`
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
		Shape: r.Request.Shape, Path: r.Request.Path, Name: r.Request.Name, Project: r.Request.Project, Repositories: r.Request.Repositories,
		FirstCommit: r.Request.FirstCommit, Remote: r.Request.Remote, State: r.StateAt(now), Report: r.Report, ReportedAt: r.ReportedAt,
		Registrable: registrable && r.Report != nil && r.Report.Clone,
	}
}
