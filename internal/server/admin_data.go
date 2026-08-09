package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/intelligence"
	"github.com/hkjang/relio/internal/platform/httpx"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/hkjang/relio/internal/platform/version"
	"github.com/jackc/pgx/v5"
)

type dataQualitySample struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Detail    string    `json:"detail"`
	Route     string    `json:"route"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type dataQualityCategory struct {
	Key               string              `json:"key"`
	Label             string              `json:"label"`
	Entity            string              `json:"entity"`
	Severity          string              `json:"severity"`
	Count             int                 `json:"count"`
	BaseCount         int                 `json:"baseCount"`
	CoveragePercent   int                 `json:"coveragePercent"`
	Weight            int                 `json:"weight"`
	Description       string              `json:"description"`
	RecommendedAction string              `json:"recommendedAction"`
	Route             string              `json:"route"`
	Samples           []dataQualitySample `json:"samples"`
}

type dataQualitySpec struct {
	Key, Label, Entity, Severity, Description, RecommendedAction, Route string
	Weight, BaseCount                                                   int
	CountSQL, SamplesSQL                                                string
}

func (s *Server) collectDataQuality(ctx context.Context) (map[string]any, error) {
	var customers, contacts, opportunities int
	if err := s.DB.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM customers WHERE active AND merged_into_id IS NULL),
		(SELECT count(*) FROM contacts ct JOIN customers c ON c.id=ct.customer_id WHERE c.active AND c.merged_into_id IS NULL),
		(SELECT count(*) FROM opportunities WHERE status='OPEN')`).Scan(&customers, &contacts, &opportunities); err != nil {
		return nil, err
	}
	specs := []dataQualitySpec{
		{Key: "customer-registration", Label: "사업자번호 누락", Entity: "CUSTOMER", Severity: "WARNING", Weight: 15, BaseCount: customers, Description: "활성 고객의 사업자번호가 비어 있습니다.", RecommendedAction: "법인 식별정보를 확인해 중복과 연계 오류를 줄이세요.", Route: "/app/customers", CountSQL: `SELECT count(*) FROM customers WHERE active AND merged_into_id IS NULL AND COALESCE(trim(registration_no),'')=''`, SamplesSQL: `SELECT id::text,name,'사업자번호 없음',updated_at FROM customers WHERE active AND merged_into_id IS NULL AND COALESCE(trim(registration_no),'')='' ORDER BY updated_at DESC LIMIT 8`},
		{Key: "customer-contact", Label: "담당자 없는 고객", Entity: "CUSTOMER", Severity: "WARNING", Weight: 15, BaseCount: customers, Description: "활성 고객에 등록된 고객 담당자가 없습니다.", RecommendedAction: "Customer 360에서 주요 담당자와 Decision Maker 후보를 등록하세요.", Route: "/app/customers", CountSQL: `SELECT count(*) FROM customers c WHERE c.active AND c.merged_into_id IS NULL AND NOT EXISTS(SELECT 1 FROM contacts ct WHERE ct.customer_id=c.id)`, SamplesSQL: `SELECT c.id::text,c.name,'등록된 담당자 없음',c.updated_at FROM customers c WHERE c.active AND c.merged_into_id IS NULL AND NOT EXISTS(SELECT 1 FROM contacts ct WHERE ct.customer_id=c.id) ORDER BY c.updated_at DESC LIMIT 8`},
		{Key: "customer-stale", Label: "90일 이상 미접촉 고객", Entity: "CUSTOMER", Severity: "WATCH", Weight: 12, BaseCount: customers, Description: "생성된 지 90일 이상이고 최근 90일 Activity가 없습니다.", RecommendedAction: "관계 유지 활동을 계획하거나 휴면 고객 상태를 검토하세요.", Route: "/app/customers", CountSQL: `SELECT count(*) FROM customers c WHERE c.active AND c.merged_into_id IS NULL AND c.created_at<now()-interval '90 days' AND NOT EXISTS(SELECT 1 FROM activities a WHERE a.customer_id=c.id AND a.occurred_at>=now()-interval '90 days')`, SamplesSQL: `SELECT c.id::text,c.name,'최근 90일 Activity 없음',c.updated_at FROM customers c WHERE c.active AND c.merged_into_id IS NULL AND c.created_at<now()-interval '90 days' AND NOT EXISTS(SELECT 1 FROM activities a WHERE a.customer_id=c.id AND a.occurred_at>=now()-interval '90 days') ORDER BY c.updated_at LIMIT 8`},
		{Key: "customer-duplicate", Label: "중복 가능 고객", Entity: "CUSTOMER", Severity: "CRITICAL", Weight: 15, BaseCount: customers, Description: "사업자번호 또는 공백을 제거한 고객명이 같은 활성 고객입니다.", RecommendedAction: "Customer 360의 중복 후보를 검토하고 안전하게 Merge하세요.", Route: "/app/customers", CountSQL: `WITH keyed AS (SELECT id,COALESCE('REG:'||NULLIF(regexp_replace(COALESCE(registration_no,''),'[^0-9]','','g'),''),'NAME:'||lower(regexp_replace(name,'\s+','','g'))) k FROM customers WHERE active AND merged_into_id IS NULL), duplicates AS (SELECT k FROM keyed GROUP BY k HAVING count(*)>1) SELECT count(*) FROM keyed JOIN duplicates USING(k)`, SamplesSQL: `WITH keyed AS (SELECT id,COALESCE('REG:'||NULLIF(regexp_replace(COALESCE(registration_no,''),'[^0-9]','','g'),''),'NAME:'||lower(regexp_replace(name,'\s+','','g'))) k FROM customers WHERE active AND merged_into_id IS NULL), duplicates AS (SELECT k FROM keyed GROUP BY k HAVING count(*)>1) SELECT c.id::text,c.name,'동일 식별자 고객 존재',c.updated_at FROM keyed k JOIN duplicates d USING(k) JOIN customers c ON c.id=k.id ORDER BY c.name LIMIT 8`},
		{Key: "contact-channel", Label: "연락수단 없는 담당자", Entity: "CONTACT", Severity: "WARNING", Weight: 10, BaseCount: contacts, Description: "이메일, 전화번호, 휴대전화가 모두 비어 있습니다.", RecommendedAction: "최소 하나의 유효한 연락수단을 확보하세요.", Route: "/app/customers", CountSQL: `SELECT count(*) FROM contacts ct JOIN customers c ON c.id=ct.customer_id WHERE c.active AND c.merged_into_id IS NULL AND COALESCE(trim(ct.email),'')='' AND COALESCE(trim(ct.phone),'')='' AND COALESCE(trim(ct.mobile),'')=''`, SamplesSQL: `SELECT c.id::text,ct.name,c.name||' · 연락수단 없음',ct.updated_at FROM contacts ct JOIN customers c ON c.id=ct.customer_id WHERE c.active AND c.merged_into_id IS NULL AND COALESCE(trim(ct.email),'')='' AND COALESCE(trim(ct.phone),'')='' AND COALESCE(trim(ct.mobile),'')='' ORDER BY ct.updated_at DESC LIMIT 8`},
		{Key: "opportunity-next-action", Label: "Next Action 없는 Deal", Entity: "OPPORTUNITY", Severity: "CRITICAL", Weight: 12, BaseCount: opportunities, Description: "진행 중인 Opportunity에 다음 행동 또는 실행일이 없습니다.", RecommendedAction: "구체적인 다음 행동과 실행 일자를 등록하세요.", Route: "/app/opportunities", CountSQL: `SELECT count(*) FROM opportunities WHERE status='OPEN' AND (COALESCE(trim(next_action),'')='' OR next_action_date IS NULL)`, SamplesSQL: `SELECT id::text,name,'다음 행동 또는 실행일 누락',updated_at FROM opportunities WHERE status='OPEN' AND (COALESCE(trim(next_action),'')='' OR next_action_date IS NULL) ORDER BY expected_amount DESC,updated_at DESC LIMIT 8`},
		{Key: "opportunity-decision-maker", Label: "Decision Maker 미확인", Entity: "OPPORTUNITY", Severity: "WARNING", Weight: 11, BaseCount: opportunities, Description: "진행 중인 Opportunity 고객에 Decision Maker가 없습니다.", RecommendedAction: "구매 의사결정자를 식별하고 관계 역할을 갱신하세요.", Route: "/app/opportunities", CountSQL: `SELECT count(*) FROM opportunities o WHERE o.status='OPEN' AND NOT EXISTS(SELECT 1 FROM contacts ct WHERE ct.customer_id=o.customer_id AND ct.decision_maker)`, SamplesSQL: `SELECT o.id::text,o.name,c.name||' · Decision Maker 없음',o.updated_at FROM opportunities o JOIN customers c ON c.id=o.customer_id WHERE o.status='OPEN' AND NOT EXISTS(SELECT 1 FROM contacts ct WHERE ct.customer_id=o.customer_id AND ct.decision_maker) ORDER BY o.expected_amount DESC,o.updated_at DESC LIMIT 8`},
		{Key: "opportunity-stale", Label: "30일 이상 정체 Deal", Entity: "OPPORTUNITY", Severity: "WARNING", Weight: 10, BaseCount: opportunities, Description: "진행 중인 Opportunity가 30일 이상 고객 Activity 없이 정체되어 있습니다.", RecommendedAction: "Deal Inspection에서 위험요인과 Stage 진행 조건을 검토하세요.", Route: "/app/intelligence", CountSQL: `SELECT count(*) FROM opportunities WHERE status='OPEN' AND COALESCE(last_activity_at,created_at)<now()-interval '30 days'`, SamplesSQL: `SELECT id::text,name,'30일 이상 고객 Activity 없음',updated_at FROM opportunities WHERE status='OPEN' AND COALESCE(last_activity_at,created_at)<now()-interval '30 days' ORDER BY COALESCE(last_activity_at,created_at) LIMIT 8`},
	}
	categories := make([]dataQualityCategory, 0, len(specs))
	totalIssues := 0
	penalty := 0.0
	for _, spec := range specs {
		var count int
		if err := s.DB.QueryRow(ctx, spec.CountSQL).Scan(&count); err != nil {
			return nil, err
		}
		samples := []dataQualitySample{}
		rows, err := s.DB.Query(ctx, spec.SamplesSQL)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item dataQualitySample
			if err = rows.Scan(&item.ID, &item.Name, &item.Detail, &item.UpdatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			item.Route = spec.Route + "/" + item.ID
			if spec.Route == "/app/intelligence" {
				item.Route = spec.Route
			}
			samples = append(samples, item)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		coverage := 100
		if spec.BaseCount > 0 {
			ratio := math.Min(1, float64(count)/float64(spec.BaseCount))
			coverage = int(math.Round((1 - ratio) * 100))
			penalty += ratio * float64(spec.Weight)
		}
		severity := spec.Severity
		if count == 0 {
			severity = "HEALTHY"
		}
		categories = append(categories, dataQualityCategory{Key: spec.Key, Label: spec.Label, Entity: spec.Entity, Severity: severity, Count: count, BaseCount: spec.BaseCount, CoveragePercent: coverage, Weight: spec.Weight, Description: spec.Description, RecommendedAction: spec.RecommendedAction, Route: spec.Route, Samples: samples})
		totalIssues += count
	}
	score := int(math.Round(math.Max(0, 100-penalty)))
	return map[string]any{
		"score": score, "totalIssues": totalIssues, "generatedAt": time.Now().UTC(),
		"counts":     map[string]int{"customers": customers, "contacts": contacts, "openOpportunities": opportunities},
		"categories": categories,
	}, nil
}

