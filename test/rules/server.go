package rules

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// A group server is the control plane alone (ADR-0031, FR-DEP-7). These checks hold its wiring free of
// everything that belongs to a worker's machine, and hold the Postgres adapter to the one file that wires it,
// so solo mode's wiring runs on SQLite alone.

const (
	serverFile = entryDir + "/server.go"
	// moduleRoot is the module's import path, which the checks cut off an import to name a package by its path.
	moduleRoot = "ropeshift.com/ropeshift"
	// pgxModule is the Postgres driver's module path. Its packages are under it.
	pgxModule = "github.com/jackc/pgx"
)

// workerMachineAdapters are the adapters of a worker's machine that the server's wiring never imports: the
// terminal, the checkouts, and the agent driver. Worktrees are one use of the git adapter, which the check
// refuses by name, because the control plane reads git only through mirrors.
var workerMachineAdapters = []string{"internal/adapters/tmux", "internal/adapters/checkouts", "internal/adapters/claudecode"}

const (
	gitAdapter    = "internal/adapters/gitcli"
	worktreesName = "Worktrees"
)

// ServerWiringFiles lists the files the server wiring check reads: server.go and every server_*.go of the
// entry, apart from tests.
func ServerWiringFiles(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(entryDir)))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !isSource(name) {
			continue
		}
		if name == "server.go" || strings.HasPrefix(name, "server_") {
			files = append(files, entryDir+"/"+name)
		}
	}
	return files, nil
}

// CheckServerWiring refuses, in the files of the server's wiring, any import of an adapter of a worker's
// machine, however the import is named, and any use of gitcli.Worktrees.
func CheckServerWiring(root string) ([]Violation, error) {
	files, err := ServerWiringFiles(root)
	if err != nil {
		return nil, err
	}
	var found []Violation
	for _, rel := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, filepath.FromSlash(rel)), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		gitName := ""
		for _, spec := range parsed.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return nil, err
			}
			imported := internalPath(path)
			for _, adapter := range workerMachineAdapters {
				if underDir(imported, adapter) {
					found = append(found, Violation{"server-wiring", rel, "imports " + imported + ", which a worker's machine holds"})
				}
			}
			if imported == gitAdapter {
				gitName = "gitcli"
				if spec.Name != nil {
					gitName = spec.Name.Name
				}
			}
		}
		if gitName == "" {
			continue
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel.Name == worktreesName {
				if name, ok := selector.X.(*ast.Ident); ok && name.Name == gitName {
					found = append(found, Violation{"server-wiring", rel, "uses gitcli.Worktrees, which a worker's machine holds"})
				}
			}
			return true
		})
	}
	return found, nil
}

// internalPath cuts an import path of the module to its path from the module's root, and leaves any other
// import as it is.
func internalPath(path string) string {
	if rel, ok := strings.CutPrefix(path, moduleRoot+"/"); ok {
		return rel
	}
	return path
}

// CheckPostgresOnlyInTheServer refuses a non-test Go file that imports the Postgres adapter, a package
// under it, or a package of the pgx driver, unless it is the server's wiring or a file of the adapter
// itself. Solo mode's wiring runs on SQLite alone, so no solo file and no other adapter reaches Postgres.
func CheckPostgresOnlyInTheServer(root string) ([]Violation, error) {
	var found []Violation
	for _, top := range []string{"cmd", "internal"} {
		dir := filepath.Join(root, top)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "testdata" {
				return filepath.SkipDir
			}
			if d.IsDir() || !isSource(d.Name()) {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if rel == serverFile || underDir(rel, postgresAdapter) {
				return nil
			}
			imports, err := fileImports(path)
			if err != nil {
				return err
			}
			for _, imported := range imports {
				if name, refused := postgresImport(imported); refused {
					found = append(found, Violation{"postgres-only-in-the-server", rel, "imports " + name})
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	slices.SortFunc(found, func(a, b Violation) int { return strings.Compare(a.String(), b.String()) })
	return found, nil
}

// postgresImport names an import that is the Postgres adapter, a package under it, or a package of the
// driver.
func postgresImport(imported string) (string, bool) {
	if imported == pgxModule || strings.HasPrefix(imported, pgxModule+"/") {
		return imported, true
	}
	if rel := internalPath(imported); rel != imported && underDir(rel, postgresAdapter) {
		return rel, true
	}
	return "", false
}
