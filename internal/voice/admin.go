package voice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5"
)

type WorkspaceInput struct {
	Code                    string `json:"code"`
	Name                    string `json:"name"`
	Description             string `json:"description"`
	OrganizationID          string `json:"organizationId"`
	Isolated                bool   `json:"isolated"`
	KnowledgeGate           bool   `json:"knowledgeGate"`
	CustomerCodeLabel       string `json:"customerCodeLabel"`
	CustomerCodePattern     string `json:"customerCodePattern"`
	CustomerCodeUniqueScope string `json:"customerCodeUniqueScope"`
	Active                  *bool  `json:"active"`
	DisplayOrder            int    `json:"displayOrder"`
}

var workspaceCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,39}$`)

// ValidateWorkspace checks a workspace definition before it is stored.
func ValidateWorkspace(in WorkspaceInput) error {
	if !workspaceCode.MatchString(strings.ToUpper(strings.TrimSpace(in.Code))) {
		return errors.New("업무 영역 코드는 영문 대문자로 시작하는 2~40자(A-Z, 0-9, _)여야 합니다")
	}
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("업무 영역 이름을 입력해야 합니다")
	}
	if in.Isolated && strings.TrimSpace(in.OrganizationID) == "" {
		return errors.New("부서 전용(격리) 업무 영역에는 소유 조직이 필요합니다")
	}
	if pattern := strings.TrimSpace(in.CustomerCodePattern); pattern != "" {
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("고객 코드 형식(정규식)이 올바르지 않습니다: %v", err)
		}
	}
	if scope := customerCodeScope(in.CustomerCodeUniqueScope); scope != "GLOBAL" && scope != "WORKSPACE" {
		return errors.New("고객 코드 중복 기준은 GLOBAL 또는 WORKSPACE여야 합니다")
	}
	return nil
}

func customerCodeScope(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return "GLOBAL"
	}
	return value
}

func (s *Service) CreateWorkspace(ctx context.Context, in WorkspaceInput) (string, error) {
	if err := ValidateWorkspace(in); err != nil {
		return "", err
	}
	id := ids.New()
	_, err := s.DB.Exec(ctx, `INSERT INTO voice_workspaces(id,code,name,description,organization_id,isolated,knowledge_gate,customer_code_label,customer_code_pattern,customer_code_unique_scope,display_order)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		id, strings.ToUpper(strings.TrimSpace(in.Code)), strings.TrimSpace(in.Name), nullable(in.Description), nullable(in.OrganizationID),
		in.Isolated, in.KnowledgeGate, nullable(in.CustomerCodeLabel), nullable(in.CustomerCodePattern), customerCodeScope(in.CustomerCodeUniqueScope), in.DisplayOrder)
	return id, err
}

