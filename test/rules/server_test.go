package rules

import (
	"slices"
	"strings"
	"testing"
)

// TestTheServerWiringHoldsNoWorkerMachineAdapter holds FR-DEP-7: the control plane's own wiring imports
// no adapter of a worker's machine and builds no worktree.
func TestTheServerWiringHoldsNoWorkerMachineAdapter(t *testing.T) {
	root := repoRoot(t)
	found, err := CheckServerWiring(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range found {
		t.Error(v)
	}
	// The rule reads something: the server's wiring is where it says, and the files it reads are many.
	if !fileHolds(t, root, serverFile, "package entry") {
		t.Fatalf("%s is not where the server's wiring is, so the check reads nothing", serverFile)
	}
	read, err := ServerWiringFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(read, serverFile) || !slices.Contains(read, entryDir+"/server_reads.go") {
		t.Errorf("the check reads %v", read)
	}
}

func TestTheServerWiringCheckCanFail(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"tmux", "package entry\n\nimport \"ropeshift.com/ropeshift/internal/adapters/tmux\"\n\nvar _ = tmux.New\n", "internal/adapters/tmux"},
		{"checkouts", "package entry\n\nimport \"ropeshift.com/ropeshift/internal/adapters/checkouts\"\n\nvar _ = checkouts.File{}\n", "internal/adapters/checkouts"},
		{"the agent driver", "package entry\n\nimport \"ropeshift.com/ropeshift/internal/adapters/claudecode\"\n\nvar _ = claudecode.New\n", "internal/adapters/claudecode"},
		{"a renamed import", "package entry\n\nimport driver \"ropeshift.com/ropeshift/internal/adapters/claudecode\"\n\nvar _ = driver.New\n", "internal/adapters/claudecode"},
		{"a blank import", "package entry\n\nimport _ \"ropeshift.com/ropeshift/internal/adapters/tmux\"\n", "internal/adapters/tmux"},
		{"worktrees", "package entry\n\nimport \"ropeshift.com/ropeshift/internal/adapters/gitcli\"\n\nvar _ = gitcli.Worktrees{}\n", "gitcli.Worktrees"},
		{"worktrees by another name", "package entry\n\nimport g \"ropeshift.com/ropeshift/internal/adapters/gitcli\"\n\nvar _ = g.Worktrees{}\n", "gitcli.Worktrees"},
		{"worktrees as a pointer", "package entry\n\nimport \"ropeshift.com/ropeshift/internal/adapters/gitcli\"\n\nvar _ = &gitcli.Worktrees{}\n", "gitcli.Worktrees"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeTree(t, root, map[string]string{serverFile: tt.text})
			found, err := CheckServerWiring(root)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(found, func(v Violation) bool {
				return v.Rule == "server-wiring" && v.Package == serverFile && strings.Contains(v.Detail, tt.want)
			}) {
				t.Errorf("%s is not refused: %v", tt.name, found)
			}
		})
	}
}

// The check follows the server's wiring into every file it is split over, and leaves the files that wire
// the worker and solo alone.
func TestTheServerWiringCheckReadsEveryServerFile(t *testing.T) {
	root := t.TempDir()
	const tmux = "package entry\n\nimport \"ropeshift.com/ropeshift/internal/adapters/tmux\"\n\nvar _ = tmux.New\n"
	writeTree(t, root, map[string]string{
		serverFile:                       "package entry\n",
		"internal/entry/server_reads.go": tmux,
		"internal/entry/worker.go":       tmux,
		"internal/entry/solo.go":         tmux,
		"internal/entry/server_test.go":  tmux,
	})
	found, err := CheckServerWiring(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Package != "internal/entry/server_reads.go" {
		t.Errorf("reported %v", found)
	}
}

// TestOnlyTheServerImportsPostgres holds what solo mode keeps while group mode is a preview: no package
// but the server's wiring imports the Postgres adapter or its driver, so solo's wiring runs on SQLite alone.
func TestOnlyTheServerImportsPostgres(t *testing.T) {
	root := repoRoot(t)
	found, err := CheckPostgresOnlyInTheServer(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range found {
		t.Error(v)
	}
	if !fileHolds(t, root, serverFile, "internal/adapters/postgres") {
		t.Fatalf("%s does not import the adapter, so the check reads nothing", serverFile)
	}
}

func TestTheOnlyPostgresImporterCheckCanFail(t *testing.T) {
	const adapter = "ropeshift.com/ropeshift/internal/adapters/postgres"
	tests := []struct {
		name string
		file string
		text string
		want string
	}{
		{"a main", "cmd/ropeshift/main.go", "package main\n\nimport _ \"" + adapter + "\"\n", "internal/adapters/postgres"},
		{"solo's wiring", "internal/entry/solo.go", "package entry\n\nimport \"" + adapter + "\"\n\nvar _ = postgres.Open\n", "internal/adapters/postgres"},
		{"another file of the entry", "internal/entry/worker.go", "package entry\n\nimport \"" + adapter + "\"\n\nvar _ = postgres.Open\n", "internal/adapters/postgres"},
		{"another adapter", "internal/adapters/other/other.go", "package other\n\nimport \"" + adapter + "\"\n\nvar _ = postgres.Open\n", "internal/adapters/postgres"},
		{"a package under the adapter", "internal/adapters/other/other.go", "package other\n\nimport \"" + adapter + "/schemas\"\n", "internal/adapters/postgres/schemas"},
		{"the driver", "internal/adapters/other/other.go", "package other\n\nimport \"github.com/jackc/pgx/v5\"\n", "github.com/jackc/pgx/v5"},
		{"the driver's pool", "internal/entry/worker.go", "package entry\n\nimport \"github.com/jackc/pgx/v5/pgxpool\"\n", "github.com/jackc/pgx/v5/pgxpool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeTree(t, root, map[string]string{tt.file: tt.text})
			found, err := CheckPostgresOnlyInTheServer(root)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(found, func(v Violation) bool {
				return v.Rule == "postgres-only-in-the-server" && v.Package == tt.file && strings.HasSuffix(v.Detail, tt.want)
			}) {
				t.Errorf("%s is not refused: %v", tt.name, found)
			}
		})
	}
}

func TestTheOnlyPostgresImporterCheckAllowsItsOwnPlaces(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		serverFile:                                "package entry\n\nimport (\n\t\"ropeshift.com/ropeshift/internal/adapters/postgres\"\n)\n\nvar _ = postgres.Open\n",
		"internal/adapters/postgres/store.go":     "package postgres\n\nimport _ \"github.com/jackc/pgx/v5\"\n",
		"internal/adapters/postgres/sub/other.go": "package sub\n\nimport _ \"github.com/jackc/pgx/v5/pgxpool\"\n",
		"internal/entry/solo_test.go":             "package entry\n\nimport _ \"ropeshift.com/ropeshift/internal/adapters/postgres\"\n",
	})
	found, err := CheckPostgresOnlyInTheServer(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("reported %v", found)
	}
}
