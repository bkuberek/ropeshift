package rules

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
)

// authorityDir holds the one function that may compare a caller's kind.
const authorityDir = "internal/authority"

// CheckOneAuthorization finds a check made outside the one authorization function: a
// comparison or a switch case on an actor's kind (FR-AUTH-9). Naming a kind to build an
// actor is no check, so a composite literal passes.
func CheckOneAuthorization(root string) ([]Violation, error) {
	var found []Violation
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isSource(d.Name()) {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.ToSlash(rel), authorityDir+"/") {
			return nil
		}
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, line := range kindChecks(file, fileSet) {
			found = append(found, Violation{"one-authorization-function", rel, line})
		}
		return nil
	})
	return found, err
}

func kindChecks(file *ast.File, fileSet *token.FileSet) []string {
	var lines []string
	report := func(at ast.Node) {
		lines = append(lines, fmt.Sprintf("line %d compares an actor's kind", fileSet.Position(at.Pos()).Line))
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.BinaryExpr:
			if (n.Op == token.EQL || n.Op == token.NEQ) && (isActorKind(n.X) || isActorKind(n.Y)) {
				report(n)
			}
		case *ast.CaseClause:
			for _, value := range n.List {
				if isActorKind(value) {
					report(value)
				}
			}
		}
		return true
	})
	return lines
}

// isActorKind reports `ledger.ActorX`.
func isActorKind(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || !strings.HasPrefix(selector.Sel.Name, "Actor") {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "ledger"
}
