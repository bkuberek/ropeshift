// Package projectremote makes a new project's folder on one machine: it checks the person's forge account, writes
// the shape's files and the files of each new code repository, makes each repository's first commit, and gives each
// repository its remote, adding a URL as origin or creating the repository on GitHub, and pushing (FR-TPL-6). A
// worker writing for a member's web request and the command line's `ropeshift new` both run it, so both make the
// same files, commits, and steps and report them in one shape.
//
// It reaches the disk, git, and `gh` only through the ports it is given, and it keeps what it has done in a
// Progress its caller saves, so a crash between two steps repeats none of them.
package projectremote

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
)

// stepLimit is how long one step may take: a create, an origin, or a push.
const stepLimit = 2 * time.Minute

// maxDetail bounds what a step's failure carries into a report.
const maxDetail = 2000

// ownPath is where the folder's own repository sits, relative to the folder.
const ownPath = "."

// Runtime is what a run reaches the machine through.
type Runtime struct {
	Folders ports.ProjectFolders
	Forge   ports.ForgeAccounts
	Render  projectfiles.Render
	// Machine names the machine in what a person reads: the worker's name, or "this machine" on the command line.
	Machine string
}

// Job is one folder to make.
type Job struct {
	// Request is what the member asked for.
	Request ledger.TemplateWriteRequest
	// NeedsRepository says the folder needs a repository of its own for its first commit: no clone holds it, or the
	// clone that holds it ignores it. A folder a clone tracks has its files committed with that clone.
	NeedsRepository bool
}

// Name is the project's name: the folder's base name made a project name.
func (j Job) Name() string { return projectfiles.NameOf(path.Base(j.Request.Path)) }

// commitName is the name the first commit's message gives: the project the commands register in.
func (j Job) commitName() string {
	if j.Request.Project != "" {
		return j.Request.Project
	}
	return j.Name()
}

// Account is what the checks learned of the person's forge account, which the run needs again after a restart.
type Account struct {
	Login       string `json:"login,omitempty"`
	GitProtocol string `json:"git_protocol,omitempty"`
	// Binary is the absolute path of the `gh` the lookup found, which an https push names as git's credential helper.
	Binary string `json:"binary,omitempty"`
}

// The states of a step in a Progress, besides done, failed, and skipped, which a report names too.
const stepStarted = "started"

// StepProgress is where one step of a repository stands.
type StepProgress struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// UnitProgress is what a run has done for one repository.
type UnitProgress struct {
	// Folder names the repository's folder as the disk said of it when it was written, so a resume is held to it.
	Folder string `json:"folder,omitempty"`
	// Initialized says the run was about to run git init, so a .git it finds on a resume is its own.
	Initialized bool `json:"initialized,omitempty"`
	// Committed says the first commit was tried, and Commit and CommitError what came of it.
	Committed   bool                     `json:"committed,omitempty"`
	Commit      string                   `json:"commit,omitempty"`
	CommitError string                   `json:"commit_error,omitempty"`
	Steps       map[string]*StepProgress `json:"steps,omitempty"`
}

// Progress is what a run decided and did, which the caller keeps so a crash repeats nothing. The zero value is a run
// that has not started.
type Progress struct {
	// Started says the checks passed and the decisions below stand.
	Started bool `json:"started,omitempty"`
	// Commit says a first commit is made in each repository, and NeedsRepository that the folder needs a repository of
	// its own. Both were decided before the first write.
	Commit          bool     `json:"commit"`
	NeedsRepository bool     `json:"needs_repository"`
	Account         *Account `json:"account,omitempty"`
	// Listed says Left was read before the first write, and Left holds the files of the shape that the folder already held
	// then, which the run leaves as they are and the first commit does not add.
	Listed bool     `json:"listed,omitempty"`
	Left   []string `json:"left,omitempty"`
	// Units are the repositories, by their path in the folder: "." for the folder's own, and repos/<name>.
	Units map[string]*UnitProgress `json:"units,omitempty"`
}

