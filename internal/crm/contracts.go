package crm

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5"
)

type Contract struct {
	ID                  string         `json:"id"`
	ContractNo          string         `json:"contractNo"`
	CustomerID          string         `json:"customerId"`
	CustomerName        string         `json:"customerName"`
	OpportunityID       string         `json:"opportunityId,omitempty"`
	OwnerID             string         `json:"ownerId"`
	OrganizationID      string         `json:"organizationId,omitempty"`
	Title               string         `json:"title"`
	Amount              float64        `json:"amount"`
	CurrencyCode        string         `json:"currencyCode"`
	ExchangeRate        float64        `json:"exchangeRate"`
	BaseAmount          float64        `json:"baseAmount"`
	StartDate           *time.Time     `json:"startDate,omitempty"`
	EndDate             *time.Time     `json:"endDate,omitempty"`
	Status              string         `json:"status"`
	AutoRenew           bool           `json:"autoRenew"`
	RevenueScheduleType string         `json:"revenueScheduleType"`
	RenewalNoticeDays   int            `json:"renewalNoticeDays"`
	RenewalStatus       string         `json:"renewalStatus"`
	RenewalAction       string         `json:"renewalAction,omitempty"`
	ActivatedAt         *time.Time     `json:"activatedAt,omitempty"`
	CustomFields        map[string]any `json:"customFields"`
	Version             int            `json:"version"`
	CreatedAt           time.Time      `json:"createdAt"`
	UpdatedAt           time.Time      `json:"updatedAt"`
}

type ContractInput struct {
	ContractNo          string         `json:"contractNo"`
	CustomerID          string         `json:"customerId"`
	OpportunityID       string         `json:"opportunityId"`
	Title               string         `json:"title"`
	Amount              float64        `json:"amount"`
	CurrencyCode        string         `json:"currencyCode"`
	ExchangeRate        *float64       `json:"exchangeRate"`
	StartDate           *string        `json:"startDate"`
	EndDate             *string        `json:"endDate"`
	Status              string         `json:"status"`
	AutoRenew           bool           `json:"autoRenew"`
	RevenueScheduleType string         `json:"revenueScheduleType"`
	RenewalNoticeDays   int            `json:"renewalNoticeDays"`
	RenewalAction       string         `json:"renewalAction"`
	CustomFields        map[string]any `json:"customFields"`
}

type RevenueSchedule struct {
	ID               string     `json:"id"`
	ContractID       string     `json:"contractId"`
	SequenceNo       int        `json:"sequenceNo"`
	ScheduledDate    time.Time  `json:"scheduledDate"`
	Amount           float64    `json:"amount"`
	CurrencyCode     string     `json:"currencyCode"`
	ExchangeRate     float64    `json:"exchangeRate"`
	BaseAmount       float64    `json:"baseAmount"`
	Status           string     `json:"status"`
	RecognizedSaleID string     `json:"recognizedSaleId,omitempty"`
	RecognizedAt     *time.Time `json:"recognizedAt,omitempty"`
}

func parseContractDate(value *string) (*time.Time, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", *value)
	if err != nil {
		return nil, errors.New("date must use YYYY-MM-DD format")
	}
	return &parsed, nil
}

func normalizeScheduleType(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		value = "ONE_TIME"
	}
	switch value {
	case "ONE_TIME", "MONTHLY", "QUARTERLY", "ANNUAL":
		return value, nil
	default:
		return "", errors.New("invalid revenueScheduleType")
	}
}

func addMonthsClamped(anchor time.Time, months int) time.Time {
	first := time.Date(anchor.Year(), anchor.Month()+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	lastDay := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	day := anchor.Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC)
}

func buildScheduleDates(start, end *time.Time, scheduleType string) ([]time.Time, error) {
	if start == nil {
		return nil, errors.New("startDate is required to activate a contract")
	}
	if end != nil && end.Before(*start) {
		return nil, errors.New("endDate cannot be before startDate")
	}
	if scheduleType == "ONE_TIME" {
		return []time.Time{*start}, nil
	}
	if end == nil {
		return nil, errors.New("endDate is required for recurring revenue schedules")
	}
	step := map[string]int{"MONTHLY": 1, "QUARTERLY": 3, "ANNUAL": 12}[scheduleType]
	if step == 0 {
		return nil, errors.New("invalid revenueScheduleType")
	}
	dates := []time.Time{}
	for n := 0; ; n++ {
		date := addMonthsClamped(*start, n*step)
		if date.After(*end) {
			break
		}
		dates = append(dates, date)
	}
	if len(dates) == 0 {
		return nil, errors.New("contract period does not contain a revenue schedule date")
	}
	return dates, nil
}

