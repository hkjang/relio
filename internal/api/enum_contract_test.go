package api

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hkjang/relio/migrations"
)

// A filter value list is written down twice: choice() in parameters.go publishes
// it in the OpenAPI document, and a CHECK (col IN (...)) constraint in
// migrations/ decides which values the database will actually store. Nothing
// tied the two together. TestDocumentedQueryParametersMatchTheHandler compares
// only the query string *keys*, and TestDocumentedSortValuesMatchTheQueryBuilder
// looks at `sort` alone, so editing one side left the build, go vet and every
// contract test green while the document lied in one of two directions: a value
// published but missing from the CHECK makes that filter return nothing or fail
// a write with SQLSTATE 23514, and a value the database accepts but the document
// omits is a valid filter a generated client cannot ask for.
//
// This test compares the two as sets, so ordering is free. It is a net, not a
// bug fix — the six comparable keys all agree today.

// knownEnumQueries is how many enum query parameters the document published, and
// knownCheckConstraints how many CHECK (col IN (...)) constraints migrations/
// held, when this test was written. Both walks silently read nothing if the
// shape they look for changes — the document grows a $ref indirection, or a
// constraint is spelled differently — and a walk that reads nothing agrees with
// everything, so the counts are checked rather than assumed.
const (
	knownEnumQueries      = 26
	knownCheckConstraints = 42
)

// enumParametersNotBackedByACheck are the enum query parameters deliberately
// left out of the comparison, with the reason. An enum parameter that is neither
// listed here nor resolvable to exactly one CHECK constraint fails the test, so
// a new filter cannot slip through unexamined.
var enumParametersNotBackedByACheck = map[string]string{
	"sort":   "a sort key, not a column value; TestDocumentedSortValuesMatchTheQueryBuilder covers it",
	"prompt": "an OIDC request parameter sent on to Keycloak, not a column",
	"status": "eleven tables constrain a `status` column and no two agree, so the column name alone does not say which table an operation filters",
	"severity": "customer_voices allows LOW/NORMAL/HIGH/CRITICAL while signals and risks allow LOW/MEDIUM/HIGH/CRITICAL; " +
		"both are published correctly today but the column name alone does not say which",
	"forecastCategory": "no CHECK constrains forecast_category; the database does not restrict it",
}

// checkConstraint is the value set one CHECK (col IN (...)) constraint allows,
// and where it was read from.
type checkConstraint struct {
	values []string
	file   string
	line   int
}

// column is a CHECK constraint's column qualified by its table, because the
// table is what decides whether a repeated column name is a replacement or a
// second, unrelated column.
type column struct {
	table, name string
}

var (
	checkInConstraint = regexp.MustCompile(`(?is)CHECK\s*\(\s*([a-z_]+)\s+IN\s*\(`)
	tableUnderEdit    = regexp.MustCompile(`(?is)(?:CREATE|ALTER)\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_]+)`)
	quotedValue       = regexp.MustCompile(`'([^']*)'`)
)

// snakeCase converts a camelCase query parameter name to the column name it
// would have, which is how all six comparable keys line up.
func snakeCase(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// enumQueryParameters reads the document api.OpenAPI() actually emits and
// returns every query parameter carrying a schema enum, keyed by operation.
func enumQueryParameters(t *testing.T) (map[string]map[string][]string, int) {
	t.Helper()
	document := OpenAPI()
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatalf("the document has no paths object; got %T", document["paths"])
	}
	out := map[string]map[string][]string{}
	seen := 0
	for path, item := range paths {
		operations, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for method, value := range operations {
			operation, ok := value.(map[string]any)
			if !ok {
				continue
			}
			parameters, ok := operation["parameters"].([]any)
			if !ok {
				continue
			}
			for _, raw := range parameters {
				parameter, ok := raw.(map[string]any)
				if !ok {
					t.Fatalf("%s %s: parameter is %T, not an object", method, path, raw)
				}
				// A $ref entry is a map too, so "not a map" does not filter it
				// out; the shared `fields` parameter is published this way.
				if _, isRef := parameter["$ref"]; isRef {
					continue
				}
				if parameter["in"] != "query" {
					continue
				}
				schema, ok := parameter["schema"].(map[string]any)
				if !ok {
					continue
				}
				raw, hasEnum := schema["enum"]
				if !hasEnum {
					continue
				}
				values, ok := raw.([]string)
				if !ok {
					t.Fatalf("%s %s: enum is %T, not []string; the document walk would read nothing", method, path, raw)
				}
				name, ok := parameter["name"].(string)
				if !ok {
					t.Fatalf("%s %s: parameter has no name", method, path)
				}
				key := strings.ToUpper(method) + " " + path
				if out[key] == nil {
					out[key] = map[string][]string{}
				}
				out[key][name] = values
				seen++
			}
		}
	}
	return out, seen
}

