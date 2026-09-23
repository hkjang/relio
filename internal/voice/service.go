// Package voice manages the post-sale half of the customer lifecycle: the
// complaints, requests, inquiries and churn signals a customer raises after the
// contract is signed. It reuses the CRM Data Scope predicate so a salesperson
// sees exactly the customers they already own, and appends an immutable handling
// history so the response to a complaint is auditable.
package voice

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
	"github.com/hkjang/relio/internal/mail"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/hkjang/relio/internal/platform/timezone"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	DB    *pgxpool.Pool
	CRM   *crm.Service
	Audit *audit.Service
	// Mail tells a newly assigned owner in the background; nil means mail is
	// not wired. Clock renders the SLA deadline in the configured zone.
	Mail  mail.Notifier
	Clock *timezone.Loader
}

// notifyAssigned mails the owner of a record that somebody else just put on
// their desk. The SLA clock is running from the moment of receipt, so this is
// the one moment they must not learn about by refreshing the list.
func (s *Service) notifyAssigned(ctx context.Context, p *auth.Principal, voice Voice) {
	if s.Mail == nil || voice.OwnerID == "" || voice.OwnerID == p.UserID {
		return
	}
	s.Mail.Notify(ctx, mail.VoiceAssigned(p.DisplayName, voice.VoiceNo, voice.Title, voice.CustomerName, voice.Severity, voice.ID, voice.ResponseDueAt, s.Clock.Location(ctx)), p.UserID, []string{voice.OwnerID})
}

type Category struct {
	ID              string `json:"id"`
	Code            string `json:"code"`
	Name            string `json:"name"`
	VoiceType       string `json:"voiceType"`
	ResponseHours   int    `json:"responseHours"`
	ResolutionHours int    `json:"resolutionHours"`
	// SLAEnabled false means no deadline is computed, stored or shown.
	SLAEnabled   bool   `json:"slaEnabled"`
	WorkspaceID  string `json:"workspaceId,omitempty"`
	Active       bool   `json:"active"`
	DisplayOrder int    `json:"displayOrder"`
}

