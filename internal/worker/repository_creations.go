package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/internal/projectremote"
)

// checkLimit is how long the checks of a repository creation wait on GitHub before they refuse.
const checkLimit = 30 * time.Second

// gitHubSilent is the refusal of checks GitHub did not answer within their limit.
const gitHubSilent = "GitHub did not answer within 30 seconds; read the preview again"

// keyLength is the length of a creation's key, a SHA-256 in hex, which names its journal.
const keyLength = 64

// errNoProjectFolders says the worker was given no template runtime, so it writes no project folder.
var errNoProjectFolders = errors.New("this worker writes no project folders")

// RepositoryCreator is this worker's creation of the code repositories a plan approval names (FR-REPO-16). It writes
// the repository shape's files, the first commit, the GitHub repository, origin, and the push through the worker's
// template runtime, as `ropeshift new repository --create-remote` does, and keeps each creation's journal in the
// runtime's state directory, at repository-creations/<key>.json.
func (w *Worker) RepositoryCreator() ports.RepositoryCreator {
	return &repositoryCreator{w: w, limit: checkLimit}
}

type repositoryCreator struct {
	w *Worker
	// limit is how long the checks wait on GitHub.
	limit time.Duration
}

// creationJournal is what a worker keeps of one creation, so a crash between two steps repeats none and a retry goes
// on from the last step done. It names the repository it is of, which every later run of its key must name too.
type creationJournal struct {
	Path       string `json:"path"`
	Owner      string `json:"owner"`
	ForgeName  string `json:"forge_name"`
	Visibility string `json:"visibility"`
	// Branch is the branch the first commit starts on, as the checks chose it.
	Branch string `json:"branch"`
	projectremote.Progress
}

// checked is what the checks of a creation found: their answer and, for a first run, the progress the checks
// started, the folders above the path to make, and where the path leads.
type checked struct {
	ledger.CreationCheck
	progress projectremote.Progress
	// missing are the folders above the path that are not there, the highest first.
	missing []string
	real    string
}

// Check runs every check that comes before a creation's first write, and writes nothing. A creation its journal shows
// started answers from the journal: its own folder and forge repository are not refused, and the URL, the account,
// and the branch are those it ran with.
func (c *repositoryCreator) Check(ctx context.Context, job ledger.RepositoryCreation) (ledger.CreationCheck, error) {
	rt, err := c.runtime()
	if err != nil {
		return ledger.CreationCheck{}, err
	}
	if refusal := jobRefusal(job); refusal != nil {
		return ledger.CreationCheck{Refusal: refusal}, nil
	}
	journal, started, err := readCreation(rt, job.Key)
	if err != nil {
		return ledger.CreationCheck{}, err
	}
	if started {
		if refusal := journal.differs(job); refusal != nil {
			return ledger.CreationCheck{Refusal: refusal}, nil
		}
		return journal.check(), nil
	}
	found, err := c.check(ctx, rt, job)
	return found.CreationCheck, err
}

// Create runs a creation, or goes on with it from its journal. A first run runs the checks again, makes the folders
// above the path that are not there, journals what the checks decided, and then writes the files, makes the first
// commit, creates the forge repository, adds origin, and pushes. A run that finds a journal runs again what failed
// and goes on from the last step done. Nothing it made is deleted.
func (c *repositoryCreator) Create(ctx context.Context, job ledger.RepositoryCreation) (ledger.TemplateWriteReport, error) {
	rt, err := c.runtime()
	if err != nil {
		return ledger.TemplateWriteReport{}, err
	}
	if refusal := jobRefusal(job); refusal != nil {
		return *refusal, nil
	}
	journal, started, err := readCreation(rt, job.Key)
	if err != nil {
		return ledger.TemplateWriteReport{}, err
	}
	if started {
		if refusal := journal.differs(job); refusal != nil {
			return *refusal, nil
		}
		journal.Retry()
	} else {
		found, err := c.check(ctx, rt, job)
		if err != nil || found.Refusal != nil {
			if err != nil {
				return ledger.TemplateWriteReport{}, err
			}
			return *found.Refusal, nil
		}
		if refusal, err := makeFolders(ctx, rt, job.Path, found); err != nil || refusal != nil {
			if err != nil {
				return ledger.TemplateWriteReport{}, err
			}
			return *refusal, nil
		}
		journal = creationJournal{
			Path: job.Path, Owner: job.Create.Owner, ForgeName: job.ForgeName, Visibility: visibilityOf(job.Create),
			Branch: found.Branch, Progress: found.progress,
		}
		if err := saveCreation(rt, job.Key, journal); err != nil {
			return ledger.TemplateWriteReport{}, err
		}
	}
	remote := creationJob(job, journal.NeedsRepository, journal.Branch)
	return projectremote.Finish(ctx, c.w.projectRuntime(rt), remote, &journal.Progress, func() error { return saveCreation(rt, job.Key, journal) })
}