// migrationCheckConstraints walks the embedded migrations in filename order,
// which is the order they run, and resolves each column to the value set in
// force once every file has been applied. A later constraint on the same table
// and column replaces the earlier one, because that is what ALTER TABLE ...
// DROP CONSTRAINT then ADD CONSTRAINT does: 014 widens analytics_providers'
// provider that way and 016 widens customer_voice_events' event_type. The same
// column name on a *different* table is a different column and does not
// replace anything, which is why `status` ends up ambiguous.
func migrationCheckConstraints(t *testing.T) (map[column]checkConstraint, int) {
	t.Helper()
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		t.Fatalf("cannot read the embedded migrations: %v", err)
	}
	names := []string{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	out := map[column]checkConstraint{}
	parsed := 0
	for _, name := range names {
		body, err := fs.ReadFile(migrations.Files, name)
		if err != nil {
			t.Fatalf("cannot read %s: %v", name, err)
		}
		sql := string(body)
		tables := tableUnderEdit.FindAllStringSubmatchIndex(sql, -1)
		for _, match := range checkInConstraint.FindAllStringSubmatchIndex(sql, -1) {
			start, end := match[0], match[1]
			col := sql[match[2]:match[3]]
			// The value list crosses a newline in places (016's event_type), so
			// the whole file is read and the list taken up to the paren that
			// closes `IN (`, not to the end of the line.
			depth, i := 0, end-1
			for ; i < len(sql); i++ {
				if sql[i] == '(' {
					depth++
				} else if sql[i] == ')' {
					if depth--; depth == 0 {
						break
					}
				}
			}
			if i >= len(sql) {
				t.Fatalf("%s: CHECK (%s IN ( near offset %d is never closed", name, col, start)
			}
			values := []string{}
			for _, value := range quotedValue.FindAllStringSubmatch(sql[end:i], -1) {
				values = append(values, value[1])
			}
			table := "?"
			for _, candidate := range tables {
				if candidate[0] < start {
					table = sql[candidate[2]:candidate[3]]
				}
			}
			out[column{table: table, name: col}] = checkConstraint{
				values: values,
				file:   name,
				line:   strings.Count(sql[:start], "\n") + 1,
			}
			parsed++
		}
	}
	return out, parsed
}

// effectiveCheckValues collapses the per-table constraints to one value set per
// column name. A column constrained identically on several tables stays usable;
// one constrained differently is reported ambiguous rather than compared
// against an arbitrary winner.
func effectiveCheckValues(constraints map[column]checkConstraint) (map[string]checkConstraint, map[string][]string) {
	byName := map[string][]checkConstraint{}
	for col, constraint := range constraints {
		byName[col.name] = append(byName[col.name], constraint)
	}
	resolved := map[string]checkConstraint{}
	ambiguous := map[string][]string{}
	for name, found := range byName {
		distinct := map[string]bool{}
		where := []string{}
		for _, constraint := range found {
			distinct[sortedSet(constraint.values)] = true
			where = append(where, fmt.Sprintf("%s:%d", constraint.file, constraint.line))
		}
		if len(distinct) > 1 {
			sort.Strings(where)
			ambiguous[name] = where
			continue
		}
		resolved[name] = found[0]
	}
	return resolved, ambiguous
}

func sortedSet(values []string) string {
	unique := map[string]bool{}
	for _, value := range values {
		unique[value] = true
	}
	out := make([]string, 0, len(unique))
	for value := range unique {
		out = append(out, value)
	}
	sort.Strings(out)
	return "{" + strings.Join(out, ", ") + "}"
}

func TestDocumentedEnumValuesMatchTheMigrations(t *testing.T) {
	documented, seenEnums := enumQueryParameters(t)
	if seenEnums < knownEnumQueries {
		t.Fatalf("read only %d enum query parameters out of the document, want at least %d; the document walk is broken", seenEnums, knownEnumQueries)
	}
	constraints, parsed := migrationCheckConstraints(t)
	if parsed < knownCheckConstraints {
		t.Fatalf("parsed only %d CHECK (col IN (...)) constraints out of migrations/, want at least %d; the migration walk is broken", parsed, knownCheckConstraints)
	}
	resolved, ambiguous := effectiveCheckValues(constraints)

	operations := make([]string, 0, len(documented))
	for operation := range documented {
		operations = append(operations, operation)
	}
	sort.Strings(operations)

	compared := map[string]bool{}
	for _, operation := range operations {
		names := make([]string, 0, len(documented[operation]))
		for name := range documented[operation] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			published := documented[operation][name]
			if _, excluded := enumParametersNotBackedByACheck[name]; excluded {
				continue
			}
			col := snakeCase(name)
			if where, isAmbiguous := ambiguous[col]; isAmbiguous {
				t.Errorf("%s: %s — %s is constrained differently on several tables (%s); compare it against one table or record it in enumParametersNotBackedByACheck with the reason",
					operation, name, col, strings.Join(where, ", "))
				continue
			}
			constraint, found := resolved[col]
			if !found {
				t.Errorf("%s: %s — no CHECK (%s IN (...)) in migrations/ constrains the published values %s; add the constraint or record the parameter in enumParametersNotBackedByACheck with the reason",
					operation, name, col, sortedSet(published))
				continue
			}
			compared[name] = true
			if document, migration := sortedSet(published), sortedSet(constraint.values); document != migration {
				t.Errorf("%s: %s — document %s, migrations %s (%s:%d)",
					operation, name, document, migration, constraint.file, constraint.line)
			}
		}
	}

	// An exclusion that stopped being true is worse than no exclusion: it hides
	// a column the comparison could now check. Only `status` and `severity` are
	// ambiguous today, and no table constrains a `sort`, `prompt` or
	// `forecast_category` column at all.
	for name, reason := range enumParametersNotBackedByACheck {
		col := snakeCase(name)
		if _, isAmbiguous := ambiguous[col]; isAmbiguous {
			continue
		}
		if constraint, found := resolved[col]; found {
			t.Errorf("%s is excluded from the comparison as %q, but exactly one CHECK now constrains %s (%s:%d); drop the exclusion so the values are compared",
				name, reason, col, constraint.file, constraint.line)
		}
	}

	if len(compared) == 0 {
		t.Fatalf("no enum parameter was compared against a CHECK constraint; the two walks are no longer meeting")
	}
}
