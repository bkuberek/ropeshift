package contract

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/repofiles"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/gittest"
)

func TestWorkSourceContract(t *testing.T) {
	t.Run("repository files", func(t *testing.T) {
		RunWorkSource(t, func(t *testing.T) SourceRig {
			repo := gittest.New(t)
			repo.Commit("README.txt", "seed\n")
			return SourceRig{
				DigestPrefix: planning.DigestGitBlob,
				Source:       repofiles.New(repofiles.Config{Repository: repo.Dir, Env: repo.Env()}),
				Commit:       func(name, content string) { repo.Commit(name, content) },
				Rename:       repo.Rename,
				Fork:         func() (SourceRig, func(name, content string)) { return repoFork(repo) },
			}
		})
	})
	t.Run("fake", func(t *testing.T) {
		RunWorkSource(t, func(*testing.T) SourceRig { return fakeRig(fake.NewWorkSource()) })
	})
}

// repoFork cuts a branch and leaves the repository on the one it stood on, so a rig on either
// commits where it should.
func repoFork(repo *gittest.Repo) (SourceRig, func(name, content string)) {
	const branch = "forked"
	home := repo.Git("symbolic-ref", "--short", "HEAD")
	repo.Git("branch", branch)
	onBranch := SourceRig{
		Source: repofiles.New(repofiles.Config{Repository: repo.Dir, Ref: "refs/heads/" + branch, Env: repo.Env()}),
		Commit: func(name, content string) {
			repo.Git("switch", "--quiet", branch)
			repo.Commit(name, content)
			repo.Git("switch", "--quiet", home)
		},
	}
	merge := func(name, content string) {
		repo.Git("merge", "--quiet", "--no-edit", "--strategy=ours", branch)
		repo.Commit(name, content)
	}
	return onBranch, merge
}

func fakeRig(source *fake.WorkSource) SourceRig {
	return SourceRig{
		DigestPrefix: planning.DigestSHA256,
		Source:       source,
		Commit:       func(name, content string) { source.Write(name, []byte(content)) },
		Rename:       source.Rename,
		Fork: func() (SourceRig, func(name, content string)) {
			branch := source.Fork()
			return fakeRig(branch), func(name, content string) { source.Merge(branch, name, []byte(content)) }
		},
	}
}

