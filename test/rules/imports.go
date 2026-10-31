package rules

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Package is one Go package and what its non-test files import. Build tags are
// ignored on purpose: a chaos-only file in a domain package must obey the rules
// too, and test files may import fakes.
type Package struct {
	Rel     string
	Imports []string
}

// Violation is one broken rule.
type Violation struct {
	Rule    string
	Package string
	Detail  string
}

func (v Violation) String() string {
	return v.Rule + ": " + v.Package + ": " + v.Detail
}

// LoadPackages reads the imports of every package under cmd and internal.
func LoadPackages(root string) ([]Package, error) {
	byRel := map[string]*Package{}
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
			rel, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			pkg := byRel[rel]
			if pkg == nil {
				pkg = &Package{Rel: rel}
				byRel[rel] = pkg
			}
			imports, err := fileImports(path)
			if err != nil {
				return err
			}
			pkg.Imports = append(pkg.Imports, imports...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	pkgs := make([]Package, 0, len(byRel))
	for _, pkg := range byRel {
		slices.Sort(pkg.Imports)
		pkg.Imports = slices.Compact(pkg.Imports)
		pkgs = append(pkgs, *pkg)
	}
	slices.SortFunc(pkgs, func(a, b Package) int { return strings.Compare(a.Rel, b.Rel) })
	return pkgs, nil
}

// FindSource lists where any of the phrases appear in the non-test Go source under
// the named directories.
func FindSource(root string, dirs, phrases []string) ([]string, error) {
	var found []string
	for _, top := range dirs {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !isSource(d.Name()) {
				return err
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, phrase := range phrases {
				if strings.Contains(string(raw), phrase) {
					rel, err := filepath.Rel(root, path)
					if err != nil {
						return err
					}
					found = append(found, rel+" holds "+phrase)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return found, nil
}

func isSource(name string) bool {
	return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

func fileImports(path string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		imports = append(imports, value)
	}
	return imports, nil
}

type importKind int

const (
	importStandard importKind = iota
	importThirdParty
	importModule
)

// kindOf sorts an import. A standard library path has no dot in its first element.
func kindOf(module, path string) (importKind, string) {
	if path == module {
		return importModule, ""
	}
	if rel, ok := strings.CutPrefix(path, module+"/"); ok {
		return importModule, rel
	}
	first, _, _ := strings.Cut(path, "/")
	if strings.Contains(first, ".") {
		return importThirdParty, ""
	}
	return importStandard, ""
}

// CheckDependencies applies the module layout's rules. It returns every
// violation, so a fixture can name the rule it breaks and a real failure shows
// the whole damage at once.
func CheckDependencies(module string, pkgs []Package) []Violation {
	var found []Violation
	for _, pkg := range pkgs {
		layer := Classify(pkg.Rel)
		if layer == LayerUnclassified {
			found = append(found, Violation{"unclassified", pkg.Rel, "no layer in the module layout"})
		}
		for _, path := range pkg.Imports {
			found = append(found, checkImport(module, pkg.Rel, layer, path)...)
		}
	}
	found = append(found, checkHookClosure(module, pkgs)...)
	found = append(found, checkClientClosure(module, pkgs)...)
	slices.SortFunc(found, func(a, b Violation) int { return strings.Compare(a.String(), b.String()) })
	return found
}

func checkImport(module, rel string, layer Layer, path string) []Violation {
	kind, target := kindOf(module, path)
	to := LayerUnclassified
	if kind == importModule {
		to = Classify(target)
	}
	fail := func(rule string) []Violation {
		return []Violation{{rule, rel, "imports " + path}}
	}
	switch layer {
	case LayerMain:
		if kind == importModule && to != LayerEntry {
			return fail("thin-mains")
		}
		if kind == importThirdParty {
			return fail("thin-mains")
		}
	case LayerDomain:
		switch {
		case kind == importStandard && !pureStandardLibrary[path]:
			return fail("domain-pure")
		case kind == importThirdParty:
			return fail("domain-pure")
		case kind == importModule && to != LayerDomain:
			return fail("domain-pure")
		}
	case LayerPorts:
		switch {
		case kind == importStandard && !pureStandardLibrary[path]:
			return fail("ports-domain-only")
		case kind == importThirdParty:
			return fail("ports-domain-only")
		case kind == importModule && to != LayerDomain:
			return fail("ports-domain-only")
		}
	case LayerApplication:
		return checkApplicationImport(rel, kind, to, target, fail)
	case LayerAdapter:
		if kind == importModule && !adapterMayImport(to) {
			return fail("adapters-outward")
		}
	case LayerClient:
		if kind == importModule && behindTheAPI(to) {
			return fail("clients-through-api")
		}
	case LayerAPI:
		if kind == importModule && to == LayerAdapter {
			return fail("only-entry-wires")
		}
		if kind == importModule && behindTheAPI(to) {
			return fail("api-client-light")
		}
	case LayerAPIServer, LayerSupport:
		if kind == importModule && to == LayerAdapter {
			return fail("only-entry-wires")
		}
		if layer == LayerSupport && kind != importStandard {
			return fail("support-leaf")
		}
	}
	return nil
}

// adapterMayImport allows the API's wire types and client, because the worker's
// HTTP link speaks the same API as every other client (FR-API-3).
func adapterMayImport(to Layer) bool {
	return to == LayerDomain || to == LayerPorts || to == LayerAdapter || to == LayerSupport || to == LayerAPI
}

// behindTheAPI names what a client must not reach (FR-UI-4). The API's handlers
// are on this side, because they hold the application, and the wire types and
// client are not.
func behindTheAPI(layer Layer) bool {
	return layer == LayerPorts || layer == LayerApplication || layer == LayerAdapter || layer == LayerAPIServer
}

func checkApplicationImport(rel string, kind importKind, to Layer, target string, fail func(string) []Violation) []Violation {
	if kind == importThirdParty {
		return fail("application-inward")
	}
	if kind != importModule {
		return nil
	}
	switch to {
	case LayerDomain, LayerPorts, LayerSupport:
		return nil
	case LayerAdapter:
		return fail("only-entry-wires")
	case LayerApplication:
		if planeOf(rel) != planeOf(target) {
			return fail("planes-apart")
		}
		return nil
	}
	return fail("application-inward")
}

func planeOf(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) < 2 {
		return rel
	}
	return parts[1]
}

// checkHookClosure follows imports from the hook package. The hook runs inside
// the NFR-PERF-2 budget, so an adapter must not reach it through any path.
func checkHookClosure(module string, pkgs []Package) []Violation {
	return checkClosure(module, pkgs, "internal/hook", "hook-no-adapter", func(l Layer) bool { return l == LayerAdapter })
}

// checkClientClosure follows imports from every client, because a client that
// reaches the Store through another package still bypasses the API (FR-UI-4).
func checkClientClosure(module string, pkgs []Package) []Violation {
	var found []Violation
	for _, pkg := range pkgs {
		if Classify(pkg.Rel) == LayerClient {
			found = append(found, checkClosure(module, pkgs, pkg.Rel, "clients-through-api", behindTheAPI)...)
		}
	}
	return found
}

func checkClosure(module string, pkgs []Package, start, rule string, forbidden func(Layer) bool) []Violation {
	imports := map[string][]string{}
	for _, pkg := range pkgs {
		imports[pkg.Rel] = pkg.Imports
	}
	var found []Violation
	seen := map[string]bool{}
	var walk func(rel string, via []string)
	walk = func(rel string, via []string) {
		if seen[rel] {
			return
		}
		seen[rel] = true
		for _, path := range imports[rel] {
			kind, target := kindOf(module, path)
			if kind != importModule {
				continue
			}
			chain := append(slices.Clone(via), target)
			if forbidden(Classify(target)) {
				found = append(found, Violation{rule, start, "reaches " + strings.Join(chain, " -> ")})
				continue
			}
			walk(target, chain)
		}
	}
	walk(start, nil)
	return found
}
