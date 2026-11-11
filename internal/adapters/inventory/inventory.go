// Package inventory takes the inventory an adopt lane is given of each repository of its project before it
// starts (ADR-0023), from git, with no model.
package inventory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/adapters/repofiles"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// Git takes inventories with the git command line. It reads commits and refs and never a working tree, so
// what a checkout holds uncommitted changes nothing, and it writes nothing. Env replaces the environment of
// every command, which is how a test keeps its own configuration.
type Git struct {
	Env []string
}

func (g Git) git(dir string) gitcli.Git { return gitcli.Git{Dir: dir, Env: g.Env} }

// Take reads one repository: what it is, its Markdown at the base commit, and every branch the base does not
// hold.
func (g Git) Take(ctx context.Context, request ports.InventoryRequest) (planning.RepositoryInventory, error) {
	found := planning.RepositoryInventory{
		Name: request.Name, Repository: request.Repository, BaseBranch: request.BaseBranch, BaseCommit: request.BaseCommit,
	}
	git := g.git(request.Path)
	if request.BaseCommit == "" || strings.HasPrefix(request.BaseCommit, "-") {
		return found, fmt.Errorf("%w: no base commit to read", ports.ErrNotFound)
	}
	if _, err := git.Commit(ctx, request.BaseCommit); err != nil {
		return found, fmt.Errorf("%w: %s", ports.ErrNotFound, request.BaseCommit)
	}
	if err := g.identity(ctx, request.Path, &found); err != nil {
		return found, err
	}
	var err error
	if found.Documents, found.Files, err = g.documents(ctx, git, request.BaseCommit); err != nil {
		return found, err
	}
	found.Branches, err = g.branches(ctx, request.Path, request.BaseCommit)
	return found, err
}

// identity reads the remote, with no credential in it, the default branch, and where that branch stands:
// the remote's copy when there is one.
func (g Git) identity(ctx context.Context, repository string, found *planning.RepositoryInventory) error {
	description, err := repofiles.New(repofiles.Config{Repository: repository, Env: g.Env}).Describe(ctx)
	if err != nil {
		return err
	}
	found.Remote, found.DefaultBranch = description.Remote, description.DefaultBranch
	if found.DefaultBranch == "" {
		return nil
	}
	git := g.git(repository)
	remotes, err := git.Text(ctx, "remote")
	if err != nil {
		return err
	}
	names := strings.Fields(remotes)
	slices.Sort(names)
	if i := slices.Index(names, "origin"); i > 0 {
		names = append([]string{"origin"}, slices.Delete(names, i, i+1)...)
	}
	for _, ref := range append(remoteRefs(names, found.DefaultBranch), "refs/heads/"+found.DefaultBranch) {
		if tip, err := git.Commit(ctx, ref); err == nil {
			found.DefaultTip = tip
			return nil
		}
	}
	return nil
}

func remoteRefs(remotes []string, branch string) []string {
	out := make([]string, 0, len(remotes))
	for _, remote := range remotes {
		out = append(out, "refs/remotes/"+remote+"/"+branch)
	}
	return out
}

