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

// Class-level "never again" guard for the worktree-delete UI freeze.
//
// models.RemoveWorktree shells out to `git worktree remove`, which rm -rf's the
// entire worktree tree (node_modules, build output, .git churn) and can take many
// seconds on a real project. Bubbletea's Update loop repaints nothing and accepts
// no input while a call it makes is still running, so invoking RemoveWorktree
// synchronously anywhere reachable from Update freezes the whole interface — the
// original bug. The safe pattern is to run it inside a returned tea.Cmd, i.e. a
// `func() tea.Msg { ... }` literal, which Bubbletea executes off the event-loop
// goroutine.
//
// The behavioral guard (TestDeleteMode_ConfirmDelete_DoesNotBlockEventLoop, in
// test/internal/tui) proves the *exercised* delete path stays responsive. This
// test proves the *property* directly against the source: every RemoveWorktree
// call in the TUI package must sit inside a func-literal command body. It catches
// a reintroduction even through a brand-new code path the behavioral test never
// exercises — mirroring how launch_critical_path_test.go guards the launch path.
func TestRemoveWorktree_OnlyCalledInsideTeaCmd(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, src := range files {
		if strings.HasSuffix(src, "_test.go") {
			continue
		}
		checked++
		assertRemoveWorktreeIsAsync(t, src)
	}
	if checked == 0 {
		t.Fatal("no non-test source files scanned; guard is not actually running")
	}
}

func assertRemoveWorktreeIsAsync(t *testing.T, src string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	// Cheap pre-check: skip files that never mention it (keeps the walk focused
	// and makes "checked but clean" the common, fast case).
	if !strings.Contains(string(data), "RemoveWorktree") {
		return
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, data, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", src, err)
	}

	// Map every node to its parent so we can walk upward from a call site to the
	// nearest enclosing function.
	parent := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		if len(stack) > 0 {
			parent[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		return true
	})

	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "RemoveWorktree" {
			return true
		}

		// Walk up to the nearest enclosing function (FuncLit or FuncDecl).
		var enclosing ast.Node
		for p := parent[n]; p != nil; p = parent[p] {
			switch p.(type) {
			case *ast.FuncLit, *ast.FuncDecl:
				enclosing = p
			}
			if enclosing != nil {
				break
			}
		}

		pos := fset.Position(call.Pos())
		lit, ok := enclosing.(*ast.FuncLit)
		if !ok {
			t.Errorf("%s: RemoveWorktree called synchronously in a method body; "+
				"git worktree remove blocks for seconds and MUST run inside a returned "+
				"`func() tea.Msg` (tea.Cmd), never on the Update goroutine", pos)
			return true
		}
		if !funcLitReturnsTeaMsg(lit.Type) {
			t.Errorf("%s: RemoveWorktree is inside a func literal that does not return tea.Msg; "+
				"it must run inside a tea.Cmd body so Bubbletea runs it off the event loop", pos)
		}
		return true
	})
}

// funcLitReturnsTeaMsg reports whether ft is `func(...) tea.Msg` — the signature of
// a tea.Cmd body. That single tea.Msg return is what marks the closure as work
// Bubbletea runs on its own goroutine.
func funcLitReturnsTeaMsg(ft *ast.FuncType) bool {
	if ft.Results == nil || len(ft.Results.List) != 1 {
		return false
	}
	sel, ok := ft.Results.List[0].Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == "Msg"
}
