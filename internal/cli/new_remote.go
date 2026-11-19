package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/projectfiles"
)

// newRemoteDetail is what the help of every `new` command says of the first commit and the remote.
const newRemoteDetail = " With --first-commit, or any remote, it also makes each new repository's first commit " +
	"(git init, add, and commit) and, for a remote, adds it as origin and pushes: --remote adds a URL, and --create-remote " +
	"github:<owner> creates the repository on GitHub with gh, private unless --public. It checks gh before it writes " +
	"anything, so a refusal creates and writes nothing."

// newFlags are the flags that ask `new` for a first commit and a remote.
type newFlags struct {
	firstCommit     bool
	remote          string
	createRemote    string
	public          bool
	newRepositories repeated
}

// register adds the flags every shape takes, and the one only a workspace takes.
func (f *newFlags) register(set *flag.FlagSet, shape projectfiles.Shape) {
	set.BoolVar(&f.firstCommit, "first-commit", false, "make each new repository's first commit: git init, add, and commit")
	set.StringVar(&f.remote, "remote", "", "the URL of the folder's own repository, added as origin and pushed")
	set.StringVar(&f.createRemote, "create-remote", "", "create each repository with no remote of its own on GitHub, as github:<owner>, and push it")
	set.BoolVar(&f.public, "public", false, "make every created repository public; they are private otherwise")
	if shape == projectfiles.Workspace {
		set.Var(&f.newRepositories, "new-repository", "a new code repository, written at repos/<name>: <name>[=<url>|github:<owner>|none], repeatable")
	}
}

// asked says the command line asks for a first commit, a remote, or a new code repository, which the plain write of
// `ropeshift new` does not do.
func (f *newFlags) asked() bool {
	return f.firstCommit || f.remote != "" || f.createRemote != "" || len(f.newRepositories) > 0
}

// parseForgeOwner reads github:<owner>. Any other forge is a usage error that names the one supported.
func parseForgeOwner(value string) (*ledger.RemoteCreate, error) {
	forge, owner, found := strings.Cut(value, ":")
	if !found || forge == "" || owner == "" {
		return nil, fmt.Errorf("%q is not github:<owner>", value)
	}
	if forge != ledger.ForgeGitHub {
		return nil, fmt.Errorf("creating a repository on %s is not supported yet; ropeshift creates repositories on GitHub: --create-remote github:<owner>", forge)
	}
	if err := ledger.CheckOwner(owner); err != nil {
		return nil, err
	}
	return &ledger.RemoteCreate{Forge: forge, Owner: owner, Visibility: ledger.VisibilityPrivate}, nil
}

// forgeForm says a value reads as forge:owner and not as a URL: letters before the colon, and after it no slash, no
// at sign, and no second colon, which a URL and an scp-like address hold.
func forgeForm(value string) bool {
	forge, owner, found := strings.Cut(value, ":")
	if !found || forge == "" || owner == "" || strings.ContainsAny(owner, "/@:") {
		return false
	}
	return strings.IndexFunc(forge, func(r rune) bool { return r < 'a' || r > 'z' }) < 0
}

