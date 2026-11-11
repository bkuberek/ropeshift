package inventory_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/inventory"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

const contractFile = `+++
kind = "contract"
id = "retry"
title = "Retry"

[[criteria]]
id = "c1"
text = "It retries"

[[work_orders]]
id = "retry-order"
title = "Add it"
+++

Retry.
`

// existing is a project as a person left it: Markdown with and without front matter, a file in the format
// and one that is not read cleanly, a branch merged into main, branches with work in progress here and on
// the remote, and a checkout standing on a branch of its own.
func existing(t *testing.T) (origin, clone *gittest.Repo, base string) {
	t.Helper()
	origin = gittest.New(t)
	origin.Commit("README.md", "# Payments\n\nIntro\n=====\n\n```sh\n# not a heading\n```\n")
	origin.Commit("docs/notes.markdown", "---\ntitle: Notes\n---\n## Why\n")
	origin.Commit("docs/retry.md", contractFile)
	origin.Commit("docs/broken.md", "+++\nkind = \"contract\"\nid = unquoted\n+++\n")
	origin.Commit("main.go", "package main\n")
	merged := origin.Git("rev-parse", "main")
	origin.Branch("old/merged", merged)
	origin.Branch("feature/remote", merged)
	origin.Advance("feature/remote")
	base = origin.Advance("main")
	clone = origin.Clone("clone")
	clone.Branch("wip/local", base)
	clone.Advance("wip/local")
	clone.Git("switch", "--quiet", "-c", "scratch")
	clone.Commit("scratch.md", "# Scratch\n")
	return origin, clone, base
}

func TestAnInventoryReadsARepositoryAtItsBaseCommit(t *testing.T) {
	t.Parallel()
	origin, clone, base := existing(t)
	found, err := inventory.Git{Env: clone.Env()}.Take(t.Context(), ports.InventoryRequest{
		Path: clone.Dir, Repository: "id-payments", Name: "payments", BaseBranch: "origin/main", BaseCommit: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	if found.Name != "payments" || found.Repository != "id-payments" || found.BaseCommit != base || found.BaseBranch != "origin/main" {
		t.Errorf("identity %+v", found)
	}
	if found.Remote != origin.Dir || found.DefaultBranch != "main" || found.DefaultTip != base {
		t.Errorf("remote %q, default %q at %q; want %q, main at %s", found.Remote, found.DefaultBranch, found.DefaultTip, origin.Dir, base)
	}
	paths := []string{}
	for _, document := range found.Documents {
		paths = append(paths, document.Path)
		if !strings.HasPrefix(document.Digest, planning.DigestGitBlob) {
			t.Errorf("%s has digest %q", document.Path, document.Digest)
		}
	}
	// What the checkout's own branch holds is not at the base commit.
	if want := []string{"README.md", "docs/broken.md", "docs/notes.markdown", "docs/retry.md"}; !slices.Equal(paths, want) {
		t.Errorf("documents %v, want %v", paths, want)
	}
	readme, notes := found.Documents[0], found.Documents[2]
	if readme.FrontMatter != planning.FrontMatterNone || !slices.Equal(readme.Headings, []planning.Heading{{Level: 1, Text: "Payments"}, {Level: 1, Text: "Intro"}}) {
		t.Errorf("README reads %+v", readme)
	}
	if notes.FrontMatter != planning.FrontMatterYAML || !slices.Equal(notes.Headings, []planning.Heading{{Level: 2, Text: "Why"}}) {
		t.Errorf("notes read %+v", notes)
	}
	if blob := clone.Git("rev-parse", base+":docs/retry.md"); found.Documents[3].Digest != planning.DigestGitBlob+blob {
		t.Errorf("the digest of docs/retry.md is %s, want the blob %s", found.Documents[3].Digest, blob)
	}
	want := []planning.InventoryFile{
		{Path: "docs/broken.md", Clean: false},
		{Path: "docs/retry.md", Kind: planning.FileContract, ID: "retry", Clean: true},
	}
	if len(found.Files) != 2 || found.Files[1] != want[1] || found.Files[0].Clean || found.Files[0].Problem == "" {
		t.Errorf("ropeshift files %+v", found.Files)
	}
	branches := map[string]planning.InventoryBranch{}
	for _, branch := range found.Branches {
		branches[branch.Ref] = branch
	}
	for _, merged := range []string{"refs/heads/main", "refs/remotes/origin/main", "refs/remotes/origin/old/merged", "refs/remotes/origin/HEAD"} {
		if _, listed := branches[merged]; listed {
			t.Errorf("%s is listed, and the base holds it", merged)
		}
	}
	remote, local, scratch := branches["refs/remotes/origin/feature/remote"], branches["refs/heads/wip/local"], branches["refs/heads/scratch"]
	if remote.Name != "origin/feature/remote" || remote.Ahead != 1 || remote.Behind != 1 || remote.MergeBase == "" || remote.Tip == "" {
		t.Errorf("the remote's branch reads %+v", remote)
	}
	if local.Name != "wip/local" || local.Ahead != 1 || local.Behind != 0 || local.MergeBase != base {
		t.Errorf("the local branch reads %+v", local)
	}
	if scratch.Ahead != 1 {
		t.Errorf("the checkout's branch reads %+v", scratch)
	}
	if _, err := time.Parse(time.RFC3339, local.LastCommit); err != nil || !strings.HasSuffix(local.LastCommit, "Z") {
		t.Errorf("the last commit reads %q: %v", local.LastCommit, err)
	}
	if len(found.Branches) != 3 {
		t.Errorf("branches %+v", found.Branches)
	}
}

// ADR-0023: two inventories taken at the same commits are the same bytes, whatever the checkout's working
// tree holds in between.
func TestTwoInventoriesAtTheSameCommitsAreTheSameBytes(t *testing.T) {
	t.Parallel()
	_, clone, base := existing(t)
	take := func() []byte {
		t.Helper()
		found, err := inventory.Git{Env: clone.Env()}.Take(t.Context(), ports.InventoryRequest{
			Path: clone.Dir, Repository: "id-payments", Name: "payments", BaseBranch: "origin/main", BaseCommit: base,
		})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := planning.RenderInventory(planning.Inventory{Repositories: []planning.RepositoryInventory{found}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	first := take()
	// The working tree moves and holds uncommitted work, and no commit or ref changes.
	clone.Git("switch", "--quiet", "wip/local")
	if err := os.WriteFile(filepath.Join(clone.Dir, "README.md"), []byte("# Something else\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := take()
	if !bytes.Equal(first, second) {
		t.Errorf("two inventories at the same commits differ:\n%s\n%s", first, second)
	}
}

func TestAnInventoryOfACommitTheRepositoryLacksIsNotFound(t *testing.T) {
	t.Parallel()
	_, clone, _ := existing(t)
	_, err := inventory.Git{Env: clone.Env()}.Take(t.Context(), ports.InventoryRequest{
		Path: clone.Dir, Repository: "id", Name: "payments", BaseBranch: "main", BaseCommit: "0000000000000000000000000000000000000000",
	})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a commit the repository lacks: %v", err)
	}
}
