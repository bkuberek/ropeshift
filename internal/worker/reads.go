package worker

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// readsDir is where a lane's read-only worktrees of other repositories sit: beside its own worktree and
// outside it, so nothing the lane commits holds them, and its policy denies every write there.
func readsDir(worktree string) string { return worktree + ".reads" }

// readPlace is where one repository's read-only worktree sits, by its name, or by its identity when its
// name could not stand as one directory.
func readPlace(worktree string, read ledger.RepositoryRead) string {
	name := read.Name
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		name = read.Repository
	}
	return filepath.Join(readsDir(worktree), name)
}

// prepareReads checks out each repository this lane reads, at the commit its claim recorded and with no
// branch, beside the lane's worktree (ADR-0022). Only the repositories of the lane's project are read,
// because the claim names only those and the checkouts are looked up in the lane's project. One that cannot
// be checked out does not stop the lane: the brief says what it could not read and why.
func (r *laneRun) prepareReads(ctx context.Context, worktree string) []lanes.ReadTree {
	if len(r.lease.Reads) == 0 {
		return nil
	}
	trees, offered := r.rt.Provisioner.Worktrees.(ports.ReviewTrees)
	out := make([]lanes.ReadTree, 0, len(r.lease.Reads))
	for _, read := range r.lease.Reads {
		tree := lanes.ReadTree{Name: cmp.Or(read.Name, read.Repository), Ref: read.Ref, Commit: read.Commit, Detail: read.Detail}
		switch {
		case read.Commit == "":
		case !offered:
			tree.Detail = "this worker's worktrees cannot check a commit out with no branch"
		default:
			tree.Path, tree.Detail = r.checkOutRead(ctx, trees, worktree, read)
		}
		out = append(out, tree)
	}
	return out
}

// checkOutRead makes one read-only worktree, or says why it could not. A worktree an earlier attempt made
// at the same commit is the answer to a retry.
func (r *laneRun) checkOutRead(ctx context.Context, trees ports.ReviewTrees, worktree string, read ledger.RepositoryRead) (path, detail string) {
	repository, err := r.rt.Repository(ctx, r.lease.Project, read.Repository)
	if err != nil {
		return "", fmt.Sprintf("this machine keeps no checkout of it: %v", err)
	}
	place := readPlace(worktree, read)
	if _, err := trees.Create(ctx, ports.WorktreeSpec{Repository: repository, Path: place, Base: read.Commit, Detached: true}); err != nil {
		return "", fmt.Sprintf("checking it out at %s failed: %v", read.Commit, err)
	}
	return place, ""
}

// readDirs are the paths of the read-only worktrees a session may read.
func readDirs(trees []lanes.ReadTree) []string {
	var out []string
	for _, tree := range trees {
		if tree.Path != "" {
			out = append(out, tree.Path)
		}
	}
	return out
}

// discardReads removes the read-only worktrees when the lane ends. They hold nothing to keep: a lane never
// writes there, and the commit each one stood at is in its repository's history.
func (r *laneRun) discardReads(ctx context.Context) {
	worktree := r.worktreePath()
	if worktree == "" || len(r.lease.Reads) == 0 {
		return
	}
	for _, read := range r.lease.Reads {
		if read.Commit == "" {
			continue
		}
		repository, err := r.rt.Repository(ctx, r.lease.Project, read.Repository)
		if err != nil {
			continue
		}
		if err := r.rt.Provisioner.Worktrees.Discard(ctx, repository, readPlace(worktree, read)); err != nil {
			r.w.config.Report(fmt.Errorf("lane %s: removing its read-only worktree of %s: %w", r.lease.Lane, cmp.Or(read.Name, read.Repository), err))
		}
	}
	_ = os.Remove(readsDir(worktree))
}

func (r *laneRun) setWorktreePath(path string) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.worktree = path
}

func (r *laneRun) worktreePath() string {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.worktree
}

func (r *laneRun) setReads(trees []lanes.ReadTree) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.reads = trees
}

func (r *laneRun) readTrees() []lanes.ReadTree {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.reads
}