// request builds what the command line asks of a folder: its remote, and each new code repository's.
func (f *newFlags) request(shape projectfiles.Shape, folder string, existing []projectfiles.CodeRepository, project string) (ledger.TemplateWriteRequest, error) {
	request := ledger.TemplateWriteRequest{Shape: string(shape), Path: folder, Project: project}
	if shape != projectfiles.Repository {
		request.Project = ""
	}
	if f.remote != "" && f.createRemote != "" && shape != projectfiles.Workspace {
		return request, errors.New("--remote and --create-remote name the one repository's remote: give one")
	}
	var create *ledger.RemoteCreate
	if f.createRemote != "" {
		var err error
		if create, err = parseForgeOwner(f.createRemote); err != nil {
			return request, fmt.Errorf("--create-remote: %w", err)
		}
		if f.public {
			create.Visibility = ledger.VisibilityPublic
		}
	}
	switch {
	case f.remote != "":
		request.Remote = &ledger.ProjectRemote{URL: f.remote}
	case create != nil:
		request.Remote = &ledger.ProjectRemote{Create: create}
	}
	if f.public && create == nil && !f.hasForgeValue() {
		return request, errors.New("--public makes created repositories public, and nothing is created: give --create-remote github:<owner>")
	}
	for _, repository := range existing {
		request.Repositories = append(request.Repositories, ledger.TemplateWriteRepository{Name: repository.Name, Remote: repository.Remote})
	}
	for _, entry := range f.newRepositories {
		name, value, hasValue := strings.Cut(entry, "=")
		repository := ledger.TemplateWriteRepository{Name: name, New: true}
		switch {
		case hasValue && value == "none":
		case hasValue && forgeForm(value):
			owned, err := parseForgeOwner(value)
			if err != nil {
				return request, fmt.Errorf("--new-repository %q: %w", entry, err)
			}
			if f.public {
				owned.Visibility = ledger.VisibilityPublic
			}
			repository.Create = owned
		case hasValue:
			repository.URL = value
		case create != nil:
			own := *create
			repository.Create = &own
		}
		request.Repositories = append(request.Repositories, repository)
	}
	request.FirstCommit = f.firstCommit || request.Remote != nil || slices.ContainsFunc(request.Repositories, func(r ledger.TemplateWriteRepository) bool {
		return r.URL != "" || r.Create != nil
	})
	if err := request.CheckRemotes(); err != nil {
		return request, err
	}
	return request, nil
}

// hasForgeValue says some --new-repository value names a forge.
func (f *newFlags) hasForgeValue() bool {
	return slices.ContainsFunc(f.newRepositories, func(entry string) bool {
		_, value, found := strings.Cut(entry, "=")
		return found && forgeForm(value)
	})
}

// needsRepositoryAt says a folder that may not exist yet needs a repository of its own for its first commit, deciding
// from the nearest folder above it that exists.
func (c *invocation) needsRepositoryAt(folder string) (bool, error) {
	if c.cloneTop == nil {
		return true, nil
	}
	here, rest := folder, ""
	for {
		if _, err := os.Stat(here); err == nil {
			break
		}
		parent := filepath.Dir(here)
		if parent == here {
			return true, nil
		}
		rest, here = filepath.Join(filepath.Base(here), rest), parent
	}
	top, err := c.cloneTop(c.ctx, here)
	if err != nil || top == "" {
		return err == nil, err
	}
	real, err := filepath.EvalSymlinks(here)
	if err != nil {
		return false, err
	}
	if resolved, err := filepath.EvalSymlinks(top); err == nil {
		top = resolved
	}
	if rest == "" && top == real {
		return false, nil
	}
	if c.cloneIgnores == nil {
		return false, nil
	}
	return c.cloneIgnores(c.ctx, top, filepath.Join(real, rest))
}

// newProject runs the request through the machine's ports, as the worker runs one for the web UI, and says what
// became of each file and repository.
func (c *invocation) newProject(request ledger.TemplateWriteRequest, data projectfiles.Data) int {
	if c.newProjectRun == nil {
		return c.refuse(ExitError, "this build can make no first commit or remote from the command line")
	}
	files, err := projectfiles.Files(projectfiles.Shape(request.Shape), data, renderTemplate)
	if err != nil {
		return c.usageError("%v", err)
	}
	if code := c.requireGit(); code != ExitOK {
		return code
	}
	existed := map[string]bool{}
	for _, file := range files {
		if _, err := os.Lstat(filepath.Join(request.Path, filepath.FromSlash(file.Path))); err == nil {
			existed[file.Path] = true
		}
	}
	needs, err := c.needsRepositoryAt(request.Path)
	if err != nil {
		return c.finish(plainErr(err), nil, nil)
	}
	if err := os.MkdirAll(filepath.Dir(request.Path), 0o755); err != nil {
		return c.finish(plainErr(err), nil, nil)
	}
	report, err := c.newProjectRun(c.ctx, request, needs)
	if err != nil {
		return c.finish(plainErr(err), nil, nil)
	}
	report = plainReport(report)
	if report.Outcome == ledger.TemplateOutcomeRefused {
		return c.refusedNew(report)
	}
	result := newResult{
		Path: request.Path, Project: data.Project, Written: []string{}, Left: []string{}, Commands: report.Commands,
		Commit: report.Commit, CommitError: report.CommitError, Repositories: report.Repositories, Remaining: report.Remaining,
	}
	for _, path := range report.Files {
		if existed[path] {
			result.Left = append(result.Left, path)
		} else {
			result.Written = append(result.Written, path)
		}
	}
	code := c.finish(nil, result, result.text)
	if code == ExitOK && result.failed() {
		return ExitError
	}
	return code
}

