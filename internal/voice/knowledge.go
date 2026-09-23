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
)

// The knowledge gate. Everything a handler writes down about a case is
// history, but only what a department reviewer has checked is knowledge: an
// agent diagnosing a new complaint searches reviewed cases by default, so an
// unverified guess recorded under pressure is never reused as if it were
// established fact.

var knowledgeLabels = map[string]string{"UNREVIEWED": "미검토", "IN_REVIEW": "검토중", "APPROVED": "반영", "EXCLUDED": "제외"}

func knowledgeLabel(status string) string {
	if label, ok := knowledgeLabels[status]; ok {
		return label
	}
	return status
}

// Review records a reviewer's judgement. It needs its own permission, which a
// handler does not hold, and only a resolved or closed case can be approved.
func (s *Service) Review(ctx context.Context, p *auth.Principal, id, status, note string, m crm.RequestMeta) (Voice, error) {
	if err := auth.Require(p, "voice:knowledge-review"); err != nil {
		return Voice{}, err
	}
	// The gate is only worth something if a person moves it. A Personal Key
	// or an OAuth token is how an agent signs in, so neither may.
	if p.AuthMethod == "PERSONAL_KEY" || p.AuthMethod == "OIDC_ACCESS_TOKEN" {
		return Voice{}, errors.New("지식 반영 판정은 Relio 화면에서 검토자가 직접 해야 합니다")
	}
	status = strings.ToUpper(strings.TrimSpace(status))
	if !knowledgeStatuses[status] {
		return Voice{}, errors.New("knowledgeStatus must be UNREVIEWED, IN_REVIEW, APPROVED or EXCLUDED")
	}
	before, _, err := s.Get(ctx, p, id)
	if err != nil {
		return Voice{}, err
	}
	if status == "APPROVED" && before.Status != "RESOLVED" && before.Status != "CLOSED" {
		return Voice{}, errors.New("해결 또는 종결된 요청만 지식으로 반영할 수 있습니다")
	}
	if status == before.KnowledgeStatus && strings.TrimSpace(note) == "" {
		return before, nil
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Voice{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE customer_voices SET knowledge_status=$2,
		knowledge_reviewed_by=CASE WHEN $2='UNREVIEWED' THEN NULL ELSE $3::uuid END,
		knowledge_reviewed_at=CASE WHEN $2='UNREVIEWED' THEN NULL ELSE now() END,
		updated_at=now() WHERE id=$1`, id, status, p.UserID); err != nil {
		return Voice{}, err
	}
	text := fmt.Sprintf("지식 반영 상태: %s → %s", knowledgeLabel(before.KnowledgeStatus), knowledgeLabel(status))
	if strings.TrimSpace(note) != "" {
		text += " · " + strings.TrimSpace(note)
	}
	if err = appendEvent(ctx, tx, id, "KNOWLEDGE_REVIEW", "", "", text, p.UserID); err != nil {
		return Voice{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Voice{}, err
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: m.Channel, Action: "VOICE_KNOWLEDGE_REVIEW",
		Resource: "customer_voice", ResourceID: id,
		Before: map[string]any{"knowledgeStatus": before.KnowledgeStatus},
		After:  map[string]any{"knowledgeStatus": status, "note": strings.TrimSpace(note)},
		IP:     m.IP, RequestID: m.RequestID, UserAgent: m.UserAgent})
	out, _, err := s.Get(ctx, p, id)
	return out, err
}

// KnowledgeCase is one case as an agent sees it: enough to judge whether the
// case applies, and the two status fields that say whether it may be relied on.
type KnowledgeCase struct {
	ID              string         `json:"id"`
	VoiceNo         string         `json:"voiceNo"`
	Title           string         `json:"title"`
	CustomerSaid    string         `json:"customerSaid"`
	RootCause       string         `json:"rootCause"`
	Resolution      string         `json:"resolution"`
	CauseEvidence   string         `json:"causeEvidence"`
	KnowledgeStatus string         `json:"knowledgeStatus"`
	Category        string         `json:"category"`
	Fields          map[string]any `json:"fields"`
	CustomerName    string         `json:"customerName"`
	CustomerCode    string         `json:"customerCode,omitempty"`
	Status          string         `json:"status"`
	OccurredAt      time.Time      `json:"occurredAt"`
	ResolvedAt      *time.Time     `json:"resolvedAt,omitempty"`
}

type KnowledgeQuery struct {
	Query       string
	WorkspaceID string
	// Fields filters on exact field values, e.g. service_type=아이핀.
	Fields        map[string]string
	CauseEvidence string
	// IncludeUnreviewed widens the search to cases no reviewer has judged.
	// Excluded cases are never returned: a reviewer ruled them out.
	IncludeUnreviewed bool
	Limit             int
}

// SearchKnowledge finds resolved cases similar to a symptom or error code.
// By default only reviewed-and-approved cases are returned.
func (s *Service) SearchKnowledge(ctx context.Context, p *auth.Principal, q KnowledgeQuery) ([]KnowledgeCase, error) {
	if err := auth.Require(p, "voice:read"); err != nil {
		return nil, err
	}
	if q.Limit < 1 || q.Limit > 50 {
		q.Limit = 10
	}
	states := []string{"APPROVED"}
	if q.IncludeUnreviewed {
		states = []string{"APPROVED", "IN_REVIEW", "UNREVIEWED"}
	}
	evidence := strings.ToUpper(strings.TrimSpace(q.CauseEvidence))
	if evidence != "" && !causeEvidence[evidence] {
		return nil, errors.New("causeEvidence must be CONFIRMED, PRESUMED or UNIDENTIFIED")
	}
	filters := map[string]string{}
	for k, v := range q.Fields {
		if strings.TrimSpace(v) != "" {
			filters[k] = strings.TrimSpace(v)
		}
	}
	fieldFilter, _ := json.Marshal(filters)
	args := []any{p.DataScope, p.UserID, orgArg(p), states, q.WorkspaceID, evidence, string(fieldFilter)}
	// Every term must appear somewhere; where it appears decides the order.
	// An error code usually lives in a field or the title, so those weigh most.
	match, score := []string{}, []string{"0"}
	for i, term := range strings.Fields(q.Query) {
		if i == 8 {
			break
		}
		pattern := crm.SearchPattern(term)
		if pattern == "" {
			continue
		}
		args = append(args, pattern)
		n := len(args)
		cols := map[string]int{
			"lower(v.title)": 3,
			// Field values only: matching the JSON text would also match
			// the field keys themselves.
			"lower(COALESCE((SELECT string_agg(value,' ') FROM jsonb_each_text(v.custom_fields)),''))": 3,
			"lower(COALESCE(v.root_cause,''))": 2,
			"lower(COALESCE(v.body,''))":       1,
			"lower(COALESCE(v.resolution,''))": 1,
		}
		alternatives := []string{}
		for col, weight := range cols {
			cond := fmt.Sprintf(`%s LIKE $%d ESCAPE '\'`, col, n)
			alternatives = append(alternatives, cond)
			score = append(score, fmt.Sprintf("(CASE WHEN %s THEN %d ELSE 0 END)", cond, weight))
		}
		match = append(match, "("+strings.Join(alternatives, " OR ")+")")
	}
	where := "true"
	if len(match) > 0 {
		where = strings.Join(match, " AND ")
	}
	args = append(args, q.Limit)
	rows, err := s.DB.Query(ctx, `SELECT v.id,v.voice_no,v.title,COALESCE(v.body,''),COALESCE(v.root_cause,''),COALESCE(v.resolution,''),
		COALESCE(v.cause_evidence,''),v.knowledge_status,COALESCE(cat.name,''),v.custom_fields,c.name,COALESCE(c.customer_code,''),
		v.status,v.occurred_at,v.resolved_at
		FROM customer_voices v JOIN customers c ON c.id=v.customer_id
		LEFT JOIN voice_categories cat ON cat.id=v.category_id
		WHERE `+VisibleSQL(p, "v")+` AND v.status IN ('RESOLVED','CLOSED')
		AND v.knowledge_status=ANY($4::text[])
		AND ($5='' OR v.workspace_id::text=$5)
		AND ($6='' OR v.cause_evidence=$6)
		AND v.custom_fields @> $7::jsonb
		AND `+where+`
		ORDER BY (`+strings.Join(score, "+")+`) DESC, v.resolved_at DESC NULLS LAST
		LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KnowledgeCase{}
	for rows.Next() {
		var k KnowledgeCase
		var raw []byte
		if err = rows.Scan(&k.ID, &k.VoiceNo, &k.Title, &k.CustomerSaid, &k.RootCause, &k.Resolution, &k.CauseEvidence,
			&k.KnowledgeStatus, &k.Category, &raw, &k.CustomerName, &k.CustomerCode, &k.Status, &k.OccurredAt, &k.ResolvedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &k.Fields)
		if k.Fields == nil {
			k.Fields = map[string]any{}
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// HistoryItem is one closed case in a customer's history.
type HistoryItem struct {
	ID               string         `json:"id"`
	VoiceNo          string         `json:"voiceNo"`
	OccurredAt       time.Time      `json:"occurredAt"`
	Title            string         `json:"title"`
	Category         string         `json:"category"`
	Status           string         `json:"status"`
	RootCauseSummary string         `json:"rootCauseSummary"`
	CauseEvidence    string         `json:"causeEvidence"`
	KnowledgeStatus  string         `json:"knowledgeStatus"`
	Fields           map[string]any `json:"fields"`
	ResolvedAt       *time.Time     `json:"resolvedAt,omitempty"`
}

type CustomerHistory struct {
	CustomerID   string        `json:"customerId"`
	CustomerName string        `json:"customerName"`
	CustomerCode string        `json:"customerCode,omitempty"`
	Items        []HistoryItem `json:"items"`
	HasMore      bool          `json:"hasMore"`
}

// History returns every closed case of one customer, newest first, reviewed
// or not: it answers "what has this member asked us before", which is context,
// not diagnostic evidence, so the knowledge gate does not apply. The status
// fields are still returned so an agent can tell the two apart.
func (s *Service) History(ctx context.Context, p *auth.Principal, customerID, customerCode, workspaceID string, limit int) (CustomerHistory, error) {
	if err := auth.Require(p, "voice:read"); err != nil {
		return CustomerHistory{}, err
	}
	if limit < 1 || limit > 200 {
		limit = 30
	}
	var customer crm.Customer
	var err error
	switch {
	case strings.TrimSpace(customerCode) != "":
		customer, err = s.CRM.CustomerByCode(ctx, p, customerCode)
	case strings.TrimSpace(customerID) != "":
		customer, err = s.CRM.GetCustomer(ctx, p, customerID)
	default:
		return CustomerHistory{}, errors.New("customerCode 또는 customerId 중 하나가 필요합니다")
	}
	if err != nil {
		return CustomerHistory{}, err
	}
	rows, err := s.DB.Query(ctx, `SELECT v.id,v.voice_no,v.occurred_at,v.title,COALESCE(cat.name,''),v.status,
		COALESCE(v.root_cause,''),COALESCE(v.cause_evidence,''),v.knowledge_status,v.custom_fields,v.resolved_at
		FROM customer_voices v LEFT JOIN voice_categories cat ON cat.id=v.category_id
		WHERE `+VisibleSQL(p, "v")+` AND v.customer_id=$4 AND v.status IN ('RESOLVED','CLOSED','REJECTED')
		AND ($5='' OR v.workspace_id::text=$5)
		ORDER BY v.occurred_at DESC LIMIT $6`, p.DataScope, p.UserID, orgArg(p), customer.ID, workspaceID, limit+1)
	if err != nil {
		return CustomerHistory{}, err
	}
	defer rows.Close()
	out := CustomerHistory{CustomerID: customer.ID, CustomerName: customer.Name, CustomerCode: customer.CustomerCode, Items: []HistoryItem{}}
	for rows.Next() {
		var h HistoryItem
		var raw []byte
		if err = rows.Scan(&h.ID, &h.VoiceNo, &h.OccurredAt, &h.Title, &h.Category, &h.Status, &h.RootCauseSummary,
			&h.CauseEvidence, &h.KnowledgeStatus, &raw, &h.ResolvedAt); err != nil {
			return CustomerHistory{}, err
		}
		h.RootCauseSummary = summarize(h.RootCauseSummary, 200)
		_ = json.Unmarshal(raw, &h.Fields)
		if h.Fields == nil {
			h.Fields = map[string]any{}
		}
		out.Items = append(out.Items, h)
	}
	if err = rows.Err(); err != nil {
		return CustomerHistory{}, err
	}
	if len(out.Items) > limit {
		out.Items, out.HasMore = out.Items[:limit], true
	}
	return out, nil
}

func summarize(text string, max int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "…"
}

// ResolveInput is the structured resolution: what was done, how well the
// cause is established, and the workspace's resolution fields.
type ResolveInput struct {
	Resolution       string         `json:"resolution"`
	RootCause        string         `json:"rootCause"`
	CauseEvidence    string         `json:"causeEvidence"`
	PreventiveAction string         `json:"preventiveAction"`
	Fields           map[string]any `json:"fields"`
	Note             string         `json:"note"`
}

// Resolve closes a case the way a handler does on the screen. A case still
// at intake is first moved into progress, since the lifecycle does not allow
// a jump from intake straight to resolved; both steps are recorded.
func (s *Service) Resolve(ctx context.Context, p *auth.Principal, id string, in ResolveInput, m crm.RequestMeta) (Voice, error) {
	current, _, err := s.Get(ctx, p, id)
	if err != nil {
		return Voice{}, err
	}
	if terminal[current.Status] {
		return Voice{}, fmt.Errorf("이미 %s 상태인 요청입니다", current.Status)
	}
	if strings.TrimSpace(in.Resolution) == "" {
		return Voice{}, errors.New("해결 내용(resolution)을 입력해야 합니다")
	}
	if current.Status == "RECEIVED" {
		if current, err = s.Update(ctx, p, id, UpdateInput{Status: "IN_PROGRESS", Input: Input{Version: current.Version}, Note: "해결 처리를 위해 처리 중으로 전환했습니다."}, m); err != nil {
			return Voice{}, err
		}
	}
	return s.Update(ctx, p, id, UpdateInput{
		Input:            Input{Version: current.Version, CustomFields: in.Fields},
		Status:           "RESOLVED",
		Resolution:       in.Resolution,
		RootCause:        in.RootCause,
		CauseEvidence:    in.CauseEvidence,
		PreventiveAction: in.PreventiveAction,
		Note:             in.Note,
	}, m)
}
