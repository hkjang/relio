package intelligence

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, time.August, 12, 9, 0, 0, 0, time.UTC)

func daysAgo(n int) *time.Time {
	t := now.AddDate(0, 0, -n)
	return &t
}

func signalTypes(signals []Signal) map[string]Signal {
	out := map[string]Signal{}
	for _, signal := range signals {
		out[signal.SignalType] = signal
	}
	return out
}

func TestNoContactFiresOnlyPastTheThreshold(t *testing.T) {
	quiet := accountSignals(&accountFacts{ID: "a", Name: "고객", LastContactAt: daysAgo(31), Contacts: 1, DecisionMakers: 1}, now)
	if _, ok := signalTypes(quiet)["NO_CONTACT"]; !ok {
		t.Fatal("31 days of silence must raise NO_CONTACT")
	}
	fresh := accountSignals(&accountFacts{ID: "a", Name: "고객", LastContactAt: daysAgo(29), Contacts: 1, DecisionMakers: 1}, now)
	if _, ok := signalTypes(fresh)["NO_CONTACT"]; ok {
		t.Fatal("29 days is inside the threshold and must stay quiet")
	}
}

func TestNoContactEscalatesWithTime(t *testing.T) {
	medium := signalTypes(accountSignals(&accountFacts{ID: "a", LastContactAt: daysAgo(35), Contacts: 1, DecisionMakers: 1}, now))["NO_CONTACT"]
	high := signalTypes(accountSignals(&accountFacts{ID: "a", LastContactAt: daysAgo(70), Contacts: 1, DecisionMakers: 1}, now))["NO_CONTACT"]
	if medium.Severity != "MEDIUM" || high.Severity != "HIGH" {
		t.Fatalf("severity = %s / %s, want MEDIUM / HIGH", medium.Severity, high.Severity)
	}
}

func TestAccountWithNoContactsIsFlaggedHigher(t *testing.T) {
	none := signalTypes(accountSignals(&accountFacts{ID: "a", LastContactAt: daysAgo(1), Contacts: 0}, now))["DECISION_MAKER_MISSING"]
	some := signalTypes(accountSignals(&accountFacts{ID: "a", LastContactAt: daysAgo(1), Contacts: 3, DecisionMakers: 0}, now))["DECISION_MAKER_MISSING"]
	if none.Severity != "HIGH" || some.Severity != "MEDIUM" {
		t.Fatalf("severity = %s / %s, want HIGH / MEDIUM", none.Severity, some.Severity)
	}
	// A fully mapped account must produce neither.
	mapped := accountSignals(&accountFacts{ID: "a", LastContactAt: daysAgo(1), Contacts: 3, DecisionMakers: 1}, now)
	if _, ok := signalTypes(mapped)["DECISION_MAKER_MISSING"]; ok {
		t.Fatal("an account with a decision maker must not raise DECISION_MAKER_MISSING")
	}
}

func TestEngagementIncreaseNeedsRealGrowth(t *testing.T) {
	growing := accountSignals(&accountFacts{ID: "a", LastContactAt: daysAgo(1), Contacts: 1, DecisionMakers: 1, RecentTouches: 4, PriorTouches: 1}, now)
	if _, ok := signalTypes(growing)["ENGAGEMENT_INCREASE"]; !ok {
		t.Fatal("4 recent versus 1 prior touch is growth")
	}
	flat := accountSignals(&accountFacts{ID: "a", LastContactAt: daysAgo(1), Contacts: 1, DecisionMakers: 1, RecentTouches: 2, PriorTouches: 5}, now)
	if _, ok := signalTypes(flat)["ENGAGEMENT_INCREASE"]; ok {
		t.Fatal("a decline must not be reported as an increase")
	}
}

func TestStalledDealAndPassedCloseDate(t *testing.T) {
	closed := now.AddDate(0, 0, -5)
	signals := opportunitySignals(&opportunityFacts{ID: "o", Name: "딜", AccountID: "a", StageName: "제안",
		StageEnteredAt: now.AddDate(0, 0, -45), Status: "OPEN", CloseDate: &closed}, now)
	found := signalTypes(signals)
	if _, ok := found["DEAL_STALLED"]; !ok {
		t.Fatal("45 days in stage must raise DEAL_STALLED")
	}
	if _, ok := found["CLOSE_DATE_PASSED"]; !ok {
		t.Fatal("a close date in the past on an open deal must be reported")
	}
	// A won deal is finished; it cannot be stalled.
	won := opportunitySignals(&opportunityFacts{ID: "o", AccountID: "a", StageName: "제안",
		StageEnteredAt: now.AddDate(0, 0, -400), Status: "WON"}, now)
	if len(won) != 0 {
		t.Fatalf("a closed deal produced %d signals", len(won))
	}
}

