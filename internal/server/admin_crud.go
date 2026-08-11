package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/httpx"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// This file completes the administrator console so every listed resource can be
// changed and removed, not only created. Destructive operations refuse to break
// referential integrity and always explain what is still using the record.

var validDataScopes = map[string]bool{"USER": true, "TEAM": true, "DEPARTMENT": true, "DIVISION": true, "COMPANY": true}

// adminMutation runs the shared admin write pre-checks and returns the principal.
func (s *Server) adminMutation(w http.ResponseWriter, r *http.Request) (*auth.Principal, bool) {
	p := principal(r)
	if err := requireAdmin(p, true); err != nil {
		s.serviceError(w, r, err)
		return nil, false
	}
	return p, true
}

func (s *Server) auditAdmin(r *http.Request, p *auth.Principal, action, resource, id string, before, after any) {
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: action, Resource: resource, ResourceID: id, Before: before, After: after, IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
}

// reference pairs a human label with a count of records still pointing at the
// row an administrator is trying to delete.
type reference struct {
	Label string
	Count int
}

// blockedBy turns reference counts into a single actionable error so an
// administrator learns exactly what to detach before deleting.
func blockedBy(subject string, refs ...reference) error {
	parts := []string{}
	for _, ref := range refs {
		if ref.Count > 0 {
			parts = append(parts, fmt.Sprintf("%s %d건", ref.Label, ref.Count))
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return fmt.Errorf("%s는 %s이 연결되어 있어 삭제할 수 없습니다", subject, strings.Join(parts, ", "))
}

// deleteGuarded runs a DELETE and turns a PostgreSQL foreign key violation into
// an administrator-readable message. Enumerating every referencing table by hand
// would silently rot as the schema grows, so the database stays the source of
// truth for what is still in use.
func (s *Server) deleteGuarded(ctx context.Context, subject, query string, args ...any) error {
	if _, err := s.DB.Exec(ctx, query, args...); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return fmt.Errorf("%s는 %s에서 사용 중이어서 삭제할 수 없습니다. 먼저 해당 데이터의 연결을 변경하세요", subject, referencingLabel(pgErr.TableName))
		}
		return err
	}
	return nil
}

// referencingLabel names the table that still points at the row, using the same
// vocabulary the administrator sees in the console.
func referencingLabel(table string) string {
	labels := map[string]string{
		"users": "사용자", "customers": "고객", "contacts": "담당자", "leads": "Lead",
		"opportunities": "Opportunity", "opportunity_products": "Opportunity 상품",
		"quotations": "견적", "contracts": "계약", "sales": "매출", "targets": "영업목표",
		"tasks": "할 일", "activities": "영업활동", "account_plans": "Account Plan",
		"approval_policies": "승인 정책", "approval_requests": "승인 요청",
		"oidc_role_mappings": "OIDC Role 매핑", "oidc_group_mappings": "OIDC Group 매핑",
		"oidc_providers": "OIDC 설정", "organizations": "하위 조직",
		"forecast_snapshot_items": "Forecast Snapshot", "forecast_overrides": "Forecast Override",
		"pipeline_stages": "Pipeline Stage", "role_permissions": "권한", "user_roles": "사용자 Role",
	}
	if label, ok := labels[table]; ok {
		return label
	}
	if table == "" {
		return "다른 데이터"
	}
	return table
}

