package intelligence

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/hkjang/relio/internal/platform/timezone"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	DB    *pgxpool.Pool
	CRM   *crm.Service
	Audit *audit.Service
	// Clock supplies the calendar date of the configured system.timezone, which
	// is the calendar every D-day here is counted on. A nil Clock answers with
	// the default zone rather than the process clock.
	Clock *timezone.Loader
}

type opportunityChange struct {
	Before    map[string]any
	After     map[string]any
	ChangedAt time.Time
	Actor     string
}

func thresholdNumber(values map[string]any, key string, fallback float64) float64 {
	v, ok := values[key]
	if !ok {
		return fallback
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return fallback
	}
}

func asNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// The windows the seeded rules declare. A rule whose threshold omits its window
// key — or carries one that cannot bound anything, such as zero, a negative
// number or a value that is not a number at all — is read as the seeded window
// rather than as a rule that can never fire.
const (
	slippageWindowDays = 180
	dropWindowDays     = 90
	// historyWindowDays is how far back DealHealth has always read opportunity
	// history. It stays the floor of the fetch so no rule sees less than before.
	historyWindowDays = 365
)

// windowDays reads a rule's lookback window in days. Unlike thresholdNumber it
// also rejects values that cannot bound a window, because a window of zero or
// less silently switches the rule off instead of widening or narrowing it.
func windowDays(values map[string]any, fallback float64) float64 {
	days := thresholdNumber(values, "days", fallback)
	if !(days > 0) {
		return fallback
	}
	return days
}

func asDate(v any) (time.Time, bool) {
	s, ok := v.(string)
	if !ok || s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse("2006-01-02", s)
	}
	return t, err == nil
}

func (s *Service) rules(ctx context.Context) ([]HealthRule, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,code,name,COALESCE(description,''),rule_type,threshold,risk_score,recommended_action,active,priority,version FROM deal_health_rules WHERE active=true ORDER BY priority,code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HealthRule{}
	for rows.Next() {
		var rule HealthRule
		var raw []byte
		if err = rows.Scan(&rule.ID, &rule.Code, &rule.Name, &rule.Description, &rule.RuleType, &raw, &rule.RiskScore, &rule.RecommendedAction, &rule.Active, &rule.Priority, &rule.Version); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &rule.Threshold)
		out = append(out, rule)
	}
	return out, rows.Err()
}

func (s *Service) AdminHealthRules(ctx context.Context, p *auth.Principal) ([]HealthRule, error) {
	if err := auth.Require(p, "admin:read"); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT id,code,name,COALESCE(description,''),rule_type,threshold,risk_score,recommended_action,active,priority,version FROM deal_health_rules ORDER BY priority,code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HealthRule{}
	for rows.Next() {
		var rule HealthRule
		var raw []byte
		if err = rows.Scan(&rule.ID, &rule.Code, &rule.Name, &rule.Description, &rule.RuleType, &raw, &rule.RiskScore, &rule.RecommendedAction, &rule.Active, &rule.Priority, &rule.Version); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &rule.Threshold)
		out = append(out, rule)
	}
	return out, rows.Err()
}

func (s *Service) SaveHealthRule(ctx context.Context, p *auth.Principal, id string, input HealthRuleInput, meta crm.RequestMeta) (HealthRule, error) {
	if err := auth.Require(p, "admin:write"); err != nil {
		return HealthRule{}, err
	}
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.RecommendedAction) == "" {
		return HealthRule{}, errors.New("rule name and recommendedAction are required")
	}
	if input.RiskScore < 0 || input.RiskScore > 100 {
		return HealthRule{}, errors.New("riskScore must be between 0 and 100")
	}
	if input.Priority == 0 {
		input.Priority = 100
	}
	threshold, err := json.Marshal(input.Threshold)
	if err != nil {
		return HealthRule{}, errors.New("invalid threshold")
	}
	beforeRules, err := s.AdminHealthRules(ctx, p)
	if err != nil {
		return HealthRule{}, err
	}
	var before HealthRule
	found := false
	for _, rule := range beforeRules {
		if rule.ID == id {
			before, found = rule, true
			break
		}
	}
	if !found {
		return HealthRule{}, errors.New("deal health rule not found")
	}
	var version int
	err = s.DB.QueryRow(ctx, `UPDATE deal_health_rules SET name=$1,description=$2,threshold=$3,risk_score=$4,recommended_action=$5,active=$6,priority=$7,updated_by=$8,updated_at=now(),version=version+1 WHERE id=$9 AND version=$10 RETURNING version`, strings.TrimSpace(input.Name), strings.TrimSpace(input.Description), threshold, input.RiskScore, strings.TrimSpace(input.RecommendedAction), input.Active, input.Priority, p.UserID, id, input.Version).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return HealthRule{}, errors.New("deal health rule was changed by another administrator")
	}
	if err != nil {
		return HealthRule{}, err
	}
	after := before
	after.Name = strings.TrimSpace(input.Name)
	after.Description = strings.TrimSpace(input.Description)
	after.Threshold = input.Threshold
	after.RiskScore = input.RiskScore
	after.RecommendedAction = strings.TrimSpace(input.RecommendedAction)
	after.Active = input.Active
	after.Priority = input.Priority
	after.Version = version
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "UPDATE_DEAL_HEALTH_RULE", Resource: "deal_health_rule", ResourceID: id, Before: before, After: after, IP: meta.IP, RequestID: meta.RequestID, UserAgent: meta.UserAgent})
	return after, nil
}

