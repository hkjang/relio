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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	DB    *pgxpool.Pool
	CRM   *crm.Service
	Audit *audit.Service
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
	rows, err := s.DB.Query(ctx, `SELECT h.before_data,h.after_data,h.changed_at,COALESCE(u.display_name,'') FROM opportunity_history h LEFT JOIN users u ON u.id=h.changed_by WHERE h.opportunity_id=$1 AND h.changed_at>=$2 ORDER BY h.changed_at DESC`, opportunityID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []opportunityChange{}
	for rows.Next() {
		var beforeRaw, afterRaw []byte
		var item opportunityChange
		if err = rows.Scan(&beforeRaw, &afterRaw, &item.ChangedAt, &item.Actor); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(beforeRaw, &item.Before)
		_ = json.Unmarshal(afterRaw, &item.After)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) DealHealth(ctx context.Context, p *auth.Principal, opportunityID string) (DealHealth, error) {
	opp, err := s.CRM.GetOpportunity(ctx, p, opportunityID)
	if err != nil {
		return DealHealth{}, err
	}
	now := time.Now().UTC()
	result := DealHealth{OpportunityID: opp.ID, OpportunityName: opp.Name, CustomerID: opp.CustomerID, CustomerName: opp.CustomerName, OwnerID: opp.OwnerID, OwnerName: opp.OwnerName, HealthScore: 100, RiskLevel: "HEALTHY", Factors: []HealthFactor{}, Recommendations: []string{}, CalculatedAt: now}
	if opp.Status != "OPEN" {
		return result, nil
	}
	rules, err := s.rules(ctx)
	if err != nil {
		return DealHealth{}, err
	}
	var decisionMakers, champions int
	_ = s.DB.QueryRow(ctx, `SELECT count(*) FILTER (WHERE decision_maker=true),count(*) FILTER (WHERE relationship_role='CHAMPION') FROM contacts WHERE customer_id=$1`, opp.CustomerID).Scan(&decisionMakers, &champions)
	var maxDays *int
	_ = s.DB.QueryRow(ctx, `SELECT max_days FROM pipeline_stages WHERE id=$1`, opp.StageID).Scan(&maxDays)
	history, err := s.changes(ctx, opp.ID, now.Add(-365*24*time.Hour))
	if err != nil {
		return DealHealth{}, err
	}
	recommendations := map[string]bool{}
	for _, rule := range rules {
		triggered := false
		evidence := map[string]any{}
		switch rule.RuleType {
		case "NO_ACTIVITY":
			days := thresholdNumber(rule.Threshold, "days", 14)
			age := math.Inf(1)
			if opp.LastActivityAt != nil {
				age = now.Sub(*opp.LastActivityAt).Hours() / 24
			}
			triggered = age >= days
			evidence = map[string]any{"daysWithoutActivity": func() any {
				if math.IsInf(age, 1) {
					return nil
				}
				return int(age)
			}(), "thresholdDays": days, "lastActivityAt": opp.LastActivityAt}
		case "CLOSE_DATE_PASSED":
			triggered = opp.ExpectedCloseDate != nil && opp.ExpectedCloseDate.Before(now)
			evidence = map[string]any{"expectedCloseDate": opp.ExpectedCloseDate}
		case "NO_NEXT_ACTION":
			triggered = strings.TrimSpace(opp.NextAction) == "" || opp.NextActionDate == nil
			evidence = map[string]any{"nextAction": opp.NextAction, "nextActionDate": opp.NextActionDate}
		case "STAGE_STALLED":
			threshold := int(thresholdNumber(rule.Threshold, "defaultDays", 30))
			if maxDays != nil && *maxDays > 0 {
				threshold = *maxDays
			}
			age := int(now.Sub(opp.StageEnteredAt).Hours() / 24)
			triggered = age > threshold
			evidence = map[string]any{"daysInStage": age, "thresholdDays": threshold, "stage": opp.StageName}
		case "CLOSE_DATE_SLIPPAGE":
			limit := int(thresholdNumber(rule.Threshold, "count", 3))
			window := time.Duration(thresholdNumber(rule.Threshold, "days", 180)) * 24 * time.Hour
			count := 0
			for _, change := range history {
				if now.Sub(change.ChangedAt) > window {
					continue
				}
				before, bok := asDate(change.Before["expectedCloseDate"])
				after, aok := asDate(change.After["expectedCloseDate"])
				if bok && aok && after.After(before) {
					count++
				}
			}
			triggered = count >= limit
			evidence = map[string]any{"slippageCount": count, "thresholdCount": limit}
		case "AMOUNT_DROP":
			limit := thresholdNumber(rule.Threshold, "percent", 30)
			maxDrop := 0.0
			for _, change := range history {
				before, bok := asNumber(change.Before["expectedAmount"])
				after, aok := asNumber(change.After["expectedAmount"])
				if bok && aok && before > 0 && after < before {
					maxDrop = math.Max(maxDrop, (before-after)/before*100)
				}
			}
			triggered = maxDrop >= limit
			evidence = map[string]any{"largestDropPercent": math.Round(maxDrop*10) / 10, "thresholdPercent": limit}
		case "PROBABILITY_DROP":
			limit := thresholdNumber(rule.Threshold, "points", 20)
			maxDrop := 0.0
			for _, change := range history {
				before, bok := asNumber(change.Before["probability"])
				after, aok := asNumber(change.After["probability"])
				if bok && aok {
					maxDrop = math.Max(maxDrop, before-after)
				}
			}
			triggered = maxDrop >= limit
			evidence = map[string]any{"largestDropPoints": maxDrop, "thresholdPoints": limit}
		case "NO_DECISION_MAKER":
			triggered = decisionMakers == 0
			evidence = map[string]any{"decisionMakerCount": decisionMakers}
		case "NO_CHAMPION":
			triggered = champions == 0
			evidence = map[string]any{"championCount": champions}
		}
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
	s.saveHealthSnapshot(ctx, result)
	return result, nil
}

func (s *Service) saveHealthSnapshot(ctx context.Context, health DealHealth) {
	factors, _ := json.Marshal(health.Factors)
	recommendations, _ := json.Marshal(health.Recommendations)
	_, _ = s.DB.Exec(ctx, `INSERT INTO opportunity_health_snapshots(id,opportunity_id,risk_score,health_score,risk_level,factors,recommendations) SELECT $1,$2,$3,$4,$5,$6,$7 WHERE NOT EXISTS (SELECT 1 FROM opportunity_health_snapshots WHERE opportunity_id=$2 AND risk_score=$3 AND calculated_at>now()-interval '6 hours')`, ids.New(), health.OpportunityID, health.RiskScore, health.HealthScore, health.RiskLevel, factors, recommendations)
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
	out := []DealHealth{}
	for _, opportunity := range page.Items {
		health, healthErr := s.DealHealth(ctx, p, opportunity.ID)
		if healthErr != nil {
			return nil, healthErr
		}
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
