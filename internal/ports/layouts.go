package ports

import (
	"context"
	"path"
	"slices"
	"strings"
)

// CodeTree is a directory of a repository that holds code, which a build file says.
type CodeTree struct {
	// Path is the directory relative to the repository's top, with slashes. A build file at the top makes the
	// whole repository the tree, and its path is ".".
	Path string `json:"path"`
	// BuildFile is the path of the build file, relative to the repository's top.
	BuildFile string `json:"build_file"`
}

// PlanContent is what a clone holds, at its default branch's tip, of the repository file format
// (ADR-0013).
type PlanContent struct {
	// Files counts the files a reader of the format reads with a kind it knows.
	Files int
	// Manifest says one of them is a repository manifest.
	Manifest bool
}

// Held says the clone holds plan content.
func (p PlanContent) Held() bool { return p.Files > 0 }

// Layouts reads how the clones on a worker's machine are laid out, for `ropeshift adopt` (FR-ADOPT-12). It
// reads only what a clone commits and what its ignore rules say, and it writes nothing. A clone is named by the
// path of its top, and what it holds is read at the tip of its default branch.
type Layouts interface {
	// Ignored lists the directories the clone's ignore rules leave out of its working tree, relative to its top,
	// with slashes and no trailing one, in order. A directory inside one of them is not listed.
	Ignored(ctx context.Context, clone string) ([]string, error)
	// Plan says what plan content the clone holds.
	Plan(ctx context.Context, clone string) (PlanContent, error)
	// Documents counts the Markdown documents the clone commits.
	Documents(ctx context.Context, clone string) (int, error)
	// Tip is the commit the clone's default branch stands at.
	Tip(ctx context.Context, clone string) (string, error)
	// Tracked lists every path a commit of the clone holds, with slashes, in order.
	Tracked(ctx context.Context, clone, commit string) ([]string, error)
	// CodeTrees lists the code trees of the clone's default branch, in order.
	CodeTrees(ctx context.Context, clone string) ([]CodeTree, error)
	// Instructions says the clone holds an instruction file, AGENTS.md or CLAUDE.md, at its top.
	Instructions(ctx context.Context, clone string) (bool, error)
}

// buildFiles are the files that make a directory a code tree, in the order one is named when a directory
// holds several. A file named for a project of any name, `*.csproj`, comes last.
var buildFiles = []string{
	"go.mod", "package.json", "Cargo.toml", "pyproject.toml", "setup.py", "pom.xml", "build.gradle", "build.gradle.kts",
	"Gemfile", "mix.exs", "composer.json", "CMakeLists.txt",
}

// buildFileRank is where a file name stands among the build files, or -1 when it is none.
func buildFileRank(name string) int {
	if at := slices.Index(buildFiles, name); at >= 0 {
		return at
	}
	if stem, ok := strings.CutSuffix(name, ".csproj"); ok && stem != "" {
		return len(buildFiles)
	}
	return -1
}

// CodeTreesOf reads the code trees of a repository from the paths it tracks: the topmost directory below the
// top that holds a build file, with the first build file the directory holds. A build file at the top makes
// the whole repository the one tree. The trees come in order of path.
func CodeTreesOf(paths []string) []CodeTree {
	best := map[string]string{}
	for _, tracked := range paths {
		name := path.Base(tracked)
		rank := buildFileRank(name)
		if rank < 0 {
			continue
		}
		dir := path.Dir(tracked)
		if held, ok := best[dir]; ok && buildFileRank(held) <= rank {
			continue
		}
		best[dir] = name
	}
	if name, ok := best["."]; ok {
		return []CodeTree{{Path: ".", BuildFile: name}}
	}
	var dirs []string
	for dir := range best {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	var trees []CodeTree
	for _, dir := range dirs {
		if slices.ContainsFunc(trees, func(above CodeTree) bool { return strings.HasPrefix(dir, above.Path+"/") }) {
			continue
		}
		trees = append(trees, CodeTree{Path: dir, BuildFile: path.Join(dir, best[dir])})
	}
	return trees
}

// HoldsInstructions says a list of tracked paths holds an instruction file, AGENTS.md or CLAUDE.md, at the top.
func HoldsInstructions(paths []string) bool {
	return slices.Contains(paths, "AGENTS.md") || slices.Contains(paths, "CLAUDE.md")
}