func (p *Progress) unit(rel string) *UnitProgress {
	if p.Units == nil {
		p.Units = map[string]*UnitProgress{}
	}
	if p.Units[rel] == nil {
		p.Units[rel] = &UnitProgress{}
	}
	return p.Units[rel]
}

// KeepEarlierRecord takes the folder identity and the git init that a journal written before each repository had a
// record of its own holds at the top level, as the folder's own repository's, unless that record already holds them.
func (p *Progress) KeepEarlierRecord(folder string, initialized bool) {
	own := p.unit(ownPath)
	if own.Folder == "" {
		own.Folder = folder
	}
	own.Initialized = own.Initialized || initialized
}

// Folder is the identity of the folder the run wrote, or empty before it wrote it.
func (p *Progress) Folder() string { return p.unit(ownPath).Folder }

func (u *UnitProgress) step(name string) *StepProgress {
	if u.Steps == nil {
		u.Steps = map[string]*StepProgress{}
	}
	if u.Steps[name] == nil {
		u.Steps[name] = &StepProgress{}
	}
	return u.Steps[name]
}

// unit is one repository of a job: the folder's own, or a new code repository.
type unit struct {
	// name is the repository's name in the report, and ghName the name it has on GitHub when it is created.
	name   string
	ghName string
	rel    string
	abs    string
	remote *ledger.ProjectRemote
	// message is the first commit's message.
	message string
}

// units lists the repositories a job makes, the folder's own first, then each new code repository in request order.
func (j Job) units() []unit {
	own := unit{name: j.Name(), ghName: path.Base(j.Request.Path), rel: ownPath, abs: j.Request.Path, remote: j.Request.Remote, message: "chore: start " + j.commitName()}
	out := []unit{own}
	for _, repository := range j.Request.Repositories {
		if !repository.New {
			continue
		}
		out = append(out, unit{
			name: repository.Name, ghName: repository.Name, rel: projectfiles.CodePath(repository.Name),
			abs: j.Request.Path + "/" + projectfiles.CodePath(repository.Name), remote: repository.ProjectRemote(),
			message: "chore: start " + repository.Name,
		})
	}
	return out
}

func (j Job) asked() bool {
	return slices.ContainsFunc(j.units(), func(u unit) bool { return u.remote != nil })
}

// Start runs the checks that come before anything is written and records the decisions that stand across a restart.
// A refusal is a report the caller returns as it is: nothing is written and nothing is created. An error is a
// failure to ask, which the caller tries again.
func Start(ctx context.Context, rt Runtime, job Job, prog *Progress) (*ledger.TemplateWriteReport, error) {
	if prog.Started {
		return nil, nil
	}
	for _, u := range job.units() {
		if u.remote != nil && u.remote.Create != nil {
			if err := checkRepositoryName(u.ghName); err != nil {
				return refusal(ledger.TemplateRefusedInvalid, "repository "+u.name+": "+err.Error(), ""), nil
			}
		}
		if u.remote != nil && u.remote.URL != "" {
			if err := projectfiles.CheckRemote(u.remote.URL); err != nil {
				return refusal(ledger.TemplateRefusedInvalid, "repository "+u.name+": "+err.Error(), ""), nil
			}
		}
	}
	if job.asked() && !job.NeedsRepository {
		return refusal(ledger.TemplateRefusedNoOwnRepository, "a remote is added only to a repository ropeshift makes, and a clone this folder lies in holds its files", ""), nil
	}
	if creates := createdUnits(job); len(creates) > 0 {
		account, refused, err := check(ctx, rt, creates)
		if err != nil || refused != nil {
			return refused, err
		}
		prog.Account = account
	}
	prog.Commit = job.Request.FirstCommit && job.NeedsRepository
	prog.NeedsRepository = job.NeedsRepository
	prog.Started = true
	return nil, nil
}