// ansiSequence matches the terminal escape sequences a tool's stderr can carry: a CSI sequence, an OSC string ended
// by BEL or ST, and any other two-character escape. The C1 forms of CSI and OSC are matched as well.
var ansiSequence = regexp.MustCompile(`(?:\x1b\[|\x{9b})[0-?]*[ -/]*[@-~]|(?:\x1b\]|\x{9d})[^\x07\x1b\x{9c}]*(?:\x07|\x1b\\|\x{9c})?|\x1b[ -~]`)

// plainText makes text a tool printed safe to show on a terminal: it drops escape sequences and every character
// ledger.HiddenRune names, which is every control character, the C0 set, DEL, and the C1 set, and every
// bidirectional override and zero-width character, but keeps newline and tab. A step's detail is clipped git or gh
// stderr, so it is untrusted.
func plainText(text string) string {
	text = ansiSequence.ReplaceAllString(text, "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if ledger.HiddenRune(r) {
			return -1
		}
		return r
	}, text)
}

// plainError is an error whose text is plainText of the wrapped error's, so what a tool printed reaches the person
// with nothing that hides or reorders it.
type plainError struct{ err error }

func (e plainError) Error() string { return plainText(e.err.Error()) }
func (e plainError) Unwrap() error { return e.err }

// plainErr wraps an error as a plainError, and leaves nil alone.
func plainErr(err error) error {
	if err == nil {
		return nil
	}
	return plainError{err}
}

// plainReport returns a report whose free text from git and gh holds no control character.
func plainReport(report ledger.TemplateWriteReport) ledger.TemplateWriteReport {
	report.Detail = plainText(report.Detail)
	report.CommitError = plainText(report.CommitError)
	report.Repositories = slices.Clone(report.Repositories)
	for at, repository := range report.Repositories {
		repository.CommitError = plainText(repository.CommitError)
		repository.Steps = slices.Clone(repository.Steps)
		for index := range repository.Steps {
			repository.Steps[index].Detail = plainText(repository.Steps[index].Detail)
		}
		report.Repositories[at] = repository
	}
	return report
}

// failed says a commit or a step did not complete.
func (r newResult) failed() bool {
	if r.CommitError != "" {
		return true
	}
	for _, repository := range r.Repositories {
		if repository.CommitError != "" {
			return true
		}
		for _, step := range repository.Steps {
			if step.Outcome != ledger.RemoteStepDone {
				return true
			}
		}
	}
	return false
}

// refusedNew prints a check that failed, with the command that fixes it, and exits. Nothing was written.
func (c *invocation) refusedNew(report ledger.TemplateWriteReport) int {
	if c.asJSON {
		c.finish(nil, report, nil)
		return ExitError
	}
	message := report.Reason + ": " + report.Detail
	if report.Remedy != "" {
		message += "\nRun: " + report.Remedy
	}
	return c.refuse(ExitError, "%s\nNothing was written and nothing was created.", message)
}

