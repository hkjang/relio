package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/approval"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/intelligence"
	"github.com/hkjang/relio/internal/platform/httpx"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/hkjang/relio/internal/platform/version"
	"github.com/hkjang/relio/internal/relationship"
	"github.com/hkjang/relio/internal/voice"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const ProtocolVersion = "2025-11-25"

// Every revision this server can speak, newest first. Nothing in the
// implementation is version-specific — the differences that matter to us
// (batching, the protocol header) are handled explicitly — so the list is the
// set of clients we accept rather than a set of behaviours.
var supportedVersionOrder = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

var supportedVersions = map[string]bool{}

func init() {
	for _, v := range supportedVersionOrder {
		supportedVersions[v] = true
	}
}

// negotiateVersion answers the version the client asked for when we speak it.
// Replying with our own newest instead makes the client send a header we then
// reject, which is how "auth works but tools/list fails" happens.
func negotiateVersion(requested string) string {
	if supportedVersions[strings.TrimSpace(requested)] {
		return strings.TrimSpace(requested)
	}
	return ProtocolVersion
}

type Server struct {
	DB            *pgxpool.Pool
	CRM           *crm.Service
	Approvals     *approval.Service
	Intelligence  *intelligence.Service
	Relationships *relationship.Service
	Voices        *voice.Service
}
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}
type tool struct {
	Name           string         `json:"name"`
	Title          string         `json:"title,omitempty"`
	Description    string         `json:"description"`
	InputSchema    map[string]any `json:"inputSchema"`
	Annotations    map[string]any `json:"annotations,omitempty"`
	RequiredScopes []string       `json:"-"`
}

// ToolCatalogItem is the server-authoritative bridge between key scopes and
// tools/list. The key screen uses it to show what a selected scope combination
// will actually expose instead of guessing from scope names.
type ToolCatalogItem struct {
	Name           string   `json:"name"`
	Title          string   `json:"title"`
	RequiredScopes []string `json:"requiredScopes"`
	ReadOnly       bool     `json:"readOnly"`
}
type toolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func schema(required []string, properties map[string]any) map[string]any {
	result := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	// JSON Schema requires `required` to be an array when present. Encoding a
	// nil Go slice produced `"required": null`; Qwen tolerated it, while
	// OpenCode correctly rejected the entire tools/list response.
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}
func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
func number(description string) map[string]any {
	return map[string]any{"type": "number", "description": description}
}
func integer(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}
func boolean(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func (s *Server) allowedOrigin(ctx context.Context, r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	var raw []byte
	allowed := []string{}
	if s.DB.QueryRow(ctx, `SELECT value FROM system_settings WHERE namespace='mcp' AND key='allowed_origins'`).Scan(&raw) == nil {
		_ = json.Unmarshal(raw, &allowed)
	}
	var serviceRaw []byte
	var serviceURL string
	if s.DB.QueryRow(ctx, `SELECT value FROM system_settings WHERE namespace='system' AND key='service_url'`).Scan(&serviceRaw) == nil {
		_ = json.Unmarshal(serviceRaw, &serviceURL)
		if u, e := url.Parse(serviceURL); e == nil && u.Scheme != "" && u.Host != "" {
			allowed = append(allowed, u.Scheme+"://"+u.Host)
		}
	}
	for _, v := range allowed {
		if strings.EqualFold(strings.TrimRight(v, "/"), strings.TrimRight(origin, "/")) {
			return true
		}
	}
	return false
}
func acceptsMCP(r *http.Request) bool {
	a := strings.ToLower(r.Header.Get("Accept"))
	if strings.TrimSpace(a) == "" {
		return true
	}
	return strings.Contains(a, "application/json") || strings.Contains(a, "text/event-stream") ||
		strings.Contains(a, "application/*") || strings.Contains(a, "*/*")
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	p := auth.FromContext(r.Context())
	success := false
	method, toolName := "", ""
	defer func() {
		var key any
		if p != nil && p.KeyDBID != "" {
			key = p.KeyDBID
		}
		var actor any
		if p != nil {
			actor = p.UserID
		}
		_, _ = s.DB.Exec(context.Background(), `INSERT INTO mcp_request_logs(id,actor_id,key_id,method,tool_name,success,duration_ms,request_id,ip) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,'')::inet)`, ids.New(), actor, key, method, nullValue(toolName), success, int(time.Since(start).Milliseconds()), httpx.RequestID(r.Context()), httpx.ClientIP(r))
	}()
	if p == nil || !p.ChannelAllowed("MCP") || !p.Has("mcp:use") {
		httpx.ErrorJSON(w, r, http.StatusForbidden, "mcp_access_denied", "MCP 채널 및 mcp:use 권한이 필요합니다.", nil)
		return
	}
	if !s.allowedOrigin(r.Context(), r) {
		httpx.ErrorJSON(w, r, http.StatusForbidden, "invalid_origin", "허용되지 않은 MCP Origin입니다.", nil)
		return
	}
	if r.Method == http.MethodGet {
		// A server that does not offer a server-initiated stream answers 405.
		w.Header().Set("Allow", "POST")
		httpx.ErrorJSON(w, r, http.StatusMethodNotAllowed, "sse_not_supported", "이 서버는 서버 주도 SSE 스트림을 제공하지 않습니다.", nil)
		return
	}
	if r.Method == http.MethodDelete {
		// Sessions are not issued, so there is nothing to tear down. Answering
		// 200 keeps clients that always send DELETE on shutdown quiet.
		w.WriteHeader(http.StatusOK)
		success = true
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST, DELETE")
		httpx.ErrorJSON(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "POST를 사용하세요.", nil)
		return
	}
	if !acceptsMCP(r) {
		httpx.ErrorJSON(w, r, http.StatusNotAcceptable, "accept_required", "Accept에 application/json을 포함해야 합니다.", nil)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		s.writeError(w, nil, -32700, "Parse error", nil)
		return
	}
	// Revisions before 2025-06-18 allow a JSON-RPC batch, and clients still send
	// them. Rejecting an array as malformed JSON broke tools/list for those.
	batch, requests, perr := parseRequests(body)
	if perr != nil {
		s.writeError(w, nil, -32700, "Parse error", map[string]any{"cause": perr.Error()})
		return
	}
	if len(requests) == 0 {
		s.writeError(w, nil, -32600, "Invalid Request", nil)
		return
	}
	methods := make([]string, 0, len(requests))
	for _, req := range requests {
		methods = append(methods, req.Method)
	}
	method = strings.Join(methods, ",")

	// The negotiated version arrives as a header on every later request. It is
	// checked once for the whole HTTP request, and reported as a JSON-RPC error
	// so a client can read it the same way it reads everything else.
	negotiated := ProtocolVersion
	if header := strings.TrimSpace(r.Header.Get("MCP-Protocol-Version")); header != "" {
		if !supportedVersions[header] {
			onlyInitialize := true
			for _, req := range requests {
				if req.Method != "initialize" {
					onlyInitialize = false
					break
				}
			}
			if !onlyInitialize {
				s.writeError(w, firstID(requests), -32600,
					"지원하지 않는 MCP 프로토콜 버전입니다: "+header,
					map[string]any{"supported": supportedVersionOrder})
				return
			}
		} else {
			negotiated = header
		}
	}

	out := make([]response, 0, len(requests))
	for _, req := range requests {
		if req.JSONRPC != "2.0" || req.Method == "" {
			out = append(out, response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32600, Message: "Invalid Request"}})
			continue
		}
		if isNotification(req) {
			// Notifications are acknowledged, never answered.
			continue
		}
		result, callErr, tool := s.dispatch(r, p, req, negotiated)
		if tool != "" {
			toolName = tool
		}
		if callErr != nil {
			out = append(out, response{JSONRPC: "2.0", ID: req.ID, Error: callErr})
			continue
		}
		out = append(out, response{JSONRPC: "2.0", ID: req.ID, Result: result})
	}

	w.Header().Set("MCP-Protocol-Version", negotiated)
	if len(out) == 0 {
		// Every message was a notification; there is nothing to return.
		w.WriteHeader(http.StatusAccepted)
		success = true
		return
	}
	if batch {
		httpx.JSON(w, http.StatusOK, out)
	} else {
		httpx.JSON(w, http.StatusOK, out[0])
	}
	// A tool that ran and failed returns a result, not a JSON-RPC error, so the
	// request log has to look inside it or every failure would read as a success.
	success = true
	for _, item := range out {
		if item.Error != nil {
			success = false
			continue
		}
		if m, ok := item.Result.(map[string]any); ok {
			if failed, _ := m["isError"].(bool); failed {
				success = false
			}
		}
	}
}

