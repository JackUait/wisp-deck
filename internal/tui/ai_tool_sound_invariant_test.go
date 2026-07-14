package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Sound preferences are per AI tool. Every production selection change must
// pass through one setter so a new navigation path cannot edit the old tool.
func TestSelectedAIToolChangesUseSoundAwareSetter(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	assignments := 0
	hasSetter := false
	files := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(files, entry.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.FuncDecl:
				if value.Name.Name == "setSelectedAI" {
					hasSetter = true
				}
			case *ast.AssignStmt:
				for _, target := range value.Lhs {
					if isSelectedAITarget(target) {
						assignments++
					}
				}
			case *ast.IncDecStmt:
				if isSelectedAITarget(value.X) {
					assignments++
				}
			}
			return true
		})
	}
	if !hasSetter {
		t.Fatal("production AI selection needs one sound-aware setter")
	}
	if assignments != 1 {
		t.Fatalf("selectedAI mutations = %d, want only the assignment inside setSelectedAI", assignments)
	}
}

func isSelectedAITarget(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "selectedAI"
}