func (s *Service) changes(ctx context.Context, opportunityID string, since time.Time) ([]opportunityChange, error) {
	grouped, err := s.changesFor(ctx, []string{opportunityID}, since)
	if err != nil {
		return nil, err
	}
	if grouped[opportunityID] == nil {
		return []opportunityChange{}, nil
	}
	return grouped[opportunityID], nil
}

// changesFor reads the history of every deal in the set at once, keyed by deal.
// The id lists travel as text[] and are cast in the statement because pgx has no
// binary encoding for a []string bound straight to uuid[]; the cast still lets
// the opportunity_id index answer the lookup.
func (s *Service) changesFor(ctx context.Context, opportunityIDs []string, since time.Time) (map[string][]opportunityChange, error) {
	out := map[string][]opportunityChange{}
	if len(opportunityIDs) == 0 {
		return out, nil
	}
	rows, err := s.DB.Query(ctx, `SELECT h.opportunity_id::text,h.before_data,h.after_data,h.changed_at,COALESCE(u.display_name,'') FROM opportunity_history h LEFT JOIN users u ON u.id=h.changed_by WHERE h.opportunity_id=ANY($1::text[]::uuid[]) AND h.changed_at>=$2 ORDER BY h.changed_at DESC`, opportunityIDs, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var beforeRaw, afterRaw []byte
		var opportunityID string
		var item opportunityChange
		if err = rows.Scan(&opportunityID, &beforeRaw, &afterRaw, &item.ChangedAt, &item.Actor); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(beforeRaw, &item.Before)
		_ = json.Unmarshal(afterRaw, &item.After)
		out[opportunityID] = append(out[opportunityID], item)
	}
	return out, rows.Err()
}

type contactRoles struct {
	DecisionMakers int
	Champions      int
}

// contactRoles counts the decision makers and champions of every customer in
// the set. Like the per-deal query it replaces it is best effort: a deal whose
// count could not be read is judged as having none, which is what the rules
// already did when this query failed.
func (s *Service) contactRoles(ctx context.Context, customerIDs []string) map[string]contactRoles {
	out := map[string]contactRoles{}
	if len(customerIDs) == 0 {
		return out
	}
	rows, err := s.DB.Query(ctx, `SELECT customer_id::text,count(*) FILTER (WHERE decision_maker=true),count(*) FILTER (WHERE relationship_role='CHAMPION') FROM contacts WHERE customer_id=ANY($1::text[]::uuid[]) GROUP BY customer_id`, customerIDs)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var customerID string
		var roles contactRoles
		if rows.Scan(&customerID, &roles.DecisionMakers, &roles.Champions) != nil {
			return out
		}
		out[customerID] = roles
	}
	return out
}

// stageLimits reads the max_days of every stage in the set. A stage without a
// limit stays absent, which leaves STAGE_STALLED on its configured default.
func (s *Service) stageLimits(ctx context.Context, stageIDs []string) map[string]*int {
	out := map[string]*int{}
	if len(stageIDs) == 0 {
		return out
	}
	rows, err := s.DB.Query(ctx, `SELECT id::text,max_days FROM pipeline_stages WHERE id=ANY($1::text[]::uuid[])`, stageIDs)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var stageID string
		var maxDays *int
		if rows.Scan(&stageID, &maxDays) != nil {
			return out
		}
		out[stageID] = maxDays
	}
	return out
}

// healthFacts is everything the rules read besides the rule itself. Collecting
// it in one place keeps evaluateHealthRule a pure function of (rule, facts), so
// every rule type and threshold can be checked without a database.
type healthFacts struct {
	Opportunity    crm.Opportunity
	History        []opportunityChange
	DecisionMakers int
	Champions      int
	StageMaxDays   *int
	// Now is the instant the elapsed-time rules measure from. Today is the
	// calendar date the configured zone is on, which is what the DATE columns
	// (expected_close_date) have to be compared against.
	Now   time.Time
	Today time.Time
}

