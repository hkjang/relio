package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"testing"
)

// One database error must not become two different answers depending on which
// door the caller came through. REST reads a SQLSTATE in pgErrorVerdict
// (server.go) and MCP reads the same code in sanitizeToolError
// (../mcp/results.go), and until this test the only thing holding the two
// tables together was a pair of comments asking the next editor to change both.
// Editing one sentence, or teaching one door a SQLSTATE the other has never
// heard of, left every test in the tree green.
//
// The two functions are compared through the source rather than by calling
// them: the return contracts differ — status+code+message there, a string here
// — and sanitizeToolError is unexported in another package. Only the switch
// tables are compared. What sits outside them is meant to differ; see
// TestSQLSTATETablesAgree for what is deliberately left alone.

// sqlstateCase is one `case "<SQLSTATE>":` arm and the sentence it returns.
type sqlstateCase struct {
	sentence string
	line     int
}

// TestSQLSTATETablesAgree fails when the two tables stop agreeing — either
// because a code reached one door and not the other, or because a sentence was
// reworded on one side only.
//
// Deliberately not compared: the HTTP status and error code, which exist only
// on the REST side (MCP has no notion of either), and the fallback each
// function returns from outside its switch — an unrecognised code is a 500
// with an empty sentence for REST and a request-id sentence for MCP, and those
// are supposed to differ. Collecting only case values that begin with a digit
// keeps the comparison to SQLSTATEs.
func TestSQLSTATETablesAgree(t *testing.T) {
	rest := sqlstateTable(t, "server.go", "pgErrorVerdict")
	mcp := sqlstateTable(t, "../mcp/results.go", "sanitizeToolError")

	for _, code := range sortedCodes(rest) {
		if _, ok := mcp[code]; !ok {
			t.Errorf("SQLSTATE %s is handled in server.go:%d pgErrorVerdict but nowhere in ../mcp/results.go sanitizeToolError; the same database error would answer one way over REST and fall through to the generic sentence over MCP", code, rest[code].line)
		}
	}
	for _, code := range sortedCodes(mcp) {
		if _, ok := rest[code]; !ok {
			t.Errorf("SQLSTATE %s is handled in ../mcp/results.go:%d sanitizeToolError but nowhere in server.go pgErrorVerdict; the same database error would answer one way over MCP and fall through to the generic 500 over REST", code, mcp[code].line)
		}
	}
	for _, code := range sortedCodes(rest) {
		other, ok := mcp[code]
		if !ok {
			continue
		}
		if other.sentence != rest[code].sentence {
			t.Errorf("SQLSTATE %s is worded differently on each side; change one table and change the other\n  server.go:%d       %q\n  ../mcp/results.go:%d %q", code, rest[code].line, rest[code].sentence, other.line, other.sentence)
		}
	}
}

// sqlstateTable parses the file, finds the named function and returns every
// `case "<digits…>":` arm in its switch statements together with the sentence
// that arm returns. It fails the test rather than returning an empty table when
// the file, the function or the cases cannot be found: a source-scanning test
// that quietly passes once somebody renames the thing it scans for is worse
// than no test, because it still reads as coverage.
func sqlstateTable(t *testing.T, path, function string) map[string]sqlstateCase {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("cannot parse %s from this package: %v", path, err)
	}
	var decl *ast.FuncDecl
	for _, node := range file.Decls {
		fn, ok := node.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == function && fn.Body != nil {
			decl = fn
			break
		}
	}
	if decl == nil {
		t.Fatalf("%s no longer declares %s; the SQLSTATE tables moved or were renamed and this comparison is scanning for something that is not there", path, function)
	}

	table := make(map[string]sqlstateCase)
	ast.Inspect(decl.Body, func(node ast.Node) bool {
		sw, ok := node.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		for _, stmt := range sw.Body.List {
			clause, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			// A default clause has no List and is skipped with it.
			codes := caseCodes(clause)
			if len(codes) == 0 {
				continue
			}
			sentence, ok := returnedSentence(clause)
			if !ok {
				t.Errorf("%s:%d: %s handles SQLSTATE %v but does not return a plain string literal, so the wording cannot be compared with the other door", path, fset.Position(clause.Pos()).Line, function, codes)
				continue
			}
			for _, code := range codes {
				if prior, dup := table[code]; dup {
					t.Errorf("%s:%d: %s handles SQLSTATE %s twice (also at line %d); the second arm is dead", path, fset.Position(clause.Pos()).Line, function, code, prior.line)
					continue
				}
				table[code] = sqlstateCase{sentence: sentence, line: fset.Position(clause.Pos()).Line}
			}
		}
		return true
	})
	// The tables hold ten codes today. A scan that suddenly collects far fewer
	// has stopped reading the switch, not found a smaller table.
	if len(table) < 10 {
		t.Fatalf("only %d SQLSTATE cases were read out of %s %s; the scan is no longer reaching the table", len(table), path, function)
	}
	return table
}

// caseCodes returns the case values that look like a SQLSTATE. Anything that is
// not a string literal beginning with a digit is left out, which is what keeps
// the comparison off arms that are not SQLSTATE lookups.
func caseCodes(clause *ast.CaseClause) []string {
	var out []string
	for _, expr := range clause.List {
		lit, ok := expr.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil || value == "" || value[0] < '0' || value[0] > '9' {
			continue
		}
		out = append(out, value)
	}
	return out
}

// returnedSentence reads the sentence a case arm hands back. REST returns
// (status, code, message) and MCP returns the message alone, so the last result
// of the return is the sentence in both. A result that is not a plain string
// literal — a concatenation, a call — is reported as absent rather than guessed
// at.
func returnedSentence(clause *ast.CaseClause) (string, bool) {
	var last *ast.ReturnStmt
	for _, stmt := range clause.Body {
		if ret, ok := stmt.(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
			last = ret
		}
	}
	if last == nil {
		return "", false
	}
	lit, ok := last.Results[len(last.Results)-1].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

// sortedCodes keeps the failure output in a stable order.
func sortedCodes(table map[string]sqlstateCase) []string {
	out := make([]string, 0, len(table))
	for code := range table {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}
