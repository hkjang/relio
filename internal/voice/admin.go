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
	Code                string `json:"code"`
	Name                string `json:"name"`
	Description         string `json:"description"`
	OrganizationID      string `json:"organizationId"`
	Isolated            bool   `json:"isolated"`
	KnowledgeGate       bool   `json:"knowledgeGate"`
	CustomerCodeLabel   string `json:"customerCodeLabel"`
	CustomerCodePattern string `json:"customerCodePattern"`
	Active              *bool  `json:"active"`
	DisplayOrder        int    `json:"displayOrder"`
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
	return nil
}

func (s *Service) CreateWorkspace(ctx context.Context, in WorkspaceInput) (string, error) {
	if err := ValidateWorkspace(in); err != nil {
		return "", err
	}
	id := ids.New()
	_, err := s.DB.Exec(ctx, `INSERT INTO voice_workspaces(id,code,name,description,organization_id,isolated,knowledge_gate,customer_code_label,customer_code_pattern,display_order)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		id, strings.ToUpper(strings.TrimSpace(in.Code)), strings.TrimSpace(in.Name), nullable(in.Description), nullable(in.OrganizationID),
		in.Isolated, in.KnowledgeGate, nullable(in.CustomerCodeLabel), nullable(in.CustomerCodePattern), in.DisplayOrder)
	return id, err
}

// UpdateWorkspace changes everything but the code, which other systems and
// the audit trail use to refer to the workspace.
func (s *Service) UpdateWorkspace(ctx context.Context, id string, in WorkspaceInput) error {
	if err := ValidateWorkspace(in); err != nil {
		return err
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	command, err := s.DB.Exec(ctx, `UPDATE voice_workspaces SET name=$2,description=$3,organization_id=$4,isolated=$5,knowledge_gate=$6,
		customer_code_label=$7,customer_code_pattern=$8,active=$9,display_order=$10,updated_at=now() WHERE id=$1`,
		id, strings.TrimSpace(in.Name), nullable(in.Description), nullable(in.OrganizationID), in.Isolated, in.KnowledgeGate,
		nullable(in.CustomerCodeLabel), nullable(in.CustomerCodePattern), active, in.DisplayOrder)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	// Requests follow their workspace's department, so a change of owner moves
	// the isolated requests with it and they stay findable by its members.
	if in.Isolated {
		_, err = s.DB.Exec(ctx, `UPDATE customer_voices SET organization_id=$2 WHERE workspace_id=$1`, id, in.OrganizationID)
	}
	return err
}

// ---------------------------------------------------------------- presets

// Preset is a ready-made workspace: types, fields and roles for one kind of
// department, applied once by an administrator and edited freely afterwards.
type Preset struct {
	Code        string         `json:"code"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Workspace   WorkspaceInput `json:"workspace"`
	Categories  []presetType   `json:"categories"`
	Fields      []presetField  `json:"fields"`
	Roles       []presetRole   `json:"roles"`
}

type presetType struct {
	Code, Name, VoiceType string
}
type presetField struct {
	Key, Label, Type, Phase, HelpText string
	Required                          bool
	Options                           []string
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
		CustomerCodeLabel: "회원사코드", CustomerCodePattern: `^[0-9]{12}$`},
	Categories: []presetType{
		{"MS_SERVICE_ERROR", "서비스 오류 신고", "DEFECT"},
		{"MS_SMS_ISSUE", "인증문자 이상", "DEFECT"},
		{"MS_INTEGRATION", "연동·개발 문의", "INQUIRY"},
		{"MS_SETTING_CHANGE", "설정·정보 변경 요청", "REQUEST"},
		{"MS_SECURITY_DOCS", "위수탁·보안 자료 요청", "REQUEST"},
		{"MS_BILLING", "요금·정산 문의", "INQUIRY"},
		{"MS_ADMIN_SITE", "관리자 사이트 문의", "INQUIRY"},
		{"MS_OTHER", "기타", "INQUIRY"},
	},
	Fields: []presetField{
		{Key: "service_type", Label: "서비스 구분", Type: "Select", Phase: "INTAKE", Required: true,
			Options: []string{"휴대폰본인확인", "아이핀", "카드본인확인", "모바일안심플러스", "기타"}},
		{Key: "dev_method", Label: "개발방식", Type: "Select", Phase: "INTAKE",
			Options:  []string{"팝업(모듈)", "임베디드(모듈)", "팝업(API)", "임베디드(API)", "전용선", "미확인"},
			HelpText: "접수 시점에 모르면 '미확인'을 고르세요."},
		{Key: "error_code", Label: "오류코드", Type: "Text", Phase: "INTAKE",
			HelpText: "오류코드 또는 핵심 오류 문구. 코드가 없는 문의는 비워 둡니다."},
		{Key: "resolver", Label: "해결 주체", Type: "Select", Phase: "RESOLUTION",
			Options: []string{"회원사 조치", "KCB 운영", "KCB IT", "안내로 종결"}},
	},
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
}

