package crm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	DB    *pgxpool.Pool
	Audit *audit.Service
}

type RequestMeta struct{ Channel, IP, RequestID, UserAgent string }

func (s *Service) audit(ctx context.Context, p *auth.Principal, m RequestMeta, action, resource, id string, before, after any) {
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: m.Channel, Action: action, Resource: resource, ResourceID: id, Before: before, After: after, IP: m.IP, RequestID: m.RequestID, UserAgent: m.UserAgent})
}

func pageOffset(cursor string) int {
	if cursor == "" {
		return 0
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0
	}
	parts := strings.Split(string(b), ":")
	if len(parts) != 2 || parts[0] != "offset" {
		return 0
	}
	n, _ := strconv.Atoi(parts[1])
	if n < 0 {
		return 0
	}
	return n
}
func nextCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("offset:%d", offset)))
}
func nullable(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return strings.TrimSpace(v)
}
func jsonValue(v map[string]any) []byte {
	if v == nil {
		v = map[string]any{}
	}
	b, _ := json.Marshal(v)
	return b
}
func dateValue(v *string) (any, error) {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil, nil
	}
	d, err := time.Parse("2006-01-02", *v)
	if err != nil {
		return nil, errors.New("date must use YYYY-MM-DD format")
	}
	return d, nil
}

// scopeSQL centralizes the data-scope rule shared by REST, MCP, dashboards and exports.
func scopeSQL(alias string) string {
	return fmt.Sprintf(`($1='COMPANY'
		OR %s.owner_id=$2
		OR ($1='TEAM' AND EXISTS(
			SELECT 1 FROM users scoped_user
			WHERE scoped_user.id=%s.owner_id AND (scoped_user.manager_id=$2 OR scoped_user.id=$2)
		))
		OR ($1 IN ('DEPARTMENT','DIVISION') AND EXISTS(
			WITH RECURSIVE user_path AS (
				SELECT id,parent_id,org_type,0 AS depth FROM organizations WHERE id=$3::uuid
				UNION ALL
				SELECT parent.id,parent.parent_id,parent.org_type,user_path.depth+1
				FROM organizations parent JOIN user_path ON parent.id=user_path.parent_id
			), scope_root AS (
				SELECT COALESCE((SELECT id FROM user_path WHERE org_type=$1 ORDER BY depth LIMIT 1),$3::uuid) AS id
			), scope_tree AS (
				SELECT id FROM scope_root
				UNION ALL
				SELECT child.id FROM organizations child JOIN scope_tree ON child.parent_id=scope_tree.id
			)
			SELECT 1 FROM scope_tree WHERE id=%s.organization_id
		)))`, alias, alias, alias)
}

