package intelligence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5"
)

func (s *Service) Playbook(ctx context.Context, p *auth.Principal, opportunityID string) (Playbook, error) {
	opp, err := s.CRM.GetOpportunity(ctx, p, opportunityID)
	if err != nil {
		return Playbook{}, err
	}
	out := Playbook{StageID: opp.StageID, StageName: opp.StageName, Items: []PlaybookItem{}}
	err = s.DB.QueryRow(ctx, `SELECT id,name,COALESCE(guidance,''),active FROM sales_playbooks WHERE stage_id=$1 AND active=true`, opp.StageID).Scan(&out.ID, &out.Name, &out.Guidance, &out.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return Playbook{}, err
	}
	rows, err := s.DB.Query(ctx, `SELECT i.id,i.title,COALESCE(i.description,''),i.item_type,COALESCE(i.field_key,''),i.required,i.display_order,COALESCE(p.completed,false),COALESCE(p.notes,''),COALESCE(u.display_name,''),p.completed_at FROM sales_playbook_items i LEFT JOIN opportunity_playbook_progress p ON p.item_id=i.id AND p.opportunity_id=$1 LEFT JOIN users u ON u.id=p.completed_by WHERE i.playbook_id=$2 ORDER BY i.display_order,i.id`, opportunityID, out.ID)
	if err != nil {
		return Playbook{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item PlaybookItem
		if err = rows.Scan(&item.ID, &item.Title, &item.Description, &item.ItemType, &item.FieldKey, &item.Required, &item.DisplayOrder, &item.Completed, &item.Notes, &item.CompletedBy, &item.CompletedAt); err != nil {
			return Playbook{}, err
		}
		if item.Completed {
			out.Completed++
		}
		if item.Required {
			out.RequiredTotal++
			if item.Completed {
				out.RequiredDone++
			}
		}
		out.Items = append(out.Items, item)
	}
	return out, rows.Err()
}

func (s *Service) SetPlaybookProgress(ctx context.Context, p *auth.Principal, opportunityID, itemID string, completed bool, notes string, meta crm.RequestMeta) (Playbook, error) {
	if err := auth.Require(p, "opportunity:write"); err != nil {
		return Playbook{}, err
	}
	playbook, err := s.Playbook(ctx, p, opportunityID)
	if err != nil {
		return Playbook{}, err
	}
	valid := false
	for _, item := range playbook.Items {
		if item.ID == itemID {
			valid = true
			break
		}
	}
	if !valid {
		return Playbook{}, errors.New("playbook item not found for the current stage")
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO opportunity_playbook_progress(opportunity_id,item_id,completed,notes,completed_by,completed_at,updated_at) VALUES($1,$2,$3,$4,$5,CASE WHEN $3 THEN now() ELSE NULL END,now()) ON CONFLICT(opportunity_id,item_id) DO UPDATE SET completed=excluded.completed,notes=excluded.notes,completed_by=excluded.completed_by,completed_at=excluded.completed_at,updated_at=now()`, opportunityID, itemID, completed, strings.TrimSpace(notes), p.UserID)
	if err != nil {
		return Playbook{}, err
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: meta.Channel, Action: "PLAYBOOK_PROGRESS", Resource: "opportunity", ResourceID: opportunityID, After: map[string]any{"itemId": itemID, "completed": completed, "notes": notes}, IP: meta.IP, RequestID: meta.RequestID, UserAgent: meta.UserAgent})
	return s.Playbook(ctx, p, opportunityID)
}

func present(value any) bool {
	if value == nil {
		return false
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v) != ""
	case float64:
		return v != 0
	case int:
		return v != 0
	case bool:
		return v
	default:
		return true
	}
}

func opportunityField(opp crm.Opportunity, key string) any {
	switch key {
	case "name":
		return opp.Name
	case "customerId", "customer_id":
		return opp.CustomerID
	case "expectedAmount", "expected_amount":
		return opp.BaseExpectedAmount
	case "expectedCloseDate", "expected_close_date":
		return opp.ExpectedCloseDate
	case "nextAction", "next_action":
		return opp.NextAction
	case "nextActionDate", "next_action_date":
		return opp.NextActionDate
	case "competitor":
		return opp.Competitor
	case "forecastCategory", "forecast_category":
		return opp.ForecastCategory
	}
	return opp.CustomFields[key]
}

func (s *Service) ValidateStageTransition(ctx context.Context, p *auth.Principal, opportunityID, targetStageID string) (crm.StageGateResult, error) {
	opp, err := s.CRM.GetOpportunity(ctx, p, opportunityID)
	if err != nil {
		return crm.StageGateResult{}, err
	}
	var validTarget bool
	if targetStageID != "" {
		err = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pipeline_stages WHERE id=$1 AND pipeline_id=$2 AND active=true)`, targetStageID, opp.PipelineID).Scan(&validTarget)
	}
	if err != nil {
		return crm.StageGateResult{}, err
	}
	if targetStageID == "" || !validTarget {
		return crm.StageGateResult{}, errors.New("target stage is not active in the opportunity pipeline")
	}
	result := crm.StageGateResult{Allowed: true, Blocked: []crm.StageGateIssue{}, Warnings: []crm.StageGateIssue{}}
	rows, err := s.DB.Query(ctx, `SELECT id,name,criterion_type,COALESCE(field_key,''),operator,expected_value,enforcement,COALESCE(message,'') FROM stage_exit_criteria WHERE stage_id=$1 AND active=true AND enforcement<>'OFF' ORDER BY display_order,id`, opp.StageID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, criterionType, fieldKey, operator, enforcement, message string
		var expectedRaw []byte
		if err = rows.Scan(&id, &name, &criterionType, &fieldKey, &operator, &expectedRaw, &enforcement, &message); err != nil {
			return result, err
		}
		var expected any
		_ = json.Unmarshal(expectedRaw, &expected)
		met := false
		switch criterionType {
		case "FIELD_PRESENT":
			met = present(opportunityField(opp, fieldKey))
		case "CUSTOM_FIELD":
			met = present(opp.CustomFields[fieldKey])
		case "DECISION_MAKER":
			var count int
			_ = s.DB.QueryRow(ctx, `SELECT count(*) FROM contacts WHERE customer_id=$1 AND decision_maker=true`, opp.CustomerID).Scan(&count)
			met = count > 0
		case "RECENT_ACTIVITY":
			days := 30.0
			if values, ok := expected.(map[string]any); ok {
				days = thresholdNumber(values, "days", 30)
			}
			met = opp.LastActivityAt != nil && time.Since(*opp.LastActivityAt) <= time.Duration(days*24)*time.Hour
		case "PLAYBOOK_COMPLETE":
			playbook, playbookErr := s.Playbook(ctx, p, opportunityID)
			if playbookErr != nil {
				return result, playbookErr
			}
			met = playbook.RequiredTotal == playbook.RequiredDone
		}
		if operator == "NOT_PRESENT" {
			met = !met
		}
		if met {
			continue
		}
		if message == "" {
			message = fmt.Sprintf("%s 조건이 충족되지 않았습니다.", name)
		}
		issue := crm.StageGateIssue{CriterionID: id, Name: name, Enforcement: enforcement, Message: message}
		if enforcement == "BLOCK" {
			result.Blocked = append(result.Blocked, issue)
			result.Allowed = false
		} else {
			result.Warnings = append(result.Warnings, issue)
		}
	}
	return result, rows.Err()
}