// maxRepositoryName is the longest name a GitHub repository has.
const maxRepositoryName = 100

// checkRepositoryName refuses a name GitHub cannot give a repository: one of 1 to 100 letters, digits, dots,
// underscores, and hyphens, which is neither "." nor "..".
func checkRepositoryName(name string) error {
	if name == "" || len(name) > maxRepositoryName || name == "." || name == ".." || strings.ContainsFunc(name, func(r rune) bool { return !nameRune(r) }) {
		return fmt.Errorf("a GitHub repository name has 1 to 100 letters, digits, dots, underscores, and hyphens and is not . or .., and %q is not one", name)
	}
	return nil
}

func nameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
}

func refusal(reason, detail, remedy string) *ledger.TemplateWriteReport {
	return &ledger.TemplateWriteReport{Outcome: ledger.TemplateOutcomeRefused, Reason: reason, Detail: detail, Remedy: remedy}
}

func createdUnits(job Job) []unit {
	var out []unit
	for _, u := range job.units() {
		if u.remote != nil && u.remote.Create != nil {
			out = append(out, u)
		}
	}
	return out
}

// The commands a person runs on the worker's machine to fix a refused check.
const (
	loginCommand = "gh auth login --hostname " + ledger.GitHubHost
)

func refreshCommand(scope string) string {
	return "gh auth refresh --hostname " + ledger.GitHubHost + " --scopes " + scope
}

// readAccount reads the sign-in, and a runtime with no forge reads as no `gh`.
func readAccount(ctx context.Context, rt Runtime) (ports.ForgeAccount, error) {
	if rt.Forge == nil {
		return ports.ForgeAccount{State: ledger.ForgeAccountMissing}, nil
	}
	return rt.Forge.Account(ctx, ledger.GitHubHost)
}

// check runs the checks a created repository needs, in order, and refuses with the first that fails: `gh` is
// installed, new enough, and signed in; its token holds the scope; each owner is the account or one of its
// organizations; and no repository exists at an owner and name already.
func check(ctx context.Context, rt Runtime, creates []unit) (*Account, *ledger.TemplateWriteReport, error) {
	account, err := readAccount(ctx, rt)
	if err != nil {
		return nil, nil, err
	}
	switch account.State {
	case ledger.ForgeAccountMissing:
		return nil, refusal(ledger.TemplateRefusedGHMissing,
			fmt.Sprintf("Install the GitHub CLI on %s from https://cli.github.com/, then run `%s` there", rt.Machine, loginCommand), loginCommand), nil
	case ledger.ForgeAccountOutdated:
		return nil, refusal(ledger.TemplateRefusedGHOutdated, fmt.Sprintf("Upgrade the GitHub CLI on %s: its `gh auth status` has no --json", rt.Machine), ""), nil
	case ledger.ForgeAccountSignedOut:
		return nil, refusal(ledger.TemplateRefusedGHSignedOut, fmt.Sprintf("Run `%s` on %s", loginCommand, rt.Machine), loginCommand), nil
	}
	allPublic := !slices.ContainsFunc(creates, func(u unit) bool { return !u.remote.Create.Public() })
	if len(account.Scopes) > 0 && !slices.Contains(account.Scopes, "repo") && (!allPublic || !slices.Contains(account.Scopes, "public_repo")) {
		return nil, refusal(ledger.TemplateRefusedGHScope, fmt.Sprintf("Run `%s` on %s", refreshCommand("repo"), rt.Machine), refreshCommand("repo")), nil
	}
	if refused, err := checkOwners(ctx, rt, account, creates); refused != nil || err != nil {
		return nil, refused, err
	}
	for _, u := range creates {
		create := u.remote.Create
		exists, err := rt.Forge.Exists(ctx, ledger.GitHubHost, create.Owner, u.ghName)
		if err != nil {
			return nil, nil, err
		}
		if exists {
			return nil, refusal(ledger.TemplateRefusedRemoteExists,
				fmt.Sprintf("%s/%s already exists on GitHub; choose another name or add its URL as the remote", create.Owner, u.ghName), ""), nil
		}
	}
	return &Account{Login: account.Login, GitProtocol: account.GitProtocol, Binary: account.Binary}, nil, nil
}