// UpdateWorkspace changes everything but the code, which other systems and
// the audit trail use to refer to the workspace.
func (s *Service) UpdateWorkspace(ctx context.Context, id string, in WorkspaceInput) error {
	if err := ValidateWorkspace(in); err != nil {
		return err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	var previousScope string
	var customerCount int
	if err = tx.QueryRow(ctx, `SELECT customer_code_unique_scope,(SELECT count(*) FROM customers WHERE home_workspace_id=$1) FROM voice_workspaces WHERE id=$1 FOR UPDATE`, id).Scan(&previousScope, &customerCount); err != nil {
		return err
	}
	nextScope := customerCodeScope(in.CustomerCodeUniqueScope)
	if customerCount > 0 && previousScope != nextScope {
		return errors.New("등록된 고객이 있는 업무 영역의 고객 코드 중복 기준은 변경할 수 없습니다")
	}
	command, err := tx.Exec(ctx, `UPDATE voice_workspaces SET name=$2,description=$3,organization_id=$4,isolated=$5,knowledge_gate=$6,
		customer_code_label=$7,customer_code_pattern=$8,customer_code_unique_scope=$9,active=$10,display_order=$11,updated_at=now() WHERE id=$1`,
		id, strings.TrimSpace(in.Name), nullable(in.Description), nullable(in.OrganizationID), in.Isolated, in.KnowledgeGate,
		nullable(in.CustomerCodeLabel), nullable(in.CustomerCodePattern), nextScope, active, in.DisplayOrder)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	// Requests follow their workspace's department, so a change of owner moves
	// the isolated requests and customers atomically, keeping both findable by
	// the new department without exposing only half of the workspace.
	if in.Isolated {
		if _, err = tx.Exec(ctx, `UPDATE customer_voices SET organization_id=$2 WHERE workspace_id=$1`, id, in.OrganizationID); err == nil {
			_, err = tx.Exec(ctx, `UPDATE customers SET organization_id=$2,updated_at=now(),version=version+1 WHERE home_workspace_id=$1`, id, in.OrganizationID)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---------------------------------------------------------------- presets

// Preset is a ready-made workspace: types, fields and roles for one kind of
// department, applied once by an administrator and edited freely afterwards.
type Preset struct {
	Code        string         `json:"code"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Workspace   WorkspaceInput `json:"workspace"`
	Roles       []presetRole   `json:"roles"`
}

type CodedValue struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type PresetCategoryInput struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	VoiceType string `json:"voiceType"`
}

type PresetFieldInput struct {
	Required     bool         `json:"required"`
	DefaultValue string       `json:"defaultValue"`
	Values       []CodedValue `json:"values"`
}

type PresetApplication struct {
	OrganizationID          string                `json:"organizationId"`
	CustomerCodePattern     string                `json:"customerCodePattern"`
	CustomerCodeUniqueScope string                `json:"customerCodeUniqueScope"`
	Categories              []PresetCategoryInput `json:"categories"`
	ServiceType             PresetFieldInput      `json:"serviceType"`
	DevelopmentMethod       PresetFieldInput      `json:"developmentMethod"`
	ErrorCode               PresetFieldInput      `json:"errorCode"`
	Resolver                PresetFieldInput      `json:"resolver"`
	AgentUserIDs            []string              `json:"agentUserIds"`
	ReviewerUserIDs         []string              `json:"reviewerUserIds"`
}

type presetField struct {
	Key, Label, Type, Phase, HelpText string
	Required                          bool
	Options                           []string
	OptionLabels                      map[string]string
	DefaultValue                      string
}

type presetRole struct {
	Code, Name, Description, DataScope string
	Permissions                        []string
}

var memberSupportPreset = Preset{
	Code: "MEMBER_SUPPORT",
	Name: "회원사 민원 (본인확인 서비스)",
	Description: "회원사 민원을 부서 전용으로 접수하고, 검토자가 확인한 해결 사례만 에이전트 진단 근거로 쓰는 구성입니다. " +
		"유형 8종(SLA 미적용), 접수 항목 3종, 해결 주체, 담당·검토자 Role을 만듭니다.",
	Workspace: WorkspaceInput{Code: "MEMBER_SUPPORT", Name: "회원사 민원", Isolated: true, KnowledgeGate: true,
		Description:       "회원사 민원 접수·처리 이력. 부서 외 비노출, 영업 지표 제외.",
		CustomerCodeLabel: "회원사코드", CustomerCodeUniqueScope: "GLOBAL"},
	Roles: []presetRole{
		{Code: "MEMBER_SUPPORT_AGENT", Name: "회원사 민원 담당", DataScope: "DEPARTMENT",
			Description: "회원사 민원을 접수·처리하고 회원사를 간이 등록합니다.",
			Permissions: []string{"voice:read", "voice:write", "customer:read", "customer:write", "contact:read", "contact:write", "mcp:use"}},
		{Code: "MEMBER_SUPPORT_REVIEWER", Name: "회원사 민원 검토자", DataScope: "DEPARTMENT",
			Description: "종결된 민원의 지식 반영 여부를 판정합니다.",
			Permissions: []string{"voice:read", "voice:write", "voice:knowledge-review", "customer:read", "customer:write", "contact:read", "contact:write", "mcp:use"}},
	},
}

// Presets lists what an administrator can apply.
func Presets() []Preset { return []Preset{memberSupportPreset} }

type PresetResult struct {
	WorkspaceID string   `json:"workspaceId"`
	Created     []string `json:"created"`
	Skipped     []string `json:"skipped"`
	Pending     []string `json:"pending"`
}

var presetCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,39}$`)

func validateCodedValues(label string, values []CodedValue, exact int, defaultValue string) error {
	if exact > 0 && len(values) != exact {
		return fmt.Errorf("%s은(는) 정확히 %d개여야 합니다(받은 값: %d개)", label, exact, len(values))
	}
	if exact == 0 && len(values) == 0 {
		return fmt.Errorf("%s 허용값을 하나 이상 입력해야 합니다", label)
	}
	seen := map[string]bool{}
	for i := range values {
		values[i].Code = strings.TrimSpace(values[i].Code)
		values[i].Name = strings.TrimSpace(values[i].Name)
		if !presetCode.MatchString(values[i].Code) || values[i].Name == "" {
			return fmt.Errorf("%s %d번째 값의 코드·명칭이 올바르지 않습니다", label, i+1)
		}
		if seen[values[i].Code] {
			return fmt.Errorf("%s 코드가 중복되었습니다: %s", label, values[i].Code)
		}
		seen[values[i].Code] = true
	}
	if defaultValue != "" && !seen[strings.TrimSpace(defaultValue)] {
		return fmt.Errorf("%s 기본값은 허용 코드 중 하나여야 합니다", label)
	}
	return nil
}

func validatePresetApplication(in PresetApplication) error {
	if strings.TrimSpace(in.OrganizationID) == "" {
		return errors.New("구성을 적용할 부서(조직)를 선택해야 합니다")
	}
	if len(in.Categories) != 8 {
		return fmt.Errorf("민원 유형은 부서가 확정한 코드·명칭으로 정확히 8개 입력해야 합니다(받은 값: %d개)", len(in.Categories))
	}
	seenCategories := map[string]bool{}
	for i, category := range in.Categories {
		code := strings.ToUpper(strings.TrimSpace(category.Code))
		if !presetCode.MatchString(code) || strings.TrimSpace(category.Name) == "" || !voiceTypes[strings.ToUpper(strings.TrimSpace(category.VoiceType))] {
			return fmt.Errorf("민원 유형 %d번째 값의 코드·명칭·구분을 확인하세요", i+1)
		}
		if seenCategories[code] {
			return fmt.Errorf("민원 유형 코드가 중복되었습니다: %s", code)
		}
		seenCategories[code] = true
	}
	for _, field := range []struct {
		name  string
		input PresetFieldInput
		exact int
	}{{"서비스 구분", in.ServiceType, 0}, {"개발방식", in.DevelopmentMethod, 0}, {"해결 주체", in.Resolver, 4}} {
		if err := validateCodedValues(field.name, field.input.Values, field.exact, field.input.DefaultValue); err != nil {
			return err
		}
	}
	if len(in.ErrorCode.Values) != 0 {
		return errors.New("오류코드는 선택형 코드 목록이 아닌 텍스트 항목입니다")
	}
	for _, id := range append(append([]string{}, in.AgentUserIDs...), in.ReviewerUserIDs...) {
		if strings.TrimSpace(id) == "" {
			return errors.New("연결할 계정 ID가 비어 있습니다")
		}
	}
	seenAgents := map[string]bool{}
	for _, id := range in.AgentUserIDs {
		seenAgents[id] = true
	}
	for _, id := range in.ReviewerUserIDs {
		if seenAgents[id] {
			return errors.New("같은 계정을 담당자와 검토자에 동시에 지정할 수 없습니다")
		}
	}
	w := memberSupportPreset.Workspace
	w.OrganizationID = in.OrganizationID
	w.CustomerCodePattern = in.CustomerCodePattern
	w.CustomerCodeUniqueScope = in.CustomerCodeUniqueScope
	return ValidateWorkspace(w)
}

func configuredField(key, label, phase, fieldType, help string, input PresetFieldInput) presetField {
	f := presetField{Key: key, Label: label, Phase: phase, Type: fieldType, HelpText: help, Required: input.Required,
		DefaultValue: strings.TrimSpace(input.DefaultValue), OptionLabels: map[string]string{}}
	for _, value := range input.Values {
		code := strings.TrimSpace(value.Code)
		f.Options = append(f.Options, code)
		f.OptionLabels[code] = strings.TrimSpace(value.Name)
	}
	return f
}

// ApplyPreset creates a preset's workspace, types, fields and roles in one
// transaction. Anything that already exists is left as it is and reported,
// so applying twice is harmless and nothing an administrator edited is lost.
func (s *Service) ApplyPreset(ctx context.Context, p *auth.Principal, code string, in PresetApplication) (PresetResult, error) {
	var preset *Preset
	for _, candidate := range Presets() {
		if candidate.Code == strings.ToUpper(strings.TrimSpace(code)) {
			c := candidate
			preset = &c
		}
	}
	if preset == nil {
		return PresetResult{}, errors.New("알 수 없는 구성 템플릿입니다")
	}
	if err := validatePresetApplication(in); err != nil {
		return PresetResult{}, err
	}
	w := preset.Workspace
	w.OrganizationID = strings.TrimSpace(in.OrganizationID)
	w.CustomerCodePattern = strings.TrimSpace(in.CustomerCodePattern)
	w.CustomerCodeUniqueScope = customerCodeScope(in.CustomerCodeUniqueScope)
	fields := []presetField{
		configuredField("service_type", "서비스 구분", "INTAKE", "Select", "부서가 확정한 서비스 코드입니다.", in.ServiceType),
		configuredField("dev_method", "개발방식", "INTAKE", "Select", "부서가 확정한 개발방식 코드입니다.", in.DevelopmentMethod),
		configuredField("error_code", "오류코드", "INTAKE", "Text", "오류코드 또는 핵심 오류 문구입니다.", in.ErrorCode),
		configuredField("resolver", "해결 주체", "RESOLUTION", "Select", "부서가 확정한 해결 주체 코드입니다.", in.Resolver),
	}
	out := PresetResult{Created: []string{}, Skipped: []string{}, Pending: []string{}}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `SELECT id FROM voice_workspaces WHERE code=$1`, w.Code).Scan(&out.WorkspaceID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		out.WorkspaceID = ids.New()
		if _, err = tx.Exec(ctx, `INSERT INTO voice_workspaces(id,code,name,description,organization_id,isolated,knowledge_gate,customer_code_label,customer_code_pattern,customer_code_unique_scope)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, out.WorkspaceID, w.Code, w.Name, w.Description, w.OrganizationID,
			w.Isolated, w.KnowledgeGate, w.CustomerCodeLabel, nullable(w.CustomerCodePattern), w.CustomerCodeUniqueScope); err != nil {
			return out, err
		}
		out.Created = append(out.Created, "업무 영역 "+w.Name)
	case err != nil:
		return out, err
	default:
		out.Skipped = append(out.Skipped, "업무 영역 "+w.Name+"(이미 있음)")
	}
	for i, t := range in.Categories {
		command, err := tx.Exec(ctx, `INSERT INTO voice_categories(id,code,name,voice_type,response_hours,resolution_hours,display_order,workspace_id,sla_enabled)
			VALUES($1,$2,$3,$4,8,72,$5,$6,false) ON CONFLICT (code) DO NOTHING`, ids.New(), strings.ToUpper(strings.TrimSpace(t.Code)), strings.TrimSpace(t.Name), strings.ToUpper(strings.TrimSpace(t.VoiceType)), (i+1)*10, out.WorkspaceID)
		if err != nil {
			return out, err
		}
		if command.RowsAffected() == 1 {
			out.Created = append(out.Created, "유형 "+t.Name)
		} else {
			out.Skipped = append(out.Skipped, "유형 "+t.Name+"(이미 있음)")
		}
	}
	for i, f := range fields {
		options, _ := json.Marshal(f.Options)
		if f.Options == nil {
			options = []byte("null")
		}
		labels, _ := json.Marshal(f.OptionLabels)
		var defaultValue any
		if f.DefaultValue != "" {
			defaultValue, _ = json.Marshal(f.DefaultValue)
		}
		command, err := tx.Exec(ctx, `INSERT INTO custom_field_definitions(id,entity_type,field_key,label,field_type,required,options,option_labels,default_value,display_order,created_by,workspace_id,phase,help_text)
			VALUES($1,'VOICE',$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT DO NOTHING`,
			ids.New(), f.Key, f.Label, f.Type, f.Required, options, labels, defaultValue, (i+1)*10, p.UserID, out.WorkspaceID, f.Phase, nullable(f.HelpText))
		if err != nil {
			return out, err
		}
		if command.RowsAffected() == 1 {
			out.Created = append(out.Created, "항목 "+f.Label)
		} else {
			out.Skipped = append(out.Skipped, "항목 "+f.Label+"(이미 있음)")
		}
	}
	roleIDs := map[string]string{}
	for _, r := range preset.Roles {
		roleID := ids.New()
		command, err := tx.Exec(ctx, `INSERT INTO roles(id,code,name,description,data_scope) VALUES($1,$2,$3,$4,$5) ON CONFLICT (code) DO NOTHING`,
			roleID, r.Code, r.Name, r.Description, r.DataScope)
		if err != nil {
			return out, err
		}
		if command.RowsAffected() == 0 {
			if err = tx.QueryRow(ctx, `SELECT id FROM roles WHERE code=$1`, r.Code).Scan(&roleID); err != nil {
				return out, err
			}
			out.Skipped = append(out.Skipped, "Role "+r.Name+"(이미 있음)")
		} else {
			out.Created = append(out.Created, "Role "+r.Name)
		}
		for _, permission := range r.Permissions {
			if _, err = tx.Exec(ctx, `INSERT INTO role_permissions(role_id,permission) VALUES($1,$2) ON CONFLICT DO NOTHING`, roleID, permission); err != nil {
				return out, err
			}
		}
		roleIDs[r.Code] = roleID
	}
	for roleCode, users := range map[string][]string{"MEMBER_SUPPORT_AGENT": in.AgentUserIDs, "MEMBER_SUPPORT_REVIEWER": in.ReviewerUserIDs} {
		for _, userID := range users {
			var allowed bool
			if err = tx.QueryRow(ctx, `WITH RECURSIVE org_tree AS (
				SELECT id FROM organizations WHERE id=$1 UNION ALL SELECT o.id FROM organizations o JOIN org_tree t ON o.parent_id=t.id
			) SELECT EXISTS(SELECT 1 FROM users WHERE id=$2 AND active AND organization_id IN (SELECT id FROM org_tree))`, w.OrganizationID, userID).Scan(&allowed); err != nil {
				return out, err
			}
			if !allowed {
				return out, fmt.Errorf("계정 %s은(는) 선택한 부서 또는 하위 조직의 활성 계정이 아닙니다", userID)
			}
			command, err := tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, userID, roleIDs[roleCode])
			if err != nil {
				return out, err
			}
			if command.RowsAffected() == 1 {
				out.Created = append(out.Created, "계정 Role 연결 "+userID)
			} else {
				out.Skipped = append(out.Skipped, "계정 Role 연결 "+userID+"(이미 있음)")
			}
		}
	}
	if len(in.AgentUserIDs) == 0 {
		out.Pending = append(out.Pending, "담당자 역할을 부여할 대상 계정")
	}
	if len(in.ReviewerUserIDs) == 0 {
		out.Pending = append(out.Pending, "검토자 역할을 부여할 대상 계정")
	}
	return out, tx.Commit(ctx)
}
