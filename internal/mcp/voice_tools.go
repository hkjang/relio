package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/voice"
)

// VOC tools for department agents. Every write here is something a handler
// would otherwise type into Relio a second time after discussing it with the
// agent; the descriptions ask the agent to show the handler what it is about
// to record and wait for approval, and the tools are marked as writes so MCP
// clients ask before running them. The knowledge gate is not among them:
// only a reviewer, on the Relio screen, can move a case into agent knowledge.

const confirmFirst = " 호출 전에 등록할 내용을 담당자에게 보여 주고 승인을 받은 뒤에만 실행하세요."

// voiceFields describes a workspace's fields as a JSON Schema object, with the
// options of every select field as an enum, so an agent is told the exact
// values instead of guessing them. Fields of workspaces the caller cannot use
// never appear.
func (s *Server) voiceFields(ctx context.Context, p *auth.Principal, phases ...string) (map[string]any, []voice.Workspace) {
	workspaces, err := s.Voices.Workspaces(ctx, p, false)
	properties := map[string]any{}
	if err == nil {
		for _, w := range workspaces {
			for _, f := range w.Fields {
				if len(phases) > 0 && !contains(phases, f.Phase) {
					continue
				}
				description := f.Label
				if f.Required {
					description += " · 필수"
				}
				if f.HelpText != "" {
					description += " · " + f.HelpText
				}
				if len(workspaces) > 1 {
					description += " · 업무 영역: " + w.Name
				}
				property := map[string]any{"type": "string", "description": description}
				switch f.Type {
				case "Select":
					property["enum"] = f.Options
				case "Multi Select":
					property = map[string]any{"type": "array", "description": description, "items": map[string]any{"type": "string", "enum": f.Options}}
				case "Number", "Money", "Percent":
					property["type"] = "number"
				case "Boolean":
					property["type"] = "boolean"
				}
				properties[f.Key] = property
			}
		}
	}
	return map[string]any{"type": "object", "description": "업무 영역의 추가 항목. 키와 허용값은 get_voice_categories에서도 확인할 수 있습니다.",
		"properties": properties, "additionalProperties": false}, workspaces
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// findCategory resolves a type the way an agent names it: by id, by code, or
// by its exact display name. A name used in two workspaces is refused with
// the candidates, never guessed.
func (s *Server) findCategory(ctx context.Context, p *auth.Principal, ref string) (voice.Category, error) {
	ref = strings.TrimSpace(ref)
	items, err := s.Voices.Categories(ctx, p, false)
	if err != nil {
		return voice.Category{}, err
	}
	matches := []voice.Category{}
	for _, c := range items {
		if c.ID == ref || strings.EqualFold(c.Code, ref) || c.Name == ref {
			matches = append(matches, c)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		names := []string{}
		for _, c := range items {
			names = append(names, c.Name)
		}
		sort.Strings(names)
		return voice.Category{}, fmt.Errorf("요청 유형 %q을(를) 찾을 수 없습니다. 사용할 수 있는 유형: %s", ref, strings.Join(names, ", "))
	}
	return voice.Category{}, fmt.Errorf("요청 유형 %q이(가) 여러 업무 영역에 있습니다. category에 유형 코드나 ID를 넘기세요", ref)
}

// customerRef resolves a customer from an id or a customer code.
func (s *Server) customerRef(ctx context.Context, p *auth.Principal, id, code string) (string, error) {
	if strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id), nil
	}
	if strings.TrimSpace(code) == "" {
		return "", errors.New("customerId 또는 customerCode 중 하나가 필요합니다")
	}
	c, err := s.CRM.CustomerByCode(ctx, p, code)
	if err != nil {
		return "", fmt.Errorf("고객 코드 %s로 등록된 고객이 없습니다. register_workspace_customer로 먼저 등록하세요", strings.TrimSpace(code))
	}
	return c.ID, nil
}

