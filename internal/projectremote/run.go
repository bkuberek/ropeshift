package projectremote

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
)

// Files returns the files the job writes, the folder's own and those of each new code repository at repos/<name>,
// rendered with the remote URLs the progress knows. A repository created on GitHub has no URL before the checks, so
// the paths are the same before and after them and only the text of the manifest and the Makefile differs.
func Files(rt Runtime, job Job, prog *Progress) ([]projectfiles.File, error) {
	return projectfiles.Files(projectfiles.Shape(job.Request.Shape), job.data(prog), rt.Render)
}

// data is what every file renders over: the project's name, and each code repository with the remote its entry
// carries. A new repository's remote is its URL, or the URL of the repository the run creates, or empty for none.
func (j Job) data(prog *Progress) projectfiles.Data {
	data := projectfiles.Data{Project: j.Name()}
	for _, r := range j.Request.Repositories {
		code := projectfiles.CodeRepository{Name: r.Name, Remote: r.Remote, New: r.New}
		if r.New {
			code.Remote = urlOf(unit{name: r.Name, ghName: r.Name, remote: r.ProjectRemote()}, prog)
		}
		data.Repositories = append(data.Repositories, code)
	}
	return data
}

// urlOf is the URL a repository's origin is: the URL the request names, or the URL of the repository the run
// creates, as the person's gh git protocol names it. It is empty for a repository with no remote, and for a created
// one before the checks have read the protocol.
func urlOf(u unit, prog *Progress) string {
	switch {
	case u.remote == nil:
		return ""
	case u.remote.URL != "":
		return u.remote.URL
	case prog == nil || prog.Account == nil:
		return ""
	}
	return ledger.CreatedURL(prog.Account.GitProtocol, u.remote.Create.Owner, u.ghName)
}

// clip bounds a tool's message to what a report carries.
func clip(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > maxDetail {
		text = text[:maxDetail]
	}
	return text
}

// runner is one run of Finish.
type runner struct {
	rt      Runtime
	job     Job
	prog    *Progress
	save    func() error
	changed bool
}

func (r *runner) persist() error {
	if r.save == nil {
		return nil
	}
	return r.save()
}

// Finish writes the folder's files, makes each first commit, and gives each repository its remote, as the progress
// says is left to do, and reports what it did. A failed step ends that repository's steps and marks the rest
// skipped, and the run goes on to the next repository; nothing local is removed and no repository created is
// deleted. A refusal is a report like any other, and an error is what keeps the run from being decided, so the
// caller runs it again.
func Finish(ctx context.Context, rt Runtime, job Job, prog *Progress, save func() error) (ledger.TemplateWriteReport, error) {
	r := &runner{rt: rt, job: job, prog: prog, save: save}
	files, err := Files(rt, job, prog)
	if errors.Is(err, projectfiles.ErrInvalid) {
		return *refusal(ledger.TemplateRefusedInvalid, "the request names no shape this worker can write", ""), nil
	}
	if err != nil {
		return ledger.TemplateWriteReport{}, err
	}
	folderFiles := make([]ports.FolderFile, 0, len(files))
	paths := make([]string, 0, len(files))
	for _, file := range files {
		folderFiles = append(folderFiles, ports.FolderFile{Path: file.Path, Text: file.Text})
		paths = append(paths, file.Path)
	}
	id, err := rt.Folders.Write(ctx, job.Request.Path, folderFiles, ports.FolderID(prog.Folder()))
	if errors.Is(err, ports.ErrFolderChanged) {
		return changedReport(), nil
	}
	if err != nil {
		return ledger.TemplateWriteReport{}, fmt.Errorf("writing the files: %w", err)
	}
	if prog.Folder() == "" {
		prog.unit(ownPath).Folder = string(id)
		if err := r.persist(); err != nil {
			return ledger.TemplateWriteReport{}, err
		}
	}
	units := job.units()
	if prog.Commit {
		for _, u := range append(slices.Clone(units[1:]), units[0]) {
			if err := r.commit(ctx, u); err != nil {
				return ledger.TemplateWriteReport{}, err
			}
		}
		if r.changed {
			return changedReport(), nil
		}
	}
	for _, u := range units {
		if u.remote == nil {
			continue
		}
		if err := r.remote(ctx, u); err != nil {
			return ledger.TemplateWriteReport{}, err
		}
	}
	return r.report(ctx, units, paths)
}

func changedReport() ledger.TemplateWriteReport {
	return *refusal(ledger.TemplateRefusedChanged, "the folder is no longer the one this worker wrote: it was swapped for a link or for another folder", "")
}