// historySince is how far back the history has to be read for these rules. Each
// history rule bounds itself by its own configured window, so the fetch has to
// cover the widest of them; it never reads less than the year it always has.
func historySince(rules []HealthRule, now time.Time) time.Time {
	widest := float64(historyWindowDays)
	for _, rule := range rules {
		switch rule.RuleType {
		case "CLOSE_DATE_SLIPPAGE":
			widest = math.Max(widest, windowDays(rule.Threshold, slippageWindowDays))
		case "AMOUNT_DROP", "PROBABILITY_DROP":
			widest = math.Max(widest, windowDays(rule.Threshold, dropWindowDays))
		}
	}
	return now.Add(-time.Duration(widest * float64(24*time.Hour)))
}

// evaluateHealthRule reports whether a rule fires for these facts, along with
// the evidence shown to the salesperson for why it did or did not.
func evaluateHealthRule(rule HealthRule, facts healthFacts) (bool, map[string]any) {
	opp, now := facts.Opportunity, facts.Now
	switch rule.RuleType {
	case "NO_ACTIVITY":
		days := thresholdNumber(rule.Threshold, "days", 14)
		age := math.Inf(1)
		if opp.LastActivityAt != nil {
			age = now.Sub(*opp.LastActivityAt).Hours() / 24
		}
		return age >= days, map[string]any{"daysWithoutActivity": func() any {
			if math.IsInf(age, 1) {
				return nil
			}
			return int(age)
		}(), "thresholdDays": days, "lastActivityAt": opp.LastActivityAt}
	case "CLOSE_DATE_PASSED":
		// expected_close_date is a DATE, so comparing it against an instant
		// called every deal due today overdue from one second after midnight.
		// It is late once the configured zone is on a later date than it.
		return opp.ExpectedCloseDate != nil && opp.ExpectedCloseDate.Before(facts.Today),
			map[string]any{"expectedCloseDate": opp.ExpectedCloseDate}
	case "NO_NEXT_ACTION":
		return strings.TrimSpace(opp.NextAction) == "" || opp.NextActionDate == nil,
			map[string]any{"nextAction": opp.NextAction, "nextActionDate": opp.NextActionDate}
	case "STAGE_STALLED":
		threshold := int(thresholdNumber(rule.Threshold, "defaultDays", 30))
		if facts.StageMaxDays != nil && *facts.StageMaxDays > 0 {
			threshold = *facts.StageMaxDays
		}
		age := int(now.Sub(opp.StageEnteredAt).Hours() / 24)
		return age > threshold, map[string]any{"daysInStage": age, "thresholdDays": threshold, "stage": opp.StageName}
	case "CLOSE_DATE_SLIPPAGE":
		limit := int(thresholdNumber(rule.Threshold, "count", 3))
		days := windowDays(rule.Threshold, slippageWindowDays)
		count := 0
		for _, change := range facts.withinWindow(days) {
			before, bok := asDate(change.Before["expectedCloseDate"])
			after, aok := asDate(change.After["expectedCloseDate"])
			if bok && aok && after.After(before) {
				count++
			}
		}
		return count >= limit, map[string]any{"slippageCount": count, "thresholdCount": limit, "windowDays": days}
	case "AMOUNT_DROP":
		limit := thresholdNumber(rule.Threshold, "percent", 30)
		days := windowDays(rule.Threshold, dropWindowDays)
		maxDrop := 0.0
		for _, change := range facts.withinWindow(days) {
			before, bok := asNumber(change.Before["expectedAmount"])
			after, aok := asNumber(change.After["expectedAmount"])
			if bok && aok && before > 0 && after < before {
				maxDrop = math.Max(maxDrop, (before-after)/before*100)
			}
		}
		return maxDrop >= limit, map[string]any{"largestDropPercent": math.Round(maxDrop*10) / 10, "thresholdPercent": limit, "windowDays": days}
	case "PROBABILITY_DROP":
		limit := thresholdNumber(rule.Threshold, "points", 20)
		days := windowDays(rule.Threshold, dropWindowDays)
		maxDrop := 0.0
		for _, change := range facts.withinWindow(days) {
			before, bok := asNumber(change.Before["probability"])
			after, aok := asNumber(change.After["probability"])
			if bok && aok {
				maxDrop = math.Max(maxDrop, before-after)
			}
		}
		return maxDrop >= limit, map[string]any{"largestDropPoints": maxDrop, "thresholdPoints": limit, "windowDays": days}
	case "NO_DECISION_MAKER":
		return facts.DecisionMakers == 0, map[string]any{"decisionMakerCount": facts.DecisionMakers}
	case "NO_CHAMPION":
		return facts.Champions == 0, map[string]any{"championCount": facts.Champions}
	}
	return false, map[string]any{}
}