// addVoiceTools registers the department VOC tools. It is called from tools()
// so the same permission and allowlist rules apply.
func (s *Server) addVoiceTools(ctx context.Context, p *auth.Principal, add func(permission, name, title, description string, input map[string]any, readOnly, dangerous bool)) {
	if !p.Has("voice:read") {
		return
	}
	intakeFields, _ := s.voiceFields(ctx, p, "INTAKE")
	resolutionFields, _ := s.voiceFields(ctx, p, "RESOLUTION")
	filterFields, _ := s.voiceFields(ctx, p)
	evidence := map[string]any{"type": "string", "enum": voice.CauseEvidenceLevels,
		"description": "원인 근거: CONFIRMED=확인(테스트·로그 등 객관 근거), PRESUMED=추정(정황상 판단), UNIDENTIFIED=미특정(원인 미특정, 증상은 해소). 실제 근거 수준 그대로 기록합니다."}

	add("customer:read voice:read voice:write", "file_customer_voice", "고객 요청 접수",
		"고객(회원사) 요청을 접수합니다. category에 유형 이름·코드·ID를, fields에 업무 영역 접수 항목을 넣습니다. customerCode(회원사코드)로 고객을 지정할 수 있습니다. SLA를 쓰지 않는 유형은 기한을 계산하지 않습니다."+confirmFirst,
		schema([]string{"title"}, map[string]any{
			"customerId": str("고객 ID · customerCode와 둘 중 하나"), "customerCode": str("고객 코드(회원사코드)"),
			"category": str("요청 유형 이름, 코드 또는 ID · 업무 영역 유형이면 필수"), "voiceType": str("COMPLAINT, REQUEST, INQUIRY, DEFECT, PRAISE, CHURN_RISK · 업무 영역 유형이면 생략"),
			"contactId": str("요청 담당자 ID"), "channel": str("PHONE, EMAIL, VISIT, PORTAL, CHAT, PARTNER, OTHER"),
			"title": str("제목"), "body": str("고객이 말한 내용(원문 그대로)"), "severity": str("LOW, NORMAL, HIGH, CRITICAL"),
			"fields": intakeFields,
		}), false, false)
	add("voice:read voice:write", "resolve_customer_voice", "고객 요청 해결 처리",
		"해결 내용, 원인 근거, 해결 주체 등 해결 항목을 기록하고 해결 상태로 바꿉니다. 원인이 특정되지 않은 해결도 정상 종결이므로 근거 수준을 높여 쓰지 마세요. 지식 반영 여부는 검토자가 Relio 화면에서 판정하며 이 도구로 바꿀 수 없습니다."+confirmFirst,
		schema([]string{"id", "resolution"}, map[string]any{
			"id": str("고객 요청 ID"), "resolution": str("무엇을 어떻게 처리했는지"), "causeEvidence": evidence,
			"rootCause": str("근본 원인"), "preventiveAction": str("재발 방지 조치"), "fields": resolutionFields,
			"note": str("처리 이력에 남길 메모"),
		}), false, false)
	add("voice:read", "search_voice_knowledge", "유사 사례 검색",
		"오류코드나 증상으로 해결된 과거 사례를 찾습니다. 기본은 검토자가 '반영'으로 판정한 사례만 돌려주며 이것만 진단 근거로 쓰세요. includeUnreviewed를 켜면 검토 전 사례도 오지만, 각 결과의 knowledgeStatus와 causeEvidence를 보고 확정 근거로 쓰지 마세요.",
		schema([]string{"query"}, map[string]any{
			"query":  str("오류코드, 오류 문구 또는 증상. 공백으로 나눈 모든 단어가 들어 있는 사례만 찾습니다."),
			"fields": filterFields, "causeEvidence": evidence,
			"includeUnreviewed": boolean("true면 검토 전(미검토·검토중) 사례도 포함"),
			"workspaceId":       str("업무 영역 ID · 비우면 사용 가능한 전체"), "limit": integer("최대 결과 수(기본 10, 최대 50)"),
		}), true, false)
	add("customer:read voice:read", "get_customer_voice_history", "회원사별 이력 조회",
		"한 고객(회원사)의 종결된 요청 전체를 최신순으로 돌려줍니다. 지식 반영 여부와 무관한 과거 문의 맥락이며, 진단 근거는 search_voice_knowledge를 쓰세요.",
		schema(nil, map[string]any{
			"customerCode": str("고객 코드(회원사코드)"), "customerId": str("고객 ID · customerCode와 둘 중 하나"),
			"workspaceId": str("업무 영역 ID"), "limit": integer("최대 결과 수(기본 30, 최대 200)"),
		}), true, false)
	add("customer:read customer:write voice:read voice:write", "register_workspace_customer", "회원사 간이 등록",
		"고객사명과 고객 코드(회원사코드)만으로 업무 영역 고객을 등록합니다. 같은 코드가 이미 있으면 등록하지 않고 기존 고객을 알려 줍니다."+confirmFirst,
		schema([]string{"workspaceId", "name", "customerCode"}, map[string]any{
			"workspaceId": str("업무 영역 ID"), "name": str("고객사명"), "customerCode": str("고객 코드(회원사코드)"),
		}), false, false)
	add("customer:read customer:write voice:read voice:write", "import_workspace_customers", "회원사 일괄 등록",
		"회원사 목록(고객사명·고객 코드)을 한 번에 등록합니다. 이미 있는 코드는 건너뛰고, updateNames가 true면 이름만 갱신합니다. 행마다 결과를 돌려줍니다."+confirmFirst,
		schema([]string{"workspaceId", "items"}, map[string]any{
			"workspaceId": str("업무 영역 ID"),
			"items": map[string]any{"type": "array", "description": "등록할 행(최대 5000)", "items": map[string]any{
				"type": "object", "properties": map[string]any{"name": str("고객사명"), "customerCode": str("고객 코드")},
				"required": []string{"name", "customerCode"}, "additionalProperties": false}},
			"updateNames": boolean("true면 이미 있는 코드의 고객사명을 이 목록 값으로 바꿉니다"),
		}), false, false)
}

