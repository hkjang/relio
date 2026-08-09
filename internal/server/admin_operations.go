package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/config"
	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/platform/database"
	"github.com/hkjang/relio/internal/platform/httpx"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/hkjang/relio/internal/platform/version"
)

func (s *Server) adminOrganizations(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT id,COALESCE(parent_id::text,''),name,code,org_type,active,created_at FROM organizations ORDER BY code`)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, parent, name, code, typ string
		var active bool
		var created time.Time
		if err = rows.Scan(&id, &parent, &name, &code, &typ, &active, &created); err != nil {
			s.serviceError(w, r, err)
			return
		}
		items = append(items, map[string]any{"id": id, "parentId": parent, "name": name, "code": code, "type": typ, "active": active, "createdAt": created})
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}
func (s *Server) createOrganization(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := requireAdmin(p, true); err != nil {
		s.serviceError(w, r, err)
		return
	}
	var in struct {
		ParentID string `json:"parentId"`
		Name     string `json:"name"`
		Code     string `json:"code"`
		Type     string `json:"type"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if in.Name == "" || in.Code == "" {
		s.serviceError(w, r, errors.New("name and code are required"))
		return
	}
	if in.Type == "" {
		in.Type = "DEPARTMENT"
	}
	id := ids.New()
	_, err := s.DB.Exec(r.Context(), `INSERT INTO organizations(id,parent_id,name,code,org_type) VALUES($1,$2,$3,$4,$5)`, id, nullAdmin(in.ParentID), in.Name, strings.ToUpper(in.Code), strings.ToUpper(in.Type))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "ORGANIZATION_CREATE", Resource: "organization", ResourceID: id, After: in, IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
	httpx.JSON(w, 201, map[string]any{"id": id})
}

func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	limit := httpx.IntQuery(r, "limit", 100, 1, 500)
	channel := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("channel")))
	resource := strings.TrimSpace(r.URL.Query().Get("resource"))
	action := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("action")))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	rows, err := s.DB.Query(r.Context(), `SELECT id,COALESCE(actor_id::text,''),COALESCE(actor_name,''),channel,action,resource,COALESCE(resource_id,''),before_data,after_data,COALESCE(ip::text,''),COALESCE(request_id,''),COALESCE(user_agent,''),occurred_at
		FROM audit_logs
		WHERE ($1='' OR channel=$1)
		  AND ($2='' OR resource=$2)
		  AND ($3='' OR action=$3)
		  AND ($4='' OR COALESCE(actor_name,'') ILIKE '%' || $4 || '%' OR action ILIKE '%' || $4 || '%' OR resource ILIKE '%' || $4 || '%' OR COALESCE(resource_id,'') ILIKE '%' || $4 || '%' OR COALESCE(request_id,'') ILIKE '%' || $4 || '%')
		ORDER BY occurred_at DESC LIMIT $5`, channel, resource, action, query, limit)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, actorID, actor, channel, action, resource, resourceID, ip, requestID, ua string
		var before, after []byte
		var occurred time.Time
		if err = rows.Scan(&id, &actorID, &actor, &channel, &action, &resource, &resourceID, &before, &after, &ip, &requestID, &ua, &occurred); err != nil {
			s.serviceError(w, r, err)
			return
		}
		var b, a any
		_ = json.Unmarshal(before, &b)
		_ = json.Unmarshal(after, &a)
		items = append(items, map[string]any{"id": id, "actorId": actorID, "actor": actor, "channel": channel, "action": action, "resource": resource, "resourceId": resourceID, "before": b, "after": a, "ip": ip, "requestId": requestID, "userAgent": ua, "occurredAt": occurred})
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}

type adminDiagnosticCheck struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Status   string `json:"status"`
	Summary  string `json:"summary"`
	Route    string `json:"route,omitempty"`
	Required bool   `json:"required"`
}

