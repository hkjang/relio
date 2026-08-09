package relationship

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/crm"
	"github.com/jackc/pgx/v5"
)

func (s *Service) allowedOpportunityRoles(ctx context.Context) map[string]bool {
	roles := []string{"PRESALES", "CONSULTANT", "MANAGER", "EXECUTIVE_SPONSOR", "LEGAL", "DELIVERY", "OTHER"}
	var raw []byte
	if err := s.DB.QueryRow(ctx, `SELECT value FROM system_settings WHERE namespace='relationship_intelligence' AND key='allowed_opportunity_roles'`).Scan(&raw); err == nil {
		_ = json.Unmarshal(raw, &roles)
	}
	out := map[string]bool{}
	for _, role := range roles {
		out[strings.ToUpper(strings.TrimSpace(role))] = true
	}
	return out
}

func (s *Service) OpportunityTeam(ctx context.Context, p *auth.Principal, opportunityID string) ([]OpportunityMember, error) {
	if _, err := s.CRM.GetOpportunity(ctx, p, opportunityID); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT m.opportunity_id,m.user_id,u.username,u.display_name,COALESCE(u.title,''),u.organization_id,COALESCE(o.name,''),m.member_role,COALESCE(m.responsibility,''),m.version,m.created_at,m.updated_at FROM opportunity_members m JOIN users u ON u.id=m.user_id LEFT JOIN organizations o ON o.id=u.organization_id WHERE m.opportunity_id=$1 ORDER BY CASE m.member_role WHEN 'MANAGER' THEN 1 WHEN 'EXECUTIVE_SPONSOR' THEN 2 WHEN 'PRESALES' THEN 3 ELSE 4 END,u.display_name`, opportunityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OpportunityMember{}
	for rows.Next() {
		var item OpportunityMember
		var organizationID *string
		if err = rows.Scan(&item.OpportunityID, &item.UserID, &item.Username, &item.DisplayName, &item.Title, &organizationID, &item.OrganizationName, &item.Role, &item.Responsibility, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if organizationID != nil {
			item.OrganizationID = *organizationID
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) SaveOpportunityMember(ctx context.Context, p *auth.Principal, opportunityID, userID string, input OpportunityMemberInput, meta crm.RequestMeta) (OpportunityMember, error) {
	if err := auth.Require(p, "opportunity:write"); err != nil {
		return OpportunityMember{}, err
	}
	opportunity, err := s.CRM.GetOpportunity(ctx, p, opportunityID)
	if err != nil {
		return OpportunityMember{}, err
	}
	if userID == "" || userID == opportunity.OwnerID {
		return OpportunityMember{}, errors.New("select an active collaborator other than the opportunity owner")
	}
	input.Role = strings.ToUpper(strings.TrimSpace(input.Role))
	if !s.allowedOpportunityRoles(ctx)[input.Role] {
		return OpportunityMember{}, errors.New("opportunity member role is not allowed by administrator policy")
	}
	var active bool
	if err = s.DB.QueryRow(ctx, `SELECT active FROM users WHERE id=$1`, userID).Scan(&active); err != nil || !active {
		return OpportunityMember{}, errors.New("active collaborator not found")
	}
	var existingVersion int
	var before any
	err = s.DB.QueryRow(ctx, `SELECT version FROM opportunity_members WHERE opportunity_id=$1 AND user_id=$2`, opportunityID, userID).Scan(&existingVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		if input.Version != 0 {
			return OpportunityMember{}, errors.New("new opportunity member version must be 0")
		}
		_, err = s.DB.Exec(ctx, `INSERT INTO opportunity_members(opportunity_id,user_id,member_role,responsibility,added_by) VALUES($1,$2,$3,$4,$5)`, opportunityID, userID, input.Role, strings.TrimSpace(input.Responsibility), p.UserID)
	} else if err != nil {
		return OpportunityMember{}, err
	} else {
		items, teamErr := s.OpportunityTeam(ctx, p, opportunityID)
		if teamErr != nil {
			return OpportunityMember{}, teamErr
		}
		for _, item := range items {
			if item.UserID == userID {
				before = item
				break
			}
		}
		tag, updateErr := s.DB.Exec(ctx, `UPDATE opportunity_members SET member_role=$1,responsibility=$2,updated_at=now(),version=version+1 WHERE opportunity_id=$3 AND user_id=$4 AND version=$5`, input.Role, strings.TrimSpace(input.Responsibility), opportunityID, userID, input.Version)
		err = updateErr
		if err == nil && tag.RowsAffected() != 1 {
			return OpportunityMember{}, errors.New("opportunity member was changed by another user")
		}
	}
	if err != nil {
		return OpportunityMember{}, err
	}
	items, err := s.OpportunityTeam(ctx, p, opportunityID)
	if err != nil {
		return OpportunityMember{}, err
	}
	for _, item := range items {
		if item.UserID == userID {
			s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: meta.Channel, Action: "SAVE_OPPORTUNITY_MEMBER", Resource: "opportunity_member", ResourceID: opportunityID + ":" + userID, Before: before, After: item, IP: meta.IP, RequestID: meta.RequestID, UserAgent: meta.UserAgent})
			return item, nil
		}
	}
	return OpportunityMember{}, errors.New("saved opportunity member not found")
}

func (s *Service) DeleteOpportunityMember(ctx context.Context, p *auth.Principal, opportunityID, userID string, version int, meta crm.RequestMeta) error {
	if err := auth.Require(p, "opportunity:write"); err != nil {
		return err
	}
	if _, err := s.CRM.GetOpportunity(ctx, p, opportunityID); err != nil {
		return err
	}
	items, err := s.OpportunityTeam(ctx, p, opportunityID)
	if err != nil {
		return err
	}
	var before any
	for _, item := range items {
		if item.UserID == userID {
			before = item
			break
		}
	}
	if before == nil {
		return errors.New("opportunity member not found")
	}
	tag, err := s.DB.Exec(ctx, `DELETE FROM opportunity_members WHERE opportunity_id=$1 AND user_id=$2 AND version=$3`, opportunityID, userID, version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("opportunity member was changed by another user")
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: meta.Channel, Action: "DELETE_OPPORTUNITY_MEMBER", Resource: "opportunity_member", ResourceID: opportunityID + ":" + userID, Before: before, IP: meta.IP, RequestID: meta.RequestID, UserAgent: meta.UserAgent})
	return nil
}

func (s *Service) Collaborators(ctx context.Context, p *auth.Principal, query string, limit int) ([]Collaborator, error) {
	if err := auth.Require(p, "opportunity:read"); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, err := s.DB.Query(ctx, `SELECT u.id,u.username,u.display_name,COALESCE(u.title,''),u.organization_id,COALESCE(o.name,'') FROM users u LEFT JOIN organizations o ON o.id=u.organization_id WHERE u.active=true AND u.is_bootstrap=false AND ($1='COMPANY' OR u.id=$2 OR ($1='TEAM' AND (u.id=$2 OR u.manager_id=$2)) OR ($1 IN ('DEPARTMENT','DIVISION') AND EXISTS (
		WITH RECURSIVE user_path AS (
			SELECT id,parent_id,org_type,0 AS depth FROM organizations WHERE id=$3::uuid
			UNION ALL
			SELECT parent.id,parent.parent_id,parent.org_type,user_path.depth+1 FROM organizations parent JOIN user_path ON parent.id=user_path.parent_id
		), scope_root AS (
			SELECT id FROM user_path WHERE org_type=$1 ORDER BY depth LIMIT 1
		), scope_tree AS (
			SELECT id FROM scope_root
			UNION ALL
			SELECT child.id FROM organizations child JOIN scope_tree ON child.parent_id=scope_tree.id
		)
		SELECT 1 FROM scope_tree WHERE id=u.organization_id
	))) AND ($4='' OR lower(u.display_name) LIKE '%'||lower($4)||'%' OR lower(u.username) LIKE '%'||lower($4)||'%') ORDER BY u.display_name LIMIT $5`, p.DataScope, p.UserID, func() any {
		if p.OrganizationID == "" {
			return nil
		}
		return p.OrganizationID
	}(), strings.TrimSpace(query), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Collaborator{}
	for rows.Next() {
		var item Collaborator
		var organizationID *string
		if err = rows.Scan(&item.ID, &item.Username, &item.DisplayName, &item.Title, &organizationID, &item.OrganizationName); err != nil {
			return nil, err
		}
		if organizationID != nil {
			item.OrganizationID = *organizationID
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
