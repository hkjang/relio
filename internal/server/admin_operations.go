package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/audit"
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
	rows, err := s.DB.Query(r.Context(), `SELECT id,COALESCE(actor_id::text,''),COALESCE(actor_name,''),channel,action,resource,COALESCE(resource_id,''),before_data,after_data,COALESCE(ip::text,''),COALESCE(request_id,''),COALESCE(user_agent,''),occurred_at FROM audit_logs WHERE ($1='' OR channel=$1) AND ($2='' OR resource=$2) ORDER BY occurred_at DESC LIMIT $3`, strings.ToUpper(r.URL.Query().Get("channel")), r.URL.Query().Get("resource"), limit)
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
func (s *Server) adminOperations(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	migration := database.MigrationStatus(r.Context(), s.DB)
	var db, serverVersion string
	var size int64
	_ = s.DB.QueryRow(r.Context(), `SELECT current_database(),version(),pg_database_size(current_database())`).Scan(&db, &serverVersion, &size)
	var ready, running, failed int
	_ = s.DB.QueryRow(r.Context(), `SELECT count(*) FILTER(WHERE status='READY'),count(*) FILTER(WHERE status='RUNNING'),count(*) FILTER(WHERE status='FAILED') FROM jobs`).Scan(&ready, &running, &failed)
	httpx.JSON(w, 200, map[string]any{"application": version.Current(), "database": map[string]any{"name": db, "serverVersion": serverVersion, "sizeBytes": size, "migration": migration}, "jobs": map[string]any{"ready": ready, "running": running, "failed": failed}, "uptimeSeconds": int(time.Since(s.started).Seconds())})
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