type adminActionItem struct {
	Severity    string `json:"severity"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Route       string `json:"route,omitempty"`
}

type adminRecentJob struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Status      string    `json:"status"`
	Attempts    int       `json:"attempts"`
	MaxAttempts int       `json:"maxAttempts"`
	RunAt       time.Time `json:"runAt"`
	LastError   string    `json:"lastError,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type adminOperationsSnapshot struct {
	Application    any                    `json:"application"`
	Database       map[string]any         `json:"database"`
	Jobs           map[string]int         `json:"jobs"`
	Counts         map[string]int         `json:"counts"`
	Features       map[string]any         `json:"features"`
	Diagnostics    []adminDiagnosticCheck `json:"diagnostics"`
	Actions        []adminActionItem      `json:"actions"`
	RecentJobs     []adminRecentJob       `json:"recentJobs"`
	ReadinessScore int                    `json:"readinessScore"`
	UptimeSeconds  int                    `json:"uptimeSeconds"`
	GeneratedAt    time.Time              `json:"generatedAt"`
}

func diagnosticReadiness(checks []adminDiagnosticCheck) int {
	required, healthy := 0, 0
	for _, check := range checks {
		if !check.Required {
			continue
		}
		required++
		if check.Status == "HEALTHY" {
			healthy++
		}
	}
	if required == 0 {
		return 100
	}
	return healthy * 100 / required
}

var diagnosticSecrets = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|passwd|pwd|client_secret|token|authorization)\s*[:=]\s*[^\s,;]+`),
	regexp.MustCompile(`(?i)postgres(?:ql)?://[^@\s]+@`),
}

func redactDiagnostic(value string) string {
	value = diagnosticSecrets[0].ReplaceAllString(value, "$1=***")
	return diagnosticSecrets[1].ReplaceAllString(value, "postgres://***@")
}

