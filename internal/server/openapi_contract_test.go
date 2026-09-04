package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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

var documentMethods = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}

// routedOperation is one line of the route table, with the handler the mux sends
// the request to and whether requireAuth wraps it.
type routedOperation struct {
	method, path, handler string
	authenticated         bool
}

// packageFiles parses the non-test sources of a package directory, because
// http.ServeMux does not report what was registered on it and a Go value cannot
// be asked which query keys it reads.
func packageFiles(t *testing.T, dir string) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	out := []*ast.File{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		out = append(out, file)
	}
	return out
}

func stringLit(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	return value, err == nil
}

func routedOperationList(t *testing.T) []routedOperation {
	t.Helper()
	out := []routedOperation{}
	for _, file := range packageFiles(t, ".") {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
				return true
			}
			if ident, ok := sel.X.(*ast.Ident); !ok || ident.Name != "mux" {
				return true
			}
			pattern, ok := stringLit(call.Args[0])
			if !ok {
				return true
			}
			method, path, found := strings.Cut(pattern, " ")
			if !found || !strings.HasPrefix(path, apiPrefix+"/") {
				return true
			}
			route := routedOperation{method: method, path: strings.TrimPrefix(path, apiPrefix)}
			ast.Inspect(call.Args[1], func(inner ast.Node) bool {
				selector, ok := inner.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if receiver, ok := selector.X.(*ast.Ident); !ok || receiver.Name != "s" {
					return true
				}
				if selector.Sel.Name == "requireAuth" {
					route.authenticated = true
					return true
				}
				if route.handler == "" {
					route.handler = selector.Sel.Name
				}
				return true
			})
			out = append(out, route)
			return true
		})
	}
	if len(out) == 0 {
		t.Fatal("found no registered /api/v1 route; the route scanner is broken")
	}
	return out
}

func routedOperations(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, route := range routedOperationList(t) {
		out[route.method+" "+route.path] = true
	}
	return out
}

func documentedOperations(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for path, operations := range documentPaths(t) {
		for method := range operations {
			if documentMethods[method] {
				out[strings.ToUpper(method)+" "+path] = true
			}
		}
	}
	return out
}

func documentPaths(t *testing.T) map[string]map[string]any {
	t.Helper()
	paths, ok := api.OpenAPI()["paths"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI document has no paths object")
	}
	out := map[string]map[string]any{}
	for path, item := range paths {
		operations, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("path %s does not describe operations", path)
		}
		out[path] = operations
	}
	return out
}

// parameterList reads the parameters of one operation, or of the path item when
// method is empty.
func parameterList(t *testing.T, operations map[string]any, method string) []map[string]any {
	t.Helper()
	holder := operations
	if method != "" {
		operation, ok := operations[method].(map[string]any)
		if !ok {
			return nil
		}
		holder = operation
	}
	raw, ok := holder["parameters"]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("parameters is not a list: %#v", raw)
	}
	out := []map[string]any{}
	for _, entry := range list {
		parameter, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("parameter is not an object: %#v", entry)
		}
		out = append(out, parameter)
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

// OpenAPI requires every `{id}` in a path to have a matching path parameter.
// Without one a generated client has nothing to put the id in, so a document
// that only names the template is not usable by the tools that read it.
func TestEveryPathTemplateVariableIsDeclared(t *testing.T) {
	for path, operations := range documentPaths(t) {
		declared := map[string]bool{}
		for _, parameter := range parameterList(t, operations, "") {
			if parameter["in"] != "path" {
				t.Errorf("%s: path item parameter %v is not a path parameter", path, parameter["name"])
				continue
			}
			if parameter["required"] != true {
				t.Errorf("%s: path parameter %v must be required", path, parameter["name"])
			}
			name, _ := parameter["name"].(string)
			declared[name] = true
		}
		templated := map[string]bool{}
		for _, segment := range strings.Split(path, "/") {
			if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
				templated[strings.Trim(segment, "{}")] = true
			}
		}
		for name := range templated {
			if !declared[name] {
				t.Errorf("%s: template variable {%s} has no path parameter", path, name)
			}
		}
		for name := range declared {
			if !templated[name] {
				t.Errorf("%s: path parameter %q is not in the path template", path, name)
			}
		}
	}
}

// queryKeysByHandler maps each *Server method to the query string keys its body
// reads, so the document can be checked against the code that answers it rather
// than against someone's memory of it.
func queryKeysByHandler(t *testing.T) map[string]map[string]bool {
	t.Helper()
	out := map[string]map[string]bool{}
	for _, file := range packageFiles(t, ".") {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil {
				continue
			}
			keys := map[string]bool{}
			aliases := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				assign, ok := n.(*ast.AssignStmt)
				if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 || !isRequestQuery(assign.Rhs[0]) {
					return true
				}
				if ident, ok := assign.Lhs[0].(*ast.Ident); ok {
					aliases[ident.Name] = true
				}
				return true
			})
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				receiver, _ := sel.X.(*ast.Ident)
				switch {
				case sel.Sel.Name == "Get" && len(call.Args) == 1 &&
					(isRequestQuery(sel.X) || (receiver != nil && aliases[receiver.Name])):
					if key, ok := stringLit(call.Args[0]); ok {
						keys[key] = true
					}
				case strings.HasSuffix(sel.Sel.Name, "Query") && len(call.Args) >= 2 &&
					receiver != nil && receiver.Name == "httpx":
					if key, ok := stringLit(call.Args[1]); ok {
						keys[key] = true
					}
				}
				return true
			})
			if len(keys) > 0 {
				out[fn.Name.Name] = keys
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("found no handler reading a query key; the query scanner is broken")
	}
	return out
}

