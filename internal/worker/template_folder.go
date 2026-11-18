package worker

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
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

// writeTemplate writes the files the folder lacks, makes the first commit when the journal says to, and builds
// the report. A commit that fails keeps the files and says why.
func (w *Worker) writeTemplate(ctx context.Context, rt *TemplateRuntime, request ledger.DesiredTemplateWrite, journal templateJournal, data projectfiles.Data, files []projectfiles.File) (ledger.TemplateWriteReport, templateJournal, error) {
	folderFiles := make([]ports.FolderFile, 0, len(files))
	paths := make([]string, 0, len(files))
	for _, file := range files {
		folderFiles = append(folderFiles, ports.FolderFile{Path: file.Path, Text: file.Text})
		paths = append(paths, file.Path)
	}
	changed := func() (ledger.TemplateWriteReport, templateJournal, error) {
		return ledger.TemplateWriteReport{
			Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedChanged,
			Detail: "the folder is no longer the one this worker wrote: it was swapped for a link or for another folder",
		}, journal, nil
	}
	folder, err := rt.Folders.Write(ctx, request.Path, folderFiles, ports.FolderID(journal.Folder))
	if errors.Is(err, ports.ErrFolderChanged) {
		return changed()
	}
	if err != nil {
		return ledger.TemplateWriteReport{}, journal, fmt.Errorf("writing the files: %w", err)
	}
	// The folder written is kept, so a resume and the first commit are held to that folder and no other at the path.
	if journal.Folder == "" {
		journal.Folder = string(folder)
		if err := rt.save(request.ID, journal); err != nil {
			return ledger.TemplateWriteReport{}, journal, err
		}
	}
	name := data.Project
	if request.Project != "" {
		name = request.Project
	}
	report := ledger.TemplateWriteReport{Outcome: ledger.TemplateOutcomeWritten, Project: data.Project, Files: paths}
	if journal.Commit {
		// The journal says the worker is about to run git init before it does, so a crash after it finds its own repository.
		resumed := journal.Initialized
		if !resumed {
			journal.Initialized = true
			if err := rt.save(request.ID, journal); err != nil {
				return ledger.TemplateWriteReport{}, journal, err
			}
		}
		commit, err := rt.Folders.FirstCommit(ctx, request.Path, "chore: start "+name, resumed, ports.FolderID(journal.Folder))
		if errors.Is(err, ports.ErrFolderChanged) {
			return changed()
		}
		if err != nil {
			report.CommitError = err.Error()
		} else {
			report.Commit = commit
		}
	}
	after, err := rt.Folders.Inspect(ctx, request.Path)
	if err != nil {
		return ledger.TemplateWriteReport{}, journal, err
	}
	report.Clone = report.Commit != "" && after.CloneTop != "" && after.CloneTop == after.Real
	var repositories []projectfiles.CodeRepository
	repositories = append(repositories, data.Repositories...)
	report.Commands = projectfiles.Commands(projectfiles.Shape(request.Shape), request.Path, name, repositories, journal.NeedsRepository && report.Commit == "")
	return report, journal, nil
}