func (s *Server) collectAdminOperations(ctx context.Context) (adminOperationsSnapshot, error) {
	migration := database.MigrationStatus(ctx, s.DB)
	var databaseName, serverVersion string
	var databaseSize int64
	if err := s.DB.QueryRow(ctx, `SELECT current_database(),version(),pg_database_size(current_database())`).Scan(&databaseName, &serverVersion, &databaseSize); err != nil {
		return adminOperationsSnapshot{}, err
	}

	counts := map[string]int{}
	var readyJobs, runningJobs, failedJobs int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='READY'),count(*) FILTER(WHERE status='RUNNING'),count(*) FILTER(WHERE status='FAILED') FROM jobs`).Scan(&readyJobs, &runningJobs, &failedJobs); err != nil {
		return adminOperationsSnapshot{}, err
	}
	var users, activeUsers, organizations, roles, pipelineStages, dealHealthRules int
	var activeKeys, approvalPolicies, audit24h, customers, openOpportunities int
	if err := s.DB.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM users),
		(SELECT count(*) FROM users WHERE active),
		(SELECT count(*) FROM organizations WHERE active),
		(SELECT count(*) FROM roles),
		(SELECT count(*) FROM pipeline_stages WHERE active),
		(SELECT count(*) FROM deal_health_rules WHERE active),
		(SELECT count(*) FROM personal_keys WHERE status IN ('ACTIVE','ROTATING')),
		(SELECT count(*) FROM approval_policies WHERE active),
		(SELECT count(*) FROM audit_logs WHERE occurred_at >= now() - interval '24 hours'),
		(SELECT count(*) FROM customers WHERE merged_into_id IS NULL),
		(SELECT count(*) FROM opportunities WHERE status='OPEN')`).Scan(
		&users, &activeUsers, &organizations, &roles, &pipelineStages, &dealHealthRules, &activeKeys,
		&approvalPolicies, &audit24h, &customers, &openOpportunities); err != nil {
		return adminOperationsSnapshot{}, err
	}
	counts["users"], counts["activeUsers"] = users, activeUsers
	counts["organizations"], counts["roles"] = organizations, roles
	counts["pipelineStages"], counts["dealHealthRules"] = pipelineStages, dealHealthRules
	counts["activeKeys"], counts["approvalPolicies"] = activeKeys, approvalPolicies
	counts["audit24h"], counts["customers"] = audit24h, customers
	counts["openOpportunities"] = openOpportunities

	var localLogin, apiEnabled, mcpEnabled bool
	var serviceURL string
	if err := s.DB.QueryRow(ctx, `SELECT
		COALESCE((SELECT (value #>> '{}')::boolean FROM system_settings WHERE namespace='auth' AND key='local_login_enabled'),true),
		COALESCE((SELECT (value #>> '{}')::boolean FROM system_settings WHERE namespace='api' AND key='enabled'),true),
		COALESCE((SELECT (value #>> '{}')::boolean FROM system_settings WHERE namespace='mcp' AND key='enabled'),true),
		COALESCE((SELECT value #>> '{}' FROM system_settings WHERE namespace='system' AND key='service_url'),'')`).Scan(&localLogin, &apiEnabled, &mcpEnabled, &serviceURL); err != nil {
		return adminOperationsSnapshot{}, err
	}

	var oidcEnabled, oidcConfigured bool
	if err := s.DB.QueryRow(ctx, `SELECT
		COALESCE(bool_or(enabled),false),
		COALESCE(bool_or(enabled AND issuer_url<>'' AND client_id<>'' AND client_secret_encrypted<>''),false)
		FROM oidc_providers`).Scan(&oidcEnabled, &oidcConfigured); err != nil {
		return adminOperationsSnapshot{}, err
	}

	masterKeyStatus := "HEALTHY"
	masterKeySummary := "Instance Master Key를 안전하게 읽을 수 있습니다."
	if info, err := os.Stat(config.MasterKeyPath); err != nil || info.Size() != 32 {
		masterKeyStatus = "CRITICAL"
		masterKeySummary = "Instance Master Key 파일을 확인할 수 없습니다."
	}
	storageStatus := "HEALTHY"
	storageSummary := config.DataDirectory + " Persistent Volume이 연결되어 있습니다."
	if info, err := os.Stat(config.DataDirectory); err != nil || !info.IsDir() {
		storageStatus = "CRITICAL"
		storageSummary = config.DataDirectory + " Persistent Volume을 확인할 수 없습니다."
	}
	serviceStatus := "HEALTHY"
	serviceSummary := "Service URL이 " + serviceURL + "로 설정되어 있습니다."
	if serviceURL == "" || strings.Contains(serviceURL, "localhost") {
		serviceStatus = "WARNING"
		serviceSummary = "운영용 Service URL을 확인하세요. 현재 값: " + serviceURL
	}
	oidcStatus, oidcSummary := "DISABLED", "Keycloak SSO를 사용하지 않습니다. Bootstrap Admin은 계속 사용할 수 있습니다."
	if oidcEnabled && oidcConfigured {
		oidcStatus, oidcSummary = "HEALTHY", "Keycloak OIDC 필수 설정이 준비되어 있습니다."
	} else if oidcEnabled {
		oidcStatus, oidcSummary = "WARNING", "SSO가 활성화되었지만 Issuer, Client ID 또는 Client Secret이 누락되었습니다."
	}
	approvalStatus, approvalSummary := "DISABLED", "승인 정책이 없어 검토·승인 UI가 숨겨져 있습니다."
	if counts["approvalPolicies"] > 0 {
		approvalStatus, approvalSummary = "HEALTHY", "활성 승인 정책이 적용되고 있습니다."
	}
	jobStatus, jobSummary := "HEALTHY", "실패한 Background Job이 없습니다."
	if failedJobs > 0 {
		jobStatus, jobSummary = "WARNING", "실패한 Background Job을 확인하세요."
	}
	checks := []adminDiagnosticCheck{
		{Key: "postgresql", Label: "PostgreSQL", Status: "HEALTHY", Summary: "Database 연결 및 조회가 정상입니다.", Required: true},
		{Key: "schema", Label: "Database Schema", Status: map[bool]string{true: "HEALTHY", false: "CRITICAL"}[migration.Status == "ok"], Summary: "Schema " + migration.Version + " · Migration " + migration.Status, Required: true},
		{Key: "master-key", Label: "Master Key", Status: masterKeyStatus, Summary: masterKeySummary, Route: "/admin/security", Required: true},
		{Key: "storage", Label: "Persistent Storage", Status: storageStatus, Summary: storageSummary, Route: "/admin/operations", Required: true},
		{Key: "service-url", Label: "Service URL", Status: serviceStatus, Summary: serviceSummary, Route: "/admin/system", Required: true},
		{Key: "users", Label: "사용자 · 조직", Status: map[bool]string{true: "HEALTHY", false: "WARNING"}[counts["activeUsers"] > 0 && counts["organizations"] > 0], Summary: "활성 사용자와 조직 구조를 확인합니다.", Route: "/admin/users", Required: true},
		{Key: "rbac", Label: "Role · Data Scope", Status: map[bool]string{true: "HEALTHY", false: "WARNING"}[counts["roles"] > 0], Summary: "기능 권한과 데이터 범위 Role이 준비되어 있습니다.", Route: "/admin/roles", Required: true},
		{Key: "pipeline", Label: "CRM Pipeline", Status: map[bool]string{true: "HEALTHY", false: "WARNING"}[counts["pipelineStages"] > 0], Summary: "활성 Stage와 Deal Health 규칙을 확인합니다.", Route: "/admin/pipeline", Required: true},
		{Key: "jobs", Label: "Background Job", Status: jobStatus, Summary: jobSummary, Route: "/admin/operations", Required: true},
		{Key: "oidc", Label: "Keycloak OIDC", Status: oidcStatus, Summary: oidcSummary, Route: "/admin/oidc"},
		{Key: "api", Label: "REST API", Status: map[bool]string{true: "HEALTHY", false: "DISABLED"}[apiEnabled], Summary: map[bool]string{true: "Personal Key REST API가 활성화되어 있습니다.", false: "관리자 정책으로 REST Personal Key가 비활성화되었습니다."}[apiEnabled], Route: "/admin/keys"},
		{Key: "mcp", Label: "MCP Server", Status: map[bool]string{true: "HEALTHY", false: "DISABLED"}[mcpEnabled], Summary: map[bool]string{true: "Streamable HTTP MCP가 활성화되어 있습니다.", false: "관리자 정책으로 MCP가 비활성화되었습니다."}[mcpEnabled], Route: "/admin/keys"},
		{Key: "approval", Label: "승인 Workflow", Status: approvalStatus, Summary: approvalSummary, Route: "/admin/approval"},
	}
	actions := []adminActionItem{}
	for _, check := range checks {
		if check.Status != "WARNING" && check.Status != "CRITICAL" {
			continue
		}
		severity := "WARNING"
		if check.Status == "CRITICAL" {
			severity = "CRITICAL"
		}
		actions = append(actions, adminActionItem{Severity: severity, Title: check.Label + " 확인 필요", Description: check.Summary, Route: check.Route})
	}
	if len(actions) == 0 {
		actions = append(actions, adminActionItem{Severity: "INFO", Title: "운영 준비 완료", Description: "필수 운영 진단 항목이 모두 정상입니다.", Route: "/admin/operations"})
	}

	recentJobs := []adminRecentJob{}
	rows, err := s.DB.Query(ctx, `SELECT id::text,job_type,status,attempts,max_attempts,run_at,COALESCE(last_error,''),updated_at FROM jobs ORDER BY updated_at DESC LIMIT 12`)
	if err != nil {
		return adminOperationsSnapshot{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item adminRecentJob
		if err = rows.Scan(&item.ID, &item.Type, &item.Status, &item.Attempts, &item.MaxAttempts, &item.RunAt, &item.LastError, &item.UpdatedAt); err != nil {
			return adminOperationsSnapshot{}, err
		}
		item.LastError = redactDiagnostic(item.LastError)
		recentJobs = append(recentJobs, item)
	}
	if err = rows.Err(); err != nil {
		return adminOperationsSnapshot{}, err
	}

	return adminOperationsSnapshot{
		Application:    version.Current(),
		Database:       map[string]any{"name": databaseName, "serverVersion": serverVersion, "sizeBytes": databaseSize, "migration": migration},
		Jobs:           map[string]int{"ready": readyJobs, "running": runningJobs, "failed": failedJobs},
		Counts:         counts,
		Features:       map[string]any{"localLogin": localLogin, "oidc": oidcEnabled, "api": apiEnabled, "mcp": mcpEnabled, "approval": counts["approvalPolicies"] > 0},
		Diagnostics:    checks,
		Actions:        actions,
		RecentJobs:     recentJobs,
		ReadinessScore: diagnosticReadiness(checks),
		UptimeSeconds:  int(time.Since(s.started).Seconds()),
		GeneratedAt:    time.Now().UTC(),
	}, nil
}