// isRequestQuery reports whether the expression is `<request>.URL.Query()`.
func isRequestQuery(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Query" {
		return false
	}
	inner, ok := sel.X.(*ast.SelectorExpr)
	return ok && inner.Sel.Name == "URL"
}

func documentedQueryParameters(t *testing.T, operation string) map[string]bool {
	t.Helper()
	method, path, _ := strings.Cut(operation, " ")
	operations, ok := documentPaths(t)[path]
	if !ok {
		t.Fatalf("%s is not in the OpenAPI document", operation)
	}
	out := map[string]bool{}
	for _, parameter := range parameterList(t, operations, strings.ToLower(method)) {
		if parameter["$ref"] != nil {
			continue
		}
		if parameter["in"] != "query" {
			t.Errorf("%s: operation parameter %v must be a query parameter", operation, parameter["name"])
			continue
		}
		name, _ := parameter["name"].(string)
		out[name] = true
	}
	return out
}

// A filter nobody wrote down is a filter nobody uses: /customers has taken a
// cursor and a sort since the paging work, and neither appeared in the contract.
func TestDocumentedQueryParametersMatchTheHandler(t *testing.T) {
	handlers := queryKeysByHandler(t)
	for _, route := range routedOperationList(t) {
		operation := route.method + " " + route.path
		documented := documentedQueryParameters(t, operation)
		read := handlers[route.handler]
		for name := range read {
			if !documented[name] {
				t.Errorf("%s: handler %s reads query key %q, which the OpenAPI document does not describe", operation, route.handler, name)
			}
		}
		for name := range documented {
			if !read[name] {
				t.Errorf("%s: OpenAPI documents query parameter %q, which handler %s never reads", operation, name, route.handler)
			}
		}
	}
}

// The projection is applied by requireAuth, not by the handlers, so it belongs
// to every authenticated GET and to no other operation.
func TestProjectionParameterCoversEveryAuthenticatedGet(t *testing.T) {
	paths := documentPaths(t)
	for _, route := range routedOperationList(t) {
		operations, ok := paths[route.path]
		if !ok {
			continue
		}
		declared := false
		for _, parameter := range parameterList(t, operations, strings.ToLower(route.method)) {
			if parameter["$ref"] == "#/components/parameters/fields" {
				declared = true
			}
		}
		want := route.authenticated && route.method == "GET"
		if want && !declared {
			t.Errorf("%s %s: authenticated GET does not document the fields projection", route.method, route.path)
		}
		if !want && declared {
			t.Errorf("%s %s: documents the fields projection, but requireAuth never applies it here", route.method, route.path)
		}
	}
	if _, ok := api.OpenAPI()["components"].(map[string]any)["parameters"].(map[string]any)["fields"]; !ok {
		t.Fatal("components.parameters.fields is missing, so every $ref to it dangles")
	}
}

// acceptedSortValues reads the sort whitelist out of the query builder. A sort
// value the builder does not know is silently replaced by the default ordering,
// so publishing the wrong list is worse than publishing none.
func acceptedSortValues(t *testing.T, function string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, file := range packageFiles(t, "../crm") {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != function || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				assign, ok := n.(*ast.AssignStmt)
				if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
					return true
				}
				if ident, ok := assign.Lhs[0].(*ast.Ident); !ok || ident.Name != "orders" {
					return true
				}
				composite, ok := assign.Rhs[0].(*ast.CompositeLit)
				if !ok {
					return true
				}
				for _, element := range composite.Elts {
					pair, ok := element.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if key, ok := stringLit(pair.Key); ok {
						out[key] = true
					}
				}
				return true
			})
		}
	}
	if len(out) == 0 {
		t.Fatalf("found no sort whitelist in crm.%s", function)
	}
	return out
}

func TestDocumentedSortValuesMatchTheQueryBuilder(t *testing.T) {
	for operation, function := range map[string]string{
		"GET /customers":     "SearchCustomers",
		"GET /opportunities": "ListOpportunities",
	} {
		method, path, _ := strings.Cut(operation, " ")
		var documented []string
		for _, parameter := range parameterList(t, documentPaths(t)[path], strings.ToLower(method)) {
			if parameter["name"] != "sort" {
				continue
			}
			schema, ok := parameter["schema"].(map[string]any)
			if !ok {
				t.Fatalf("%s: sort parameter has no schema", operation)
			}
			values, ok := schema["enum"].([]string)
			if !ok {
				t.Fatalf("%s: sort parameter declares no enum", operation)
			}
			documented = values
		}
		if len(documented) == 0 {
			t.Fatalf("%s: sort is not documented", operation)
		}
		accepted := acceptedSortValues(t, function)
		for _, value := range documented {
			if !accepted[value] {
				t.Errorf("%s: documents sort=%s, which crm.%s ignores", operation, value, function)
			}
		}
		if len(documented) != len(accepted) {
			t.Errorf("%s: crm.%s accepts %d sort values, the document lists %d", operation, function, len(accepted), len(documented))
		}
	}
}
