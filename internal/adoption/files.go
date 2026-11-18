package adoption

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
)

// Files are the files a worker adds to the planning repository's worktree for the files a plan lists, in the
// order the plan lists them: the manifest of the project's repositories, the ignore file or its lines, the
// Makefile, and the instruction files, each for a file the plan says the repository lacks. It reads each clone's
// remote through kept, renders each file that names a template through render, and writes nothing. The manifest and
// the Makefile list the repositories as they sit once the moves the plan lists are made, in the folder of clones, so
// the files fit a machine that has made them and one that clones into the folder.
func Files(ctx context.Context, plan planning.AdoptionPlan, project string, kept ports.Checkouts, render projectfiles.Render) ([]ports.TreeFile, error) {
	var repositories []planning.WorkspaceRepository
	needsList := slices.ContainsFunc(plan.Files, func(file planning.PlannedFile) bool {
		return file.Action == planning.ActionAdd && (file.Path == ManifestPath || file.Template != "")
	})
	if needsList {
		var err error
		if repositories, err = workspaceRepositories(ctx, plan, kept); err != nil {
			return nil, err
		}
	}
	var files []ports.TreeFile
	for _, listed := range plan.Files {
		switch {
		case listed.Action == planning.ActionAdd && listed.Template != "":
			text, err := renderListed(listed, project, repositories, render)
			if err != nil {
				return nil, err
			}
			files = append(files, ports.TreeFile{Path: listed.Path, Text: text})
		case listed.Action == planning.ActionAdd && listed.Path == ManifestPath:
			files = append(files, ports.TreeFile{Path: listed.Path, Text: planning.RenderRepositoriesFile(project, repositories)})
		case listed.Action == planning.ActionAppend && strings.EqualFold(listed.Path, planning.GitignorePath):
			files = append(files, ports.TreeFile{Path: listed.Path, Lines: slices.Clone(listed.Lines)})
		default:
			return nil, fmt.Errorf("the plan lists %s %s, which no worker writes", listed.Action, listed.Path)
		}
	}
	return files, nil
}

// renderListed writes a file the layout lacks from the template the plan names, over the project and the repositories
// a Makefile can carry: each repository whose name and path pass makeWord and makePath, at any depth, with its remote
// when that is one a Makefile can carry, a clone's local path as the file URL that names it, and none otherwise. A
// repository at another path than repos/<name> is listed with its path. The manifest holds every repository.
func renderListed(listed planning.PlannedFile, project string, repositories []planning.WorkspaceRepository, render projectfiles.Render) (string, error) {
	if render == nil {
		return "", fmt.Errorf("no renderer writes %s from the template %s", listed.Path, listed.Template)
	}
	carried, _ := splitCarried(repositories)
	text, err := render(listed.Template, StartingData(project, carried))
	if err != nil {
		return "", fmt.Errorf("render %s from %s: %w", listed.Path, listed.Template, err)
	}
	return text, nil
}

// StartingData is what a starting file renders over: the project and each repository as a Makefile and a table of
// repositories carry it. The remote is kept when it is one a Makefile can carry, a clone's local path becomes the file
// URL that names it, and any other remote is left empty. A path other than repos/<name> is kept.
func StartingData(project string, repositories []planning.WorkspaceRepository) projectfiles.Data {
	data := projectfiles.Data{Project: project}
	for _, repository := range repositories {
		remote := repository.Remote
		if strings.HasPrefix(remote, "/") {
			remote = "file://" + remote
		}
		if projectfiles.CheckRemote(remote) != nil {
			remote = ""
		}
		at := repository.Path
		if at == path.Join(CodeFolder, repository.Name) {
			at = ""
		}
		data.Repositories = append(data.Repositories, projectfiles.CodeRepository{Name: repository.Name, Remote: remote, Path: at})
	}
	return data
}

// Carried says a Makefile can carry the repository: it lists its name and its path as words its recipes read.
func Carried(repository planning.WorkspaceRepository) bool {
	return makeWord(repository.Name) && makePath(repository.Path)
}