func (s *Server) adminOperations(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	snapshot, err := s.collectAdminOperations(r.Context())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, snapshot)
}

func (s *Server) adminSupportBundle(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := requireAdmin(p, false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	snapshot, err := s.collectAdminOperations(r.Context())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "SUPPORT_BUNDLE_EXPORT", Resource: "diagnostics", After: map[string]any{"generatedAt": snapshot.GeneratedAt, "containsSecrets": false}, IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="relio-support-`+time.Now().UTC().Format("20060102T150405Z")+`.json"`)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"product":    "Relio",
		"notice":     "Secret, password, token, Personal Key, PostgreSQL DSN 및 OIDC Client Secret은 포함되지 않습니다.",
		"operations": snapshot,
	})
}

func (s *Server) customFields(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT id,entity_type,field_key,label,field_type,required,options,active,display_order,updated_at FROM custom_field_definitions ORDER BY entity_type,display_order,label`)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, entity, key, label, typ string
		var required, active bool
		var options []byte
		var order int
		var updated time.Time
		if err = rows.Scan(&id, &entity, &key, &label, &typ, &required, &options, &active, &order, &updated); err != nil {
			s.serviceError(w, r, err)
			return
		}
		var opts any
		_ = json.Unmarshal(options, &opts)
		items = append(items, map[string]any{"id": id, "entityType": entity, "key": key, "label": label, "type": typ, "required": required, "options": opts, "active": active, "displayOrder": order, "updatedAt": updated})
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}
func (s *Server) createCustomField(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := requireAdmin(p, true); err != nil {
		s.serviceError(w, r, err)
		return
	}
	var in struct {
		EntityType   string `json:"entityType"`
		Key          string `json:"key"`
		Label        string `json:"label"`
		Type         string `json:"type"`
		Required     bool   `json:"required"`
		Options      any    `json:"options"`
		DisplayOrder int    `json:"displayOrder"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	allowedTypes := map[string]bool{"Text": true, "Textarea": true, "Number": true, "Money": true, "Percent": true, "Date": true, "Datetime": true, "Boolean": true, "Select": true, "Multi Select": true, "User": true, "Organization": true, "URL": true}
	if in.EntityType == "" || in.Key == "" || in.Label == "" || !allowedTypes[in.Type] {
		s.serviceError(w, r, errors.New("invalid custom field definition"))
		return
	}
	id := ids.New()
	raw, _ := json.Marshal(in.Options)
	_, err := s.DB.Exec(r.Context(), `INSERT INTO custom_field_definitions(id,entity_type,field_key,label,field_type,required,options,display_order,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, strings.ToUpper(in.EntityType), strings.ToLower(in.Key), in.Label, in.Type, in.Required, raw, in.DisplayOrder, p.UserID)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "CUSTOM_FIELD_CREATE", Resource: "custom_field", ResourceID: id, After: in, IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
	httpx.JSON(w, 201, map[string]any{"id": id})
}
func (s *Server) adminPipelines(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	v, err := s.CRM.Pipelines(r.Context(), principal(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}
func (s *Server) createStage(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := requireAdmin(p, true); err != nil {
		s.serviceError(w, r, err)
		return
	}
	var in crm.Stage
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if in.Name == "" || in.Order < 1 || in.Probability < 0 || in.Probability > 100 {
		s.serviceError(w, r, errors.New("invalid pipeline stage"))
		return
	}
	id := ids.New()
	_, err := s.DB.Exec(r.Context(), `INSERT INTO pipeline_stages(id,pipeline_id,name,stage_order,probability,forecast_category,is_won,is_lost,active,color,min_days,max_days) VALUES($1,$2,$3,$4,$5,$6,$7,$8,true,$9,$10,$11)`, id, r.PathValue("id"), in.Name, in.Order, in.Probability, in.ForecastCategory, in.IsWon, in.IsLost, in.Color, in.MinDays, in.MaxDays)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "PIPELINE_STAGE_CREATE", Resource: "pipeline_stage", ResourceID: id, After: in, IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
	httpx.JSON(w, 201, map[string]any{"id": id})
}
