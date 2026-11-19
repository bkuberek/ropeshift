package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
)

// project stands in for the machine's folders, git, and gh: it records each request and answers with a report.
type project struct {
	requests []ledger.TemplateWriteRequest
	needs    []bool
	report   ledger.TemplateWriteReport
	reads    int
	account  ledger.ForgeAccountReport
}

func (p *project) options(input string) cli.Options {
	opts := cli.Options{
		NewProject: func(_ context.Context, request ledger.TemplateWriteRequest, needs bool) (ledger.TemplateWriteReport, error) {
			p.requests, p.needs = append(p.requests, request), append(p.needs, needs)
			report := p.report
			if report.Outcome == "" {
				report = ledger.TemplateWriteReport{Outcome: ledger.TemplateOutcomeWritten, Files: []string{"AGENTS.md", "plan/goal.md"}, Commit: "abc123", Commands: []string{"ropeshift project create orchid"}}
			}
			return report, nil
		},
		ReadForgeAccount: func(context.Context) (ledger.ForgeAccountReport, error) {
			p.reads++
			return p.account, nil
		},
	}
	if input != "" {
		opts.Terminal = strings.NewReader(input)
	}
	return opts
}

func createGitHub(owner, visibility string) *ledger.RemoteCreate {
	return &ledger.RemoteCreate{Forge: ledger.ForgeGitHub, Owner: owner, Visibility: visibility}
}