func (c *repositoryCreator) runtime() (*TemplateRuntime, error) {
	if c.w.config.Templates == nil {
		return nil, errNoProjectFolders
	}
	return c.w.config.Templates, nil
}

// check runs the checks of a creation in order and refuses with the first that fails: the path's spelling, where it
// leads, the folders above it, the folder itself as a template write checks it, and then the forge, under the
// limit GitHub has to answer in.
func (c *repositoryCreator) check(ctx context.Context, rt *TemplateRuntime, job ledger.RepositoryCreation) (checked, error) {
	refused := func(refusal *ledger.TemplateWriteReport) (checked, error) {
		return checked{CreationCheck: ledger.CreationCheck{Refusal: refusal}}, nil
	}
	if refusal := spellingRefusal(job.Path); refusal != nil {
		return refused(refusal)
	}
	found, err := rt.Folders.Inspect(ctx, job.Path)
	if err != nil {
		return checked{}, err
	}
	switch {
	case !within(job.Bound, found.Real):
		return refused(folderRefusal(ledger.TemplateRefusedOutsideBound,
			fmt.Sprintf("the folder leads to %s, which is not inside %s, the folder that holds the planning repository's clone", found.Real, job.Bound)))
	case found.Real == job.PlanningClone:
		return refused(folderRefusal(ledger.TemplateRefusedInsideClone, fmt.Sprintf("the folder is %s, the planning repository's clone", job.PlanningClone)))
	}
	missing, above, refusal, err := missingFolders(ctx, rt, job.Path)
	if err != nil || refusal != nil {
		if err != nil {
			return checked{}, err
		}
		return refused(refusal)
	}
	if refusal, err := c.w.checkFolder(ctx, rt, job.Path, found, folderRules{parentsMade: len(missing) > 0, ignoreLine: true}); err != nil || refusal != nil {
		if err != nil {
			return checked{}, err
		}
		return refused(refusal)
	}
	// Git names no clone for a folder whose parent is not there yet, so the clone the folder will lie in is the one
	// that holds the nearest folder above it.
	if len(missing) > 0 {
		found.CloneTop = above.CloneTop
	}
	needs, err := c.w.needsRepository(ctx, rt, found)
	if err != nil {
		return checked{}, err
	}
	branch := job.DefaultBranch
	if branch == "" {
		branch = rt.Folders.DefaultBranch(ctx, above.Real)
	}
	remote := creationJob(job, needs, branch)
	var progress projectremote.Progress
	limited, cancel := context.WithTimeout(ctx, c.limit)
	defer cancel()
	refusal, err = projectremote.Start(limited, c.w.projectRuntime(rt), remote, &progress)
	if limited.Err() != nil {
		// What gh answers once its time is up says nothing, so the checks refuse whatever they read.
		if ctx.Err() != nil {
			return checked{}, ctx.Err()
		}
		return refused(folderRefusal(ledger.TemplateRefusedGHTimeout, gitHubSilent))
	}
	if err != nil {
		return checked{}, err
	}
	if refusal != nil {
		if refusal.Reason == ledger.TemplateRefusedRemoteExists {
			refusal.Detail = fmt.Sprintf("%s/%s already exists on GitHub; clone it to %s and write `remote` in place of `create`",
				job.Create.Owner, job.ForgeName, manifestPath(job, found.Real))
		}
		return refused(refusal)
	}
	answer := ledger.CreationCheck{Branch: branch}
	if progress.Account != nil {
		answer.URL = ledger.CreatedURL(progress.Account.GitProtocol, job.Create.Owner, job.ForgeName)
		answer.Login = progress.Account.Login
	}
	return checked{CreationCheck: answer, progress: progress, missing: missing, real: found.Real}, nil
}

