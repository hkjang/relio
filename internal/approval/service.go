package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Policy struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	EntityType        string    `json:"entityType"`
	ConditionField    string    `json:"conditionField,omitempty"`
	ConditionOperator string    `json:"conditionOperator,omitempty"`
	ConditionValue    any       `json:"conditionValue,omitempty"`
	ApproverMethod    string    `json:"approverMethod"`
	ApproverRoleID    string    `json:"approverRoleId,omitempty"`
	ApproverOrgID     string    `json:"approverOrgId,omitempty"`
	ApprovalSteps     int       `json:"approvalSteps"`
	AllowReject       bool      `json:"allowReject"`
	AllowResubmit     bool      `json:"allowResubmit"`
	AllowDelegate     bool      `json:"allowDelegate"`
	Active            bool      `json:"active"`
	Priority          int       `json:"priority"`
	CreatedAt         time.Time `json:"createdAt"`
}
type Request struct {
	ID            string         `json:"id"`
	PolicyID      string         `json:"policyId"`
	PolicyName    string         `json:"policyName"`
	EntityType    string         `json:"entityType"`
	EntityID      string         `json:"entityId"`
	RequesterID   string         `json:"requesterId"`
	RequesterName string         `json:"requesterName"`
	ApproverID    string         `json:"approverId,omitempty"`
	ApproverName  string         `json:"approverName,omitempty"`
	Status        string         `json:"status"`
	CurrentStep   int            `json:"currentStep"`
	Snapshot      map[string]any `json:"snapshot"`
	Reason        string         `json:"reason,omitempty"`
	RequestedAt   time.Time      `json:"requestedAt"`
	DecidedAt     *time.Time     `json:"decidedAt,omitempty"`
	Version       int            `json:"version"`
}
type Service struct {
	DB    *pgxpool.Pool
	Audit *audit.Service
}

