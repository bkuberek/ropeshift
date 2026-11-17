package entry

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/checkouts"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/templates"
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
	repo := gittest.New(t)
	for _, entry := range repo.Env() {
		name, value, _ := strings.Cut(entry, "=")
		t.Setenv(name, value)
	}
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