func splitScheduleAmount(total float64, count int) []float64 {
	if count <= 0 {
		return nil
	}
	totalCents := int64(math.Round(total * 100))
	base := totalCents / int64(count)
	remainder := totalCents % int64(count)
	amounts := make([]float64, count)
	for i := range amounts {
		cents := base
		if int64(i) < remainder {
			cents++
		}
		amounts[i] = float64(cents) / 100
	}
	return amounts
}

func (s *Service) CreateContract(ctx context.Context, p *auth.Principal, in ContractInput, m RequestMeta) (Contract, error) {
	if err := auth.Require(p, "contract:write"); err != nil {
		return Contract{}, err
	}
	if in.CustomerID == "" || strings.TrimSpace(in.Title) == "" || in.Amount < 0 {
		return Contract{}, errors.New("customerId, title and valid amount are required")
	}
	if _, err := s.GetCustomer(ctx, p, in.CustomerID); err != nil {
		return Contract{}, errors.New("customer not found or inaccessible")
	}
	if in.OpportunityID != "" {
		opp, err := s.GetOpportunity(ctx, p, in.OpportunityID)
		if err != nil || opp.CustomerID != in.CustomerID {
			return Contract{}, errors.New("opportunity not found, inaccessible, or belongs to another customer")
		}
	}
	start, err := parseContractDate(in.StartDate)
	if err != nil {
		return Contract{}, err
	}
	end, err := parseContractDate(in.EndDate)
	if err != nil {
		return Contract{}, err
	}
	if end != nil && start != nil && end.Before(*start) {
		return Contract{}, errors.New("endDate cannot be before startDate")
	}
	currency := strings.ToUpper(strings.TrimSpace(in.CurrencyCode))
	if currency == "" {
		currency = baseCurrencyCode
	}
	rate := 1.0
	if in.ExchangeRate != nil {
		rate = *in.ExchangeRate
	} else if currency != baseCurrencyCode {
		return Contract{}, errors.New("exchangeRate is required for non-KRW contracts")
	}
	if err = validateCurrency(currency, rate); err != nil {
		return Contract{}, err
	}
	scheduleType, err := normalizeScheduleType(in.RevenueScheduleType)
	if err != nil {
		return Contract{}, err
	}
	status := strings.ToUpper(strings.TrimSpace(in.Status))
	if status == "" {
		status = "DRAFT"
	}
	if status != "DRAFT" && status != "ACTIVE" {
		return Contract{}, errors.New("contract status must be DRAFT or ACTIVE when created")
	}
	if in.RenewalNoticeDays == 0 {
		in.RenewalNoticeDays = 90
	}
	if in.RenewalNoticeDays < 0 || in.RenewalNoticeDays > 730 {
		return Contract{}, errors.New("renewalNoticeDays must be between 0 and 730")
	}
	if in.ContractNo == "" {
		in.ContractNo = "C-" + time.Now().Format("20060102") + "-" + strings.ToUpper(ids.HexToken(3))
	}
	contract := Contract{ID: ids.New(), ContractNo: strings.TrimSpace(in.ContractNo), CustomerID: in.CustomerID, OpportunityID: in.OpportunityID, OwnerID: p.UserID, OrganizationID: p.OrganizationID, Title: strings.TrimSpace(in.Title), Amount: in.Amount, CurrencyCode: currency, ExchangeRate: rate, BaseAmount: in.Amount * rate, StartDate: start, EndDate: end, Status: status, AutoRenew: in.AutoRenew, RevenueScheduleType: scheduleType, RenewalNoticeDays: in.RenewalNoticeDays, RenewalStatus: "NOT_STARTED", RenewalAction: strings.TrimSpace(in.RenewalAction), CustomFields: in.CustomFields, Version: 1}
	if status == "ACTIVE" {
		now := time.Now().UTC()
		contract.ActivatedAt = &now
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Contract{}, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO contracts(id,contract_no,customer_id,opportunity_id,owner_id,organization_id,title,amount,currency_code,exchange_rate,start_date,end_date,status,auto_renew,revenue_schedule_type,renewal_notice_days,renewal_action,activated_at,custom_fields,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$20)`, contract.ID, contract.ContractNo, contract.CustomerID, nullable(contract.OpportunityID), contract.OwnerID, nullable(contract.OrganizationID), contract.Title, contract.Amount, contract.CurrencyCode, contract.ExchangeRate, contract.StartDate, contract.EndDate, contract.Status, contract.AutoRenew, contract.RevenueScheduleType, contract.RenewalNoticeDays, nullable(contract.RenewalAction), contract.ActivatedAt, jsonValue(contract.CustomFields), p.UserID)
	if err != nil {
		return Contract{}, err
	}
	if status == "ACTIVE" {
		if err = createRevenueSchedule(ctx, tx, contract, p.UserID); err != nil {
			return Contract{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Contract{}, err
	}
	out, err := s.GetContract(ctx, p, contract.ID)
	if err == nil {
		s.audit(ctx, p, m, "CREATE", "contract", contract.ID, nil, out)
	}
	return out, err
}

func createRevenueSchedule(ctx context.Context, tx pgx.Tx, contract Contract, actorID string) error {
	dates, err := buildScheduleDates(contract.StartDate, contract.EndDate, contract.RevenueScheduleType)
	if err != nil {
		return err
	}
	amounts := splitScheduleAmount(contract.Amount, len(dates))
	for i, date := range dates {
		_, err = tx.Exec(ctx, `INSERT INTO revenue_schedules(id,contract_id,sequence_no,scheduled_date,amount,currency_code,exchange_rate,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, ids.New(), contract.ID, i+1, date, amounts[i], contract.CurrencyCode, contract.ExchangeRate, actorID)
		if err != nil {
			return err
		}
	}
	return nil
}