// ApplyPreset creates a preset's workspace, types, fields and roles in one
// transaction. Anything that already exists is left as it is and reported,
// so applying twice is harmless and nothing an administrator edited is lost.
func (s *Service) ApplyPreset(ctx context.Context, p *auth.Principal, code, organizationID string) (PresetResult, error) {
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
	w := preset.Workspace
	w.OrganizationID = strings.TrimSpace(organizationID)
	if err := ValidateWorkspace(w); err != nil {
		return PresetResult{}, err
	}
	out := PresetResult{Created: []string{}, Skipped: []string{}}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `SELECT id FROM voice_workspaces WHERE code=$1`, w.Code).Scan(&out.WorkspaceID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		out.WorkspaceID = ids.New()
		if _, err = tx.Exec(ctx, `INSERT INTO voice_workspaces(id,code,name,description,organization_id,isolated,knowledge_gate,customer_code_label,customer_code_pattern)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, out.WorkspaceID, w.Code, w.Name, w.Description, w.OrganizationID,
			w.Isolated, w.KnowledgeGate, w.CustomerCodeLabel, w.CustomerCodePattern); err != nil {
			return out, err
		}
		out.Created = append(out.Created, "업무 영역 "+w.Name)
	case err != nil:
		return out, err
	default:
		out.Skipped = append(out.Skipped, "업무 영역 "+w.Name+"(이미 있음)")
	}
	for i, t := range preset.Categories {
		command, err := tx.Exec(ctx, `INSERT INTO voice_categories(id,code,name,voice_type,response_hours,resolution_hours,display_order,workspace_id,sla_enabled)
			VALUES($1,$2,$3,$4,8,72,$5,$6,false) ON CONFLICT (code) DO NOTHING`, ids.New(), t.Code, t.Name, t.VoiceType, (i+1)*10, out.WorkspaceID)
		if err != nil {
			return out, err
		}
		if command.RowsAffected() == 1 {
			out.Created = append(out.Created, "유형 "+t.Name)
		} else {
			out.Skipped = append(out.Skipped, "유형 "+t.Name+"(이미 있음)")
		}
	}
	for i, f := range preset.Fields {
		options, _ := json.Marshal(f.Options)
		if f.Options == nil {
			options = []byte("null")
		}
		command, err := tx.Exec(ctx, `INSERT INTO custom_field_definitions(id,entity_type,field_key,label,field_type,required,options,display_order,created_by,workspace_id,phase,help_text)
			VALUES($1,'VOICE',$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (entity_type,field_key) DO NOTHING`,
			ids.New(), f.Key, f.Label, f.Type, f.Required, options, (i+1)*10, p.UserID, out.WorkspaceID, f.Phase, nullable(f.HelpText))
		if err != nil {
			return out, err
		}
		if command.RowsAffected() == 1 {
			out.Created = append(out.Created, "항목 "+f.Label)
		} else {
			out.Skipped = append(out.Skipped, "항목 "+f.Label+"(이미 있음)")
		}
	}
	for _, r := range preset.Roles {
		roleID := ids.New()
		command, err := tx.Exec(ctx, `INSERT INTO roles(id,code,name,description,data_scope) VALUES($1,$2,$3,$4,$5) ON CONFLICT (code) DO NOTHING`,
			roleID, r.Code, r.Name, r.Description, r.DataScope)
		if err != nil {
			return out, err
		}
		if command.RowsAffected() == 0 {
			out.Skipped = append(out.Skipped, "Role "+r.Name+"(이미 있음)")
			continue
		}
		for _, permission := range r.Permissions {
			if _, err = tx.Exec(ctx, `INSERT INTO role_permissions(role_id,permission) VALUES($1,$2) ON CONFLICT DO NOTHING`, roleID, permission); err != nil {
				return out, err
			}
		}
		out.Created = append(out.Created, "Role "+r.Name)
	}
	return out, tx.Commit(ctx)
}