func TestContractExpirySeverityTightensAsTheDateNears(t *testing.T) {
	cases := []struct {
		days     int
		renewal  string
		auto     bool
		expected string
	}{
		{80, "NOT_STARTED", false, "MEDIUM"},
		{25, "NOT_STARTED", false, "HIGH"},
		{10, "NOT_STARTED", false, "CRITICAL"},
		{10, "NOT_STARTED", true, "HIGH"}, // auto-renewing contracts are not a crisis
		{10, "IN_PROGRESS", false, "HIGH"},
	}
	for _, c := range cases {
		signals := contractSignals(&contractFacts{ID: "k", Title: "계약", AccountID: "a",
			EndDate: now.AddDate(0, 0, c.days), RenewalStatus: c.renewal, AutoRenew: c.auto}, now)
		if len(signals) != 1 {
			t.Fatalf("D-%d produced %d signals", c.days, len(signals))
		}
		if signals[0].Severity != c.expected {
			t.Fatalf("D-%d renewal=%s auto=%v severity = %s, want %s", c.days, c.renewal, c.auto, signals[0].Severity, c.expected)
		}
	}
	// Outside the notice window, and already expired, are both silent.
	if len(contractSignals(&contractFacts{EndDate: now.AddDate(0, 0, 120)}, now)) != 0 {
		t.Fatal("a contract outside the notice window must stay quiet")
	}
	if len(contractSignals(&contractFacts{EndDate: now.AddDate(0, 0, -5)}, now)) != 0 {
		t.Fatal("an expired contract is not an expiring one")
	}
}

func TestContractTitleDoesNotLeakStatusCodes(t *testing.T) {
	signals := contractSignals(&contractFacts{ID: "k", Title: "연간 유지보수 계약", AccountID: "a",
		EndDate: now.AddDate(0, 0, 20), RenewalStatus: "NOT_STARTED"}, now)
	if got := signals[0].Description; !strings.Contains(got, "미착수") || strings.Contains(got, "NOT_STARTED") {
		t.Fatalf("description = %q, want the Korean word and no enum code", got)
	}
}

func TestRelationshipRiskIsOffsetByRisingEngagement(t *testing.T) {
	quiet := []Signal{{SignalType: "NO_CONTACT", Severity: "HIGH", Title: "60일간 접촉 없음", EntityType: "ACCOUNT", EntityID: "a"}}
	alone := scoreRisks("a", quiet, &accountFacts{ID: "a"}, nil)
	withGrowth := scoreRisks("a", append(append([]Signal{}, quiet...),
		Signal{SignalType: "ENGAGEMENT_INCREASE", Severity: "LOW", Title: "접점 증가", EntityType: "ACCOUNT", EntityID: "a"}),
		&accountFacts{ID: "a"}, nil)
	if len(alone) != 1 || len(withGrowth) != 1 {
		t.Fatalf("expected one risk each, got %d and %d", len(alone), len(withGrowth))
	}
	if withGrowth[0].RiskScore >= alone[0].RiskScore {
		t.Fatalf("evidence against the risk must lower the score: %d vs %d", withGrowth[0].RiskScore, alone[0].RiskScore)
	}
}

func TestRiskScoreIsCappedAndExplained(t *testing.T) {
	signals := []Signal{
		{SignalType: "DEAL_STALLED", Severity: "HIGH", Title: "정체", EntityType: "OPPORTUNITY", EntityID: "o"},
		{SignalType: "CLOSE_DATE_PASSED", Severity: "HIGH", Title: "종료일 경과", EntityType: "OPPORTUNITY", EntityID: "o"},
	}
	risks := scoreRisks("a", signals, &accountFacts{ID: "a"}, map[string]*opportunityFacts{"o": {ID: "o", Name: "딜"}})
	if len(risks) != 1 {
		t.Fatalf("expected one deal risk, got %d", len(risks))
	}
	risk := risks[0]
	if risk.RiskScore != 100 {
		t.Fatalf("score = %d, want it clamped to 100", risk.RiskScore)
	}
	if len(risk.Factors) != 2 {
		t.Fatalf("a score must carry its factors, got %d", len(risk.Factors))
	}
	// Every factor names the signal it came from, or the explanation is a
	// number with no provenance.
	for _, factor := range risk.Factors {
		if factor.Signal == "" || factor.Detail == "" {
			t.Fatalf("incomplete factor %#v", factor)
		}
	}
}