type Voice struct {
	ID                  string         `json:"id"`
	VoiceNo             string         `json:"voiceNo"`
	CustomerID          string         `json:"customerId"`
	CustomerName        string         `json:"customerName"`
	ContactID           string         `json:"contactId,omitempty"`
	ContactName         string         `json:"contactName,omitempty"`
	OpportunityID       string         `json:"opportunityId,omitempty"`
	ContractID          string         `json:"contractId,omitempty"`
	CategoryID          string         `json:"categoryId,omitempty"`
	CategoryName        string         `json:"categoryName,omitempty"`
	VoiceType           string         `json:"voiceType"`
	Channel             string         `json:"channel"`
	Title               string         `json:"title"`
	Body                string         `json:"body,omitempty"`
	Severity            string         `json:"severity"`
	Status              string         `json:"status"`
	OwnerID             string         `json:"ownerId"`
	OwnerName           string         `json:"ownerName"`
	OccurredAt          time.Time      `json:"occurredAt"`
	ResponseDueAt       *time.Time     `json:"responseDueAt,omitempty"`
	ResolutionDueAt     *time.Time     `json:"resolutionDueAt,omitempty"`
	FirstRespondedAt    *time.Time     `json:"firstRespondedAt,omitempty"`
	ResolvedAt          *time.Time     `json:"resolvedAt,omitempty"`
	ClosedAt            *time.Time     `json:"closedAt,omitempty"`
	Resolution          string         `json:"resolution,omitempty"`
	RootCause           string         `json:"rootCause,omitempty"`
	PreventiveAction    string         `json:"preventiveAction,omitempty"`
	SatisfactionScore   *int           `json:"satisfactionScore,omitempty"`
	SatisfactionComment string         `json:"satisfactionComment,omitempty"`
	CustomFields        map[string]any `json:"customFields"`
	WorkspaceID         string         `json:"workspaceId,omitempty"`
	WorkspaceName       string         `json:"workspaceName,omitempty"`
	CustomerCode        string         `json:"customerCode,omitempty"`
	CauseEvidence       string         `json:"causeEvidence,omitempty"`
	KnowledgeStatus     string         `json:"knowledgeStatus"`
	KnowledgeReviewedAt *time.Time     `json:"knowledgeReviewedAt,omitempty"`
	KnowledgeReviewer   string         `json:"knowledgeReviewer,omitempty"`
	// SLAApplied is false for a type with SLA switched off; screens then show
	// elapsed days instead of deadlines.
	SLAApplied bool      `json:"slaApplied"`
	Version    int       `json:"version"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	// Derived for the UI so the list can highlight risk without extra calls.
	ResponseOverdue   bool `json:"responseOverdue"`
	ResolutionOverdue bool `json:"resolutionOverdue"`
	OpenDays          int  `json:"openDays"`
}

type Event struct {
	ID         string    `json:"id"`
	EventType  string    `json:"eventType"`
	FromStatus string    `json:"fromStatus,omitempty"`
	ToStatus   string    `json:"toStatus,omitempty"`
	Note       string    `json:"note,omitempty"`
	ActorID    string    `json:"actorId"`
	ActorName  string    `json:"actorName"`
	OccurredAt time.Time `json:"occurredAt"`
}

type Input struct {
	CustomerID    string         `json:"customerId"`
	ContactID     string         `json:"contactId"`
	OpportunityID string         `json:"opportunityId"`
	ContractID    string         `json:"contractId"`
	CategoryID    string         `json:"categoryId"`
	VoiceType     string         `json:"voiceType"`
	Channel       string         `json:"channel"`
	Title         string         `json:"title"`
	Body          string         `json:"body"`
	Severity      string         `json:"severity"`
	OwnerID       string         `json:"ownerId"`
	OccurredAt    *time.Time     `json:"occurredAt"`
	CustomFields  map[string]any `json:"customFields"`
	Version       int            `json:"version"`
}

type UpdateInput struct {
	Input
	Status              string `json:"status"`
	Resolution          string `json:"resolution"`
	RootCause           string `json:"rootCause"`
	PreventiveAction    string `json:"preventiveAction"`
	SatisfactionScore   *int   `json:"satisfactionScore"`
	SatisfactionComment string `json:"satisfactionComment"`
	Note                string `json:"note"`
	// CauseEvidence is how well the root cause is established. A knowledge
	// gated workspace requires it before a case can be resolved.
	CauseEvidence string `json:"causeEvidence"`
}

var (
	voiceTypes = map[string]bool{"COMPLAINT": true, "REQUEST": true, "INQUIRY": true, "DEFECT": true, "PRAISE": true, "CHURN_RISK": true}
	channels   = map[string]bool{"PHONE": true, "EMAIL": true, "VISIT": true, "PORTAL": true, "CHAT": true, "PARTNER": true, "OTHER": true}
	severities = map[string]bool{"LOW": true, "NORMAL": true, "HIGH": true, "CRITICAL": true}
	statuses   = map[string]bool{"RECEIVED": true, "IN_REVIEW": true, "IN_PROGRESS": true, "PENDING_CUSTOMER": true, "RESOLVED": true, "CLOSED": true, "REJECTED": true}
	// Closing states are terminal for SLA purposes and for the open-count badge.
	terminal = map[string]bool{"RESOLVED": true, "CLOSED": true, "REJECTED": true}
)

// allowedTransitions keeps the lifecycle honest: a ticket cannot jump from
// intake straight to closed without a recorded resolution, and a closed ticket
// must be explicitly reopened rather than quietly edited.
var allowedTransitions = map[string][]string{
	"RECEIVED":         {"IN_REVIEW", "IN_PROGRESS", "REJECTED"},
	"IN_REVIEW":        {"IN_PROGRESS", "PENDING_CUSTOMER", "RESOLVED", "REJECTED"},
	"IN_PROGRESS":      {"PENDING_CUSTOMER", "RESOLVED", "REJECTED"},
	"PENDING_CUSTOMER": {"IN_PROGRESS", "RESOLVED", "REJECTED"},
	"RESOLVED":         {"CLOSED", "IN_PROGRESS"},
	"CLOSED":           {"IN_PROGRESS"},
	"REJECTED":         {"IN_PROGRESS"},
}

func canTransition(from, to string) bool {
	if from == to {
		return true
	}
	for _, allowed := range allowedTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func (s *Service) Categories(ctx context.Context, p *auth.Principal, includeInactive bool) ([]Category, error) {
	if err := auth.Require(p, "voice:read"); err != nil {
		return nil, err
	}
	// A type of an isolated workspace is only offered to that workspace's members.
	rows, err := s.DB.Query(ctx, `SELECT c.id,c.code,c.name,c.voice_type,c.response_hours,c.resolution_hours,c.sla_enabled,COALESCE(c.workspace_id::text,''),c.active,c.display_order
		FROM voice_categories c LEFT JOIN voice_workspaces w ON w.id=c.workspace_id
		WHERE (c.active OR $4) AND (c.workspace_id IS NULL OR `+workspaceAccessSQL(p)+`) AND `+bindCaller+`
		ORDER BY c.display_order,c.name`, p.DataScope, p.UserID, orgArg(p), includeInactive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		if err = rows.Scan(&c.ID, &c.Code, &c.Name, &c.VoiceType, &c.ResponseHours, &c.ResolutionHours, &c.SLAEnabled, &c.WorkspaceID, &c.Active, &c.DisplayOrder); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

const voiceColumns = `v.id,v.voice_no,v.customer_id,c.name,COALESCE(v.contact_id::text,''),COALESCE(ct.name,''),
	COALESCE(v.opportunity_id::text,''),COALESCE(v.contract_id::text,''),COALESCE(v.category_id::text,''),COALESCE(cat.name,''),
	v.voice_type,v.channel,v.title,COALESCE(v.body,''),v.severity,v.status,v.owner_id,u.display_name,
	v.occurred_at,v.response_due_at,v.resolution_due_at,v.first_responded_at,v.resolved_at,v.closed_at,
	COALESCE(v.resolution,''),COALESCE(v.root_cause,''),COALESCE(v.preventive_action,''),
	v.satisfaction_score,COALESCE(v.satisfaction_comment,''),v.custom_fields,v.version,v.created_at,v.updated_at,
	COALESCE(v.workspace_id::text,''),COALESCE(ws.name,''),COALESCE(c.customer_code,''),COALESCE(v.cause_evidence,''),
	v.knowledge_status,v.knowledge_reviewed_at,COALESCE(kr.display_name,''),COALESCE(cat.sla_enabled,true)`

const voiceJoins = `FROM customer_voices v
	JOIN customers c ON c.id=v.customer_id
	JOIN users u ON u.id=v.owner_id
	LEFT JOIN contacts ct ON ct.id=v.contact_id
	LEFT JOIN voice_categories cat ON cat.id=v.category_id
	LEFT JOIN voice_workspaces ws ON ws.id=v.workspace_id
	LEFT JOIN users kr ON kr.id=v.knowledge_reviewed_by`

func scanVoice(rows pgx.Rows) (Voice, error) {
	var x Voice
	var raw []byte
	err := rows.Scan(&x.ID, &x.VoiceNo, &x.CustomerID, &x.CustomerName, &x.ContactID, &x.ContactName,
		&x.OpportunityID, &x.ContractID, &x.CategoryID, &x.CategoryName,
		&x.VoiceType, &x.Channel, &x.Title, &x.Body, &x.Severity, &x.Status, &x.OwnerID, &x.OwnerName,
		&x.OccurredAt, &x.ResponseDueAt, &x.ResolutionDueAt, &x.FirstRespondedAt, &x.ResolvedAt, &x.ClosedAt,
		&x.Resolution, &x.RootCause, &x.PreventiveAction,
		&x.SatisfactionScore, &x.SatisfactionComment, &raw, &x.Version, &x.CreatedAt, &x.UpdatedAt,
		&x.WorkspaceID, &x.WorkspaceName, &x.CustomerCode, &x.CauseEvidence,
		&x.KnowledgeStatus, &x.KnowledgeReviewedAt, &x.KnowledgeReviewer, &x.SLAApplied)
	if err != nil {
		return Voice{}, err
	}
	_ = json.Unmarshal(raw, &x.CustomFields)
	if x.CustomFields == nil {
		x.CustomFields = map[string]any{}
	}
	decorate(&x)
	return x, nil
}

// decorate computes the SLA breach flags and age once, so the list, the customer
// 360 panel and MCP all agree on what "overdue" means.
func decorate(x *Voice) {
	now := time.Now()
	open := !terminal[x.Status]
	if x.ResponseDueAt != nil && x.FirstRespondedAt == nil && open && now.After(*x.ResponseDueAt) {
		x.ResponseOverdue = true
	}
	if x.ResolutionDueAt != nil && x.ResolvedAt == nil && open && now.After(*x.ResolutionDueAt) {
		x.ResolutionOverdue = true
	}
	end := now
	if x.ResolvedAt != nil {
		end = *x.ResolvedAt
	}
	if days := int(end.Sub(x.OccurredAt).Hours() / 24); days > 0 {
		x.OpenDays = days
	}
}

type Query struct {
	CustomerID  string
	WorkspaceID string
	CategoryID  string
	Status      string
	VoiceType   string
	Severity    string
	OwnerID     string
	Overdue     bool
	OpenOnly    bool
	// KnowledgeStatus filters on the verification gate.
	KnowledgeStatus string
	// ReviewPending lists closed cases a reviewer has not looked at yet.
	ReviewPending bool
	// MinAgeDays keeps open cases at least this many days old: the way a
	// department without SLA finds work that has been waiting too long.
	MinAgeDays int
	Limit      int
}

func (s *Service) List(ctx context.Context, p *auth.Principal, q Query) ([]Voice, error) {
	if err := auth.Require(p, "voice:read"); err != nil {
		return nil, err
	}
	if q.Limit < 1 || q.Limit > 200 {
		q.Limit = 50
	}
	query := `SELECT ` + voiceColumns + ` ` + voiceJoins + ` WHERE ` + VisibleSQL(p, "v") + `
		AND ($4='' OR v.customer_id::text=$4)
		AND ($5='' OR v.status=$5)
		AND ($6='' OR v.voice_type=$6)
		AND ($7='' OR v.severity=$7)
		AND ($8='' OR v.owner_id::text=$8)
		AND (NOT $9 OR v.status NOT IN ('RESOLVED','CLOSED','REJECTED'))
		AND (NOT $10 OR (v.status NOT IN ('RESOLVED','CLOSED','REJECTED') AND (
			(v.response_due_at IS NOT NULL AND v.first_responded_at IS NULL AND v.response_due_at < now())
			OR (v.resolution_due_at IS NOT NULL AND v.resolved_at IS NULL AND v.resolution_due_at < now()))))
		AND ($12='' OR v.workspace_id::text=$12)
		AND ($13='' OR v.category_id::text=$13)
		AND ($14='' OR v.knowledge_status=$14)
		AND (NOT $15 OR (v.status IN ('RESOLVED','CLOSED') AND v.knowledge_status='UNREVIEWED'))
		AND ($16=0 OR (v.status NOT IN ('RESOLVED','CLOSED','REJECTED') AND v.occurred_at <= now()-make_interval(days => $16)))
		ORDER BY (v.status NOT IN ('RESOLVED','CLOSED','REJECTED')) DESC,
			CASE v.severity WHEN 'CRITICAL' THEN 0 WHEN 'HIGH' THEN 1 WHEN 'NORMAL' THEN 2 ELSE 3 END,
			v.occurred_at DESC
		LIMIT $11`
	rows, err := s.DB.Query(ctx, query, p.DataScope, p.UserID, orgArg(p), q.CustomerID,
		strings.ToUpper(q.Status), strings.ToUpper(q.VoiceType), strings.ToUpper(q.Severity), q.OwnerID,
		q.OpenOnly, q.Overdue, q.Limit, q.WorkspaceID, q.CategoryID, strings.ToUpper(q.KnowledgeStatus), q.ReviewPending, max(q.MinAgeDays, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Voice{}
	for rows.Next() {
		x, err := scanVoice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func orgArg(p *auth.Principal) any {
	if p.OrganizationID == "" {
		return nil
	}
	return p.OrganizationID
}

func (s *Service) Get(ctx context.Context, p *auth.Principal, id string) (Voice, []Event, error) {
	if err := auth.Require(p, "voice:read"); err != nil {
		return Voice{}, nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT `+voiceColumns+` `+voiceJoins+` WHERE v.id=$4 AND `+VisibleSQL(p, "v"),
		p.DataScope, p.UserID, orgArg(p), id)
	if err != nil {
		return Voice{}, nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return Voice{}, nil, err
		}
		return Voice{}, nil, pgx.ErrNoRows
	}
	x, err := scanVoice(rows)
	if err != nil {
		return Voice{}, nil, err
	}
	rows.Close()
	events, err := s.events(ctx, id)
	return x, events, err
}

