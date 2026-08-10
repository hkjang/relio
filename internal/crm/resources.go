package crm

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/ids"
)

type LeadInput struct {
	Name         string         `json:"name"`
	Company      string         `json:"company"`
	Email        string         `json:"email"`
	Phone        string         `json:"phone"`
	Source       string         `json:"source"`
	Status       string         `json:"status"`
	CustomFields map[string]any `json:"customFields"`
}

func (s *Service) ListLeads(ctx context.Context, p *auth.Principal, q string, limit int) ([]map[string]any, error) {
	if err := auth.Require(p, "lead:read"); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx, `SELECT l.id,l.name,COALESCE(l.company,''),COALESCE(l.email,''),COALESCE(l.phone,''),COALESCE(l.source,''),l.status,l.owner_id,u.display_name,l.version,l.created_at,l.updated_at FROM leads l JOIN users u ON u.id=l.owner_id WHERE `+scopeSQL("l")+` AND ($4='' OR lower(l.name) LIKE '%'||lower($4)||'%' OR lower(COALESCE(l.company,'')) LIKE '%'||lower($4)||'%') ORDER BY l.updated_at DESC LIMIT $5`, p.DataScope, p.UserID, nullable(p.OrganizationID), strings.TrimSpace(q), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, company, email, phone, source, status, owner, ownerName string
		var version int
		var created, updated time.Time
		if err = rows.Scan(&id, &name, &company, &email, &phone, &source, &status, &owner, &ownerName, &version, &created, &updated); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "name": name, "company": company, "email": email, "phone": phone, "source": source, "status": status, "ownerId": owner, "ownerName": ownerName, "version": version, "createdAt": created, "updatedAt": updated})
	}
	return out, rows.Err()
}
func (s *Service) CreateLead(ctx context.Context, p *auth.Principal, in LeadInput, m RequestMeta) (map[string]any, error) {
	if err := auth.Require(p, "lead:write"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, errors.New("lead name is required")
	}
	if in.Status == "" {
		in.Status = "NEW"
	}
	id := ids.New()
	_, err := s.DB.Exec(ctx, `INSERT INTO leads(id,name,company,email,phone,source,status,owner_id,organization_id,custom_fields,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$8,$8)`, id, strings.TrimSpace(in.Name), nullable(in.Company), nullable(in.Email), nullable(in.Phone), nullable(in.Source), strings.ToUpper(in.Status), p.UserID, nullable(p.OrganizationID), jsonValue(in.CustomFields))
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": id, "name": in.Name, "company": in.Company, "status": strings.ToUpper(in.Status), "ownerId": p.UserID, "version": 1}
	s.audit(ctx, p, m, "CREATE", "lead", id, nil, out)
	return out, nil
}

type ProductInput struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	UnitPrice   float64 `json:"unitPrice"`
	Active      *bool   `json:"active"`
}

func (s *Service) ListProducts(ctx context.Context, p *auth.Principal, q string, limit int) ([]map[string]any, error) {
	if err := auth.Require(p, "product:read"); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.Query(ctx, `SELECT id,code,name,COALESCE(description,''),unit_price,active,created_at,updated_at FROM products WHERE ($1='' OR lower(name) LIKE '%'||lower($1)||'%' OR lower(code) LIKE '%'||lower($1)||'%') ORDER BY active DESC,name LIMIT $2`, strings.TrimSpace(q), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, code, name, description string
		var price float64
		var active bool
		var created, updated time.Time
		if err = rows.Scan(&id, &code, &name, &description, &price, &active, &created, &updated); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "code": code, "name": name, "description": description, "unitPrice": price, "active": active, "createdAt": created, "updatedAt": updated})
	}
	return out, rows.Err()
}
func (s *Service) CreateProduct(ctx context.Context, p *auth.Principal, in ProductInput, m RequestMeta) (map[string]any, error) {
	if err := auth.Require(p, "product:write"); err != nil {
		return nil, err
	}
	if in.Code == "" || in.Name == "" || in.UnitPrice < 0 {
		return nil, errors.New("valid code, name and unitPrice are required")
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	id := ids.New()
	_, err := s.DB.Exec(ctx, `INSERT INTO products(id,code,name,description,unit_price,active) VALUES($1,$2,$3,$4,$5,$6)`, id, strings.ToUpper(in.Code), in.Name, nullable(in.Description), in.UnitPrice, active)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": id, "code": strings.ToUpper(in.Code), "name": in.Name, "unitPrice": in.UnitPrice, "active": active}
	s.audit(ctx, p, m, "CREATE", "product", id, nil, out)
	return out, nil
}

type SaleInput struct {
	CustomerID     string   `json:"customerId"`
	ContractID     string   `json:"contractId"`
	Amount         float64  `json:"amount"`
	CurrencyCode   string   `json:"currencyCode"`
	ExchangeRate   *float64 `json:"exchangeRate"`
	RecognizedDate string   `json:"recognizedDate"`
	Description    string   `json:"description"`
}

func (s *Service) ListSales(ctx context.Context, p *auth.Principal, limit int) ([]map[string]any, error) {
	if err := auth.Require(p, "sales:read"); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.Query(ctx, `SELECT s.id,s.customer_id,c.name,s.contract_id,s.amount,s.currency_code,s.exchange_rate,s.base_amount,s.recognized_date,COALESCE(s.description,''),s.owner_id,s.created_at FROM sales s JOIN customers c ON c.id=s.customer_id WHERE `+scopeSQL("s")+` ORDER BY s.recognized_date DESC LIMIT $4`, p.DataScope, p.UserID, nullable(p.OrganizationID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, cid, cname, currency, description, owner string
		var contract *string
		var amount, rate, base float64
		var recognized, created time.Time
		if err = rows.Scan(&id, &cid, &cname, &contract, &amount, &currency, &rate, &base, &recognized, &description, &owner, &created); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "customerId": cid, "customerName": cname, "contractId": contract, "amount": amount, "currencyCode": currency, "exchangeRate": rate, "baseAmount": base, "recognizedDate": recognized, "description": description, "ownerId": owner, "createdAt": created})
	}
	return out, rows.Err()
}
func (s *Service) CreateSale(ctx context.Context, p *auth.Principal, in SaleInput, m RequestMeta) (map[string]any, error) {
	if err := auth.Require(p, "sales:write"); err != nil {
		return nil, err
	}
	if in.CustomerID == "" || in.Amount < 0 {
		return nil, errors.New("customerId and valid amount are required")
	}
	recognized, err := time.Parse("2006-01-02", in.RecognizedDate)
	if err != nil {
		return nil, errors.New("recognizedDate must use YYYY-MM-DD")
	}
	if _, err = s.GetCustomer(ctx, p, in.CustomerID); err != nil {
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
		return nil, errors.New("exchangeRate is required for non-KRW sales")
	}
	if err = validateCurrency(currency, rate); err != nil {
		return nil, err
	}
	id := ids.New()
	_, err = s.DB.Exec(ctx, `INSERT INTO sales(id,customer_id,contract_id,owner_id,organization_id,amount,currency_code,exchange_rate,recognized_date,description,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$4)`, id, in.CustomerID, nullable(in.ContractID), p.UserID, nullable(p.OrganizationID), in.Amount, currency, rate, recognized, nullable(in.Description))
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": id, "customerId": in.CustomerID, "amount": in.Amount, "currencyCode": currency, "exchangeRate": rate, "baseAmount": in.Amount * rate, "recognizedDate": recognized}
	s.audit(ctx, p, m, "CREATE", "sale", id, nil, out)
	return out, nil
}

