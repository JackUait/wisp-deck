package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Shield must run before anything reads os.Stderr, and before a helper's
// first libdispatch use arms the memory-pressure source that prints into
// the agent pane.
func TestMainShieldsNativeStderrFirst(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" {
			continue
		}
		first, ok := fn.Body.List[0].(*ast.ExprStmt)
		if !ok {
			t.Fatal("main must start with nativestderr.Shield()")
		}
		call, ok := first.X.(*ast.CallExpr)
		if !ok {
			t.Fatal("main must start with nativestderr.Shield()")
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			t.Fatal("main must start with nativestderr.Shield()")
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "nativestderr" || sel.Sel.Name != "Shield" {
			t.Fatal("main must start with nativestderr.Shield()")
		}
		return
	}
	t.Fatal("main not found")
}