func (s *Service) events(ctx context.Context, id string) ([]Event, error) {
	rows, err := s.DB.Query(ctx, `SELECT e.id,e.event_type,COALESCE(e.from_status,''),COALESCE(e.to_status,''),COALESCE(e.note,''),e.actor_id,u.display_name,e.occurred_at
		FROM customer_voice_events e JOIN users u ON u.id=e.actor_id WHERE e.voice_id=$1 ORDER BY e.occurred_at,e.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err = rows.Scan(&e.ID, &e.EventType, &e.FromStatus, &e.ToStatus, &e.Note, &e.ActorID, &e.ActorName, &e.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func validate(in Input) error {
	if strings.TrimSpace(in.Title) == "" {
		return errors.New("title is required")
	}
	if in.CustomerID == "" {
		return errors.New("customerId is required")
	}
	if !voiceTypes[strings.ToUpper(in.VoiceType)] {
		return errors.New("invalid voiceType")
	}
	if in.Channel != "" && !channels[strings.ToUpper(in.Channel)] {
		return errors.New("invalid channel")
	}
	if in.Severity != "" && !severities[strings.ToUpper(in.Severity)] {
		return errors.New("invalid severity")
	}
	return nil
}

// slaDeadlines derives the response and resolution targets from the category, or
// from the severity when the intake has no category yet.
func (s *Service) slaDeadlines(ctx context.Context, categoryID, severity string, from time.Time) (*time.Time, *time.Time) {
	responseHours, resolutionHours := 8, 72
	if categoryID != "" {
		_ = s.DB.QueryRow(ctx, `SELECT response_hours,resolution_hours FROM voice_categories WHERE id=$1`, categoryID).
			Scan(&responseHours, &resolutionHours)
	}
	switch strings.ToUpper(severity) {
	case "CRITICAL":
		responseHours, resolutionHours = min(responseHours, 2), min(resolutionHours, 24)
	case "HIGH":
		responseHours, resolutionHours = min(responseHours, 4), min(resolutionHours, 48)
	}
	response := from.Add(time.Duration(responseHours) * time.Hour)
	resolution := from.Add(time.Duration(resolutionHours) * time.Hour)
	return &response, &resolution
}

// category returns a request type the caller may use, active or not.
func (s *Service) category(ctx context.Context, p *auth.Principal, id string) (Category, error) {
	items, err := s.Categories(ctx, p, true)
	if err != nil {
		return Category{}, err
	}
	for _, c := range items {
		if c.ID == id {
			return c, nil
		}
	}
	return Category{}, errors.New("voice category not found")
}

// intakeContext resolves what a request's type decides: its workspace, its
// field definitions and whether it keeps an SLA.
type intakeContext struct {
	category  *Category
	workspace *Workspace
	fields    []Field
}

func (s *Service) intakeFor(ctx context.Context, p *auth.Principal, categoryID string) (intakeContext, error) {
	var out intakeContext
	if strings.TrimSpace(categoryID) != "" {
		c, err := s.category(ctx, p, strings.TrimSpace(categoryID))
		if err != nil {
			return out, err
		}
		out.category = &c
		if c.WorkspaceID != "" {
			w, err := s.workspace(ctx, p, c.WorkspaceID)
			if err != nil {
				return out, err
			}
			out.workspace = &w
		}
	}
	workspaceID := ""
	if out.workspace != nil {
		workspaceID = out.workspace.ID
	}
	fields, err := s.fields(ctx, workspaceID)
	out.fields = fields
	return out, err
}

func (s *Service) Create(ctx context.Context, p *auth.Principal, in Input, m crm.RequestMeta) (Voice, error) {
	if err := auth.Require(p, "voice:write"); err != nil {
		return Voice{}, err
	}
	intake, err := s.intakeFor(ctx, p, in.CategoryID)
	if err != nil {
		return Voice{}, err
	}
	if intake.category != nil {
		if !intake.category.Active {
			return Voice{}, errors.New("사용이 중지된 요청 유형입니다")
		}
		// A workspace type decides the broad voice type; the intake screen
		// asks only for the type the department defined.
		if strings.TrimSpace(in.VoiceType) == "" {
			in.VoiceType = intake.category.VoiceType
		}
	}
	if intake.workspace != nil && intake.category == nil {
		return Voice{}, errors.New("요청 유형을 선택해야 합니다")
	}
	if err := validate(in); err != nil {
		return Voice{}, err
	}
	// Fields are checked only where they are defined; requests outside any
	// workspace with no definitions keep accepting what they always did.
	fieldValues := orEmpty(in.CustomFields)
	if len(intake.fields) > 0 {
		if fieldValues, err = validateFieldValues(intake.fields, "INTAKE", fieldValues, true); err != nil {
			return Voice{}, err
		}
	}
	// Reuse the CRM read check so a VOC cannot be filed against a customer the
	// user is not allowed to see.
	customer, err := s.CRM.GetCustomer(ctx, p, in.CustomerID)
	if err != nil {
		return Voice{}, err
	}
	owner := in.OwnerID
	if owner == "" || (!p.IsBootstrap && !p.Has("voice:manage")) {
		owner = p.UserID
	}
	severity := strings.ToUpper(in.Severity)
	if severity == "" {
		severity = "NORMAL"
	}
	channel := strings.ToUpper(in.Channel)
	if channel == "" {
		channel = "PHONE"
	}
	occurred := time.Now()
	if in.OccurredAt != nil {
		occurred = *in.OccurredAt
	}
	var responseDue, resolutionDue *time.Time
	if intake.category == nil || intake.category.SLAEnabled {
		responseDue, resolutionDue = s.slaDeadlines(ctx, in.CategoryID, severity, occurred)
	}
	fields, _ := json.Marshal(fieldValues)
	// An isolated workspace's request belongs to the owning department, so its
	// members find it through their Data Scope even when the customer is owned
	// elsewhere.
	organizationID := customer.OrganizationID
	workspaceID := ""
	if intake.workspace != nil {
		workspaceID = intake.workspace.ID
		if intake.workspace.Isolated {
			organizationID = intake.workspace.OrganizationID
		}
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Voice{}, err
	}
	defer tx.Rollback(ctx)
	var voiceNo string
	if err = tx.QueryRow(ctx, `SELECT 'VOC-'||to_char(now(),'YYYY')||'-'||lpad(nextval('customer_voice_no_seq')::text,6,'0')`).Scan(&voiceNo); err != nil {
		return Voice{}, err
	}
	id := ids.New()
	_, err = tx.Exec(ctx, `INSERT INTO customer_voices(id,voice_no,customer_id,contact_id,opportunity_id,contract_id,category_id,
		voice_type,channel,title,body,severity,status,owner_id,organization_id,occurred_at,response_due_at,resolution_due_at,
		custom_fields,created_by,updated_by,workspace_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'RECEIVED',$13,$14,$15,$16,$17,$18,$19,$19,$20)`,
		id, voiceNo, in.CustomerID, nullable(in.ContactID), nullable(in.OpportunityID), nullable(in.ContractID), nullable(in.CategoryID),
		strings.ToUpper(in.VoiceType), channel, strings.TrimSpace(in.Title), nullable(in.Body), severity, owner,
		nullable(organizationID), occurred, responseDue, resolutionDue, fields, p.UserID, nullable(workspaceID))
	if err != nil {
		return Voice{}, err
	}
	if err = appendEvent(ctx, tx, id, "CREATED", "", "RECEIVED", in.Body, p.UserID); err != nil {
		return Voice{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Voice{}, err
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: m.Channel, Action: "VOICE_CREATE",
		Resource: "customer_voice", ResourceID: id,
		After: map[string]any{"voiceNo": voiceNo, "customerId": in.CustomerID, "voiceType": in.VoiceType, "severity": severity, "title": in.Title},
		IP:    m.IP, RequestID: m.RequestID, UserAgent: m.UserAgent})
	out, _, err := s.Get(ctx, p, id)
	if err == nil {
		s.notifyAssigned(ctx, p, out)
	}
	return out, err
}

func orEmpty(v map[string]any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	return v
}

func appendEvent(ctx context.Context, tx pgx.Tx, voiceID, eventType, from, to, note, actorID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO customer_voice_events(id,voice_id,event_type,from_status,to_status,note,actor_id)
		VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),$7)`,
		ids.New(), voiceID, eventType, from, to, strings.TrimSpace(note), actorID)
	return err
}