func TestSeverityThresholdsMatchTheRequirement(t *testing.T) {
	cases := map[int]string{0: "LOW", 39: "LOW", 40: "MEDIUM", 69: "MEDIUM", 70: "HIGH", 89: "HIGH", 90: "CRITICAL", 100: "CRITICAL"}
	for score, want := range cases {
		if got := severityForScore(score); got != want {
			t.Fatalf("severityForScore(%d) = %s, want %s", score, got, want)
		}
	}
}

func TestRecommendationsOnlyForHighRisk(t *testing.T) {
	account := &accountFacts{ID: "a", Name: "고객", OwnerID: "u"}
	low := recommend(account, nil, []Risk{{RiskType: "RELATIONSHIP_RISK", RiskScore: 55, ID: "r"}}, now)
	if len(low) != 0 {
		t.Fatalf("a 55-point risk must not generate advice, got %d", len(low))
	}
	high := recommend(account, nil, []Risk{{RiskType: "RELATIONSHIP_RISK", RiskScore: 72, ID: "r"}}, now)
	if len(high) != 1 || high[0].RecommendationType != "SCHEDULE_MEETING" {
		t.Fatalf("unexpected advice: %#v", high)
	}
	if high[0].AssigneeID != "u" {
		t.Fatalf("advice must be addressed to the account owner, got %q", high[0].AssigneeID)
	}
	// A critical risk must be given a shorter deadline than a high one.
	critical := recommend(account, nil, []Risk{{RiskType: "RELATIONSHIP_RISK", RiskScore: 95, ID: "r"}}, now)
	if !critical[0].DueDate.Before(*high[0].DueDate) {
		t.Fatalf("critical due %v is not sooner than high due %v", critical[0].DueDate, high[0].DueDate)
	}
}

func TestRecommendationsAreNotDuplicatedPerType(t *testing.T) {
	account := &accountFacts{ID: "a", Name: "고객", OwnerID: "u"}
	out := recommend(account, nil, []Risk{
		{RiskType: "DEAL_RISK", RiskScore: 90, ID: "r1"},
		{RiskType: "DEAL_RISK", RiskScore: 85, ID: "r2"},
	}, now)
	if len(out) != 1 {
		t.Fatalf("two deal risks must collapse into one action, got %d", len(out))
	}
}

func TestInsightNeedsCorroboration(t *testing.T) {
	account := &accountFacts{ID: "a", Name: "고객"}
	if _, ok := buildInsight(account, []Signal{{Sentiment: "NEGATIVE", Title: "하나"}}, nil); ok {
		t.Fatal("a single signal is an anecdote, not an insight")
	}
	insight, ok := buildInsight(account, []Signal{
		{Sentiment: "NEGATIVE", Severity: "HIGH", Title: "하나"},
		{Sentiment: "NEGATIVE", Severity: "MEDIUM", Title: "둘"},
		{Sentiment: "POSITIVE", Severity: "LOW", Title: "긍정"},
	}, []Risk{{RiskScore: 80}})
	if !ok {
		t.Fatal("two negative signals must produce an insight")
	}
	if len(insight.Evidence) != 2 {
		t.Fatalf("evidence = %v, want only the negative signals", insight.Evidence)
	}
	if insight.Evidence[0] != "하나" {
		t.Fatalf("evidence must lead with the most severe signal, got %v", insight.Evidence)
	}
	if insight.Confidence <= 50 || insight.Confidence > 95 {
		t.Fatalf("confidence = %d, want it to rise with corroboration and stay bounded", insight.Confidence)
	}
}

func TestSignalKeyIsStablePerEntity(t *testing.T) {
	a := Signal{SignalType: "NO_CONTACT", EntityType: "ACCOUNT", EntityID: "x", Title: "45일간 접촉 없음"}
	b := Signal{SignalType: "NO_CONTACT", EntityType: "ACCOUNT", EntityID: "x", Title: "46일간 접촉 없음"}
	if signalKey(a) != signalKey(b) {
		t.Fatal("the dedupe key must not depend on the title, or every day opens a new signal")
	}
	c := Signal{SignalType: "NO_CONTACT", EntityType: "ACCOUNT", EntityID: "y"}
	if signalKey(a) == signalKey(c) {
		t.Fatal("different entities must not share a key")
	}
}