// ---------------------------------------------------------------- users

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	var in struct {
		DisplayName    string `json:"displayName"`
		Email          string `json:"email"`
		OrganizationID string `json:"organizationId"`
		ManagerID      string `json:"managerId"`
		Title          string `json:"title"`
		Active         *bool  `json:"active"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if strings.TrimSpace(in.DisplayName) == "" {
		s.serviceError(w, r, errors.New("displayName is required"))
		return
	}
	if in.ManagerID != "" && in.ManagerID == id {
		s.serviceError(w, r, errors.New("a user cannot be their own manager"))
		return
	}
	var beforeName, beforeEmail, beforeOrg, beforeManager, beforeTitle string
	var beforeActive, bootstrap bool
	err := s.DB.QueryRow(r.Context(), `SELECT display_name,COALESCE(email,''),COALESCE(organization_id::text,''),COALESCE(manager_id::text,''),COALESCE(title,''),active,is_bootstrap FROM users WHERE id=$1`, id).Scan(&beforeName, &beforeEmail, &beforeOrg, &beforeManager, &beforeTitle, &beforeActive, &bootstrap)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	active := beforeActive
	if in.Active != nil {
		active = *in.Active
	}
	if bootstrap && !active {
		s.serviceError(w, r, errors.New("bootstrap administrator is a break glass account and cannot be deactivated"))
		return
	}
	if in.ManagerID != "" {
		cycle, err := managerCycle(r.Context(), s, id, in.ManagerID)
		if err != nil {
			s.serviceError(w, r, err)
			return
		}
		if cycle {
			s.serviceError(w, r, errors.New("the selected manager already reports to this user"))
			return
		}
	}
	_, err = s.DB.Exec(r.Context(), `UPDATE users SET display_name=$2,email=$3,organization_id=$4,manager_id=$5,title=$6,active=$7,updated_at=now(),version=version+1 WHERE id=$1`,
		id, strings.TrimSpace(in.DisplayName), nullAdmin(in.Email), nullAdmin(in.OrganizationID), nullAdmin(in.ManagerID), nullAdmin(in.Title), active)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	if beforeActive && !active {
		s.detachUser(r.Context(), id)
	}
	s.auditAdmin(r, p, "USER_UPDATE", "user", id,
		map[string]any{"displayName": beforeName, "email": beforeEmail, "organizationId": beforeOrg, "managerId": beforeManager, "title": beforeTitle, "active": beforeActive},
		map[string]any{"displayName": in.DisplayName, "email": in.Email, "organizationId": in.OrganizationID, "managerId": in.ManagerID, "title": in.Title, "active": active})
	httpx.JSON(w, 200, map[string]any{"saved": true, "active": active})
}

// managerCycle prevents a reporting loop, which would make every TEAM scoped
// query recurse forever.
func managerCycle(ctx context.Context, s *Server, userID, managerID string) (bool, error) {
	var cycle bool
	err := s.DB.QueryRow(ctx, `WITH RECURSIVE chain AS (
		SELECT id,manager_id FROM users WHERE id=$2
		UNION ALL
		SELECT u.id,u.manager_id FROM users u JOIN chain c ON u.id=c.manager_id
	) SELECT EXISTS(SELECT 1 FROM chain WHERE id=$1)`, userID, managerID).Scan(&cycle)
	return cycle, err
}

// detachUser revokes everything a deactivated account could still use. Without
// this a disabled user keeps a live browser session and working Personal Keys.
func (s *Server) detachUser(ctx context.Context, id string) {
	_, _ = s.DB.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, id)
	_, _ = s.DB.Exec(ctx, `UPDATE personal_keys SET status='REVOKED',revoked_at=now(),grace_expires_at=NULL WHERE user_id=$1 AND status IN ('ACTIVE','ROTATING')`, id)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id == p.UserID {
		s.serviceError(w, r, errors.New("an administrator cannot deactivate their own account"))
		return
	}
	var username string
	var bootstrap, active bool
	if err := s.DB.QueryRow(r.Context(), `SELECT username,is_bootstrap,active FROM users WHERE id=$1`, id).Scan(&username, &bootstrap, &active); err != nil {
		s.serviceError(w, r, err)
		return
	}
	if bootstrap {
		s.serviceError(w, r, errors.New("bootstrap administrator is a break glass account and cannot be deleted"))
		return
	}
	// A user owns customers, opportunities and audit history, so the account is
	// deactivated rather than removed. Ownership stays intact and auditable.
	if _, err := s.DB.Exec(r.Context(), `UPDATE users SET active=false,updated_at=now(),version=version+1 WHERE id=$1`, id); err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.detachUser(r.Context(), id)
	s.auditAdmin(r, p, "USER_DEACTIVATE", "user", id, map[string]any{"username": username, "active": active}, map[string]any{"active": false, "sessionsRevoked": true, "personalKeysRevoked": true})
	httpx.JSON(w, 200, map[string]any{"deactivated": true, "note": "담당 데이터와 감사 이력을 보존하기 위해 계정을 비활성화했습니다."})
}

func (s *Server) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if len(in.Password) < 12 {
		s.serviceError(w, r, errors.New("password must contain at least 12 characters"))
		return
	}
	id := r.PathValue("id")
	var source, username string
	if err := s.DB.QueryRow(r.Context(), `SELECT auth_source,username FROM users WHERE id=$1`, id).Scan(&source, &username); err != nil {
		s.serviceError(w, r, err)
		return
	}
	if source != "LOCAL" {
		s.serviceError(w, r, errors.New("only a local account has a Relio password; this user signs in through SSO"))
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	if _, err = s.DB.Exec(r.Context(), `UPDATE users SET password_hash=$2,must_change_password=true,updated_at=now(),version=version+1 WHERE id=$1`, id, hash); err != nil {
		s.serviceError(w, r, err)
		return
	}
	// The temporary password must not survive as a live session elsewhere.
	_, _ = s.DB.Exec(r.Context(), `DELETE FROM sessions WHERE user_id=$1`, id)
	s.auditAdmin(r, p, "USER_PASSWORD_RESET", "user", id, nil, map[string]any{"username": username, "mustChangePassword": true, "sessionsRevoked": true})
	httpx.JSON(w, 200, map[string]any{"reset": true, "mustChangePassword": true})
}

// ---------------------------------------------------------------- roles

func (s *Server) updateRole(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	var in struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		DataScope   string   `json:"dataScope"`
		Permissions []string `json:"permissions"`
		IsDefault   bool     `json:"isDefault"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	in.DataScope = strings.ToUpper(strings.TrimSpace(in.DataScope))
	if strings.TrimSpace(in.Name) == "" || !validDataScopes[in.DataScope] {
		s.serviceError(w, r, errors.New("name and a valid dataScope are required"))
		return
	}
	if err := validatePermissions(in.Permissions); err != nil {
		s.serviceError(w, r, err)
		return
	}
	var code, beforeName, beforeScope string
	var system, beforeDefault bool
	var beforePermissions []string
	err := s.DB.QueryRow(r.Context(), `SELECT r.code,r.name,r.data_scope,r.system_role,r.is_default,COALESCE(array_agg(rp.permission) FILTER(WHERE rp.permission IS NOT NULL),'{}') FROM roles r LEFT JOIN role_permissions rp ON rp.role_id=r.id WHERE r.id=$1 GROUP BY r.id`, id).Scan(&code, &beforeName, &beforeScope, &system, &beforeDefault, &beforePermissions)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	if system && !containsPermission(in.Permissions, "admin:*") {
		s.serviceError(w, r, errors.New("a system Role must keep the admin:* permission so the console stays reachable"))
		return
	}
	if in.IsDefault && system {
		s.serviceError(w, r, errors.New("a system administrator Role cannot be the default sign-in Role"))
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	if in.IsDefault {
		if _, err = tx.Exec(r.Context(), `UPDATE roles SET is_default=false WHERE is_default AND id<>$1`, id); err != nil {
			s.serviceError(w, r, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `UPDATE roles SET name=$2,description=$3,data_scope=$4,is_default=$5,updated_at=now() WHERE id=$1`, id, strings.TrimSpace(in.Name), nullAdmin(in.Description), in.DataScope, in.IsDefault); err != nil {
		s.serviceError(w, r, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM role_permissions WHERE role_id=$1`, id); err != nil {
		s.serviceError(w, r, err)
		return
	}
	for _, permission := range normalizePermissions(in.Permissions) {
		if _, err = tx.Exec(r.Context(), `INSERT INTO role_permissions(role_id,permission) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, permission); err != nil {
			s.serviceError(w, r, err)
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.serviceError(w, r, err)
		return
	}
	// Permission changes must reach live sessions immediately, otherwise a
	// revoked permission keeps working until the session expires.
	s.invalidateRoleSessions(r.Context(), id)
	s.auditAdmin(r, p, "ROLE_UPDATE", "role", id,
		map[string]any{"name": beforeName, "dataScope": beforeScope, "permissions": beforePermissions, "isDefault": beforeDefault},
		map[string]any{"code": code, "name": in.Name, "dataScope": in.DataScope, "permissions": in.Permissions, "isDefault": in.IsDefault})
	httpx.JSON(w, 200, map[string]any{"saved": true})
}

func containsPermission(permissions []string, want string) bool {
	for _, permission := range permissions {
		if permission == want {
			return true
		}
	}
	return false
}

func normalizePermissions(permissions []string) []string {
	out := make([]string, 0, len(permissions))
	seen := map[string]bool{}
	for _, permission := range permissions {
		value := strings.ToLower(strings.TrimSpace(permission))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// invalidateRoleSessions forces every affected user to reload their principal on
// the next request by ending their browser sessions.
func (s *Server) invalidateRoleSessions(ctx context.Context, roleID string) {
	_, _ = s.DB.Exec(ctx, `DELETE FROM sessions WHERE user_id IN (SELECT user_id FROM user_roles WHERE role_id=$1) AND user_id NOT IN (SELECT id FROM users WHERE is_bootstrap)`, roleID)
}

func (s *Server) deleteRole(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var code, name string
	var system, isDefault bool
	if err := s.DB.QueryRow(r.Context(), `SELECT code,name,system_role,is_default FROM roles WHERE id=$1`, id).Scan(&code, &name, &system, &isDefault); err != nil {
		s.serviceError(w, r, err)
		return
	}
	if system {
		s.serviceError(w, r, errors.New("a system Role cannot be deleted"))
		return
	}
	if isDefault {
		s.serviceError(w, r, errors.New("the default sign-in Role cannot be deleted; assign the default to another Role first"))
		return
	}
	var assigned, policies, mappings, providers int
	err := s.DB.QueryRow(r.Context(), `SELECT
		(SELECT count(*) FROM user_roles WHERE role_id=$1),
		(SELECT count(*) FROM approval_policies WHERE approver_role_id=$1),
		(SELECT count(*) FROM oidc_role_mappings WHERE role_id=$1),
		(SELECT count(*) FROM oidc_providers WHERE default_role_id=$1)`, id).Scan(&assigned, &policies, &mappings, &providers)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	blocked := blockedBy("Role",
		reference{"사용자", assigned}, reference{"승인 정책", policies},
		reference{"OIDC Role 매핑", mappings}, reference{"OIDC 기본 Role 설정", providers})
	if blocked != nil {
		s.serviceError(w, r, blocked)
		return
	}
	if err = s.deleteGuarded(r.Context(), "Role", `DELETE FROM roles WHERE id=$1`, id); err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "ROLE_DELETE", "role", id, map[string]any{"code": code, "name": name}, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------- organizations

func (s *Server) updateOrganization(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	var in struct {
		ParentID string `json:"parentId"`
		Name     string `json:"name"`
		Code     string `json:"code"`
		Type     string `json:"type"`
		Active   *bool  `json:"active"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Code) == "" {
		s.serviceError(w, r, errors.New("name and code are required"))
		return
	}
	if in.ParentID == id {
		s.serviceError(w, r, errors.New("an organization cannot be its own parent"))
		return
	}
	var beforeName, beforeCode, beforeType, beforeParent string
	var beforeActive bool
	if err := s.DB.QueryRow(r.Context(), `SELECT name,code,org_type,COALESCE(parent_id::text,''),active FROM organizations WHERE id=$1`, id).Scan(&beforeName, &beforeCode, &beforeType, &beforeParent, &beforeActive); err != nil {
		s.serviceError(w, r, err)
		return
	}
	if in.ParentID != "" {
		var cycle bool
		err := s.DB.QueryRow(r.Context(), `WITH RECURSIVE chain AS (
			SELECT id,parent_id FROM organizations WHERE id=$2
			UNION ALL
			SELECT o.id,o.parent_id FROM organizations o JOIN chain c ON o.id=c.parent_id
		) SELECT EXISTS(SELECT 1 FROM chain WHERE id=$1)`, id, in.ParentID).Scan(&cycle)
		if err != nil {
			s.serviceError(w, r, err)
			return
		}
		if cycle {
			s.serviceError(w, r, errors.New("the selected parent is already below this organization"))
			return
		}
	}
	active := beforeActive
	if in.Active != nil {
		active = *in.Active
	}
	if strings.ToUpper(beforeCode) == "RELIO" && !active {
		s.serviceError(w, r, errors.New("the root RELIO organization cannot be deactivated"))
		return
	}
	_, err := s.DB.Exec(r.Context(), `UPDATE organizations SET parent_id=$2,name=$3,code=$4,org_type=$5,active=$6,updated_at=now() WHERE id=$1`,
		id, nullAdmin(in.ParentID), strings.TrimSpace(in.Name), strings.ToUpper(strings.TrimSpace(in.Code)), strings.ToUpper(defaultString(in.Type, "DEPARTMENT")), active)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "ORGANIZATION_UPDATE", "organization", id,
		map[string]any{"name": beforeName, "code": beforeCode, "type": beforeType, "parentId": beforeParent, "active": beforeActive},
		map[string]any{"name": in.Name, "code": in.Code, "type": in.Type, "parentId": in.ParentID, "active": active})
	httpx.JSON(w, 200, map[string]any{"saved": true})
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func (s *Server) deleteOrganization(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var name, code string
	if err := s.DB.QueryRow(r.Context(), `SELECT name,code FROM organizations WHERE id=$1`, id).Scan(&name, &code); err != nil {
		s.serviceError(w, r, err)
		return
	}
	if strings.ToUpper(code) == "RELIO" {
		s.serviceError(w, r, errors.New("the root RELIO organization cannot be deleted"))
		return
	}
	var children, members, policies int
	err := s.DB.QueryRow(r.Context(), `SELECT
		(SELECT count(*) FROM organizations WHERE parent_id=$1),
		(SELECT count(*) FROM users WHERE organization_id=$1),
		(SELECT count(*) FROM approval_policies WHERE approver_org_id=$1)`, id).Scan(&children, &members, &policies)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	blocked := blockedBy("조직", reference{"하위 조직", children}, reference{"사용자", members}, reference{"승인 정책", policies})
	if blocked != nil {
		s.serviceError(w, r, blocked)
		return
	}
	if err = s.deleteGuarded(r.Context(), "조직", `DELETE FROM organizations WHERE id=$1`, id); err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "ORGANIZATION_DELETE", "organization", id, map[string]any{"name": name, "code": code}, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------- custom fields

var allowedCustomFieldTypes = map[string]bool{"Text": true, "Textarea": true, "Number": true, "Money": true, "Percent": true, "Date": true, "Datetime": true, "Boolean": true, "Select": true, "Multi Select": true, "User": true, "Organization": true, "URL": true}

func (s *Server) updateCustomField(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	var in struct {
		Label        string `json:"label"`
		Type         string `json:"type"`
		Required     bool   `json:"required"`
		Options      any    `json:"options"`
		Active       *bool  `json:"active"`
		DisplayOrder int    `json:"displayOrder"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if strings.TrimSpace(in.Label) == "" || !allowedCustomFieldTypes[in.Type] {
		s.serviceError(w, r, errors.New("label and a supported field type are required"))
		return
	}
	var entity, key, beforeLabel, beforeType string
	var beforeRequired, beforeActive bool
	if err := s.DB.QueryRow(r.Context(), `SELECT entity_type,field_key,label,field_type,required,active FROM custom_field_definitions WHERE id=$1`, id).Scan(&entity, &key, &beforeLabel, &beforeType, &beforeRequired, &beforeActive); err != nil {
		s.serviceError(w, r, err)
		return
	}
	active := beforeActive
	if in.Active != nil {
		active = *in.Active
	}
	raw, _ := json.Marshal(in.Options)
	_, err := s.DB.Exec(r.Context(), `UPDATE custom_field_definitions SET label=$2,field_type=$3,required=$4,options=$5,active=$6,display_order=$7,updated_at=now() WHERE id=$1`,
		id, strings.TrimSpace(in.Label), in.Type, in.Required, raw, active, in.DisplayOrder)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "CUSTOM_FIELD_UPDATE", "custom_field", id,
		map[string]any{"label": beforeLabel, "type": beforeType, "required": beforeRequired, "active": beforeActive},
		map[string]any{"entityType": entity, "key": key, "label": in.Label, "type": in.Type, "required": in.Required, "active": active, "displayOrder": in.DisplayOrder})
	httpx.JSON(w, 200, map[string]any{"saved": true})
}

func (s *Server) deleteCustomField(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var entity, key, label string
	if err := s.DB.QueryRow(r.Context(), `SELECT entity_type,field_key,label FROM custom_field_definitions WHERE id=$1`, id).Scan(&entity, &key, &label); err != nil {
		s.serviceError(w, r, err)
		return
	}
	// Values already captured on records stay in their custom_fields document.
	// Removing the definition only stops the field from being collected.
	used, err := s.customFieldUsage(r.Context(), entity, key)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	if _, err = s.DB.Exec(r.Context(), `DELETE FROM custom_field_definitions WHERE id=$1`, id); err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "CUSTOM_FIELD_DELETE", "custom_field", id, map[string]any{"entityType": entity, "key": key, "label": label, "recordsWithValue": used}, nil)
	httpx.JSON(w, 200, map[string]any{"deleted": true, "recordsWithValue": used, "note": "이미 입력된 값은 각 레코드에 그대로 보존됩니다."})
}

// customFieldUsage counts records that already carry a value for the field so
// the administrator can see the impact of removing the definition.
func (s *Server) customFieldUsage(ctx context.Context, entity, key string) (int, error) {
	table := map[string]string{"CUSTOMER": "customers", "CONTACT": "contacts", "LEAD": "leads", "OPPORTUNITY": "opportunities"}[strings.ToUpper(entity)]
	if table == "" {
		return 0, nil
	}
	var count int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE custom_fields ? $1`, key).Scan(&count)
	return count, err
}

// ---------------------------------------------------------------- pipelines

func (s *Server) createPipeline(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	var in struct {
		Name    string `json:"name"`
		Active  bool   `json:"active"`
		Default bool   `json:"default"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		s.serviceError(w, r, errors.New("name is required"))
		return
	}
	id := ids.New()
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	if in.Default {
		if _, err = tx.Exec(r.Context(), `UPDATE pipelines SET is_default=false WHERE is_default`); err != nil {
			s.serviceError(w, r, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO pipelines(id,name,active,is_default) VALUES($1,$2,$3,$4)`, id, strings.TrimSpace(in.Name), in.Active, in.Default); err != nil {
		s.serviceError(w, r, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "PIPELINE_CREATE", "pipeline", id, nil, in)
	httpx.JSON(w, 201, map[string]any{"id": id})
}

func (s *Server) updatePipeline(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	var in struct {
		Name    string `json:"name"`
		Active  *bool  `json:"active"`
		Default *bool  `json:"default"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if strings.TrimSpace(in.Name) == "" {
		s.serviceError(w, r, errors.New("name is required"))
		return
	}
	var beforeName string
	var beforeActive, beforeDefault bool
	if err := s.DB.QueryRow(r.Context(), `SELECT name,active,is_default FROM pipelines WHERE id=$1`, id).Scan(&beforeName, &beforeActive, &beforeDefault); err != nil {
		s.serviceError(w, r, err)
		return
	}
	active, isDefault := beforeActive, beforeDefault
	if in.Active != nil {
		active = *in.Active
	}
	if in.Default != nil {
		isDefault = *in.Default
	}
	if beforeDefault && !isDefault {
		s.serviceError(w, r, errors.New("promote another Pipeline to default instead of clearing the current one"))
		return
	}
	if isDefault && !active {
		s.serviceError(w, r, errors.New("the default Pipeline must stay active"))
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	if isDefault && !beforeDefault {
		if _, err = tx.Exec(r.Context(), `UPDATE pipelines SET is_default=false WHERE is_default AND id<>$1`, id); err != nil {
			s.serviceError(w, r, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `UPDATE pipelines SET name=$2,active=$3,is_default=$4,updated_at=now() WHERE id=$1`, id, strings.TrimSpace(in.Name), active, isDefault); err != nil {
		s.serviceError(w, r, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "PIPELINE_UPDATE", "pipeline", id, map[string]any{"name": beforeName, "active": beforeActive, "default": beforeDefault}, map[string]any{"name": in.Name, "active": active, "default": isDefault})
	httpx.JSON(w, 200, map[string]any{"saved": true})
}

func (s *Server) deletePipeline(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var name string
	var isDefault bool
	if err := s.DB.QueryRow(r.Context(), `SELECT name,is_default FROM pipelines WHERE id=$1`, id).Scan(&name, &isDefault); err != nil {
		s.serviceError(w, r, err)
		return
	}
	if isDefault {
		s.serviceError(w, r, errors.New("the default Pipeline cannot be deleted; promote another Pipeline first"))
		return
	}
	var deals int
	if err := s.DB.QueryRow(r.Context(), `SELECT count(*) FROM opportunities WHERE pipeline_id=$1`, id).Scan(&deals); err != nil {
		s.serviceError(w, r, err)
		return
	}
	if blocked := blockedBy("Pipeline", reference{"Opportunity", deals}); blocked != nil {
		s.serviceError(w, r, blocked)
		return
	}
	// Stages, playbooks and exit criteria cascade with the Pipeline by design.
	if err := s.deleteGuarded(r.Context(), "Pipeline", `DELETE FROM pipelines WHERE id=$1`, id); err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "PIPELINE_DELETE", "pipeline", id, map[string]any{"name": name}, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------- pipeline stages

var validForecastCategories = map[string]bool{"PIPELINE": true, "BEST_CASE": true, "COMMIT": true, "CLOSED": true, "OMITTED": true}

func (s *Server) updateStage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	var in struct {
		Name             string  `json:"name"`
		Order            int     `json:"order"`
		Probability      float64 `json:"probability"`
		ForecastCategory string  `json:"forecastCategory"`
		IsWon            bool    `json:"isWon"`
		IsLost           bool    `json:"isLost"`
		Active           *bool   `json:"active"`
		Color            string  `json:"color"`
		MinDays          *int    `json:"minDays"`
		MaxDays          *int    `json:"maxDays"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	in.ForecastCategory = strings.ToUpper(strings.TrimSpace(in.ForecastCategory))
	if strings.TrimSpace(in.Name) == "" || in.Order < 1 || in.Probability < 0 || in.Probability > 100 || !validForecastCategories[in.ForecastCategory] {
		s.serviceError(w, r, errors.New("invalid pipeline stage"))
		return
	}
	if in.IsWon && in.IsLost {
		s.serviceError(w, r, errors.New("a stage cannot be both the Won and the Lost stage"))
		return
	}
	if in.MinDays != nil && in.MaxDays != nil && *in.MinDays > *in.MaxDays {
		s.serviceError(w, r, errors.New("minDays cannot exceed maxDays"))
		return
	}
	var pipelineID, beforeName, beforeCategory string
	var beforeOrder int
	var beforeProbability float64
	var beforeWon, beforeLost, beforeActive bool
	err := s.DB.QueryRow(r.Context(), `SELECT pipeline_id,name,stage_order,probability,forecast_category,is_won,is_lost,active FROM pipeline_stages WHERE id=$1`, id).Scan(&pipelineID, &beforeName, &beforeOrder, &beforeProbability, &beforeCategory, &beforeWon, &beforeLost, &beforeActive)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	active := beforeActive
	if in.Active != nil {
		active = *in.Active
	}
	if !active {
		var open int
		if err = s.DB.QueryRow(r.Context(), `SELECT count(*) FROM opportunities WHERE stage_id=$1 AND status='OPEN'`, id).Scan(&open); err != nil {
			s.serviceError(w, r, err)
			return
		}
		if open > 0 {
			s.serviceError(w, r, fmt.Errorf("진행 중인 Opportunity %d건이 이 Stage에 있어 비활성화할 수 없습니다", open))
			return
		}
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	if in.Order != beforeOrder {
		if err = reorderStage(r.Context(), tx, pipelineID, id, beforeOrder, in.Order); err != nil {
			s.serviceError(w, r, err)
			return
		}
	}
	if in.IsWon {
		if _, err = tx.Exec(r.Context(), `UPDATE pipeline_stages SET is_won=false WHERE pipeline_id=$1 AND id<>$2`, pipelineID, id); err != nil {
			s.serviceError(w, r, err)
			return
		}
	}
	if in.IsLost {
		if _, err = tx.Exec(r.Context(), `UPDATE pipeline_stages SET is_lost=false WHERE pipeline_id=$1 AND id<>$2`, pipelineID, id); err != nil {
			s.serviceError(w, r, err)
			return
		}
	}
	_, err = tx.Exec(r.Context(), `UPDATE pipeline_stages SET name=$2,stage_order=$3,probability=$4,forecast_category=$5,is_won=$6,is_lost=$7,active=$8,color=$9,min_days=$10,max_days=$11 WHERE id=$1`,
		id, strings.TrimSpace(in.Name), in.Order, in.Probability, in.ForecastCategory, in.IsWon, in.IsLost, active, defaultString(in.Color, "#64748b"), in.MinDays, in.MaxDays)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "PIPELINE_STAGE_UPDATE", "pipeline_stage", id,
		map[string]any{"name": beforeName, "order": beforeOrder, "probability": beforeProbability, "forecastCategory": beforeCategory, "isWon": beforeWon, "isLost": beforeLost, "active": beforeActive},
		map[string]any{"name": in.Name, "order": in.Order, "probability": in.Probability, "forecastCategory": in.ForecastCategory, "isWon": in.IsWon, "isLost": in.IsLost, "active": active})
	httpx.JSON(w, 200, map[string]any{"saved": true})
}

// reorderStage makes room for a new position without ever colliding with the
// UNIQUE(pipeline_id, stage_order) constraint: the moved stage is parked outside
// the visible range first, then the block between the two positions shifts.
func reorderStage(ctx context.Context, tx pgx.Tx, pipelineID, stageID string, from, to int) error {
	if _, err := tx.Exec(ctx, `UPDATE pipeline_stages SET stage_order=-1 WHERE id=$1`, stageID); err != nil {
		return err
	}
	if from < to {
		if _, err := tx.Exec(ctx, `UPDATE pipeline_stages SET stage_order=stage_order-1 WHERE pipeline_id=$1 AND stage_order>$2 AND stage_order<=$3`, pipelineID, from, to); err != nil {
			return err
		}
		return nil
	}
	// Shift downwards from the highest position so each step lands on a free slot.
	rows, err := tx.Query(ctx, `SELECT id FROM pipeline_stages WHERE pipeline_id=$1 AND stage_order>=$2 AND stage_order<$3 ORDER BY stage_order DESC`, pipelineID, to, from)
	if err != nil {
		return err
	}
	shifted := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		shifted = append(shifted, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, id := range shifted {
		if _, err = tx.Exec(ctx, `UPDATE pipeline_stages SET stage_order=stage_order+1 WHERE id=$1`, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) deleteStage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var pipelineID, name string
	var order int
	if err := s.DB.QueryRow(r.Context(), `SELECT pipeline_id,name,stage_order FROM pipeline_stages WHERE id=$1`, id).Scan(&pipelineID, &name, &order); err != nil {
		s.serviceError(w, r, err)
		return
	}
	var deals, snapshots int
	err := s.DB.QueryRow(r.Context(), `SELECT
		(SELECT count(*) FROM opportunities WHERE stage_id=$1),
		(SELECT count(*) FROM forecast_snapshot_items WHERE stage_id=$1)`, id).Scan(&deals, &snapshots)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	if blocked := blockedBy("Stage", reference{"Opportunity", deals}, reference{"Forecast Snapshot", snapshots}); blocked != nil {
		s.serviceError(w, r, blocked)
		return
	}
	var remaining int
	if err = s.DB.QueryRow(r.Context(), `SELECT count(*) FROM pipeline_stages WHERE pipeline_id=$1`, pipelineID).Scan(&remaining); err != nil {
		s.serviceError(w, r, err)
		return
	}
	if remaining <= 1 {
		s.serviceError(w, r, errors.New("a Pipeline must keep at least one Stage"))
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `DELETE FROM pipeline_stages WHERE id=$1`, id); err != nil {
		s.serviceError(w, r, err)
		return
	}
	// Keep the remaining positions contiguous so the board order stays readable.
	if _, err = tx.Exec(r.Context(), `UPDATE pipeline_stages SET stage_order=stage_order-1 WHERE pipeline_id=$1 AND stage_order>$2`, pipelineID, order); err != nil {
		s.serviceError(w, r, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "PIPELINE_STAGE_DELETE", "pipeline_stage", id, map[string]any{"name": name, "order": order}, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------- approval policies

func (s *Server) deletePolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var name, entity string
	if err := s.DB.QueryRow(r.Context(), `SELECT name,entity_type FROM approval_policies WHERE id=$1`, id).Scan(&name, &entity); err != nil {
		s.serviceError(w, r, err)
		return
	}
	var pending, total int
	if err := s.DB.QueryRow(r.Context(), `SELECT count(*) FILTER(WHERE status='PENDING'),count(*) FROM approval_requests WHERE policy_id=$1`, id).Scan(&pending, &total); err != nil {
		s.serviceError(w, r, err)
		return
	}
	if pending > 0 {
		s.serviceError(w, r, fmt.Errorf("대기 중인 승인 요청 %d건을 먼저 처리하세요", pending))
		return
	}
	if total > 0 {
		// Decided requests are audit history, so the policy is deactivated
		// instead of deleted and the approval menu disappears the same way.
		if _, err := s.DB.Exec(r.Context(), `UPDATE approval_policies SET active=false,updated_by=$2,updated_at=now() WHERE id=$1`, id, p.UserID); err != nil {
			s.serviceError(w, r, err)
			return
		}
		s.auditAdmin(r, p, "APPROVAL_POLICY_DEACTIVATE", "approval_policy", id, map[string]any{"name": name, "entityType": entity, "decidedRequests": total}, map[string]any{"active": false})
		httpx.JSON(w, 200, map[string]any{"deactivated": true, "decidedRequests": total, "note": "처리 완료된 승인 이력을 보존하기 위해 정책을 비활성화했습니다."})
		return
	}
	if _, err := s.DB.Exec(r.Context(), `DELETE FROM approval_policies WHERE id=$1`, id); err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "APPROVAL_POLICY_DELETE", "approval_policy", id, map[string]any{"name": name, "entityType": entity}, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------- settings

func (s *Server) deleteSetting(w http.ResponseWriter, r *http.Request) {
	p, ok := s.adminMutation(w, r)
	if !ok {
		return
	}
	namespace, key := r.PathValue("namespace"), r.PathValue("key")
	if err := s.Settings.Delete(r.Context(), p, namespace, key, httpx.ClientIP(r), httpx.RequestID(r.Context()), r.UserAgent()); err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"reset": true, "note": "설정을 삭제하여 기본값으로 되돌렸습니다."})
}

// ---------------------------------------------------------------- products

func (s *Server) updateProduct(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := auth.Require(p, "product:write"); err != nil {
		s.serviceError(w, r, err)
		return
	}
	var in struct {
		Code        string  `json:"code"`
		Name        string  `json:"name"`
		Description string  `json:"description"`
		UnitPrice   float64 `json:"unitPrice"`
		Active      *bool   `json:"active"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if strings.TrimSpace(in.Code) == "" || strings.TrimSpace(in.Name) == "" || in.UnitPrice < 0 {
		s.serviceError(w, r, errors.New("code, name and a non-negative unitPrice are required"))
		return
	}
	var beforeCode, beforeName string
	var beforePrice float64
	var beforeActive bool
	if err := s.DB.QueryRow(r.Context(), `SELECT code,name,unit_price,active FROM products WHERE id=$1`, id).Scan(&beforeCode, &beforeName, &beforePrice, &beforeActive); err != nil {
		s.serviceError(w, r, err)
		return
	}
	active := beforeActive
	if in.Active != nil {
		active = *in.Active
	}
	_, err := s.DB.Exec(r.Context(), `UPDATE products SET code=$2,name=$3,description=$4,unit_price=$5,active=$6,updated_at=now() WHERE id=$1`,
		id, strings.ToUpper(strings.TrimSpace(in.Code)), strings.TrimSpace(in.Name), nullAdmin(in.Description), in.UnitPrice, active)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "PRODUCT_UPDATE", "product", id, map[string]any{"code": beforeCode, "name": beforeName, "unitPrice": beforePrice, "active": beforeActive}, map[string]any{"code": in.Code, "name": in.Name, "unitPrice": in.UnitPrice, "active": active})
	httpx.JSON(w, 200, map[string]any{"saved": true})
}

func (s *Server) deleteProduct(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := auth.Require(p, "product:write"); err != nil {
		s.serviceError(w, r, err)
		return
	}
	id := r.PathValue("id")
	var code, name string
	if err := s.DB.QueryRow(r.Context(), `SELECT code,name FROM products WHERE id=$1`, id).Scan(&code, &name); err != nil {
		s.serviceError(w, r, err)
		return
	}
	var lineItems int
	err := s.DB.QueryRow(r.Context(), `SELECT count(*) FROM opportunity_products WHERE product_id=$1`, id).Scan(&lineItems)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	if blocked := blockedBy("상품", reference{"Opportunity 상품", lineItems}); blocked != nil {
		// Referenced products are retired instead of removed so historical
		// quotations and pipelines keep their line items.
		if _, updateErr := s.DB.Exec(r.Context(), `UPDATE products SET active=false,updated_at=now() WHERE id=$1`, id); updateErr != nil {
			s.serviceError(w, r, updateErr)
			return
		}
		s.auditAdmin(r, p, "PRODUCT_RETIRE", "product", id, map[string]any{"code": code, "name": name}, map[string]any{"active": false, "reason": blocked.Error()})
		httpx.JSON(w, 200, map[string]any{"retired": true, "note": "이미 사용된 상품이라 판매 중지 처리했습니다. 과거 견적과 Opportunity는 그대로 유지됩니다."})
		return
	}
	if _, err = s.DB.Exec(r.Context(), `DELETE FROM products WHERE id=$1`, id); err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.auditAdmin(r, p, "PRODUCT_DELETE", "product", id, map[string]any{"code": code, "name": name}, nil)
	w.WriteHeader(http.StatusNoContent)
}