// jobRefusal refuses a job no manifest entry gives, before anything is read: a key that names no journal file, a
// bound or a planning clone that is not an absolute path in its clean form, a forge, owner, or visibility an entry
// cannot hold, a forge name that is not the path's last element, and a branch git refuses.
func jobRefusal(job ledger.RepositoryCreation) *ledger.TemplateWriteReport {
	invalid := func(detail string) *ledger.TemplateWriteReport {
		return folderRefusal(ledger.TemplateRefusedInvalid, detail)
	}
	switch {
	case !validKey(job.Key):
		return invalid("the creation's key is not 64 lowercase hex characters")
	case !absoluteClean(job.Bound) || !absoluteClean(job.PlanningClone):
		return invalid("the folder that holds the planning repository's clone, or the clone, is not an absolute path in its clean form")
	case job.ForgeName != path.Base(job.Path):
		return invalid(fmt.Sprintf("the forge name %q is not the last element of the path", job.ForgeName))
	}
	if err := job.Create.Check(); err != nil {
		return invalid(err.Error())
	}
	if job.DefaultBranch != "" {
		if err := ledger.CheckBranch(job.DefaultBranch); err != nil {
			return invalid(err.Error())
		}
	}
	return nil
}

// validKey says a key is 64 lowercase hex characters, as ledger.CreationKey makes it, and so a plain file name.
func validKey(key string) bool {
	if len(key) != keyLength {
		return false
	}
	for _, r := range key {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func absoluteClean(at string) bool {
	return len(at) > 0 && at[0] == '/' && path.Clean(at) == at
}

// within says real lies inside dir, and is not dir itself.
func within(dir, real string) bool {
	rel, err := filepath.Rel(dir, real)
	return err == nil && rel != "." && filepath.IsLocal(rel)
}

// manifestPath is the path a manifest gives a folder, relative to the planning repository's clone.
func manifestPath(job ledger.RepositoryCreation, real string) string {
	rel, err := filepath.Rel(job.PlanningClone, real)
	if err != nil {
		return job.Path
	}
	return filepath.ToSlash(rel)
}

func visibilityOf(create ledger.RemoteCreate) string {
	if create.Visibility == "" {
		return ledger.VisibilityPrivate
	}
	return create.Visibility
}

// creationJob is the job the shared logic of `ropeshift new` runs for a creation: the repository shape at the path,
// with its first commit on the branch and its repository created on the forge.
func creationJob(job ledger.RepositoryCreation, needsRepository bool, branch string) projectremote.Job {
	create := job.Create
	create.Visibility = visibilityOf(create)
	return projectremote.Job{
		Request: ledger.TemplateWriteRequest{
			Shape: string(projectfiles.Repository), Path: job.Path, Project: job.Project, FirstCommit: true,
			Remote: &ledger.ProjectRemote{Create: &create},
		},
		NeedsRepository: needsRepository,
		Branch:          branch,
	}
}

// missingFolders lists the folders above a folder that are not there, the highest first, and reads the nearest
// folder above it that is, a link followed. Something there that is not a folder is refused, because no folder can be
// made in it.
func missingFolders(ctx context.Context, rt *TemplateRuntime, at string) ([]string, ports.FolderInspection, *ledger.TemplateWriteReport, error) {
	var missing []string
	for dir := path.Dir(at); ; dir = path.Dir(dir) {
		found, err := rt.Folders.Inspect(ctx, dir)
		if err != nil {
			return nil, ports.FolderInspection{}, nil, err
		}
		if found.Exists {
			if found.Link {
				if found, err = rt.Folders.Inspect(ctx, found.Real); err != nil {
					return nil, ports.FolderInspection{}, nil, err
				}
			}
			if !found.Exists || !found.Directory {
				return nil, found, folderRefusal(ledger.TemplateRefusedNoParent, fmt.Sprintf("%s is not a folder, so no folder can be made in it", dir)), nil
			}
			return missing, found, nil, nil
		}
		if dir == "/" {
			return nil, found, folderRefusal(ledger.TemplateRefusedNoParent, "no folder above the folder exists"), nil
		}
		missing = append([]string{dir}, missing...)
	}
}

// makeFolders makes the folders above the path that the checks found missing, the highest first, and refuses when the
// path no longer leads where the checks found it leading, as a link swapped in above it since makes it. A folder made
// stays.
func makeFolders(ctx context.Context, rt *TemplateRuntime, at string, found checked) (*ledger.TemplateWriteReport, error) {
	if len(found.missing) == 0 {
		return nil, nil
	}
	for _, dir := range found.missing {
		if _, err := rt.Folders.Write(ctx, dir, nil, ""); err != nil {
			return folderRefusal(ledger.TemplateRefusedNoParent, fmt.Sprintf("making the folder %s: %s", dir, ledger.PlainText(err.Error()))), nil
		}
	}
	after, err := rt.Folders.Inspect(ctx, at)
	if err != nil {
		return nil, err
	}
	if !after.ParentDirectory || after.Real != found.real {
		return folderRefusal(ledger.TemplateRefusedChanged, fmt.Sprintf("the folder leads to %s now, not to %s, where the checks found it leading", after.Real, found.real)), nil
	}
	return nil, nil
}

// differs refuses a job of this journal's key that names another repository than the journal is of.
func (j creationJournal) differs(job ledger.RepositoryCreation) *ledger.TemplateWriteReport {
	if j.Path == job.Path && j.Owner == job.Create.Owner && j.ForgeName == job.ForgeName && j.Visibility == visibilityOf(job.Create) &&
		(job.DefaultBranch == "" || job.DefaultBranch == j.Branch) {
		return nil
	}
	return folderRefusal(ledger.TemplateRefusedInvalid, fmt.Sprintf("the journal of this creation is of %s/%s, %s, at %s on branch %s, which this job does not name",
		j.Owner, j.ForgeName, j.Visibility, j.Path, j.Branch))
}

// check is what the checks found when the creation started, as its journal keeps it.
func (j creationJournal) check() ledger.CreationCheck {
	answer := ledger.CreationCheck{Branch: j.Branch}
	if j.Account != nil {
		answer.URL = ledger.CreatedURL(j.Account.GitProtocol, j.Owner, j.ForgeName)
		answer.Login = j.Account.Login
	}
	return answer
}

func creationJournalPath(rt *TemplateRuntime, key string) string {
	return filepath.Join(rt.StateDir, "repository-creations", key+".json")
}

// readCreation returns a creation's journal, and whether it has one.
func readCreation(rt *TemplateRuntime, key string) (creationJournal, bool, error) {
	at := creationJournalPath(rt, key)
	raw, err := os.ReadFile(at)
	if errors.Is(err, fs.ErrNotExist) {
		return creationJournal{}, false, nil
	}
	if err != nil {
		return creationJournal{}, false, err
	}
	var journal creationJournal
	if err := json.Unmarshal(raw, &journal); err != nil {
		return creationJournal{}, false, fmt.Errorf("%s: %w", at, err)
	}
	return journal, true, nil
}

// saveCreation replaces a creation's journal through a rename, so a crash leaves the old one or the new one.
func saveCreation(rt *TemplateRuntime, key string, journal creationJournal) error {
	raw, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	at := creationJournalPath(rt, key)
	dir := filepath.Dir(at)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "."+key+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if _, err := temp.Write(append(raw, '\n')); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), at)
}
