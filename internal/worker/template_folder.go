package worker

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
)

// holdsOnly says a folder holds files of the shape and nothing else, which is how a crash between the first file
// and the report leaves it.
func (w *Worker) holdsOnly(ctx context.Context, rt *TemplateRuntime, folder string, files []projectfiles.File) (bool, error) {
	have, err := rt.Folders.Files(ctx, folder)
	if err != nil {
		return false, err
	}
	for _, path := range have {
		if !slices.ContainsFunc(files, func(file projectfiles.File) bool { return file.Path == path }) {
			return false, nil
		}
	}
	return true, nil
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
