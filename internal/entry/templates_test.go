package entry

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/checkouts"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/templates"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/gittest"
)

// examplePlanSet is the example project's plan files: the path each is committed at, and the template it
// comes from. The decision record sits where a project keeps its records, outside plan/.
var examplePlanSet = map[string]string{
	"plan/goal.md":                          "workspace/plan/goal.md",
	"plan/plan.md":                          "workspace/plan/plan.md",
	"plan/requirements.md":                  "workspace/plan/requirements.md",
	"plan/repositories.md":                  "workspace/plan/repositories.md",
	"plan/specs/accounts-endpoints.md":      "workspace/plan/spec.md",
	"plan/architecture/acme-accounts.md":    "workspace/plan/architecture.md",
	"plan/design/account-settings.md":       "workspace/plan/experience-design.md",
	"plan/milestones/m1-accounts.md":        "workspace/plan/milestone.md",
	"plan/contracts/accounts-api.md":        "workspace/plan/shared-contract.md",
	"plan/contracts/accounts-web.md":        "workspace/plan/contract.md",
	"docs/decisions/0001-plan-of-record.md": "workspace/docs/decision.md",
}

// exampleRepositories are the code repositories the example manifest names, which plan approval registers.
var exampleRepositories = []string{"acme-api", "acme-web"}