// isMarkdown says a path names a Markdown file.
func isMarkdown(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

// documents lists every Markdown file of a commit with the digest git gives its content, its front matter,
// and its headings, and the ones intake reads as the repository file format, with whether it reads each one
// cleanly. Every blob is read in one run of git.
func (g Git) documents(ctx context.Context, git gitcli.Git, commit string) ([]planning.InventoryDocument, []planning.InventoryFile, error) {
	listing, err := git.Output(ctx, "ls-tree", "-r", "-z", "--full-tree", commit)
	if err != nil {
		return nil, nil, err
	}
	type entry struct{ path, blob string }
	var entries []entry
	for line := range strings.SplitSeq(string(listing), "\x00") {
		meta, name, ok := strings.Cut(line, "\t")
		fields := strings.Fields(meta)
		if ok && len(fields) == 3 && fields[1] == "blob" && isMarkdown(name) {
			entries = append(entries, entry{path: name, blob: fields[2]})
		}
	}
	if len(entries) == 0 {
		return nil, nil, nil
	}
	var input bytes.Buffer
	for _, one := range entries {
		input.WriteString(one.blob + "\n")
	}
	out, err := git.Feed(ctx, input.Bytes(), "cat-file", "--batch")
	if err != nil {
		return nil, nil, err
	}
	var documents []planning.InventoryDocument
	var files []planning.InventoryFile
	for _, one := range entries {
		var content []byte
		if content, out, err = nextBlob(out, one.blob); err != nil {
			return nil, nil, err
		}
		format, headings := planning.ReadMarkdown(content)
		documents = append(documents, planning.InventoryDocument{
			Path: one.path, Digest: planning.DigestGitBlob + one.blob, FrontMatter: format, Headings: headings,
		})
		if opensWithFence(content) {
			files = append(files, fileOf(one.path, content))
		}
	}
	return documents, files, nil
}

// nextBlob reads one answer of `cat-file --batch`: a header naming the object and its size, the content,
// and a newline.
func nextBlob(out []byte, blob string) (content, rest []byte, err error) {
	header, body, found := bytes.Cut(out, []byte("\n"))
	fields := strings.Fields(string(header))
	if !found || len(fields) != 3 || fields[0] != blob {
		return nil, nil, fmt.Errorf("reading blob %s: git answered %q", blob, header)
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil || size > len(body) {
		return nil, nil, fmt.Errorf("reading blob %s: size %q", blob, fields[2])
	}
	return body[:size], bytes.TrimPrefix(body[size:], []byte("\n")), nil
}

// opensWithFence says a file opens with the `+++` line intake reads as the repository file format's front
// matter, closed or not, because intake reports one it cannot close.
func opensWithFence(content []byte) bool {
	first, _, _ := bytes.Cut(content, []byte("\n"))
	return bytes.Equal(bytes.TrimRight(first, "\r"), []byte("+++"))
}

// fileOf says what intake makes of a file that opens with TOML front matter: its kind and ID when it reads
// it, and the problem it would report when it does not.
func fileOf(name string, content []byte) planning.InventoryFile {
	file := planning.InventoryFile{Path: name}
	doc, err := repofiles.ParseFile(content)
	if err != nil {
		file.Problem = err.Error()
		return file
	}
	file.Kind, file.ID, file.Clean = doc.Kind, documentID(doc), true
	return file
}

func documentID(doc planning.Document) string {
	switch {
	case doc.Contract != nil:
		return doc.Contract.ID
	case doc.Goal != nil:
		return doc.Goal.ID
	case doc.Plan != nil:
		return doc.Plan.ID
	}
	head, _ := doc.Head()
	return head.ID
}

// branches lists every local and remote-tracking branch whose tip the base does not hold, with where it left
// the base, the commits on each side, and when its last commit was made. A remote's HEAD names another
// branch and is not one.
func (g Git) branches(ctx context.Context, repository, base string) ([]planning.InventoryBranch, error) {
	git := g.git(repository)
	listing, err := git.Text(ctx, "for-each-ref", "--format=%(refname)%09%(objectname)%09%(objecttype)%09%(symref)", "refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	bases := gitcli.Worktrees{Env: g.Env}
	var out []planning.InventoryBranch
	for line := range strings.SplitSeq(listing, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || fields[2] != "commit" || fields[3] != "" {
			continue
		}
		ref, tip := fields[0], fields[1]
		merged, err := bases.Contains(ctx, repository, tip, base)
		if err != nil {
			return nil, err
		}
		if merged {
			continue
		}
		branch := planning.InventoryBranch{Name: branchName(ref), Ref: ref, Tip: tip}
		switch shared, err := bases.MergeBase(ctx, repository, base, tip); {
		case err == nil:
			branch.MergeBase = shared
		case !errors.Is(err, ports.ErrNotFound):
			return nil, err
		}
		if branch.Behind, branch.Ahead, err = sides(ctx, git, base, tip); err != nil {
			return nil, err
		}
		if branch.LastCommit, err = lastCommit(ctx, git, tip); err != nil {
			return nil, err
		}
		out = append(out, branch)
	}
	return out, nil
}

// branchName is what a person calls a branch: a local one by its name, a remote-tracking one with its remote.
func branchName(ref string) string {
	if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
		return name
	}
	return strings.TrimPrefix(ref, "refs/remotes/")
}

// sides counts the commits the base holds that the branch lacks, and the ones the branch holds that the
// base lacks.
func sides(ctx context.Context, git gitcli.Git, base, tip string) (behind, ahead int, err error) {
	counts, err := git.Text(ctx, "rev-list", "--left-right", "--count", base+"..."+tip)
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(counts)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("counting commits of %s: git answered %q", tip, counts)
	}
	if behind, err = strconv.Atoi(fields[0]); err != nil {
		return 0, 0, err
	}
	ahead, err = strconv.Atoi(fields[1])
	return behind, ahead, err
}

// lastCommit is when a branch's last commit was made, in UTC, from the commit itself.
func lastCommit(ctx context.Context, git gitcli.Git, tip string) (string, error) {
	text, err := git.Text(ctx, "log", "-1", "--format=%ct", tip)
	if err != nil {
		return "", err
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return "", err
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339), nil
}

var _ ports.Inventory = Git{}
