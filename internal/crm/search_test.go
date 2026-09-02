package crm

import (
	"os"
	"path/filepath"
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
