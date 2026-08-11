package personal

import (
	"strings"
	"testing"

	"github.com/hkjang/relio/internal/auth"
)

func TestNormalizeResourceRejectsAnythingUnmapped(t *testing.T) {
	// A saved view may target a list screen; a favorite may not target ACTIVITY
	// because there is no single activity detail route to return to.
	if _, err := normalizeResource("customer", viewResources); err != nil {
		t.Fatalf("lowercase input must be accepted: %v", err)
	}
	if got, _ := normalizeResource(" voice ", viewResources); got != "VOICE" {
		t.Fatalf("expected VOICE, got %q", got)
	}
	if _, err := normalizeResource("ACTIVITY", viewResources); err != nil {
		t.Fatal("ACTIVITY is a valid saved-view resource")
	}
	if _, err := normalizeResource("ACTIVITY", favoriteResources); err == nil {
		t.Fatal("ACTIVITY must not be favoritable: it has no detail route")
	}
	for _, bad := range []string{"", "USERS", "customers; DROP TABLE", "CUSTOMER "} {
		if _, err := normalizeResource(strings.TrimSpace(bad), viewResources); bad != "CUSTOMER " && err == nil {
			t.Fatalf("%q must be rejected", bad)
		}
	}
}

func TestEveryFavoriteResourceHasASource(t *testing.T) {
	// A favorite is resolved by interpolating its source table into a scoped
	// query, so a resource without an entry would build invalid SQL.
	for resource := range favoriteResources {
		source, ok := favoriteSources[resource]
		if !ok {
			t.Fatalf("%s is favoritable but has no source mapping", resource)
		}
		if source.table == "" || source.title == "" || source.route == "" {
			t.Fatalf("%s source mapping is incomplete: %+v", resource, source)
		}
	}
	// Nothing may be listed as a source that is not an allowed resource, since
	// the table name reaches the query unquoted.
	for resource := range favoriteSources {
		if !favoriteResources[resource] {
			t.Fatalf("%s has a source mapping but is not an allowed favorite", resource)
		}
	}
}

func TestFavoriteSourceTablesAreIdentifiersOnly(t *testing.T) {
	// These values are concatenated into SQL, so they must never carry anything
	// that could terminate the statement.
	for resource, source := range favoriteSources {
		for _, fragment := range []string{source.table, source.title, source.subtitle} {
			if strings.ContainsAny(fragment, ";\"") || strings.Contains(fragment, "--") {
				t.Fatalf("%s fragment %q is not safe to interpolate", resource, fragment)
			}
		}
		for _, char := range source.table {
			if !(char >= 'a' && char <= 'z' || char == '_') {
				t.Fatalf("%s table %q must be a bare identifier", resource, source.table)
			}
		}
	}
}

func TestOrgArgOmitsAnEmptyOrganization(t *testing.T) {
	// The scope predicate casts $3 to uuid, so an empty string would error.
	if orgArg(principalWith("")) != nil {
		t.Fatal("a user with no organization must pass NULL")
	}
	if orgArg(principalWith("3f2504e0-4f89-11d3-9a0c-0305e82c3301")) == nil {
		t.Fatal("a user with an organization must pass it through")
	}
}

func TestMaxQueryLengthLeavesRoomForRealFilters(t *testing.T) {
	// The customer screen saves q + customerType + grade; the VOC screen saves
	// four filters. The cap only needs to stop abuse, not real usage.
	if maxQueryLength < 200 {
		t.Fatalf("maxQueryLength %d is too small for the list filters", maxQueryLength)
	}
}

func principalWith(org string) *auth.Principal {
	return &auth.Principal{UserID: "u1", OrganizationID: org}
}
