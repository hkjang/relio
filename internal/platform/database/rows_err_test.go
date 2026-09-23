package database

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scannedPackages are the directories this invariant is enforced over. The
// list is deliberately narrow rather than exception-ridden: every loop in
// these packages feeds a caller that can return an error, so a stream failure
// has somewhere to go. internal/intelligence keeps two best-effort lookups
// (contactRoles, stageLimits) whose signatures cannot carry an error, so that
// package is left for a later pass instead of being whitelisted line by line.
var scannedPackages = []string{"server", "crm", "apikey", "auth", "oidc", "audit", "admin", "approval", "voice", "relationship", "analytics", "job", "personal", "mcp"}

// rowsLoop is one `for <rows>.Next()` loop and where it sits.
type rowsLoop struct {
	path string
	line int
	name string
}

// TestEveryRowsLoopChecksRowsErr keeps stream failures from leaving through
// the success path. pgx returns false from rows.Next() when the stream breaks
// just as it does when the rows run out, so a loop that never consults
// rows.Err() hands its caller a truncated list that looks complete — a 200
// with half the audit log in it. The check has to come before the variable is
// reused: a function that scans two result sets into the same `rows` and only
// checks the second one loses the first loop's failure entirely.
func TestEveryRowsLoopChecksRowsErr(t *testing.T) {
	root := filepath.Join("..", "..", "..", "internal")
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("cannot reach the internal tree from this package: %v", err)
	}
	for _, pkg := range scannedPackages {
		dir := filepath.Join(root, pkg)
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("scanned package %s is missing: %v", pkg, err)
		}
	}
	fset := token.NewFileSet()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		if !inScannedPackage(root, path) {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, loop := range uncheckedRowsLoops(file, fset, path) {
			t.Errorf("%s:%d: the %s.Next() loop never checks %s.Err(); a broken stream ends the loop exactly like the last row and the caller returns a partial result as a success", loop.path, loop.line, loop.name, loop.name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func inScannedPackage(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	top := strings.Split(filepath.ToSlash(rel), "/")[0]
	for _, pkg := range scannedPackages {
		if top == pkg {
			return true
		}
	}
	return false
}

// uncheckedRowsLoops reports every `for <name>.Next()` loop in the file whose
// enclosing block does not consult `<name>.Err()` afterwards, while the name
// still holds that result set.
func uncheckedRowsLoops(file *ast.File, fset *token.FileSet, path string) []rowsLoop {
	var out []rowsLoop
	ast.Inspect(file, func(node ast.Node) bool {
		block, ok := node.(*ast.BlockStmt)
		if !ok {
			return true
		}
		for i, stmt := range block.List {
			loop, ok := stmt.(*ast.ForStmt)
			if !ok {
				continue
			}
			name, ok := nextLoopVariable(loop)
			if !ok {
				continue
			}
			if checksErrAfter(block.List[i+1:], name) {
				continue
			}
			out = append(out, rowsLoop{path: path, line: fset.Position(loop.Pos()).Line, name: name})
		}
		return true
	})
	return out
}

// nextLoopVariable returns the receiver of a `for <name>.Next()` loop.
func nextLoopVariable(loop *ast.ForStmt) (string, bool) {
	if loop.Init != nil || loop.Post != nil {
		return "", false
	}
	call, ok := loop.Cond.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return "", false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Next" {
		return "", false
	}
	ident, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	return ident.Name, true
}

// checksErrAfter looks for `<name>.Err()` in the statements that follow the
// loop, stopping as soon as the name is reassigned or driven by another loop:
// past that point an Err() call reports on a different result set.
func checksErrAfter(rest []ast.Stmt, name string) bool {
	for _, stmt := range rest {
		if rebinds(stmt, name) {
			return false
		}
		found := false
		ast.Inspect(stmt, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 0 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Err" {
				return true
			}
			if ident, ok := selector.X.(*ast.Ident); ok && ident.Name == name {
				found = true
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// rebinds reports whether the statement gives the name a new result set.
func rebinds(stmt ast.Stmt, name string) bool {
	assign, ok := stmt.(*ast.AssignStmt)
	if !ok {
		return false
	}
	for _, lhs := range assign.Lhs {
		if ident, ok := lhs.(*ast.Ident); ok && ident.Name == name {
			return true
		}
	}
	return false
}
