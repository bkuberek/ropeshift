package worker

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
)

// folderRules are what a folder check allows beyond an empty folder whose parent is there.
type folderRules struct {
	// files are the shape's files, which the folder may hold, and started says the write started before, so a
	// repository in the folder is its own.
	files   []projectfiles.File
	started bool
	// parentsMade says the caller makes the folders above the folder that are not there yet once every check
	// passed, so a missing parent is not refused.
	parentsMade bool
	// ignoreLine adds to the refusal of a folder inside a clone the line the clone's .gitignore needs to ignore it.
	ignoreLine bool
}

func folderRefusal(reason, detail string) *ledger.TemplateWriteReport {
	return &ledger.TemplateWriteReport{Outcome: ledger.TemplateOutcomeRefused, Reason: reason, Detail: detail}
}

// spellingRefusal refuses a folder that is not written as an absolute path in its clean form, with no .. element.
func spellingRefusal(at string) *ledger.TemplateWriteReport {
	switch {
	case !strings.HasPrefix(at, "/"):
		return folderRefusal(ledger.TemplateRefusedNotAbsolute, "the folder is not an absolute path")
	case ledger.HasParentElement(at):
		return folderRefusal(ledger.TemplateRefusedParentElement, "the folder holds a .. element")
	case at != path.Clean(at):
		return folderRefusal(ledger.TemplateRefusedNotClean, "the folder is not written in its clean form, with no trailing slash, //, or /./")
	}
	return nil
}

// checkFolder runs the checks of the folder a write makes, after its spelling, and refuses with the first that fails:
// the folder's parent, a link, something that is not a folder, a folder ropeshift keeps for itself, a folder that
// holds files, and a folder inside a checkout this worker keeps that does not ignore it.
func (w *Worker) checkFolder(ctx context.Context, rt *TemplateRuntime, at string, found ports.FolderInspection, rules folderRules) (*ledger.TemplateWriteReport, error) {
	switch {
	case !found.ParentDirectory && !rules.parentsMade:
		return folderRefusal(ledger.TemplateRefusedNoParent, "the folder's parent does not exist, and a write makes no parent"), nil
	case found.Link:
		return folderRefusal(ledger.TemplateRefusedLink, "the folder is a symbolic link"), nil
	case found.Exists && !found.Directory:
		return folderRefusal(ledger.TemplateRefusedNotDirectory, "something that is not a folder is there"), nil
	}
	if reserved, err := w.insideReserved(ctx, rt, found.Real); err != nil {
		return nil, err
	} else if reserved {
		return folderRefusal(ledger.TemplateRefusedReserved, "the folder is at or inside a folder ropeshift keeps for itself"), nil
	}
	if found.Exists && !found.Empty {
		foreign, repositories, err := w.foreignEntries(ctx, rt, at, rules.files, rules.started)
		if err != nil {
			return nil, err
		}
		if len(foreign) > 0 || len(repositories) > 0 {
			return folderRefusal(ledger.TemplateRefusedNotEmpty, refusalDetail(foreign, repositories)), nil
		}
	}
	inside, err := w.insideKeptClone(ctx, rt, found.Real)
	if err != nil || !inside.inside {
		return nil, err
	}
	detail := inside.detail
	if rules.ignoreLine && inside.line != "" {
		detail += fmt.Sprintf("; add the line %s to %s", inside.line, filepath.Join(inside.top, ".gitignore"))
	}
	return folderRefusal(ledger.TemplateRefusedInsideClone, detail), nil
}

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

// keptClone is what insideKeptClone found of the checkout a folder lies in.
type keptClone struct {
	inside bool
	// detail names the clone and its project, top is the clone's top, and line is the line of its .gitignore that
	// would ignore the folder, empty when the folder is the clone's top.
	detail string
	top    string
	line   string
}

// insideKeptClone says the folder is a checkout this worker keeps, in any project, or lies inside one, unless
// that clone ignores the folder, as a workspace's repos/ is ignored.
func (w *Worker) insideKeptClone(ctx context.Context, rt *TemplateRuntime, real string) (keptClone, error) {
	if rt.Checkouts == nil {
		return keptClone{}, nil
	}
	kept, err := rt.Checkouts.Every(ctx)
	if err != nil {
		return keptClone{}, err
	}
	clone, inside, err := ports.InsideClone(ctx, rt.Checkouts, kept, real, "")
	if err != nil || !inside {
		return keptClone{}, err
	}
	top, _, err := rt.Checkouts.Resolve(ctx, clone)
	if err != nil {
		return keptClone{}, err
	}
	detail, err := w.describeClone(ctx, rt, clone)
	if err != nil {
		return keptClone{}, err
	}
	found := keptClone{inside: true, detail: detail, top: top}
	rel, err := filepath.Rel(top, real)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return found, nil
	}
	ignored, err := rt.Checkouts.Ignored(ctx, top, filepath.ToSlash(rel))
	if err != nil || ignored {
		return keptClone{}, err
	}
	found.line = "/" + filepath.ToSlash(rel) + "/"
	return found, nil
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
