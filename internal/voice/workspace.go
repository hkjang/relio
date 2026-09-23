package voice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/crm"
)

// A workspace lets one department run its own intake on the shared VOC engine:
// its own request types, fields collected at intake and at resolution, its own
// customer code, and — for data other departments must not see — isolation
// from their screens and from the sales metrics that read customer requests.
// Requests outside any workspace (workspace_id NULL) behave as they always did.

type Workspace struct {
	ID                  string     `json:"id"`
	Code                string     `json:"code"`
	Name                string     `json:"name"`
	Description         string     `json:"description,omitempty"`
	OrganizationID      string     `json:"organizationId,omitempty"`
	OrganizationName    string     `json:"organizationName,omitempty"`
	Isolated            bool       `json:"isolated"`
	KnowledgeGate       bool       `json:"knowledgeGate"`
	CustomerCodeLabel   string     `json:"customerCodeLabel,omitempty"`
	CustomerCodePattern string     `json:"customerCodePattern,omitempty"`
	Active              bool       `json:"active"`
	DisplayOrder        int        `json:"displayOrder"`
	Fields              []Field    `json:"fields"`
	Categories          []Category `json:"categories"`
	// SLAEnabled is true when any of the workspace's types still keeps a
	// deadline, so screens know whether deadline columns mean anything here.
	SLAEnabled bool `json:"slaEnabled"`
}

// Field is a VOICE custom field definition, collected at intake or resolution.
type Field struct {
	ID           string   `json:"id"`
	WorkspaceID  string   `json:"workspaceId,omitempty"`
	Key          string   `json:"key"`
	Label        string   `json:"label"`
	Type         string   `json:"type"`
	Required     bool     `json:"required"`
	Options      []string `json:"options,omitempty"`
	Phase        string   `json:"phase"`
	HelpText     string   `json:"helpText,omitempty"`
	DisplayOrder int      `json:"displayOrder"`
}

// Cause evidence levels and knowledge states. The labels are the words the
// requirement uses, so screens, exports and agents say the same thing.
var (
	CauseEvidenceLevels = []string{"CONFIRMED", "PRESUMED", "UNIDENTIFIED"}
	KnowledgeStatuses   = []string{"UNREVIEWED", "IN_REVIEW", "APPROVED", "EXCLUDED"}
	causeEvidence       = setOf(CauseEvidenceLevels)
	knowledgeStatuses   = setOf(KnowledgeStatuses)
)

func setOf(values []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}

func isAdmin(p *auth.Principal) bool { return crm.IsAdmin(p) }

func memberSQL(orgColumn string) string { return crm.MemberOfSQL(orgColumn) }

// VisibleSQL is the Data Scope predicate for requests with workspace isolation
// on top. It expects the same $1..$3 arguments as crm.ScopeSQL.
func VisibleSQL(p *auth.Principal, alias string) string {
	scope := crm.ScopeSQL(alias)
	if isAdmin(p) {
		return scope
	}
	return scope + fmt.Sprintf(` AND (%[1]s.workspace_id IS NULL OR NOT EXISTS(
		SELECT 1 FROM voice_workspaces iso WHERE iso.id=%[1]s.workspace_id AND iso.isolated AND NOT `+memberSQL("iso.organization_id")+`))`, alias)
}

// SalesSignalSQL keeps an isolated workspace's requests out of churn risk,
// intelligence signals and every other sales metric. It needs no arguments.
func SalesSignalSQL(alias string) string {
	return fmt.Sprintf(`NOT EXISTS(SELECT 1 FROM voice_workspaces iso WHERE iso.id=%s.workspace_id AND iso.isolated)`, alias)
}

// bindCaller references the three caller arguments in queries whose access
// rule may not use them all (an administrator's rule is simply true), since
// PostgreSQL refuses a parameter whose type it cannot infer.
const bindCaller = `(($1::text,$2::text,$3::uuid) IS NOT DISTINCT FROM ($1::text,$2::text,$3::uuid))`

// workspaceAccessSQL is the same rule applied to a workspace row itself.
func workspaceAccessSQL(p *auth.Principal) string {
	if isAdmin(p) {
		return "true"
	}
	return `(NOT w.isolated OR ` + memberSQL("w.organization_id") + `)`
}

