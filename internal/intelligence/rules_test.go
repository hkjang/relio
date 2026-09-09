package intelligence

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hkjang/relio/internal/crm"
)

// The rules run against an instant and against the calendar date the configured
// zone is on at that instant. Seoul is nine hours ahead of the process clock, so
// these two disagree exactly the way they do for a request served before 09:00 UTC.
var (
	ruleNow   = time.Date(2026, time.September, 10, 2, 0, 0, 0, time.UTC)
	ruleToday = time.Date(2026, time.September, 10, 0, 0, 0, 0, time.UTC)
)

func rule(ruleType string, threshold map[string]any) HealthRule {
	return HealthRule{Code: ruleType, Name: ruleType, RuleType: ruleType, Threshold: threshold, RiskScore: 20, Active: true}
}

func at(daysBack int) time.Time { return ruleNow.AddDate(0, 0, -daysBack) }

func atPtr(daysBack int) *time.Time {
	t := at(daysBack)
	return &t
}

func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func amountChange(daysBack int, before, after float64) opportunityChange {
	return opportunityChange{
		Before:    map[string]any{"expectedAmount": before},
		After:     map[string]any{"expectedAmount": after},
		ChangedAt: at(daysBack),
	}
}

func probabilityChange(daysBack int, before, after float64) opportunityChange {
	return opportunityChange{
		Before:    map[string]any{"probability": before},
		After:     map[string]any{"probability": after},
		ChangedAt: at(daysBack),
	}
}

func closeDateChange(daysBack int, before, after string) opportunityChange {
	return opportunityChange{
		Before:    map[string]any{"expectedCloseDate": before},
		After:     map[string]any{"expectedCloseDate": after},
		ChangedAt: at(daysBack),
	}
}

func facts(history ...opportunityChange) healthFacts {
	return healthFacts{
		Opportunity: crm.Opportunity{ID: "o1", Name: "연간 계약", StageEnteredAt: at(1), LastActivityAt: atPtr(1), NextAction: "제안서 발송", NextActionDate: atPtr(-3)},
		History:     history,
		// A fully mapped account, so the contact rules stay quiet unless a case
		// asks about them.
		DecisionMakers: 1,
		Champions:      1,
		Now:            ruleNow,
		Today:          ruleToday,
	}
}

func fires(t *testing.T, r HealthRule, f healthFacts) map[string]any {
	t.Helper()
	triggered, evidence := evaluateHealthRule(r, f)
	if !triggered {
		t.Fatalf("%s must fire, evidence %v", r.RuleType, evidence)
	}
	return evidence
}

func quiet(t *testing.T, r HealthRule, f healthFacts) map[string]any {
	t.Helper()
	triggered, evidence := evaluateHealthRule(r, f)
	if triggered {
		t.Fatalf("%s must stay quiet, evidence %v", r.RuleType, evidence)
	}
	return evidence
}

func TestNoActivityFiresOnlyPastTheThreshold(t *testing.T) {
	r := rule("NO_ACTIVITY", map[string]any{"days": 14.0})
	f := facts()
	f.Opportunity.LastActivityAt = atPtr(14)
	if got := fires(t, r, f)["daysWithoutActivity"]; got != 14 {
		t.Fatalf("daysWithoutActivity = %v, want 14", got)
	}
	f.Opportunity.LastActivityAt = atPtr(13)
	quiet(t, r, f)
	// A deal nobody ever touched has no age to report but is still silent.
	f.Opportunity.LastActivityAt = nil
	if got := fires(t, r, f)["daysWithoutActivity"]; got != nil {
		t.Fatalf("daysWithoutActivity = %v, want nil for a deal with no activity", got)
	}
}

func TestCloseDatePassedUsesTheConfiguredCalendarDate(t *testing.T) {
	r := rule("CLOSE_DATE_PASSED", nil)
	f := facts()
	f.Opportunity.ExpectedCloseDate = day(2026, time.September, 10)
	quiet(t, r, f) // due today is not late
	f.Opportunity.ExpectedCloseDate = day(2026, time.September, 9)
	fires(t, r, f)
	f.Opportunity.ExpectedCloseDate = nil
	quiet(t, r, f)
	// The process clock is already on the 10th but Seoul was on the 10th nine
	// hours earlier; a deal due on the 9th is late on both, one due on the 10th
	// on neither. The rule reads the date it is handed, not the instant.
	f.Opportunity.ExpectedCloseDate = day(2026, time.September, 10)
	f.Today = time.Date(2026, time.September, 11, 0, 0, 0, 0, time.UTC)
	fires(t, r, f)
}