// repositoriesText says each repository's commit, remote, and steps, and what remains.
func (r newResult) repositoriesText(out *strings.Builder) {
	for _, repository := range r.Repositories {
		fmt.Fprintf(out, "\nrepository\t%s\t%s\n", repository.Name, repository.Path)
		switch {
		case repository.CommitError != "":
			fmt.Fprintf(out, "  commit\tfailed\t%s\n", repository.CommitError)
		case repository.Commit != "":
			fmt.Fprintf(out, "  commit\t%s\n", repository.Commit)
		}
		if repository.Remote != "" {
			fmt.Fprintf(out, "  remote\t%s\n", repository.Remote)
		}
		for _, step := range repository.Steps {
			fmt.Fprintf(out, "  %s\t%s\t%s\n", step.Step, step.Outcome, step.Detail)
		}
	}
	if len(r.Remaining) > 0 {
		out.WriteString("\nTo finish what did not run:\n")
		for _, command := range r.Remaining {
			fmt.Fprintf(out, "  %s\n", command)
		}
	}
}

// answers reads the person's answers on the terminal, one line each.
type answers struct {
	in  *bufio.Reader
	out io.Writer
}

// ask prints a question and reads a line. An end of input is an error, so a script never answers by silence.
func (a *answers) ask(question, fallback string) (string, error) {
	fmt.Fprint(a.out, question+" ")
	line, err := a.in.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return "", errors.New("no answer: the terminal closed")
	}
	if line = strings.TrimSpace(line); line == "" {
		return fallback, nil
	}
	return line, nil
}

// yes reads a yes or no, with the default the empty answer gives.
func (a *answers) yes(question string, fallback bool) (bool, error) {
	hint, def := "[y/N]", "n"
	if fallback {
		hint, def = "[Y/n]", "y"
	}
	for {
		line, err := a.ask(question+" "+hint, def)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(line) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(a.out, "Answer y or n.")
	}
}

// newInteractive is `ropeshift new` with no shape: it asks what the project is, then writes it. A project of one
// repository is a monorepo, and a project of several is a workspace with a new code repository for each name.
func (c *invocation) newInteractive(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if c.terminal == nil {
		return c.usageError("name a shape: %s new workspace|monorepo|repository <path>, with --first-commit, --remote, --create-remote, and --public, or run it on a terminal to be asked", program)
	}
	if len(positional) > 1 {
		return c.usageError("unexpected argument %q: it takes at most one folder", positional[1])
	}
	in := &answers{in: bufio.NewReader(c.terminal), out: c.stdout}
	answer, err := in.ask("Will this project live in one repository, or in several? [one/several]", "")
	if err != nil {
		return c.usageError("%v", err)
	}
	shape := projectfiles.Monorepo
	switch strings.ToLower(answer) {
	case "one", "1":
	case "several", "many":
		shape = projectfiles.Workspace
	default:
		return c.usageError("answer one or several; to add a code repository to a project planned elsewhere, run %s new repository <path>", program)
	}
	var path string
	if len(positional) == 1 {
		path = positional[0]
	} else if path, err = in.ask("Folder to write:", ""); err != nil || path == "" {
		return c.usageError("name the folder to write")
	}
	folder, err := filepath.Abs(path)
	if err != nil {
		return c.usageError("the folder %q: %v", path, err)
	}
	data := projectfiles.Data{Project: projectNameOf(filepath.Base(folder))}
	if data.Project == "" {
		return c.usageError("the folder %q makes no project name; rename it to a name with letters or digits", filepath.Base(folder))
	}
	request := ledger.TemplateWriteRequest{Shape: string(shape), Path: folder}
	if shape == projectfiles.Workspace {
		fmt.Fprintln(c.stdout, "Name each code repository, one per line. An empty line ends them.")
		for {
			name, err := in.ask("Code repository:", "")
			if err != nil {
				return c.usageError("%v", err)
			}
			if name == "" {
				break
			}
			request.Repositories = append(request.Repositories, ledger.TemplateWriteRepository{Name: name, New: true})
		}
		if len(request.Repositories) == 0 {
			return c.usageError("name at least one code repository")
		}
	}
	if request.FirstCommit, err = in.yes("Make the first commit in each repository?", true); err != nil {
		return c.usageError("%v", err)
	}
	creates := 0
	if request.FirstCommit {
		if creates, err = c.askRemotes(in, &request); err != nil {
			if errors.Is(err, errStopped) {
				return ExitError
			}
			return c.usageError("%v", plainErr(err))
		}
	}
	for _, repository := range request.Repositories {
		data.Repositories = append(data.Repositories, projectfiles.CodeRepository{Name: repository.Name, Remote: repository.URL, New: repository.New})
	}
	if err := request.CheckRemotes(); err != nil {
		return c.usageError("%v", err)
	}
	if err := projectfiles.Check(shape, data.Repositories); err != nil {
		return c.usageError("%v", err)
	}
	c.printPlan(request, creates)
	if creates > 0 {
		write, err := in.yes(fmt.Sprintf("Write the files and create %d repositories on GitHub?", creates), false)
		if err != nil {
			return c.usageError("%v", err)
		}
		if !write {
			fmt.Fprintln(c.stdout, "Nothing was written.")
			return ExitOK
		}
	}
	return c.newProject(request, data)
}

