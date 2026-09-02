package intelligence

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/hkjang/relio/internal/auth"
)

func principal() *auth.Principal {
	return &auth.Principal{UserID: "u-1", Username: "sales", DataScope: "TEAM", OrganizationID: "org-1"}
}

type builtQuery struct {
	name string
	sql  string
	args []any
}

// byID builds each list statement the way the Get* readers do: one named record
// out of the scoped query.
func byID(id string) []builtQuery {
	out := []builtQuery{}
	sql, args := signalQuery(principal(), SignalFilter{ID: id, Status: "ALL", Limit: 1})
	out = append(out, builtQuery{"signal g", sql, args})
	sql, args = riskQuery(principal(), RiskFilter{ID: id, Status: "ALL", Limit: 1})
	out = append(out, builtQuery{"risk r", sql, args})
	sql, args = insightQuery(principal(), InsightFilter{ID: id, Status: "ALL", Limit: 1})
	out = append(out, builtQuery{"insight i", sql, args})
	sql, args = recommendationQuery(principal(), RecommendationFilter{ID: id, Status: "ALL", Limit: 1})
	out = append(out, builtQuery{"recommendation n", sql, args})
	return out
}

func listQueries() []builtQuery {
	out := []builtQuery{}
	sql, args := signalQuery(principal(), SignalFilter{AccountID: "a-1"})
	out = append(out, builtQuery{"signal g", sql, args})
	sql, args = riskQuery(principal(), RiskFilter{AccountID: "a-1"})
	out = append(out, builtQuery{"risk r", sql, args})
	sql, args = insightQuery(principal(), InsightFilter{AccountID: "a-1"})
	out = append(out, builtQuery{"insight i", sql, args})
	sql, args = recommendationQuery(principal(), RecommendationFilter{AccountID: "a-1"})
	out = append(out, builtQuery{"recommendation n", sql, args})
	return out
}

// alias is the table alias the query uses, taken from the case name.
func (q builtQuery) alias() string { return q.name[strings.LastIndex(q.name, " ")+1:] }

// Reading one record must not mean listing a capped page and searching it in Go.
// Past the cap the record could not be opened at all — and because every write
// (ignore, accept, dismiss) reads the record first, the write failed with
// "not found" too.
func TestSingleRecordQueriesFilterByIDInSQL(t *testing.T) {
	for _, q := range byID("x-1") {
		t.Run(q.name, func(t *testing.T) {
			predicate := q.alias() + ".id::text=$"
			index := strings.Index(q.sql, predicate)
			if index < 0 {
				t.Fatalf("query has no id predicate %q:\n%s", predicate, q.sql)
			}
			n := placeholderAt(t, q.sql, index+len(predicate)-1)
			if n > len(q.args) {
				t.Fatalf("id predicate uses $%d but only %d arguments are passed", n, len(q.args))
			}
			if got := q.args[n-1]; got != "x-1" {
				t.Fatalf("$%d = %v, want the requested id", n, got)
			}
			// The id must stay optional so the same statement still serves lists.
			if want := fmt.Sprintf("($%d='' OR %s%d)", n, predicate, n); !strings.Contains(q.sql, want) {
				t.Fatalf("id predicate must be optional (%s):\n%s", want, q.sql)
			}
		})
	}
}

// A blank id means "no filter", exactly like every other optional predicate, so
// list callers that never set one keep listing everything in scope.
func TestBlankIDLeavesListsUnfiltered(t *testing.T) {
	for _, q := range listQueries() {
		index := strings.Index(q.sql, q.alias()+".id::text=$")
		n := placeholderAt(t, q.sql, index+len(q.alias()+".id::text=$")-1)
		if got := q.args[n-1]; got != "" {
			t.Fatalf("%s: unset id = %v, want the empty no-filter value", q.name, got)
		}
	}
	for _, q := range byID("   ") {
		index := strings.Index(q.sql, q.alias()+".id::text=$")
		n := placeholderAt(t, q.sql, index+len(q.alias()+".id::text=$")-1)
		if got := q.args[n-1]; got != "" {
			t.Fatalf("%s: whitespace id = %q, want it trimmed to the no-filter value", q.name, got)
		}
	}
}

// Every placeholder the statement names must have an argument behind it and no
// argument may go unused: pgx rejects both, at runtime, in production.
func TestQueryPlaceholdersMatchArguments(t *testing.T) {
	for _, q := range append(byID("x-1"), listQueries()...) {
		used := map[int]bool{}
		highest := 0
		for _, match := range placeholderPattern.FindAllStringSubmatch(q.sql, -1) {
			n, err := strconv.Atoi(match[1])
			if err != nil {
				t.Fatalf("%s: %v", q.name, err)
			}
			used[n] = true
			if n > highest {
				highest = n
			}
		}
		if highest != len(q.args) {
			t.Fatalf("%s query names $1..$%d but passes %d arguments", q.name, highest, len(q.args))
		}
		for n := 1; n <= highest; n++ {
			if !used[n] {
				t.Fatalf("%s query skips $%d", q.name, n)
			}
		}
	}
}

var placeholderPattern = regexp.MustCompile(`\$(\d+)`)

// placeholderAt reads the $N that starts at index.
func placeholderAt(t *testing.T, sql string, index int) int {
	t.Helper()
	if index < 0 || index >= len(sql) || sql[index] != '$' {
		t.Fatalf("expected a placeholder at %d", index)
	}
	end := index + 1
	for end < len(sql) && sql[end] >= '0' && sql[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(sql[index+1 : end])
	if err != nil {
		t.Fatalf("unreadable placeholder %q: %v", sql[index:end], err)
	}
	return n
}