// checkOwners refuses an owner that is neither the signed-in account nor one of its organizations. The organizations
// are listed only when an owner is not the account itself.
func checkOwners(ctx context.Context, rt Runtime, account ports.ForgeAccount, creates []unit) (*ledger.TemplateWriteReport, error) {
	available := []string{account.Login}
	var listed bool
	for _, u := range creates {
		owner := u.remote.Create.Owner
		if slices.ContainsFunc(available, func(o string) bool { return strings.EqualFold(o, owner) }) {
			continue
		}
		if !listed {
			orgs, err := rt.Forge.Owners(ctx, ledger.GitHubHost)
			if errors.Is(err, ports.ErrForgeScope) {
				return refusal(ledger.TemplateRefusedGHScope, fmt.Sprintf("Run `%s` on %s to list its organizations", refreshCommand("read:org"), rt.Machine), refreshCommand("read:org")), nil
			}
			if err != nil {
				return nil, err
			}
			available, listed = append(available, orgs...), true
		}
		if !slices.ContainsFunc(available, func(o string) bool { return strings.EqualFold(o, owner) }) {
			return refusal(ledger.TemplateRefusedOwner, fmt.Sprintf("%s is neither the signed-in account nor one of its organizations; the owners available are %s", owner, strings.Join(available, ", ")), ""), nil
		}
	}
	return nil, nil
}

// ReadAccount reads what a forge account read reports: whether `gh` can create repositories, and under which owners.
// The scope check passes with either repo or public_repo, because the visibility is not chosen yet.
func ReadAccount(ctx context.Context, rt Runtime) (ledger.ForgeAccountReport, error) {
	account, err := readAccount(ctx, rt)
	if err != nil {
		return ledger.ForgeAccountReport{}, err
	}
	report := ledger.ForgeAccountReport{State: account.State, Login: account.Login, GitProtocol: account.GitProtocol}
	switch account.State {
	case ledger.ForgeAccountMissing:
		report.Detail = fmt.Sprintf("Install the GitHub CLI on %s from https://cli.github.com/, then run `%s` there", rt.Machine, loginCommand)
		report.Remedy = loginCommand
		return report, nil
	case ledger.ForgeAccountOutdated:
		report.Detail = fmt.Sprintf("Upgrade the GitHub CLI on %s: its `gh auth status` has no --json", rt.Machine)
		return report, nil
	case ledger.ForgeAccountSignedOut:
		report.Detail = fmt.Sprintf("Run `%s` on %s", loginCommand, rt.Machine)
		report.Remedy = loginCommand
		return report, nil
	}
	if len(account.Scopes) > 0 && !slices.Contains(account.Scopes, "repo") && !slices.Contains(account.Scopes, "public_repo") {
		report.State = ledger.ForgeAccountScope
		report.Detail = fmt.Sprintf("Run `%s` on %s", refreshCommand("repo"), rt.Machine)
		report.Remedy = refreshCommand("repo")
		return report, nil
	}
	orgs, err := rt.Forge.Owners(ctx, ledger.GitHubHost)
	if errors.Is(err, ports.ErrForgeScope) {
		report.State = ledger.ForgeAccountScope
		report.Detail = fmt.Sprintf("Run `%s` on %s to list its organizations", refreshCommand("read:org"), rt.Machine)
		report.Remedy = refreshCommand("read:org")
		return report, nil
	}
	if err != nil {
		return ledger.ForgeAccountReport{}, err
	}
	report.State = ledger.ForgeAccountOK
	report.Owners = append([]string{account.Login}, orgs...)
	return report, nil
}