func TestNoNextActionNeedsBothTheActionAndItsDate(t *testing.T) {
	r := rule("NO_NEXT_ACTION", nil)
	quiet(t, r, facts())
	f := facts()
	f.Opportunity.NextAction = "   "
	fires(t, r, f)
	f = facts()
	f.Opportunity.NextActionDate = nil
	fires(t, r, f)
}

func TestStageStalledPrefersTheStageLimitOverTheDefault(t *testing.T) {
	r := rule("STAGE_STALLED", map[string]any{"defaultDays": 30.0})
	f := facts()
	f.Opportunity.StageEnteredAt = at(31)
	if got := fires(t, r, f)["thresholdDays"]; got != 30 {
		t.Fatalf("thresholdDays = %v, want the rule default 30", got)
	}
	f.Opportunity.StageEnteredAt = at(30)
	quiet(t, r, f) // the rule is "over", not "at"
	// A stage that declares its own limit overrides the rule default; a stage
	// that declares none (0) leaves the default in place.
	limit := 7
	f.Opportunity.StageEnteredAt = at(10)
	f.StageMaxDays = &limit
	if got := fires(t, r, f)["thresholdDays"]; got != 7 {
		t.Fatalf("thresholdDays = %v, want the stage limit 7", got)
	}
	zero := 0
	f.StageMaxDays = &zero
	quiet(t, r, f)
}

func TestCloseDateSlippageCountsOnlyForwardMovesInsideTheWindow(t *testing.T) {
	r := rule("CLOSE_DATE_SLIPPAGE", map[string]any{"count": 3.0, "days": 180.0})
	fires(t, r, facts(
		closeDateChange(10, "2026-08-01", "2026-09-01"),
		closeDateChange(40, "2026-07-01", "2026-08-01"),
		closeDateChange(90, "2026-06-01", "2026-07-01"),
	))
	// A date pulled forward is not slippage, and neither is a move older than
	// the window.
	evidence := quiet(t, r, facts(
		closeDateChange(10, "2026-09-01", "2026-08-01"),
		closeDateChange(40, "2026-07-01", "2026-08-01"),
		closeDateChange(200, "2026-06-01", "2026-07-01"),
	))
	if evidence["slippageCount"] != 1 {
		t.Fatalf("slippageCount = %v, want 1", evidence["slippageCount"])
	}
}

// The seeded AMOUNT_DROP and PROBABILITY_DROP rules both declare a 90 day
// window. Reading the whole history instead left a deal that recovered months
// ago permanently flagged as dropping.
func TestDropRulesOnlyCountChangesInsideTheirWindow(t *testing.T) {
	amount := rule("AMOUNT_DROP", map[string]any{"percent": 30.0, "days": 90.0})
	probability := rule("PROBABILITY_DROP", map[string]any{"points": 20.0, "days": 90.0})
	fires(t, amount, facts(amountChange(89, 100, 50)))
	fires(t, probability, facts(probabilityChange(89, 80, 40)))
	if evidence := quiet(t, amount, facts(amountChange(91, 100, 50))); evidence["largestDropPercent"] != 0.0 {
		t.Fatalf("largestDropPercent = %v, want 0 outside the window", evidence["largestDropPercent"])
	}
	if evidence := quiet(t, probability, facts(probabilityChange(91, 80, 40))); evidence["largestDropPoints"] != 0.0 {
		t.Fatalf("largestDropPoints = %v, want 0 outside the window", evidence["largestDropPoints"])
	}
	// A narrower window an administrator configures has to narrow the rule.
	narrow := rule("AMOUNT_DROP", map[string]any{"percent": 30.0, "days": 7.0})
	quiet(t, narrow, facts(amountChange(30, 100, 50)))
	fires(t, narrow, facts(amountChange(30, 100, 50), amountChange(3, 100, 50)))
}