// splitCarried sorts repositories into those a Makefile carries and the omissions for the rest, each with why.
func splitCarried(repositories []planning.WorkspaceRepository) ([]planning.WorkspaceRepository, []planning.Omission) {
	var carried []planning.WorkspaceRepository
	var left []planning.Omission
	for _, repository := range repositories {
		switch {
		case !makeWord(repository.Name):
			left = append(left, planning.Omission{Name: repository.Name, Path: repository.Path,
				Reason: "its name is not made of letters, digits, \".\", \"_\", and \"-\", or it starts with \"-\""})
		case !makePath(repository.Path):
			left = append(left, planning.Omission{Name: repository.Name, Path: repository.Path,
				Reason: "its path has a segment that is empty, \".\", \"..\", or not made of letters, digits, \".\", \"_\", and \"-\", or it starts with \"-\""})
		default:
			carried = append(carried, repository)
		}
	}
	return carried, left
}

// makeWord says a repository's name is one a Makefile and its shell recipes read as the word it is: a name the ledger
// accepts, made of letters, digits, and . _ - only, so no make variable, comment, or shell syntax can be written in it.
func makeWord(name string) bool {
	return ledger.CheckRepositoryName(name) == nil && safeSegment(name)
}

// makePath says a path relative to the planning repository is one a Makefile and its shell recipes read as the path it
// is: segments joined by a slash, each non-empty, neither . nor .., and made of letters, digits, and . _ - only, and
// the first not starting with a dash, which a command would read as an option. The ledger's name check refuses a slash
// and refuses . and .. only as a whole name, so each segment is checked here.
func makePath(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "-") {
		return false
	}
	for segment := range strings.SplitSeq(rel, "/") {
		if segment == "." || segment == ".." || !safeSegment(segment) {
			return false
		}
	}
	return true
}

// safeSegment says a word is not empty and holds only letters, digits, and . _ -.
func safeSegment(word string) bool {
	return word != "" && strings.Trim(word, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") == ""
}

// workspaceRepositories are the repositories the project holds beside the planning repository, as the manifest lists
// them: by name, with the remote of each clone, and the path of each relative to the planning repository's clone.
// A clone the plan moves is listed where the move leads, and one inside the planning clone where it is.
func workspaceRepositories(ctx context.Context, plan planning.AdoptionPlan, kept ports.Checkouts) ([]planning.WorkspaceRepository, error) {
	listed, origins := placedRepositories(plan)
	paths := origins
	remotes := map[string]string{}
	if len(paths) != 0 {
		clones, _, err := kept.Find(ctx, paths, 1)
		if err != nil {
			return nil, err
		}
		for _, clone := range clones {
			remotes[clone.Path] = clone.Remote
		}
	}
	for i := range listed {
		listed[i].Remote = remotes[origins[i]]
	}
	return listed, nil
}

// placedRepositories lists the repositories the project holds beside the planning repository as they sit once the
// plan's moves are made, with no remote, and beside each the path of the clone as it is now.
func placedRepositories(plan planning.AdoptionPlan) ([]planning.WorkspaceRepository, []string) {
	home := plan.Planning.Path
	leadsTo := map[string]string{}
	for _, move := range plan.Moves {
		leadsTo[move.From] = move.To
	}
	var out []planning.WorkspaceRepository
	var origins []string
	for _, repository := range plan.Repositories {
		switch {
		case repository.Outcome == planning.OutcomeNotRegistered, repository.Outcome == string(ledger.RegistrationRefused),
			repository.Name == "", repository.Path == home:
			continue
		}
		at := repository.Path
		if to, moved := leadsTo[at]; moved {
			at = to
		}
		relative, inside := strings.CutPrefix(at, home+"/")
		if !inside {
			relative = path.Join(CodeFolder, repository.Name)
		}
		out = append(out, planning.WorkspaceRepository{Name: repository.Name, Path: relative})
		origins = append(origins, repository.Path)
	}
	return out, origins
}

// makefileLeftOut says which repositories the Makefile the plan writes leaves out, and why, or none when the plan
// writes no Makefile.
func makefileLeftOut(plan planning.AdoptionPlan) []planning.Omission {
	if !slices.ContainsFunc(plan.Files, func(file planning.PlannedFile) bool {
		return file.Action == planning.ActionAdd && file.Template == templateMakefile
	}) {
		return nil
	}
	listed, _ := placedRepositories(plan)
	_, left := splitCarried(listed)
	return left
}
