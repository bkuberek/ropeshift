package cli

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/projectfiles"
)

// plannedStep is the outcome a dry run gives each step it lists: one it would run and did not.
const plannedStep = "planned"

// newDryRunDetail is what the help of every `new` command says of --dry-run.
const newDryRunDetail = " With --dry-run it prints what it would write, leave, commit, create, and push, and writes " +
	"nothing: it creates no folder, runs no gh and no git command that changes a repository, and adds no remote."

// dryRunFlag adds --dry-run to a `new` command's flags.
func dryRunFlag(set *flag.FlagSet) *bool {
	return set.Bool("dry-run", false, "print what would be written, committed, and created, and write and run nothing")
}

// dryRunNew prints what a run for the folder would do, with the same text and the same --json document as a run,
// marked as a dry run. It writes nothing and runs no gh. A request is the first commit and remotes the command line
// or the answers ask for, and is nil for a plain write. When the request asks for a remote, the dry run asks git
// which clone holds the folder, as a run does, with no hook and no file system monitor, and refuses as the run does
// when git is too old or the folder makes no repository of its own.
func (c *invocation) dryRunNew(shape projectfiles.Shape, folder, project string, data projectfiles.Data, request *ledger.TemplateWriteRequest) int {
	files, err := projectfiles.Files(shape, data, renderTemplate)
	if errors.Is(err, projectfiles.ErrInvalid) {
		return c.usageError("%v", err)
	}
	if err != nil {
		return c.finish(err, nil, nil)
	}
	if request != nil && request.AsksRemote() {
		if code := c.requireGit(); code != ExitOK {
			return code
		}
		needs, err := c.needsRepositoryAt(request.Path)
		if err != nil {
			return c.finish(plainErr(err), nil, nil)
		}
		if refusal := request.RefuseNoOwnRepository(needs); refusal != nil {
			return c.refusedNew(*refusal)
		}
	}
	result := newResult{Path: folder, Project: data.Project, Written: []string{}, Left: []string{}, DryRun: true}
	for _, file := range files {
		_, err := os.Lstat(filepath.Join(folder, filepath.FromSlash(file.Path)))
		switch {
		case err == nil:
			result.Left = append(result.Left, file.Path)
		case errors.Is(err, fs.ErrNotExist):
			result.Written = append(result.Written, file.Path)
		default:
			return c.finish(plainErr(err), nil, nil)
		}
	}
	if request != nil && request.FirstCommit {
		result.Commands = projectfiles.Commands(shape, folder, project, data.Repositories, false)
		result.Repositories = plannedRepositories(*request, data.Project)
	} else {
		result.Commands = projectfiles.CommandsStaging(shape, folder, project, data.Repositories, result.Written, "")
	}
	return c.finish(nil, result, result.text)
}

// plannedRepositories lists the repositories a request makes, the folder's own first and then each new code
// repository, each with the steps a run would take.
func plannedRepositories(request ledger.TemplateWriteRequest, name string) []ledger.RepositoryReport {
	out := []ledger.RepositoryReport{plannedRepository(name, ".", path.Base(request.Path), request.Remote)}
	for _, repository := range request.Repositories {
		if repository.New {
			out = append(out, plannedRepository(repository.Name, projectfiles.CodePath(repository.Name), repository.Name, repository.ProjectRemote()))
		}
	}
	return out
}

// plannedRepository is one repository's plan: the steps that give it its remote.
func plannedRepository(name, rel, forgeName string, remote *ledger.ProjectRemote) ledger.RepositoryReport {
	report := ledger.RepositoryReport{Name: name, Path: rel}
	switch {
	case remote == nil:
		return report
	case remote.Create != nil:
		report.Remote = ledger.GitHubHost + "/" + remote.Create.Owner + "/" + forgeName
		report.Steps = append(report.Steps, ledger.RemoteStep{Step: ledger.RemoteStepCreate, Outcome: plannedStep, Detail: remote.Create.Visibility})
	default:
		report.Remote = remote.URL
	}
	report.Steps = append(report.Steps,
		ledger.RemoteStep{Step: ledger.RemoteStepOrigin, Outcome: plannedStep},
		ledger.RemoteStep{Step: ledger.RemoteStepPush, Outcome: plannedStep})
	return report
}

// askGitHubDry is askGitHub for a dry run, which runs no gh and so reads no account: it asks for the owner, the
// visibility, and an owner for each code repository, and checks none of them against the account.
func (c *invocation) askGitHubDry(in *answers, request *ledger.TemplateWriteRequest) (int, error) {
	owner, err := in.ask("Owner:", "")
	if err != nil {
		return 0, err
	}
	if err := ledger.CheckOwner(owner); err != nil {
		return 0, err
	}
	visibility, err := in.ask("Private or public? [private]", ledger.VisibilityPrivate)
	if err != nil {
		return 0, err
	}
	visibility = strings.ToLower(visibility)
	create := func(owner string) *ledger.RemoteCreate {
		return &ledger.RemoteCreate{Forge: ledger.ForgeGitHub, Owner: owner, Visibility: visibility}
	}
	request.Remote = &ledger.ProjectRemote{Create: create(owner)}
	creates := 1
	for i := range request.Repositories {
		own, err := in.ask(fmt.Sprintf("Owner for %s [%s]:", request.Repositories[i].Name, owner), owner)
		if err != nil {
			return 0, err
		}
		request.Repositories[i].Create = create(own)
		creates++
	}
	return creates, nil
}