func TestDropRulesMeasureTheLargestDropAgainstTheThreshold(t *testing.T) {
	amount := rule("AMOUNT_DROP", map[string]any{"percent": 30.0})
	// 30% exactly is a drop; 29.9% is not, and a raise never is.
	fires(t, amount, facts(amountChange(1, 1000, 700)))
	quiet(t, amount, facts(amountChange(1, 1000, 701)))
	quiet(t, amount, facts(amountChange(1, 700, 1000)))
	evidence := fires(t, amount, facts(amountChange(1, 1000, 900), amountChange(2, 1000, 400)))
	if evidence["largestDropPercent"] != 60.0 {
		t.Fatalf("largestDropPercent = %v, want 60", evidence["largestDropPercent"])
	}
	probability := rule("PROBABILITY_DROP", map[string]any{"points": 20.0})
	fires(t, probability, facts(probabilityChange(1, 60, 40)))
	quiet(t, probability, facts(probabilityChange(1, 60, 41)))
	quiet(t, probability, facts(probabilityChange(1, 40, 60)))
}

func TestContactRulesReadTheAccountCounts(t *testing.T) {
	f := facts()
	quiet(t, rule("NO_DECISION_MAKER", nil), f)
	quiet(t, rule("NO_CHAMPION", nil), f)
	f.DecisionMakers, f.Champions = 0, 0
	if got := fires(t, rule("NO_DECISION_MAKER", nil), f)["decisionMakerCount"]; got != 0 {
		t.Fatalf("decisionMakerCount = %v, want 0", got)
	}
	if got := fires(t, rule("NO_CHAMPION", nil), f)["championCount"]; got != 0 {
		t.Fatalf("championCount = %v, want 0", got)
	}
}

func TestUnknownRuleTypeNeverFires(t *testing.T) {
	if evidence := quiet(t, rule("SOMETHING_ELSE", nil), facts(amountChange(1, 1000, 1))); len(evidence) != 0 {
		t.Fatalf("evidence = %v, want empty for an unknown rule type", evidence)
	}
}

func TestWindowDaysFallsBackForValuesThatCannotBoundAWindow(t *testing.T) {
	for name, threshold := range map[string]map[string]any{
		"missing":     {"percent": 30.0},
		"zero":        {"days": 0.0},
		"negative":    {"days": -30.0},
		"not-numeric": {"days": "90"},
	} {
		if got := windowDays(threshold, dropWindowDays); got != dropWindowDays {
			t.Fatalf("windowDays(%s) = %v, want the %d day default", name, got, dropWindowDays)
		}
	}
	if got := windowDays(map[string]any{"days": json.Number("45")}, dropWindowDays); got != 45 {
		t.Fatalf("windowDays(json.Number) = %v, want 45", got)
	}
	// A window an administrator switched off must not switch the rule off with it.
	fires(t, rule("AMOUNT_DROP", map[string]any{"percent": 30.0, "days": 0.0}), facts(amountChange(80, 1000, 100)))
}

// The history fetch has to reach back at least as far as the widest window any
// rule asks for, or a window wider than the fetch would be silently clipped.
func TestHistoryFetchCoversTheWidestConfiguredWindow(t *testing.T) {
	since := func(rules ...HealthRule) float64 {
		return ruleNow.Sub(historySince(rules, ruleNow)).Hours() / 24
	}
	if got := since(); got != historyWindowDays {
		t.Fatalf("no rules = %v days, want the %d day floor", got, historyWindowDays)
	}
	if got := since(rule("AMOUNT_DROP", map[string]any{"days": 90.0}), rule("NO_ACTIVITY", map[string]any{"days": 14.0})); got != historyWindowDays {
		t.Fatalf("narrow windows = %v days, want the %d day floor", got, historyWindowDays)
	}
	if got := since(rule("AMOUNT_DROP", map[string]any{"days": 90.0}), rule("CLOSE_DATE_SLIPPAGE", map[string]any{"days": 540.0})); got != 540 {
		t.Fatalf("widest window = %v days, want 540", got)
	}
	if got := since(rule("PROBABILITY_DROP", map[string]any{"days": 730.0})); got != 730 {
		t.Fatalf("widest window = %v days, want 730", got)
	}
}
