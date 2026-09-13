package intelligence

import (
	"testing"

	"github.com/hkjang/relio/internal/crm"
)

// openFacts is facts() for a deal that is still being worked, which is the only
// state scoreDealHealth looks at any rule for.
func openFacts(history ...opportunityChange) healthFacts {
	f := facts(history...)
	f.Opportunity.Status = "OPEN"
	f.Opportunity.CustomerID = "c1"
	f.Opportunity.CustomerName = "가나상사"
	f.Opportunity.OwnerID = "u1"
	f.Opportunity.OwnerName = "김영업"
	return f
}

// scoring returns a rule of the given weight that always fires, so a case can
// say what a total should be without also arguing about which rules fired.
func scoring(code string, riskScore int, action string) HealthRule {
	return HealthRule{Code: code, Name: code, RuleType: "NO_DECISION_MAKER", RiskScore: riskScore, RecommendedAction: action, Active: true}
}

func TestScoreDealHealthAddsTheRiskOfEveryRuleThatFired(t *testing.T) {
	f := openFacts()
	f.DecisionMakers = 0 // every scoring() rule fires
	silent := HealthRule{Code: "NO_CHAMPION", Name: "NO_CHAMPION", RuleType: "NO_CHAMPION", RiskScore: 50, RecommendedAction: "챔피언 확보"}

	health := scoreDealHealth([]HealthRule{scoring("A", 15, "의사결정자 확인"), silent}, f)
	if health.RiskScore != 15 || health.HealthScore != 85 {
		t.Fatalf("risk/health = %d/%d, want 15/85", health.RiskScore, health.HealthScore)
	}
	if len(health.Factors) != 1 || health.Factors[0].Code != "A" {
		t.Fatalf("factors = %v, want only the rule that fired", health.Factors)
	}
	if health.Factors[0].Evidence["decisionMakerCount"] != 0 {
		t.Fatalf("evidence = %v, want the count the rule read", health.Factors[0].Evidence)
	}
	// The deal's identity travels with the verdict; the dashboards render it
	// without going back to the opportunity.
	if health.OpportunityID != "o1" || health.CustomerName != "가나상사" || health.OwnerName != "김영업" {
		t.Fatalf("identity = %+v", health)
	}
	if !health.CalculatedAt.Equal(f.Now) {
		t.Fatalf("calculatedAt = %v, want the instant the facts were read", health.CalculatedAt)
	}
}

func TestScoreDealHealthNamesTheLevelAtEachBoundary(t *testing.T) {
	for _, tc := range []struct {
		risk  int
		level string
	}{{0, "HEALTHY"}, {19, "HEALTHY"}, {20, "WATCH"}, {39, "WATCH"}, {40, "RISK"}, {69, "RISK"}, {70, "CRITICAL"}, {100, "CRITICAL"}} {
		f := openFacts()
		f.DecisionMakers = 0
		rules := []HealthRule{scoring("A", tc.risk, "확인")}
		if tc.risk == 0 {
			rules = nil
		}
		health := scoreDealHealth(rules, f)
		if health.RiskScore != tc.risk || health.RiskLevel != tc.level {
			t.Fatalf("risk %d = level %q, want %q", tc.risk, health.RiskLevel, tc.level)
		}
	}
}

func TestScoreDealHealthNeverReportsMoreRiskThanExists(t *testing.T) {
	f := openFacts()
	f.DecisionMakers = 0
	health := scoreDealHealth([]HealthRule{scoring("A", 60, "하나"), scoring("B", 70, "둘")}, f)
	// Two rules worth 130 together still leave a health score, not a negative one.
	if health.RiskScore != 100 || health.HealthScore != 0 {
		t.Fatalf("risk/health = %d/%d, want 100/0", health.RiskScore, health.HealthScore)
	}
	if len(health.Factors) != 2 {
		t.Fatalf("factors = %d, want both rules still listed", len(health.Factors))
	}
}

func TestScoreDealHealthListsEachRecommendationOnce(t *testing.T) {
	f := openFacts()
	f.DecisionMakers = 0
	health := scoreDealHealth([]HealthRule{
		scoring("A", 10, "의사결정자와 미팅"),
		scoring("B", 10, "의사결정자와 미팅"),
		scoring("C", 10, "가격 재검토"),
	}, f)
	want := []string{"의사결정자와 미팅", "가격 재검토"}
	if len(health.Recommendations) != len(want) {
		t.Fatalf("recommendations = %v, want %v", health.Recommendations, want)
	}
	for i, action := range want {
		if health.Recommendations[i] != action {
			t.Fatalf("recommendations = %v, want %v", health.Recommendations, want)
		}
	}
}

func TestScoreDealHealthLeavesAClosedDealAlone(t *testing.T) {
	for _, status := range []string{"WON", "LOST"} {
		f := openFacts()
		f.Opportunity.Status = status
		f.DecisionMakers = 0
		health := scoreDealHealth([]HealthRule{scoring("A", 80, "확인")}, f)
		if health.RiskScore != 0 || health.HealthScore != 100 || health.RiskLevel != "HEALTHY" {
			t.Fatalf("%s deal = %d/%d/%s, want 0/100/HEALTHY", status, health.RiskScore, health.HealthScore, health.RiskLevel)
		}
		if len(health.Factors) != 0 || len(health.Recommendations) != 0 {
			t.Fatalf("%s deal carries %v / %v", status, health.Factors, health.Recommendations)
		}
		// An empty list, not a null one: the clients render it either way only
		// because it is always an array.
		if health.Factors == nil || health.Recommendations == nil {
			t.Fatalf("%s deal must carry empty lists, not nil", status)
		}
	}
}

func TestHealthInputsAsksForEachCustomerAndStageOnce(t *testing.T) {
	opportunities := []crm.Opportunity{
		{ID: "o1", CustomerID: "c1", StageID: "s1"},
		{ID: "o2", CustomerID: "c1", StageID: "s1"},
		{ID: "o3", CustomerID: "c2", StageID: "s1"},
		{ID: "o4", CustomerID: "c2", StageID: "s2"},
	}
	customers, stages, deals := healthInputs(opportunities)
	if len(customers) != 2 || customers[0] != "c1" || customers[1] != "c2" {
		t.Fatalf("customers = %v, want [c1 c2]", customers)
	}
	if len(stages) != 2 || stages[0] != "s1" || stages[1] != "s2" {
		t.Fatalf("stages = %v, want [s1 s2]", stages)
	}
	if len(deals) != 4 {
		t.Fatalf("deals = %v, want all four", deals)
	}
}

func TestHealthInputsSkipsRowsWithNothingToLookUp(t *testing.T) {
	customers, stages, deals := healthInputs([]crm.Opportunity{{ID: "", CustomerID: "", StageID: ""}})
	if len(customers) != 0 || len(stages) != 0 || len(deals) != 0 {
		t.Fatalf("empty ids must not be looked up: %v %v %v", customers, stages, deals)
	}
	if customers, stages, deals = healthInputs(nil); customers != nil || stages != nil || deals != nil {
		t.Fatalf("no deals must ask for nothing: %v %v %v", customers, stages, deals)
	}
}
