package api

import "github.com/hkjang/relio/internal/platform/version"

func OpenAPI() map[string]any {
	return map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": "Relio REST API", "version": version.Current().Version, "description": "오프라인 B2B CRM 및 영업관리 API. 세션 또는 Personal Access Key로 인증합니다."},
		"servers": []map[string]any{{"url": "/api/v1"}},
		"components": map[string]any{
			"securitySchemes": map[string]any{"personalKey": map[string]any{"type": "http", "scheme": "bearer", "description": "relio_{keyId}_{secret}"}},
			"schemas":         map[string]any{"Error": map[string]any{"type": "object", "properties": map[string]any{"error": map[string]any{"type": "object", "properties": map[string]any{"code": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}, "requestId": map[string]any{"type": "string"}}}}}},
		},
		"security": []map[string]any{{"personalKey": []string{}}},
		"paths": map[string]any{
			"/system/version":               get("서비스 빌드 버전", false),
			"/customers":                    methods("고객 검색 및 생성", "customer:read", "customer:write"),
			"/customers/{id}":               readUpdate("고객 조회 및 수정", "customer:read", "customer:write"),
			"/customers/{id}/360":           get("Customer 360", "customer:read + opportunity:read + activity:read"),
			"/customers/{id}/relationships": methods("고객 담당자 Relationship Graph", "customer:read + contact:read", "contact:write"),
			"/customers/{id}/relationships/{relationshipId}": map[string]any{"put": operation("담당자 관계 수정", "contact:write"), "delete": operation("담당자 관계 삭제", "contact:write")},
			"/customers/{id}/account-plan":                   readUpdate("Strategic Account Plan", "customer:read", "customer:write"),
			"/customers/{id}/cross-sell":                     get("White Space Cross-sell 후보", "customer:read"),
			"/customers/{id}/duplicates":                     get("중복 고객 후보", "customer:read"),
			"/customers/{id}/merge":                          map[string]any{"post": operation("고객 병합", "customer:write")},
			"/contacts":                                      methods("담당자 조회 및 생성", "contact:read", "contact:write"),
			"/leads":                                         methods("Lead 조회 및 생성", "lead:read", "lead:write"),
			"/opportunities":                                 methods("영업기회 조회 및 생성", "opportunity:read", "opportunity:write"),
			"/opportunities/{id}":                            readUpdate("영업기회 조회 및 수정", "opportunity:read", "opportunity:write"),
			"/opportunities/{id}/stage":                      map[string]any{"post": operation("Stage 변경", "opportunity:write")},
			"/opportunities/{id}/health":                     get("설명 가능한 Deal Health", "opportunity:read"),
			"/opportunities/{id}/inspection":                 get("Deal 변화 및 위험 분석", "opportunity:read"),
			"/opportunities/{id}/playbook":                   readUpdate("Stage Sales Playbook", "opportunity:read", "opportunity:write"),
			"/opportunities/{id}/stage-readiness":            get("Stage Exit Criteria 판정", "opportunity:read"),
			"/opportunities/{id}/team":                       get("Opportunity Team", "opportunity:read"),
			"/opportunities/{id}/team/{userId}":              map[string]any{"put": operation("Opportunity Team 구성원 저장", "opportunity:write"), "delete": operation("Opportunity Team 구성원 제외", "opportunity:write")},
			"/collaborators":                                 get("Data Scope 내 협업 사용자", "opportunity:read"),
			"/deal-intelligence/at-risk":                     get("위험 Deal 우선순위", "opportunity:read"),
			"/deal-intelligence/coaching":                    get("팀장 Sales Coaching", "opportunity:read"),
			"/activities":                                    methods("활동 조회 및 등록", "activity:read", "activity:write"),
			"/pipeline":                                      get("Pipeline 조회", "opportunity:read"),
			"/products":                                      methods("상품 조회 및 생성", "product:read", "product:write"),
			"/forecasts":                                     get("Forecast 조회", "forecast:read"),
			"/forecasts/intelligence":                        get("Forecast Snapshot 및 Waterfall", "forecast:read"),
			"/forecasts/overrides/{id}":                      map[string]any{"put": operation("Manager Forecast Override", "forecast:write")},
			"/sales/kpi":                                     get("영업 KPI", "sales:read"),
			"/tasks/due":                                     get("기한 임박 후속 활동", "activity:read"),
			"/quotations":                                    methods("견적 조회 및 생성", "quotation:read", "quotation:write"),
			"/contracts":                                     methods("계약 조회 및 생성", "contract:read", "contract:write"),
			"/sales":                                         methods("매출 조회 및 등록", "sales:read", "sales:write"),
			"/targets":                                       methods("영업목표 조회 및 생성", "target:read", "target:write"),
			"/notifications":                                 get("알림 조회", "notification:read"),
			"/notifications/{id}/read":                       map[string]any{"post": operation("알림 읽음 처리", "notification:write")},
			"/reports":                                       get("영업 보고서", "report:read"),
			"/reports/win-loss":                              get("Win/Loss 분석", "report:read"),
			"/search":                                        get("Data Scope 적용 통합 검색", "customer:read"),
			"/approvals":                                     methods("승인 요청 목록 및 제출", "approval:request", "approval:request"),
			"/approvals/status":                              get("승인 Workflow 활성 상태", "approval:request"),
			"/approvals/capability":                          get("대상별 승인 정책 판정", "approval:request"),
			"/approvals/{id}":                                get("승인 상태", "approval:request"),
			"/approvals/{id}/approve":                        map[string]any{"post": operation("승인", "approval:approve")},
			"/approvals/{id}/reject":                         map[string]any{"post": operation("반려", "approval:approve")},
			"/me/keys":                                       methods("개인 API/MCP Key 목록 및 생성", "", ""),
			"/me/keys/{id}/rotate":                           map[string]any{"post": operation("개인 Key Rotation", "")},
			"/me/keys/{id}":                                  map[string]any{"delete": operation("개인 Key Revoke", "")},
			"/me/activity":                                   get("내 접속/API/MCP 활동", ""),
			"/me/sessions":                                   get("내 로그인 Session", ""),
			"/me/sessions/{id}":                              map[string]any{"delete": operation("Session 폐기", "")},
			"/admin/settings":                                get("관리자 설정 목록", "admin:read"),
			"/admin/settings/{namespace}/{key}":              map[string]any{"put": operation("관리자 설정 변경", "admin:write")},
			"/admin/oidc":                                    readUpdate("Keycloak OIDC 설정", "admin:read", "admin:write"),
			"/admin/oidc/test":                               map[string]any{"post": operation("OIDC 연결 테스트", "admin:write")},
			"/admin/oidc/mappings":                           readUpdate("OIDC Role·Group Claim 매핑", "admin:read", "admin:write"),
			"/admin/approval-policies":                       methods("승인 정책 조회 및 생성", "admin:read", "admin:write"),
			"/admin/audit":                                   get("필터·검색 가능한 감사 로그", "admin:read"),
			"/admin/operations":                              get("운영 준비도·진단·조치 센터", "admin:read"),
			"/admin/operations/support-bundle":               get("비밀값을 제외한 운영 Support Bundle", "admin:read"),
			"/admin/personal-keys":                           get("Personal Key Metadata", "admin:read"),
			"/admin/personal-keys/{id}":                      map[string]any{"delete": operation("Personal Key 강제 폐기", "admin:write")},
			"/admin/users/{id}/keys/revoke-all":              map[string]any{"post": operation("사용자 전체 Key 폐기", "admin:write")},
			"/admin/sales-execution":                         get("Stage Playbook 및 Exit Criteria", "admin:read"),
			"/admin/stages/{id}/sales-execution":             map[string]any{"put": operation("Sales Execution 정책 저장", "admin:write")},
			"/admin/deal-health-rules":                       get("Deal Health Rule 목록", "admin:read"),
			"/admin/deal-health-rules/{id}":                  map[string]any{"put": operation("Deal Health Rule 변경", "admin:write")},
		},
	}
}
func get(summary string, permission any) map[string]any {
	return map[string]any{"get": operation(summary, permission)}
}
func methods(summary, read, write string) map[string]any {
	return map[string]any{"get": operation(summary, read), "post": operation(summary, write)}
}

func readUpdate(summary, read, write string) map[string]any {
	return map[string]any{"get": operation(summary, read), "put": operation(summary, write)}
}
func operation(summary string, permission any) map[string]any {
	return map[string]any{"summary": summary, "description": func() string {
		if p, ok := permission.(string); ok && p != "" {
			return "Required scope: " + p
		}
		return ""
	}(), "responses": map[string]any{"200": map[string]any{"description": "Success"}, "400": map[string]any{"description": "Invalid request"}, "401": map[string]any{"description": "Authentication required"}, "403": map[string]any{"description": "Permission denied"}}}
}