type TargetInput struct {
	UserID         string  `json:"userId"`
	OrganizationID string  `json:"organizationId"`
	PeriodStart    string  `json:"periodStart"`
	PeriodEnd      string  `json:"periodEnd"`
	Amount         float64 `json:"amount"`
}

func (s *Service) ListTargets(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	if err := auth.Require(p, "target:read"); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT t.id,t.user_id,COALESCE(u.display_name,''),t.organization_id,t.period_start,t.period_end,t.amount,t.created_at FROM targets t LEFT JOIN users u ON u.id=t.user_id WHERE ($1='COMPANY' OR t.user_id=$2 OR ($1='TEAM' AND (t.user_id=$2 OR u.manager_id=$2)) OR ($1 IN ('DEPARTMENT','DIVISION') AND t.organization_id=$3)) ORDER BY t.period_start DESC`, p.DataScope, p.UserID, nullable(p.OrganizationID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var userID, orgID *string
		var start, end, created time.Time
		var amount float64
		if err = rows.Scan(&id, &userID, &name, &orgID, &start, &end, &amount, &created); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "userId": userID, "userName": name, "organizationId": orgID, "periodStart": start, "periodEnd": end, "amount": amount, "createdAt": created})
	}
	return out, rows.Err()
}
func (s *Service) CreateTarget(ctx context.Context, p *auth.Principal, in TargetInput, m RequestMeta) (map[string]any, error) {
	if err := auth.Require(p, "target:write"); err != nil {
		return nil, err
	}
	start, err := time.Parse("2006-01-02", in.PeriodStart)
	if err != nil {
		return nil, errors.New("periodStart must use YYYY-MM-DD")
	}
	end, err := time.Parse("2006-01-02", in.PeriodEnd)
	if err != nil || end.Before(start) {
		return nil, errors.New("periodEnd is invalid")
	}
	if in.UserID == "" && in.OrganizationID == "" {
		in.UserID = p.UserID
	}
	id := ids.New()
	_, err = s.DB.Exec(ctx, `INSERT INTO targets(id,user_id,organization_id,period_start,period_end,amount,created_by) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, nullable(in.UserID), nullable(in.OrganizationID), start, end, in.Amount, p.UserID)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": id, "userId": in.UserID, "organizationId": in.OrganizationID, "periodStart": start, "periodEnd": end, "amount": in.Amount}
	s.audit(ctx, p, m, "CREATE", "target", id, nil, out)
	return out, nil
}

func (s *Service) Notifications(ctx context.Context, p *auth.Principal, unreadOnly bool, limit int) ([]map[string]any, error) {
	if err := auth.Require(p, "notification:read"); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, err := s.DB.Query(ctx, `SELECT id,notification_type,title,COALESCE(body,''),COALESCE(resource_type,''),resource_id,read_at,created_at FROM notifications WHERE user_id=$1 AND (NOT $2 OR read_at IS NULL) ORDER BY created_at DESC LIMIT $3`, p.UserID, unreadOnly, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, typ, title, body, resource string
		var resourceID *string
		var read *time.Time
		var created time.Time
		if err = rows.Scan(&id, &typ, &title, &body, &resource, &resourceID, &read, &created); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "type": typ, "title": title, "body": body, "resourceType": resource, "resourceId": resourceID, "readAt": read, "createdAt": created})
	}
	return out, rows.Err()
}
func (s *Service) ReadNotification(ctx context.Context, p *auth.Principal, id string, m RequestMeta) error {
	if err := auth.Require(p, "notification:write"); err != nil {
		return err
	}
	cmd, err := s.DB.Exec(ctx, `UPDATE notifications SET read_at=COALESCE(read_at,now()) WHERE id=$1 AND user_id=$2`, id, p.UserID)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return errors.New("notification not found")
	}
	s.audit(ctx, p, m, "READ", "notification", id, nil, map[string]any{"read": true})
	return nil
}