// Workspaces lists the workspaces the caller may use, with their types and
// fields, which is everything an intake screen or an agent needs.
func (s *Service) Workspaces(ctx context.Context, p *auth.Principal, includeInactive bool) ([]Workspace, error) {
	if err := auth.Require(p, "voice:read"); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT w.id,w.code,w.name,COALESCE(w.description,''),COALESCE(w.organization_id::text,''),COALESCE(o.name,''),
		w.isolated,w.knowledge_gate,COALESCE(w.customer_code_label,''),COALESCE(w.customer_code_pattern,''),w.active,w.display_order
		FROM voice_workspaces w LEFT JOIN organizations o ON o.id=w.organization_id
		WHERE (w.active OR $4) AND `+workspaceAccessSQL(p)+` AND `+bindCaller+`
		ORDER BY w.display_order,w.name`, p.DataScope, p.UserID, orgArg(p), includeInactive)
	if err != nil {
		return nil, err
	}
	out := []Workspace{}
	for rows.Next() {
		var w Workspace
		if err = rows.Scan(&w.ID, &w.Code, &w.Name, &w.Description, &w.OrganizationID, &w.OrganizationName,
			&w.Isolated, &w.KnowledgeGate, &w.CustomerCodeLabel, &w.CustomerCodePattern, &w.Active, &w.DisplayOrder); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, w)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	categories, err := s.Categories(ctx, p, includeInactive)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Fields, err = s.fields(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Categories = []Category{}
		for _, c := range categories {
			if c.WorkspaceID == out[i].ID {
				out[i].Categories = append(out[i].Categories, c)
				out[i].SLAEnabled = out[i].SLAEnabled || c.SLAEnabled
			}
		}
	}
	return out, nil
}

// workspace loads one workspace the caller may use.
func (s *Service) workspace(ctx context.Context, p *auth.Principal, id string) (Workspace, error) {
	items, err := s.Workspaces(ctx, p, false)
	if err != nil {
		return Workspace{}, err
	}
	for _, w := range items {
		if w.ID == id || strings.EqualFold(w.Code, id) {
			return w, nil
		}
	}
	return Workspace{}, errors.New("voice workspace not found")
}

// fields returns the VOICE field definitions for a workspace; an empty id
// means the fields of requests that belong to no workspace.
func (s *Service) fields(ctx context.Context, workspaceID string) ([]Field, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,COALESCE(workspace_id::text,''),field_key,label,field_type,required,options,COALESCE(phase,'INTAKE'),COALESCE(help_text,''),display_order
		FROM custom_field_definitions
		WHERE entity_type='VOICE' AND active AND COALESCE(workspace_id::text,'')=$1
		ORDER BY display_order,label`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Field{}
	for rows.Next() {
		var f Field
		var options []byte
		if err = rows.Scan(&f.ID, &f.WorkspaceID, &f.Key, &f.Label, &f.Type, &f.Required, &options, &f.Phase, &f.HelpText, &f.DisplayOrder); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(options, &f.Options)
		out = append(out, f)
	}
	return out, rows.Err()
}

var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// validateFieldValues checks submitted values against the definitions. Only
// defined keys are accepted, select values must be one of the options, and
// the required fields of the given phase must be present when enforce is set.
// It returns the cleaned values, keyed as defined.
func validateFieldValues(defs []Field, phase string, values map[string]any, enforce bool) (map[string]any, error) {
	byKey := map[string]Field{}
	for _, f := range defs {
		byKey[f.Key] = f
	}
	out := map[string]any{}
	problems := []string{}
	for key, raw := range values {
		f, ok := byKey[key]
		if !ok {
			problems = append(problems, fmt.Sprintf("알 수 없는 항목입니다: %s", key))
			continue
		}
		if raw == nil {
			continue
		}
		value, problem := fieldValue(f, raw)
		if problem != "" {
			problems = append(problems, problem)
			continue
		}
		if value != nil {
			out[key] = value
		}
	}
	if enforce {
		for _, f := range defs {
			if f.Phase == phase && f.Required {
				if _, ok := out[f.Key]; !ok {
					problems = append(problems, fmt.Sprintf("%s을(를) 입력해야 합니다", f.Label))
				}
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, errors.New(strings.Join(problems, " / "))
	}
	return out, nil
}

// fieldValue normalises one value, returning nil for "no value".
func fieldValue(f Field, raw any) (any, string) {
	text := func() (string, bool) {
		switch v := raw.(type) {
		case string:
			return strings.TrimSpace(v), true
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64), true
		}
		return "", false
	}
	switch f.Type {
	case "Select":
		v, ok := text()
		if !ok {
			return nil, fmt.Sprintf("%s은(는) 선택값이어야 합니다", f.Label)
		}
		if v == "" {
			return nil, ""
		}
		for _, option := range f.Options {
			if option == v {
				return v, ""
			}
		}
		return nil, fmt.Sprintf("%s은(는) 다음 중 하나여야 합니다: %s (받은 값: %s)", f.Label, strings.Join(f.Options, ", "), v)
	case "Multi Select":
		items, ok := raw.([]any)
		if !ok {
			return nil, fmt.Sprintf("%s은(는) 선택값 목록이어야 합니다", f.Label)
		}
		out := []string{}
		for _, item := range items {
			v, _ := item.(string)
			found := false
			for _, option := range f.Options {
				if option == v {
					found = true
				}
			}
			if !found {
				return nil, fmt.Sprintf("%s의 값 %v은(는) 허용되지 않습니다", f.Label, item)
			}
			out = append(out, v)
		}
		if len(out) == 0 {
			return nil, ""
		}
		return out, ""
	case "Number", "Money", "Percent":
		switch v := raw.(type) {
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Sprintf("%s은(는) 숫자여야 합니다", f.Label)
			}
			return v, ""
		case string:
			if strings.TrimSpace(v) == "" {
				return nil, ""
			}
			n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return nil, fmt.Sprintf("%s은(는) 숫자여야 합니다", f.Label)
			}
			return n, ""
		}
		return nil, fmt.Sprintf("%s은(는) 숫자여야 합니다", f.Label)
	case "Boolean":
		if v, ok := raw.(bool); ok {
			return v, ""
		}
		return nil, fmt.Sprintf("%s은(는) true 또는 false여야 합니다", f.Label)
	case "Date":
		v, ok := text()
		if !ok || (v != "" && !datePattern.MatchString(v)) {
			return nil, fmt.Sprintf("%s은(는) YYYY-MM-DD 형식이어야 합니다", f.Label)
		}
		if v == "" {
			return nil, ""
		}
		if _, err := time.Parse("2006-01-02", v); err != nil {
			return nil, fmt.Sprintf("%s은(는) 올바른 날짜여야 합니다", f.Label)
		}
		return v, ""
	default:
		v, ok := text()
		if !ok {
			return nil, fmt.Sprintf("%s은(는) 문자열이어야 합니다", f.Label)
		}
		if v == "" {
			return nil, ""
		}
		if len([]rune(v)) > 2000 {
			return nil, fmt.Sprintf("%s은(는) 2000자 이하여야 합니다", f.Label)
		}
		return v, ""
	}
}