func scanContract(row rowScanner) (Contract, error) {
	var out Contract
	var opportunity, organization *string
	var custom []byte
	err := row.Scan(&out.ID, &out.ContractNo, &out.CustomerID, &out.CustomerName, &opportunity, &out.OwnerID, &organization, &out.Title, &out.Amount, &out.CurrencyCode, &out.ExchangeRate, &out.BaseAmount, &out.StartDate, &out.EndDate, &out.Status, &out.AutoRenew, &out.RevenueScheduleType, &out.RenewalNoticeDays, &out.RenewalStatus, &out.RenewalAction, &out.ActivatedAt, &custom, &out.Version, &out.CreatedAt, &out.UpdatedAt)
	if opportunity != nil {
		out.OpportunityID = *opportunity
	}
	if organization != nil {
		out.OrganizationID = *organization
	}
	_ = json.Unmarshal(custom, &out.CustomFields)
	return out, err
}

func (s *Service) GetContract(ctx context.Context, p *auth.Principal, id string) (Contract, error) {
	if err := auth.Require(p, "contract:read"); err != nil {
		return Contract{}, err
	}
	query := `SELECT c.id,c.contract_no,c.customer_id,cu.name,c.opportunity_id,c.owner_id,c.organization_id,c.title,c.amount,c.currency_code,c.exchange_rate,c.base_amount,c.start_date,c.end_date,c.status,c.auto_renew,c.revenue_schedule_type,c.renewal_notice_days,c.renewal_status,COALESCE(c.renewal_action,''),c.activated_at,c.custom_fields,c.version,c.created_at,c.updated_at FROM contracts c JOIN customers cu ON cu.id=c.customer_id WHERE c.id=$4 AND ` + scopeSQL("c")
	return scanContract(s.DB.QueryRow(ctx, query, p.DataScope, p.UserID, nullable(p.OrganizationID), id))
}

