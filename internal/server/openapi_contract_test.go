package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/hkjang/relio/internal/api"
)

// The OpenAPI document at /api/openapi.json is the only contract a REST or MCP
// client has. It drifted: it promised `PUT /opportunities/{id}/playbook` while
// the router only answered `PUT /opportunities/{id}/playbook/{itemId}`, and it
// left the login, dashboard and password endpoints out entirely. Nothing failed,
// because nothing compared the two. These tests do.

const apiPrefix = "/api/v1"

// routedOperations reads the route table out of the package source instead of
// the mux, because http.ServeMux does not report what was registered on it.
func routedOperations(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	out := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
				return true
			}
			if ident, ok := sel.X.(*ast.Ident); !ok || ident.Name != "mux" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			pattern, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			method, path, found := strings.Cut(pattern, " ")
			if !found || !strings.HasPrefix(path, apiPrefix+"/") {
				return true
			}
			out[method+" "+strings.TrimPrefix(path, apiPrefix)] = true
			return true
		})
	}
	if len(out) == 0 {
		t.Fatal("found no registered /api/v1 route; the route scanner is broken")
	}
	return out
}

func documentedOperations(t *testing.T) map[string]bool {
	t.Helper()
	paths, ok := api.OpenAPI()["paths"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI document has no paths object")
	}
	out := map[string]bool{}
	for path, item := range paths {
		operations, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("path %s does not describe operations", path)
		}
		for method := range operations {
			out[strings.ToUpper(method)+" "+path] = true
		}
	}
	return out
}

func missing(from, in map[string]bool) []string {
	var out []string
	for operation := range from {
		if !in[operation] {
			out = append(out, operation)
		}
	}
	sort.Strings(out)
	return out
}

func TestEveryDocumentedOperationIsRouted(t *testing.T) {
	if orphans := missing(documentedOperations(t), routedOperations(t)); len(orphans) > 0 {
		t.Fatalf("OpenAPI documents operations the router does not answer:\n  %s", strings.Join(orphans, "\n  "))
	}
}

func TestEveryRoutedOperationIsDocumented(t *testing.T) {
	if undocumented := missing(routedOperations(t), documentedOperations(t)); len(undocumented) > 0 {
		t.Fatalf("routes are missing from the OpenAPI document:\n  %s", strings.Join(undocumented, "\n  "))
	}
}