// commit makes one repository's first commit, once. A commit git refuses keeps the files and says why.
func (r *runner) commit(ctx context.Context, u unit) error {
	up := r.prog.unit(u.rel)
	if up.Committed {
		return nil
	}
	id := ports.FolderID(up.Folder)
	if u.rel != ownPath && up.Folder == "" {
		var err error
		if id, err = r.rt.Folders.Write(ctx, u.abs, nil, ""); err != nil {
			return fmt.Errorf("reading the folder %s: %w", u.rel, err)
		}
		up.Folder = string(id)
	}
	resumed := up.Initialized
	if !resumed {
		// The journal says the run is about to run git init before it does, so a crash after it finds its own repository.
		up.Initialized = true
		if err := r.persist(); err != nil {
			return err
		}
	}
	commit, err := r.rt.Folders.FirstCommit(ctx, u.abs, u.message, resumed, id)
	switch {
	case errors.Is(err, ports.ErrFolderChanged):
		r.changed = true
		return nil
	case err != nil:
		up.CommitError = err.Error()
	default:
		up.Commit = commit
	}
	up.Committed = true
	return r.persist()
}

// remote runs the steps that give one repository its remote: create, origin, push. Each is journaled before and after
// it runs, and one a restart finds started is checked before it runs again.
func (r *runner) remote(ctx context.Context, u unit) error {
	up := r.prog.unit(u.rel)
	names := stepNames(u)
	if up.Commit == "" {
		for _, name := range names {
			if sp := up.step(name); sp.State == "" {
				sp.State, sp.Detail = ledger.RemoteStepSkipped, "the repository has no first commit to push"
			}
		}
		return r.persist()
	}
	url := urlOf(u, r.prog)
	failed := false
	for _, name := range names {
		sp := up.step(name)
		switch sp.State {
		case ledger.RemoteStepDone, ledger.RemoteStepSkipped:
			continue
		case ledger.RemoteStepFailed:
			failed = true
			continue
		}
		if failed {
			sp.State, sp.Detail = ledger.RemoteStepSkipped, "an earlier step failed"
			continue
		}
		if sp.State == stepStarted && r.verify(ctx, u, name, url) {
			sp.State, sp.Detail = ledger.RemoteStepDone, ""
			if err := r.persist(); err != nil {
				return err
			}
			continue
		}
		sp.State, sp.Detail = stepStarted, ""
		if err := r.persist(); err != nil {
			return err
		}
		if err := r.runStep(ctx, u, name, url); err != nil {
			sp.State, sp.Detail = ledger.RemoteStepFailed, clip(err.Error())
			failed = true
		} else {
			sp.State = ledger.RemoteStepDone
		}
		if err := r.persist(); err != nil {
			return err
		}
	}
	return r.persist()
}

// stepNames are the steps a repository's remote takes, in order.
func stepNames(u unit) []string {
	if u.remote != nil && u.remote.Create != nil {
		return []string{ledger.RemoteStepCreate, ledger.RemoteStepOrigin, ledger.RemoteStepPush}
	}
	return []string{ledger.RemoteStepOrigin, ledger.RemoteStepPush}
}

// runStep runs one step, within the limit one step has. git never waits on a prompt.
func (r *runner) runStep(ctx context.Context, u unit, name, url string) error {
	ctx, cancel := context.WithTimeout(ctx, stepLimit)
	defer cancel()
	switch name {
	case ledger.RemoteStepCreate:
		create := u.remote.Create
		return r.rt.Forge.Create(ctx, ledger.GitHubHost, ports.CreateRepository{Owner: create.Owner, Name: u.ghName, Public: create.Public()})
	case ledger.RemoteStepOrigin:
		return r.rt.Folders.AddRemote(ctx, u.abs, url)
	}
	state, err := r.rt.Folders.Remote(ctx, u.abs)
	if err != nil {
		return err
	}
	if state.Branch == "" {
		return errors.New("the repository's HEAD names no branch")
	}
	return r.rt.Folders.Push(ctx, u.abs, ports.PushSpec{Branch: state.Branch, CredentialHelper: r.helper(u)})
}

// helper is the `gh` that serves git's credentials for a push of a created repository over https, as `gh` does for
// its own pushes. A repository whose remote is a URL pushes with the person's own git credentials.
func (r *runner) helper(u unit) string {
	if u.remote == nil || u.remote.Create == nil || r.prog.Account == nil || r.prog.Account.GitProtocol == "ssh" {
		return ""
	}
	return r.prog.Account.Binary
}

