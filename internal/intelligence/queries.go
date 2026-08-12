package intelligence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/platform/ids"
)

// Reading intelligence. Every query joins customers and applies the same Data
// Scope predicate the rest of the CRM uses, so a salesperson sees analysis of
// exactly the accounts they can already open — no more, and no less.

func scopeArgs(p *auth.Principal) []any {
	organization := any(nil)
	if strings.TrimSpace(p.OrganizationID) != "" {
		organization = p.OrganizationID
	}
	return []any{p.DataScope, p.UserID, organization}
}

func clampLimit(limit int) int {
	if limit < 1 || limit > 200 {
		return 50
	}
	return limit
}

func (s *Service) ListSignals(ctx context.Context, p *auth.Principal, f SignalFilter) ([]Signal, error) {
	if err := auth.Require(p, "intelligence:read"); err != nil {
		return nil, err
	}
	args := scopeArgs(p)
	args = append(args, f.AccountID, strings.ToUpper(f.EntityType), f.EntityID, strings.ToUpper(f.SignalType),
		strings.ToUpper(f.Severity), strings.ToUpper(f.Sentiment), statusOr(f.Status, "ACTIVE"), clampLimit(f.Limit))
	rows, err := s.DB.Query(ctx, `
		SELECT g.id::text,g.signal_type,g.sentiment,g.severity,g.entity_type,g.entity_id::text,g.account_id::text,c.name,
		       g.title,g.description,g.evidence,g.detected_at,g.source_type,COALESCE(g.source_id::text,''),g.status,g.resolved_at
		FROM signals g JOIN customers c ON c.id=g.account_id
		WHERE `+crm.ScopeSQL("c")+`
		  AND ($4='' OR g.account_id::text=$4) AND ($5='' OR g.entity_type=$5) AND ($6='' OR g.entity_id::text=$6)
		  AND ($7='' OR g.signal_type=$7) AND ($8='' OR g.severity=$8) AND ($9='' OR g.sentiment=$9)
		  AND ($10='ALL' OR g.status=$10)
		ORDER BY CASE g.severity WHEN 'CRITICAL' THEN 0 WHEN 'HIGH' THEN 1 WHEN 'MEDIUM' THEN 2 ELSE 3 END,
		         g.detected_at DESC LIMIT $11`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Signal{}
	for rows.Next() {
		var x Signal
		var evidence []byte
		if err = rows.Scan(&x.ID, &x.SignalType, &x.Sentiment, &x.Severity, &x.EntityType, &x.EntityID, &x.AccountID,
			&x.AccountName, &x.Title, &x.Description, &evidence, &x.DetectedAt, &x.SourceType, &x.SourceID,
			&x.Status, &x.ResolvedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(evidence, &x.Evidence)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Service) GetSignal(ctx context.Context, p *auth.Principal, id string) (Signal, error) {
	items, err := s.ListSignals(ctx, p, SignalFilter{Status: "ALL", Limit: 200})
	if err != nil {
		return Signal{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return Signal{}, errors.New("signal not found")
}

func (s *Service) ListRisks(ctx context.Context, p *auth.Principal, f RiskFilter) ([]Risk, error) {
	if err := auth.Require(p, "intelligence:read"); err != nil {
		return nil, err
	}
	args := scopeArgs(p)
	args = append(args, f.AccountID, strings.ToUpper(f.EntityType), f.EntityID, strings.ToUpper(f.RiskType),
		strings.ToUpper(f.Severity), statusOr(f.Status, "OPEN"), f.MinScore, clampLimit(f.Limit))
	rows, err := s.DB.Query(ctx, `
		SELECT r.id::text,r.risk_type,r.entity_type,r.entity_id::text,r.account_id::text,c.name,r.risk_score,r.severity,
		       r.title,r.description,r.factors,r.detected_at,r.resolved_at,COALESCE(r.accepted_note,''),r.status
		FROM risks r JOIN customers c ON c.id=r.account_id
		WHERE `+crm.ScopeSQL("c")+`
		  AND ($4='' OR r.account_id::text=$4) AND ($5='' OR r.entity_type=$5) AND ($6='' OR r.entity_id::text=$6)
		  AND ($7='' OR r.risk_type=$7) AND ($8='' OR r.severity=$8) AND ($9='ALL' OR r.status=$9)
		  AND r.risk_score >= $10
		ORDER BY r.risk_score DESC, r.detected_at DESC LIMIT $11`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Risk{}
	for rows.Next() {
		var x Risk
		var factors []byte
		if err = rows.Scan(&x.ID, &x.RiskType, &x.EntityType, &x.EntityID, &x.AccountID, &x.AccountName, &x.RiskScore,
			&x.Severity, &x.Title, &x.Description, &factors, &x.DetectedAt, &x.ResolvedAt, &x.AcceptedNote, &x.Status); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(factors, &x.Factors)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Service) GetRisk(ctx context.Context, p *auth.Principal, id string) (Risk, error) {
	items, err := s.ListRisks(ctx, p, RiskFilter{Status: "ALL", Limit: 200})
	if err != nil {
		return Risk{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return Risk{}, errors.New("risk not found")
}

// ExplainRisk restates a score as the arithmetic that produced it. A number
// nobody can question is a number nobody will trust.
func (s *Service) ExplainRisk(ctx context.Context, p *auth.Principal, id string) (map[string]any, error) {
	risk, err := s.GetRisk(ctx, p, id)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(risk.Factors))
	for _, factor := range risk.Factors {
		sign := "+"
		if factor.Points < 0 {
			sign = ""
		}
		lines = append(lines, fmt.Sprintf("%s (%s%d점)", factor.Detail, sign, factor.Points))
	}
	signals, err := s.ListSignals(ctx, p, SignalFilter{AccountID: risk.AccountID, Limit: 50})
	if err != nil {
		return nil, err
	}
	related := []Signal{}
	for _, signal := range signals {
		for _, factor := range risk.Factors {
			if signal.SignalType == factor.Signal {
				related = append(related, signal)
				break
			}
		}
	}
	return map[string]any{
		"risk": risk, "score": risk.RiskScore, "severity": risk.Severity,
		"reasons": lines, "signals": related,
		"threshold": map[string]int{"MEDIUM": 40, "HIGH": 70, "CRITICAL": 90},
	}, nil
}

func (s *Service) ListInsights(ctx context.Context, p *auth.Principal, f InsightFilter) ([]Insight, error) {
	if err := auth.Require(p, "intelligence:read"); err != nil {
		return nil, err
	}
	args := scopeArgs(p)
	args = append(args, f.AccountID, f.OpportunityID, strings.ToUpper(f.InsightType), statusOr(f.Status, "ACTIVE"), clampLimit(f.Limit))
	rows, err := s.DB.Query(ctx, `
		SELECT i.id::text,i.account_id::text,c.name,COALESCE(i.opportunity_id::text,''),i.insight_type,i.title,i.summary,
		       i.evidence,i.confidence,i.generated_at,i.expires_at,i.status
		FROM insights i JOIN customers c ON c.id=i.account_id
		WHERE `+crm.ScopeSQL("c")+`
		  AND ($4='' OR i.account_id::text=$4) AND ($5='' OR i.opportunity_id::text=$5)
		  AND ($6='' OR i.insight_type=$6) AND ($7='ALL' OR i.status=$7)
		ORDER BY i.generated_at DESC LIMIT $8`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Insight{}
	for rows.Next() {
		var x Insight
		var evidence []byte
		var expires *time.Time
		if err = rows.Scan(&x.ID, &x.AccountID, &x.AccountName, &x.OpportunityID, &x.InsightType, &x.Title, &x.Summary,
			&evidence, &x.Confidence, &x.GeneratedAt, &expires, &x.Status); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(evidence, &x.Evidence)
		if expires != nil {
			x.ExpiresAt = *expires
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Service) GetInsight(ctx context.Context, p *auth.Principal, id string) (Insight, error) {
	items, err := s.ListInsights(ctx, p, InsightFilter{Status: "ALL", Limit: 200})
	if err != nil {
		return Insight{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return Insight{}, errors.New("insight not found")
}

func (s *Service) ListRecommendations(ctx context.Context, p *auth.Principal, f RecommendationFilter) ([]Recommendation, error) {
	if err := auth.Require(p, "intelligence:read"); err != nil {
		return nil, err
	}
	assignee := f.AssigneeID
	if f.Mine {
		assignee = p.UserID
	}
	args := scopeArgs(p)
	args = append(args, f.AccountID, f.OpportunityID, assignee, strings.ToUpper(f.Priority),
		statusOr(f.Status, "OPEN"), clampLimit(f.Limit))
	rows, err := s.DB.Query(ctx, `
		SELECT n.id::text,n.account_id::text,c.name,COALESCE(n.opportunity_id::text,''),n.recommendation_type,n.priority,
		       n.title,n.description,n.due_date,n.source_type,COALESCE(n.source_id::text,''),n.assignee_id::text,
		       u.display_name,n.status,COALESCE(n.task_id::text,''),COALESCE(n.dismiss_reason,''),n.generated_at,n.decided_at
		FROM recommendations n JOIN customers c ON c.id=n.account_id JOIN users u ON u.id=n.assignee_id
		WHERE `+crm.ScopeSQL("c")+`
		  AND ($4='' OR n.account_id::text=$4) AND ($5='' OR n.opportunity_id::text=$5)
		  AND ($6='' OR n.assignee_id::text=$6) AND ($7='' OR n.priority=$7) AND ($8='ALL' OR n.status=$8)
		ORDER BY CASE n.priority WHEN 'HIGH' THEN 0 WHEN 'MEDIUM' THEN 1 ELSE 2 END,
		         n.due_date NULLS LAST, n.generated_at DESC LIMIT $9`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Recommendation{}
	for rows.Next() {
		var x Recommendation
		if err = rows.Scan(&x.ID, &x.AccountID, &x.AccountName, &x.OpportunityID, &x.RecommendationType, &x.Priority,
			&x.Title, &x.Description, &x.DueDate, &x.SourceType, &x.SourceID, &x.AssigneeID, &x.AssigneeName,
			&x.Status, &x.TaskID, &x.DismissReason, &x.GeneratedAt, &x.DecidedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Service) GetRecommendation(ctx context.Context, p *auth.Principal, id string) (Recommendation, error) {
	items, err := s.ListRecommendations(ctx, p, RecommendationFilter{Status: "ALL", Limit: 200})
	if err != nil {
		return Recommendation{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return Recommendation{}, errors.New("recommendation not found")
}

// AcceptRecommendation turns advice into work. The Task it creates is an
// ordinary CRM task, so it shows up in the same queue as everything else the
// user has to do — advice that lives only in an intelligence panel gets read
// once and forgotten.
func (s *Service) AcceptRecommendation(ctx context.Context, p *auth.Principal, id string, assigneeID string, dueDate *time.Time, m crm.RequestMeta) (Recommendation, error) {
	if err := auth.Require(p, "intelligence:write"); err != nil {
		return Recommendation{}, err
	}
	before, err := s.GetRecommendation(ctx, p, id)
	if err != nil {
		return Recommendation{}, err
	}
	if before.Status != "OPEN" {
		return Recommendation{}, fmt.Errorf("이미 %s 처리된 추천입니다", before.Status)
	}
	assignee := strings.TrimSpace(assigneeID)
	if assignee == "" {
		assignee = before.AssigneeID
	}
	due := before.DueDate
	if dueDate != nil {
		due = dueDate
	}
	taskID := ids.New()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Recommendation{}, err
	}
	defer tx.Rollback(ctx)
	priority := "NORMAL"
	if before.Priority == "HIGH" {
		priority = "HIGH"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO tasks(id,customer_id,opportunity_id,title,due_at,status,priority,assignee_id,created_by)
		VALUES($1,$2,$3,$4,$5,'OPEN',$6,$7,$8)`,
		taskID, before.AccountID, nullableID(before.OpportunityID), before.Title, due, priority, assignee, p.UserID); err != nil {
		return Recommendation{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE recommendations SET status='ACCEPTED',task_id=$2,decided_by=$3,decided_at=now(),
		assignee_id=$4,due_date=$5,updated_at=now() WHERE id=$1`, id, taskID, p.UserID, assignee, due); err != nil {
		return Recommendation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Recommendation{}, err
	}
	after, err := s.GetRecommendation(ctx, p, id)
	if err != nil {
		return Recommendation{}, err
	}
	s.record(ctx, p, m, "ACCEPT", "recommendation", id, before, after)
	return after, nil
}

// DismissRecommendation records that a human judged the advice wrong. The reason
// is required: a dismissal with no reason teaches the system nothing and reads,
// six months later, as if the recommendation was never seen.
func (s *Service) DismissRecommendation(ctx context.Context, p *auth.Principal, id, reason string, m crm.RequestMeta) (Recommendation, error) {
	if err := auth.Require(p, "intelligence:write"); err != nil {
		return Recommendation{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return Recommendation{}, errors.New("무시 사유를 입력하세요")
	}
	before, err := s.GetRecommendation(ctx, p, id)
	if err != nil {
		return Recommendation{}, err
	}
	if before.Status != "OPEN" {
		return Recommendation{}, fmt.Errorf("이미 %s 처리된 추천입니다", before.Status)
	}
	if _, err = s.DB.Exec(ctx, `UPDATE recommendations SET status='DISMISSED',dismiss_reason=$2,decided_by=$3,
		decided_at=now(),updated_at=now() WHERE id=$1`, id, strings.TrimSpace(reason), p.UserID); err != nil {
		return Recommendation{}, err
	}
	after, err := s.GetRecommendation(ctx, p, id)
	if err != nil {
		return Recommendation{}, err
	}
	s.record(ctx, p, m, "DISMISS", "recommendation", id, before, after)
	return after, nil
}

// AcceptRisk marks a risk as one the business has chosen to carry. It stays
// visible with its note rather than disappearing, because an accepted risk is
// still a risk.
func (s *Service) AcceptRisk(ctx context.Context, p *auth.Principal, id, note string, m crm.RequestMeta) (Risk, error) {
	if err := auth.Require(p, "intelligence:write"); err != nil {
		return Risk{}, err
	}
	if strings.TrimSpace(note) == "" {
		return Risk{}, errors.New("감수 사유를 입력하세요")
	}
	before, err := s.GetRisk(ctx, p, id)
	if err != nil {
		return Risk{}, err
	}
	if _, err = s.DB.Exec(ctx, `UPDATE risks SET status='ACCEPTED',accepted_by=$2,accepted_note=$3,updated_at=now()
		WHERE id=$1`, id, p.UserID, strings.TrimSpace(note)); err != nil {
		return Risk{}, err
	}
	after, err := s.GetRisk(ctx, p, id)
	if err != nil {
		return Risk{}, err
	}
	s.record(ctx, p, m, "ACCEPT", "risk", id, before, after)
	return after, nil
}

// IgnoreSignal hides one observation the user judges irrelevant. The engine will
// keep the decision even when the condition re-detects.
func (s *Service) IgnoreSignal(ctx context.Context, p *auth.Principal, id string, m crm.RequestMeta) (Signal, error) {
	if err := auth.Require(p, "intelligence:write"); err != nil {
		return Signal{}, err
	}
	before, err := s.GetSignal(ctx, p, id)
	if err != nil {
		return Signal{}, err
	}
	if _, err = s.DB.Exec(ctx, `UPDATE signals SET status='IGNORED',resolved_at=now(),updated_at=now() WHERE id=$1`, id); err != nil {
		return Signal{}, err
	}
	after, err := s.GetSignal(ctx, p, id)
	if err != nil {
		return Signal{}, err
	}
	s.record(ctx, p, m, "IGNORE", "signal", id, before, after)
	return after, nil
}

// AccountIntelligenceFor assembles the panel shown on Customer 360 and, when an
// opportunity is named, narrows the deal-specific parts to that deal.
func (s *Service) AccountIntelligenceFor(ctx context.Context, p *auth.Principal, accountID, opportunityID string) (AccountIntelligence, error) {
	if err := auth.Require(p, "intelligence:read"); err != nil {
		return AccountIntelligence{}, err
	}
	out := AccountIntelligence{AccountID: accountID, Severity: "LOW"}
	signals, err := s.ListSignals(ctx, p, SignalFilter{AccountID: accountID, Limit: 50})
	if err != nil {
		return out, err
	}
	risks, err := s.ListRisks(ctx, p, RiskFilter{AccountID: accountID, Status: "ALL", Limit: 50})
	if err != nil {
		return out, err
	}
	insights, err := s.ListInsights(ctx, p, InsightFilter{AccountID: accountID, Limit: 20})
	if err != nil {
		return out, err
	}
	recommendations, err := s.ListRecommendations(ctx, p, RecommendationFilter{AccountID: accountID, Limit: 20})
	if err != nil {
		return out, err
	}
	if opportunityID != "" {
		signals = filterSignals(signals, opportunityID)
		risks = filterRisks(risks, opportunityID)
	}
	open := risks[:0:0]
	for _, risk := range risks {
		if risk.Status == "RESOLVED" {
			continue
		}
		open = append(open, risk)
		// An accepted risk still counts toward the score shown; hiding it would
		// make the account look healthier than the business decided it is.
		if risk.RiskScore > out.RiskScore {
			out.RiskScore = risk.RiskScore
		}
	}
	out.Severity = severityForScore(out.RiskScore)
	out.Signals, out.Risks, out.Insights, out.Recommendations = signals, open, insights, recommendations
	if run, err := s.LastRun(ctx); err == nil && run != nil {
		out.AnalyzedAt = run.FinishedAt
	}
	return out, nil
}

func filterSignals(items []Signal, opportunityID string) []Signal {
	out := items[:0:0]
	for _, item := range items {
		if item.EntityType != "OPPORTUNITY" || item.EntityID == opportunityID {
			out = append(out, item)
		}
	}
	return out
}

func filterRisks(items []Risk, opportunityID string) []Risk {
	out := items[:0:0]
	for _, item := range items {
		if item.EntityType != "OPPORTUNITY" || item.EntityID == opportunityID {
			out = append(out, item)
		}
	}
	return out
}

func statusOr(value, fallback string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	return value
}

func (s *Service) record(ctx context.Context, p *auth.Principal, m crm.RequestMeta, action, resource, id string, before, after any) {
	if s.Audit == nil {
		return
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: m.Channel,
		Action: action + "_" + strings.ToUpper(resource), Resource: resource, ResourceID: id,
		Before: before, After: after, IP: m.IP, RequestID: m.RequestID, UserAgent: m.UserAgent})
}