// withinWindow is the history a rule with this window may count. A deal that
// lost half its amount a year ago is not a deal whose amount is dropping now.
func (f healthFacts) withinWindow(days float64) []opportunityChange {
	window := time.Duration(days * float64(24*time.Hour))
	out := make([]opportunityChange, 0, len(f.History))
	for _, change := range f.History {
		if f.Now.Sub(change.ChangedAt) <= window {
			out = append(out, change)
		}
	}
	return out
}

// scoreDealHealth is the whole of the scoring: which rules fired, what that
// adds up to and what it is called. Keeping it a pure function of (rules,
// facts) is what lets one deal and a dashboard of two hundred share a verdict
// while reading their facts through different queries.
func scoreDealHealth(rules []HealthRule, facts healthFacts) DealHealth {
	opp := facts.Opportunity
	result := DealHealth{OpportunityID: opp.ID, OpportunityName: opp.Name, CustomerID: opp.CustomerID, CustomerName: opp.CustomerName, OwnerID: opp.OwnerID, OwnerName: opp.OwnerName, HealthScore: 100, RiskLevel: "HEALTHY", Factors: []HealthFactor{}, Recommendations: []string{}, CalculatedAt: facts.Now}
	if opp.Status != "OPEN" {
		return result
	}
	recommendations := map[string]bool{}
	for _, rule := range rules {
		triggered, evidence := evaluateHealthRule(rule, facts)
		if triggered {
			result.RiskScore += rule.RiskScore
			result.Factors = append(result.Factors, HealthFactor{Code: rule.Code, Name: rule.Name, Description: rule.Description, RiskScore: rule.RiskScore, Evidence: evidence, RecommendedAction: rule.RecommendedAction})
			if !recommendations[rule.RecommendedAction] {
				recommendations[rule.RecommendedAction] = true
				result.Recommendations = append(result.Recommendations, rule.RecommendedAction)
			}
		}
	}
	if result.RiskScore > 100 {
		result.RiskScore = 100
	}
	result.HealthScore = 100 - result.RiskScore
	switch {
	case result.RiskScore >= 70:
		result.RiskLevel = "CRITICAL"
	case result.RiskScore >= 40:
		result.RiskLevel = "RISK"
	case result.RiskScore >= 20:
		result.RiskLevel = "WATCH"
	}
	return result
}

// healthInputs is the set of rows the rules need for these deals: one entry per
// distinct customer, stage and deal. A pipeline usually crowds many deals onto
// a few stages and customers, so collapsing the duplicates keeps the batched
// lookups smaller than the deal list itself.
func healthInputs(opportunities []crm.Opportunity) (customerIDs, stageIDs, opportunityIDs []string) {
	seenCustomer, seenStage := map[string]bool{}, map[string]bool{}
	for _, opp := range opportunities {
		if opp.CustomerID != "" && !seenCustomer[opp.CustomerID] {
			seenCustomer[opp.CustomerID] = true
			customerIDs = append(customerIDs, opp.CustomerID)
		}
		if opp.StageID != "" && !seenStage[opp.StageID] {
			seenStage[opp.StageID] = true
			stageIDs = append(stageIDs, opp.StageID)
		}
		if opp.ID != "" {
			opportunityIDs = append(opportunityIDs, opp.ID)
		}
	}
	return customerIDs, stageIDs, opportunityIDs
}

