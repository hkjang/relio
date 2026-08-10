package crm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/ids"
)

func (s *Service) SearchContacts(ctx context.Context, p *auth.Principal, q, customerID string, limit int) ([]Contact, error) {
	if err := auth.Require(p, "contact:read"); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx, `SELECT ct.id,ct.customer_id,ct.name,COALESCE(ct.title,''),COALESCE(ct.department,''),COALESCE(ct.email,''),COALESCE(ct.phone,''),COALESCE(ct.mobile,''),ct.decision_maker,ct.primary_contact,ct.relationship_role,ct.influence,ct.sentiment,ct.relationship_strength,ct.decision_power,ct.last_contact_at,ct.owner_id,ct.created_at FROM contacts ct JOIN customers c ON c.id=ct.customer_id WHERE `+scopeSQL("c")+` AND ($4='' OR ct.customer_id::text=$4) AND ($5='' OR lower(ct.name) LIKE '%'||lower($5)||'%' OR lower(COALESCE(ct.email,'')) LIKE '%'||lower($5)||'%') ORDER BY ct.decision_maker DESC,(ct.relationship_role='CHAMPION') DESC,ct.primary_contact DESC,ct.name LIMIT $6`, p.DataScope, p.UserID, nullable(p.OrganizationID), customerID, strings.TrimSpace(q), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Contact{}
	for rows.Next() {
		var x Contact
		if err = rows.Scan(&x.ID, &x.CustomerID, &x.Name, &x.Title, &x.Department, &x.Email, &x.Phone, &x.Mobile, &x.DecisionMaker, &x.PrimaryContact, &x.RelationshipRole, &x.Influence, &x.Sentiment, &x.RelationshipStrength, &x.DecisionPower, &x.LastContactAt, &x.OwnerID, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type ContactInput struct {
	CustomerID           string `json:"customerId"`
	Name                 string `json:"name"`
	Title                string `json:"title"`
	Department           string `json:"department"`
	Email                string `json:"email"`
	Phone                string `json:"phone"`
	Mobile               string `json:"mobile"`
	DecisionMaker        bool   `json:"decisionMaker"`
	PrimaryContact       bool   `json:"primaryContact"`
	RelationshipRole     string `json:"relationshipRole"`
	Influence            string `json:"influence"`
	Sentiment            string `json:"sentiment"`
	RelationshipStrength *int   `json:"relationshipStrength"`
	DecisionPower        *int   `json:"decisionPower"`
}

func (s *Service) CreateContact(ctx context.Context, p *auth.Principal, in ContactInput, m RequestMeta) (Contact, error) {
	if err := auth.Require(p, "contact:write"); err != nil {
		return Contact{}, err
	}
	if strings.TrimSpace(in.Name) == "" || in.CustomerID == "" {
		return Contact{}, errors.New("customerId and name are required")
	}
	if _, err := s.GetCustomer(ctx, p, in.CustomerID); err != nil {
		return Contact{}, errors.New("customer not found or inaccessible")
	}
	id := ids.New()
	role := strings.ToUpper(strings.TrimSpace(in.RelationshipRole))
	if role == "" {
		role = "USER"
	}
	if role != "DECISION_MAKER" && role != "CHAMPION" && role != "INFLUENCER" && role != "USER" && role != "PROCUREMENT" {
		return Contact{}, errors.New("invalid relationshipRole")
	}
	influence := strings.ToUpper(strings.TrimSpace(in.Influence))
	if influence == "" {
		influence = "MEDIUM"
	}
	if influence != "HIGH" && influence != "MEDIUM" && influence != "LOW" {
		return Contact{}, errors.New("invalid influence")
	}
	sentiment := strings.ToUpper(strings.TrimSpace(in.Sentiment))
	if sentiment == "" {
		sentiment = "NEUTRAL"
	}
	if sentiment != "SUPPORT" && sentiment != "NEUTRAL" && sentiment != "OPPOSE" {
		return Contact{}, errors.New("invalid sentiment")
	}
	if in.RelationshipStrength != nil && (*in.RelationshipStrength < 0 || *in.RelationshipStrength > 100) {
		return Contact{}, errors.New("relationshipStrength must be between 0 and 100")
	}
	if in.DecisionPower != nil && (*in.DecisionPower < 0 || *in.DecisionPower > 100) {
		return Contact{}, errors.New("decisionPower must be between 0 and 100")
	}
	relationshipStrength := 50
	if in.RelationshipStrength != nil {
		relationshipStrength = *in.RelationshipStrength
	}
	decisionPower := 50
	if in.DecisionPower != nil {
		decisionPower = *in.DecisionPower
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO contacts(id,customer_id,name,title,department,email,phone,mobile,decision_maker,primary_contact,relationship_role,influence,sentiment,relationship_strength,decision_power,owner_id,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16,$16)`, id, in.CustomerID, strings.TrimSpace(in.Name), nullable(in.Title), nullable(in.Department), nullable(in.Email), nullable(in.Phone), nullable(in.Mobile), in.DecisionMaker, in.PrimaryContact, role, influence, sentiment, relationshipStrength, decisionPower, p.UserID)
	if err != nil {
		return Contact{}, err
	}
	items, err := s.SearchContacts(ctx, p, "", in.CustomerID, 200)
	if err != nil {
		return Contact{}, err
	}
	for _, x := range items {
		if x.ID == id {
			s.audit(ctx, p, m, "CREATE", "contact", id, nil, x)
			return x, nil
		}
	}
	return Contact{}, errors.New("created contact not found")
}

func (s *Service) DueActions(ctx context.Context, p *auth.Principal, days, limit int) ([]map[string]any, error) {
	if err := auth.Require(p, "activity:read"); err != nil {
		return nil, err
	}
	if days < 0 || days > 365 {
		days = 7
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx, `SELECT t.id,t.title,t.due_at,t.status,t.priority,t.customer_id,t.opportunity_id,t.assignee_id FROM tasks t WHERE ($1='COMPANY' OR t.assignee_id=$2 OR ($1='TEAM' AND EXISTS(SELECT 1 FROM users scoped_user WHERE scoped_user.id=t.assignee_id AND (scoped_user.manager_id=$2 OR scoped_user.id=$2))) OR ($1 IN ('DEPARTMENT','DIVISION') AND t.organization_id=$3)) AND t.status='OPEN' AND t.due_at<=now()+make_interval(days=>$4) ORDER BY t.due_at LIMIT $5`, p.DataScope, p.UserID, nullable(p.OrganizationID), days, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, title, status, priority, assignee string
		var due *time.Time
		var customer, opp *string
		if err = rows.Scan(&id, &title, &due, &status, &priority, &customer, &opp, &assignee); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "title": title, "dueAt": due, "status": status, "priority": priority, "customerId": customer, "opportunityId": opp, "assigneeId": assignee})
	}
	return out, rows.Err()
}

func (s *Service) Contracts(ctx context.Context, p *auth.Principal, customerID string, expiringDays int, renewalOnly bool, limit int) ([]map[string]any, error) {
	if err := auth.Require(p, "contract:read"); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if expiringDays < 0 || expiringDays > 3650 {
		expiringDays = 0
	}
	rows, err := s.DB.Query(ctx, `SELECT c.id,c.contract_no,c.customer_id,cu.name,c.opportunity_id,c.title,c.amount,c.currency_code,c.exchange_rate,c.base_amount,c.start_date,c.end_date,c.status,CASE WHEN c.status='ACTIVE' AND c.end_date<current_date THEN 'EXPIRED' WHEN c.status='ACTIVE' AND c.end_date<=current_date+c.renewal_notice_days THEN 'EXPIRING' ELSE c.status END,c.auto_renew,c.revenue_schedule_type,c.renewal_notice_days,c.renewal_status,COALESCE(c.renewal_action,''),c.activated_at,c.owner_id,c.version,c.created_at,c.updated_at,CASE WHEN c.end_date IS NULL THEN NULL ELSE c.end_date-current_date END,COALESCE(rs.schedule_count,0),COALESCE(rs.recognized_count,0),COALESCE(rs.recognized_base_amount,0) FROM contracts c JOIN customers cu ON cu.id=c.customer_id LEFT JOIN LATERAL (SELECT count(*) schedule_count,count(*) FILTER(WHERE status='RECOGNIZED') recognized_count,COALESCE(sum(base_amount) FILTER(WHERE status='RECOGNIZED'),0) recognized_base_amount FROM revenue_schedules WHERE contract_id=c.id) rs ON true WHERE `+scopeSQL("c")+` AND ($4='' OR c.customer_id::text=$4) AND ($5=0 OR c.end_date BETWEEN current_date AND current_date+$5) AND (NOT $6 OR c.auto_renew=true) ORDER BY c.end_date NULLS LAST LIMIT $7`, p.DataScope, p.UserID, nullable(p.OrganizationID), customerID, expiringDays, renewalOnly, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, no, cid, cname, title, currency, status, radarStatus, scheduleType, renewalStatus, renewalAction, owner string
		var opportunity *string
		var amount, rate, baseAmount, recognizedBaseAmount float64
		var start, end *time.Time
		var activated *time.Time
		var renew bool
		var version, renewalNotice, scheduleCount, recognizedCount int
		var daysToExpiry *int
		var created, updated time.Time
		if err = rows.Scan(&id, &no, &cid, &cname, &opportunity, &title, &amount, &currency, &rate, &baseAmount, &start, &end, &status, &radarStatus, &renew, &scheduleType, &renewalNotice, &renewalStatus, &renewalAction, &activated, &owner, &version, &created, &updated, &daysToExpiry, &scheduleCount, &recognizedCount, &recognizedBaseAmount); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "contractNo": no, "customerId": cid, "customerName": cname, "opportunityId": opportunity, "title": title, "amount": amount, "currencyCode": currency, "exchangeRate": rate, "baseAmount": baseAmount, "startDate": start, "endDate": end, "status": status, "radarStatus": radarStatus, "daysToExpiry": daysToExpiry, "autoRenew": renew, "revenueScheduleType": scheduleType, "scheduleCount": scheduleCount, "recognizedCount": recognizedCount, "recognizedBaseAmount": recognizedBaseAmount, "renewalNoticeDays": renewalNotice, "renewalStatus": renewalStatus, "renewalAction": renewalAction, "activatedAt": activated, "ownerId": owner, "version": version, "createdAt": created, "updatedAt": updated})
	}
	return out, rows.Err()
}

type QuotationInput struct {
	CustomerID      string         `json:"customerId"`
	OpportunityID   string         `json:"opportunityId"`
	Title           string         `json:"title"`
	Amount          float64        `json:"amount"`
	CurrencyCode    string         `json:"currencyCode"`
	ExchangeRate    *float64       `json:"exchangeRate"`
	DiscountPercent float64        `json:"discountPercent"`
	ValidUntil      *string        `json:"validUntil"`
	CustomFields    map[string]any `json:"customFields"`
}

func (s *Service) CreateQuotation(ctx context.Context, p *auth.Principal, in QuotationInput, m RequestMeta) (map[string]any, error) {
	if err := auth.Require(p, "quotation:write"); err != nil {
		return nil, err
	}
	if in.CustomerID == "" || strings.TrimSpace(in.Title) == "" {
		return nil, errors.New("customerId and title are required")
	}
	if in.Amount < 0 || in.DiscountPercent < 0 || in.DiscountPercent > 100 {
		return nil, errors.New("amount or discountPercent is invalid")
	}
	if _, err := s.GetCustomer(ctx, p, in.CustomerID); err != nil {
		return nil, errors.New("customer not found or inaccessible")
	}
	currency := strings.ToUpper(strings.TrimSpace(in.CurrencyCode))
	if currency == "" {
		currency = baseCurrencyCode
	}
	rate := 1.0
	if in.ExchangeRate != nil {
		rate = *in.ExchangeRate
	} else if currency != baseCurrencyCode {
		return nil, errors.New("exchangeRate is required for non-KRW quotations")
	}
	if err := validateCurrency(currency, rate); err != nil {
		return nil, err
	}
	valid, err := dateValue(in.ValidUntil)
	if err != nil {
		return nil, err
	}
	id := ids.New()
	no := "Q-" + time.Now().Format("20060102") + "-" + strings.ToUpper(ids.Token(4))
	_, err = s.DB.Exec(ctx, `INSERT INTO quotations(id,quotation_no,customer_id,opportunity_id,owner_id,organization_id,title,amount,currency_code,exchange_rate,discount_percent,valid_until,custom_fields,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$5,$5)`, id, no, in.CustomerID, nullable(in.OpportunityID), p.UserID, nullable(p.OrganizationID), strings.TrimSpace(in.Title), in.Amount, currency, rate, in.DiscountPercent, valid, jsonValue(in.CustomFields))
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": id, "quotationNo": no, "customerId": in.CustomerID, "opportunityId": in.OpportunityID, "title": in.Title, "amount": in.Amount, "currencyCode": currency, "exchangeRate": rate, "baseAmount": in.Amount * rate, "discountPercent": in.DiscountPercent, "validUntil": valid, "status": "DRAFT", "version": 1}
	_, _ = s.DB.Exec(ctx, `INSERT INTO quotation_versions(id,quotation_id,version_no,snapshot,created_by) VALUES($1,$2,1,$3,$4)`, ids.New(), id, mustJSON(out), p.UserID)
	s.audit(ctx, p, m, "CREATE", "quotation", id, nil, out)
	return out, nil
}

func (s *Service) ListQuotations(ctx context.Context, p *auth.Principal, customerID string, limit int) ([]map[string]any, error) {
	if err := auth.Require(p, "quotation:read"); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx, `SELECT q.id,q.quotation_no,q.customer_id,c.name,q.opportunity_id,q.title,q.amount,q.currency_code,q.exchange_rate,q.base_amount,q.discount_percent,q.status,q.valid_until,q.owner_id,q.version,q.created_at,q.updated_at FROM quotations q JOIN customers c ON c.id=q.customer_id WHERE `+scopeSQL("q")+` AND ($4='' OR q.customer_id::text=$4) ORDER BY q.updated_at DESC LIMIT $5`, p.DataScope, p.UserID, nullable(p.OrganizationID), customerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, no, cid, cname, title, currency, status, owner string
		var opp *string
		var amount, rate, base, discount float64
		var valid *time.Time
		var version int
		var created, updated time.Time
		if err = rows.Scan(&id, &no, &cid, &cname, &opp, &title, &amount, &currency, &rate, &base, &discount, &status, &valid, &owner, &version, &created, &updated); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "quotationNo": no, "customerId": cid, "customerName": cname, "opportunityId": opp, "title": title, "amount": amount, "currencyCode": currency, "exchangeRate": rate, "baseAmount": base, "discountPercent": discount, "status": status, "validUntil": valid, "ownerId": owner, "version": version, "createdAt": created, "updatedAt": updated})
	}
	return out, rows.Err()
}

func (s *Service) SalesKPI(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	if err := auth.Require(p, "sales:read"); err != nil {
		return nil, err
	}
	var revenue, target float64
	_ = s.DB.QueryRow(ctx, `SELECT COALESCE(sum(base_amount),0) FROM sales s WHERE recognized_date>=date_trunc('month',now())::date AND `+scopeSQL("s"), p.DataScope, p.UserID, nullable(p.OrganizationID)).Scan(&revenue)
	_ = s.DB.QueryRow(ctx, `SELECT COALESCE(sum(amount),0) FROM targets t WHERE current_date BETWEEN period_start AND period_end AND ($1='COMPANY' OR t.user_id=$2 OR ($1 IN ('DEPARTMENT','DIVISION') AND t.organization_id=$3) OR ($1='TEAM' AND (t.user_id=$2 OR t.user_id IN (SELECT id FROM users WHERE manager_id=$2))))`, p.DataScope, p.UserID, nullable(p.OrganizationID)).Scan(&target)
	forecast, err := s.Forecast(ctx, p)
	if err != nil {
		return nil, err
	}
	attainment := 0.0
	if target > 0 {
		attainment = revenue / target * 100
	}
	return map[string]any{"revenueThisMonth": revenue, "target": target, "attainmentPercent": attainment, "forecast": forecast}, nil
}

func (s *Service) WinLossAnalysis(ctx context.Context, p *auth.Principal, months int) (map[string]any, error) {
	if err := auth.Require(p, "forecast:read"); err != nil {
		return nil, err
	}
	if months < 1 || months > 60 {
		months = 12
	}
	rows, err := s.DB.Query(ctx, `SELECT status,count(*),COALESCE(sum(base_expected_amount),0) FROM opportunities o WHERE status IN ('WON','LOST') AND updated_at>=now()-make_interval(months=>$4) AND `+scopeSQL("o")+` GROUP BY status`, p.DataScope, p.UserID, nullable(p.OrganizationID), months)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	won, lost := 0, 0
	wonAmount, lostAmount := 0.0, 0.0
	for rows.Next() {
		var status string
		var count int
		var amount float64
		if err = rows.Scan(&status, &count, &amount); err != nil {
			return nil, err
		}
		if status == "WON" {
			won, wonAmount = count, amount
		} else {
			lost, lostAmount = count, amount
		}
	}
	rate := 0.0
	if won+lost > 0 {
		rate = float64(won) / float64(won+lost) * 100
	}
	return map[string]any{"wonCount": won, "lostCount": lost, "wonAmount": wonAmount, "lostAmount": lostAmount, "winRate": rate, "months": months}, rows.Err()
}

func (s *Service) GlobalSearch(ctx context.Context, p *auth.Principal, q string, limit int) (map[string]any, error) {
	if strings.TrimSpace(q) == "" {
		return map[string]any{"customers": []Customer{}, "opportunities": []Opportunity{}}, nil
	}
	customers, err := s.ListCustomers(ctx, p, q, "", "", limit)
	if err != nil {
		return nil, err
	}
	opps, err := s.ListOpportunities(ctx, p, OpportunityFilter{Query: q, Limit: limit})
	if err != nil {
		return nil, err
	}
	contacts := []Contact{}
	if p.Has("contact:read") {
		contacts, _ = s.SearchContacts(ctx, p, q, "", limit)
	}
	return map[string]any{"customers": customers.Items, "opportunities": opps.Items, "contacts": contacts}, nil
}

func formatResourceID(entity, id string) string { return fmt.Sprintf("%s:%s", entity, id) }