// parseRequests accepts either one JSON-RPC message or a batch of them.
func parseRequests(body []byte) (bool, []request, error) {
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		var items []request
		if err := json.Unmarshal(body, &items); err != nil {
			return true, nil, err
		}
		return true, items, nil
	}
	var single request
	if err := json.Unmarshal(body, &single); err != nil {
		return false, nil, err
	}
	return false, []request{single}, nil
}

func isNotification(req request) bool {
	return len(req.ID) == 0 || string(req.ID) == "null"
}

func firstID(requests []request) json.RawMessage {
	for _, req := range requests {
		if !isNotification(req) {
			return req.ID
		}
	}
	return nil
}

// dispatch runs one JSON-RPC method and returns its result, a JSON-RPC error, or
// the tool name it invoked for the request log.
func (s *Server) dispatch(r *http.Request, p *auth.Principal, req request, negotiated string) (any, *rpcError, string) {
	switch req.Method {
	case "initialize":
		// Answer the version the client asked for when we speak it. Replying
		// with our own newest regardless is what made the client send a header
		// we then rejected.
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		return map[string]any{
			"protocolVersion": negotiateVersion(params.ProtocolVersion),
			"capabilities": map[string]any{
				"tools":     map[string]any{"listChanged": false},
				"resources": map[string]any{"subscribe": false, "listChanged": false},
			},
			"serverInfo":   map[string]any{"name": "Relio", "title": "Relio CRM MCP Server", "version": version.Current().Version},
			"instructions": "Relio CRM data is filtered by the authenticated user's permissions, data scope, and key scopes.",
		}, nil, ""
	case "ping":
		return map[string]any{}, nil, ""
	case "tools/list":
		return map[string]any{"tools": s.tools(r.Context(), p)}, nil, ""
	case "tools/call":
		var call toolCall
		if err := json.Unmarshal(req.Params, &call); err != nil {
			return nil, &rpcError{Code: -32602, Message: "Invalid params: " + err.Error()}, ""
		}
		result, err := s.callTool(r.Context(), p, call, r)
		if errors.Is(err, errUnknownTool) {
			return nil, &rpcError{Code: -32602, Message: err.Error()}, call.Name
		}
		if err != nil {
			return toolFailure(err), nil, call.Name
		}
		return result, nil, call.Name
	case "resources/list":
		return map[string]any{"resources": s.resources(p)}, nil, ""
	case "resources/templates/list":
		return map[string]any{"resourceTemplates": s.templates(p)}, nil, ""
	case "resources/read":
		var params struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &rpcError{Code: -32602, Message: "Invalid params: " + err.Error()}, ""
		}
		result, err := s.readResource(r.Context(), p, params.URI)
		if err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}, ""
		}
		return result, nil, ""
	// Clients send these after initialize and on shutdown; acknowledging them
	// beats answering "Method not found".
	case "notifications/initialized", "notifications/cancelled", "completion/complete":
		return map[string]any{}, nil, ""
	}
	return nil, &rpcError{Code: -32601, Message: "Method not found: " + req.Method}, ""
}

func nullValue(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func keys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}
func (s *Server) writeError(w http.ResponseWriter, id json.RawMessage, code int, message string, data any) {
	httpx.JSON(w, http.StatusOK, response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message, Data: data}})
}