// TestDigestIsTheGitBlobHash is D1's digest: the hash git holds, of the file as
// written.
func TestDigestIsTheGitBlobHash(t *testing.T) {
	repo := gittest.New(t)
	text := ContractText("alpha", "Alpha", "One.")
	repo.Commit("contract.md", text)
	cmd := exec.Command("git", "hash-object", "--stdin")
	cmd.Dir, cmd.Env, cmd.Stdin = repo.Dir, repo.Env(), bytes.NewReader([]byte(text))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	batch, err := repofiles.New(repofiles.Config{Repository: repo.Dir, Env: repo.Env()}).Poll(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	want := planning.DigestGitBlob + strings.TrimSpace(string(out))
	if got := batch.Items[0].Digest; got != want {
		t.Errorf("digest %s, want %s", got, want)
	}
}

// TestADocumentDigestIsTheGitBlobHash holds a document to the same digest as a contract: the hash git
// holds, of the file as written, so a version of a document names the content it was read from.
func TestADocumentDigestIsTheGitBlobHash(t *testing.T) {
	repo := gittest.New(t)
	text := DocumentText("spec", "checkout-api", "Checkout API", "", "The calls.")
	repo.Commit("spec.md", text)
	cmd := exec.Command("git", "hash-object", "--stdin")
	cmd.Dir, cmd.Env, cmd.Stdin = repo.Dir, repo.Env(), bytes.NewReader([]byte(text))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	batch, err := repofiles.New(repofiles.Config{Repository: repo.Dir, Env: repo.Env()}).Poll(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	want := planning.DigestGitBlob + strings.TrimSpace(string(out))
	if len(batch.Records) != 1 || batch.Records[0].Digest != want {
		t.Errorf("records %+v, want digest %s", batch.Records, want)
	}
}

// TestIntakeReadsCommittedContentOnly proves an uncommitted edit is not intake.
func TestIntakeReadsCommittedContentOnly(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("contract.md", ContractText("alpha", "Alpha", "Committed."))
	if err := writeFile(repo.Dir+"/contract.md", ContractText("alpha", "Alpha", "Not committed.")); err != nil {
		t.Fatal(err)
	}
	source := repofiles.New(repofiles.Config{Repository: repo.Dir, Env: repo.Env()})
	batch, err := source.Poll(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := batch.Items[0].Statement; got != "Committed.\n" {
		t.Errorf("statement %q", got)
	}
}

func TestConfiguredRefAndDirectory(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("elsewhere/other.md", ContractText("other", "Other", "One."))
	repo.Commit("plans/contract.md", ContractText("alpha", "Alpha", "On main."))
	repo.Git("switch", "--quiet", "-c", "next")
	repo.Commit("plans/contract.md", ContractText("alpha", "Alpha", "On next."))
	repo.Git("switch", "--quiet", "main")
	source := repofiles.New(repofiles.Config{Repository: repo.Dir, Ref: "refs/heads/next", Directory: "plans", Env: repo.Env()})
	batch, err := source.Poll(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Items) != 1 || batch.Items[0].Statement != "On next.\n" {
		t.Errorf("items %+v", batch.Items)
	}
}

func TestRemoteAndFetch(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("contract.md", ContractText("alpha", "Alpha", "One."))
	clone := gittest.New(t)
	clone.Git("remote", "add", "origin", origin.Dir)
	clone.Git("fetch", "--quiet", "origin")
	source := repofiles.New(repofiles.Config{Repository: clone.Dir, Env: clone.Env()})
	ctx := t.Context()
	first, err := source.Poll(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := onlyItem(t, first).Statement; got != "One.\n" {
		t.Fatalf("statement %q", got)
	}
	origin.Commit("contract.md", ContractText("alpha", "Alpha", "Two."))
	second, err := source.Poll(ctx, first.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if got := onlyItem(t, second).Statement; got != "Two.\n" {
		t.Errorf("a poll did not fetch: statement %q", got)
	}
}

// TestFailedFetchIsAHealthState holds FR-DEP-1: with no network, intake still reads
// what the machine has.
func TestFailedFetchIsAHealthState(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("contract.md", ContractText("alpha", "Alpha", "One."))
	repo.Git("remote", "add", "origin", repo.Dir+"/does-not-exist")
	source := repofiles.New(repofiles.Config{Repository: repo.Dir, Env: repo.Env()})
	ctx := t.Context()
	batch, err := source.Poll(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	onlyItem(t, batch)
	health, err := source.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if health.State != planning.HealthFetchFailed || health.Detail == "" {
		t.Errorf("health %+v", health)
	}
}

func TestRepositoryWithNoRemote(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("contract.md", ContractText("alpha", "Alpha", "One."))
	source := repofiles.New(repofiles.Config{Repository: repo.Dir, Env: repo.Env()})
	if _, err := source.Poll(context.Background(), ports.Cursor("")); err != nil {
		t.Fatal(err)
	}
	health, err := source.Health(t.Context())
	if err != nil || health.State != planning.HealthOK {
		t.Errorf("health %+v, %v", health, err)
	}
}

// TestIntakeOnARealRepository walks the unit's intake path on real git.
func TestIntakeOnARealRepository(t *testing.T) {
	store, err := fake.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	actor := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	scope := ledger.WorkspaceID("w")
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: scope}, actor); err != nil {
		t.Fatal(err)
	}
	repo := gittest.New(t)
	first := repo.Commit("contract.md", ContractText("alpha", "Alpha", "One."))
	source := repofiles.New(repofiles.Config{Repository: repo.Dir, Env: repo.Env()})
	intake := control.NewIntake(store)
	revision := func() int64 {
		var rev int64
		err := store.Read(ctx, scope, func(tx ports.ReadTx) error {
			w, err := tx.Workspace()
			rev = w.Rev
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return rev
	}
	contracts := func() []planning.Contract {
		var out []planning.Contract
		err := store.Read(ctx, scope, func(tx ports.ReadTx) error {
			var err error
			out, err = tx.Planning().Contracts()
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if _, err := intake.Poll(ctx, scope, source); err != nil {
		t.Fatal(err)
	}
	recorded := contracts()
	if len(recorded) != 1 || !strings.HasSuffix(recorded[0].Source, "@"+first) || !strings.HasPrefix(recorded[0].Digest, planning.DigestGitBlob) {
		t.Fatalf("contracts %+v", recorded)
	}
	before := revision()
	if _, err := intake.Poll(ctx, scope, source); err != nil {
		t.Fatal(err)
	}
	if after := revision(); after != before {
		t.Errorf("a second poll moved the revision from %d to %d", before, after)
	}
	repo.Commit("notes.md", "# Prose\n")
	if _, err := intake.Poll(ctx, scope, source); err != nil {
		t.Fatal(err)
	}
	if after := revision(); after != before {
		t.Errorf("a commit that touches no contract moved the revision from %d to %d", before, after)
	}
	repo.Commit("contract.md", ContractText("alpha", "Alpha", "Two."))
	if _, err := intake.Poll(ctx, scope, source); err != nil {
		t.Fatal(err)
	}
	recorded = contracts()
	if len(recorded) != 2 || recorded[1].Version != 2 {
		t.Fatalf("contracts %+v", recorded)
	}
	var orders []workorders.WorkOrder
	err = store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		orders, err = tx.WorkOrders().List()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].ContractVersion != 2 {
		t.Errorf("work in review stayed on an old version: %+v", orders)
	}
}

// TestDocumentsOnARealRepository follows a spec through two commits: each version names the commit
// and the blob it was read from, and a contract pins the version at its own commit (FR-DOC-2, FR-DOC-4).
func TestDocumentsOnARealRepository(t *testing.T) {
	store, err := fake.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	scope := ledger.WorkspaceID("w")
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: scope}, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}); err != nil {
		t.Fatal(err)
	}
	repo := gittest.New(t)
	repo.Commit("plan.md", PlanText("payments-v2", "Payments v2", ""))
	repo.Commit("spec.md", DocumentText("spec", "checkout-api", "Checkout API", "payments-v2", "The calls."))
	// Intake reads the commit the ref stands at, so that is the commit each version names.
	first := repo.Commit("contract.md", citing("alpha", "payments-v2", "One.", "spec:checkout-api"))
	source := repofiles.New(repofiles.Config{Repository: repo.Dir, Env: repo.Env()})
	intake := control.NewIntake(store)
	report, err := intake.Poll(ctx, scope, source)
	if err != nil {
		t.Fatal(err)
	}
	if report.Versions != 1 || len(report.Problems) != 0 {
		t.Fatalf("report %+v", report)
	}
	second := repo.Commit("spec.md", DocumentText("spec", "checkout-api", "Checkout API", "payments-v2", "The calls, changed."))
	if _, err := intake.Poll(ctx, scope, source); err != nil {
		t.Fatal(err)
	}
	var versions []planning.DocumentVersion
	var contracts []planning.Contract
	err = store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		if versions, err = tx.Planning().DocumentVersions(); err != nil {
			return err
		}
		contracts, err = tx.Planning().Contracts()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || !strings.HasSuffix(versions[0].Source, "@"+first) || !strings.HasSuffix(versions[1].Source, "@"+second) {
		t.Fatalf("versions %+v", versions)
	}
	for _, version := range versions {
		if !strings.HasPrefix(version.Digest, planning.DigestGitBlob) || version.Digest == "" {
			t.Errorf("digest %q is not a blob", version.Digest)
		}
	}
	if versions[0].Digest == versions[1].Digest || versions[0].Body != "The calls.\n" {
		t.Errorf("versions %+v", versions)
	}
	if len(contracts) != 1 || len(contracts[0].Documents) != 1 || contracts[0].Documents[0].Version != 1 {
		t.Errorf("contracts %+v: the pin moved with the document", contracts)
	}
}
