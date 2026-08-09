package relationship

import "testing"

func TestValidRelationshipType(t *testing.T) {
	for _, value := range []string{"REPORTS_TO", "INFLUENCES", "WORKS_WITH", "BLOCKS", "TRUSTS", "ADVISES", "OTHER"} {
		if !validRelationshipType(value) {
			t.Fatalf("expected %s to be valid", value)
		}
	}
	if validRelationshipType("UNKNOWN") {
		t.Fatal("unexpected valid relationship type")
	}
}

func TestNormalizeWhiteSpaces(t *testing.T) {
	items, err := normalizeWhiteSpaces([]WhiteSpace{{ProductName: "  Analytics  ", Status: "discovery", PotentialAmount: 1200, Notes: "  validate  "}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID == "" || items[0].ProductName != "Analytics" || items[0].Status != "DISCOVERY" || items[0].Notes != "validate" {
		t.Fatalf("unexpected normalized item: %#v", items)
	}
	if _, err = normalizeWhiteSpaces([]WhiteSpace{{ProductName: "", Status: "NOT_OFFERED"}}); err == nil {
		t.Fatal("expected empty product name to fail")
	}
	if _, err = normalizeWhiteSpaces([]WhiteSpace{{ProductName: "CRM", Status: "INVALID"}}); err == nil {
		t.Fatal("expected invalid status to fail")
	}
	if _, err = normalizeWhiteSpaces([]WhiteSpace{{ProductName: "CRM", PotentialAmount: -1}}); err == nil {
		t.Fatal("expected negative amount to fail")
	}
}

func TestNormalizedStrings(t *testing.T) {
	items := normalizedStrings([]string{" Goal A ", "", "Goal A", "Goal B"})
	if len(items) != 2 || items[0] != "Goal A" || items[1] != "Goal B" {
		t.Fatalf("unexpected normalized strings: %#v", items)
	}
}