// healthOf scores a whole set of deals, returning one verdict per deal in the
// order they were given. The rules, the contact roles, the stage limits and the
// history are each read once for the entire set instead of once per deal, and
// the snapshots are written in a single round trip.
func (s *Service) healthOf(ctx context.Context, opportunities []crm.Opportunity) ([]DealHealth, error) {
	out := make([]DealHealth, len(opportunities))
	// One instant for the whole set, so two deals on the same dashboard cannot
	// be judged against different days or different ages.
	now := time.Now().UTC()
	today := s.Clock.DateAt(ctx, now)
	open := make([]int, 0, len(opportunities))
	for i, opp := range opportunities {
		if opp.Status == "OPEN" {
			open = append(open, i)
			continue
		}
		out[i] = scoreDealHealth(nil, healthFacts{Opportunity: opp, Now: now, Today: today})
	}
	if len(open) == 0 {
		return out, nil
	}
	rules, err := s.rules(ctx)
	if err != nil {
		return nil, err
	}
	scoring := make([]crm.Opportunity, 0, len(open))
	for _, i := range open {
		scoring = append(scoring, opportunities[i])
	}
	customerIDs, stageIDs, opportunityIDs := healthInputs(scoring)
	roles := s.contactRoles(ctx, customerIDs)
	limits := s.stageLimits(ctx, stageIDs)
	history, err := s.changesFor(ctx, opportunityIDs, historySince(rules, now))
	if err != nil {
		return nil, err
	}
	scored := make([]DealHealth, 0, len(open))
	for _, i := range open {
		opp := opportunities[i]
		facts := healthFacts{Opportunity: opp, History: history[opp.ID], DecisionMakers: roles[opp.CustomerID].DecisionMakers, Champions: roles[opp.CustomerID].Champions, StageMaxDays: limits[opp.StageID], Now: now, Today: today}
		out[i] = scoreDealHealth(rules, facts)
		scored = append(scored, out[i])
	}
	s.saveHealthSnapshots(ctx, scored)
	return out, nil
}

func (s *Service) DealHealth(ctx context.Context, p *auth.Principal, opportunityID string) (DealHealth, error) {
	opp, err := s.CRM.GetOpportunity(ctx, p, opportunityID)
	if err != nil {
		return DealHealth{}, err
	}
	health, err := s.healthOf(ctx, []crm.Opportunity{opp})
	if err != nil {
		return DealHealth{}, err
	}
	return health[0], nil
}

// saveHealthSnapshots records the verdicts in one round trip. The write stays
// best effort as it always was, and each row still skips itself if the same
// score was already recorded in the last six hours; a batch that fails leaves
// the whole set for the next refresh to record instead of half of it.
func (s *Service) saveHealthSnapshots(ctx context.Context, healths []DealHealth) {
	if len(healths) == 0 {
		return
	}
	batch := &pgx.Batch{}
	for _, health := range healths {
		factors, _ := json.Marshal(health.Factors)
		recommendations, _ := json.Marshal(health.Recommendations)
		batch.Queue(`INSERT INTO opportunity_health_snapshots(id,opportunity_id,risk_score,health_score,risk_level,factors,recommendations) SELECT $1,$2,$3,$4,$5,$6,$7 WHERE NOT EXISTS (SELECT 1 FROM opportunity_health_snapshots WHERE opportunity_id=$2 AND risk_score=$3 AND calculated_at>now()-interval '6 hours')`, ids.New(), health.OpportunityID, health.RiskScore, health.HealthScore, health.RiskLevel, factors, recommendations)
	}
	_ = s.DB.SendBatch(ctx, batch).Close()
}

func (s *Service) DealInspection(ctx context.Context, p *auth.Principal, opportunityID string, days int) (DealInspection, error) {
	if days < 1 || days > 365 {
		days = 7
	}
	health, err := s.DealHealth(ctx, p, opportunityID)
	if err != nil {
		return DealInspection{}, err
	}
	history, err := s.changes(ctx, opportunityID, time.Now().Add(-time.Duration(days)*24*time.Hour))
	if err != nil {
		return DealInspection{}, err
	}
	fields := []string{"expectedCloseDate", "expectedAmount", "probability", "stageName", "forecastCategory", "nextAction", "status"}
	changes := []DealChange{}
	for _, entry := range history {
		for _, field := range fields {
			before, after := entry.Before[field], entry.After[field]
			if !valuesEqual(before, after) {
				changes = append(changes, DealChange{Field: field, Before: before, After: after, ChangedAt: entry.ChangedAt, ChangedBy: entry.Actor})
			}
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].ChangedAt.After(changes[j].ChangedAt) })
	return DealInspection{Health: health, PeriodDays: days, Changes: changes, ChangeCount: len(changes)}, nil
}

func valuesEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func (s *Service) DealsAtRisk(ctx context.Context, p *auth.Principal, minimum, limit int) ([]DealHealth, error) {
	if minimum < 1 {
		minimum = 40
	}
	if limit < 1 || limit > 100 {
		limit = 25
	}
	page, err := s.CRM.ListOpportunities(ctx, p, crm.OpportunityFilter{Status: "OPEN", Limit: 200})
	if err != nil {
		return nil, err
	}
	healths, err := s.healthOf(ctx, page.Items)
	if err != nil {
		return nil, err
	}
	out := []DealHealth{}
	for _, health := range healths {
		if health.RiskScore >= minimum {
			out = append(out, health)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RiskScore == out[j].RiskScore {
			return out[i].OpportunityName < out[j].OpportunityName
		}
		return out[i].RiskScore > out[j].RiskScore
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