func (s *Server) adminDataQuality(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	result, err := s.collectDataQuality(r.Context())
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

type bundleSetting struct {
	Namespace       string `json:"namespace"`
	Key             string `json:"key"`
	Value           any    `json:"value"`
	ValueType       string `json:"valueType"`
	RestartRequired bool   `json:"restartRequired"`
}

type bundleRole struct {
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	DataScope   string   `json:"dataScope"`
	Permissions []string `json:"permissions"`
}

type bundleStage struct {
	Name             string  `json:"name"`
	Order            int     `json:"order"`
	Probability      float64 `json:"probability"`
	ForecastCategory string  `json:"forecastCategory"`
	IsWon            bool    `json:"isWon"`
	IsLost           bool    `json:"isLost"`
	Active           bool    `json:"active"`
	Color            string  `json:"color"`
	MinDays          *int    `json:"minDays,omitempty"`
	MaxDays          *int    `json:"maxDays,omitempty"`
}

type bundlePipeline struct {
	Name      string        `json:"name"`
	Active    bool          `json:"active"`
	IsDefault bool          `json:"isDefault"`
	Stages    []bundleStage `json:"stages"`
}

type bundleCustomField struct {
	EntityType   string `json:"entityType"`
	Key          string `json:"key"`
	Label        string `json:"label"`
	Type         string `json:"type"`
	Required     bool   `json:"required"`
	Options      any    `json:"options,omitempty"`
	Active       bool   `json:"active"`
	DisplayOrder int    `json:"displayOrder"`
}

type bundleHealthRule struct {
	Code              string `json:"code"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	RuleType          string `json:"ruleType"`
	RecommendedAction string `json:"recommendedAction"`
	Threshold         any    `json:"threshold"`
	RiskScore         int    `json:"riskScore"`
	Priority          int    `json:"priority"`
	Active            bool   `json:"active"`
}

type bundleApprovalPolicy struct {
	Name                     string `json:"name"`
	EntityType               string `json:"entityType"`
	ConditionField           string `json:"conditionField,omitempty"`
	ConditionOperator        string `json:"conditionOperator,omitempty"`
	ConditionValue           any    `json:"conditionValue,omitempty"`
	ApproverMethod           string `json:"approverMethod"`
	ApproverRoleCode         string `json:"approverRoleCode,omitempty"`
	ApproverOrganizationCode string `json:"approverOrganizationCode,omitempty"`
	ApprovalSteps            int    `json:"approvalSteps"`
	Priority                 int    `json:"priority"`
	AllowReject              bool   `json:"allowReject"`
	AllowResubmit            bool   `json:"allowResubmit"`
	AllowDelegate            bool   `json:"allowDelegate"`
	Active                   bool   `json:"active"`
}

type bundleStageExecution struct {
	PipelineName string                           `json:"pipelineName"`
	StageOrder   int                              `json:"stageOrder"`
	Playbook     intelligence.StageExecutionInput `json:"playbook"`
}

type configurationBundle struct {
	Format           string                 `json:"format"`
	Product          string                 `json:"product"`
	SourceVersion    string                 `json:"sourceVersion"`
	GeneratedAt      time.Time              `json:"generatedAt"`
	Settings         []bundleSetting        `json:"settings"`
	Roles            []bundleRole           `json:"roles"`
	Pipelines        []bundlePipeline       `json:"pipelines"`
	CustomFields     []bundleCustomField    `json:"customFields"`
	DealHealthRules  []bundleHealthRule     `json:"dealHealthRules"`
	ApprovalPolicies []bundleApprovalPolicy `json:"approvalPolicies"`
	SalesExecution   []bundleStageExecution `json:"salesExecution"`
}

func (s *Server) configurationBundle(ctx context.Context, p *auth.Principal) (configurationBundle, error) {
	bundle := configurationBundle{Format: "relio-config/v1", Product: "Relio", SourceVersion: version.Current().Version, GeneratedAt: time.Now().UTC(), Settings: []bundleSetting{}, Roles: []bundleRole{}, Pipelines: []bundlePipeline{}, CustomFields: []bundleCustomField{}, DealHealthRules: []bundleHealthRule{}, ApprovalPolicies: []bundleApprovalPolicy{}, SalesExecution: []bundleStageExecution{}}
	settings, err := s.Settings.List(ctx, "")
	if err != nil {
		return bundle, err
	}
	for _, item := range settings {
		if item.Secret || sensitiveBundleSetting(item.Namespace, item.Key) {
			continue
		}
		bundle.Settings = append(bundle.Settings, bundleSetting{Namespace: item.Namespace, Key: item.Key, Value: item.Value, ValueType: item.ValueType, RestartRequired: item.RestartRequired})
	}
	rows, err := s.DB.Query(ctx, `SELECT r.code,r.name,COALESCE(r.description,''),r.data_scope,COALESCE(array_agg(rp.permission ORDER BY rp.permission) FILTER(WHERE rp.permission IS NOT NULL),ARRAY[]::text[]) FROM roles r LEFT JOIN role_permissions rp ON rp.role_id=r.id WHERE NOT r.system_role GROUP BY r.id ORDER BY r.code`)
	if err != nil {
		return bundle, err
	}
	for rows.Next() {
		var item bundleRole
		if err = rows.Scan(&item.Code, &item.Name, &item.Description, &item.DataScope, &item.Permissions); err != nil {
			rows.Close()
			return bundle, err
		}
		bundle.Roles = append(bundle.Roles, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return bundle, err
	}
	rows.Close()
	rows, err = s.DB.Query(ctx, `SELECT id,name,active,is_default FROM pipelines ORDER BY is_default DESC,name`)
	if err != nil {
		return bundle, err
	}
	for rows.Next() {
		var id string
		var item bundlePipeline
		if err = rows.Scan(&id, &item.Name, &item.Active, &item.IsDefault); err != nil {
			rows.Close()
			return bundle, err
		}
		item.Stages = []bundleStage{}
		stageRows, stageErr := s.DB.Query(ctx, `SELECT name,stage_order,probability,forecast_category,is_won,is_lost,active,color,min_days,max_days FROM pipeline_stages WHERE pipeline_id=$1 ORDER BY stage_order`, id)
		if stageErr != nil {
			rows.Close()
			return bundle, stageErr
		}
		for stageRows.Next() {
			var stage bundleStage
			if stageErr = stageRows.Scan(&stage.Name, &stage.Order, &stage.Probability, &stage.ForecastCategory, &stage.IsWon, &stage.IsLost, &stage.Active, &stage.Color, &stage.MinDays, &stage.MaxDays); stageErr != nil {
				stageRows.Close()
				rows.Close()
				return bundle, stageErr
			}
			item.Stages = append(item.Stages, stage)
		}
		stageErr = stageRows.Err()
		stageRows.Close()
		if stageErr != nil {
			rows.Close()
			return bundle, stageErr
		}
		bundle.Pipelines = append(bundle.Pipelines, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return bundle, err
	}
	rows.Close()
	if err = s.collectBundleTables(ctx, &bundle); err != nil {
		return bundle, err
	}
	executions, err := s.Intel.AdminStageExecutions(ctx, p)
	if err != nil {
		return bundle, err
	}
	for _, item := range executions {
		if item.Playbook.ID == "" && len(item.Criteria) == 0 {
			continue
		}
		playbookName := item.Playbook.Name
		if strings.TrimSpace(playbookName) == "" {
			playbookName = item.StageName + " Playbook"
		}
		input := intelligence.StageExecutionInput{PlaybookName: playbookName, Guidance: item.Playbook.Guidance, Active: item.Playbook.Active, Items: []intelligence.PlaybookItemInput{}, Criteria: []intelligence.CriterionInput{}}
		for _, value := range item.Playbook.Items {
			input.Items = append(input.Items, intelligence.PlaybookItemInput{Title: value.Title, Description: value.Description, ItemType: value.ItemType, FieldKey: value.FieldKey, Required: value.Required, DisplayOrder: value.DisplayOrder})
		}
		for _, value := range item.Criteria {
			value.ID = ""
			input.Criteria = append(input.Criteria, value)
		}
		bundle.SalesExecution = append(bundle.SalesExecution, bundleStageExecution{PipelineName: item.PipelineName, StageOrder: item.StageOrder, Playbook: input})
	}
	return bundle, nil
}

func (s *Server) collectBundleTables(ctx context.Context, bundle *configurationBundle) error {
	rows, err := s.DB.Query(ctx, `SELECT entity_type,field_key,label,field_type,required,options,active,display_order FROM custom_field_definitions ORDER BY entity_type,display_order,field_key`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item bundleCustomField
		var raw []byte
		if err = rows.Scan(&item.EntityType, &item.Key, &item.Label, &item.Type, &item.Required, &raw, &item.Active, &item.DisplayOrder); err != nil {
			rows.Close()
			return err
		}
		_ = json.Unmarshal(raw, &item.Options)
		bundle.CustomFields = append(bundle.CustomFields, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = s.DB.Query(ctx, `SELECT code,name,COALESCE(description,''),rule_type,threshold,risk_score,recommended_action,active,priority FROM deal_health_rules ORDER BY priority,code`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item bundleHealthRule
		var raw []byte
		if err = rows.Scan(&item.Code, &item.Name, &item.Description, &item.RuleType, &raw, &item.RiskScore, &item.RecommendedAction, &item.Active, &item.Priority); err != nil {
			rows.Close()
			return err
		}
		_ = json.Unmarshal(raw, &item.Threshold)
		bundle.DealHealthRules = append(bundle.DealHealthRules, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = s.DB.Query(ctx, `SELECT p.name,p.entity_type,COALESCE(p.condition_field,''),COALESCE(p.condition_operator,''),p.condition_value,p.approver_method,COALESCE(r.code,''),COALESCE(o.code,''),p.approval_steps,p.priority,p.allow_reject,p.allow_resubmit,p.allow_delegate,p.active FROM approval_policies p LEFT JOIN roles r ON r.id=p.approver_role_id LEFT JOIN organizations o ON o.id=p.approver_org_id ORDER BY p.priority,p.entity_type,p.name`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item bundleApprovalPolicy
		var raw []byte
		if err = rows.Scan(&item.Name, &item.EntityType, &item.ConditionField, &item.ConditionOperator, &raw, &item.ApproverMethod, &item.ApproverRoleCode, &item.ApproverOrganizationCode, &item.ApprovalSteps, &item.Priority, &item.AllowReject, &item.AllowResubmit, &item.AllowDelegate, &item.Active); err != nil {
			rows.Close()
			return err
		}
		_ = json.Unmarshal(raw, &item.ConditionValue)
		bundle.ApprovalPolicies = append(bundle.ApprovalPolicies, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	return nil
}

type bundleComparable struct {
	Section string
	Key     string
	Value   any
}

type bundleChange struct {
	Section string `json:"section"`
	Key     string `json:"key"`
	Action  string `json:"action"`
	Before  any    `json:"before,omitempty"`
	After   any    `json:"after"`
}

type bundleDiffSection struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Create    int    `json:"create"`
	Update    int    `json:"update"`
	Unchanged int    `json:"unchanged"`
	Total     int    `json:"total"`
}

type bundlePreview struct {
	Format        string              `json:"format"`
	SourceVersion string              `json:"sourceVersion"`
	GeneratedAt   time.Time           `json:"generatedAt"`
	Sections      []bundleDiffSection `json:"sections"`
	Changes       []bundleChange      `json:"changes"`
	Summary       map[string]int      `json:"summary"`
	SafeToApply   bool                `json:"safeToApply"`
	Notices       []string            `json:"notices"`
}

var bundleNamePattern = regexp.MustCompile(`^[a-z0-9_-]{1,80}$`)
var roleCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,79}$`)

func sensitiveBundleSetting(namespace, key string) bool {
	value := strings.ToLower(namespace + "." + key)
	for _, word := range []string{"password", "secret", "token", "credential", "private_key", "master_key", "dsn"} {
		if strings.Contains(value, word) {
			return true
		}
	}
	return false
}

func flattenBundle(bundle configurationBundle) ([]bundleComparable, error) {
	items := []bundleComparable{}
	add := func(section, key string, value any) {
		items = append(items, bundleComparable{Section: section, Key: key, Value: value})
	}
	for _, item := range bundle.Settings {
		add("settings", item.Namespace+"."+item.Key, item)
	}
	for _, item := range bundle.Roles {
		add("roles", strings.ToUpper(item.Code), item)
	}
	for _, pipeline := range bundle.Pipelines {
		value := pipeline
		value.Stages = nil
		add("pipelines", pipeline.Name, value)
		for _, stage := range pipeline.Stages {
			add("pipelineStages", fmt.Sprintf("%s#%d", pipeline.Name, stage.Order), stage)
		}
	}
	for _, item := range bundle.CustomFields {
		add("customFields", strings.ToUpper(item.EntityType)+"."+strings.ToLower(item.Key), item)
	}
	for _, item := range bundle.DealHealthRules {
		add("dealHealthRules", strings.ToUpper(item.Code), item)
	}
	for _, item := range bundle.ApprovalPolicies {
		add("approvalPolicies", strings.ToUpper(item.EntityType)+"."+item.Name, item)
	}
	for _, item := range bundle.SalesExecution {
		add("salesExecution", fmt.Sprintf("%s#%d", item.PipelineName, item.StageOrder), item)
	}
	seen := map[string]bool{}
	for _, item := range items {
		key := item.Section + ":" + item.Key
		if seen[key] {
			return nil, fmt.Errorf("configuration bundle contains duplicate key %s", key)
		}
		seen[key] = true
	}
	return items, nil
}

func validateConfigurationBundle(bundle configurationBundle) error {
	if bundle.Format != "relio-config/v1" || bundle.Product != "Relio" {
		return errors.New("unsupported Relio configuration bundle format")
	}
	items, err := flattenBundle(bundle)
	if err != nil {
		return err
	}
	if len(items) > 5000 {
		return errors.New("configuration bundle exceeds 5000 items")
	}
	for _, item := range bundle.Settings {
		if !bundleNamePattern.MatchString(item.Namespace) || !bundleNamePattern.MatchString(item.Key) || sensitiveBundleSetting(item.Namespace, item.Key) {
			return fmt.Errorf("setting %s.%s is not allowed in a configuration bundle", item.Namespace, item.Key)
		}
		if item.ValueType != "string" && item.ValueType != "number" && item.ValueType != "boolean" && item.ValueType != "json" {
			return fmt.Errorf("setting %s.%s has invalid valueType", item.Namespace, item.Key)
		}
	}
	validScopes := map[string]bool{"USER": true, "TEAM": true, "DEPARTMENT": true, "DIVISION": true, "COMPANY": true}
	for _, item := range bundle.Roles {
		if strings.EqualFold(item.Code, "SYSTEM_ADMIN") || !roleCodePattern.MatchString(item.Code) || strings.TrimSpace(item.Name) == "" || !validScopes[strings.ToUpper(item.DataScope)] {
			return fmt.Errorf("role %s is invalid", item.Code)
		}
	}
	defaultPipelines := 0
	for _, pipeline := range bundle.Pipelines {
		if strings.TrimSpace(pipeline.Name) == "" {
			return errors.New("pipeline name is required")
		}
		if pipeline.IsDefault {
			defaultPipelines++
		}
		for _, stage := range pipeline.Stages {
			if strings.TrimSpace(stage.Name) == "" || stage.Order < 1 || stage.Probability < 0 || stage.Probability > 100 {
				return fmt.Errorf("pipeline stage %s#%d is invalid", pipeline.Name, stage.Order)
			}
		}
	}
	if defaultPipelines > 1 {
		return errors.New("configuration bundle can contain only one default pipeline")
	}
	validEntities := map[string]bool{"CUSTOMER": true, "CONTACT": true, "LEAD": true, "OPPORTUNITY": true, "QUOTATION": true, "CONTRACT": true}
	validFieldTypes := map[string]bool{"Text": true, "Textarea": true, "Number": true, "Money": true, "Percent": true, "Date": true, "Datetime": true, "Boolean": true, "Select": true, "Multi Select": true, "User": true, "Organization": true, "URL": true}
	for _, item := range bundle.CustomFields {
		if !validEntities[strings.ToUpper(item.EntityType)] || !bundleNamePattern.MatchString(strings.ToLower(item.Key)) || strings.TrimSpace(item.Label) == "" || !validFieldTypes[item.Type] {
			return fmt.Errorf("custom field %s.%s is invalid", item.EntityType, item.Key)
		}
	}
	for _, item := range bundle.DealHealthRules {
		if !roleCodePattern.MatchString(strings.ToUpper(item.Code)) || item.RiskScore < 0 || item.RiskScore > 100 || strings.TrimSpace(item.Name) == "" {
			return fmt.Errorf("deal health rule %s is invalid", item.Code)
		}
	}
	for _, item := range bundle.ApprovalPolicies {
		if strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.EntityType) == "" || item.ApprovalSteps < 1 {
			return fmt.Errorf("approval policy %s is invalid", item.Name)
		}
	}
	validItemTypes := map[string]bool{"CHECKLIST": true, "ACTION": true, "FIELD": true}
	validCriterionTypes := map[string]bool{"FIELD_PRESENT": true, "DECISION_MAKER": true, "RECENT_ACTIVITY": true, "PLAYBOOK_COMPLETE": true, "CUSTOM_FIELD": true}
	validEnforcement := map[string]bool{"OFF": true, "WARNING": true, "BLOCK": true}
	for _, execution := range bundle.SalesExecution {
		if strings.TrimSpace(execution.PipelineName) == "" || execution.StageOrder < 1 || strings.TrimSpace(execution.Playbook.PlaybookName) == "" {
			return fmt.Errorf("sales execution %s#%d is invalid", execution.PipelineName, execution.StageOrder)
		}
		for _, item := range execution.Playbook.Items {
			if strings.TrimSpace(item.Title) == "" || !validItemTypes[item.ItemType] {
				return fmt.Errorf("sales playbook item %q is invalid", item.Title)
			}
		}
		for _, criterion := range execution.Playbook.Criteria {
			if strings.TrimSpace(criterion.Name) == "" || !validCriterionTypes[criterion.CriterionType] || !validEnforcement[criterion.Enforcement] {
				return fmt.Errorf("stage exit criterion %q is invalid", criterion.Name)
			}
		}
	}
	return nil
}

