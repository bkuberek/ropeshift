package control_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/checkouts"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/practicegit"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practicerun"
	"ropeshift.com/ropeshift/internal/practices"
)

// practiceIDs are the IDs of the findings a repository's practices hold.
func practiceIDs(repository planning.PlannedRepository) []string {
	var ids []string
	if repository.Practices == nil {
		return ids
	}
	for _, finding := range repository.Practices.Findings {
		ids = append(ids, finding.ID)
	}
	slices.Sort(ids)
	return ids
}

// A dry run over a layout of real clones lists the findings of the clone that holds them, records no event, and has
// the digest of the same layout once the findings are fixed.
func TestGitDryRunListsThePracticesFindingsAndRecordsNothing(t *testing.T) {
	t.Parallel()
	l := newLayout(t)
	l.clone("ws", workspaceFiles(map[string]string{"AGENTS.md": "rules\n"}))
	l.clone("ws/repos/api", map[string]string{"go.mod": "module acme.test/api\n\ngo 1.24\n\nrequire example.test/dep v1.0.0\n", "main.go": "package main\n\nfunc main() {}\n"})
	store := l.ledger.get()
	machine := &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName), Env: l.env}
	sources := practicerun.Sources{
		Snapshots: practicegit.Snapshots{Env: l.env}, Secrets: practicegit.SecretScanner{Env: l.env},
		Forge: &fake.Practices{}, Clock: fake.NewClock(time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)),
	}
	adoptions := control.NewAdoptions(control.NewRepositories(store)).Checking(sources)
	events := func() int {
		var n int
		if err := store.Read(t.Context(), "acme", func(tx ports.ReadTx) error {
			all, err := tx.Events(0, 1000)
			n = len(all)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	request := control.AdoptionRequest{Root: l.path("ws"), Practices: true}
	before := events()
	plan, err := adoptions.Plan(t.Context(), "acme", request, machine, machine, projectMember)
	if err != nil {
		t.Fatal(err)
	}
	api := practicedRepository(t, plan, l.path("ws/repos/api"))
	want := []string{"ci", "codeowners", "dependency-updates", "env-ignored:ignore:.", "lockfile:go", "pre-commit", "security-policy", "tests:go"}
	if got := practiceIDs(api); !slices.Equal(got, want) {
		t.Fatalf("the api repository's findings are %v, want %v", got, want)
	}
	if ws := practicedRepository(t, plan, l.path("ws")); ws.Practices == nil {
		t.Errorf("the workspace holds no practices: %q", ws.PracticesNote)
	}
	if after := events(); after != before {
		t.Errorf("the dry run appended %d events", after-before)
	}

	// The findings that need no document are fixed in the clone, which leaves the layout as it was.
	fix := map[string]string{
		".gitignore": ".env\n.env.*\n!.env.example\n", ".pre-commit-config.yaml": "repos: []\n", "CODEOWNERS": "* @acme\n",
		"go.sum": "example.test/dep v1.0.0 h1:x=\n", "main_test.go": "package main\n",
	}
	l.commitIn("ws/repos/api", fix)
	fixed, err := adoptions.Plan(t.Context(), "acme", request, machine, machine, projectMember)
	if err != nil {
		t.Fatal(err)
	}
	after := practicedRepository(t, fixed, l.path("ws/repos/api"))
	left := []string{"dependency-updates", "security-policy"}
	if got := practiceIDs(after); !slices.Equal(got, left) {
		t.Fatalf("the fixed repository's findings are %v, want %v", got, left)
	}
	if fixed.Digest != plan.Digest {
		t.Errorf("the plan with the findings fixed has the digest %s, want %s", fixed.Digest, plan.Digest)
	}
	if differs := fixed.DiffersIn(plan.Parts()); len(differs) != 0 {
		t.Errorf("the parts of the plan differ in %v", differs)
	}
	for _, id := range []practices.ID{practices.BranchProtection, practices.RequiredReview, practices.License} {
		for _, status := range after.Practices.Practices {
			if status.ID == id && status.Status != practices.StatusNotApplicable {
				t.Errorf("%s is %s with no forge binding", id, status.Status)
			}
		}
	}
}

// commitIn writes files in a clone of the layout, commits them, and moves the clone's view of its origin's main to
// the commit, as a fetch would.
func (l *layout) commitIn(rel string, files map[string]string) {
	l.t.Helper()
	dir := l.path(rel)
	var names []string
	for name, content := range files {
		file := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			l.t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			l.t.Fatal(err)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	for _, args := range [][]string{append([]string{"add", "--"}, names...), {"commit", "--quiet", "-m", "fix the findings"}, {"update-ref", "refs/remotes/origin/main", "HEAD"}} {
		cmd := exec.CommandContext(l.t.Context(), "git", append([]string{"-C", dir}, args...)...)
		cmd.Env = l.env
		if out, err := cmd.CombinedOutput(); err != nil {
			l.t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}