// The flags of a script become the request a worker would be sent: the folder's own remote, and each new code
// repository's, with --create-remote for every repository that has none of its own.
func TestNewFlagsMakeTheRequestAWorkerWouldBeSent(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	cases := map[string]struct {
		args []string
		want ledger.TemplateWriteRequest
	}{
		"a monorepo with a URL": {
			[]string{"new", "monorepo", folder, "--remote", "git@example.com:orchid/orchid.git"},
			ledger.TemplateWriteRequest{Shape: "monorepo", Path: folder, FirstCommit: true, Remote: &ledger.ProjectRemote{URL: "git@example.com:orchid/orchid.git"}},
		},
		"a monorepo created on GitHub": {
			[]string{"new", "monorepo", folder, "--create-remote", "github:acme"},
			ledger.TemplateWriteRequest{Shape: "monorepo", Path: folder, FirstCommit: true, Remote: &ledger.ProjectRemote{Create: createGitHub("acme", "private")}},
		},
		"a public repository": {
			[]string{"new", "repository", folder, "--create-remote", "github:acme", "--public", "--project", "orchid"},
			ledger.TemplateWriteRequest{Shape: "repository", Path: folder, Project: "orchid", FirstCommit: true, Remote: &ledger.ProjectRemote{Create: createGitHub("acme", "public")}},
		},
		"only the first commit": {
			[]string{"new", "monorepo", folder, "--first-commit"},
			ledger.TemplateWriteRequest{Shape: "monorepo", Path: folder, FirstCommit: true},
		},
		"a workspace that creates every repository": {
			[]string{"new", "workspace", folder, "--create-remote", "github:acme", "--new-repository", "api", "--new-repository", "web=none",
				"--new-repository", "tools=https://example.com/tools.git", "--new-repository", "ops=github:ops-co", "--repository", "legacy=git@example.com:orchid/legacy.git"},
			ledger.TemplateWriteRequest{
				Shape: "workspace", Path: folder, FirstCommit: true, Remote: &ledger.ProjectRemote{Create: createGitHub("acme", "private")},
				Repositories: []ledger.TemplateWriteRepository{
					{Name: "legacy", Remote: "git@example.com:orchid/legacy.git"},
					{Name: "api", New: true, Create: createGitHub("acme", "private")},
					{Name: "web", New: true},
					{Name: "tools", New: true, URL: "https://example.com/tools.git"},
					{Name: "ops", New: true, Create: createGitHub("ops-co", "private")},
				},
			},
		},
		"a workspace with its own URL and created code repositories": {
			[]string{"new", "workspace", folder, "--remote", "https://example.com/orchid.git", "--create-remote", "github:acme", "--public", "--new-repository", "api"},
			ledger.TemplateWriteRequest{
				Shape: "workspace", Path: folder, FirstCommit: true, Remote: &ledger.ProjectRemote{URL: "https://example.com/orchid.git"},
				Repositories: []ledger.TemplateWriteRepository{{Name: "api", New: true, Create: createGitHub("acme", "public")}},
			},
		},
		"a new repository with no remote": {
			[]string{"new", "workspace", folder, "--new-repository", "api"},
			ledger.TemplateWriteRequest{Shape: "workspace", Path: folder, Repositories: []ledger.TemplateWriteRepository{{Name: "api", New: true}}},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := &project{}
			code, stdout, stderr := runNew(p.options(""), c.args...)
			if code != cli.ExitOK {
				t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
			}
			if len(p.requests) != 1 {
				t.Fatalf("%d requests", len(p.requests))
			}
			if got, want := mustJSON(t, p.requests[0]), mustJSON(t, c.want); got != want {
				t.Errorf("request\n got %s\nwant %s", got, want)
			}
			if !p.needs[0] {
				t.Error("a folder in no clone needs a repository of its own")
			}
		})
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// A script that asks for what no worker could make is a usage error, and nothing is written.
func TestNewFlagsThatAskForWhatCannotBeMadeAreUsageErrors(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	cases := map[string]struct {
		args []string
		says string
	}{
		"another forge":                    {[]string{"new", "monorepo", folder, "--create-remote", "gitlab:acme"}, "creating a repository on gitlab is not supported yet; ropeshift creates repositories on GitHub: --create-remote github:<owner>"},
		"another forge for one repository": {[]string{"new", "workspace", folder, "--new-repository", "api=gitlab:acme"}, "creating a repository on gitlab is not supported yet"},
		"a URL and a create on a monorepo": {[]string{"new", "monorepo", folder, "--remote", "https://example.com/a.git", "--create-remote", "github:acme"}, "give one"},
		"public with nothing to create":    {[]string{"new", "monorepo", folder, "--remote", "https://example.com/a.git", "--public"}, "--public"},
		"a bad owner":                      {[]string{"new", "monorepo", folder, "--create-remote", "github:-acme"}, "no GitHub account name"},
		"no owner":                         {[]string{"new", "monorepo", folder, "--create-remote", "github"}, "github:<owner>"},
		"a URL that runs":                  {[]string{"new", "monorepo", folder, "--remote", "ext::sh -c id"}, "whitespace"},
		"a code repository URL that runs":  {[]string{"new", "workspace", folder, "--new-repository", "api=ext::sh%20x"}, "remote helper"},
		"a name the ledger refuses":        {[]string{"new", "workspace", folder, "--new-repository", "a/b"}, "a/b"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := &project{}
			code, _, stderr := runNew(p.options(""), c.args...)
			if code != cli.ExitUsage || !strings.Contains(stderr, c.says) {
				t.Errorf("exit %d: %s, want a usage error that says %q", code, stderr, c.says)
			}
			if len(p.requests) != 0 {
				t.Error("a usage error ran a request")
			}
			if _, err := os.Stat(folder); !os.IsNotExist(err) {
				t.Errorf("a usage error made the folder: %v", err)
			}
		})
	}
}

// With no flag that asks for a commit or a remote, the command is the plain write: it runs no git command that changes a
// repository and never reaches the machine's folders through the request.
func TestNewWithNoRemoteFlagsRunsNoRequest(t *testing.T) {
	p := &project{}
	folder := filepath.Join(t.TempDir(), "orchid")
	if code, _, stderr := runNew(p.options(""), "new", "monorepo", folder); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if len(p.requests) != 0 {
		t.Errorf("a plain write ran %+v", p.requests)
	}
}