func (s *Service) Policies(ctx context.Context, p *auth.Principal, entity string) ([]Policy, error) {
	if err := auth.Require(p, "admin:read"); err != nil {
		return nil, err
	}
	query := `SELECT id,name,entity_type,COALESCE(condition_field,''),COALESCE(condition_operator,''),condition_value,approver_method,COALESCE(approver_role_id::text,''),COALESCE(approver_org_id::text,''),approval_steps,allow_reject,allow_resubmit,allow_delegate,active,priority,created_at FROM approval_policies`
	args := []any{}
	if entity != "" {
		query += ` WHERE entity_type=$1`
		args = append(args, entity)
	}
	query += ` ORDER BY priority,name`
	rows, err := s.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Policy{}
	for rows.Next() {
		var x Policy
		var raw []byte
		if err = rows.Scan(&x.ID, &x.Name, &x.EntityType, &x.ConditionField, &x.ConditionOperator, &raw, &x.ApproverMethod, &x.ApproverRoleID, &x.ApproverOrgID, &x.ApprovalSteps, &x.AllowReject, &x.AllowResubmit, &x.AllowDelegate, &x.Active, &x.Priority, &x.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &x.ConditionValue)
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) SavePolicy(ctx context.Context, p *auth.Principal, in Policy, ip, requestID, ua string) (Policy, error) {
	if err := auth.Require(p, "admin:write"); err != nil {
		return Policy{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	in.EntityType = strings.ToUpper(strings.TrimSpace(in.EntityType))
	if in.Name == "" || !allowedEntity(in.EntityType) {
		return Policy{}, errors.New("name and a supported entityType are required")
	}
	if in.ApproverMethod == "" {
		in.ApproverMethod = "MANAGER"
	}
	if in.ApprovalSteps < 1 {
		in.ApprovalSteps = 1
	}
	if in.Priority == 0 {
		in.Priority = 100
	}
	raw, _ := json.Marshal(in.ConditionValue)
	id := in.ID
	if id == "" {
		id = ids.New()
		_, err := s.DB.Exec(ctx, `INSERT INTO approval_policies(id,name,entity_type,condition_field,condition_operator,condition_value,approver_method,approver_role_id,approver_org_id,approval_steps,allow_reject,allow_resubmit,allow_delegate,active,priority,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16)`, id, in.Name, in.EntityType, nullable(in.ConditionField), nullable(in.ConditionOperator), raw, in.ApproverMethod, nullable(in.ApproverRoleID), nullable(in.ApproverOrgID), in.ApprovalSteps, in.AllowReject, in.AllowResubmit, in.AllowDelegate, in.Active, in.Priority, p.UserID)
		if err != nil {
			return Policy{}, err
		}
	} else {
		_, err := s.DB.Exec(ctx, `UPDATE approval_policies SET name=$2,entity_type=$3,condition_field=$4,condition_operator=$5,condition_value=$6,approver_method=$7,approver_role_id=$8,approver_org_id=$9,approval_steps=$10,allow_reject=$11,allow_resubmit=$12,allow_delegate=$13,active=$14,priority=$15,updated_by=$16,updated_at=now() WHERE id=$1`, id, in.Name, in.EntityType, nullable(in.ConditionField), nullable(in.ConditionOperator), raw, in.ApproverMethod, nullable(in.ApproverRoleID), nullable(in.ApproverOrgID), in.ApprovalSteps, in.AllowReject, in.AllowResubmit, in.AllowDelegate, in.Active, in.Priority, p.UserID)
		if err != nil {
			return Policy{}, err
		}
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "APPROVAL_POLICY_SAVE", Resource: "approval_policy", ResourceID: id, After: in, IP: ip, RequestID: requestID, UserAgent: ua})
	items, err := s.Policies(ctx, p, in.EntityType)
	if err != nil {
		return Policy{}, err
	}
	for _, v := range items {
		if v.ID == id {
			return v, nil
		}
	}
	return Policy{}, errors.New("saved policy not found")
}
func allowedEntity(v string) bool {
	switch v {
	case "OPPORTUNITY", "QUOTATION", "CONTRACT", "CUSTOMER":
		return true
	}
	return false
}
func nullable(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return strings.TrimSpace(v)
}

func (s *Service) snapshot(ctx context.Context, entity, id string) (map[string]any, error) {
	var raw []byte
	switch entity {
	case "OPPORTUNITY":
		err := s.DB.QueryRow(ctx, `SELECT jsonb_build_object('id',id,'name',name,'expected_amount',expected_amount,'status',status,'stage_id',stage_id,'owner_id',owner_id,'customer_id',customer_id) FROM opportunities WHERE id=$1`, id).Scan(&raw)
		if err != nil {
			return nil, err
		}
	case "CUSTOMER":
		err := s.DB.QueryRow(ctx, `SELECT jsonb_build_object('id',id,'name',name,'customer_type',customer_type,'grade',grade,'owner_id',owner_id) FROM customers WHERE id=$1`, id).Scan(&raw)
		if err != nil {
			return nil, err
		}
	case "QUOTATION":
		err := s.DB.QueryRow(ctx, `SELECT jsonb_build_object('id',id,'title',title,'amount',amount,'discount_percent',discount_percent,'status',status,'owner_id',owner_id) FROM quotations WHERE id=$1`, id).Scan(&raw)
		if err != nil {
			return nil, err
		}
	case "CONTRACT":
		err := s.DB.QueryRow(ctx, `SELECT jsonb_build_object('id',id,'title',title,'amount',amount,'status',status,'owner_id',owner_id) FROM contracts WHERE id=$1`, id).Scan(&raw)
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unsupported entity type")
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *Service) applicable(ctx context.Context, entity, id string) (*Policy, map[string]any, error) {
	entity = strings.ToUpper(entity)
	snapshot, err := s.snapshot(ctx, entity, id)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT id,name,entity_type,COALESCE(condition_field,''),COALESCE(condition_operator,''),condition_value,approver_method,COALESCE(approver_role_id::text,''),COALESCE(approver_org_id::text,''),approval_steps,allow_reject,allow_resubmit,allow_delegate,active,priority,created_at FROM approval_policies WHERE active=true AND entity_type=$1 ORDER BY priority,id`, entity)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var x Policy
		var raw []byte
		if err = rows.Scan(&x.ID, &x.Name, &x.EntityType, &x.ConditionField, &x.ConditionOperator, &raw, &x.ApproverMethod, &x.ApproverRoleID, &x.ApproverOrgID, &x.ApprovalSteps, &x.AllowReject, &x.AllowResubmit, &x.AllowDelegate, &x.Active, &x.Priority, &x.CreatedAt); err != nil {
			return nil, nil, err
		}
		_ = json.Unmarshal(raw, &x.ConditionValue)
		if matches(x, snapshot) {
			return &x, snapshot, nil
		}
	}
	return nil, snapshot, rows.Err()
}
func matches(p Policy, snap map[string]any) bool {
	if p.ConditionField == "" {
		return true
	}
	actual, ok := snap[p.ConditionField]
	if !ok {
		return false
	}
	op := strings.ToUpper(p.ConditionOperator)
	if op == "" {
		op = "EQ"
	}
	aNum, aok := asFloat(actual)
	bNum, bok := asFloat(p.ConditionValue)
	if aok && bok {
		switch op {
		case "GT":
			return aNum > bNum
		case "GTE":
			return aNum >= bNum
		case "LT":
			return aNum < bNum
		case "LTE":
			return aNum <= bNum
		case "NE":
			return aNum != bNum
		default:
			return aNum == bNum
		}
	}
	a, b := fmt.Sprint(actual), fmt.Sprint(p.ConditionValue)
	switch op {
	case "NE":
		return a != b
	case "CONTAINS":
		return strings.Contains(strings.ToLower(a), strings.ToLower(b))
	default:
		return strings.EqualFold(a, b)
	}
}
func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		x, e := n.Float64()
		return x, e == nil
	case string:
		x, e := strconv.ParseFloat(n, 64)
		return x, e == nil
	}
	return 0, false
}

func (s *Service) Capability(ctx context.Context, p *auth.Principal, entity, id string) (map[string]any, error) {
	policy, _, err := s.applicable(ctx, entity, id)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		return map[string]any{"enabled": false}, nil
	}
	return map[string]any{"enabled": true, "policy": policy, "canRequest": p.Has("approval:request")}, nil
}
func (s *Service) Submit(ctx context.Context, p *auth.Principal, entity, id, reason string, ip, requestID, ua string) (Request, error) {
	if err := auth.Require(p, "approval:request"); err != nil {
		return Request{}, err
	}
	policy, snapshot, err := s.applicable(ctx, entity, id)
	if err != nil {
		return Request{}, err
	}
	if policy == nil {
		return Request{}, errors.New("no approval policy applies; the entity is processed immediately")
	}
	var exists bool
	_ = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM approval_requests WHERE entity_type=$1 AND entity_id=$2 AND status='PENDING')`, strings.ToUpper(entity), id).Scan(&exists)
	if exists {
		return Request{}, errors.New("a pending approval request already exists")
	}
	approver, err := s.resolveApprover(ctx, p, policy)
	if err != nil {
		return Request{}, err
	}
	reqID := ids.New()
	raw, _ := json.Marshal(snapshot)
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Request{}, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO approval_requests(id,policy_id,entity_type,entity_id,requester_id,approver_id,snapshot,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, reqID, policy.ID, strings.ToUpper(entity), id, p.UserID, approver, raw, nullable(reason))
	if err != nil {
		return Request{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO approval_steps(id,request_id,step_no,approver_id) VALUES($1,$2,1,$3)`, ids.New(), reqID, approver)
	if err != nil {
		return Request{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO approval_history(id,request_id,action,actor_id,comment) VALUES($1,$2,'SUBMIT',$3,$4)`, ids.New(), reqID, p.UserID, nullable(reason))
	if err != nil {
		return Request{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Request{}, err
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "WEB", Action: "APPROVAL_SUBMIT", Resource: strings.ToLower(entity), ResourceID: id, After: map[string]any{"requestId": reqID, "policyId": policy.ID}, IP: ip, RequestID: requestID, UserAgent: ua})
	return s.Get(ctx, p, reqID)
}
func (s *Service) resolveApprover(ctx context.Context, p *auth.Principal, policy *Policy) (string, error) {
	var id string
	if policy.ApproverMethod == "MANAGER" {
		err := s.DB.QueryRow(ctx, `SELECT manager_id FROM users WHERE id=$1 AND manager_id IS NOT NULL`, p.UserID).Scan(&id)
		if err == nil {
			return id, nil
		}
	}
	if policy.ApproverRoleID != "" {
		err := s.DB.QueryRow(ctx, `SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id WHERE ur.role_id=$1 AND u.active=true ORDER BY CASE WHEN u.organization_id=$2 THEN 0 ELSE 1 END,u.created_at LIMIT 1`, policy.ApproverRoleID, nullable(p.OrganizationID)).Scan(&id)
		if err == nil {
			return id, nil
		}
	}
	err := s.DB.QueryRow(ctx, `SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN role_permissions rp ON rp.role_id=ur.role_id WHERE rp.permission='admin:*' AND u.active=true ORDER BY CASE WHEN u.id=$1 THEN 1 ELSE 0 END,u.created_at LIMIT 1`, p.UserID).Scan(&id)
	if err != nil {
		return "", errors.New("approval policy has no available approver")
	}
	return id, nil
}

func (s *Service) Get(ctx context.Context, p *auth.Principal, id string) (Request, error) {
	var x Request
	var raw []byte
	err := s.DB.QueryRow(ctx, `SELECT ar.id,ar.policy_id,ap.name,ar.entity_type,ar.entity_id,ar.requester_id,req.display_name,COALESCE(ar.approver_id::text,''),COALESCE(app.display_name,''),ar.status,ar.current_step,ar.snapshot,COALESCE(ar.reason,''),ar.requested_at,ar.decided_at,ar.version FROM approval_requests ar JOIN approval_policies ap ON ap.id=ar.policy_id JOIN users req ON req.id=ar.requester_id LEFT JOIN users app ON app.id=ar.approver_id WHERE ar.id=$1 AND (ar.requester_id=$2 OR ar.approver_id=$2 OR $3)`, id, p.UserID, p.IsBootstrap || p.Has("admin:read")).Scan(&x.ID, &x.PolicyID, &x.PolicyName, &x.EntityType, &x.EntityID, &x.RequesterID, &x.RequesterName, &x.ApproverID, &x.ApproverName, &x.Status, &x.CurrentStep, &raw, &x.Reason, &x.RequestedAt, &x.DecidedAt, &x.Version)
	_ = json.Unmarshal(raw, &x.Snapshot)
	return x, err
}
func (s *Service) List(ctx context.Context, p *auth.Principal, status string) ([]Request, error) {
	query := `SELECT ar.id,ar.policy_id,ap.name,ar.entity_type,ar.entity_id,ar.requester_id,req.display_name,COALESCE(ar.approver_id::text,''),COALESCE(app.display_name,''),ar.status,ar.current_step,ar.snapshot,COALESCE(ar.reason,''),ar.requested_at,ar.decided_at,ar.version FROM approval_requests ar JOIN approval_policies ap ON ap.id=ar.policy_id JOIN users req ON req.id=ar.requester_id LEFT JOIN users app ON app.id=ar.approver_id WHERE (ar.requester_id=$1 OR ar.approver_id=$1 OR $2) AND ($3='' OR ar.status=$3) ORDER BY ar.requested_at DESC LIMIT 200`
	rows, err := s.DB.Query(ctx, query, p.UserID, p.IsBootstrap || p.Has("admin:read"), status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		var x Request
		var raw []byte
		if err = rows.Scan(&x.ID, &x.PolicyID, &x.PolicyName, &x.EntityType, &x.EntityID, &x.RequesterID, &x.RequesterName, &x.ApproverID, &x.ApproverName, &x.Status, &x.CurrentStep, &raw, &x.Reason, &x.RequestedAt, &x.DecidedAt, &x.Version); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &x.Snapshot)
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) Decide(ctx context.Context, p *auth.Principal, id, decision, comment string, version int, ip, requestID, ua string) (Request, error) {
	if err := auth.Require(p, "approval:approve"); err != nil {
		return Request{}, err
	}
	decision = strings.ToUpper(decision)
	if decision != "APPROVE" && decision != "REJECT" {
		return Request{}, errors.New("decision must be APPROVE or REJECT")
	}
	before, err := s.Get(ctx, p, id)
	if err != nil {
		return Request{}, err
	}
	if before.Status != "PENDING" {
		return Request{}, errors.New("approval request is not pending")
	}
	if before.ApproverID != p.UserID && !p.IsBootstrap && !p.Has("admin:write") {
		return Request{}, errors.New("only the designated approver can decide")
	}
	if version == 0 {
		version = before.Version
	}
	status := "APPROVED"
	if decision == "REJECT" {
		status = "REJECTED"
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Request{}, err
	}
	defer tx.Rollback(ctx)
	cmd, err := tx.Exec(ctx, `UPDATE approval_requests SET status=$2,decided_at=now(),version=version+1 WHERE id=$1 AND status='PENDING' AND version=$3`, id, status, version)
	if err != nil {
		return Request{}, err
	}
	if cmd.RowsAffected() == 0 {
		return Request{}, errors.New("approval request was changed by another user")
	}
	_, err = tx.Exec(ctx, `UPDATE approval_steps SET status=$2,comment=$3,decided_at=now() WHERE request_id=$1 AND step_no=$4`, id, status, nullable(comment), before.CurrentStep)
	if err != nil {
		return Request{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO approval_history(id,request_id,action,actor_id,comment) VALUES($1,$2,$3,$4,$5)`, ids.New(), id, decision, p.UserID, nullable(comment))
	if err != nil {
		return Request{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Request{}, err
	}
	after, err := s.Get(ctx, p, id)
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "WEB", Action: "APPROVAL_" + decision, Resource: strings.ToLower(before.EntityType), ResourceID: before.EntityID, Before: before, After: after, IP: ip, RequestID: requestID, UserAgent: ua})
	return after, err
}

func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
