package relationship

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5"
)

func normalizedStrings(items []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

func validWhiteSpaceStatus(value string) bool {
	switch value {
	case "NOT_OFFERED", "DISCOVERY", "OPPORTUNITY", "CUSTOMER", "NOT_APPLICABLE":
		return true
	default:
		return false
	}
}

func normalizeWhiteSpaces(items []WhiteSpace) ([]WhiteSpace, error) {
	out := []WhiteSpace{}
	for _, item := range items {
		item.ProductName = strings.TrimSpace(item.ProductName)
		item.Status = strings.ToUpper(strings.TrimSpace(item.Status))
		item.Notes = strings.TrimSpace(item.Notes)
		if item.ProductName == "" {
			return nil, errors.New("white space productName is required")
		}
		if item.Status == "" {
			item.Status = "NOT_OFFERED"
		}
		if !validWhiteSpaceStatus(item.Status) {
			return nil, errors.New("invalid white space status")
		}
		if item.PotentialAmount < 0 {
			return nil, errors.New("white space potentialAmount cannot be negative")
		}
		if item.ID == "" {
			item.ID = ids.New()
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) planYear(ctx context.Context, requested int) int {
	if requested >= 2000 && requested <= 2200 {
		return requested
	}
	configured := 0
	_ = s.DB.QueryRow(ctx, `SELECT COALESCE((SELECT value::text::integer FROM system_settings WHERE namespace='relationship_intelligence' AND key='default_plan_year'),0)`).Scan(&configured)
	if configured >= 2000 && configured <= 2200 {
		return configured
	}
	return time.Now().Year()
}

func (s *Service) GetAccountPlan(ctx context.Context, p *auth.Principal, customerID string, requestedYear int) (AccountPlan, error) {
	customer, err := s.CRM.GetCustomer(ctx, p, customerID)
	if err != nil {
		return AccountPlan{}, err
	}
	year := s.planYear(ctx, requestedYear)
	out := AccountPlan{CustomerID: customerID, CustomerName: customer.Name, PlanYear: year, Status: "DRAFT", CustomerGoals: []string{}, StrategicInitiatives: []string{}, OurObjectives: []string{}, WhiteSpaces: []WhiteSpace{}, Competitors: []string{}, Risks: []string{}}
	var goalsRaw, initiativesRaw, objectivesRaw, whiteSpacesRaw, competitorsRaw, risksRaw []byte
	err = s.DB.QueryRow(ctx, `SELECT p.id,p.status,COALESCE(p.strategy,''),p.customer_goals,p.strategic_initiatives,p.our_objectives,p.white_spaces,p.competitors,p.risks,p.target_revenue,p.potential_revenue,p.owner_id,u.display_name,p.version,p.created_at,p.updated_at FROM account_plans p JOIN users u ON u.id=p.owner_id WHERE p.customer_id=$1 AND p.plan_year=$2`, customerID, year).Scan(&out.ID, &out.Status, &out.Strategy, &goalsRaw, &initiativesRaw, &objectivesRaw, &whiteSpacesRaw, &competitorsRaw, &risksRaw, &out.TargetRevenue, &out.PotentialRevenue, &out.OwnerID, &out.OwnerName, &out.Version, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return AccountPlan{}, err
	}
	_ = json.Unmarshal(goalsRaw, &out.CustomerGoals)
	_ = json.Unmarshal(initiativesRaw, &out.StrategicInitiatives)
	_ = json.Unmarshal(objectivesRaw, &out.OurObjectives)
	_ = json.Unmarshal(whiteSpacesRaw, &out.WhiteSpaces)
	_ = json.Unmarshal(competitorsRaw, &out.Competitors)
	_ = json.Unmarshal(risksRaw, &out.Risks)
	return out, nil
}

func (s *Service) SaveAccountPlan(ctx context.Context, p *auth.Principal, customerID string, input AccountPlanInput, meta crm.RequestMeta) (AccountPlan, error) {
	if err := auth.Require(p, "customer:write"); err != nil {
		return AccountPlan{}, err
	}
	customer, err := s.CRM.GetCustomer(ctx, p, customerID)
	if err != nil {
		return AccountPlan{}, err
	}
	input.PlanYear = s.planYear(ctx, input.PlanYear)
	input.Status = strings.ToUpper(strings.TrimSpace(input.Status))
	if input.Status == "" {
		input.Status = "DRAFT"
	}
	if input.Status != "DRAFT" && input.Status != "ACTIVE" && input.Status != "ARCHIVED" {
		return AccountPlan{}, errors.New("invalid account plan status")
	}
	if input.TargetRevenue < 0 || input.PotentialRevenue < 0 {
		return AccountPlan{}, errors.New("revenue values cannot be negative")
	}
	input.CustomerGoals = normalizedStrings(input.CustomerGoals)
	input.StrategicInitiatives = normalizedStrings(input.StrategicInitiatives)
	input.OurObjectives = normalizedStrings(input.OurObjectives)
	input.Competitors = normalizedStrings(input.Competitors)
	input.Risks = normalizedStrings(input.Risks)
	input.WhiteSpaces, err = normalizeWhiteSpaces(input.WhiteSpaces)
	if err != nil {
		return AccountPlan{}, err
	}
	current, err := s.GetAccountPlan(ctx, p, customerID, input.PlanYear)
	if err != nil {
		return AccountPlan{}, err
	}
	goals, _ := json.Marshal(input.CustomerGoals)
	initiatives, _ := json.Marshal(input.StrategicInitiatives)
	objectives, _ := json.Marshal(input.OurObjectives)
	whiteSpaces, _ := json.Marshal(input.WhiteSpaces)
	competitors, _ := json.Marshal(input.Competitors)
	risks, _ := json.Marshal(input.Risks)
	if current.ID == "" {
		if input.Version != 0 {
			return AccountPlan{}, errors.New("new account plan version must be 0")
		}
		_, err = s.DB.Exec(ctx, `INSERT INTO account_plans(id,customer_id,plan_year,status,strategy,customer_goals,strategic_initiatives,our_objectives,white_spaces,competitors,risks,target_revenue,potential_revenue,owner_id,organization_id,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16)`, ids.New(), customerID, input.PlanYear, input.Status, strings.TrimSpace(input.Strategy), goals, initiatives, objectives, whiteSpaces, competitors, risks, input.TargetRevenue, input.PotentialRevenue, customer.OwnerID, func() any {
			if customer.OrganizationID == "" {
				return nil
			}
			return customer.OrganizationID
		}(), p.UserID)
	} else {
		tag, updateErr := s.DB.Exec(ctx, `UPDATE account_plans SET status=$1,strategy=$2,customer_goals=$3,strategic_initiatives=$4,our_objectives=$5,white_spaces=$6,competitors=$7,risks=$8,target_revenue=$9,potential_revenue=$10,updated_by=$11,updated_at=now(),version=version+1 WHERE id=$12 AND version=$13`, input.Status, strings.TrimSpace(input.Strategy), goals, initiatives, objectives, whiteSpaces, competitors, risks, input.TargetRevenue, input.PotentialRevenue, p.UserID, current.ID, input.Version)
		err = updateErr
		if err == nil && tag.RowsAffected() != 1 {
			return AccountPlan{}, errors.New("account plan was changed by another user")
		}
	}
	if err != nil {
		return AccountPlan{}, err
	}
	after, err := s.GetAccountPlan(ctx, p, customerID, input.PlanYear)
	if err != nil {
		return AccountPlan{}, err
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: meta.Channel, Action: "SAVE_ACCOUNT_PLAN", Resource: "account_plan", ResourceID: after.ID, Before: func() any {
		if current.ID == "" {
			return nil
		}
		return current
	}(), After: after, IP: meta.IP, RequestID: meta.RequestID, UserAgent: meta.UserAgent})
	return after, nil
}

func (s *Service) CrossSellOpportunities(ctx context.Context, p *auth.Principal, customerID string, year int) ([]WhiteSpace, error) {
	plan, err := s.GetAccountPlan(ctx, p, customerID, year)
	if err != nil {
		return nil, err
	}
	out := []WhiteSpace{}
	for _, item := range plan.WhiteSpaces {
		if item.Status == "NOT_OFFERED" || item.Status == "DISCOVERY" {
			out = append(out, item)
		}
	}
	return out, nil
}

func (s *Service) AccountBrief(ctx context.Context, p *auth.Principal, customerID string, year int) (AccountBrief, error) {
	customer360, err := s.CRM.Customer360(ctx, p, customerID)
	if err != nil {
		return AccountBrief{}, err
	}
	graph, err := s.Graph(ctx, p, customerID)
	if err != nil {
		return AccountBrief{}, err
	}
	plan, err := s.GetAccountPlan(ctx, p, customerID, year)
	if err != nil {
		return AccountBrief{}, err
	}
	return AccountBrief{Customer360: customer360, Relationships: graph, AccountPlan: plan, GeneratedAt: time.Now().UTC()}, nil
}