// intakeOf commits the files to a repository the worker keeps a checkout of, with the project holding the
// named repositories, runs intake over it in process, and returns each problem intake reports and the work
// orders it records.
func intakeOf(t *testing.T, files map[string]string, held ...string) ([]planning.SourceProblem, []string) {
	t.Helper()
	repo := gittest.New(t)
	for path, text := range files {
		full := filepath.Join(repo.Dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	repo.Git("add", "-A")
	repo.Git("commit", "--quiet", "-m", "docs: add the plan")
	return intakeIn(t, repo, held...)
}

// intakeIn runs intake in process over a repository with a commit, which the worker keeps a checkout of, with
// the project holding the named repositories, and returns each problem intake reports and the work orders it
// records.
func intakeIn(t *testing.T, repo *gittest.Repo, held ...string) ([]planning.SourceProblem, []string) {
	t.Helper()
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	state, store, err := sqlite.Upgrade(t.Context(), t.TempDir(), "ledger.db", ledger.Workspace{ID: soloScope, Name: "Solo"}, member, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	defer store.Close()
	for _, name := range held {
		found := control.DetectedRepository{ID: name + "-id", Remote: "git@github.com:acme/" + name + ".git", DefaultBranch: "main", Directory: name}
		if _, err := control.NewRepositories(store).Detect(t.Context(), soloScope, found, member); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range repo.Env() {
		name, value, _ := strings.Cut(entry, "=")
		t.Setenv(name, value)
	}
	id := repo.Git("rev-parse", "HEAD")
	kept := &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName)}
	if err := kept.Keep(t.Context(), soloScope, id, repo.Dir); err != nil {
		t.Fatal(err)
	}
	ticks := soloTicks(state, soloScope, soloOptions{intakeTick: time.Second}, kept, questions.BatchPolicy{}, nil)
	at := slices.IndexFunc(ticks, func(tick control.Tick) bool { return tick.Name == "intake" })
	if err := ticks[at].Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	home, err := state.Named(t.Context(), ledger.DefaultProjectName)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := state.Open(t.Context(), home)
	if err != nil {
		t.Fatal(err)
	}
	var problems []planning.SourceProblem
	var work []string
	if err := opened.Read(t.Context(), home.ID, func(tx ports.ReadTx) error {
		bindings, err := tx.Planning().Bindings()
		if err != nil {
			return err
		}
		for _, binding := range bindings {
			problems = append(problems, binding.Problems...)
		}
		orders, err := tx.WorkOrders().List()
		for _, order := range orders {
			work = append(work, order.Title)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(work)
	return problems, work
}

// renderedExamplePlanSet renders each template of examplePlanSet with the example data.
func renderedExamplePlanSet(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	for path, name := range examplePlanSet {
		text, err := templates.Render(name, templates.Data{Example: true})
		if err != nil {
			t.Fatal(err)
		}
		files[path] = text
	}
	return files
}

// TestTheExamplePlanSetPassesIntake holds FR-TPL-4's last check: the plan files of the example project, as the
// templates render them and committed to a repository, are read by intake with no problem, and the contracts'
// work orders are recorded.
func TestTheExamplePlanSetPassesIntake(t *testing.T) {
	problems, work := intakeOf(t, renderedExamplePlanSet(t), exampleRepositories...)
	if len(problems) > 0 {
		t.Errorf("intake reports problems: %+v", problems)
	}
	if want := []string{"Add the account endpoints", "Build the account settings page"}; !slices.Equal(work, want) {
		t.Errorf("intake recorded work %v, want %v", work, want)
	}
}

// A plan file that no longer parses is a problem intake reports, so the check above can fail.
func TestAnExamplePlanFileThatDoesNotParseIsAnIntakeProblem(t *testing.T) {
	files := renderedExamplePlanSet(t)
	files["plan/goal.md"] = strings.Replace(files["plan/goal.md"], `kind = "goal"`, `kind = "goals"`, 1)
	problems, _ := intakeOf(t, files, exampleRepositories...)
	if !slices.ContainsFunc(problems, func(p planning.SourceProblem) bool { return p.Path == "plan/goal.md" }) {
		t.Errorf("intake reports %+v, want a problem in plan/goal.md", problems)
	}
}

// wordsOf splits a command `new` prints into its words as a shell reads them, for the quoting this test's
// commands hold: words with no quotes, and a message in double quotes.
func wordsOf(t *testing.T, command string) []string {
	t.Helper()
	if strings.Contains(command, "'") {
		t.Fatalf("%q quotes a path, which this test's folder does not need", command)
	}
	var words []string
	var word strings.Builder
	quoted, started := false, false
	for _, r := range command {
		switch {
		case r == '"':
			quoted, started = !quoted, true
		case r == ' ' && !quoted:
			if started {
				words = append(words, word.String())
			}
			word.Reset()
			started = false
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if started {
		words = append(words, word.String())
	}
	return words
}

// TestANewWorkspaceRegistersAndPassesIntake is FR-TPL-1's exit criterion end to end. `new workspace` with two
// code repositories writes its files with no daemon and no session. The git commands it prints make the
// folder's first commit, of exactly the files written. Intake reads the commit with no problem. The ropeshift
// commands it prints create the project, and the repository add of the Makefile's register target registers
// the workspace and the clone of each repository it lists. The test runs the add for the workspace alone, because
// make setup would clone the code repositories from remotes this test does not have.
func TestANewWorkspaceRegistersAndPassesIntake(t *testing.T) {
	identity := gittest.New(t)
	gitOf(t, identity)
	folder := filepath.Join(filepath.Dir(identity.Dir), "orchid")
	repositories := []projectfiles.CodeRepository{
		{Name: "orchid-api", Remote: "git@example.invalid:orchid/orchid-api.git"},
		{Name: "orchid-web", Remote: "git@example.invalid:orchid/orchid-web.git"},
	}
	outcome := runNew(t, "workspace", folder,
		"--repository", "orchid-api="+repositories[0].Remote, "--repository", "orchid-web="+repositories[1].Remote)
	if want := projectfiles.Commands(projectfiles.Workspace, folder, "orchid", repositories, true); !slices.Equal(outcome.Commands, want) {
		t.Fatalf("commands %v, want %v", outcome.Commands, want)
	}

	var ropeshift [][]string
	for _, command := range outcome.Commands {
		words := wordsOf(t, command)
		switch words[0] {
		case "git":
			run := exec.Command("git", words[1:]...)
			run.Env = identity.Env()
			if out, err := run.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", command, err, out)
			}
		case "ropeshift":
			ropeshift = append(ropeshift, words[1:])
		}
	}
	workspace := &gittest.Repo{T: t, Dir: folder, Home: identity.Home}
	if tracked := strings.Split(workspace.Git("ls-files"), "\n"); !slices.Equal(tracked, outcome.Written) {
		t.Errorf("the first commit holds %v, want the files written %v", tracked, outcome.Written)
	}
	if !slices.Contains(outcome.Written, "plan/repositories.md") {
		t.Errorf("no manifest among %v", outcome.Written)
	}

	problems, work := intakeIn(t, workspace)
	if len(problems) > 0 {
		t.Errorf("intake reports problems: %+v", problems)
	}
	if len(work) > 0 {
		t.Errorf("intake recorded work %v from files that hold no contract", work)
	}

	installation := apitest.StartInstallation(t)
	daemon := cli.Options{Socket: installation.Socket, Session: installation.Session}
	run := func(args ...string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := cli.Run(args, &stdout, &stderr, daemon); code != cli.ExitOK {
			t.Fatalf("ropeshift %v: exit %d, stdout %q, stderr %q", args, code, stdout.String(), stderr.String())
		}
		return stdout.String()
	}
	if want := [][]string{{"project", "create", "orchid"}}; !slices.EqualFunc(ropeshift, want, slices.Equal) {
		t.Fatalf("the ropeshift commands are %v, want %v", ropeshift, want)
	}
	for _, args := range ropeshift {
		run(args...)
	}
	makefile, err := os.ReadFile(filepath.Join(folder, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"PROJECT := orchid", "\t@$(CLONES); echo \"ropeshift repository add $$* --project $(PROJECT)\"; ropeshift repository add \"$$@\" --project $(PROJECT)"} {
		if !strings.Contains(string(makefile), line+"\n") {
			t.Fatalf("the Makefile does not hold %q", line)
		}
	}
	found, _, err := (&checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName)}).Find(t.Context(), []string{folder}, 1)
	if err != nil || len(found) != 1 {
		t.Fatalf("read clones %+v (%v), want the workspace", found, err)
	}
	// The harness's machine matches a path as it is spelled, so the command names the clone by the path the
	// reader found it at, with any link in the temporary directory resolved.
	installation.Checkouts.Place(found[0])
	if out := run("repository", "add", found[0].Path, "--project", "orchid"); !strings.Contains(out, "registered\torchid\t") {
		t.Errorf("repository add did not register the workspace:\n%s", out)
	}
	if list := run("repository", "list", "--project", "orchid"); !strings.Contains(list, "orchid") {
		t.Errorf("the project lists:\n%s", list)
	}
}