// verify says a step a crash left started has in fact run: the repository exists, origin names the URL, or origin
// holds the commit.
func (r *runner) verify(ctx context.Context, u unit, name, url string) bool {
	ctx, cancel := context.WithTimeout(ctx, stepLimit)
	defer cancel()
	switch name {
	case ledger.RemoteStepCreate:
		exists, err := r.rt.Forge.Exists(ctx, ledger.GitHubHost, u.remote.Create.Owner, u.ghName)
		return err == nil && exists
	case ledger.RemoteStepOrigin:
		state, err := r.rt.Folders.Remote(ctx, u.abs)
		return err == nil && state.Origin == url
	}
	state, err := r.rt.Folders.Remote(ctx, u.abs)
	return err == nil && state.Head != "" && state.RemoteHead == state.Head
}

// report is what the run did: the files, each repository with its commit and steps, the commands that register the
// folder, and the commands that finish what did not run.
func (r *runner) report(ctx context.Context, units []unit, paths []string) (ledger.TemplateWriteReport, error) {
	own := r.prog.unit(ownPath)
	report := ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeWritten, Project: r.job.Name(), Files: paths, Commit: own.Commit, CommitError: own.CommitError,
	}
	after, err := r.rt.Folders.Inspect(ctx, r.job.Request.Path)
	if err != nil {
		return ledger.TemplateWriteReport{}, err
	}
	report.Clone = report.Commit != "" && after.CloneTop != "" && after.CloneTop == after.Real
	var commits []string
	for _, u := range units[1:] {
		if r.prog.NeedsRepository && r.prog.unit(u.rel).Commit == "" {
			commits = append(commits, projectfiles.FirstCommitCommands(u.abs, u.name)...)
		}
	}
	if r.prog.NeedsRepository && own.Commit == "" {
		commits = append(commits, projectfiles.FirstCommitCommands(r.job.Request.Path, r.job.commitName())...)
	}
	codes := r.job.data(r.prog).Repositories
	report.Commands = append(commits, projectfiles.Commands(projectfiles.Shape(r.job.Request.Shape), r.job.Request.Path, r.job.commitName(), codes, false)...)
	if r.prog.Commit {
		for _, u := range units {
			report.Repositories = append(report.Repositories, r.repositoryReport(u))
			report.Remaining = append(report.Remaining, r.remaining(ctx, u)...)
		}
	}
	return report, nil
}

func (r *runner) repositoryReport(u unit) ledger.RepositoryReport {
	up := r.prog.unit(u.rel)
	out := ledger.RepositoryReport{Name: u.name, Path: u.rel, Commit: up.Commit, CommitError: up.CommitError, Remote: urlOf(u, r.prog)}
	if u.remote == nil {
		return out
	}
	for _, name := range stepNames(u) {
		sp := up.step(name)
		state, detail := sp.State, sp.Detail
		if state == "" || state == stepStarted {
			state, detail = ledger.RemoteStepSkipped, "the step did not run"
		}
		out.Steps = append(out.Steps, ledger.RemoteStep{Step: name, Outcome: state, Detail: detail})
	}
	return out
}

// remaining are the exact commands that finish the steps of one repository that did not run.
func (r *runner) remaining(ctx context.Context, u unit) []string {
	if u.remote == nil {
		return nil
	}
	up := r.prog.unit(u.rel)
	url := urlOf(u, r.prog)
	folder := projectfiles.Quote(u.abs)
	var out []string
	for _, name := range stepNames(u) {
		if up.step(name).State == ledger.RemoteStepDone {
			continue
		}
		switch name {
		case ledger.RemoteStepCreate:
			visibility := "--private"
			if u.remote.Create.Public() {
				visibility = "--public"
			}
			out = append(out, fmt.Sprintf("gh repo create %s/%s %s", u.remote.Create.Owner, u.ghName, visibility))
		case ledger.RemoteStepOrigin:
			out = append(out, "git -C "+folder+" remote add origin "+url)
		case ledger.RemoteStepPush:
			branch := "main"
			if state, err := r.rt.Folders.Remote(ctx, u.abs); err == nil && state.Branch != "" {
				branch = state.Branch
			}
			if r.helper(u) != "" || (u.remote.Create != nil && strings.HasPrefix(url, "https://")) {
				out = append(out, "gh auth setup-git")
			}
			out = append(out, "git -C "+folder+" push --set-upstream origin "+branch)
		}
	}
	return out
}