func (s *Service) AdminStageExecutions(ctx context.Context, p *auth.Principal) ([]StageExecution, error) {
	if err := auth.Require(p, "admin:read"); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT p.id,p.name,s.id,s.name,s.stage_order FROM pipelines p JOIN pipeline_stages s ON s.pipeline_id=p.id ORDER BY p.is_default DESC,p.name,s.stage_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StageExecution{}
	for rows.Next() {
		var item StageExecution
		if err = rows.Scan(&item.PipelineID, &item.PipelineName, &item.StageID, &item.StageName, &item.StageOrder); err != nil {
			return nil, err
		}
		item.Playbook, err = s.stagePlaybook(ctx, item.StageID, item.StageName)
		if err != nil {
			return nil, err
		}
		item.Criteria, err = s.stageCriteria(ctx, item.StageID)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) stagePlaybook(ctx context.Context, stageID, stageName string) (Playbook, error) {
	out := Playbook{StageID: stageID, StageName: stageName, Items: []PlaybookItem{}}
	err := s.DB.QueryRow(ctx, `SELECT id,name,COALESCE(guidance,''),active FROM sales_playbooks WHERE stage_id=$1`, stageID).Scan(&out.ID, &out.Name, &out.Guidance, &out.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	rows, err := s.DB.Query(ctx, `SELECT id,title,COALESCE(description,''),item_type,COALESCE(field_key,''),required,display_order FROM sales_playbook_items WHERE playbook_id=$1 ORDER BY display_order,id`, out.ID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item PlaybookItem
		if err = rows.Scan(&item.ID, &item.Title, &item.Description, &item.ItemType, &item.FieldKey, &item.Required, &item.DisplayOrder); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	return out, rows.Err()
}

func (s *Service) stageCriteria(ctx context.Context, stageID string) ([]CriterionInput, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,name,criterion_type,COALESCE(field_key,''),operator,expected_value,enforcement,COALESCE(message,''),active,display_order FROM stage_exit_criteria WHERE stage_id=$1 ORDER BY display_order,id`, stageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CriterionInput{}
	for rows.Next() {
		var item CriterionInput
		var raw []byte
		if err = rows.Scan(&item.ID, &item.Name, &item.CriterionType, &item.FieldKey, &item.Operator, &raw, &item.Enforcement, &item.Message, &item.Active, &item.DisplayOrder); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &item.ExpectedValue)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) SaveStageExecution(ctx context.Context, p *auth.Principal, stageID string, input StageExecutionInput, meta crm.RequestMeta) (StageExecution, error) {
	if err := auth.Require(p, "admin:write"); err != nil {
		return StageExecution{}, err
	}
	var pipelineID, pipelineName, stageName string
	var order int
	if err := s.DB.QueryRow(ctx, `SELECT p.id,p.name,s.name,s.stage_order FROM pipeline_stages s JOIN pipelines p ON p.id=s.pipeline_id WHERE s.id=$1`, stageID).Scan(&pipelineID, &pipelineName, &stageName, &order); err != nil {
		return StageExecution{}, errors.New("stage not found")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return StageExecution{}, err
	}
	defer tx.Rollback(ctx)
	var playbookID string
	err = tx.QueryRow(ctx, `SELECT id FROM sales_playbooks WHERE stage_id=$1`, stageID).Scan(&playbookID)
	if errors.Is(err, pgx.ErrNoRows) {
		playbookID = ids.New()
		_, err = tx.Exec(ctx, `INSERT INTO sales_playbooks(id,stage_id,name,guidance,active,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$6)`, playbookID, stageID, strings.TrimSpace(input.PlaybookName), strings.TrimSpace(input.Guidance), input.Active, p.UserID)
	} else if err == nil {
		_, err = tx.Exec(ctx, `UPDATE sales_playbooks SET name=$1,guidance=$2,active=$3,updated_by=$4,updated_at=now() WHERE id=$5`, strings.TrimSpace(input.PlaybookName), strings.TrimSpace(input.Guidance), input.Active, p.UserID, playbookID)
	}
	if err != nil {
		return StageExecution{}, err
	}
	existingItems := map[string]bool{}
	itemRows, queryErr := tx.Query(ctx, `SELECT id FROM sales_playbook_items WHERE playbook_id=$1`, playbookID)
	if queryErr != nil {
		return StageExecution{}, queryErr
	}
	for itemRows.Next() {
		var id string
		if queryErr = itemRows.Scan(&id); queryErr != nil {
			itemRows.Close()
			return StageExecution{}, queryErr
		}
		existingItems[id] = true
	}
	queryErr = itemRows.Err()
	itemRows.Close()
	if queryErr != nil {
		return StageExecution{}, queryErr
	}
	for i, item := range input.Items {
		if strings.TrimSpace(item.Title) == "" {
			return StageExecution{}, errors.New("playbook item title is required")
		}
		if item.ItemType == "" {
			item.ItemType = "CHECKLIST"
		}
		if item.DisplayOrder == 0 {
			item.DisplayOrder = (i + 1) * 10
		}
		fieldKey := func() any {
			if item.FieldKey == "" {
				return nil
			}
			return item.FieldKey
		}()
		if item.ID == "" {
			_, err = tx.Exec(ctx, `INSERT INTO sales_playbook_items(id,playbook_id,title,description,item_type,field_key,required,display_order) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, ids.New(), playbookID, strings.TrimSpace(item.Title), strings.TrimSpace(item.Description), item.ItemType, fieldKey, item.Required, item.DisplayOrder)
		} else if existingItems[item.ID] {
			delete(existingItems, item.ID)
			_, err = tx.Exec(ctx, `UPDATE sales_playbook_items SET title=$1,description=$2,item_type=$3,field_key=$4,required=$5,display_order=$6 WHERE id=$7 AND playbook_id=$8`, strings.TrimSpace(item.Title), strings.TrimSpace(item.Description), item.ItemType, fieldKey, item.Required, item.DisplayOrder, item.ID, playbookID)
		} else {
			return StageExecution{}, errors.New("playbook item does not belong to this stage")
		}
		if err != nil {
			return StageExecution{}, err
		}
	}
	for itemID := range existingItems {
		if _, err = tx.Exec(ctx, `DELETE FROM sales_playbook_items WHERE id=$1 AND playbook_id=$2`, itemID, playbookID); err != nil {
			return StageExecution{}, err
		}
	}
	existingCriteria := map[string]bool{}
	criterionRows, queryErr := tx.Query(ctx, `SELECT id FROM stage_exit_criteria WHERE stage_id=$1`, stageID)
	if queryErr != nil {
		return StageExecution{}, queryErr
	}
	for criterionRows.Next() {
		var id string
		if queryErr = criterionRows.Scan(&id); queryErr != nil {
			criterionRows.Close()
			return StageExecution{}, queryErr
		}
		existingCriteria[id] = true
	}
	queryErr = criterionRows.Err()
	criterionRows.Close()
	if queryErr != nil {
		return StageExecution{}, queryErr
	}
	for i, item := range input.Criteria {
		if strings.TrimSpace(item.Name) == "" {
			return StageExecution{}, errors.New("criterion name is required")
		}
		if item.CriterionType == "" {
			item.CriterionType = "FIELD_PRESENT"
		}
		if item.Enforcement == "" {
			item.Enforcement = "WARNING"
		}
		if item.Operator == "" {
			item.Operator = "PRESENT"
		}
		if item.DisplayOrder == 0 {
			item.DisplayOrder = (i + 1) * 10
		}
		expected, _ := json.Marshal(item.ExpectedValue)
		fieldKey := func() any {
			if item.FieldKey == "" {
				return nil
			}
			return item.FieldKey
		}()
		message := func() any {
			if item.Message == "" {
				return nil
			}
			return item.Message
		}()
		if item.ID == "" {
			_, err = tx.Exec(ctx, `INSERT INTO stage_exit_criteria(id,stage_id,name,criterion_type,field_key,operator,expected_value,enforcement,message,active,display_order,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)`, ids.New(), stageID, strings.TrimSpace(item.Name), item.CriterionType, fieldKey, item.Operator, expected, item.Enforcement, message, item.Active, item.DisplayOrder, p.UserID)
		} else if existingCriteria[item.ID] {
			delete(existingCriteria, item.ID)
			_, err = tx.Exec(ctx, `UPDATE stage_exit_criteria SET name=$1,criterion_type=$2,field_key=$3,operator=$4,expected_value=$5,enforcement=$6,message=$7,active=$8,display_order=$9,updated_by=$10,updated_at=now() WHERE id=$11 AND stage_id=$12`, strings.TrimSpace(item.Name), item.CriterionType, fieldKey, item.Operator, expected, item.Enforcement, message, item.Active, item.DisplayOrder, p.UserID, item.ID, stageID)
		} else {
			return StageExecution{}, errors.New("exit criterion does not belong to this stage")
		}
		if err != nil {
			return StageExecution{}, err
		}
	}
	for criterionID := range existingCriteria {
		if _, err = tx.Exec(ctx, `DELETE FROM stage_exit_criteria WHERE id=$1 AND stage_id=$2`, criterionID, stageID); err != nil {
			return StageExecution{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return StageExecution{}, err
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "UPDATE_SALES_EXECUTION", Resource: "pipeline_stage", ResourceID: stageID, After: input, IP: meta.IP, RequestID: meta.RequestID, UserAgent: meta.UserAgent})
	playbook, err := s.stagePlaybook(ctx, stageID, stageName)
	if err != nil {
		return StageExecution{}, err
	}
	criteria, err := s.stageCriteria(ctx, stageID)
	if err != nil {
		return StageExecution{}, err
	}
	return StageExecution{PipelineID: pipelineID, PipelineName: pipelineName, StageID: stageID, StageName: stageName, StageOrder: order, Playbook: playbook, Criteria: criteria}, nil
}
