package crm

import "testing"

func TestNormalizeContactAppliesDefaultsAndTrims(t *testing.T) {
	in := ContactInput{Name: "  김도현 "}
	if err := normalizeContact(&in); err != nil {
		t.Fatal(err)
	}
	if in.Name != "김도현" {
		t.Fatalf("name = %q, want trimmed", in.Name)
	}
	if in.RelationshipRole != "USER" || in.Influence != "MEDIUM" || in.Sentiment != "NEUTRAL" {
		t.Fatalf("defaults not applied: %+v", in)
	}
}

func TestNormalizeContactUppercasesEnums(t *testing.T) {
	in := ContactInput{Name: "김도현", RelationshipRole: " champion ", Influence: "high", Sentiment: "support"}
	if err := normalizeContact(&in); err != nil {
		t.Fatal(err)
	}
	if in.RelationshipRole != "CHAMPION" || in.Influence != "HIGH" || in.Sentiment != "SUPPORT" {
		t.Fatalf("enums not normalized: %+v", in)
	}
}

func TestNormalizeContactRejectsBadInput(t *testing.T) {
	over, under := 101, -1
	cases := map[string]ContactInput{
		"blank name":         {Name: "   "},
		"unknown role":       {Name: "김도현", RelationshipRole: "OWNER"},
		"unknown influence":  {Name: "김도현", Influence: "EXTREME"},
		"unknown sentiment":  {Name: "김도현", Sentiment: "ANGRY"},
		"strength above 100": {Name: "김도현", RelationshipStrength: &over},
		"power below zero":   {Name: "김도현", DecisionPower: &under},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			input := in
			if err := normalizeContact(&input); err == nil {
				t.Fatalf("normalizeContact accepted %+v", input)
			}
		})
	}
}

// CreateContact and UpdateContact must agree on what a valid contact is, which
// they only do while both route through normalizeContact. These catalogs are the
// single source of truth the UI mirrors.
func TestContactCatalogsMatchTheUI(t *testing.T) {
	for _, role := range []string{"DECISION_MAKER", "CHAMPION", "INFLUENCER", "USER", "PROCUREMENT"} {
		if !contactRoles[role] {
			t.Fatalf("role %s missing from the catalog", role)
		}
	}
	if len(contactRoles) != 5 || len(contactInfluences) != 3 || len(contactSentiments) != 3 {
		t.Fatalf("catalog sizes changed: %d roles, %d influences, %d sentiments",
			len(contactRoles), len(contactInfluences), len(contactSentiments))
	}
}