// callVoiceTool runs the department VOC tools; ok is false for any other name.
func (s *Server) callVoiceTool(ctx context.Context, p *auth.Principal, name string, a map[string]any, meta crm.RequestMeta) (any, bool, error) {
	switch name {
	case "file_customer_voice":
		customerID, err := s.customerRef(ctx, p, strArg(a, "customerId"), strArg(a, "customerCode"))
		if err != nil {
			return nil, true, err
		}
		in := voice.Input{CustomerID: customerID, ContactID: strArg(a, "contactId"), VoiceType: strArg(a, "voiceType"),
			Channel: strArg(a, "channel"), Title: strArg(a, "title"), Body: strArg(a, "body"), Severity: strArg(a, "severity")}
		if ref := strArg(a, "category"); ref != "" {
			c, err := s.findCategory(ctx, p, ref)
			if err != nil {
				return nil, true, err
			}
			in.CategoryID = c.ID
		}
		if fields, ok := a["fields"].(map[string]any); ok {
			in.CustomFields = fields
		}
		v, err := s.Voices.Create(ctx, p, in, meta)
		return v, true, err
	case "resolve_customer_voice":
		in := voice.ResolveInput{Resolution: strArg(a, "resolution"), RootCause: strArg(a, "rootCause"), CauseEvidence: strArg(a, "causeEvidence"),
			PreventiveAction: strArg(a, "preventiveAction"), Note: strArg(a, "note")}
		if fields, ok := a["fields"].(map[string]any); ok {
			in.Fields = fields
		}
		v, err := s.Voices.Resolve(ctx, p, strArg(a, "id"), in, meta)
		return v, true, err
	case "search_voice_knowledge":
		filters := map[string]string{}
		if fields, ok := a["fields"].(map[string]any); ok {
			for k, v := range fields {
				if text, ok := v.(string); ok {
					filters[k] = text
				}
			}
		}
		v, err := s.Voices.SearchKnowledge(ctx, p, voice.KnowledgeQuery{Query: strArg(a, "query"), WorkspaceID: strArg(a, "workspaceId"),
			Fields: filters, CauseEvidence: strArg(a, "causeEvidence"), IncludeUnreviewed: boolArg(a, "includeUnreviewed", false), Limit: intArg(a, "limit", 10)})
		return v, true, err
	case "get_customer_voice_history":
		v, err := s.Voices.History(ctx, p, strArg(a, "customerId"), strArg(a, "customerCode"), strArg(a, "workspaceId"), intArg(a, "limit", 30))
		return v, true, err
	case "register_workspace_customer":
		v, err := s.Voices.QuickRegister(ctx, p, strArg(a, "workspaceId"), voice.QuickCustomerInput{Name: strArg(a, "name"), CustomerCode: strArg(a, "customerCode")}, meta)
		if errors.Is(err, crm.ErrCustomerCodeTaken) {
			if existing, lookupErr := s.CRM.CustomerByCode(ctx, p, strArg(a, "customerCode")); lookupErr == nil {
				return map[string]any{"registered": false, "existing": existing, "message": "같은 고객 코드의 고객이 이미 있어 그 고객을 사용하면 됩니다."}, true, nil
			}
		}
		return v, true, err
	case "import_workspace_customers":
		rows := []voice.ImportRow{}
		if err := decodeArgs(map[string]any{"rows": a["items"]}, &struct {
			Rows *[]voice.ImportRow `json:"rows"`
		}{&rows}); err != nil {
			return nil, true, err
		}
		v, err := s.Voices.ImportCustomers(ctx, p, strArg(a, "workspaceId"), rows, boolArg(a, "updateNames", false), meta)
		return v, true, err
	case "get_voice_categories":
		categories, err := s.Voices.Categories(ctx, p, false)
		if err != nil {
			return nil, true, err
		}
		workspaces, err := s.Voices.Workspaces(ctx, p, false)
		return map[string]any{"categories": categories, "workspaces": workspaces, "causeEvidence": voice.CauseEvidenceLevels}, true, err
	}
	return nil, false, nil
}
