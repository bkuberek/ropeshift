package rules

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestDialectSQLStaysInTheDialectAdapters holds NFR-TEST-2's seam on the tree as it stands: SQL only one
// database runs lives in that database's adapter and its counterpart migrations.
func TestDialectSQLStaysInTheDialectAdapters(t *testing.T) {
	found, err := CheckDialectSQL(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range found {
		t.Error(v)
	}
}

// TestDialectSQLRefusesEachToken proves the check can fail for every token it lists: a string literal in a
// Go file outside the two adapters, and a neutral migration, each hold the token.
func TestDialectSQLRefusesEachToken(t *testing.T) {
	for _, token := range DialectTokens() {
		t.Run(token, func(t *testing.T) {
			root := t.TempDir()
			writeTree(t, root, map[string]string{
				"internal/control/statement.go":                    "package control\n\nconst statement = \"SELECT 1 /* " + token + " */\"\n",
				"internal/adapters/sqlite/migrations/0001_one.sql": "-- neutral\nSELECT 1; " + strings.ToUpper(token) + "\n",
			})
			found, err := CheckDialectSQL(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range []string{"internal/control/statement.go", "internal/adapters/sqlite/migrations/0001_one.sql"} {
				if !slices.ContainsFunc(found, func(v Violation) bool {
					return v.Rule == "dialect-sql" && v.Package == file && strings.Contains(v.Detail, token)
				}) {
					t.Errorf("%s holds %q and was not refused: %v", file, token, found)
				}
			}
		})
	}
}

// TestDialectSQLAllowsItsOwnPlaces proves the check refuses nothing it should leave: the adapters' own
// statements, counterpart migrations, a comment or an identifier that names a token, and a test file.
func TestDialectSQLAllowsItsOwnPlaces(t *testing.T) {
	found, err := CheckDialectSQL(filepath.Join("testdata", "dialects", "allowed"))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("reported %v", found)
	}
}

func TestDialectMigrationsPairUp(t *testing.T) {
	found, err := CheckDialectCounterparts(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range found {
		t.Error(v)
	}
	one, err := CheckDialectCounterparts(filepath.Join("testdata", "dialects", "allowed"))
	if err != nil || len(one) != 0 {
		t.Errorf("a pair is refused: %v, %v", one, err)
	}
}

func TestDialectMigrationsPairUpFixtures(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		detail  string
	}{
		{"SQLite's file has no counterpart", "counterpart-missing", "0013_search_index.sql"},
		{"Postgres's file has no counterpart", "counterpart-extra", "0020_extra.sql"},
		{"a neutral file holds the version too", "counterpart-neutral", "version 13"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found, err := CheckDialectCounterparts(filepath.Join("testdata", "dialects", tt.fixture))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(found, func(v Violation) bool {
				return v.Rule == "dialect-counterpart" && strings.Contains(v.String(), tt.detail)
			}) {
				t.Errorf("not reported: %v", found)
			}
		})
	}
}

// TestSoloNeverReachesThePostgresAdapter holds the preview gate's first rule: no binary imports the
// Postgres adapter, a package under it, or the pgx driver, so solo mode cannot run on Postgres. The slice
// that wires group mode changes this test on purpose.
func TestSoloNeverReachesThePostgresAdapter(t *testing.T) {
	pkgs, err := LoadPackages(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	mains := 0
	for _, pkg := range pkgs {
		if Classify(pkg.Rel) == LayerMain {
			mains++
		}
	}
	if mains == 0 {
		t.Fatal("no main package found, so the rule checked nothing")
	}
	if !slices.ContainsFunc(pkgs, func(pkg Package) bool { return pkg.Rel == postgresAdapter }) {
		t.Fatalf("%s is not in the tree", postgresAdapter)
	}
	for _, v := range CheckSoloWithoutPostgres(module, pkgs) {
		t.Error(v)
	}
}

func TestSoloNeverReachesThePostgresAdapterFixtures(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		reaches string
	}{
		{"a main imports the adapter", "main-imports-postgres", "internal/adapters/postgres"},
		{"the entry wires the adapter", "entry-imports-postgres", "internal/adapters/postgres"},
		{"the adapter arrives through another adapter", "transitive-postgres", "internal/adapters/postgres"},
		{"the entry wires a package under the adapter", "subpackage-of-postgres", "internal/adapters/postgres/schemas"},
		{"a main imports the driver", "main-imports-pgx", "github.com/jackc/pgx/v5"},
		{"the driver arrives through another adapter", "transitive-pgx", "github.com/jackc/pgx/v5/pgxpool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkgs, err := LoadPackages(filepath.Join("testdata", "solo", tt.fixture))
			if err != nil {
				t.Fatal(err)
			}
			found := CheckSoloWithoutPostgres(module, pkgs)
			if !slices.ContainsFunc(found, func(v Violation) bool {
				return v.Rule == "solo-without-postgres" && strings.HasSuffix(v.Detail, tt.reaches)
			}) {
				t.Errorf("a chain that ends at %s is not reported: %v", tt.reaches, found)
			}
		})
	}
}

// TestSoloMayHoldPostgresCodeNoBinaryReaches holds that the rule follows the imports of a binary, and does
// not refuse a tree for holding the adapter, or code that imports the driver, where no binary reaches it.
func TestSoloMayHoldPostgresCodeNoBinaryReaches(t *testing.T) {
	pkgs, err := LoadPackages(filepath.Join("testdata", "solo", "unreachable-pgx"))
	if err != nil {
		t.Fatal(err)
	}
	if found := CheckSoloWithoutPostgres(module, pkgs); len(found) != 0 {
		t.Errorf("reported: %v", found)
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
