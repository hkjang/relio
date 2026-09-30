package crm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestSearchPatternKeepsWildcardsLiteral(t *testing.T) {
	cases := map[string]string{
		"50%":        `%50\%%`,
		"AB_C":       `%ab\_c%`,
		"%":          `%\%%`,
		`back\slash`: `%back\\slash%`,
		"  대성 ":      "%대성%",
		"Acme":       "%acme%",
	}
	for in, want := range cases {
		if got := searchPattern(in); got != want {
			t.Fatalf("searchPattern(%q) = %q, want %q", in, got, want)
		}
	}
}

// A blank query means "no filter", which every search expresses as the empty
// guard in front of the LIKE. Returning "%%" instead would silently turn that
// guard off and make the filter a no-op the query planner still has to run.
func TestSearchPatternLeavesABlankQueryEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\n"} {
		if got := searchPattern(in); got != "" {
			t.Fatalf("searchPattern(%q) = %q, want an empty pattern", in, got)
		}
	}
	if SearchPattern(" %x ") != searchPattern("%x") {
		t.Fatal("SearchPattern must apply the same rule as searchPattern")
	}
}

// The escaping only holds while every free-text search declares the escape
// character, so a query that interpolates its own wildcards is a regression even
// when it lives in another package.
func TestEveryFreeTextSearchDeclaresItsEscapeCharacter(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(body), "\n") {
			for _, operator := range []string{"LIKE '", "LIKE $", "ILIKE '", "ILIKE $"} {
				if strings.Contains(line, operator) && !strings.Contains(line, "ESCAPE") {
					t.Errorf("%s:%d uses %s without ESCAPE; build the pattern with crm.SearchPattern", path, i+1, strings.TrimSpace(operator))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// searchPattern lowercases before it escapes, so every pattern it hands down is
// already folded. A comparison fed one of those patterns therefore only matches
// when the other side is folded too — the left operand wrapped in lower(), or
// the operator ILIKE, which ignores case by itself. Nothing enforced that
// pairing: dropping a lower() or writing a new search as `name LIKE $1 ESCAPE
// '\'` compiles, passes every test, and turns a search for "Acme" into %acme%
// against unfolded text. The failure is not an error but an empty list, which
// reads on screen as "no records".
//
// This is a source invariant, not a runtime proof. It says the code is written
// this way; whether PostgreSQL then matches the rows is a question only a real
// database answers, and no test here does that.

// likeComparison matches the operator of a free-text comparison. Requiring a
// placeholder or a quoted literal after it keeps prose containing the word LIKE
// out. The placeholder is only anchored on its $ because knowledge.go writes
// its number as a verb: `%s LIKE $%d ESCAPE '\'`.
var likeComparison = regexp.MustCompile(`\bI?LIKE\s+[$']`)

// TestEveryFreeTextSearchComparesAgainstFoldedText reads every LIKE comparison
// in a non-test file under internal/ and fails the ones whose left operand
// cannot match a lowercased pattern.
//
// String literals are collected through the AST rather than by reading lines,
// because internal/voice/knowledge.go builds its comparison with
// fmt.Sprintf("%s LIKE $%d ESCAPE '\\'", col, n) — the line holding the
// operator has no left operand on it at all. For those the verb is followed
// back to the argument that fills it, so the folding of the column list is
// checked where it is actually written.
func TestEveryFreeTextSearchComparesAgainstFoldedText(t *testing.T) {
	const knownComparisons = 19 // 13 lower(...) LIKE + 5 ILIKE + 1 built with fmt.Sprintf

	seen := 0
	root := filepath.Join("..", "..", "internal")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		var stack []ast.Node
		ast.Inspect(file, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return false
			}
			stack = append(stack, n)
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			// lit.Value is the source text, quotes included, so an offset
			// into it is an offset into the file.
			for _, at := range likeComparison.FindAllStringIndex(lit.Value, -1) {
				seen++
				line := fset.Position(lit.Pos()).Line + strings.Count(lit.Value[:at[0]], "\n")
				if strings.HasPrefix(lit.Value[at[0]:], "ILIKE") {
					continue
				}
				left, start := leftOperand(lit.Value[:at[0]])
				switch {
				case strings.HasPrefix(left, "lower("):
				case strings.HasPrefix(left, "%"):
					checkFormattedOperand(t, path, line, lit, stack, verbsBefore(lit.Value[:start]))
				default:
					t.Errorf("%s:%d compares %s with LIKE against a pattern crm.searchPattern already lowercased; wrap the left side in lower() or use ILIKE, or the search silently returns nothing for anything typed in capitals", path, line, left)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen < knownComparisons {
		t.Fatalf("only %d LIKE comparisons were read out of %s, want at least %d; the scan is no longer reaching the search queries", seen, root, knownComparisons)
	}
}

// leftOperand returns the expression a LIKE was applied to, given the source
// text in front of the operator, plus the offset it starts at. A trailing
// call's arguments are stepped over so COALESCE(actor_name,”) comes back whole
// rather than as its last argument.
func leftOperand(before string) (string, int) {
	end := len(strings.TrimRight(before, " \t\r\n"))
	i := end
	if i > 0 && before[i-1] == ')' {
		for depth := 0; i > 0; {
			i--
			switch before[i] {
			case ')':
				depth++
			case '(':
				depth--
			}
			if depth == 0 {
				break
			}
		}
	}
	for i > 0 && isOperandByte(before[i-1]) {
		i--
	}
	if i > 0 && before[i-1] == '%' { // a format verb, not a column
		i--
	}
	return before[i:end], i
}

func isOperandByte(c byte) bool {
	return c == '_' || c == '.' || c == '"' || '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// verbsBefore counts the fmt verbs ahead of an offset, so a left operand
// written as a verb can be matched to the argument that fills it.
func verbsBefore(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		if i+1 < len(s) && s[i+1] == '%' {
			i++
			continue
		}
		n++
	}
	return n
}

// checkFormattedOperand follows a %-verb left operand back to the argument that
// fills it and fails unless every value that argument can hold is folded. An
// argument it cannot follow is a failure too: an unreadable operand is not a
// checked one.
func checkFormattedOperand(t *testing.T, path string, line int, lit *ast.BasicLit, stack []ast.Node, verb int) {
	t.Helper()
	call, fn := enclosing(stack)
	if call == nil || fn == nil || len(call.Args) == 0 || call.Args[0] != lit || verb+1 >= len(call.Args) {
		t.Errorf("%s:%d builds a LIKE comparison from a format verb this test cannot follow to its argument; fold the column in the literal or extend this check", path, line)
		return
	}
	arg, ok := call.Args[verb+1].(*ast.Ident)
	if !ok {
		t.Errorf("%s:%d fills the left side of a LIKE comparison with an expression this test cannot follow; fold the column in the literal or extend this check", path, line)
		return
	}
	columns, ok := operandValues(fn, arg.Name)
	if !ok {
		t.Errorf("%s:%d fills the left side of a LIKE comparison from %s, whose values this test cannot read; fold the column in the literal or extend this check", path, line, arg.Name)
		return
	}
	for _, col := range columns {
		if !strings.HasPrefix(col, "lower(") {
			t.Errorf("%s:%d compares %s with LIKE against a pattern crm.searchPattern already lowercased; wrap the left side in lower() or use ILIKE, or the search silently returns nothing for anything typed in capitals", path, line, col)
		}
	}
}

// enclosing picks the innermost call and the function declaration a literal
// sits in, out of the ancestors collected on the way down.
func enclosing(stack []ast.Node) (*ast.CallExpr, *ast.FuncDecl) {
	var call *ast.CallExpr
	var fn *ast.FuncDecl
	for i := len(stack) - 1; i >= 0; i-- {
		switch node := stack[i].(type) {
		case *ast.CallExpr:
			if call == nil {
				call = node
			}
		case *ast.FuncDecl:
			fn = node
		}
	}
	return call, fn
}

// operandValues reads the string literals a name can hold inside one function:
// either it is assigned one, or it is the key of a range over a composite
// literal whose keys are written out.
func operandValues(fn *ast.FuncDecl, name string) ([]string, bool) {
	if values, ok := literalsAssignedTo(fn, name); ok {
		return values, true
	}
	collection := ""
	ast.Inspect(fn, func(n ast.Node) bool {
		rng, ok := n.(*ast.RangeStmt)
		if !ok || collection != "" {
			return collection == ""
		}
		if key, ok := rng.Key.(*ast.Ident); !ok || key.Name != name {
			return true
		}
		if over, ok := rng.X.(*ast.Ident); ok {
			collection = over.Name
		}
		return true
	})
	if collection == "" {
		return nil, false
	}
	return literalsAssignedTo(fn, collection)
}

func literalsAssignedTo(fn *ast.FuncDecl, name string) ([]string, bool) {
	var values []string
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		if id, ok := assign.Lhs[0].(*ast.Ident); !ok || id.Name != name {
			return true
		}
		switch rhs := assign.Rhs[0].(type) {
		case *ast.BasicLit:
			if value, ok := stringLiteral(rhs); ok {
				values, found = append(values, value), true
			}
		case *ast.CompositeLit:
			for _, element := range rhs.Elts {
				candidate := element
				if kv, ok := element.(*ast.KeyValueExpr); ok {
					candidate = kv.Key
				}
				lit, ok := candidate.(*ast.BasicLit)
				if !ok {
					return true
				}
				if value, ok := stringLiteral(lit); ok {
					values, found = append(values, value), true
				}
			}
		}
		return true
	})
	return values, found && len(values) > 0
}

func stringLiteral(lit *ast.BasicLit) (string, bool) {
	if lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	return value, err == nil
}
