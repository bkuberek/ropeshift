package rules

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// CheckPortFakes finds ports with no fake. A port is the exported interface named
// after its file in internal/ports, so doc.go and helper interfaces do not count.
// A fake declares itself with `var _ ports.Name = ...` in internal/adapters/fake,
// which the compiler then proves.
func CheckPortFakes(root string) ([]Violation, error) {
	ports, err := declaredPorts(filepath.Join(root, "internal", "ports"))
	if err != nil {
		return nil, err
	}
	fakes, err := declaredFakes(filepath.Join(root, "internal", "adapters", "fake"))
	if err != nil {
		return nil, err
	}
	var found []Violation
	for _, port := range ports.names {
		if !fakes[port] {
			found = append(found, Violation{"port-has-fake", "internal/ports", port + " has no fake"})
		}
	}
	found = append(found, ports.problems...)
	return found, nil
}

type portScan struct {
	names    []string
	problems []Violation
}

func declaredPorts(dir string) (portScan, error) {
	var scan portScan
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return scan, nil
	}
	if err != nil {
		return scan, err
	}
	for _, entry := range entries {
		if !isSource(entry.Name()) || entry.Name() == "doc.go" {
			continue
		}
		want := portName(strings.TrimSuffix(entry.Name(), ".go"))
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			return scan, err
		}
		if declaresInterface(file, want) {
			scan.names = append(scan.names, want)
			continue
		}
		scan.problems = append(scan.problems, Violation{"port-file", "internal/ports", entry.Name() + " does not declare interface " + want})
	}
	slices.Sort(scan.names)
	return scan, nil
}

// portName turns a file name such as work_source into WorkSource.
func portName(file string) string {
	var b strings.Builder
	for word := range strings.SplitSeq(file, "_") {
		runes := []rune(word)
		if len(runes) == 0 {
			continue
		}
		runes[0] = unicode.ToUpper(runes[0])
		b.WriteString(string(runes))
	}
	return b.String()
}

func declaresInterface(file *ast.File, name string) bool {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts := spec.(*ast.TypeSpec)
			if _, isInterface := ts.Type.(*ast.InterfaceType); isInterface && ts.Name.Name == name {
				return true
			}
		}
	}
	return false
}

func declaredFakes(dir string) (map[string]bool, error) {
	fakes := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return fakes, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !isSource(entry.Name()) {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				if name, ok := assertedPort(spec.(*ast.ValueSpec)); ok {
					fakes[name] = true
				}
			}
		}
	}
	return fakes, nil
}

func assertedPort(spec *ast.ValueSpec) (string, bool) {
	if len(spec.Names) != 1 || spec.Names[0].Name != "_" {
		return "", false
	}
	sel, ok := spec.Type.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "ports" {
		return "", false
	}
	return sel.Sel.Name, true
}