func (s *Service) Update(ctx context.Context, p *auth.Principal, id string, in UpdateInput, m crm.RequestMeta) (Voice, error) {
	if err := auth.Require(p, "voice:write"); err != nil {
		return Voice{}, err
	}
	before, _, err := s.Get(ctx, p, id)
	if err != nil {
		return Voice{}, err
	}
	// Handling someone else's ticket is a lead responsibility.
	if before.OwnerID != p.UserID && !p.IsBootstrap && !p.Has("voice:manage") {
		return Voice{}, errors.New("only the assigned owner can update this record; voice:manage permission is required")
	}
	if in.Version != 0 && in.Version != before.Version {
		return Voice{}, errors.New("this record was changed by another user")
	}
	status := strings.ToUpper(in.Status)
	if status == "" {
		status = before.Status
	}
	if !statuses[status] {
		return Voice{}, errors.New("invalid status")
	}
	if !canTransition(before.Status, status) {
		return Voice{}, fmt.Errorf("%s 상태에서 %s로 바로 변경할 수 없습니다", before.Status, status)
	}
	if status == "RESOLVED" && strings.TrimSpace(coalesce(in.Resolution, before.Resolution)) == "" {
		return Voice{}, errors.New("resolution is required before a record can be resolved")
	}
	evidence := strings.ToUpper(strings.TrimSpace(in.CauseEvidence))
	if evidence != "" && !causeEvidence[evidence] {
		return Voice{}, errors.New("causeEvidence must be CONFIRMED, PRESUMED or UNIDENTIFIED")
	}
	if in.CategoryID != "" && in.CategoryID != before.CategoryID {
		next, err := s.category(ctx, p, in.CategoryID)
		if err != nil {
			return Voice{}, err
		}
		// Moving a case into another workspace would move it across a data
		// boundary; that is a new intake, not an edit.
		if next.WorkspaceID != before.WorkspaceID {
			return Voice{}, errors.New("다른 업무 영역의 요청 유형으로는 변경할 수 없습니다")
		}
	}
	intake, err := s.intakeFor(ctx, p, before.CategoryID)
	if err != nil {
		return Voice{}, err
	}
	fieldPatch := map[string]any{}
	if len(in.CustomFields) > 0 {
		if len(intake.fields) == 0 && before.WorkspaceID == "" {
			fieldPatch = in.CustomFields
		} else if fieldPatch, err = validateFieldValues(intake.fields, "", in.CustomFields, false); err != nil {
			return Voice{}, err
		}
	}
	resolving := status == "RESOLVED" && before.Status != "RESOLVED"
	if resolving {
		merged := map[string]any{}
		for k, v := range before.CustomFields {
			merged[k] = v
		}
		for k, v := range fieldPatch {
			merged[k] = v
		}
		// Required resolution fields are checked on the values the case will
		// hold, so a value recorded earlier still counts.
		resolutionFields := phaseFields(intake.fields, "RESOLUTION")
		if _, err = validateFieldValues(resolutionFields, "RESOLUTION", onlyDefined(resolutionFields, merged), true); err != nil {
			return Voice{}, err
		}
		if intake.workspace != nil && intake.workspace.KnowledgeGate && coalesce(evidence, before.CauseEvidence) == "" {
			return Voice{}, errors.New("원인 근거(확인·추정·미특정)를 선택해야 해결로 변경할 수 있습니다")
		}
	}
	reopening := terminal[before.Status] && !terminal[status]
	patch, _ := json.Marshal(fieldPatch)
	severity := strings.ToUpper(in.Severity)
	if severity == "" {
		severity = before.Severity
	}
	if !severities[severity] {
		return Voice{}, errors.New("invalid severity")
	}
	if in.SatisfactionScore != nil && (*in.SatisfactionScore < 1 || *in.SatisfactionScore > 5) {
		return Voice{}, errors.New("satisfactionScore must be between 1 and 5")
	}
	owner := in.OwnerID
	if owner == "" {
		owner = before.OwnerID
	}
	if owner != before.OwnerID && !p.IsBootstrap && !p.Has("voice:manage") {
		return Voice{}, errors.New("voice:manage permission is required to reassign a record")
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Voice{}, err
	}
	defer tx.Rollback(ctx)
	// Timestamps advance only on the first transition into each state so the SLA
	// measurement is not reset by a later edit.
	command, err := tx.Exec(ctx, `UPDATE customer_voices SET
		title=COALESCE(NULLIF($2,''),title),
		body=COALESCE(NULLIF($3,''),body),
		severity=$4,
		status=$5,
		owner_id=$6,
		category_id=COALESCE($7::uuid,category_id),
		contact_id=COALESCE($8::uuid,contact_id),
		resolution=COALESCE(NULLIF($9,''),resolution),
		root_cause=COALESCE(NULLIF($10,''),root_cause),
		preventive_action=COALESCE(NULLIF($11,''),preventive_action),
		satisfaction_score=COALESCE($12,satisfaction_score),
		satisfaction_comment=COALESCE(NULLIF($13,''),satisfaction_comment),
		cause_evidence=COALESCE(NULLIF($16,''),cause_evidence),
		custom_fields=custom_fields||$17::jsonb,
		knowledge_status=CASE WHEN $18 THEN 'UNREVIEWED' ELSE knowledge_status END,
		knowledge_reviewed_by=CASE WHEN $18 THEN NULL ELSE knowledge_reviewed_by END,
		knowledge_reviewed_at=CASE WHEN $18 THEN NULL ELSE knowledge_reviewed_at END,
		first_responded_at=CASE WHEN first_responded_at IS NULL AND $5<>'RECEIVED' THEN now() ELSE first_responded_at END,
		resolved_at=CASE WHEN $5 IN ('RESOLVED','CLOSED') AND resolved_at IS NULL THEN now()
			WHEN $5 NOT IN ('RESOLVED','CLOSED') THEN NULL ELSE resolved_at END,
		closed_at=CASE WHEN $5='CLOSED' AND closed_at IS NULL THEN now()
			WHEN $5<>'CLOSED' THEN NULL ELSE closed_at END,
		updated_by=$14,updated_at=now(),version=version+1
		WHERE id=$1 AND version=$15`,
		id, strings.TrimSpace(in.Title), strings.TrimSpace(in.Body), severity, status, owner,
		nullable(in.CategoryID), nullable(in.ContactID),
		strings.TrimSpace(in.Resolution), strings.TrimSpace(in.RootCause), strings.TrimSpace(in.PreventiveAction),
		in.SatisfactionScore, strings.TrimSpace(in.SatisfactionComment), p.UserID, before.Version,
		evidence, string(patch), reopening && before.KnowledgeStatus != "UNREVIEWED")
	if err != nil {
		return Voice{}, err
	}
	if command.RowsAffected() == 0 {
		return Voice{}, errors.New("this record was changed by another user")
	}
	if status != before.Status {
		event := "STATUS_CHANGE"
		if status == "RESOLVED" {
			event = "RESOLVED"
		} else if terminal[before.Status] {
			event = "REOPENED"
		}
		if err = appendEvent(ctx, tx, id, event, before.Status, status, in.Note, p.UserID); err != nil {
			return Voice{}, err
		}
	} else if strings.TrimSpace(in.Note) != "" {
		if err = appendEvent(ctx, tx, id, "COMMENT", "", "", in.Note, p.UserID); err != nil {
			return Voice{}, err
		}
	}
	if reopening && before.KnowledgeStatus != "UNREVIEWED" {
		// A reopened case may no longer mean what the reviewer approved, so it
		// goes back through the gate instead of staying agent knowledge.
		if err = appendEvent(ctx, tx, id, "KNOWLEDGE_REVIEW", "", "", "재처리로 지식 반영 상태를 미검토로 되돌렸습니다. (이전: "+knowledgeLabel(before.KnowledgeStatus)+")", p.UserID); err != nil {
			return Voice{}, err
		}
	}
	if owner != before.OwnerID {
		if err = appendEvent(ctx, tx, id, "ASSIGNED", "", "", "담당자를 변경했습니다.", p.UserID); err != nil {
			return Voice{}, err
		}
	}
	if in.SatisfactionScore != nil {
		if err = appendEvent(ctx, tx, id, "SATISFACTION", "", "", in.SatisfactionComment, p.UserID); err != nil {
			return Voice{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Voice{}, err
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: m.Channel, Action: "VOICE_UPDATE",
		Resource: "customer_voice", ResourceID: id,
		Before: map[string]any{"status": before.Status, "severity": before.Severity, "ownerId": before.OwnerID},
		After:  map[string]any{"status": status, "severity": severity, "ownerId": owner},
		IP:     m.IP, RequestID: m.RequestID, UserAgent: m.UserAgent})
	out, _, err := s.Get(ctx, p, id)
	if err == nil && owner != before.OwnerID {
		s.notifyAssigned(ctx, p, out)
	}
	return out, err
}

func phaseFields(defs []Field, phase string) []Field {
	out := []Field{}
	for _, f := range defs {
		if f.Phase == phase {
			out = append(out, f)
		}
	}
	return out
}

// onlyDefined keeps the values a definition list knows, so a check of one
// phase is not tripped by keys left over from a retired field.
func onlyDefined(defs []Field, values map[string]any) map[string]any {
	out := map[string]any{}
	for _, f := range defs {
		if v, ok := values[f.Key]; ok {
			out[f.Key] = v
		}
	}
	return out
}

func coalesce(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Comment records a customer contact or an internal note without changing state.
func (s *Service) Comment(ctx context.Context, p *auth.Principal, id, eventType, note string, m crm.RequestMeta) ([]Event, error) {
	if err := auth.Require(p, "voice:write"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(note) == "" {
		return nil, errors.New("note is required")
	}
	eventType = strings.ToUpper(eventType)
	if eventType != "COMMENT" && eventType != "CUSTOMER_CONTACT" && eventType != "ESCALATED" {
		return nil, errors.New("invalid eventType")
	}
	if _, _, err := s.Get(ctx, p, id); err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = appendEvent(ctx, tx, id, eventType, "", "", note, p.UserID); err != nil {
		return nil, err
	}
	// A recorded customer contact satisfies the first-response SLA.
	if eventType == "CUSTOMER_CONTACT" {
		if _, err = tx.Exec(ctx, `UPDATE customer_voices SET first_responded_at=COALESCE(first_responded_at,now()),updated_at=now() WHERE id=$1`, id); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: m.Channel, Action: "VOICE_" + eventType,
		Resource: "customer_voice", ResourceID: id, After: map[string]any{"note": note},
		IP: m.IP, RequestID: m.RequestID, UserAgent: m.UserAgent})
	return s.events(ctx, id)
}

// Summary powers the customer 360 panel and the dashboard tile.
type Summary struct {
	Open              int      `json:"open"`
	Overdue           int      `json:"overdue"`
	Critical          int      `json:"critical"`
	ResolvedLast30    int      `json:"resolvedLast30"`
	ChurnRisk         int      `json:"churnRisk"`
	AverageResolution float64  `json:"averageResolutionHours"`
	Satisfaction      *float64 `json:"satisfactionAverage,omitempty"`
	// KnowledgePending counts closed cases no reviewer has judged yet: the
	// queue behind the knowledge gate.
	KnowledgePending int      `json:"knowledgePending"`
	ByType           []Bucket `json:"byType"`
}

type Bucket struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

func (s *Service) Summary(ctx context.Context, p *auth.Principal, customerID, workspaceID string) (Summary, error) {
	if err := auth.Require(p, "voice:read"); err != nil {
		return Summary{}, err
	}
	var out Summary
	err := s.DB.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE v.status NOT IN ('RESOLVED','CLOSED','REJECTED')),
		count(*) FILTER (WHERE v.status NOT IN ('RESOLVED','CLOSED','REJECTED') AND (
			(v.response_due_at IS NOT NULL AND v.first_responded_at IS NULL AND v.response_due_at < now())
			OR (v.resolution_due_at IS NOT NULL AND v.resolved_at IS NULL AND v.resolution_due_at < now()))),
		count(*) FILTER (WHERE v.status NOT IN ('RESOLVED','CLOSED','REJECTED') AND v.severity='CRITICAL'),
		count(*) FILTER (WHERE v.resolved_at IS NOT NULL AND v.resolved_at > now()-interval '30 days'),
		count(*) FILTER (WHERE v.voice_type='CHURN_RISK' AND v.status NOT IN ('RESOLVED','CLOSED','REJECTED')),
		COALESCE(avg(EXTRACT(EPOCH FROM (v.resolved_at-v.occurred_at))/3600) FILTER (WHERE v.resolved_at IS NOT NULL),0),
		avg(v.satisfaction_score) FILTER (WHERE v.satisfaction_score IS NOT NULL),
		count(*) FILTER (WHERE v.status IN ('RESOLVED','CLOSED') AND v.knowledge_status='UNREVIEWED')
		FROM customer_voices v WHERE `+VisibleSQL(p, "v")+` AND ($4='' OR v.customer_id::text=$4) AND ($5='' OR v.workspace_id::text=$5)`,
		p.DataScope, p.UserID, orgArg(p), customerID, workspaceID).
		Scan(&out.Open, &out.Overdue, &out.Critical, &out.ResolvedLast30, &out.ChurnRisk, &out.AverageResolution, &out.Satisfaction, &out.KnowledgePending)
	if err != nil {
		return Summary{}, err
	}
	rows, err := s.DB.Query(ctx, `SELECT v.voice_type,count(*) FROM customer_voices v
		WHERE `+VisibleSQL(p, "v")+` AND ($4='' OR v.customer_id::text=$4) AND ($5='' OR v.workspace_id::text=$5)
		GROUP BY v.voice_type ORDER BY count(*) DESC`, p.DataScope, p.UserID, orgArg(p), customerID, workspaceID)
	if err != nil {
		return Summary{}, err
	}
	defer rows.Close()
	out.ByType = []Bucket{}
	for rows.Next() {
		var b Bucket
		if err = rows.Scan(&b.Key, &b.Count); err != nil {
			return Summary{}, err
		}
		out.ByType = append(out.ByType, b)
	}
	return out, rows.Err()
}

// ValidateCategory is shared with the administrator handler so the console and
// the service agree on what a usable SLA looks like.
func ValidateCategory(code, name, voiceType string, responseHours, resolutionHours int) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" {
		return errors.New("code and name are required")
	}
	if !voiceTypes[strings.ToUpper(voiceType)] {
		return errors.New("invalid voiceType")
	}
	if responseHours < 1 || resolutionHours < 1 {
		return errors.New("responseHours and resolutionHours must be at least 1")
	}
	if responseHours > resolutionHours {
		return errors.New("responseHours cannot exceed resolutionHours")
	}
	return nil
}

func (s *Service) CreateCategory(ctx context.Context, c Category) (string, error) {
	id := ids.New()
	_, err := s.DB.Exec(ctx, `INSERT INTO voice_categories(id,code,name,voice_type,response_hours,resolution_hours,display_order,workspace_id,sla_enabled)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id, strings.ToUpper(strings.TrimSpace(c.Code)), strings.TrimSpace(c.Name), strings.ToUpper(c.VoiceType),
		c.ResponseHours, c.ResolutionHours, c.DisplayOrder, nullable(c.WorkspaceID), c.SLAEnabled)
	return id, err
}
