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
)

// Files are the files a worker adds to the planning repository's worktree for the files a plan lists, in the
// order the plan lists them: the manifest of the project's repositories, the lines of the ignore file, and the
// Makefile, each for a file the plan says the repository lacks. It reads each clone's remote through kept, and
// writes nothing. The manifest and the Makefile list the repositories as they sit once the moves the plan lists are
// made, in the folder of clones, so the files fit a machine that has made them and one that clones into the folder.
func Files(ctx context.Context, plan planning.AdoptionPlan, project string, kept ports.Checkouts) ([]ports.TreeFile, error) {
	var repositories []planning.WorkspaceRepository
	needsList := slices.ContainsFunc(plan.Files, func(file planning.PlannedFile) bool {
		return file.Action == planning.ActionAdd && (file.Path == ManifestPath || file.Path == planning.MakefilePath)
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
		case listed.Action == planning.ActionAdd && listed.Path == ManifestPath:
			files = append(files, ports.TreeFile{Path: listed.Path, Text: planning.RenderRepositoriesFile(project, repositories)})
		case listed.Action == planning.ActionAdd && listed.Path == planning.MakefilePath:
			files = append(files, ports.TreeFile{Path: listed.Path, Text: planning.RenderWorkspaceMakefile(repositories)})
		case listed.Action == planning.ActionAppend && listed.Path == planning.GitignorePath:
			files = append(files, ports.TreeFile{Path: listed.Path, Lines: slices.Clone(listed.Lines)})
		default:
			return nil, fmt.Errorf("the plan lists %s %s, which no worker writes", listed.Action, listed.Path)
		}
	}
	return files, nil
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