func (s *Service) ActivateContract(ctx context.Context, p *auth.Principal, id string, version int, m RequestMeta) (Contract, error) {
	if err := auth.Require(p, "contract:write"); err != nil {
		return Contract{}, err
	}
	before, err := s.GetContract(ctx, p, id)
	if err != nil {
		return Contract{}, err
	}
	if before.Status != "DRAFT" {
		return Contract{}, errors.New("only DRAFT contracts can be activated")
	}
	if version == 0 {
		version = before.Version
	}
	if _, err = buildScheduleDates(before.StartDate, before.EndDate, before.RevenueScheduleType); err != nil {
		return Contract{}, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Contract{}, err
	}
	defer tx.Rollback(ctx)
	cmd, err := tx.Exec(ctx, `UPDATE contracts SET status='ACTIVE',activated_at=now(),updated_by=$1,updated_at=now(),version=version+1 WHERE id=$2 AND status='DRAFT' AND version=$3`, p.UserID, id, version)
	if err != nil {
		return Contract{}, err
	}
	if cmd.RowsAffected() == 0 {
		return Contract{}, errors.New("contract was changed by another user")
	}
	if err = createRevenueSchedule(ctx, tx, before, p.UserID); err != nil {
		return Contract{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Contract{}, err
	}
	out, err := s.GetContract(ctx, p, id)
	if err == nil {
		s.audit(ctx, p, m, "ACTIVATE", "contract", id, before, out)
	}
	return out, err
}

func (s *Service) ListRevenueSchedules(ctx context.Context, p *auth.Principal, contractID string) ([]RevenueSchedule, error) {
	if _, err := s.GetContract(ctx, p, contractID); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT id,contract_id,sequence_no,scheduled_date,amount,currency_code,exchange_rate,base_amount,CASE WHEN status='PLANNED' AND scheduled_date<current_date THEN 'DUE' ELSE status END,recognized_sale_id,recognized_at FROM revenue_schedules WHERE contract_id=$1 ORDER BY sequence_no`, contractID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RevenueSchedule{}
	for rows.Next() {
		var item RevenueSchedule
		var sale *string
		if err = rows.Scan(&item.ID, &item.ContractID, &item.SequenceNo, &item.ScheduledDate, &item.Amount, &item.CurrencyCode, &item.ExchangeRate, &item.BaseAmount, &item.Status, &sale, &item.RecognizedAt); err != nil {
			return nil, err
		}
		if sale != nil {
			item.RecognizedSaleID = *sale
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) RecognizeRevenueSchedule(ctx context.Context, p *auth.Principal, scheduleID string, recognizedDate string, m RequestMeta) (RevenueSchedule, error) {
	if err := auth.Require(p, "sales:write"); err != nil {
		return RevenueSchedule{}, err
	}
	date := time.Now().UTC()
	if strings.TrimSpace(recognizedDate) != "" {
		var err error
		date, err = time.Parse("2006-01-02", recognizedDate)
		if err != nil {
			return RevenueSchedule{}, errors.New("recognizedDate must use YYYY-MM-DD")
		}
	}
	var contractID string
	if err := s.DB.QueryRow(ctx, `SELECT contract_id FROM revenue_schedules WHERE id=$1`, scheduleID).Scan(&contractID); err != nil {
		return RevenueSchedule{}, err
	}
	contract, err := s.GetContract(ctx, p, contractID)
	if err != nil {
		return RevenueSchedule{}, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return RevenueSchedule{}, err
	}
	defer tx.Rollback(ctx)
	var amount, rate float64
	var currency, status string
	err = tx.QueryRow(ctx, `SELECT amount,currency_code,exchange_rate,status FROM revenue_schedules WHERE id=$1 AND contract_id=$2 FOR UPDATE`, scheduleID, contract.ID).Scan(&amount, &currency, &rate, &status)
	if err != nil {
		return RevenueSchedule{}, err
	}
	if status != "PLANNED" {
		return RevenueSchedule{}, errors.New("revenue schedule is already recognized or cancelled")
	}
	saleID := ids.New()
	_, err = tx.Exec(ctx, `INSERT INTO sales(id,customer_id,contract_id,owner_id,organization_id,amount,currency_code,exchange_rate,recognized_date,description,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, saleID, contract.CustomerID, contract.ID, contract.OwnerID, nullable(contract.OrganizationID), amount, currency, rate, date, "Contract revenue schedule #"+scheduleID, p.UserID)
	if err != nil {
		return RevenueSchedule{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE revenue_schedules SET status='RECOGNIZED',recognized_sale_id=$1,recognized_at=now(),updated_at=now() WHERE id=$2`, saleID, scheduleID)
	if err != nil {
		return RevenueSchedule{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return RevenueSchedule{}, err
	}
	items, err := s.ListRevenueSchedules(ctx, p, contract.ID)
	if err != nil {
		return RevenueSchedule{}, err
	}
	for _, item := range items {
		if item.ID == scheduleID {
			s.audit(ctx, p, m, "RECOGNIZE_REVENUE", "revenue_schedule", scheduleID, nil, item)
			return item, nil
		}
	}
	return RevenueSchedule{}, errors.New("recognized revenue schedule not found")
}

func (s *Service) UpdateContractRenewal(ctx context.Context, p *auth.Principal, id, status, action string, version int, m RequestMeta) (Contract, error) {
	if err := auth.Require(p, "contract:write"); err != nil {
		return Contract{}, err
	}
	before, err := s.GetContract(ctx, p, id)
	if err != nil {
		return Contract{}, err
	}
	status = strings.ToUpper(strings.TrimSpace(status))
	if status == "" {
		status = before.RenewalStatus
	}
	valid := map[string]bool{"NOT_STARTED": true, "PLANNED": true, "IN_PROGRESS": true, "RENEWED": true, "CHURNED": true}
	if !valid[status] {
		return Contract{}, errors.New("invalid renewalStatus")
	}
	if version == 0 {
		version = before.Version
	}
	cmd, err := s.DB.Exec(ctx, `UPDATE contracts SET renewal_status=$1,renewal_action=$2,updated_by=$3,updated_at=now(),version=version+1 WHERE id=$4 AND version=$5`, status, nullable(action), p.UserID, id, version)
	if err != nil {
		return Contract{}, err
	}
	if cmd.RowsAffected() == 0 {
		return Contract{}, errors.New("contract was changed by another user")
	}
	out, err := s.GetContract(ctx, p, id)
	if err == nil {
		s.audit(ctx, p, m, "UPDATE_RENEWAL", "contract", id, before, out)
	}
	return out, err
}
