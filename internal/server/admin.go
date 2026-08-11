package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/admin"
	"github.com/hkjang/relio/internal/approval"
	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/oidc"
	"github.com/hkjang/relio/internal/platform/httpx"
	"github.com/hkjang/relio/internal/platform/ids"
)

func requireAdmin(p *auth.Principal, write bool) error {
	permission := "admin:read"
	if write {
		permission = "admin:write"
	}
	return auth.Require(p, permission)
}
func (s *Server) listSettings(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	v, err := s.Settings.List(r.Context(), r.URL.Query().Get("namespace"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v})
}
func (s *Server) putSetting(w http.ResponseWriter, r *http.Request) {
	var in admin.Setting
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	in.Namespace = r.PathValue("namespace")
	in.Key = r.PathValue("key")
	if err := s.Settings.Put(r.Context(), principal(r), in, httpx.ClientIP(r), httpx.RequestID(r.Context()), r.UserAgent()); err != nil {
		s.serviceError(w, r, err)
		return
	}
	items, err := s.Settings.List(r.Context(), in.Namespace)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	for _, v := range items {
		if v.Key == in.Key {
			httpx.JSON(w, 200, v)
			return
		}
	}
	httpx.JSON(w, 200, map[string]any{"saved": true})
}
func (s *Server) getOIDC(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	v, err := s.OIDC.Get(r.Context())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) putOIDC(w http.ResponseWriter, r *http.Request) {
	var in oidc.Config
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	v, err := s.OIDC.Save(r.Context(), principal(r), in, httpx.ClientIP(r), httpx.RequestID(r.Context()), r.UserAgent())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) testOIDC(w http.ResponseWriter, r *http.Request) {
	v, err := s.OIDC.Test(r.Context(), principal(r))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

type oidcRoleMappingInput struct {
	ExternalRole string `json:"externalRole"`
	RoleID       string `json:"roleId"`
}
type oidcGroupMappingInput struct {
	ExternalGroup  string `json:"externalGroup"`
	OrganizationID string `json:"organizationId"`
}
type oidcMappingsInput struct {
	Roles  []oidcRoleMappingInput  `json:"roles"`
	Groups []oidcGroupMappingInput `json:"groups"`
}

func (s *Server) oidcMappings(r *http.Request) (map[string]any, error) {
	provider, err := s.OIDC.Get(r.Context())
	if err != nil {
		return nil, err
	}
	out := map[string]any{"roles": []map[string]any{}, "groups": []map[string]any{}}
	if provider.ID == "" {
		return out, nil
	}
	roles := []map[string]any{}
	rows, err := s.DB.Query(r.Context(), `SELECT m.external_role,m.role_id,r.name FROM oidc_role_mappings m JOIN roles r ON r.id=m.role_id WHERE m.provider_id=$1 ORDER BY m.external_role`, provider.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var external, id, name string
		if err = rows.Scan(&external, &id, &name); err != nil {
			rows.Close()
			return nil, err
		}
		roles = append(roles, map[string]any{"externalRole": external, "roleId": id, "roleName": name})
	}
	rows.Close()
	groups := []map[string]any{}
	rows, err = s.DB.Query(r.Context(), `SELECT m.external_group,m.organization_id,o.name FROM oidc_group_mappings m JOIN organizations o ON o.id=m.organization_id WHERE m.provider_id=$1 ORDER BY m.external_group`, provider.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var external, id, name string
		if err = rows.Scan(&external, &id, &name); err != nil {
			return nil, err
		}
		groups = append(groups, map[string]any{"externalGroup": external, "organizationId": id, "organizationName": name})
	}
	out["roles"], out["groups"] = roles, groups
	return out, rows.Err()
}

func (s *Server) getOIDCMappings(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	v, err := s.oidcMappings(r)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) putOIDCMappings(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := requireAdmin(p, true); err != nil {
		s.serviceError(w, r, err)
		return
	}
	var in oidcMappingsInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	provider, err := s.OIDC.Get(r.Context())
	if err != nil || provider.ID == "" {
		s.serviceError(w, r, errors.New("OIDC provider must be saved before claim mappings"))
		return
	}
	before, _ := s.oidcMappings(r)
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `DELETE FROM oidc_role_mappings WHERE provider_id=$1`, provider.ID)
	if err == nil {
		_, err = tx.Exec(r.Context(), `DELETE FROM oidc_group_mappings WHERE provider_id=$1`, provider.ID)
	}
	if err == nil {
		for _, item := range in.Roles {
			if strings.TrimSpace(item.ExternalRole) == "" || strings.TrimSpace(item.RoleID) == "" {
				err = errors.New("externalRole and roleId are required")
				break
			}
			_, err = tx.Exec(r.Context(), `INSERT INTO oidc_role_mappings(id,provider_id,external_role,role_id) VALUES($1,$2,$3,$4)`, ids.New(), provider.ID, strings.TrimSpace(item.ExternalRole), item.RoleID)
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		for _, item := range in.Groups {
			if strings.TrimSpace(item.ExternalGroup) == "" || strings.TrimSpace(item.OrganizationID) == "" {
				err = errors.New("externalGroup and organizationId are required")
				break
			}
			_, err = tx.Exec(r.Context(), `INSERT INTO oidc_group_mappings(id,provider_id,external_group,organization_id) VALUES($1,$2,$3,$4)`, ids.New(), provider.ID, strings.TrimSpace(item.ExternalGroup), item.OrganizationID)
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "OIDC_CLAIM_MAPPINGS_UPDATE", Resource: "oidc_provider", ResourceID: provider.ID, Before: before, After: in, IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
	v, err := s.oidcMappings(r)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}

func (s *Server) listPolicies(w http.ResponseWriter, r *http.Request) {
	v, err := s.Approvals.Policies(r.Context(), principal(r), strings.ToUpper(r.URL.Query().Get("entityType")))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": v, "workflowEnabled": len(v) > 0})
}
func (s *Server) savePolicy(w http.ResponseWriter, r *http.Request) {
	var in approval.Policy
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if id := r.PathValue("id"); id != "" {
		in.ID = id
	}
	v, err := s.Approvals.SavePolicy(r.Context(), principal(r), in, httpx.ClientIP(r), httpx.RequestID(r.Context()), r.UserAgent())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	status := 200
	if r.Method == "POST" {
		status = 201
	}
	httpx.JSON(w, status, v)
}

func nullAdmin(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return strings.TrimSpace(v)
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT u.id,u.username,COALESCE(u.email,''),u.display_name,u.auth_source,COALESCE(u.organization_id::text,''),COALESCE(o.name,''),COALESCE(u.manager_id::text,''),COALESCE(u.title,''),u.active,u.is_bootstrap,u.last_login_at,u.created_at,COALESCE(array_agg(r.name) FILTER(WHERE r.id IS NOT NULL),'{}') FROM users u LEFT JOIN organizations o ON o.id=u.organization_id LEFT JOIN user_roles ur ON ur.user_id=u.id LEFT JOIN roles r ON r.id=ur.role_id GROUP BY u.id,o.name ORDER BY u.display_name`)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, username, email, name, source, orgID, orgName, manager, title string
		var active, bootstrap bool
		var last *time.Time
		var created time.Time
		var roles []string
		if err = rows.Scan(&id, &username, &email, &name, &source, &orgID, &orgName, &manager, &title, &active, &bootstrap, &last, &created, &roles); err != nil {
			s.serviceError(w, r, err)
			return
		}
		items = append(items, map[string]any{"id": id, "username": username, "email": email, "displayName": name, "authSource": source, "organizationId": orgID, "organizationName": orgName, "managerId": manager, "title": title, "active": active, "isBootstrap": bootstrap, "lastLoginAt": last, "createdAt": created, "roles": roles})
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := requireAdmin(p, true); err != nil {
		s.serviceError(w, r, err)
		return
	}
	var in struct {
		Username       string   `json:"username"`
		DisplayName    string   `json:"displayName"`
		Email          string   `json:"email"`
		Password       string   `json:"password"`
		OrganizationID string   `json:"organizationId"`
		ManagerID      string   `json:"managerId"`
		Title          string   `json:"title"`
		RoleIDs        []string `json:"roleIds"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Username) == "" || strings.TrimSpace(in.DisplayName) == "" {
		s.serviceError(w, r, errors.New("username and displayName are required"))
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	id := ids.New()
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `INSERT INTO users(id,username,email,display_name,password_hash,auth_source,organization_id,manager_id,title,active,must_change_password) VALUES($1,$2,$3,$4,$5,'LOCAL',$6,$7,$8,true,true)`, id, strings.TrimSpace(in.Username), nullAdmin(in.Email), strings.TrimSpace(in.DisplayName), hash, nullAdmin(in.OrganizationID), nullAdmin(in.ManagerID), nullAdmin(in.Title))
	if err == nil {
		for _, role := range in.RoleIDs {
			_, err = tx.Exec(r.Context(), `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, role)
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "USER_CREATE", Resource: "user", ResourceID: id, After: map[string]any{"username": in.Username, "roles": in.RoleIDs}, IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
	httpx.JSON(w, 201, map[string]any{"id": id, "username": in.Username, "mustChangePassword": true})
}
func (s *Server) setUserRoles(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := requireAdmin(p, true); err != nil {
		s.serviceError(w, r, err)
		return
	}
	var in struct {
		RoleIDs []string `json:"roleIds"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	var bootstrap bool
	if err := s.DB.QueryRow(r.Context(), `SELECT is_bootstrap FROM users WHERE id=$1`, id).Scan(&bootstrap); err != nil {
		s.serviceError(w, r, err)
		return
	}
	if bootstrap && len(in.RoleIDs) == 0 {
		s.serviceError(w, r, errors.New("bootstrap administrator role cannot be removed"))
		return
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `DELETE FROM user_roles WHERE user_id=$1`, id)
	if err == nil {
		for _, role := range in.RoleIDs {
			_, err = tx.Exec(r.Context(), `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2)`, id, role)
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "USER_ROLES_UPDATE", Resource: "user", ResourceID: id, After: map[string]any{"roleIds": in.RoleIDs}, IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
	httpx.JSON(w, 200, map[string]any{"saved": true})
}

func (s *Server) adminRoles(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT r.id,r.code,r.name,COALESCE(r.description,''),r.data_scope,r.system_role,r.is_default,
		COALESCE(array_agg(DISTINCT rp.permission) FILTER(WHERE rp.permission IS NOT NULL),'{}'),
		(SELECT count(*) FROM user_roles ur WHERE ur.role_id=r.id)
		FROM roles r LEFT JOIN role_permissions rp ON rp.role_id=r.id GROUP BY r.id ORDER BY r.system_role DESC,r.name`)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, code, name, description, scope string
		var system, isDefault bool
		var permissions []string
		var userCount int
		if err = rows.Scan(&id, &code, &name, &description, &scope, &system, &isDefault, &permissions, &userCount); err != nil {
			s.serviceError(w, r, err)
			return
		}
		items = append(items, map[string]any{"id": id, "code": code, "name": name, "description": description, "dataScope": scope, "systemRole": system, "isDefault": isDefault, "permissions": permissions, "userCount": userCount})
	}
	httpx.JSON(w, 200, map[string]any{"items": items})
}

// permissionCatalog is the authoritative list of function permissions the Role
// editor may assign. Keeping it in one place stops the console from drifting
// away from the checks the services actually perform.
var permissionCatalog = []struct {
	Group       string
	Permission  string
	Label       string
	Description string
}{
	{"고객", "customer:read", "고객 조회", "Data Scope 범위의 고객과 Customer 360을 조회합니다."},
	{"고객", "customer:write", "고객 등록·수정", "고객을 생성, 수정, 병합하고 Account Plan을 저장합니다."},
	{"담당자", "contact:read", "담당자 조회", "고객 담당자와 Relationship Map을 조회합니다."},
	{"담당자", "contact:write", "담당자 등록·수정", "담당자와 담당자 간 관계를 관리합니다."},
	{"Lead", "lead:read", "Lead 조회", "미전환 Lead 목록을 조회합니다."},
	{"Lead", "lead:write", "Lead 등록·수정", "Lead를 생성하고 전환합니다."},
	{"Opportunity", "opportunity:read", "Opportunity 조회", "Pipeline, Deal Health, Playbook을 조회합니다. Dashboard 진입에 필요합니다."},
	{"Opportunity", "opportunity:write", "Opportunity 등록·수정", "Opportunity와 Stage, Team, Playbook 실행 상태를 변경합니다."},
	{"영업활동", "activity:read", "활동 조회", "Activity Timeline을 조회합니다."},
	{"영업활동", "activity:write", "활동 기록", "미팅, 통화, 이메일 등 고객 접점을 기록합니다."},
	{"상품", "product:read", "상품 조회", "상품 카탈로그를 조회합니다."},
	{"상품", "product:write", "상품 등록·수정", "상품을 등록, 수정, 판매중지합니다."},
	{"견적", "quotation:read", "견적 조회", "견적과 버전 이력을 조회합니다."},
	{"견적", "quotation:write", "견적 작성", "견적을 생성하고 개정합니다."},
	{"계약", "contract:read", "계약 조회", "계약과 Revenue Schedule을 조회합니다."},
	{"계약", "contract:write", "계약 등록·활성화", "계약을 등록하고 활성화하며 갱신 정보를 관리합니다."},
	{"매출", "sales:read", "매출 조회", "확정 매출을 조회합니다."},
	{"매출", "sales:write", "매출 인식", "Revenue Schedule을 인식하고 매출을 등록합니다."},
	{"영업목표", "target:read", "목표 조회", "영업 목표와 달성률을 조회합니다."},
	{"영업목표", "target:write", "목표 설정", "기간별 영업 목표를 설정합니다."},
	{"Forecast", "forecast:read", "Forecast 조회", "Forecast, Waterfall, Snapshot을 조회합니다."},
	{"Forecast", "forecast:write", "Forecast Override", "팀장 판단으로 Forecast를 조정합니다."},
	{"보고서", "report:read", "보고서 조회", "Win/Loss 분석과 리포트를 조회합니다."},
	{"알림", "notification:read", "알림 조회", "본인 알림을 조회합니다."},
	{"알림", "notification:write", "알림 처리", "알림을 읽음 처리합니다."},
	{"승인", "approval:request", "검토 요청", "활성 승인 정책이 있을 때 팀장 검토를 요청합니다."},
	{"승인", "approval:approve", "검토 승인·반려", "지정된 승인자로서 요청을 승인하거나 반려합니다."},
	{"고객의 목소리", "voice:read", "고객 요청 조회", "담당 고객의 불만, 요청, 문의와 처리 이력을 조회합니다."},
	{"고객의 목소리", "voice:write", "고객 요청 접수·처리", "요청을 접수하고 상태를 진행, 해결로 변경합니다."},
	{"고객의 목소리", "voice:manage", "타인 요청 처리·재배정", "다른 담당자의 요청을 처리하거나 담당자를 변경합니다."},
	{"연동", "mcp:use", "MCP 사용", "AI 에이전트가 MCP 채널로 접근할 수 있게 합니다."},
	{"관리", "admin:read", "관리자 조회", "Admin Console의 설정, 감사 로그, 진단을 조회합니다."},
	{"관리", "admin:write", "관리자 변경", "설정, 사용자, Role, 정책을 변경합니다."},
	{"관리", "admin:*", "전체 관리자", "모든 기능 권한을 포함합니다. 시스템 관리자 전용입니다."},
}

// knownPermission rejects a typo before it silently produces a Role that grants
// nothing, which is exactly how an SSO user ends up locked out of every screen.
func knownPermission(permission string) bool {
	for _, entry := range permissionCatalog {
		if entry.Permission == permission {
			return true
		}
	}
	return false
}

func validatePermissions(permissions []string) error {
	for _, permission := range permissions {
		if !knownPermission(strings.ToLower(strings.TrimSpace(permission))) {
			return fmt.Errorf("unknown permission %q", permission)
		}
	}
	return nil
}

func (s *Server) adminPermissionCatalog(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(permissionCatalog))
	for _, entry := range permissionCatalog {
		items = append(items, map[string]any{"group": entry.Group, "permission": entry.Permission, "label": entry.Label, "description": entry.Description})
	}
	httpx.JSON(w, 200, map[string]any{"items": items, "dataScopes": []map[string]string{
		{"value": "USER", "label": "본인 데이터"},
		{"value": "TEAM", "label": "팀 (직속 부하 포함)"},
		{"value": "DEPARTMENT", "label": "부서"},
		{"value": "DIVISION", "label": "본부"},
		{"value": "COMPANY", "label": "전사"},
	}})
}
func (s *Server) createRole(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := requireAdmin(p, true); err != nil {
		s.serviceError(w, r, err)
		return
	}
	var in struct {
		Code        string   `json:"code"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		DataScope   string   `json:"dataScope"`
		Permissions []string `json:"permissions"`
		IsDefault   bool     `json:"isDefault"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.DataScope = strings.ToUpper(in.DataScope)
	if in.Code == "" || in.Name == "" {
		s.serviceError(w, r, errors.New("code and name are required"))
		return
	}
	if !validDataScopes[in.DataScope] {
		s.serviceError(w, r, errors.New("invalid dataScope"))
		return
	}
	if err := validatePermissions(in.Permissions); err != nil {
		s.serviceError(w, r, err)
		return
	}
	id := ids.New()
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	if in.IsDefault {
		if _, err = tx.Exec(r.Context(), `UPDATE roles SET is_default=false WHERE is_default`); err != nil {
			s.serviceError(w, r, err)
			return
		}
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO roles(id,code,name,description,data_scope,is_default) VALUES($1,$2,$3,$4,$5,$6)`, id, in.Code, in.Name, nullAdmin(in.Description), in.DataScope, in.IsDefault)
	if err == nil {
		for _, permission := range normalizePermissions(in.Permissions) {
			_, err = tx.Exec(r.Context(), `INSERT INTO role_permissions(role_id,permission) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, permission)
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "ROLE_CREATE", Resource: "role", ResourceID: id, After: in, IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
	httpx.JSON(w, 201, map[string]any{"id": id})
}