func (s *Server) approvalsEnabled(ctx context.Context) bool {
	var enabled bool
	_ = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM approval_policies WHERE active=true)`).Scan(&enabled)
	return enabled
}

func (s *Server) toolAllowlist(ctx context.Context) (map[string]bool, bool) {
	var raw []byte
	if err := s.DB.QueryRow(ctx, `SELECT value FROM system_settings WHERE namespace='mcp' AND key='tool_allowlist'`).Scan(&raw); err != nil {
		return nil, false
	}
	var allowed []string
	if err := json.Unmarshal(raw, &allowed); err != nil || len(allowed) == 0 {
		return nil, false
	}
	result := make(map[string]bool, len(allowed))
	for _, item := range allowed {
		result[item] = true
	}
	return result, true
}

func (s *Server) tools(ctx context.Context, p *auth.Principal) []tool {
	out := []tool{}
	allowlist, restricted := s.toolAllowlist(ctx)
	add := func(permission, name, title, description string, input map[string]any, readOnly, dangerous bool) {
		requiredScopes := strings.Fields(permission)
		permitted := true
		for _, required := range requiredScopes {
			if !p.Has(required) {
				permitted = false
				break
			}
		}
		if permitted && (!restricted || allowlist[name]) {
			riskLevel := "READ"
			if !readOnly {
				riskLevel = "WRITE"
			}
			if strings.HasPrefix(name, "find_") || strings.HasPrefix(name, "explain_") || strings.HasPrefix(name, "recommend_") || name == "get_sales_coaching_insights" || name == "get_manager_review_queue" {
				riskLevel = "ANALYZE"
			}
			if name == "approve_request" || name == "reject_request" {
				riskLevel = "APPROVAL"
			}
			out = append(out, tool{Name: name, Title: title, Description: description, InputSchema: input, Annotations: map[string]any{"readOnlyHint": readOnly, "destructiveHint": dangerous, "idempotentHint": readOnly, "relio/riskLevel": riskLevel}, RequiredScopes: requiredScopes})
		}
	}
	qprops := map[string]any{"query": str("검색어"), "limit": integer("최대 결과 수")}
	add("customer:read", "search_customers", "고객 검색", "이름 또는 사업자번호로 접근 가능한 고객을 검색합니다.", schema(nil, qprops), true, false)
	add("customer:read", "get_customer", "고객 상세", "고객 기본 정보를 조회합니다.", schema([]string{"id"}, map[string]any{"id": str("고객 ID")}), true, false)
	add("customer:read", "get_customer_360", "Customer 360", "담당자, 영업기회, 활동, 계약, 누적매출을 함께 조회합니다.", schema([]string{"id"}, map[string]any{"id": str("고객 ID")}), true, false)
	customerProps := map[string]any{"name": str("고객명"), "registrationNo": str("사업자등록번호"), "customerType": str("PROSPECT, CUSTOMER, PARTNER"), "grade": str("고객 등급"), "industry": str("산업군"), "website": str("웹사이트"), "phone": str("대표 전화"), "email": str("대표 이메일"), "address": str("주소"), "health": str("NORMAL, ATTENTION, RISK"), "annualRevenue": number("연 매출"), "employeeCount": integer("직원 수"), "customFields": map[string]any{"type": "object", "description": "사용자 정의 필드", "additionalProperties": true}}
	add("customer:read customer:write", "create_customer", "고객 등록", "새 고객을 CRM에 등록하고 생성된 고객 ID와 버전을 반환합니다. 고객명만 필수이며 나머지는 선택입니다.", schema([]string{"name"}, customerProps), false, false)
	updateCustomerProps := map[string]any{"id": str("고객 ID"), "version": integer("get_customer에서 확인한 현재 버전")}
	for key, value := range customerProps {
		updateCustomerProps[key] = value
	}
	add("customer:read customer:write", "update_customer", "고객 수정", "고객을 버전 검사와 함께 부분 수정합니다. 보내지 않은 필드는 기존 값을 유지합니다.", schema([]string{"id", "version"}, updateCustomerProps), false, false)
	add("customer:read customer:delete", "delete_customer", "고객 삭제 또는 비활성화", "이력이 없는 고객은 삭제하고, 영업 이력이 있으면 감사 기록 보존을 위해 비활성화합니다.", schema([]string{"id"}, map[string]any{"id": str("고객 ID")}), false, true)
	add("contact:read", "search_contacts", "담당자 검색", "고객 담당자를 검색합니다.", schema(nil, map[string]any{"query": str("검색어"), "customerId": str("고객 ID"), "limit": integer("최대 결과 수")}), true, false)
	add("customer:read contact:read", "get_contact", "담당자 상세", "접근 가능한 고객의 담당자 한 명을 조회합니다.", schema([]string{"id"}, map[string]any{"id": str("담당자 ID")}), true, false)
	contactProps := map[string]any{"customerId": str("고객 ID"), "name": str("담당자명"), "title": str("직함"), "department": str("부서"), "email": str("이메일"), "phone": str("전화"), "mobile": str("휴대전화"), "decisionMaker": boolean("의사결정권자 여부"), "primaryContact": boolean("주 담당자 여부"), "relationshipRole": str("DECISION_MAKER, CHAMPION, INFLUENCER, USER, PROCUREMENT"), "influence": str("HIGH, MEDIUM, LOW"), "sentiment": str("SUPPORT, NEUTRAL, OPPOSE"), "relationshipStrength": integer("관계 강도 0~100"), "decisionPower": integer("의사결정 영향력 0~100")}
	add("customer:read contact:read contact:write", "create_contact", "담당자 등록", "고객사 담당자를 등록하고 관계 역할과 영향력을 함께 기록합니다.", schema([]string{"customerId", "name"}, contactProps), false, false)
	updateContactProps := map[string]any{"id": str("담당자 ID")}
	for key, value := range contactProps {
		updateContactProps[key] = value
	}
	add("customer:read contact:read contact:write", "update_contact", "담당자 수정", "담당자 정보를 부분 수정합니다. 보내지 않은 필드는 기존 값을 유지합니다.", schema([]string{"id"}, updateContactProps), false, false)
	add("customer:read contact:read contact:write", "delete_contact", "담당자 삭제", "고객 요청 이력이 없는 담당자와 관계 연결을 삭제합니다.", schema([]string{"id"}, map[string]any{"id": str("담당자 ID")}), false, true)
	add("lead:read", "list_leads", "Lead 조회", "접근 가능한 잠재고객 Lead를 검색합니다.", schema(nil, qprops), true, false)
	add("lead:write", "create_lead", "Lead 등록", "초기 잠재고객 Lead를 등록합니다.", schema([]string{"name"}, map[string]any{"name": str("Lead 이름"), "company": str("회사명"), "email": str("이메일"), "phone": str("전화"), "source": str("유입 경로"), "status": str("NEW 등 Lead 상태"), "customFields": map[string]any{"type": "object", "additionalProperties": true}}), false, false)
	add("product:read", "list_products", "상품 조회", "활성 여부를 포함한 상품·서비스 카탈로그를 검색합니다.", schema(nil, qprops), true, false)
	add("product:write", "create_product", "상품 등록", "견적과 영업에 사용할 상품·서비스를 등록합니다.", schema([]string{"code", "name", "unitPrice"}, map[string]any{"code": str("상품 코드"), "name": str("상품명"), "description": str("설명"), "unitPrice": number("단가"), "active": boolean("활성 여부")}), false, false)
	add("opportunity:read", "list_opportunities", "영업기회 조회", "접근 가능한 영업기회를 조회합니다.", schema(nil, map[string]any{"query": str("검색어"), "customerId": str("고객 ID"), "status": str("OPEN, WON, LOST"), "limit": integer("최대 결과 수")}), true, false)
	add("opportunity:read", "get_opportunity", "영업기회 상세", "영업기회와 자동 Health 신호를 조회합니다.", schema([]string{"id"}, map[string]any{"id": str("영업기회 ID")}), true, false)
	oppProps := map[string]any{"name": str("영업기회명"), "customerId": str("고객 ID"), "stageId": str("Stage ID"), "expectedAmount": number("예상 금액"), "expectedCloseDate": str("YYYY-MM-DD"), "nextAction": str("다음 행동"), "nextActionDate": str("YYYY-MM-DD")}
	add("customer:read opportunity:read opportunity:write", "create_opportunity", "영업기회 생성", "새 영업기회를 생성합니다.", schema([]string{"name", "customerId"}, oppProps), false, false)
	updateProps := map[string]any{"id": str("영업기회 ID"), "version": integer("낙관적 잠금 버전")}
	for k, v := range oppProps {
		updateProps[k] = v
	}
	add("opportunity:read opportunity:write", "update_opportunity", "영업기회 수정", "버전 검사를 적용해 영업기회를 수정합니다.", schema([]string{"id", "version"}, updateProps), false, false)
	add("opportunity:read opportunity:write", "change_opportunity_stage", "Stage 변경", "영업기회의 Stage를 변경하고 이력을 남깁니다.", schema([]string{"id", "stageId", "version"}, map[string]any{"id": str("영업기회 ID"), "stageId": str("Stage ID"), "version": integer("현재 버전")}), false, false)
	add("activity:write", "add_activity", "영업활동 등록", "통화, 미팅, 이메일 등 활동을 등록합니다.", schema([]string{"activityType", "subject"}, map[string]any{"customerId": str("고객 ID"), "opportunityId": str("영업기회 ID"), "activityType": str("활동 유형"), "subject": str("제목"), "description": str("설명"), "nextAction": str("후속 행동"), "nextActionDate": str("YYYY-MM-DD")}), false, false)
	add("activity:read", "list_activities", "활동 이력", "고객 또는 영업기회의 최근 활동을 조회합니다.", schema(nil, map[string]any{"customerId": str("고객 ID"), "opportunityId": str("영업기회 ID"), "limit": integer("최대 결과 수")}), true, false)
	add("opportunity:read", "get_pipeline", "Pipeline 조회", "설정된 Pipeline과 Stage를 조회합니다.", schema(nil, map[string]any{}), true, false)
	add("forecast:read", "get_forecast", "Forecast 조회", "Commit, Best Case, Pipeline 금액을 조회합니다.", schema(nil, map[string]any{}), true, false)
	add("sales:read forecast:read", "get_sales_kpi", "영업 KPI", "매출, 목표 달성률과 Forecast를 조회합니다.", schema(nil, map[string]any{}), true, false)
	add("activity:read", "get_due_actions", "후속활동 조회", "기한이 다가온 후속 작업을 조회합니다.", schema(nil, map[string]any{"days": integer("조회할 일수"), "limit": integer("최대 결과 수")}), true, false)
	add("opportunity:read", "get_stale_opportunities", "장기 미활동 조회", "30일 이상 활동이 없는 영업기회를 조회합니다.", schema(nil, map[string]any{"limit": integer("최대 결과 수")}), true, false)
	add("contract:read", "get_contracts", "계약 조회", "접근 가능한 계약을 조회합니다.", schema(nil, map[string]any{"customerId": str("고객 ID"), "limit": integer("최대 결과 수")}), true, false)
	add("contract:read", "get_contract", "계약 상세", "계약 한 건의 금액, 기간, 갱신 상태와 버전을 조회합니다.", schema([]string{"id"}, map[string]any{"id": str("계약 ID")}), true, false)
	add("contract:read", "get_expiring_contracts", "만료 계약 조회", "지정 기간 안에 만료되는 계약을 조회합니다.", schema(nil, map[string]any{"days": integer("만료까지의 일수"), "limit": integer("최대 결과 수")}), true, false)
	add("contract:read", "get_renewal_pipeline", "갱신 영업 조회", "자동 갱신 또는 갱신 대상 계약을 조회합니다.", schema(nil, map[string]any{"days": integer("만료까지의 일수"), "limit": integer("최대 결과 수")}), true, false)
	add("customer:read contract:read contract:write", "create_contract", "계약 등록", "고객 계약을 DRAFT 또는 ACTIVE 상태로 등록합니다. 영업기회를 연결하면 opportunity:read도 필요합니다.", schema([]string{"customerId", "title", "amount"}, map[string]any{"contractNo": str("계약 번호 · 비우면 자동 생성"), "customerId": str("고객 ID"), "opportunityId": str("연결할 영업기회 ID"), "title": str("계약명"), "amount": number("계약 금액"), "currencyCode": str("ISO 통화 코드 · 기본 KRW"), "exchangeRate": number("KRW 환율"), "startDate": str("YYYY-MM-DD"), "endDate": str("YYYY-MM-DD"), "status": str("DRAFT, ACTIVE"), "autoRenew": boolean("자동 갱신"), "revenueScheduleType": str("ONE_TIME, MONTHLY, QUARTERLY, ANNUAL"), "renewalNoticeDays": integer("갱신 사전 알림 일수"), "renewalAction": str("갱신 계획")}), false, false)
	add("contract:read contract:write", "activate_contract", "계약 활성화", "DRAFT 계약을 활성화하고 매출 인식 일정을 생성합니다.", schema([]string{"id", "version"}, map[string]any{"id": str("계약 ID"), "version": integer("현재 버전")}), false, false)
	add("contract:read contract:write", "update_contract_renewal", "계약 갱신 진행 수정", "계약의 갱신 상태와 다음 행동을 버전 검사와 함께 갱신합니다.", schema([]string{"id", "version"}, map[string]any{"id": str("계약 ID"), "renewalStatus": str("NOT_STARTED, PLANNED, IN_PROGRESS, RENEWED, CHURNED"), "renewalAction": str("갱신 다음 행동"), "version": integer("현재 버전")}), false, false)
	add("contract:read", "list_contract_revenue_schedules", "계약 매출 일정", "계약의 예정·인식 매출 일정을 조회합니다.", schema([]string{"id"}, map[string]any{"id": str("계약 ID")}), true, false)
	add("forecast:read", "get_win_loss_analysis", "성공·실패 분석", "기간별 Win/Loss 건수, 금액, 승률을 조회합니다.", schema(nil, map[string]any{"months": integer("분석 개월 수")}), true, false)
	add("opportunity:read forecast:write", "set_forecast_override", "Forecast 조정", "영업기회 담당자의 팀장이 Forecast 범주, 확률 또는 금액을 근거와 함께 조정합니다.", schema([]string{"id", "reason", "version"}, map[string]any{"id": str("영업기회 ID"), "forecastCategory": str("COMMIT, BEST_CASE, PIPELINE, CLOSED"), "probability": number("조정 확률 0~100"), "amount": number("조정 금액"), "reason": str("조정 근거"), "version": integer("기존 조정이 없으면 0")}), false, false)
	add("report:read opportunity:read forecast:read", "get_sales_report", "영업 종합 보고서", "Dashboard, Forecast, Win/Loss를 한 번에 조회합니다.", schema(nil, map[string]any{"months": integer("Win/Loss 분석 개월 수")}), true, false)
	add("sales:read", "list_sales", "매출 조회", "인식된 매출 내역을 최신순으로 조회합니다.", schema(nil, map[string]any{"limit": integer("최대 결과 수")}), true, false)
	add("customer:read sales:write", "create_sale", "매출 등록", "고객의 인식 매출을 날짜와 통화 기준으로 등록합니다.", schema([]string{"customerId", "amount", "recognizedDate"}, map[string]any{"customerId": str("고객 ID"), "contractId": str("계약 ID"), "amount": number("매출 금액"), "currencyCode": str("ISO 통화 코드 · 기본 KRW"), "exchangeRate": number("KRW 환율"), "recognizedDate": str("YYYY-MM-DD"), "description": str("설명")}), false, false)
	add("target:read", "list_targets", "영업목표 조회", "사용자의 데이터 범위에 해당하는 기간별 영업목표를 조회합니다.", schema(nil, map[string]any{}), true, false)
	add("target:write", "create_target", "영업목표 등록", "사용자 또는 조직의 기간별 영업목표를 등록합니다.", schema([]string{"periodStart", "periodEnd", "amount"}, map[string]any{"userId": str("대상 사용자 ID · 비우면 본인"), "organizationId": str("대상 조직 ID"), "periodStart": str("YYYY-MM-DD"), "periodEnd": str("YYYY-MM-DD"), "amount": number("목표 금액")}), false, false)
	add("notification:read", "list_notifications", "알림 조회", "본인의 알림을 읽지 않은 항목만 또는 전체로 조회합니다.", schema(nil, map[string]any{"unreadOnly": boolean("읽지 않은 알림만 조회"), "limit": integer("최대 결과 수")}), true, false)
	add("notification:write", "mark_notification_read", "알림 읽음 처리", "본인의 알림 한 건을 읽음 처리합니다.", schema([]string{"id"}, map[string]any{"id": str("알림 ID")}), false, false)
	voiceProps := map[string]any{"customerId": str("고객 ID"), "status": str("RECEIVED, IN_REVIEW, IN_PROGRESS, PENDING_CUSTOMER, RESOLVED, CLOSED, REJECTED"), "voiceType": str("COMPLAINT, REQUEST, INQUIRY, DEFECT, PRAISE, CHURN_RISK"), "severity": str("LOW, NORMAL, HIGH, CRITICAL"), "open": str("true면 미해결 건만"), "overdue": str("true면 응답·해결 기한 초과 건만"), "limit": integer("최대 결과 수")}
	add("voice:read", "list_customer_voices", "고객 요청 조회", "불만, 요청, 문의와 이탈 징후를 조건으로 조회합니다. 기한 초과 여부가 함께 계산됩니다.", schema(nil, voiceProps), true, false)
	add("voice:read", "get_customer_voice", "고객 요청 상세", "고객 요청 한 건과 전체 처리 이력을 조회합니다.", schema([]string{"id"}, map[string]any{"id": str("고객 요청 ID")}), true, false)
	add("voice:read", "get_voice_summary", "고객 요청 요약", "미해결, 기한 초과, 긴급, 이탈 징후 건수와 평균 해결 시간, 만족도를 조회합니다.", schema(nil, map[string]any{"customerId": str("고객 ID · 비우면 전체")}), true, false)
	add("voice:read", "get_overdue_voices", "기한 초과 요청", "응답 또는 해결 기한을 넘긴 미해결 요청만 조회합니다.", schema(nil, map[string]any{"limit": integer("최대 결과 수")}), true, false)
	add("voice:read", "get_customer_churn_risk", "고객 이탈 위험도", "이탈 징후, 미해결 불만, 미착수 갱신, 접점 공백을 합산한 위험도와 근거를 조회합니다.", schema([]string{"id"}, map[string]any{"id": str("고객 ID")}), true, false)
	add("voice:read", "get_top_churn_risks", "이탈 위험 고객 순위", "담당 범위에서 이탈 위험이 높은 고객을 근거와 함께 조회합니다.", schema(nil, map[string]any{"limit": integer("최대 결과 수")}), true, false)
	add("customer:read voice:read voice:write", "file_customer_voice", "고객 요청 접수", "고객이 제기한 불만, 요청, 문의를 접수합니다. 유형과 심각도에 따라 응답·해결 기한이 자동 설정됩니다.", schema([]string{"customerId", "voiceType", "title"}, map[string]any{"customerId": str("고객 ID"), "contactId": str("요청 담당자 ID"), "categoryId": str("세부 분류 ID"), "voiceType": str("COMPLAINT, REQUEST, INQUIRY, DEFECT, PRAISE, CHURN_RISK"), "channel": str("PHONE, EMAIL, VISIT, PORTAL, CHAT, PARTNER, OTHER"), "title": str("제목"), "body": str("고객이 말한 내용"), "severity": str("LOW, NORMAL, HIGH, CRITICAL")}), false, false)
	add("voice:read voice:write", "record_voice_response", "고객 응대 기록", "고객에게 안내한 내용이나 내부 확인 사항을 처리 이력에 남깁니다. 고객 응대로 기록하면 응답 기한이 충족됩니다.", schema([]string{"id", "note"}, map[string]any{"id": str("고객 요청 ID"), "note": str("기록할 내용"), "eventType": str("CUSTOMER_CONTACT, COMMENT, ESCALATED")}), false, false)
	add("voice:read voice:write", "progress_customer_voice", "고객 요청 상태 변경", "요청 상태를 진행, 해결 등으로 변경합니다. 해결로 변경할 때는 해결 내용이 반드시 필요합니다.", schema([]string{"id", "status", "version"}, map[string]any{"id": str("고객 요청 ID"), "status": str("IN_REVIEW, IN_PROGRESS, PENDING_CUSTOMER, RESOLVED, CLOSED, REJECTED"), "version": integer("현재 버전"), "resolution": str("해결 내용 · RESOLVED로 변경할 때 필수"), "rootCause": str("근본 원인"), "preventiveAction": str("재발 방지 조치"), "note": str("변경 사유")}), false, false)
	add("voice:read", "get_voice_categories", "요청 유형 조회", "접수 가능한 요청 유형과 응답·해결 목표 시간을 조회합니다.", schema(nil, map[string]any{}), true, false)
	add("intelligence:read", "get_customer_signals", "고객 Signal 조회", "고객에게서 감지된 변화(접촉 공백, 정체, 만료 임박, 긍정 신호)를 조회합니다.", schema(nil, map[string]any{"customerId": str("고객 ID · 비우면 담당 범위 전체"), "severity": str("LOW, MEDIUM, HIGH, CRITICAL"), "sentiment": str("POSITIVE, NEGATIVE, NEUTRAL"), "signalType": str("NO_CONTACT, DEAL_STALLED, CRITICAL_VOC, CONTRACT_EXPIRING, DECISION_MAKER_MISSING, ENGAGEMENT_INCREASE, QUOTE_REQUESTED, CLOSE_DATE_PASSED"), "limit": integer("최대 결과 수")}), true, false)
	add("intelligence:read", "get_customer_risks", "고객 Risk 조회", "0~100 점수로 정량화된 관계, 갱신, VOC, Deal 위험을 조회합니다.", schema(nil, map[string]any{"customerId": str("고객 ID · 비우면 담당 범위 전체"), "riskType": str("RELATIONSHIP_RISK, RENEWAL_RISK, VOC_RISK, DEAL_RISK"), "minScore": integer("최소 위험 점수"), "limit": integer("최대 결과 수")}), true, false)
	add("intelligence:read", "get_deal_insights", "Deal Insight 조회", "여러 신호를 묶어 사람이 읽을 수 있게 요약한 분석을 조회합니다.", schema(nil, map[string]any{"customerId": str("고객 ID"), "opportunityId": str("영업기회 ID"), "limit": integer("최대 결과 수")}), true, false)
	add("intelligence:read", "get_recommendations", "추천 행동 조회", "위험과 신호에서 도출된 다음 행동 추천을 조회합니다.", schema(nil, map[string]any{"customerId": str("고객 ID"), "mine": str("true면 본인에게 배정된 추천만"), "priority": str("LOW, MEDIUM, HIGH"), "status": str("OPEN, ACCEPTED, DISMISSED, COMPLETED, ALL"), "limit": integer("최대 결과 수")}), true, false)
	add("intelligence:read", "explain_risk", "Risk 근거 설명", "위험 점수를 구성한 요인과 점수 배분, 관련 Signal을 설명합니다.", schema([]string{"id"}, map[string]any{"id": str("Risk ID")}), true, false)
	add("intelligence:read intelligence:write", "accept_recommendation", "추천 수락", "추천을 수락해 담당자와 기한이 있는 Task로 전환합니다.", schema([]string{"id"}, map[string]any{"id": str("추천 ID"), "assigneeId": str("담당자 ID · 비우면 고객 담당자"), "dueDate": str("YYYY-MM-DD")}), false, false)
	add("intelligence:read intelligence:write", "dismiss_recommendation", "추천 무시", "추천이 적절하지 않은 이유를 남기고 무시 처리합니다.", schema([]string{"id", "reason"}, map[string]any{"id": str("추천 ID"), "reason": str("무시 사유")}), false, false)
	add("customer:read contact:read opportunity:read activity:read", "get_account_brief", "고객 종합 브리핑", "Customer 360, 관계망, 전략 Account Plan을 미팅 준비용 브리핑으로 제공합니다.", schema([]string{"id"}, map[string]any{"id": str("고객 ID"), "year": integer("Account Plan 연도")}), true, false)
	add("customer:read contact:read", "get_account_relationships", "고객 관계망", "고객 담당자의 의사결정 역할, 영향력과 연결 관계를 조회합니다.", schema([]string{"id"}, map[string]any{"id": str("고객 ID")}), true, false)
	add("customer:read", "get_account_plan", "Account Plan", "전략 고객의 목표, 전략, 경쟁사, 위험과 White Space를 조회합니다.", schema([]string{"id"}, map[string]any{"id": str("고객 ID"), "year": integer("계획 연도")}), true, false)
	add("customer:read", "find_cross_sell_opportunities", "Cross-sell 기회", "Account Plan의 미제안 또는 탐색 중 White Space를 찾습니다.", schema([]string{"id"}, map[string]any{"id": str("고객 ID"), "year": integer("계획 연도")}), true, false)
	add("customer:read customer:write", "build_account_plan", "Account Plan 저장", "전략 고객 목표, 영업 전략과 White Space 계획을 생성하거나 갱신합니다.", schema([]string{"id", "planYear", "status", "version"}, map[string]any{"id": str("고객 ID"), "planYear": integer("계획 연도"), "status": str("DRAFT, ACTIVE, ARCHIVED"), "strategy": str("영업 전략"), "customerGoals": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "strategicInitiatives": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "ourObjectives": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "whiteSpaces": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}, "competitors": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "risks": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "targetRevenue": number("목표 매출"), "potentialRevenue": number("잠재 매출"), "version": integer("낙관적 잠금 버전")}), false, false)
	add("opportunity:read", "get_opportunity_team", "Opportunity Team", "Owner 이외의 Presales, Manager, Legal 등 협업 구성원을 조회합니다.", schema([]string{"id"}, map[string]any{"id": str("Opportunity ID")}), true, false)
	add("opportunity:read opportunity:write", "add_opportunity_member", "Opportunity Team 구성", "관리자 정책에 허용된 역할로 협업 구성원을 추가하거나 갱신합니다.", schema([]string{"id", "userId", "role", "version"}, map[string]any{"id": str("Opportunity ID"), "userId": str("사용자 ID"), "role": str("협업 역할"), "responsibility": str("담당 책임"), "version": integer("낙관적 잠금 버전")}), false, false)
	add("opportunity:read", "find_deals_at_risk", "위험 Deal 탐지", "설명 가능한 규칙으로 위험 점수 이상의 영업건을 찾습니다.", schema(nil, map[string]any{"minimum": integer("최소 위험 점수"), "limit": integer("최대 결과 수")}), true, false)
	add("opportunity:read", "explain_deal_risk", "Deal 위험 설명", "위험 점수, 근거, 권장 행동과 최근 변화를 설명합니다.", schema([]string{"id"}, map[string]any{"id": str("Opportunity ID"), "days": integer("변화 분석 기간")}), true, false)
	add("opportunity:read", "recommend_next_actions", "다음 행동 추천", "Deal Health와 Stage Playbook을 결합해 다음 행동을 추천합니다.", schema([]string{"id"}, map[string]any{"id": str("Opportunity ID")}), true, false)
	add("opportunity:read", "get_stage_readiness", "Stage 전환 준비도", "현재 Stage의 Exit Criteria 충족 여부를 확인합니다.", schema([]string{"id", "stageId"}, map[string]any{"id": str("Opportunity ID"), "stageId": str("이동 대상 Stage ID")}), true, false)
	add("forecast:read", "explain_forecast_change", "Forecast 변화 설명", "Snapshot을 비교해 신규, Lost, 금액 증감과 Slippage를 설명합니다.", schema(nil, map[string]any{"days": integer("비교 기간")}), true, false)
	add("opportunity:read", "get_sales_coaching_insights", "영업 Coaching", "담당자별 위험 Deal과 실행 공백을 팀장 Coaching 관점으로 제공합니다.", schema(nil, map[string]any{}), true, false)
	add("opportunity:read", "get_manager_review_queue", "팀장 검토 Queue", "위험도가 높은 영업건을 우선순위 순으로 제공합니다.", schema(nil, map[string]any{"minimum": integer("최소 위험 점수"), "limit": integer("최대 결과 수")}), true, false)
	add("quotation:read", "list_quotations", "견적 조회", "고객별 또는 전체 견적을 최신순으로 조회합니다.", schema(nil, map[string]any{"customerId": str("고객 ID"), "limit": integer("최대 결과 수")}), true, false)
	add("customer:read quotation:write", "create_quotation", "견적 생성", "고객 또는 영업기회에 견적 초안을 생성합니다.", schema([]string{"customerId", "title", "amount"}, map[string]any{"customerId": str("고객 ID"), "opportunityId": str("영업기회 ID"), "title": str("견적 제목"), "amount": number("견적 금액"), "currencyCode": str("ISO 통화 코드 · 기본 KRW"), "exchangeRate": number("KRW 환율"), "discountPercent": number("할인율"), "validUntil": str("YYYY-MM-DD")}), false, false)
	if s.approvalsEnabled(ctx) {
		add("approval:request", "submit_approval", "승인 요청", "적용되는 정책에 따라 팀장 승인을 요청합니다.", schema([]string{"entityType", "entityId"}, map[string]any{"entityType": str("OPPORTUNITY, QUOTATION, CONTRACT, CUSTOMER"), "entityId": str("대상 ID"), "reason": str("요청 사유")}), false, false)
		add("approval:request", "get_approval_status", "승인 상태", "승인 요청 상태를 조회합니다.", schema([]string{"id"}, map[string]any{"id": str("승인 요청 ID")}), true, false)
		add("approval:approve", "approve_request", "승인", "고위험 권한으로 승인 요청을 승인합니다.", schema([]string{"id", "version"}, map[string]any{"id": str("승인 요청 ID"), "version": integer("현재 버전"), "comment": str("검토 의견")}), false, true)
		add("approval:approve", "reject_request", "반려", "고위험 권한으로 승인 요청을 반려합니다.", schema([]string{"id", "version", "comment"}, map[string]any{"id": str("승인 요청 ID"), "version": integer("현재 버전"), "comment": str("반려 사유")}), false, true)
	}
	return out
}

// ToolCatalog returns the exact tool set available under the current user's
// permissions and the administrator allowlist. A key's own scopes are applied
// by the UI (and again by authentication when tools/list is called).
func (s *Server) ToolCatalog(ctx context.Context, p *auth.Principal) []ToolCatalogItem {
	items := s.tools(ctx, p)
	out := make([]ToolCatalogItem, 0, len(items))
	for _, item := range items {
		readOnly, _ := item.Annotations["readOnlyHint"].(bool)
		out = append(out, ToolCatalogItem{Name: item.Name, Title: item.Title, RequiredScopes: item.RequiredScopes, ReadOnly: readOnly})
	}
	return out
}

func decodeArgs(args map[string]any, target any) error {
	b, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}
func intArg(args map[string]any, key string, fallback int) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	return fallback
}
func strArg(args map[string]any, key string) string { v, _ := args[key].(string); return v }
func boolArg(args map[string]any, key string, fallback bool) bool {
	v, ok := args[key].(bool)
	if !ok {
		return fallback
	}
	return v
}
func toolResult(v any) map[string]any {
	b, _ := json.Marshal(v)
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "structuredContent": v, "isError": false}
}

// errUnknownTool separates "there is no such tool" — a protocol fault the client
// must fix — from a tool that ran and failed, which the model should see.
var errUnknownTool = errors.New("unknown or disallowed tool")

// toolFailure reports a tool that ran and could not complete. The specification
// asks for this to be a successful result carrying isError, not a JSON-RPC
// error: a permission denial or a validation message is information the model
// can act on, while a transport error usually just ends the conversation.
func toolFailure(err error) map[string]any {
	message := err.Error()
	// An agent reading "no rows in result set" cannot tell a missing record from
	// a broken server. Say which it is.
	if errors.Is(err, pgx.ErrNoRows) || message == "no rows in result set" {
		message = "요청한 데이터를 찾을 수 없거나 접근 권한이 없습니다."
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": message}},
		"isError": true,
	}
}
func (s *Server) callTool(ctx context.Context, p *auth.Principal, call toolCall, r *http.Request) (any, error) {
	permitted := false
	for _, available := range s.tools(ctx, p) {
		if available.Name == call.Name {
			permitted = true
			break
		}
	}
	if !permitted {
		return nil, fmt.Errorf("%w: %s", errUnknownTool, call.Name)
	}
	a := call.Arguments
	meta := crm.RequestMeta{Channel: "MCP", IP: httpx.ClientIP(r), RequestID: httpx.RequestID(ctx), UserAgent: r.UserAgent()}
	var v any
	var err error
	switch call.Name {
	case "search_customers":
		v, err = s.CRM.ListCustomers(ctx, p, strArg(a, "query"), "", "", intArg(a, "limit", 50))
	case "get_customer":
		v, err = s.CRM.GetCustomer(ctx, p, strArg(a, "id"))
	case "get_customer_360":
		v, err = s.CRM.Customer360(ctx, p, strArg(a, "id"))
	case "create_customer":
		var in crm.CustomerInput
		err = decodeArgs(a, &in)
		if err == nil {
			v, err = s.CRM.CreateCustomer(ctx, p, in, meta)
		}
	case "update_customer":
		var before crm.Customer
		before, err = s.CRM.GetCustomer(ctx, p, strArg(a, "id"))
		if err == nil {
			in := crm.CustomerInput{Name: before.Name, RegistrationNo: before.RegistrationNo, CustomerType: before.CustomerType, Grade: before.Grade, Industry: before.Industry, Website: before.Website, Phone: before.Phone, Email: before.Email, Address: before.Address, OwnerID: before.OwnerID, Health: before.Health, AnnualRevenue: before.AnnualRevenue, EmployeeCount: before.EmployeeCount, CustomFields: before.CustomFields, Version: before.Version}
			err = decodeArgs(a, &in)
			if err == nil {
				v, err = s.CRM.UpdateCustomer(ctx, p, before.ID, in, meta)
			}
		}
	case "delete_customer":
		v, err = s.CRM.DeleteCustomer(ctx, p, strArg(a, "id"), meta)
	case "search_contacts":
		v, err = s.CRM.SearchContacts(ctx, p, strArg(a, "query"), strArg(a, "customerId"), intArg(a, "limit", 50))
	case "get_contact":
		v, err = s.CRM.GetContact(ctx, p, strArg(a, "id"))
	case "create_contact":
		var in crm.ContactInput
		err = decodeArgs(a, &in)
		if err == nil {
			v, err = s.CRM.CreateContact(ctx, p, in, meta)
		}
	case "update_contact":
		var before crm.Contact
		before, err = s.CRM.GetContact(ctx, p, strArg(a, "id"))
		if err == nil {
			strength, power := before.RelationshipStrength, before.DecisionPower
			in := crm.ContactInput{CustomerID: before.CustomerID, Name: before.Name, Title: before.Title, Department: before.Department, Email: before.Email, Phone: before.Phone, Mobile: before.Mobile, DecisionMaker: before.DecisionMaker, PrimaryContact: before.PrimaryContact, RelationshipRole: before.RelationshipRole, Influence: before.Influence, Sentiment: before.Sentiment, RelationshipStrength: &strength, DecisionPower: &power}
			err = decodeArgs(a, &in)
			if err == nil {
				v, err = s.CRM.UpdateContact(ctx, p, before.ID, in, meta)
			}
		}
	case "delete_contact":
		v, err = s.CRM.DeleteContact(ctx, p, strArg(a, "id"), meta)
	case "list_leads":
		v, err = s.CRM.ListLeads(ctx, p, strArg(a, "query"), intArg(a, "limit", 50))
	case "create_lead":
		var in crm.LeadInput
		err = decodeArgs(a, &in)
		if err == nil {
			v, err = s.CRM.CreateLead(ctx, p, in, meta)
		}
	case "list_products":
		v, err = s.CRM.ListProducts(ctx, p, strArg(a, "query"), intArg(a, "limit", 100))
	case "create_product":
		var in crm.ProductInput
		err = decodeArgs(a, &in)
		if err == nil {
			v, err = s.CRM.CreateProduct(ctx, p, in, meta)
		}
	case "list_opportunities":
		v, err = s.CRM.ListOpportunities(ctx, p, crm.OpportunityFilter{Query: strArg(a, "query"), CustomerID: strArg(a, "customerId"), Status: strArg(a, "status"), Limit: intArg(a, "limit", 50)})
	case "get_opportunity":
		v, err = s.CRM.GetOpportunity(ctx, p, strArg(a, "id"))
	case "create_opportunity":
		var in crm.OpportunityInput
		err = decodeArgs(a, &in)
		if err == nil {
			v, err = s.CRM.CreateOpportunity(ctx, p, in, meta)
		}
	case "update_opportunity":
		var in crm.OpportunityInput
		err = decodeArgs(a, &in)
		if err == nil {
			v, err = s.CRM.UpdateOpportunity(ctx, p, strArg(a, "id"), in, meta)
		}
	case "change_opportunity_stage":
		v, err = s.CRM.ChangeOpportunityStage(ctx, p, strArg(a, "id"), strArg(a, "stageId"), intArg(a, "version", 0), meta)
	case "add_activity":
		var in crm.ActivityInput
		err = decodeArgs(a, &in)
		if err == nil {
			v, err = s.CRM.AddActivity(ctx, p, in, meta)
		}
	case "list_activities":
		v, err = s.CRM.ListActivities(ctx, p, strArg(a, "customerId"), strArg(a, "opportunityId"), intArg(a, "limit", 50))
	case "get_pipeline":
		v, err = s.CRM.Pipelines(ctx, p)
	case "get_forecast":
		v, err = s.CRM.Forecast(ctx, p)
	case "get_sales_kpi":
		v, err = s.CRM.SalesKPI(ctx, p)
	case "get_due_actions":
		v, err = s.CRM.DueActions(ctx, p, intArg(a, "days", 7), intArg(a, "limit", 50))
	case "get_stale_opportunities":
		v, err = s.CRM.ListOpportunities(ctx, p, crm.OpportunityFilter{StaleOnly: true, Limit: intArg(a, "limit", 50)})
	case "get_contracts":
		v, err = s.CRM.Contracts(ctx, p, strArg(a, "customerId"), 0, false, intArg(a, "limit", 50))
	case "get_contract":
		v, err = s.CRM.GetContract(ctx, p, strArg(a, "id"))
	case "get_expiring_contracts":
		v, err = s.CRM.Contracts(ctx, p, "", intArg(a, "days", 90), false, intArg(a, "limit", 50))
	case "get_renewal_pipeline":
		v, err = s.CRM.Contracts(ctx, p, "", intArg(a, "days", 180), true, intArg(a, "limit", 50))
	case "create_contract":
		var in crm.ContractInput
		err = decodeArgs(a, &in)
		if err == nil {
			v, err = s.CRM.CreateContract(ctx, p, in, meta)
		}
	case "activate_contract":
		v, err = s.CRM.ActivateContract(ctx, p, strArg(a, "id"), intArg(a, "version", 0), meta)
	case "update_contract_renewal":
		v, err = s.CRM.UpdateContractRenewal(ctx, p, strArg(a, "id"), strArg(a, "renewalStatus"), strArg(a, "renewalAction"), intArg(a, "version", 0), meta)
	case "list_contract_revenue_schedules":
		v, err = s.CRM.ListRevenueSchedules(ctx, p, strArg(a, "id"))
	case "get_win_loss_analysis":
		v, err = s.CRM.WinLossAnalysis(ctx, p, intArg(a, "months", 12))
	case "set_forecast_override":
		var in intelligence.ForecastOverrideInput
		err = decodeArgs(a, &in)
		if err == nil {
			v, err = s.Intelligence.SaveForecastOverride(ctx, p, strArg(a, "id"), in, meta)
		}
	case "get_sales_report":
		var dashboard, forecast, winLoss any
		dashboard, err = s.CRM.Dashboard(ctx, p)
		if err == nil {
			forecast, err = s.CRM.Forecast(ctx, p)
		}
		if err == nil {
			winLoss, err = s.CRM.WinLossAnalysis(ctx, p, intArg(a, "months", 12))
		}
		if err == nil {
			v = map[string]any{"dashboard": dashboard, "forecast": forecast, "winLoss": winLoss}
		}
	case "list_sales":
		v, err = s.CRM.ListSales(ctx, p, intArg(a, "limit", 100))
	case "create_sale":
		var in crm.SaleInput
		err = decodeArgs(a, &in)
		if err == nil {
			v, err = s.CRM.CreateSale(ctx, p, in, meta)
		}
	case "list_targets":
		v, err = s.CRM.ListTargets(ctx, p)
	case "create_target":
		var in crm.TargetInput
		err = decodeArgs(a, &in)
		if err == nil {
			v, err = s.CRM.CreateTarget(ctx, p, in, meta)
		}
	case "list_notifications":
		v, err = s.CRM.Notifications(ctx, p, boolArg(a, "unreadOnly", false), intArg(a, "limit", 100))
	case "mark_notification_read":
		err = s.CRM.ReadNotification(ctx, p, strArg(a, "id"), meta)
		if err == nil {
			v = map[string]any{"id": strArg(a, "id"), "read": true}
		}
	case "list_customer_voices":
		v, err = s.Voices.List(ctx, p, voice.Query{CustomerID: strArg(a, "customerId"), Status: strArg(a, "status"),
			VoiceType: strArg(a, "voiceType"), Severity: strArg(a, "severity"),
			OpenOnly: strArg(a, "open") == "true", Overdue: strArg(a, "overdue") == "true", Limit: intArg(a, "limit", 50)})
	case "get_customer_voice":
		var record voice.Voice
		var events []voice.Event
		record, events, err = s.Voices.Get(ctx, p, strArg(a, "id"))
		if err == nil {
			v = map[string]any{"voice": record, "events": events}
		}
	case "get_voice_summary":
		v, err = s.Voices.Summary(ctx, p, strArg(a, "customerId"))
	case "get_overdue_voices":
		v, err = s.Voices.List(ctx, p, voice.Query{Overdue: true, OpenOnly: true, Limit: intArg(a, "limit", 25)})
	case "get_customer_signals":
		v, err = s.Intelligence.ListSignals(ctx, p, intelligence.SignalFilter{AccountID: strArg(a, "customerId"),
			Severity: strArg(a, "severity"), Sentiment: strArg(a, "sentiment"), SignalType: strArg(a, "signalType"),
			Limit: intArg(a, "limit", 25)})
	case "get_customer_risks":
		v, err = s.Intelligence.ListRisks(ctx, p, intelligence.RiskFilter{AccountID: strArg(a, "customerId"),
			RiskType: strArg(a, "riskType"), MinScore: intArg(a, "minScore", 0), Limit: intArg(a, "limit", 25)})
	case "get_deal_insights":
		v, err = s.Intelligence.ListInsights(ctx, p, intelligence.InsightFilter{AccountID: strArg(a, "customerId"),
			OpportunityID: strArg(a, "opportunityId"), Limit: intArg(a, "limit", 25)})
	case "get_recommendations":
		v, err = s.Intelligence.ListRecommendations(ctx, p, intelligence.RecommendationFilter{AccountID: strArg(a, "customerId"),
			Mine: strArg(a, "mine") == "true", Priority: strArg(a, "priority"), Status: strArg(a, "status"),
			Limit: intArg(a, "limit", 25)})
	case "explain_risk":
		v, err = s.Intelligence.ExplainRisk(ctx, p, strArg(a, "id"))
	case "accept_recommendation":
		var due *time.Time
		if date := strings.TrimSpace(strArg(a, "dueDate")); date != "" {
			var parsed time.Time
			parsed, err = time.Parse("2006-01-02", date)
			if err == nil {
				due = &parsed
			}
		}
		if err == nil {
			v, err = s.Intelligence.AcceptRecommendation(ctx, p, strArg(a, "id"), strArg(a, "assigneeId"), due, meta)
		}
	case "dismiss_recommendation":
		v, err = s.Intelligence.DismissRecommendation(ctx, p, strArg(a, "id"), strArg(a, "reason"), meta)
	case "get_customer_churn_risk":
		v, err = s.Voices.Risk(ctx, p, strArg(a, "id"))
	case "get_top_churn_risks":
		v, err = s.Voices.TopRisks(ctx, p, intArg(a, "limit", 5))
	case "get_voice_categories":
		v, err = s.Voices.Categories(ctx, p, false)
	case "file_customer_voice":
		var in voice.Input
		err = decodeArgs(a, &in)
		if err == nil {
			v, err = s.Voices.Create(ctx, p, in, meta)
		}
	case "record_voice_response":
		eventType := strArg(a, "eventType")
		if eventType == "" {
			eventType = "CUSTOMER_CONTACT"
		}
		v, err = s.Voices.Comment(ctx, p, strArg(a, "id"), eventType, strArg(a, "note"), meta)
	case "progress_customer_voice":
		var in voice.UpdateInput
		err = decodeArgs(a, &in)
		if err == nil {
			v, err = s.Voices.Update(ctx, p, strArg(a, "id"), in, meta)
		}
	case "get_account_brief":
		v, err = s.Relationships.AccountBrief(ctx, p, strArg(a, "id"), intArg(a, "year", 0))
	case "get_account_relationships":
		v, err = s.Relationships.Graph(ctx, p, strArg(a, "id"))
	case "get_account_plan":
		v, err = s.Relationships.GetAccountPlan(ctx, p, strArg(a, "id"), intArg(a, "year", 0))
	case "find_cross_sell_opportunities":
		v, err = s.Relationships.CrossSellOpportunities(ctx, p, strArg(a, "id"), intArg(a, "year", 0))
	case "build_account_plan":
		var in relationship.AccountPlanInput
		err = decodeArgs(a, &in)
		if err == nil {
			v, err = s.Relationships.SaveAccountPlan(ctx, p, strArg(a, "id"), in, meta)
		}
	case "get_opportunity_team":
		v, err = s.Relationships.OpportunityTeam(ctx, p, strArg(a, "id"))
	case "add_opportunity_member":
		var in relationship.OpportunityMemberInput
		err = decodeArgs(a, &in)
		if err == nil {
			v, err = s.Relationships.SaveOpportunityMember(ctx, p, strArg(a, "id"), strArg(a, "userId"), in, meta)
		}
	case "find_deals_at_risk", "get_manager_review_queue":
		v, err = s.Intelligence.DealsAtRisk(ctx, p, intArg(a, "minimum", 40), intArg(a, "limit", 25))
	case "explain_deal_risk":
		v, err = s.Intelligence.DealInspection(ctx, p, strArg(a, "id"), intArg(a, "days", 7))
	case "recommend_next_actions":
		health, healthErr := s.Intelligence.DealHealth(ctx, p, strArg(a, "id"))
		if healthErr != nil {
			err = healthErr
			break
		}
		playbook, playbookErr := s.Intelligence.Playbook(ctx, p, strArg(a, "id"))
		if playbookErr != nil {
			err = playbookErr
			break
		}
		v = map[string]any{"health": health, "playbook": playbook, "recommendations": health.Recommendations}
	case "get_stage_readiness":
		v, err = s.Intelligence.ValidateStageTransition(ctx, p, strArg(a, "id"), strArg(a, "stageId"))
	case "explain_forecast_change":
		v, err = s.Intelligence.ForecastIntelligence(ctx, p, intArg(a, "days", 7))
	case "get_sales_coaching_insights":
		v, err = s.Intelligence.Coaching(ctx, p)
	case "create_quotation":
		var in crm.QuotationInput
		err = decodeArgs(a, &in)
		if err == nil {
			v, err = s.CRM.CreateQuotation(ctx, p, in, meta)
		}
	case "list_quotations":
		v, err = s.CRM.ListQuotations(ctx, p, strArg(a, "customerId"), intArg(a, "limit", 50))
	case "submit_approval":
		v, err = s.Approvals.Submit(ctx, p, strArg(a, "entityType"), strArg(a, "entityId"), strArg(a, "reason"), meta.IP, meta.RequestID, meta.UserAgent)
	case "get_approval_status":
		v, err = s.Approvals.Get(ctx, p, strArg(a, "id"))
	case "approve_request":
		v, err = s.Approvals.Decide(ctx, p, strArg(a, "id"), "APPROVE", strArg(a, "comment"), intArg(a, "version", 0), meta.IP, meta.RequestID, meta.UserAgent)
	case "reject_request":
		v, err = s.Approvals.Decide(ctx, p, strArg(a, "id"), "REJECT", strArg(a, "comment"), intArg(a, "version", 0), meta.IP, meta.RequestID, meta.UserAgent)
	default:
		return nil, errUnknownTool
	}
	if err != nil {
		return nil, err
	}
	return toolResult(v), nil
}

func (s *Server) resources(p *auth.Principal) []map[string]any {
	out := []map[string]any{}
	add := func(permission, uri, name, description string) {
		if p.Has(permission) {
			out = append(out, map[string]any{"uri": uri, "name": name, "description": description, "mimeType": "application/json"})
		}
	}
	add("opportunity:read", "relio://pipeline/me", "내 Pipeline", "내 데이터 범위의 Pipeline")
	add("forecast:read", "relio://forecast/me", "내 Forecast", "내 데이터 범위의 Forecast")
	add("activity:read", "relio://activities/today", "오늘 활동", "오늘의 영업활동")
	add("activity:read", "relio://actions/due", "기한 도래 작업", "7일 내 후속 작업")
	add("contract:read", "relio://contracts/expiring", "만료 계약", "90일 내 만료 계약")
	return out
}
func (s *Server) templates(p *auth.Principal) []map[string]any {
	out := []map[string]any{}
	add := func(permission, uri, name, description string) {
		if p.Has(permission) {
			out = append(out, map[string]any{"uriTemplate": uri, "name": name, "description": description, "mimeType": "application/json"})
		}
	}
	add("customer:read", "relio://customers/{id}", "고객", "고객 상세")
	add("customer:read", "relio://customers/{id}/360", "Customer 360", "고객 통합 정보")
	add("customer:read", "relio://customers/{id}/relationships", "고객 관계망", "고객 의사결정 관계와 영향력")
	add("customer:read", "relio://customers/{id}/account-plan", "Account Plan", "전략 고객 계획과 White Space")
	add("contact:read", "relio://contacts/{id}", "담당자", "고객 담당자")
	add("opportunity:read", "relio://opportunities/{id}", "영업기회", "영업기회 상세")
	add("contract:read", "relio://contracts/{id}", "계약", "계약 상세")
	return out
}
func resourceResult(uri string, v any) map[string]any {
	b, _ := json.Marshal(v)
	return map[string]any{"contents": []map[string]any{{"uri": uri, "mimeType": "application/json", "text": string(b)}}}
}
func (s *Server) readResource(ctx context.Context, p *auth.Principal, uri string) (any, error) {
	switch uri {
	case "relio://pipeline/me", "relio://pipeline/team":
		v, e := s.CRM.Pipelines(ctx, p)
		return resourceResult(uri, v), e
	case "relio://forecast/me", "relio://forecast/team":
		v, e := s.CRM.Forecast(ctx, p)
		return resourceResult(uri, v), e
	case "relio://activities/today":
		v, e := s.CRM.ListActivities(ctx, p, "", "", 100)
		return resourceResult(uri, v), e
	case "relio://actions/due":
		v, e := s.CRM.DueActions(ctx, p, 7, 100)
		return resourceResult(uri, v), e
	case "relio://contracts/expiring":
		v, e := s.CRM.Contracts(ctx, p, "", 90, false, 100)
		return resourceResult(uri, v), e
	}
	parts := strings.Split(strings.TrimPrefix(uri, "relio://"), "/")
	if len(parts) >= 2 {
		switch parts[0] {
		case "customers":
			if len(parts) == 3 && parts[2] == "360" {
				v, e := s.CRM.Customer360(ctx, p, parts[1])
				return resourceResult(uri, v), e
			}
			if len(parts) == 3 && parts[2] == "relationships" {
				v, e := s.Relationships.Graph(ctx, p, parts[1])
				return resourceResult(uri, v), e
			}
			if len(parts) == 3 && parts[2] == "account-plan" {
				v, e := s.Relationships.GetAccountPlan(ctx, p, parts[1], 0)
				return resourceResult(uri, v), e
			}
			v, e := s.CRM.GetCustomer(ctx, p, parts[1])
			return resourceResult(uri, v), e
		case "opportunities":
			v, e := s.CRM.GetOpportunity(ctx, p, parts[1])
			return resourceResult(uri, v), e
		case "contracts":
			items, e := s.CRM.Contracts(ctx, p, "", 0, false, 200)
			if e != nil {
				return nil, e
			}
			for _, v := range items {
				if fmt.Sprint(v["id"]) == parts[1] {
					return resourceResult(uri, v), nil
				}
			}
			return nil, errors.New("contract not found")
		case "contacts":
			items, e := s.CRM.SearchContacts(ctx, p, "", "", 200)
			if e != nil {
				return nil, e
			}
			for _, v := range items {
				if v.ID == parts[1] {
					return resourceResult(uri, v), nil
				}
			}
			return nil, errors.New("contact not found")
		}
	}
	return nil, errors.New("resource not found")
}