func sameBundleValue(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

var bundleSectionLabels = map[string]string{
	"settings": "시스템 설정", "roles": "Custom Role · Permission", "pipelines": "Pipeline", "pipelineStages": "Pipeline Stage", "customFields": "Custom Field", "dealHealthRules": "Deal Health Rule", "approvalPolicies": "승인 정책", "salesExecution": "Playbook · Exit Criteria",
}

func diffConfigurationBundles(current, incoming configurationBundle) (bundlePreview, error) {
	currentItems, err := flattenBundle(current)
	if err != nil {
		return bundlePreview{}, err
	}
	incomingItems, err := flattenBundle(incoming)
	if err != nil {
		return bundlePreview{}, err
	}
	existing := map[string]any{}
	for _, item := range currentItems {
		existing[item.Section+":"+item.Key] = item.Value
	}
	sectionMap := map[string]*bundleDiffSection{}
	order := []string{"settings", "roles", "pipelines", "pipelineStages", "customFields", "dealHealthRules", "approvalPolicies", "salesExecution"}
	for _, key := range order {
		sectionMap[key] = &bundleDiffSection{Key: key, Label: bundleSectionLabels[key]}
	}
	changes := []bundleChange{}
	summary := map[string]int{"create": 0, "update": 0, "unchanged": 0, "total": len(incomingItems)}
	for _, item := range incomingItems {
		section := sectionMap[item.Section]
		section.Total++
		before, found := existing[item.Section+":"+item.Key]
		action := "CREATE"
		if found && sameBundleValue(before, item.Value) {
			action = "UNCHANGED"
			section.Unchanged++
			summary["unchanged"]++
		} else if found {
			action = "UPDATE"
			section.Update++
			summary["update"]++
		} else {
			section.Create++
			summary["create"]++
		}
		if action != "UNCHANGED" && len(changes) < 300 {
			changes = append(changes, bundleChange{Section: item.Section, Key: item.Key, Action: action, Before: before, After: item.Value})
		}
	}
	sections := make([]bundleDiffSection, 0, len(order))
	for _, key := range order {
		sections = append(sections, *sectionMap[key])
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Section+changes[i].Key < changes[j].Section+changes[j].Key })
	return bundlePreview{Format: incoming.Format, SourceVersion: incoming.SourceVersion, GeneratedAt: incoming.GeneratedAt, Sections: sections, Changes: changes, Summary: summary, SafeToApply: true, Notices: []string{"Secret, OIDC Client Secret, 사용자와 CRM 업무 데이터는 Bundle 대상이 아닙니다.", "Import는 항목을 삭제하지 않고 논리 Key 기준으로 생성 또는 갱신합니다.", "Playbook Item과 Exit Criteria는 기존 진행 이력을 보존하도록 비파괴 Upsert합니다."}}, nil
}

