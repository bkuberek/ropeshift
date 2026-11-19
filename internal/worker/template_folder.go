package worker

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
)

// fileManagerFiles are the files a file manager leaves in any folder it shows, which a write accepts anywhere, leaves as
// they are, and never adds to a commit.
var fileManagerFiles = []string{".DS_Store", "Thumbs.db"}

// listedNames and nameLength bound what a refusal repeats of the folder: the first few names, each cut to a length.
const (
	listedNames = 5
	nameLength  = 80
)

// foreignEntries are the files the folder holds that the shape does not write, except the files a file manager leaves,
// and, for a write that has not started, the first .git of a repository the folder holds at any depth, none of which this
// worker made. It is the one rule of which folders a write may go into: a crash between the first file and the report
// leaves a folder of the shape's files, and so does a person who started the project's files by hand. It stops reading the
// folder at the first repository and at the sixth file of another kind, because a refusal names five and says there are
// more, so a large folder costs no more than a small one.
func (w *Worker) foreignEntries(ctx context.Context, rt *TemplateRuntime, folder string, files []projectfiles.File, started bool) (foreign, repositories []string, err error) {
	err = rt.Folders.Walk(ctx, folder, func(path string, git bool) bool {
		switch {
		case git:
			if !started {
				repositories = append(repositories, path)
				return false
			}
		case slices.Contains(fileManagerFiles, path[strings.LastIndex(path, "/")+1:]):
		case !slices.ContainsFunc(files, func(file projectfiles.File) bool { return file.Path == path }):
			foreign = append(foreign, path)
			return len(foreign) <= listedNames
		}
		return true
	})
	return foreign, repositories, err
}

// refusalDetail says what a folder holds that a write cannot go into. Each name is cleaned of escape sequences and
// overrides, quoted, and cut to a length, and only the first few are named, so a refusal carries no more than a person
// reads and a folder cannot choose how a refusal looks.
func refusalDetail(foreign, repositories []string) string {
	var parts []string
	if len(foreign) > 0 {
		parts = append(parts, "the folder holds "+listNames(foreign)+", which a write of this shape does not add to: it goes only into a folder whose files are its own")
	}
	if len(repositories) > 0 {
		parts = append(parts, "the folder holds "+listNames(repositories)+", a repository that ropeshift did not make")
	}
	return strings.Join(parts, "; ")
}

// listNames quotes the first listedNames of names, each cleaned and cut to nameLength characters, and says there are more
// when there are, with no count, because the worker stopped looking.
func listNames(names []string) string {
	shown := make([]string, 0, listedNames)
	for _, name := range names[:min(len(names), listedNames)] {
		name = ledger.PlainText(name)
		if runes := []rune(name); len(runes) > nameLength {
			name = string(runes[:nameLength]) + "..."
		}
		shown = append(shown, strconv.Quote(name))
	}
	out := strings.Join(shown, ", ")
	if len(names) > len(shown) {
		out += " and more"
	}
	return out
}

// insideReserved says the folder is at or inside a folder ropeshift keeps for itself: the state, run, or worktree
// folder the worker's checkouts name as reserved. A folder that is not there yet is judged by the nearest folder above
// it that is.
func (w *Worker) insideReserved(ctx context.Context, rt *TemplateRuntime, real string) (bool, error) {
	if rt.Checkouts == nil {
		return false, nil
	}
	for at := real; ; at = filepath.Dir(at) {
		_, reserved, err := rt.Checkouts.Resolve(ctx, at)
		switch {
		case err == nil:
			return reserved, nil
		case !errors.Is(err, ports.ErrNotFound):
			return false, err
		case filepath.Dir(at) == at:
			return false, nil
		}
	}
}

// insideKeptClone says the folder is a checkout this worker keeps, in any project, or lies inside one, unless
// that clone ignores the folder, as a workspace's repos/ is ignored. The detail names the clone and its project.
func (w *Worker) insideKeptClone(ctx context.Context, rt *TemplateRuntime, real string) (string, bool, error) {
	if rt.Checkouts == nil {
		return "", false, nil
	}
	kept, err := rt.Checkouts.Every(ctx)
	if err != nil {
		return "", false, err
	}
	clone, inside, err := ports.InsideClone(ctx, rt.Checkouts, kept, real, "")
	if err != nil || !inside {
		return "", false, err
	}
	top, _, err := rt.Checkouts.Resolve(ctx, clone)
	if err != nil {
		return "", false, err
	}
	detail, err := w.describeClone(ctx, rt, clone)
	if err != nil {
		return "", false, err
	}
	rel, err := filepath.Rel(top, real)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return detail, true, nil
	}
	ignored, err := rt.Checkouts.Ignored(ctx, top, filepath.ToSlash(rel))
	if err != nil {
		return "", false, err
	}
	return detail, !ignored, nil
}

// describeClone names a checkout this worker keeps, with the repository and project it is the checkout of.
func (w *Worker) describeClone(ctx context.Context, rt *TemplateRuntime, clone string) (string, error) {
	all, err := rt.Checkouts.All(ctx)
	if err != nil {
		return "", err
	}
	for _, checkout := range all {
		if checkout.Path == clone {
			return fmt.Sprintf("the folder is inside %s, this worker's checkout of repository %s in project %s, which does not ignore it", clone, checkout.Repository, checkout.Project), nil
		}
	}
	return fmt.Sprintf("the folder is inside %s, a checkout this worker keeps, which does not ignore it", clone), nil
}

// needsRepository says the folder needs a repository of its own for its first commit: no clone holds it, or the
// clone that holds it ignores it, as a workspace's ignored repos/ holds a code repository's folder. A folder a
// clone tracks has its files committed with that clone.
func (w *Worker) needsRepository(ctx context.Context, rt *TemplateRuntime, found ports.FolderInspection) (bool, error) {
	if found.CloneTop == "" {
		return true, nil
	}
	if found.CloneTop == found.Real || rt.Checkouts == nil {
		return false, nil
	}
	rel, err := filepath.Rel(found.CloneTop, found.Real)
	if err != nil {
		return false, err
	}
	return rt.Checkouts.Ignored(ctx, found.CloneTop, filepath.ToSlash(rel))
}
