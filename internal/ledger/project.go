package ledger

import (
	"errors"
	"fmt"
	"strings"
)

// A project is a workspace whose ledger is a database of its own, and an installation holds many
// (ADR-0021). The installation database beside them holds the registry of projects, the installation's
// own event log, the repository index, and the records every project shares: members and their
// sessions, workers with their join grants, autonomy, and run windows, and orchestrators.

// SubjectProject is what a project's installation events are about.
const SubjectProject = "project"

// The installation's events.
const (
	// EventProjectCreated records a project registered in the installation, with its database.
	EventProjectCreated = "project.created"
	// EventProjectRenamed records a new name, which the ID and the database keep.
	EventProjectRenamed = "project.renamed"
	// EventProjectArchived and EventProjectRestored record a project leaving the active state and coming
	// back to it.
	EventProjectArchived = "project.archived"
	EventProjectRestored = "project.restored"
	// EventProjectRemoved records a project the installation forgot, whose database went.
	EventProjectRemoved = "project.removed"
	// EventProjectImported records a project registered from a backup, with the ID the backup holds.
	EventProjectImported = "project.imported"
	// EventRecordsMoved records the installation records an upgrade copied out of a project's database.
	EventRecordsMoved = "installation.records_moved"
)

// DefaultProjectName is the name of an installation's first project, which an installation with one
// ledger becomes.
const DefaultProjectName = "default"

// InstallationScope is the workspace ID the installation's shared records carry, so the statements that
// read them inside a project's transaction are the ones a single ledger runs. No project has it.
const InstallationScope WorkspaceID = "installation"

// ErrInvalidProject marks a project record the installation must not accept.
var ErrInvalidProject = errors.New("invalid project")

// ErrProjectExists means another project of the installation holds the ID, the name, or the database.
var ErrProjectExists = errors.New("a project holds that ID, name, or database")

// ErrProjectState means a project's state refuses a lifecycle step: an archive while a lane runs, a
// restore of a project that is active, or a removal of one that is not archived.
var ErrProjectState = errors.New("the project's state refuses this")

// ErrProjectArchived means a change to an archived project, which is read-only until it is restored.
var ErrProjectArchived = errors.New("the project is archived, so it is read-only")

// ErrRepositoryHeld means another project of the installation holds the repository (ADR-0021). The error
// names that project.
var ErrRepositoryHeld = errors.New("another project holds that repository")

// projectNameLimit keeps a name usable as one segment of a URL path and a DNS label.
const projectNameLimit = 63

// ProjectState is where a project is in its lifecycle.
type ProjectState string

// The project states. An archived project takes no claims and runs no ticks.
const (
	ProjectActive   ProjectState = "active"
	ProjectArchived ProjectState = "archived"
)

// Valid reports a state this build knows.
func (s ProjectState) Valid() bool { return s == ProjectActive || s == ProjectArchived }

// Project is one entry of the installation's registry. Its ID is the workspace ID every record of its
// ledger carries, and Database names where that ledger lives: a file relative to the state directory, or
// a schema on a server.
type Project struct {
	ID          WorkspaceID
	Name        string
	DisplayName string
	State       ProjectState
	Database    string
	CreatedAt   int64
}

// Validate refuses a record the registry cannot hold.
func (p Project) Validate() error {
	if err := p.ID.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProject, err)
	}
	if p.ID == InstallationScope {
		return fmt.Errorf("%w: %s is the installation's own scope", ErrInvalidProject, p.ID)
	}
	if err := ValidateProjectName(p.Name); err != nil {
		return err
	}
	if !p.State.Valid() {
		return fmt.Errorf("%w: state %q", ErrInvalidProject, p.State)
	}
	if p.Database == "" {
		return fmt.Errorf("%w: no database", ErrInvalidProject)
	}
	return nil
}

// ValidateProjectName refuses a name that is not a lowercase, hyphenated slug: letters and digits in
// words joined by single hyphens.
func ValidateProjectName(name string) error {
	if name == "" || len(name) > projectNameLimit {
		return fmt.Errorf("%w: a name has 1 to %d characters, and %q has %d", ErrInvalidProject, projectNameLimit, name, len(name))
	}
	for word := range strings.SplitSeq(name, "-") {
		if word == "" {
			return fmt.Errorf("%w: %q has an empty word between hyphens", ErrInvalidProject, name)
		}
		for _, c := range word {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return fmt.Errorf("%w: %q holds %q, and a name holds lowercase letters, digits, and hyphens", ErrInvalidProject, name, c)
			}
		}
	}
	return nil
}

// IndexedRepository is one entry of the installation's repository index: which project holds the
// repository with an identity, and its record's ID there.
type IndexedRepository struct {
	Identity   string
	Project    WorkspaceID
	Repository string
}

// RepositoryIdentity is what makes two records the same repository (ADR-0021): the remote's host and path,
// with no credential, port, or `.git` suffix, so every protocol of one remote agrees. A remote that is a
// path on this machine is that path, and a repository with no remote is its record's ID, which names the
// clone it was read from.
func RepositoryIdentity(remote, id string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return id
	}
	host, path := splitRemote(remote)
	path = strings.TrimSuffix(strings.TrimRight(path, "/"), ".git")
	if host == "" {
		return path
	}
	return strings.ToLower(host) + "/" + strings.TrimLeft(path, "/")
}

// splitRemote reads the host and path of a URL, of git's scp-like form, or of a local path, which has no
// host.
func splitRemote(remote string) (host, path string) {
	if scheme, rest, found := strings.Cut(remote, "://"); found {
		if scheme == "file" {
			return "", rest
		}
		authority, path, _ := strings.Cut(rest, "/")
		return hostOf(authority), "/" + path
	}
	if strings.HasPrefix(remote, "/") || strings.HasPrefix(remote, ".") {
		return "", remote
	}
	if authority, path, found := strings.Cut(remote, ":"); found && !strings.Contains(authority, "/") {
		return hostOf(authority), path
	}
	return "", remote
}

// hostOf drops a credential and a port from a URL's authority.
func hostOf(authority string) string {
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		authority = authority[at+1:]
	}
	host, _, _ := strings.Cut(authority, ":")
	return host
}