func (s *Server) previewConfigurationBundle(ctx context.Context, p *auth.Principal, incoming configurationBundle) (bundlePreview, error) {
	if err := validateConfigurationBundle(incoming); err != nil {
		return bundlePreview{}, err
	}
	current, err := s.configurationBundle(ctx, p)
	if err != nil {
		return bundlePreview{}, err
	}
	return diffConfigurationBundles(current, incoming)
}

func jsonDocument(value any) ([]byte, error) {
	if value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(value)
}

func resolveBundleReference(ctx context.Context, tx pgx.Tx, table, code string) (any, error) {
	if strings.TrimSpace(code) == "" {
		return nil, nil
	}
	if table != "roles" && table != "organizations" {
		return nil, errors.New("invalid configuration reference")
	}
	var id string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM `+table+` WHERE code=$1`, code).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("referenced %s code %s does not exist", table, code)
		}
		return nil, err
	}
	return id, nil
}

func (s *Server) applyConfigurationBundle(ctx context.Context, p *auth.Principal, bundle configurationBundle) (bundlePreview, error) {
	preview, err := s.previewConfigurationBundle(ctx, p, bundle)
	if err != nil {
		return bundlePreview{}, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return bundlePreview{}, err
	}
	defer tx.Rollback(ctx)
	for _, item := range bundle.Settings {
		var secret bool
		err = tx.QueryRow(ctx, `SELECT secret_yn FROM system_settings WHERE namespace=$1 AND key=$2 FOR UPDATE`, item.Namespace, item.Key).Scan(&secret)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return bundlePreview{}, err
		}
		if secret {
			return bundlePreview{}, fmt.Errorf("secret setting %s.%s cannot be imported", item.Namespace, item.Key)
		}
		raw, rawErr := jsonDocument(item.Value)
		if rawErr != nil {
			return bundlePreview{}, rawErr
		}
		_, err = tx.Exec(ctx, `INSERT INTO system_settings(namespace,key,value,value_type,secret_yn,restart_required,updated_by,version) VALUES($1,$2,$3,$4,false,$5,$6,1) ON CONFLICT(namespace,key) DO UPDATE SET value=excluded.value,value_type=excluded.value_type,restart_required=excluded.restart_required,updated_by=excluded.updated_by,updated_at=now(),version=system_settings.version+1 WHERE system_settings.value IS DISTINCT FROM excluded.value OR system_settings.value_type IS DISTINCT FROM excluded.value_type OR system_settings.restart_required IS DISTINCT FROM excluded.restart_required`, item.Namespace, item.Key, raw, item.ValueType, item.RestartRequired, p.UserID)
		if err != nil {
			return bundlePreview{}, err
		}
	}
	for _, item := range bundle.Roles {
		var roleID string
		var systemRole bool
		err = tx.QueryRow(ctx, `SELECT id::text,system_role FROM roles WHERE code=$1 FOR UPDATE`, strings.ToUpper(item.Code)).Scan(&roleID, &systemRole)
		if errors.Is(err, pgx.ErrNoRows) {
			roleID = ids.New()
			_, err = tx.Exec(ctx, `INSERT INTO roles(id,code,name,description,data_scope,system_role) VALUES($1,$2,$3,$4,$5,false)`, roleID, strings.ToUpper(item.Code), strings.TrimSpace(item.Name), nullAdmin(item.Description), strings.ToUpper(item.DataScope))
		} else if err == nil {
			if systemRole {
				return bundlePreview{}, fmt.Errorf("system role %s cannot be imported", item.Code)
			}
			_, err = tx.Exec(ctx, `UPDATE roles SET name=$1,description=$2,data_scope=$3,updated_at=now() WHERE id=$4`, strings.TrimSpace(item.Name), nullAdmin(item.Description), strings.ToUpper(item.DataScope), roleID)
		}
		if err != nil {
			return bundlePreview{}, err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id=$1`, roleID); err != nil {
			return bundlePreview{}, err
		}
		seenPermissions := map[string]bool{}
		for _, permission := range item.Permissions {
			permission = strings.TrimSpace(permission)
			if permission == "" || seenPermissions[permission] {
				continue
			}
			seenPermissions[permission] = true
			if _, err = tx.Exec(ctx, `INSERT INTO role_permissions(role_id,permission) VALUES($1,$2)`, roleID, permission); err != nil {
				return bundlePreview{}, err
			}
		}
	}
	stageIDs := map[string]string{}
	for _, pipeline := range bundle.Pipelines {
		var pipelineID string
		err = tx.QueryRow(ctx, `SELECT id::text FROM pipelines WHERE name=$1 ORDER BY is_default DESC,id LIMIT 1`, pipeline.Name).Scan(&pipelineID)
		if errors.Is(err, pgx.ErrNoRows) {
			pipelineID = ids.New()
			_, err = tx.Exec(ctx, `INSERT INTO pipelines(id,name,active,is_default) VALUES($1,$2,$3,$4)`, pipelineID, pipeline.Name, pipeline.Active, pipeline.IsDefault)
		} else if err == nil {
			_, err = tx.Exec(ctx, `UPDATE pipelines SET active=$1,is_default=$2,updated_at=now() WHERE id=$3`, pipeline.Active, pipeline.IsDefault, pipelineID)
		}
		if err != nil {
			return bundlePreview{}, err
		}
		if pipeline.IsDefault {
			if _, err = tx.Exec(ctx, `UPDATE pipelines SET is_default=false,updated_at=now() WHERE id<>$1 AND is_default`, pipelineID); err != nil {
				return bundlePreview{}, err
			}
		}
		for _, stage := range pipeline.Stages {
			var stageID string
			err = tx.QueryRow(ctx, `SELECT id::text FROM pipeline_stages WHERE pipeline_id=$1 AND stage_order=$2`, pipelineID, stage.Order).Scan(&stageID)
			if errors.Is(err, pgx.ErrNoRows) {
				stageID = ids.New()
				_, err = tx.Exec(ctx, `INSERT INTO pipeline_stages(id,pipeline_id,name,stage_order,probability,forecast_category,is_won,is_lost,active,color,min_days,max_days) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, stageID, pipelineID, stage.Name, stage.Order, stage.Probability, stage.ForecastCategory, stage.IsWon, stage.IsLost, stage.Active, stage.Color, stage.MinDays, stage.MaxDays)
			} else if err == nil {
				_, err = tx.Exec(ctx, `UPDATE pipeline_stages SET name=$1,probability=$2,forecast_category=$3,is_won=$4,is_lost=$5,active=$6,color=$7,min_days=$8,max_days=$9 WHERE id=$10`, stage.Name, stage.Probability, stage.ForecastCategory, stage.IsWon, stage.IsLost, stage.Active, stage.Color, stage.MinDays, stage.MaxDays, stageID)
			}
			if err != nil {
				return bundlePreview{}, err
			}
			stageIDs[fmt.Sprintf("%s#%d", pipeline.Name, stage.Order)] = stageID
		}
	}
	for _, item := range bundle.CustomFields {
		raw, rawErr := jsonDocument(item.Options)
		if rawErr != nil {
			return bundlePreview{}, rawErr
		}
		_, err = tx.Exec(ctx, `INSERT INTO custom_field_definitions(id,entity_type,field_key,label,field_type,required,options,active,display_order,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(entity_type,field_key) DO UPDATE SET label=excluded.label,field_type=excluded.field_type,required=excluded.required,options=excluded.options,active=excluded.active,display_order=excluded.display_order,updated_at=now()`, ids.New(), strings.ToUpper(item.EntityType), strings.ToLower(item.Key), item.Label, item.Type, item.Required, raw, item.Active, item.DisplayOrder, p.UserID)
		if err != nil {
			return bundlePreview{}, err
		}
	}
	for _, item := range bundle.DealHealthRules {
		raw, rawErr := jsonDocument(item.Threshold)
		if rawErr != nil {
			return bundlePreview{}, rawErr
		}
		_, err = tx.Exec(ctx, `INSERT INTO deal_health_rules(id,code,name,description,rule_type,threshold,risk_score,recommended_action,active,priority,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11) ON CONFLICT(code) DO UPDATE SET name=excluded.name,description=excluded.description,rule_type=excluded.rule_type,threshold=excluded.threshold,risk_score=excluded.risk_score,recommended_action=excluded.recommended_action,active=excluded.active,priority=excluded.priority,updated_by=excluded.updated_by,updated_at=now(),version=deal_health_rules.version+1 WHERE (deal_health_rules.name,deal_health_rules.description,deal_health_rules.rule_type,deal_health_rules.threshold,deal_health_rules.risk_score,deal_health_rules.recommended_action,deal_health_rules.active,deal_health_rules.priority) IS DISTINCT FROM (excluded.name,excluded.description,excluded.rule_type,excluded.threshold,excluded.risk_score,excluded.recommended_action,excluded.active,excluded.priority)`, ids.New(), strings.ToUpper(item.Code), item.Name, nullAdmin(item.Description), item.RuleType, raw, item.RiskScore, item.RecommendedAction, item.Active, item.Priority, p.UserID)
		if err != nil {
			return bundlePreview{}, err
		}
	}
	for _, item := range bundle.ApprovalPolicies {
		roleID, refErr := resolveBundleReference(ctx, tx, "roles", item.ApproverRoleCode)
		if refErr != nil {
			return bundlePreview{}, refErr
		}
		orgID, refErr := resolveBundleReference(ctx, tx, "organizations", item.ApproverOrganizationCode)
		if refErr != nil {
			return bundlePreview{}, refErr
		}
		condition, rawErr := jsonDocument(item.ConditionValue)
		if rawErr != nil {
			return bundlePreview{}, rawErr
		}
		var policyID string
		err = tx.QueryRow(ctx, `SELECT id::text FROM approval_policies WHERE name=$1 AND entity_type=$2 ORDER BY id LIMIT 1`, item.Name, strings.ToUpper(item.EntityType)).Scan(&policyID)
		if errors.Is(err, pgx.ErrNoRows) {
			policyID = ids.New()
			_, err = tx.Exec(ctx, `INSERT INTO approval_policies(id,name,entity_type,condition_field,condition_operator,condition_value,approver_method,approver_role_id,approver_org_id,approval_steps,allow_reject,allow_resubmit,allow_delegate,active,priority,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16)`, policyID, item.Name, strings.ToUpper(item.EntityType), nullAdmin(item.ConditionField), nullAdmin(item.ConditionOperator), condition, item.ApproverMethod, roleID, orgID, item.ApprovalSteps, item.AllowReject, item.AllowResubmit, item.AllowDelegate, item.Active, item.Priority, p.UserID)
		} else if err == nil {
			_, err = tx.Exec(ctx, `UPDATE approval_policies SET condition_field=$1,condition_operator=$2,condition_value=$3,approver_method=$4,approver_role_id=$5,approver_org_id=$6,approval_steps=$7,allow_reject=$8,allow_resubmit=$9,allow_delegate=$10,active=$11,priority=$12,updated_by=$13,updated_at=now() WHERE id=$14`, nullAdmin(item.ConditionField), nullAdmin(item.ConditionOperator), condition, item.ApproverMethod, roleID, orgID, item.ApprovalSteps, item.AllowReject, item.AllowResubmit, item.AllowDelegate, item.Active, item.Priority, p.UserID, policyID)
		}
		if err != nil {
			return bundlePreview{}, err
		}
	}
	for _, item := range bundle.SalesExecution {
		stageKey := fmt.Sprintf("%s#%d", item.PipelineName, item.StageOrder)
		stageID := stageIDs[stageKey]
		if stageID == "" {
			err = tx.QueryRow(ctx, `SELECT s.id::text FROM pipeline_stages s JOIN pipelines p ON p.id=s.pipeline_id WHERE p.name=$1 AND s.stage_order=$2 ORDER BY p.is_default DESC,p.id LIMIT 1`, item.PipelineName, item.StageOrder).Scan(&stageID)
			if err != nil {
				return bundlePreview{}, fmt.Errorf("sales execution stage %s does not exist", stageKey)
			}
		}
		if err = upsertBundleSalesExecution(ctx, tx, p.UserID, stageID, item.Playbook); err != nil {
			return bundlePreview{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return bundlePreview{}, err
	}
	return preview, nil
}

func upsertBundleSalesExecution(ctx context.Context, tx pgx.Tx, userID, stageID string, input intelligence.StageExecutionInput) error {
	var playbookID string
	err := tx.QueryRow(ctx, `SELECT id::text FROM sales_playbooks WHERE stage_id=$1`, stageID).Scan(&playbookID)
	if errors.Is(err, pgx.ErrNoRows) {
		playbookID = ids.New()
		_, err = tx.Exec(ctx, `INSERT INTO sales_playbooks(id,stage_id,name,guidance,active,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$6)`, playbookID, stageID, input.PlaybookName, input.Guidance, input.Active, userID)
	} else if err == nil {
		_, err = tx.Exec(ctx, `UPDATE sales_playbooks SET name=$1,guidance=$2,active=$3,updated_by=$4,updated_at=now() WHERE id=$5`, input.PlaybookName, input.Guidance, input.Active, userID, playbookID)
	}
	if err != nil {
		return err
	}
	for _, item := range input.Items {
		var itemID string
		err = tx.QueryRow(ctx, `SELECT id::text FROM sales_playbook_items WHERE playbook_id=$1 AND title=$2 ORDER BY display_order,id LIMIT 1`, playbookID, item.Title).Scan(&itemID)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `INSERT INTO sales_playbook_items(id,playbook_id,title,description,item_type,field_key,required,display_order) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, ids.New(), playbookID, item.Title, nullAdmin(item.Description), item.ItemType, nullAdmin(item.FieldKey), item.Required, item.DisplayOrder)
		} else if err == nil {
			_, err = tx.Exec(ctx, `UPDATE sales_playbook_items SET description=$1,item_type=$2,field_key=$3,required=$4,display_order=$5 WHERE id=$6`, nullAdmin(item.Description), item.ItemType, nullAdmin(item.FieldKey), item.Required, item.DisplayOrder, itemID)
		}
		if err != nil {
			return err
		}
	}
	for _, item := range input.Criteria {
		expected, rawErr := jsonDocument(item.ExpectedValue)
		if rawErr != nil {
			return rawErr
		}
		var criterionID string
		err = tx.QueryRow(ctx, `SELECT id::text FROM stage_exit_criteria WHERE stage_id=$1 AND name=$2 ORDER BY display_order,id LIMIT 1`, stageID, item.Name).Scan(&criterionID)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `INSERT INTO stage_exit_criteria(id,stage_id,name,criterion_type,field_key,operator,expected_value,enforcement,message,active,display_order,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)`, ids.New(), stageID, item.Name, item.CriterionType, nullAdmin(item.FieldKey), item.Operator, expected, item.Enforcement, nullAdmin(item.Message), item.Active, item.DisplayOrder, userID)
		} else if err == nil {
			_, err = tx.Exec(ctx, `UPDATE stage_exit_criteria SET criterion_type=$1,field_key=$2,operator=$3,expected_value=$4,enforcement=$5,message=$6,active=$7,display_order=$8,updated_by=$9,updated_at=now() WHERE id=$10`, item.CriterionType, nullAdmin(item.FieldKey), item.Operator, expected, item.Enforcement, nullAdmin(item.Message), item.Active, item.DisplayOrder, userID, criterionID)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) exportConfiguration(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := requireAdmin(p, false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	bundle, err := s.configurationBundle(r.Context(), p)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "CONFIGURATION_BUNDLE_EXPORT", Resource: "configuration", After: map[string]any{"format": bundle.Format, "sourceVersion": bundle.SourceVersion, "containsSecrets": false}, IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="relio-config-`+time.Now().UTC().Format("20060102T150405Z")+`.json"`)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(bundle)
}

func (s *Server) previewConfiguration(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := requireAdmin(p, false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	var bundle configurationBundle
	if !httpx.DecodeJSON(w, r, &bundle) {
		return
	}
	preview, err := s.previewConfigurationBundle(r.Context(), p, bundle)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, preview)
}

func (s *Server) applyConfiguration(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := requireAdmin(p, true); err != nil {
		s.serviceError(w, r, err)
		return
	}
	var input struct {
		Confirmation string              `json:"confirmation"`
		Bundle       configurationBundle `json:"bundle"`
	}
	if !httpx.DecodeJSON(w, r, &input) {
		return
	}
	if input.Confirmation != "APPLY" {
		s.serviceError(w, r, errors.New("confirmation must be APPLY"))
		return
	}
	preview, err := s.applyConfigurationBundle(r.Context(), p, input.Bundle)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "CONFIGURATION_BUNDLE_APPLY", Resource: "configuration", After: map[string]any{"format": input.Bundle.Format, "sourceVersion": input.Bundle.SourceVersion, "summary": preview.Summary}, IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
	httpx.JSON(w, http.StatusOK, map[string]any{"applied": true, "preview": preview})
}