func (s *Service) ListCustomers(ctx context.Context, p *auth.Principal, q, cursor, sortBy string, limit int) (Page[Customer], error) {
	if err := auth.Require(p, "customer:read"); err != nil {
		return Page[Customer]{}, err
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset := pageOffset(cursor)
	orders := map[string]string{"name": "c.name ASC,c.id", "-name": "c.name DESC,c.id", "annualRevenue": "c.annual_revenue ASC NULLS LAST,c.id", "-annualRevenue": "c.annual_revenue DESC NULLS LAST,c.id", "createdAt": "c.created_at ASC,c.id", "-createdAt": "c.created_at DESC,c.id", "updatedAt": "c.updated_at ASC,c.id", "-updatedAt": "c.updated_at DESC,c.id"}
	order := orders[sortBy]
	if order == "" {
		order = "c.updated_at DESC,c.id"
	}
	query := `SELECT c.id,c.name,COALESCE(c.registration_no,''),c.customer_type,COALESCE(c.grade,''),COALESCE(c.industry,''),COALESCE(c.website,''),COALESCE(c.phone,''),COALESCE(c.email,''),COALESCE(c.address,''),c.owner_id,u.display_name,COALESCE(c.organization_id::text,''),c.health,COALESCE(c.annual_revenue,0),COALESCE(c.employee_count,0),c.custom_fields,c.version,c.created_at,c.updated_at FROM customers c JOIN users u ON u.id=c.owner_id WHERE c.active=true AND c.merged_into_id IS NULL AND ` + scopeSQL("c") + ` AND ($4='' OR lower(c.name) LIKE '%'||lower($4)||'%' OR lower(COALESCE(c.registration_no,'')) LIKE '%'||lower($4)||'%') ORDER BY ` + order + ` LIMIT $5 OFFSET $6`
	rows, err := s.DB.Query(ctx, query, p.DataScope, p.UserID, nullable(p.OrganizationID), strings.TrimSpace(q), limit+1, offset)
	if err != nil {
		return Page[Customer]{}, err
	}
	defer rows.Close()
	items := []Customer{}
	for rows.Next() {
		var x Customer
		var raw []byte
		if err = rows.Scan(&x.ID, &x.Name, &x.RegistrationNo, &x.CustomerType, &x.Grade, &x.Industry, &x.Website, &x.Phone, &x.Email, &x.Address, &x.OwnerID, &x.OwnerName, &x.OrganizationID, &x.Health, &x.AnnualRevenue, &x.EmployeeCount, &raw, &x.Version, &x.CreatedAt, &x.UpdatedAt); err != nil {
			return Page[Customer]{}, err
		}
		_ = json.Unmarshal(raw, &x.CustomFields)
		items = append(items, x)
	}
	if err = rows.Err(); err != nil {
		return Page[Customer]{}, err
	}
	has := len(items) > limit
	if has {
		items = items[:limit]
	}
	out := Page[Customer]{Items: items, HasMore: has}
	if has {
		out.NextCursor = nextCursor(offset + limit)
	}
	return out, nil
}

func (s *Service) GetCustomer(ctx context.Context, p *auth.Principal, id string) (Customer, error) {
	if err := auth.Require(p, "customer:read"); err != nil {
		return Customer{}, err
	}
	var x Customer
	var raw []byte
	err := s.DB.QueryRow(ctx, `SELECT c.id,c.name,COALESCE(c.registration_no,''),c.customer_type,COALESCE(c.grade,''),COALESCE(c.industry,''),COALESCE(c.website,''),COALESCE(c.phone,''),COALESCE(c.email,''),COALESCE(c.address,''),c.owner_id,u.display_name,COALESCE(c.organization_id::text,''),c.health,COALESCE(c.annual_revenue,0),COALESCE(c.employee_count,0),c.custom_fields,c.version,c.created_at,c.updated_at FROM customers c JOIN users u ON u.id=c.owner_id WHERE c.id=$4 AND c.active=true AND `+scopeSQL("c"), p.DataScope, p.UserID, nullable(p.OrganizationID), id).Scan(&x.ID, &x.Name, &x.RegistrationNo, &x.CustomerType, &x.Grade, &x.Industry, &x.Website, &x.Phone, &x.Email, &x.Address, &x.OwnerID, &x.OwnerName, &x.OrganizationID, &x.Health, &x.AnnualRevenue, &x.EmployeeCount, &raw, &x.Version, &x.CreatedAt, &x.UpdatedAt)
	_ = json.Unmarshal(raw, &x.CustomFields)
	return x, err
}

func validateCustomer(in CustomerInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("customer name is required")
	}
	if len(in.Name) > 200 {
		return errors.New("customer name is too long")
	}
	return nil
}
func (s *Service) CreateCustomer(ctx context.Context, p *auth.Principal, in CustomerInput, m RequestMeta) (Customer, error) {
	if err := auth.Require(p, "customer:write"); err != nil {
		return Customer{}, err
	}
	if err := validateCustomer(in); err != nil {
		return Customer{}, err
	}
	id := ids.New()
	owner := in.OwnerID
	if owner == "" || (!p.IsBootstrap && !p.Has("admin:write")) {
		owner = p.UserID
	}
	typ := in.CustomerType
	if typ == "" {
		typ = "PROSPECT"
	}
	health := in.Health
	if health == "" {
		health = "NORMAL"
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO customers(id,name,registration_no,customer_type,grade,industry,website,phone,email,address,owner_id,organization_id,health,annual_revenue,employee_count,custom_fields,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,(SELECT organization_id FROM users WHERE id=$11),$12,$13,$14,$15,$16,$16)`, id, strings.TrimSpace(in.Name), nullable(in.RegistrationNo), typ, nullable(in.Grade), nullable(in.Industry), nullable(in.Website), nullable(in.Phone), nullable(in.Email), nullable(in.Address), owner, health, in.AnnualRevenue, in.EmployeeCount, jsonValue(in.CustomFields), p.UserID)
	if err != nil {
		return Customer{}, err
	}
	out, err := s.GetCustomer(ctx, p, id)
	if err == nil {
		s.audit(ctx, p, m, "CREATE", "customer", id, nil, out)
	}
	return out, err
}

func (s *Service) UpdateCustomer(ctx context.Context, p *auth.Principal, id string, in CustomerInput, m RequestMeta) (Customer, error) {
	if err := auth.Require(p, "customer:write"); err != nil {
		return Customer{}, err
	}
	if err := validateCustomer(in); err != nil {
		return Customer{}, err
	}
	before, err := s.GetCustomer(ctx, p, id)
	if err != nil {
		return Customer{}, err
	}
	if in.Version == 0 {
		in.Version = before.Version
	}
	owner := in.OwnerID
	if owner == "" {
		owner = before.OwnerID
	}
	if owner != before.OwnerID && !p.IsBootstrap && !p.Has("admin:write") {
		return Customer{}, errors.New("owner change requires admin:write permission")
	}
	typ := in.CustomerType
	if typ == "" {
		typ = before.CustomerType
	}
	health := in.Health
	if health == "" {
		health = before.Health
	}
	cmd, err := s.DB.Exec(ctx, `UPDATE customers SET name=$1,registration_no=$2,customer_type=$3,grade=$4,industry=$5,website=$6,phone=$7,email=$8,address=$9,owner_id=$10,organization_id=(SELECT organization_id FROM users WHERE id=$10),health=$11,annual_revenue=$12,employee_count=$13,custom_fields=$14,updated_by=$15,updated_at=now(),version=version+1 WHERE id=$16 AND version=$17`, strings.TrimSpace(in.Name), nullable(in.RegistrationNo), typ, nullable(in.Grade), nullable(in.Industry), nullable(in.Website), nullable(in.Phone), nullable(in.Email), nullable(in.Address), owner, health, in.AnnualRevenue, in.EmployeeCount, jsonValue(in.CustomFields), p.UserID, id, in.Version)
	if err != nil {
		return Customer{}, err
	}
	if cmd.RowsAffected() == 0 {
		return Customer{}, errors.New("customer was changed by another user")
	}
	out, err := s.GetCustomer(ctx, p, id)
	if err == nil {
		s.audit(ctx, p, m, "UPDATE", "customer", id, before, out)
	}
	return out, err
}

func (s *Service) DuplicateCustomers(ctx context.Context, p *auth.Principal, id string) ([]Customer, error) {
	original, err := s.GetCustomer(ctx, p, id)
	if err != nil {
		return nil, err
	}
	page, err := s.ListCustomers(ctx, p, original.Name, "", "", 100)
	if err != nil {
		return nil, err
	}
	out := []Customer{}
	for _, c := range page.Items {
		if c.ID != id && (strings.EqualFold(strings.TrimSpace(c.Name), strings.TrimSpace(original.Name)) || (original.RegistrationNo != "" && c.RegistrationNo == original.RegistrationNo)) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *Service) MergeCustomers(ctx context.Context, p *auth.Principal, targetID string, sourceIDs []string, m RequestMeta) (Customer, error) {
	if err := auth.Require(p, "customer:write"); err != nil {
		return Customer{}, err
	}
	target, err := s.GetCustomer(ctx, p, targetID)
	if err != nil {
		return Customer{}, err
	}
	if len(sourceIDs) == 0 || len(sourceIDs) > 20 {
		return Customer{}, errors.New("between 1 and 20 source customers are required")
	}
	sources := make([]Customer, 0, len(sourceIDs))
	seen := map[string]bool{targetID: true}
	for _, sourceID := range sourceIDs {
		if seen[sourceID] {
			return Customer{}, errors.New("source customers must be unique and different from the target")
		}
		seen[sourceID] = true
		source, getErr := s.GetCustomer(ctx, p, sourceID)
		if getErr != nil {
			return Customer{}, errors.New("source customer not found or inaccessible")
		}
		sources = append(sources, source)
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Customer{}, err
	}
	defer tx.Rollback(ctx)
	for _, source := range sources {
		for _, statement := range []string{
			`UPDATE contacts SET customer_id=$1 WHERE customer_id=$2`,
			`UPDATE opportunities SET customer_id=$1,updated_at=now(),version=version+1 WHERE customer_id=$2`,
			`UPDATE activities SET customer_id=$1 WHERE customer_id=$2`,
			`UPDATE tasks SET customer_id=$1 WHERE customer_id=$2`,
			`UPDATE quotations SET customer_id=$1,updated_at=now(),version=version+1 WHERE customer_id=$2`,
			`UPDATE contracts SET customer_id=$1,updated_at=now(),version=version+1 WHERE customer_id=$2`,
			`UPDATE sales SET customer_id=$1 WHERE customer_id=$2`,
		} {
			if _, err = tx.Exec(ctx, statement, targetID, source.ID); err != nil {
				return Customer{}, err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE customers SET active=false,merged_into_id=$1,updated_by=$3,updated_at=now(),version=version+1 WHERE id=$2 AND active=true`, targetID, source.ID, p.UserID); err != nil {
			return Customer{}, err
		}
		details, _ := json.Marshal(map[string]any{"targetName": target.Name, "sourceName": source.Name})
		if _, err = tx.Exec(ctx, `INSERT INTO customer_merge_history(id,target_customer_id,source_customer_id,merged_by,details) VALUES($1,$2,$3,$4,$5)`, ids.New(), targetID, source.ID, p.UserID, details); err != nil {
			return Customer{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Customer{}, err
	}
	out, err := s.GetCustomer(ctx, p, targetID)
	if err == nil {
		s.audit(ctx, p, m, "MERGE", "customer", targetID, map[string]any{"target": target, "sources": sources}, out)
	}
	return out, err
}

type Customer360 struct {
	Customer      Customer         `json:"customer"`
	Contacts      []Contact        `json:"contacts"`
	Opportunities []Opportunity    `json:"opportunities"`
	Activities    []Activity       `json:"activities"`
	Contracts     []map[string]any `json:"contracts"`
	Metrics       map[string]any   `json:"metrics"`
}

func (s *Service) Customer360(ctx context.Context, p *auth.Principal, id string) (Customer360, error) {
	c, err := s.GetCustomer(ctx, p, id)
	if err != nil {
		return Customer360{}, err
	}
	out := Customer360{Customer: c, Contacts: []Contact{}, Opportunities: []Opportunity{}, Activities: []Activity{}, Contracts: []map[string]any{}, Metrics: map[string]any{}}
	rows, err := s.DB.Query(ctx, `SELECT id,customer_id,name,COALESCE(title,''),COALESCE(department,''),COALESCE(email,''),COALESCE(phone,''),COALESCE(mobile,''),decision_maker,primary_contact,owner_id,created_at FROM contacts WHERE customer_id=$1 ORDER BY primary_contact DESC,decision_maker DESC,name LIMIT 100`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var x Contact
		if err = rows.Scan(&x.ID, &x.CustomerID, &x.Name, &x.Title, &x.Department, &x.Email, &x.Phone, &x.Mobile, &x.DecisionMaker, &x.PrimaryContact, &x.OwnerID, &x.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Contacts = append(out.Contacts, x)
	}
	rows.Close()
	opps, err := s.ListOpportunities(ctx, p, OpportunityFilter{CustomerID: id, Limit: 100})
	if err != nil {
		return out, err
	}
	out.Opportunities = opps.Items
	acts, err := s.ListActivities(ctx, p, id, "", 50)
	if err != nil {
		return out, err
	}
	out.Activities = acts
	rows, err = s.DB.Query(ctx, `SELECT id,contract_no,title,amount,status,start_date,end_date,auto_renew FROM contracts WHERE customer_id=$1 ORDER BY end_date DESC NULLS LAST LIMIT 50`, id)
	if err != nil {
		return out, err
	}
	var cumulative float64
	_ = s.DB.QueryRow(ctx, `SELECT COALESCE(sum(amount),0) FROM sales WHERE customer_id=$1`, id).Scan(&cumulative)
	for rows.Next() {
		var cid, no, title, status string
		var amount float64
		var start, end *time.Time
		var renew bool
		if err = rows.Scan(&cid, &no, &title, &amount, &status, &start, &end, &renew); err != nil {
			rows.Close()
			return out, err
		}
		out.Contracts = append(out.Contracts, map[string]any{"id": cid, "contractNo": no, "title": title, "amount": amount, "status": status, "startDate": start, "endDate": end, "autoRenew": renew})
	}
	rows.Close()
	openAmount := 0.0
	for _, o := range out.Opportunities {
		if o.Status == "OPEN" {
			openAmount += o.ExpectedAmount
		}
	}
	out.Metrics = map[string]any{"cumulativeRevenue": cumulative, "openPipeline": openAmount, "opportunityCount": len(out.Opportunities), "contactCount": len(out.Contacts), "lastActivityAt": func() any {
		if len(out.Activities) > 0 {
			return out.Activities[0].OccurredAt
		}
		return nil
	}()}
	return out, nil
}

type OpportunityFilter struct {
	Query, CustomerID, Status, StageID, Cursor, Sort string
	Limit                                            int
	StaleOnly                                        bool
}

func (s *Service) ListOpportunities(ctx context.Context, p *auth.Principal, f OpportunityFilter) (Page[Opportunity], error) {
	if err := auth.Require(p, "opportunity:read"); err != nil {
		return Page[Opportunity]{}, err
	}
	if f.Limit < 1 || f.Limit > 200 {
		f.Limit = 50
	}
	offset := pageOffset(f.Cursor)
	orders := map[string]string{"name": "o.name ASC,o.id", "-name": "o.name DESC,o.id", "expectedAmount": "o.expected_amount ASC,o.id", "-expectedAmount": "o.expected_amount DESC,o.id", "probability": "o.probability ASC,o.id", "-probability": "o.probability DESC,o.id", "expectedCloseDate": "o.expected_close_date ASC NULLS LAST,o.id", "-expectedCloseDate": "o.expected_close_date DESC NULLS LAST,o.id", "updatedAt": "o.updated_at ASC,o.id", "-updatedAt": "o.updated_at DESC,o.id"}
	order := orders[f.Sort]
	if order == "" {
		order = "o.updated_at DESC,o.id"
	}
	query := `SELECT o.id,o.name,o.customer_id,c.name,o.owner_id,u.display_name,COALESCE(o.organization_id::text,''),o.pipeline_id,o.stage_id,ps.name,ps.color,o.expected_amount,o.probability,o.weighted_amount,o.expected_close_date,o.forecast_category,COALESCE(o.competitor,''),COALESCE(o.next_action,''),o.next_action_date,o.status,COALESCE(o.lost_reason,''),COALESCE(o.win_reason,''),o.stage_entered_at,o.last_activity_at,o.custom_fields,o.version,o.created_at,o.updated_at FROM opportunities o JOIN customers c ON c.id=o.customer_id JOIN users u ON u.id=o.owner_id JOIN pipeline_stages ps ON ps.id=o.stage_id WHERE ` + scopeSQL("o") + ` AND ($4='' OR lower(o.name) LIKE '%'||lower($4)||'%' OR lower(c.name) LIKE '%'||lower($4)||'%') AND ($5='' OR o.customer_id::text=$5) AND ($6='' OR o.status=$6) AND ($7='' OR o.stage_id::text=$7) AND (NOT $8 OR o.last_activity_at IS NULL OR o.last_activity_at<now()-interval '30 days') ORDER BY ` + order + ` LIMIT $9 OFFSET $10`
	rows, err := s.DB.Query(ctx, query, p.DataScope, p.UserID, nullable(p.OrganizationID), strings.TrimSpace(f.Query), f.CustomerID, f.Status, f.StageID, f.StaleOnly, f.Limit+1, offset)
	if err != nil {
		return Page[Opportunity]{}, err
	}
	defer rows.Close()
	items := []Opportunity{}
	for rows.Next() {
		x, err := scanOpportunity(rows)
		if err != nil {
			return Page[Opportunity]{}, err
		}
		x.Health = opportunityHealth(x)
		items = append(items, x)
	}
	if err = rows.Err(); err != nil {
		return Page[Opportunity]{}, err
	}
	has := len(items) > f.Limit
	if has {
		items = items[:f.Limit]
	}
	out := Page[Opportunity]{Items: items, HasMore: has}
	if has {
		out.NextCursor = nextCursor(offset + f.Limit)
	}
	return out, nil
}

type rowScanner interface{ Scan(...any) error }

func scanOpportunity(row rowScanner) (Opportunity, error) {
	var x Opportunity
	var raw []byte
	err := row.Scan(&x.ID, &x.Name, &x.CustomerID, &x.CustomerName, &x.OwnerID, &x.OwnerName, &x.OrganizationID, &x.PipelineID, &x.StageID, &x.StageName, &x.StageColor, &x.ExpectedAmount, &x.Probability, &x.WeightedAmount, &x.ExpectedCloseDate, &x.ForecastCategory, &x.Competitor, &x.NextAction, &x.NextActionDate, &x.Status, &x.LostReason, &x.WinReason, &x.StageEnteredAt, &x.LastActivityAt, &raw, &x.Version, &x.CreatedAt, &x.UpdatedAt)
	_ = json.Unmarshal(raw, &x.CustomFields)
	return x, err
}
func opportunityHealth(o Opportunity) []string {
	now := time.Now()
	out := []string{}
	if o.LastActivityAt == nil || now.Sub(*o.LastActivityAt) > 30*24*time.Hour {
		out = append(out, "NO_RECENT_ACTIVITY")
	}
	if o.ExpectedCloseDate != nil && o.ExpectedCloseDate.Before(now) && o.Status == "OPEN" {
		out = append(out, "CLOSE_DATE_OVERDUE")
	}
	if o.NextAction == "" && o.Status == "OPEN" {
		out = append(out, "NO_NEXT_ACTION")
	}
	if now.Sub(o.StageEnteredAt) > 30*24*time.Hour && o.Status == "OPEN" {
		out = append(out, "STAGE_STALLED")
	}
	return out
}

func (s *Service) GetOpportunity(ctx context.Context, p *auth.Principal, id string) (Opportunity, error) {
	if err := auth.Require(p, "opportunity:read"); err != nil {
		return Opportunity{}, err
	}
	row := s.DB.QueryRow(ctx, `SELECT o.id,o.name,o.customer_id,c.name,o.owner_id,u.display_name,COALESCE(o.organization_id::text,''),o.pipeline_id,o.stage_id,ps.name,ps.color,o.expected_amount,o.probability,o.weighted_amount,o.expected_close_date,o.forecast_category,COALESCE(o.competitor,''),COALESCE(o.next_action,''),o.next_action_date,o.status,COALESCE(o.lost_reason,''),COALESCE(o.win_reason,''),o.stage_entered_at,o.last_activity_at,o.custom_fields,o.version,o.created_at,o.updated_at FROM opportunities o JOIN customers c ON c.id=o.customer_id JOIN users u ON u.id=o.owner_id JOIN pipeline_stages ps ON ps.id=o.stage_id WHERE o.id=$4 AND `+scopeSQL("o"), p.DataScope, p.UserID, nullable(p.OrganizationID), id)
	x, err := scanOpportunity(row)
	x.Health = opportunityHealth(x)
	return x, err
}

func validateOpportunity(in OpportunityInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("opportunity name is required")
	}
	if in.CustomerID == "" {
		return errors.New("customerId is required")
	}
	if in.ExpectedAmount < 0 {
		return errors.New("expectedAmount cannot be negative")
	}
	return nil
}
func (s *Service) CreateOpportunity(ctx context.Context, p *auth.Principal, in OpportunityInput, m RequestMeta) (Opportunity, error) {
	if err := auth.Require(p, "opportunity:write"); err != nil {
		return Opportunity{}, err
	}
	if err := validateOpportunity(in); err != nil {
		return Opportunity{}, err
	}
	owner := in.OwnerID
	if owner == "" || (!p.IsBootstrap && !p.Has("admin:write")) {
		owner = p.UserID
	}
	if _, err := s.GetCustomer(ctx, p, in.CustomerID); err != nil {
		return Opportunity{}, errors.New("customer not found or inaccessible")
	}
	var pipeID, stageID string
	var probability float64
	var category string
	var won, lost bool
	if in.StageID != "" {
		err := s.DB.QueryRow(ctx, `SELECT pipeline_id,id,probability,forecast_category,is_won,is_lost FROM pipeline_stages WHERE id=$1 AND active=true`, in.StageID).Scan(&pipeID, &stageID, &probability, &category, &won, &lost)
		if err != nil {
			return Opportunity{}, errors.New("invalid stageId")
		}
	} else {
		err := s.DB.QueryRow(ctx, `SELECT p.id,s.id,s.probability,s.forecast_category,s.is_won,s.is_lost FROM pipelines p JOIN pipeline_stages s ON s.pipeline_id=p.id WHERE p.active=true AND p.is_default=true AND s.active=true ORDER BY s.stage_order LIMIT 1`).Scan(&pipeID, &stageID, &probability, &category, &won, &lost)
		if err != nil {
			return Opportunity{}, errors.New("default pipeline is not configured")
		}
	}
	if in.PipelineID != "" && in.PipelineID != pipeID {
		return Opportunity{}, errors.New("stage does not belong to pipelineId")
	}
	if in.Probability != nil {
		probability = *in.Probability
	}
	if in.ForecastCategory != "" {
		category = in.ForecastCategory
	}
	status := "OPEN"
	if won {
		status = "WON"
	}
	if lost {
		status = "LOST"
	}
	closeDate, err := dateValue(in.ExpectedCloseDate)
	if err != nil {
		return Opportunity{}, err
	}
	nextDate, err := dateValue(in.NextActionDate)
	if err != nil {
		return Opportunity{}, err
	}
	id := ids.New()
	_, err = s.DB.Exec(ctx, `INSERT INTO opportunities(id,name,customer_id,owner_id,organization_id,pipeline_id,stage_id,expected_amount,probability,expected_close_date,forecast_category,competitor,next_action,next_action_date,status,lost_reason,win_reason,custom_fields,created_by,updated_by) VALUES($1,$2,$3,$4,(SELECT organization_id FROM users WHERE id=$4),$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$18)`, id, strings.TrimSpace(in.Name), in.CustomerID, owner, pipeID, stageID, in.ExpectedAmount, probability, closeDate, category, nullable(in.Competitor), nullable(in.NextAction), nextDate, status, nullable(in.LostReason), nullable(in.WinReason), jsonValue(in.CustomFields), p.UserID)
	if err != nil {
		return Opportunity{}, err
	}
	out, err := s.GetOpportunity(ctx, p, id)
	if err == nil {
		s.audit(ctx, p, m, "CREATE", "opportunity", id, nil, out)
	}
	return out, err
}

func (s *Service) UpdateOpportunity(ctx context.Context, p *auth.Principal, id string, in OpportunityInput, m RequestMeta) (Opportunity, error) {
	if err := auth.Require(p, "opportunity:write"); err != nil {
		return Opportunity{}, err
	}
	before, err := s.GetOpportunity(ctx, p, id)
	if err != nil {
		return Opportunity{}, err
	}
	if in.Name == "" {
		in.Name = before.Name
	}
	if in.CustomerID == "" {
		in.CustomerID = before.CustomerID
	}
	if err = validateOpportunity(in); err != nil {
		return Opportunity{}, err
	}
	if in.Version == 0 {
		in.Version = before.Version
	}
	owner := in.OwnerID
	if owner == "" {
		owner = before.OwnerID
	}
	if owner != before.OwnerID && !p.IsBootstrap && !p.Has("admin:write") {
		return Opportunity{}, errors.New("owner change requires admin:write permission")
	}
	stageID := in.StageID
	if stageID == "" {
		stageID = before.StageID
	}
	var pipeID string
	var probability float64
	var category string
	var won, lost bool
	if err = s.DB.QueryRow(ctx, `SELECT pipeline_id,probability,forecast_category,is_won,is_lost FROM pipeline_stages WHERE id=$1`, stageID).Scan(&pipeID, &probability, &category, &won, &lost); err != nil {
		return Opportunity{}, errors.New("invalid stageId")
	}
	if in.Probability != nil {
		probability = *in.Probability
	}
	if in.ForecastCategory != "" {
		category = in.ForecastCategory
	}
	status := in.Status
	if status == "" {
		status = before.Status
	}
	if won {
		status = "WON"
	}
	if lost {
		status = "LOST"
	}
	closeDate, err := dateValue(in.ExpectedCloseDate)
	if err != nil {
		return Opportunity{}, err
	}
	if in.ExpectedCloseDate == nil {
		closeDate = before.ExpectedCloseDate
	}
	nextDate, err := dateValue(in.NextActionDate)
	if err != nil {
		return Opportunity{}, err
	}
	if in.NextActionDate == nil {
		nextDate = before.NextActionDate
	}
	amount := in.ExpectedAmount
	if amount == 0 && before.ExpectedAmount != 0 {
		amount = before.ExpectedAmount
	}
	custom := in.CustomFields
	if custom == nil {
		custom = before.CustomFields
	}
	cmd, err := s.DB.Exec(ctx, `UPDATE opportunities SET name=$1,customer_id=$2,owner_id=$3,organization_id=(SELECT organization_id FROM users WHERE id=$3),pipeline_id=$4,stage_id=$5,expected_amount=$6,probability=$7,expected_close_date=$8,forecast_category=$9,competitor=$10,next_action=$11,next_action_date=$12,status=$13,lost_reason=$14,win_reason=$15,custom_fields=$16,stage_entered_at=CASE WHEN stage_id<>$5 THEN now() ELSE stage_entered_at END,updated_by=$17,updated_at=now(),version=version+1 WHERE id=$18 AND version=$19`, in.Name, in.CustomerID, owner, pipeID, stageID, amount, probability, closeDate, category, nullable(in.Competitor), nullable(in.NextAction), nextDate, status, nullable(in.LostReason), nullable(in.WinReason), jsonValue(custom), p.UserID, id, in.Version)
	if err != nil {
		return Opportunity{}, err
	}
	if cmd.RowsAffected() == 0 {
		return Opportunity{}, errors.New("opportunity was changed by another user")
	}
	out, err := s.GetOpportunity(ctx, p, id)
	if err == nil {
		_, _ = s.DB.Exec(ctx, `INSERT INTO opportunity_history(id,opportunity_id,change_type,before_data,after_data,changed_by) VALUES($1,$2,$3,$4,$5,$6)`, ids.New(), id, func() string {
			if before.StageID != out.StageID {
				return "STAGE_CHANGE"
			}
			return "UPDATE"
		}(), mustJSON(before), mustJSON(out), p.UserID)
		s.audit(ctx, p, m, "UPDATE", "opportunity", id, before, out)
	}
	return out, err
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func (s *Service) ChangeOpportunityStage(ctx context.Context, p *auth.Principal, id, stageID string, version int, m RequestMeta) (Opportunity, error) {
	before, err := s.GetOpportunity(ctx, p, id)
	if err != nil {
		return Opportunity{}, err
	}
	d := ""
	if before.ExpectedCloseDate != nil {
		d = before.ExpectedCloseDate.Format("2006-01-02")
	}
	nd := ""
	if before.NextActionDate != nil {
		nd = before.NextActionDate.Format("2006-01-02")
	}
	return s.UpdateOpportunity(ctx, p, id, OpportunityInput{Name: before.Name, CustomerID: before.CustomerID, OwnerID: before.OwnerID, StageID: stageID, ExpectedAmount: before.ExpectedAmount, ExpectedCloseDate: &d, NextAction: before.NextAction, NextActionDate: &nd, Competitor: before.Competitor, LostReason: before.LostReason, WinReason: before.WinReason, CustomFields: before.CustomFields, Version: version}, m)
}

func (s *Service) Pipelines(ctx context.Context, p *auth.Principal) ([]Pipeline, error) {
	if err := auth.Require(p, "opportunity:read"); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT p.id,p.name,p.active,p.is_default,s.id,s.name,s.stage_order,s.probability,s.forecast_category,s.is_won,s.is_lost,s.active,s.color,s.min_days,s.max_days FROM pipelines p LEFT JOIN pipeline_stages s ON s.pipeline_id=p.id WHERE p.active=true ORDER BY p.is_default DESC,p.name,s.stage_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pipeline{}
	idx := map[string]int{}
	for rows.Next() {
		var pID, name string
		var active, def bool
		var sid, sname, cat, color *string
		var order *int
		var probability *float64
		var won, lost, sactive *bool
		var min, max *int
		if err = rows.Scan(&pID, &name, &active, &def, &sid, &sname, &order, &probability, &cat, &won, &lost, &sactive, &color, &min, &max); err != nil {
			return nil, err
		}
		i, ok := idx[pID]
		if !ok {
			idx[pID] = len(out)
			out = append(out, Pipeline{ID: pID, Name: name, Active: active, Default: def, Stages: []Stage{}})
			i = len(out) - 1
		}
		if sid != nil {
			out[i].Stages = append(out[i].Stages, Stage{ID: *sid, PipelineID: pID, Name: *sname, Order: *order, Probability: *probability, ForecastCategory: *cat, IsWon: *won, IsLost: *lost, Active: *sactive, Color: *color, MinDays: min, MaxDays: max})
		}
	}
	return out, rows.Err()
}

func (s *Service) AddActivity(ctx context.Context, p *auth.Principal, in ActivityInput, m RequestMeta) (Activity, error) {
	if err := auth.Require(p, "activity:write"); err != nil {
		return Activity{}, err
	}
	if strings.TrimSpace(in.Subject) == "" || strings.TrimSpace(in.ActivityType) == "" {
		return Activity{}, errors.New("activityType and subject are required")
	}
	occurred := time.Now()
	if in.OccurredAt != nil {
		occurred = *in.OccurredAt
	}
	nextDate, err := dateValue(in.NextActionDate)
	if err != nil {
		return Activity{}, err
	}
	id := ids.New()
	_, err = s.DB.Exec(ctx, `INSERT INTO activities(id,customer_id,opportunity_id,activity_type,subject,description,occurred_at,next_action,next_action_date,owner_id,organization_id,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$10,$10)`, id, nullable(in.CustomerID), nullable(in.OpportunityID), strings.ToUpper(in.ActivityType), strings.TrimSpace(in.Subject), nullable(in.Description), occurred, nullable(in.NextAction), nextDate, p.UserID, nullable(p.OrganizationID))
	if err != nil {
		return Activity{}, err
	}
	if in.OpportunityID != "" {
		_, _ = s.DB.Exec(ctx, `UPDATE opportunities SET last_activity_at=$1,next_action=COALESCE(NULLIF($2,''),next_action),next_action_date=COALESCE($3,next_action_date),updated_at=now() WHERE id=$4`, occurred, in.NextAction, nextDate, in.OpportunityID)
	}
	out, err := s.getActivity(ctx, p, id)
	if err == nil {
		s.audit(ctx, p, m, "CREATE", "activity", id, nil, out)
	}
	return out, err
}
func (s *Service) getActivity(ctx context.Context, p *auth.Principal, id string) (Activity, error) {
	var x Activity
	var customer, opp *string
	err := s.DB.QueryRow(ctx, `SELECT a.id,a.customer_id,a.opportunity_id,a.activity_type,a.subject,COALESCE(a.description,''),a.occurred_at,COALESCE(a.next_action,''),a.next_action_date,a.owner_id,u.display_name,a.created_at FROM activities a JOIN users u ON u.id=a.owner_id WHERE a.id=$4 AND `+scopeSQL("a"), p.DataScope, p.UserID, nullable(p.OrganizationID), id).Scan(&x.ID, &customer, &opp, &x.ActivityType, &x.Subject, &x.Description, &x.OccurredAt, &x.NextAction, &x.NextActionDate, &x.OwnerID, &x.OwnerName, &x.CreatedAt)
	if customer != nil {
		x.CustomerID = *customer
	}
	if opp != nil {
		x.OpportunityID = *opp
	}
	return x, err
}
func (s *Service) ListActivities(ctx context.Context, p *auth.Principal, customerID, opportunityID string, limit int) ([]Activity, error) {
	if err := auth.Require(p, "activity:read"); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx, `SELECT a.id,a.customer_id,a.opportunity_id,a.activity_type,a.subject,COALESCE(a.description,''),a.occurred_at,COALESCE(a.next_action,''),a.next_action_date,a.owner_id,u.display_name,a.created_at FROM activities a JOIN users u ON u.id=a.owner_id WHERE `+scopeSQL("a")+` AND ($4='' OR a.customer_id::text=$4) AND ($5='' OR a.opportunity_id::text=$5) ORDER BY a.occurred_at DESC LIMIT $6`, p.DataScope, p.UserID, nullable(p.OrganizationID), customerID, opportunityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var x Activity
		var customer, opp *string
		if err = rows.Scan(&x.ID, &customer, &opp, &x.ActivityType, &x.Subject, &x.Description, &x.OccurredAt, &x.NextAction, &x.NextActionDate, &x.OwnerID, &x.OwnerName, &x.CreatedAt); err != nil {
			return nil, err
		}
		if customer != nil {
			x.CustomerID = *customer
		}
		if opp != nil {
			x.OpportunityID = *opp
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Service) Dashboard(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	if err := auth.Require(p, "opportunity:read"); err != nil {
		return nil, err
	}
	var customerCount, openCount, staleCount int
	var pipeline, weighted, won float64
	args := []any{p.DataScope, p.UserID, nullable(p.OrganizationID)}
	_ = s.DB.QueryRow(ctx, `SELECT count(*) FROM customers c WHERE c.active=true AND c.merged_into_id IS NULL AND `+scopeSQL("c"), args...).Scan(&customerCount)
	err := s.DB.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='OPEN'),COALESCE(sum(expected_amount) FILTER(WHERE status='OPEN'),0),COALESCE(sum(weighted_amount) FILTER(WHERE status='OPEN'),0),COALESCE(sum(expected_amount) FILTER(WHERE status='WON' AND updated_at>=date_trunc('month',now())),0),count(*) FILTER(WHERE status='OPEN' AND (last_activity_at IS NULL OR last_activity_at<now()-interval '30 days')) FROM opportunities o WHERE `+scopeSQL("o"), args...).Scan(&openCount, &pipeline, &weighted, &won, &staleCount)
	if err != nil {
		return nil, err
	}
	var due int
	_ = s.DB.QueryRow(ctx, `SELECT count(*) FROM tasks t WHERE t.status='OPEN' AND t.due_at<now()+interval '7 days' AND ($1='COMPANY' OR t.assignee_id=$2 OR ($1='TEAM' AND EXISTS(SELECT 1 FROM users scoped_user WHERE scoped_user.id=t.assignee_id AND (scoped_user.manager_id=$2 OR scoped_user.id=$2))) OR ($1 IN ('DEPARTMENT','DIVISION') AND t.organization_id=$3))`, args...).Scan(&due)
	return map[string]any{"customerCount": customerCount, "openOpportunities": openCount, "pipelineAmount": pipeline, "weightedAmount": weighted, "wonThisMonth": won, "staleOpportunities": staleCount, "dueActions": due}, nil
}

func (s *Service) Forecast(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	if err := auth.Require(p, "forecast:read"); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT forecast_category,count(*),COALESCE(sum(expected_amount),0),COALESCE(sum(weighted_amount),0) FROM opportunities o WHERE status='OPEN' AND `+scopeSQL("o")+` GROUP BY forecast_category ORDER BY forecast_category`, p.DataScope, p.UserID, nullable(p.OrganizationID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cats := []map[string]any{}
	var total, weighted float64
	for rows.Next() {
		var cat string
		var count int
		var amount, w float64
		if err = rows.Scan(&cat, &count, &amount, &w); err != nil {
			return nil, err
		}
		cats = append(cats, map[string]any{"category": cat, "count": count, "amount": amount, "weightedAmount": w})
		total += amount
		weighted += w
	}
	return map[string]any{"categories": cats, "pipeline": total, "weighted": weighted, "generatedAt": time.Now()}, rows.Err()
}

func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
