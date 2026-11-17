package rules

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// The seam between the two databases (ADR-0010, NFR-TEST-2). Neutral SQL runs on SQLite and on Postgres, so
// what only one of them runs lives in that database's adapter, or in a counterpart migration at a
// version the neutral files leave free. These checks hold the seam on the source, so a statement that
// reaches only one database cannot hide in the code both run.

const (
	sqliteAdapter   = "internal/adapters/sqlite"
	postgresAdapter = "internal/adapters/postgres"

	neutralMigrations   = sqliteAdapter + "/migrations"
	sqliteCounterparts  = sqliteAdapter + "/dialect_migrations"
	postgresCounterpart = postgresAdapter + "/migrations"
)

// dialectTokens are the words that tie a statement to one database: SQLite's write lock, clock, and
// full-text search, and Postgres's clock, full-text search, JSON type, schema path, and advisory lock.
var dialectTokens = []string{
	"BEGIN IMMEDIATE", "unixepoch(", "clock_timestamp(", "fts5", "bm25(",
	"tsvector", "tsquery", "jsonb", "search_path", "pg_advisory",
}

// DialectTokens lists what the dialect check refuses outside the two adapters.
func DialectTokens() []string { return slices.Clone(dialectTokens) }

// CheckDialectSQL refuses a dialect token in a string literal of a Go file, or in a .sql file, anywhere but
// the two dialect adapters' own Go files and the counterpart migrations. Test files and this package are
// left out: a test may build the statement it needs, and this package names the tokens. A neutral
// migration is checked although it sits under the SQLite adapter, because both databases run it.
func CheckDialectSQL(root string) ([]Violation, error) {
	var found []Violation
	err := filepath.WalkDir(root, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if file != root && skipsDialectCheck(d.Name(), rel) {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case strings.HasSuffix(d.Name(), ".sql"):
			if underDir(rel, sqliteCounterparts) || underDir(rel, postgresCounterpart) {
				return nil
			}
			raw, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			found = append(found, tokensIn(rel, string(raw))...)
		case isSource(d.Name()):
			if underDir(rel, sqliteAdapter) || underDir(rel, postgresAdapter) {
				return nil
			}
			literals, err := stringLiterals(file)
			if err != nil {
				return err
			}
			found = append(found, tokensIn(rel, strings.Join(literals, "\n"))...)
		}
		return nil
	})
	return found, err
}

// skipsDialectCheck leaves out the directories that hold no product source: hidden ones, dependencies,
// the web build, fixtures, and this package.
func skipsDialectCheck(name, rel string) bool {
	switch name {
	case "testdata", "node_modules", "vendor":
		return true
	}
	return strings.HasPrefix(name, ".") || rel == "web" || rel == "dist" || rel == "test/rules"
}

func underDir(rel, dir string) bool {
	return rel == dir || strings.HasPrefix(rel, dir+"/")
}

// stringLiterals returns the value of every string literal in a Go file.
func stringLiterals(file string) ([]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(parsed, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if value, err := strconv.Unquote(lit.Value); err == nil {
			out = append(out, value)
		}
		return true
	})
	return out, nil
}

// tokensIn reports each dialect token a text holds once, in any case, because SQL is case-insensitive.
func tokensIn(rel, text string) []Violation {
	lower := strings.ToLower(text)
	var found []Violation
	for _, word := range dialectTokens {
		if strings.Contains(lower, strings.ToLower(word)) {
			found = append(found, Violation{"dialect-sql", rel, fmt.Sprintf("holds %q outside a dialect adapter", word)})
		}
	}
	return found
}