// A report that says a step failed is an error exit, with the commands that finish it, so a script notices.
func TestNewReportsWhatEachRepositoryDidAndExitsWithAnErrorForAFailedStep(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	p := &project{report: ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeWritten, Files: []string{"AGENTS.md"}, Commit: "abc123",
		Repositories: []ledger.RepositoryReport{{Name: "orchid", Path: ".", Commit: "abc123", Remote: "https://github.com/acme/orchid.git",
			Steps: []ledger.RemoteStep{{Step: "create", Outcome: "done"}, {Step: "origin", Outcome: "done"}, {Step: "push", Outcome: "failed", Detail: "remote: denied"}}}},
		Remaining: []string{"gh auth setup-git", "git -C " + folder + " push --set-upstream origin -- main"},
		Commands:  []string{"ropeshift project create orchid"},
	}}
	code, stdout, _ := runNew(p.options(""), "new", "monorepo", folder, "--create-remote", "github:acme")
	if code != cli.ExitError {
		t.Errorf("exit %d, want an error for the failed push", code)
	}
	for _, want := range []string{"push\tfailed\tremote: denied", "create\tdone", "To finish what did not run:", "gh auth setup-git", "push --set-upstream origin -- main", "ropeshift project create orchid"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the output lacks %q:\n%s", want, stdout)
		}
	}
	p = &project{report: p.report}
	code, stdout, _ = runNew(p.options(""), "new", "monorepo", folder, "--create-remote", "github:acme", "--json")
	var got struct {
		Repositories []ledger.RepositoryReport `json:"repositories"`
		Remaining    []string                  `json:"remaining"`
		Commit       string                    `json:"commit"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || code != cli.ExitError || len(got.Repositories) != 1 || got.Commit != "abc123" || len(got.Remaining) != 2 {
		t.Errorf("--json: exit %d, %+v, %v", code, got, err)
	}
}

// A refused check prints the reason and the command, writes nothing, and exits with an error.
func TestNewPrintsTheCommandThatFixesARefusedCheck(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	p := &project{report: ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedGHSignedOut,
		Detail: "Run `gh auth login --hostname github.com` on this machine", Remedy: "gh auth login --hostname github.com",
	}}
	code, _, stderr := runNew(p.options(""), "new", "monorepo", folder, "--create-remote", "github:acme")
	if code != cli.ExitError || !strings.Contains(stderr, "gh_signed_out") || !strings.Contains(stderr, "Run: gh auth login --hostname github.com") || !strings.Contains(stderr, "Nothing was written") {
		t.Errorf("exit %d: %s", code, stderr)
	}
}

// A files the folder already holds are left, and listed as left, with a remote asked for as without.
func TestNewListsTheFilesItLeftWhenARemoteIsAsked(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "AGENTS.md"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &project{}
	p.report = ledger.TemplateWriteReport{Outcome: ledger.TemplateOutcomeWritten, Files: []string{"AGENTS.md", "CLAUDE.md"}, Commit: "abc"}
	code, stdout, _ := runNew(p.options(""), "new", "monorepo", folder, "--first-commit")
	if code != cli.ExitOK || !strings.Contains(stdout, "left\tAGENTS.md\talready exists") || !strings.Contains(stdout, "wrote\tCLAUDE.md") {
		t.Errorf("exit %d:\n%s", code, stdout)
	}
}

// With no shape and no terminal, `ropeshift new` is a usage error that names the three shapes and the flags.
func TestNewWithNoShapeAndNoTerminalNamesTheShapesAndFlags(t *testing.T) {
	p := &project{}
	code, _, stderr := runNew(p.options(""), "new")
	if code != cli.ExitUsage {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"workspace", "monorepo", "repository", "--first-commit", "--remote", "--create-remote"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the usage error lacks %q: %s", want, stderr)
		}
	}
}

// `ropeshift new` on a terminal asks whether the project lives in one repository or several before any shape name, and
// writes what the answers say.
func TestNewAsksOneRepositoryOrSeveralAndWritesTheAnswers(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	p := &project{}
	code, stdout, stderr := runNew(p.options("one\ny\n1\n"), "new", folder)
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s%s", code, stdout, stderr)
	}
	if !strings.HasPrefix(stdout, "Will this project live in one repository, or in several?") || strings.Contains(strings.SplitN(stdout, "\n", 2)[0], "monorepo") {
		t.Errorf("the first question is %q", strings.SplitN(stdout, "\n", 2)[0])
	}
	if len(p.requests) != 1 || p.requests[0].Shape != "monorepo" || !p.requests[0].FirstCommit || p.requests[0].Remote != nil {
		t.Errorf("requests = %+v", p.requests)
	}

	p = &project{}
	code, stdout, stderr = runNew(p.options("several\napi\nweb\n\nn\n"), "new", folder)
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s%s", code, stdout, stderr)
	}
	want := []ledger.TemplateWriteRepository{{Name: "api", New: true}, {Name: "web", New: true}}
	if len(p.requests) != 1 || p.requests[0].Shape != "workspace" || p.requests[0].FirstCommit || !slices.Equal(p.requests[0].Repositories, want) {
		t.Errorf("requests = %+v", p.requests)
	}
}

// Choosing a URL for each repository asks for one per repository, the folder's own first.
func TestNewAsksAURLForEachRepository(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	p := &project{}
	input := "several\napi\nweb\n\ny\n2\nhttps://example.com/orchid.git\nhttps://example.com/api.git\n\n"
	if code, stdout, stderr := runNew(p.options(input), "new", folder); code != cli.ExitOK {
		t.Fatalf("exit %d: %s%s", code, stdout, stderr)
	}
	got := p.requests[0]
	if got.Remote == nil || got.Remote.URL != "https://example.com/orchid.git" || got.Repositories[0].URL != "https://example.com/api.git" || got.Repositories[1].URL != "" {
		t.Errorf("request = %+v", got)
	}
}

// Choosing GitHub runs the checks, lists the owners, asks for one, asks private or public, asks an owner for each code
// repository, and writes only after a yes.
func TestNewCreatesOnGitHubAfterTheOwnersAreChosenAndTheWriteIsConfirmed(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	account := ledger.ForgeAccountReport{State: ledger.ForgeAccountOK, Login: "me", Owners: []string{"me", "acme"}}
	input := "several\napi\nweb\n\ny\n3\n2\npublic\n\nme\ny\n"
	p := &project{account: account}
	code, stdout, stderr := runNew(p.options(input), "new", folder)
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s%s", code, stdout, stderr)
	}
	for _, want := range []string{"Remote: 1 none, 2 a URL for each repository, 3 create on GitHub", "Owners:", "Private or public? [private]", "Owner for api [acme]:", "Write the files and create 3 repositories on GitHub? [y/N]"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the dialogue lacks %q:\n%s", want, stdout)
		}
	}
	got := p.requests[0]
	if got.Remote.Create.Owner != "acme" || got.Remote.Create.Visibility != "public" || got.Repositories[0].Create.Owner != "acme" || got.Repositories[1].Create.Owner != "me" {
		t.Errorf("request = %+v", got)
	}

	p = &project{account: account}
	code, stdout, _ = runNew(p.options("one\ny\n3\n1\n\ni\nn\n"), "new", folder)
	if code != cli.ExitOK || len(p.requests) != 0 || !strings.Contains(stdout, "Nothing was written.") {
		t.Errorf("declined: exit %d, requests %+v\n%s", code, p.requests, stdout)
	}
}

// A check that fails stops the dialogue with the command for the person, before anything is asked of the owners.
func TestNewStopsAtAFailedCheckWithTheCommand(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	p := &project{account: ledger.ForgeAccountReport{State: ledger.ForgeAccountSignedOut, Detail: "Run `gh auth login --hostname github.com` on this machine", Remedy: "gh auth login --hostname github.com"}}
	code, stdout, stderr := runNew(p.options("one\ny\n3\n"), "new", folder)
	if code != cli.ExitError || !strings.Contains(stderr, "gh_signed_out") || !strings.Contains(stderr, "Run: gh auth login --hostname github.com") {
		t.Errorf("exit %d: %s", code, stderr)
	}
	if strings.Contains(stdout, "Owners:") || len(p.requests) != 0 {
		t.Errorf("the dialogue went on: %s, %+v", stdout, p.requests)
	}
}

// A script never answers by silence: a terminal that closes mid-dialogue is an error.
func TestNewRefusesAnAnswerThatIsNotThere(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	p := &project{}
	if code, _, _ := runNew(p.options("several\napi\n"), "new", folder); code != cli.ExitUsage || len(p.requests) != 0 {
		t.Errorf("exit %d, requests %+v", code, p.requests)
	}
	if code, _, stderr := runNew(p.options("neither\n"), "new", folder); code != cli.ExitUsage || !strings.Contains(stderr, "new repository") {
		t.Errorf("exit %d: %s", code, stderr)
	}
}

// What git and gh said is clipped stderr that may hold terminal escapes, so the command line prints it with every
// escape sequence and control character removed, newline and tab kept.
func TestNewPrintsWhatGitAndGHSaidWithNoControlCharacters(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	hostile := "\x1b[31mremote: denied\x1b[0m\x1b]0;title\x07\r\nfatal:\tno\x00 \u009b2Jaccess\x08\x7f"
	p := &project{report: ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeWritten, Files: []string{"AGENTS.md"}, Commit: "abc123",
		Repositories: []ledger.RepositoryReport{{Name: "orchid", Path: ".", Commit: "abc123", CommitError: hostile,
			Steps: []ledger.RemoteStep{{Step: "push", Outcome: "failed", Detail: hostile}}}},
	}}
	code, stdout, stderr := runNew(p.options(""), "new", "monorepo", folder, "--create-remote", "github:acme")
	if code != cli.ExitError {
		t.Errorf("exit %d, want an error for the failed push", code)
	}
	for name, text := range map[string]string{"stdout": stdout, "stderr": stderr} {
		for _, r := range text {
			if r != '\n' && r != '\t' && (r < 0x20 || (r >= 0x7f && r <= 0x9f)) {
				t.Errorf("%s holds the control character %U:\n%q", name, r, text)
				break
			}
		}
		if strings.Contains(text, "[31m") || strings.Contains(text, "title") {
			t.Errorf("%s holds an escape sequence's text:\n%q", name, text)
		}
	}
	if !strings.Contains(stdout, "push\tfailed\tremote: denied\nfatal:\tno access") {
		t.Errorf("the plain text of the detail is gone:\n%q", stdout)
	}

	refused := &project{report: ledger.TemplateWriteReport{Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedGHSignedOut, Detail: "\x1b[1mnot signed in\x1b[0m\x07"}}
	_, _, stderr = runNew(refused.options(""), "new", "monorepo", folder, "--create-remote", "github:acme")
	if strings.ContainsAny(stderr, "\x1b\x07") || !strings.Contains(stderr, "gh_signed_out: not signed in") {
		t.Errorf("a refusal's detail: %q", stderr)
	}
}

// hiddenText is gh stderr with what hides or reorders text: an escape, a bidirectional override, an isolate, a
// directional mark, and zero-width characters.
const hiddenText = "gh: HTTP 403\x1b[31m: \u202eevil\u202c \u2066x\u2069\u200e\u200f\u061c\u200b\u200c\u200d\u2060\ufeff text\x07"

// plainHidden is hiddenText with all of it dropped.
const plainHidden = "gh: HTTP 403: evil x text"

func hidesNothing(t *testing.T, name, text string) {
	t.Helper()
	for _, r := range text {
		if r != '\n' && r != '\t' && ledger.HiddenRune(r) {
			t.Errorf("%s holds the hidden character %U:\n%q", name, r, text)
			return
		}
	}
}

// An error of the account read or of the request, which carries what `gh` printed, reaches the person with no
// control, bidirectional, or zero-width character.
func TestNewPrintsAnErrorOfGHWithNoHiddenText(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	for name, run := range map[string]func() (int, string, string){
		"the account read": func() (int, string, string) {
			opts := (&project{}).options("one\ny\n3\n")
			opts.ReadForgeAccount = func(context.Context) (ledger.ForgeAccountReport, error) {
				return ledger.ForgeAccountReport{}, errors.New(hiddenText)
			}
			return runNew(opts, "new", folder)
		},
		"the request": func() (int, string, string) {
			opts := (&project{}).options("")
			opts.NewProject = func(context.Context, ledger.TemplateWriteRequest, bool) (ledger.TemplateWriteReport, error) {
				return ledger.TemplateWriteReport{}, errors.New(hiddenText)
			}
			return runNew(opts, "new", "monorepo", folder, "--create-remote", "github:acme")
		},
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := run()
			if code == cli.ExitOK {
				t.Errorf("exit %d", code)
			}
			hidesNothing(t, "stdout", stdout)
			hidesNothing(t, "stderr", stderr)
			if !strings.Contains(stderr, plainHidden) {
				t.Errorf("the plain text of the error is gone:\n%q", stderr)
			}
		})
	}
}

// The free text of a report is held to the same rule.
func TestNewPrintsAReportWithNoBidirectionalOrZeroWidthCharacter(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	p := &project{report: ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeWritten, Files: []string{"AGENTS.md"}, Commit: "abc123",
		Repositories: []ledger.RepositoryReport{{Name: "orchid", Path: ".", Commit: "abc123", CommitError: hiddenText,
			Steps: []ledger.RemoteStep{{Step: "push", Outcome: "failed", Detail: hiddenText}}}},
	}}
	_, stdout, stderr := runNew(p.options(""), "new", "monorepo", folder, "--create-remote", "github:acme")
	hidesNothing(t, "stdout", stdout)
	hidesNothing(t, "stderr", stderr)
	if !strings.Contains(stdout, "push\tfailed\t"+plainHidden) {
		t.Errorf("the plain text of the detail is gone:\n%q", stdout)
	}
	refused := &project{report: ledger.TemplateWriteReport{Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedGHSignedOut, Detail: hiddenText}}
	_, _, stderr = runNew(refused.options(""), "new", "monorepo", folder, "--create-remote", "github:acme")
	hidesNothing(t, "stderr", stderr)
	if !strings.Contains(stderr, "gh_signed_out: "+plainHidden) {
		t.Errorf("a refusal's detail: %q", stderr)
	}
	signedOut := &project{account: ledger.ForgeAccountReport{State: ledger.ForgeAccountSignedOut, Detail: hiddenText}}
	_, _, stderr = runNew(signedOut.options("one\ny\n3\n"), "new", folder)
	hidesNothing(t, "stderr", stderr)
	if !strings.Contains(stderr, "gh_signed_out: "+plainHidden) {
		t.Errorf("a failed check's detail: %q", stderr)
	}
}

// The folders above the new folder are made by the run that writes it, after its checks pass, so a refused check leaves
// nothing on disk.
func TestNewMakesNoFolderWhenACheckRefuses(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "code")
	folder := filepath.Join(parent, "orchid")
	p := &project{report: ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedGHSignedOut,
		Detail: "Run `gh auth login --hostname github.com` on this machine", Remedy: "gh auth login --hostname github.com",
	}}
	code, _, _ := runNew(p.options(""), "new", "monorepo", folder, "--create-remote", "github:acme")
	if code != cli.ExitError {
		t.Errorf("exit %d, want an error for the refused check", code)
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Errorf("a refused check left the parent folder: %v", err)
	}
}