// errStopped ends an interactive run that already said why.
var errStopped = errors.New("stopped")

// askRemotes asks for each repository's remote, and returns how many repositories the answers create.
func (c *invocation) askRemotes(in *answers, request *ledger.TemplateWriteRequest) (int, error) {
	choice, err := in.ask("Remote: 1 none, 2 a URL for each repository, 3 create on GitHub [1]", "1")
	if err != nil {
		return 0, err
	}
	switch choice {
	case "1", "none":
		return 0, nil
	case "2", "url":
		url, err := in.ask("URL for "+filepath.Base(request.Path)+" (empty for none):", "")
		if err != nil {
			return 0, err
		}
		if url != "" {
			request.Remote = &ledger.ProjectRemote{URL: url}
		}
		for i := range request.Repositories {
			if request.Repositories[i].URL, err = in.ask("URL for "+request.Repositories[i].Name+" (empty for none):", ""); err != nil {
				return 0, err
			}
		}
		return 0, nil
	case "3", "github":
		return c.askGitHub(in, request)
	}
	return 0, errors.New("answer 1, 2, or 3")
}

// askGitHub runs the checks of gh, prints the command that fixes any that fails and stops, then asks for the owner,
// the visibility, and an owner for each code repository.
func (c *invocation) askGitHub(in *answers, request *ledger.TemplateWriteRequest) (int, error) {
	if c.readForgeAccount == nil {
		return 0, errors.New("this build cannot read the GitHub account")
	}
	account, err := c.readForgeAccount(c.ctx)
	if err != nil {
		return 0, err
	}
	if account.State != ledger.ForgeAccountOK {
		fmt.Fprintf(c.stderr, "ropeshift %s: %s: %s\n", c.name, account.State, plainText(account.Detail))
		if account.Remedy != "" {
			fmt.Fprintf(c.stderr, "Run: %s\n", account.Remedy)
		}
		return 0, errStopped
	}
	fmt.Fprintln(c.stdout, "Owners:")
	for i, owner := range account.Owners {
		fmt.Fprintf(c.stdout, "  %d %s\n", i+1, owner)
	}
	owner, err := in.ask(fmt.Sprintf("Owner [%s]:", account.Login), account.Login)
	if err != nil {
		return 0, err
	}
	for i, listed := range account.Owners {
		if owner == fmt.Sprint(i+1) {
			owner = listed
		}
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

// printPlan says what the run will write and create.
func (c *invocation) printPlan(request ledger.TemplateWriteRequest, creates int) {
	fmt.Fprintf(c.stdout, "\nIt will write a %s into %s", request.Shape, request.Path)
	for _, repository := range request.Repositories {
		if repository.New {
			fmt.Fprintf(c.stdout, ", with a new repository at repos/%s", repository.Name)
		}
	}
	fmt.Fprintln(c.stdout, ".")
	if request.FirstCommit {
		fmt.Fprintln(c.stdout, "It will make the first commit in each repository.")
	}
	if creates > 0 {
		fmt.Fprintf(c.stdout, "It will create %d repositories on GitHub and push each.\n", creates)
	}
}
