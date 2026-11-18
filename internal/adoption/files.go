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
		case listed.Action == planning.ActionAppend && listed.Path == planning.GitignorePath:
			files = append(files, ports.TreeFile{Path: listed.Path, Lines: slices.Clone(listed.Lines)})
		default:
			return nil, fmt.Errorf("the plan lists %s %s, which no worker writes", listed.Action, listed.Path)
		}
	}
	return files, nil
}

// renderListed writes a file the layout lacks from the template the plan names, over the project and the repositories
// a Makefile can carry: a repository whose clone is at repos/<name> under a name a shell word holds, with its remote
// when that is one a Makefile can carry, a clone's local path as the file URL that names it, and none otherwise. A repository the Makefile cannot carry is in the manifest
// alone, which holds every one.
func renderListed(listed planning.PlannedFile, project string, repositories []planning.WorkspaceRepository, render projectfiles.Render) (string, error) {
	if render == nil {
		return "", fmt.Errorf("no renderer writes %s from the template %s", listed.Path, listed.Template)
	}
	data := projectfiles.Data{Project: project}
	for _, repository := range repositories {
		if !makeWord(repository.Name) || repository.Path != path.Join(CodeFolder, repository.Name) {
			continue
		}
		remote := repository.Remote
		if strings.HasPrefix(remote, "/") {
			remote = "file://" + remote
		}
		if projectfiles.CheckRemote(remote) != nil {
			remote = ""
		}
		data.Repositories = append(data.Repositories, projectfiles.CodeRepository{Name: repository.Name, Remote: remote})
	}
	text, err := render(listed.Template, data)
	if err != nil {
		return "", fmt.Errorf("render %s from %s: %w", listed.Path, listed.Template, err)
	}
	return text, nil
}

// makeWord says a repository's name is one a Makefile and its shell recipes read as the word it is: a name the ledger
// accepts, made of letters, digits, and . _ - only, so no make variable, comment, or shell syntax can be written in it.
func makeWord(name string) bool {
	return ledger.CheckRepositoryName(name) == nil && strings.Trim(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") == ""
}

// workspaceRepositories are the repositories the project holds beside the planning repository, as the manifest lists
// them: by name, with the remote of each clone, and the path of each relative to the planning repository's clone.
// A clone the plan moves is listed where the move leads, and one inside the planning clone where it is.
func workspaceRepositories(ctx context.Context, plan planning.AdoptionPlan, kept ports.Checkouts) ([]planning.WorkspaceRepository, error) {
	home := plan.Planning.Path
	var listed []planning.PlannedRepository
	var paths []string
	for _, repository := range plan.Repositories {
		switch {
		case repository.Outcome == planning.OutcomeNotRegistered, repository.Outcome == string(ledger.RegistrationRefused),
			repository.Name == "", repository.Path == home:
			continue
		}
		listed = append(listed, repository)
		paths = append(paths, repository.Path)
	}
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
	leadsTo := map[string]string{}
	for _, move := range plan.Moves {
		leadsTo[move.From] = move.To
	}
	var out []planning.WorkspaceRepository
	for _, repository := range listed {
		at := repository.Path
		if to, moved := leadsTo[at]; moved {
			at = to
		}
		relative, inside := strings.CutPrefix(at, home+"/")
		if !inside {
			relative = path.Join(CodeFolder, repository.Name)
		}
		out = append(out, planning.WorkspaceRepository{Name: repository.Name, Remote: remotes[repository.Path], Path: relative})
	}
	return out, nil
}