// CheckDialectCounterparts holds that each change only one database expresses has its partner: a file in
// SQLite's dialect_migrations has the file of the same name in Postgres's migrations, and the other way
// round, and neither holds a version a neutral migration holds, because the neutral file already covers
// both databases and the runtime would refuse the shared version.
func CheckDialectCounterparts(root string) ([]Violation, error) {
	sqliteOwn, err := migrationFiles(root, sqliteCounterparts)
	if err != nil {
		return nil, err
	}
	postgresOwn, err := migrationFiles(root, postgresCounterpart)
	if err != nil {
		return nil, err
	}
	neutral, err := migrationFiles(root, neutralMigrations)
	if err != nil {
		return nil, err
	}
	versions := map[int64]string{}
	for _, name := range neutral {
		if v, ok := migrationVersion(name); ok {
			versions[v] = name
		}
	}
	var found []Violation
	for _, name := range sqliteOwn {
		if !slices.Contains(postgresOwn, name) {
			found = append(found, Violation{"dialect-counterpart", sqliteCounterparts + "/" + name, "has no counterpart in " + postgresCounterpart})
		}
	}
	for _, name := range postgresOwn {
		if !slices.Contains(sqliteOwn, name) {
			found = append(found, Violation{"dialect-counterpart", postgresCounterpart + "/" + name, "has no counterpart in " + sqliteCounterparts})
		}
	}
	for _, own := range slices.Concat(sqliteOwn, postgresOwn) {
		if v, ok := migrationVersion(own); ok {
			if neutralName, shared := versions[v]; shared {
				found = append(found, Violation{"dialect-counterpart", own, fmt.Sprintf("version %d is also the neutral migration %s", v, neutralName)})
			}
		}
	}
	slices.SortFunc(found, func(a, b Violation) int { return strings.Compare(a.String(), b.String()) })
	return slices.CompactFunc(found, func(a, b Violation) bool { return a == b }), nil
}

// migrationFiles lists the .sql files of a directory, which may not exist.
func migrationFiles(root, dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// migrationVersion reads the number a migration file starts with.
func migrationVersion(name string) (int64, bool) {
	number, _, ok := strings.Cut(path.Base(name), "_")
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseInt(number, 10, 64)
	return v, err == nil
}

// pgxModule is the Postgres driver's module path. Its packages are under it.
const pgxModule = "github.com/jackc/pgx"

// CheckSoloWithoutPostgres holds that no binary reaches the Postgres adapter, a package under it, or the
// pgx driver, through any chain of imports, build tags ignored. Solo mode runs on SQLite alone, and group
// mode is wired in internal/entry by the slice that adds its entry points, which changes this rule on
// purpose. The driver is refused wherever a package that a binary reaches imports it, so a second adapter
// or a shared package cannot bring it in around the adapter.
func CheckSoloWithoutPostgres(module string, pkgs []Package) []Violation {
	imports := map[string][]string{}
	for _, pkg := range pkgs {
		imports[pkg.Rel] = pkg.Imports
	}
	var found []Violation
	for _, pkg := range pkgs {
		if Classify(pkg.Rel) != LayerMain {
			continue
		}
		if chain := importChain(module, imports, pkg.Rel, soloRefuses); chain != nil {
			found = append(found, Violation{"solo-without-postgres", pkg.Rel, "reaches " + strings.Join(chain, " -> ")})
		}
	}
	return found
}

// soloRefuses names what a solo binary may not import: the Postgres adapter and every package under it,
// and every package of the pgx driver.
func soloRefuses(module, imported string) (string, bool) {
	if imported == pgxModule || strings.HasPrefix(imported, pgxModule+"/") {
		return imported, true
	}
	if kind, rel := kindOf(module, imported); kind == importModule && underDir(rel, postgresAdapter) {
		return rel, true
	}
	return "", false
}

// importChain finds one path of imports inside the module from a package to an import that refuses names,
// or nil. The chain ends with the name refuses gives that import.
func importChain(module string, imports map[string][]string, from string, refuses func(module, imported string) (string, bool)) []string {
	seen := map[string]bool{}
	var walk func(rel string, via []string) []string
	walk = func(rel string, via []string) []string {
		if seen[rel] {
			return nil
		}
		seen[rel] = true
		for _, imported := range imports[rel] {
			if name, refused := refuses(module, imported); refused {
				return append(slices.Clone(via), name)
			}
			kind, next := kindOf(module, imported)
			if kind != importModule {
				continue
			}
			if found := walk(next, append(slices.Clone(via), next)); found != nil {
				return found
			}
		}
		return nil
	}
	return walk(from, []string{from})
}
