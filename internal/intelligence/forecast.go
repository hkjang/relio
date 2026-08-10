package intelligence

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5"
)

type snapshotItem struct {
	ID, Name, Category, Status, StageID, StageName string
	Amount, Weighted, Probability                  float64
	CloseDate                                      *time.Time
}

func (s *Service) CaptureForecastSnapshots(ctx context.Context) error {
	var enabled bool
	if err := s.DB.QueryRow(ctx, `SELECT COALESCE((SELECT value::text::boolean FROM system_settings WHERE namespace='sales_intelligence' AND key='snapshot_enabled'),true)`).Scan(&enabled); err != nil || !enabled {
		return err
	}
	rows, err := s.DB.Query(ctx, `SELECT id,organization_id FROM users WHERE active=true`)
	if err != nil {
		return err
	}
	type owner struct {
		id             string
		organizationID *string
	}
	owners := []owner{}
	for rows.Next() {
		var item owner
		if err = rows.Scan(&item.id, &item.organizationID); err != nil {
			rows.Close()
			return err
		}
		owners = append(owners, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, owner := range owners {
		var pipeline, weighted, commit, bestCase float64
		var open int
		if err = s.DB.QueryRow(ctx, `SELECT COALESCE(sum(base_expected_amount) FILTER(WHERE status='OPEN'),0),COALESCE(sum(base_weighted_amount) FILTER(WHERE status='OPEN'),0),COALESCE(sum(base_expected_amount) FILTER(WHERE status='OPEN' AND forecast_category='COMMIT'),0),COALESCE(sum(base_expected_amount) FILTER(WHERE status='OPEN' AND forecast_category='BEST_CASE'),0),count(*) FILTER(WHERE status='OPEN') FROM opportunities WHERE owner_id=$1`, owner.id).Scan(&pipeline, &weighted, &commit, &bestCase, &open); err != nil {
			return err
		}
		metrics := map[string]any{"pipeline": pipeline, "weighted": weighted, "commit": commit, "bestCase": bestCase, "openDeals": open}
		var snapshotID string
		err = s.DB.QueryRow(ctx, `INSERT INTO forecast_snapshots(id,snapshot_date,owner_id,organization_id,metrics) VALUES($1,current_date,$2,$3,$4) ON CONFLICT(snapshot_date,owner_id) WHERE owner_id IS NOT NULL DO UPDATE SET metrics=excluded.metrics,created_at=now() RETURNING id`, ids.New(), owner.id, owner.organizationID, metrics).Scan(&snapshotID)
		if err != nil {
			return err
		}
		if _, err = s.DB.Exec(ctx, `DELETE FROM forecast_snapshot_items WHERE snapshot_id=$1`, snapshotID); err != nil {
			return err
		}
		_, err = s.DB.Exec(ctx, `INSERT INTO forecast_snapshot_items(snapshot_id,opportunity_id,owner_id,organization_id,forecast_category,status,stage_id,stage_name,expected_amount,weighted_amount,probability,expected_close_date) SELECT $1,o.id,o.owner_id,o.organization_id,o.forecast_category,o.status,o.stage_id,s.name,o.base_expected_amount,o.base_weighted_amount,o.probability,o.expected_close_date FROM opportunities o JOIN pipeline_stages s ON s.id=o.stage_id WHERE o.owner_id=$2`, snapshotID, owner.id)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) snapshotDate(ctx context.Context, before *time.Time) (time.Time, error) {
	var value time.Time
	var err error
	if before == nil {
		err = s.DB.QueryRow(ctx, `SELECT max(snapshot_date) FROM forecast_snapshots WHERE owner_id IS NOT NULL`).Scan(&value)
	} else {
		err = s.DB.QueryRow(ctx, `SELECT COALESCE(max(snapshot_date) FILTER(WHERE snapshot_date<=$1),min(snapshot_date)) FROM forecast_snapshots WHERE owner_id IS NOT NULL`, *before).Scan(&value)
	}
	return value, err
}

func (s *Service) loadSnapshot(ctx context.Context, p *auth.Principal, date time.Time) (map[string]snapshotItem, error) {
	query := `SELECT i.opportunity_id,o.name,i.forecast_category,i.status,i.stage_id,i.stage_name,i.expected_amount,i.weighted_amount,i.probability,i.expected_close_date FROM forecast_snapshot_items i JOIN forecast_snapshots f ON f.id=i.snapshot_id JOIN opportunities o ON o.id=i.opportunity_id WHERE f.snapshot_date=$4 AND ` + crm.ScopeSQL("o")
	rows, err := s.DB.Query(ctx, query, p.DataScope, p.UserID, func() any {
		if p.OrganizationID == "" {
			return nil
		}
		return p.OrganizationID
	}(), date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]snapshotItem{}
	for rows.Next() {
		var item snapshotItem
		if err = rows.Scan(&item.ID, &item.Name, &item.Category, &item.Status, &item.StageID, &item.StageName, &item.Amount, &item.Weighted, &item.Probability, &item.CloseDate); err != nil {
			return nil, err
		}
		out[item.ID] = item
	}
	return out, rows.Err()
}

func addMovement(items *[]ForecastMovement, kind string, amount float64, item snapshotItem) {
	*items = append(*items, ForecastMovement{Type: kind, Count: 1, Amount: amount, OpportunityID: item.ID, Name: item.Name})
}

func (s *Service) ForecastIntelligence(ctx context.Context, p *auth.Principal, days int) (ForecastIntelligence, error) {
	if err := auth.Require(p, "forecast:read"); err != nil {
		return ForecastIntelligence{}, err
	}
	if days < 1 || days > 365 {
		days = 7
	}
	if err := s.CaptureForecastSnapshots(ctx); err != nil {
		return ForecastIntelligence{}, err
	}
	currentDate, err := s.snapshotDate(ctx, nil)
	if err != nil {
		return ForecastIntelligence{}, err
	}
	cutoff := currentDate.AddDate(0, 0, -days)
	previousDate, err := s.snapshotDate(ctx, &cutoff)
	if err != nil {
		return ForecastIntelligence{}, err
	}
	previous, err := s.loadSnapshot(ctx, p, previousDate)
	if err != nil {
		return ForecastIntelligence{}, err
	}
	current, err := s.loadSnapshot(ctx, p, currentDate)
	if err != nil {
		return ForecastIntelligence{}, err
	}
	out := ForecastIntelligence{FromDate: previousDate.Format("2006-01-02"), ToDate: currentDate.Format("2006-01-02"), Movements: []ForecastMovement{}, GeneratedAt: time.Now().UTC()}
	for _, item := range previous {
		if item.Status == "OPEN" {
			out.PreviousAmount += item.Amount
		}
	}
	for id, item := range current {
		if item.Status == "OPEN" {
			out.CurrentAmount += item.Amount
			out.Weighted += item.Weighted
			if item.Category == "COMMIT" {
				out.RepCommit += item.Amount
			}
		}
		before, ok := previous[id]
		if !ok {
			if item.Status == "OPEN" {
				addMovement(&out.Movements, "NEW_PIPELINE", item.Amount, item)
			}
			continue
		}
		if before.Status == "OPEN" && item.Status == "LOST" {
			addMovement(&out.Movements, "LOST", -before.Amount, item)
			continue
		}
		if before.Status == "OPEN" && item.Status == "WON" {
			addMovement(&out.Movements, "WON", -before.Amount, item)
			continue
		}
		if item.Amount > before.Amount {
			addMovement(&out.Movements, "AMOUNT_INCREASE", item.Amount-before.Amount, item)
		} else if item.Amount < before.Amount {
			addMovement(&out.Movements, "AMOUNT_DECREASE", item.Amount-before.Amount, item)
		}
		if before.CloseDate != nil && item.CloseDate != nil && item.CloseDate.After(*before.CloseDate) {
			addMovement(&out.Movements, "SLIPPAGE", -item.Amount, item)
		}
	}
	for id, item := range previous {
		if _, ok := current[id]; !ok && item.Status == "OPEN" {
			addMovement(&out.Movements, "REMOVED", -item.Amount, item)
		}
	}
	out.ChangeAmount = out.CurrentAmount - out.PreviousAmount
	query := `SELECT COALESCE(sum(CASE WHEN COALESCE(ov.forecast_category,o.forecast_category)='COMMIT' THEN COALESCE(ov.amount,o.base_expected_amount) ELSE 0 END),0) FROM opportunities o LEFT JOIN LATERAL (SELECT forecast_category,amount FROM forecast_overrides f WHERE f.opportunity_id=o.id AND f.active=true ORDER BY f.updated_at DESC LIMIT 1) ov ON true WHERE o.status='OPEN' AND ` + crm.ScopeSQL("o")
	if err = s.DB.QueryRow(ctx, query, p.DataScope, p.UserID, func() any {
		if p.OrganizationID == "" {
			return nil
		}
		return p.OrganizationID
	}()).Scan(&out.ManagerCommit); err != nil {
		return ForecastIntelligence{}, err
	}
	sort.Slice(out.Movements, func(i, j int) bool {
		if out.Movements[i].Type == out.Movements[j].Type {
			return out.Movements[i].Name < out.Movements[j].Name
		}
		return out.Movements[i].Type < out.Movements[j].Type
	})
	return out, nil
}

func (s *Service) SaveForecastOverride(ctx context.Context, p *auth.Principal, opportunityID string, input ForecastOverrideInput, meta crm.RequestMeta) (map[string]any, error) {
	if !p.Has("forecast:write") && !p.Has("admin:write") {
		return nil, errors.New("permission forecast:write is required")
	}
	opp, err := s.CRM.GetOpportunity(ctx, p, opportunityID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Reason) == "" {
		return nil, errors.New("override reason is required")
	}
	if input.ForecastCategory != "" && input.ForecastCategory != "COMMIT" && input.ForecastCategory != "BEST_CASE" && input.ForecastCategory != "PIPELINE" && input.ForecastCategory != "CLOSED" {
		return nil, errors.New("invalid forecastCategory")
	}
	if !p.IsBootstrap && !p.Has("admin:write") {
		var managerID *string
		if err = s.DB.QueryRow(ctx, `SELECT manager_id FROM users WHERE id=$1`, opp.OwnerID).Scan(&managerID); err != nil {
			return nil, err
		}
		if managerID == nil || *managerID != p.UserID {
			return nil, errors.New("only the opportunity owner's manager can override forecast")
		}
	}
	if input.Probability != nil && (*input.Probability < 0 || *input.Probability > 100) {
		return nil, errors.New("probability must be between 0 and 100")
	}
	if input.Amount != nil && *input.Amount < 0 {
		return nil, errors.New("amount cannot be negative")
	}
	id := ids.New()
	var version int
	err = s.DB.QueryRow(ctx, `INSERT INTO forecast_overrides(id,opportunity_id,manager_id,forecast_category,probability,amount,reason) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(opportunity_id,manager_id) DO UPDATE SET forecast_category=excluded.forecast_category,probability=excluded.probability,amount=excluded.amount,reason=excluded.reason,active=true,version=forecast_overrides.version+1,updated_at=now() WHERE forecast_overrides.version=$8 RETURNING id,version`, id, opportunityID, p.UserID, func() any {
		if input.ForecastCategory == "" {
			return nil
		}
		return input.ForecastCategory
	}(), input.Probability, input.Amount, strings.TrimSpace(input.Reason), input.Version).Scan(&id, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("forecast override was changed by another manager")
	}
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": id, "opportunityId": opportunityID, "managerId": p.UserID, "forecastCategory": input.ForecastCategory, "probability": input.Probability, "amount": input.Amount, "reason": input.Reason, "version": version}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: meta.Channel, Action: "FORECAST_OVERRIDE", Resource: "opportunity", ResourceID: opportunityID, After: out, IP: meta.IP, RequestID: meta.RequestID, UserAgent: meta.UserAgent})
	return out, nil
}

func (s *Service) Coaching(ctx context.Context, p *auth.Principal) (CoachingDashboard, error) {
	if err := auth.Require(p, "opportunity:read"); err != nil {
		return CoachingDashboard{}, err
	}
	threshold := 40
	_ = s.DB.QueryRow(ctx, `SELECT COALESCE((SELECT value::text::integer FROM system_settings WHERE namespace='sales_intelligence' AND key='risk_threshold'),40)`).Scan(&threshold)
	page, err := s.CRM.ListOpportunities(ctx, p, crm.OpportunityFilter{Status: "OPEN", Limit: 200})
	if err != nil {
		return CoachingDashboard{}, err
	}
	type aggregate struct {
		item        CoachingOwner
		healthTotal int
	}
	owners := map[string]*aggregate{}
	attention := []DealHealth{}
	for _, opp := range page.Items {
		health, healthErr := s.DealHealth(ctx, p, opp.ID)
		if healthErr != nil {
			return CoachingDashboard{}, healthErr
		}
		a := owners[opp.OwnerID]
		if a == nil {
			a = &aggregate{item: CoachingOwner{OwnerID: opp.OwnerID, OwnerName: opp.OwnerName}}
			owners[opp.OwnerID] = a
		}
		a.item.OpenDeals++
		a.item.Pipeline += opp.BaseExpectedAmount
		a.item.Weighted += opp.BaseWeightedAmount
		a.healthTotal += health.HealthScore
		if health.RiskScore >= threshold {
			a.item.AtRiskDeals++
			attention = append(attention, health)
		}
		for _, factor := range health.Factors {
			if factor.Code == "NO_NEXT_ACTION" {
				a.item.NoNextAction++
			}
			if factor.Code == "STAGE_STALLED" {
				a.item.StalledDeals++
			}
		}
	}
	ownerList := []CoachingOwner{}
	for _, a := range owners {
		if a.item.OpenDeals > 0 {
			a.item.AverageHealth = float64(a.healthTotal) / float64(a.item.OpenDeals)
		}
		ownerList = append(ownerList, a.item)
	}
	sort.Slice(ownerList, func(i, j int) bool {
		if ownerList[i].AtRiskDeals == ownerList[j].AtRiskDeals {
			return ownerList[i].OwnerName < ownerList[j].OwnerName
		}
		return ownerList[i].AtRiskDeals > ownerList[j].AtRiskDeals
	})
	sort.Slice(attention, func(i, j int) bool { return attention[i].RiskScore > attention[j].RiskScore })
	if len(attention) > 25 {
		attention = attention[:25]
	}
	return CoachingDashboard{RiskThreshold: threshold, Attention: attention, Owners: ownerList, GeneratedAt: time.Now().UTC()}, nil
}
