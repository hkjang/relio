package relationship

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	DB    *pgxpool.Pool
	CRM   *crm.Service
	Audit *audit.Service
}

func validRelationshipType(value string) bool {
	switch value {
	case "REPORTS_TO", "INFLUENCES", "WORKS_WITH", "BLOCKS", "TRUSTS", "ADVISES", "OTHER":
		return true
	default:
		return false
	}
}

func (s *Service) graphMaximum(ctx context.Context) int {
	maximum := 100
	_ = s.DB.QueryRow(ctx, `SELECT COALESCE((SELECT value::text::integer FROM system_settings WHERE namespace='relationship_intelligence' AND key='graph_max_nodes'),100)`).Scan(&maximum)
	if maximum < 10 {
		maximum = 10
	}
	if maximum > 200 {
		maximum = 200
	}
	return maximum
}

func (s *Service) Graph(ctx context.Context, p *auth.Principal, customerID string) (RelationshipGraph, error) {
	customer, err := s.CRM.GetCustomer(ctx, p, customerID)
	if err != nil {
		return RelationshipGraph{}, err
	}
	maximum := s.graphMaximum(ctx)
	nodes, err := s.CRM.SearchContacts(ctx, p, "", customerID, maximum)
	if err != nil {
		return RelationshipGraph{}, err
	}
	rows, err := s.DB.Query(ctx, `SELECT r.id,r.customer_id,r.source_contact_id,src.name,r.target_contact_id,dst.name,r.relationship_type,r.strength,COALESCE(r.description,''),r.active,r.version,r.created_at,r.updated_at FROM contact_relationships r JOIN contacts src ON src.id=r.source_contact_id JOIN contacts dst ON dst.id=r.target_contact_id WHERE r.customer_id=$1 AND r.active=true ORDER BY r.strength DESC,r.created_at`, customerID)
	if err != nil {
		return RelationshipGraph{}, err
	}
	defer rows.Close()
	edges := []ContactRelationship{}
	for rows.Next() {
		var item ContactRelationship
		if err = rows.Scan(&item.ID, &item.CustomerID, &item.SourceContactID, &item.SourceName, &item.TargetContactID, &item.TargetName, &item.RelationshipType, &item.Strength, &item.Description, &item.Active, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return RelationshipGraph{}, err
		}
		edges = append(edges, item)
	}
	if err = rows.Err(); err != nil {
		return RelationshipGraph{}, err
	}
	metrics := RelationshipMetrics{Contacts: len(nodes)}
	strengthTotal := 0
	for _, node := range nodes {
		if node.DecisionMaker || node.RelationshipRole == "DECISION_MAKER" {
			metrics.DecisionMakers++
		}
		if node.RelationshipRole == "CHAMPION" {
			metrics.Champions++
		}
		if node.Sentiment == "SUPPORT" {
			metrics.Supporters++
		}
		if node.Sentiment == "OPPOSE" {
			metrics.Opponents++
		}
		strengthTotal += node.RelationshipStrength
	}
	if len(nodes) > 0 {
		metrics.AverageStrength = math.Round(float64(strengthTotal)/float64(len(nodes))*10) / 10
	}
	score := int(math.Min(25, metrics.AverageStrength/4))
	if metrics.DecisionMakers > 0 {
		score += 30
	}
	if metrics.Champions > 0 {
		score += 25
	}
	if len(nodes) >= 3 {
		score += 10
	}
	if len(edges) >= 2 {
		score += 10
	}
	if score > 100 {
		score = 100
	}
	metrics.RelationshipScore = score
	return RelationshipGraph{Customer: customer, Nodes: nodes, Edges: edges, Metrics: metrics, GeneratedAt: time.Now().UTC(), Truncated: len(nodes) >= maximum, MaximumNodes: maximum}, nil
}

func (s *Service) relationship(ctx context.Context, id string) (ContactRelationship, error) {
	var item ContactRelationship
	err := s.DB.QueryRow(ctx, `SELECT r.id,r.customer_id,r.source_contact_id,src.name,r.target_contact_id,dst.name,r.relationship_type,r.strength,COALESCE(r.description,''),r.active,r.version,r.created_at,r.updated_at FROM contact_relationships r JOIN contacts src ON src.id=r.source_contact_id JOIN contacts dst ON dst.id=r.target_contact_id WHERE r.id=$1`, id).Scan(&item.ID, &item.CustomerID, &item.SourceContactID, &item.SourceName, &item.TargetContactID, &item.TargetName, &item.RelationshipType, &item.Strength, &item.Description, &item.Active, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func (s *Service) SaveRelationship(ctx context.Context, p *auth.Principal, customerID, relationshipID string, input ContactRelationshipInput, meta crm.RequestMeta) (ContactRelationship, error) {
	if err := auth.Require(p, "contact:write"); err != nil {
		return ContactRelationship{}, err
	}
	if _, err := s.CRM.GetCustomer(ctx, p, customerID); err != nil {
		return ContactRelationship{}, err
	}
	input.RelationshipType = strings.ToUpper(strings.TrimSpace(input.RelationshipType))
	if input.SourceContactID == "" || input.TargetContactID == "" || input.SourceContactID == input.TargetContactID {
		return ContactRelationship{}, errors.New("two different contacts are required")
	}
	if !validRelationshipType(input.RelationshipType) {
		return ContactRelationship{}, errors.New("invalid relationshipType")
	}
	strength := 50
	if input.Strength != nil {
		strength = *input.Strength
	}
	if strength < 0 || strength > 100 {
		return ContactRelationship{}, errors.New("strength must be between 0 and 100")
	}
	var contactCount int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM contacts WHERE customer_id=$1 AND id IN ($2,$3)`, customerID, input.SourceContactID, input.TargetContactID).Scan(&contactCount); err != nil || contactCount != 2 {
		return ContactRelationship{}, errors.New("both contacts must belong to the customer")
	}
	active := true
	if input.Active != nil {
		active = *input.Active
	}
	var before any
	if relationshipID == "" {
		relationshipID = ids.New()
		_, err := s.DB.Exec(ctx, `INSERT INTO contact_relationships(id,customer_id,source_contact_id,target_contact_id,relationship_type,strength,description,active,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`, relationshipID, customerID, input.SourceContactID, input.TargetContactID, input.RelationshipType, strength, strings.TrimSpace(input.Description), active, p.UserID)
		if err != nil {
			return ContactRelationship{}, err
		}
	} else {
		current, err := s.relationship(ctx, relationshipID)
		if err != nil || current.CustomerID != customerID {
			return ContactRelationship{}, errors.New("contact relationship not found")
		}
		before = current
		if input.Active == nil {
			active = current.Active
		}
		if input.Strength == nil {
			strength = current.Strength
		}
		tag, err := s.DB.Exec(ctx, `UPDATE contact_relationships SET source_contact_id=$1,target_contact_id=$2,relationship_type=$3,strength=$4,description=$5,active=$6,updated_by=$7,updated_at=now(),version=version+1 WHERE id=$8 AND customer_id=$9 AND version=$10`, input.SourceContactID, input.TargetContactID, input.RelationshipType, strength, strings.TrimSpace(input.Description), active, p.UserID, relationshipID, customerID, input.Version)
		if err != nil {
			return ContactRelationship{}, err
		}
		if tag.RowsAffected() != 1 {
			return ContactRelationship{}, errors.New("contact relationship was changed by another user")
		}
	}
	after, err := s.relationship(ctx, relationshipID)
	if err != nil {
		return ContactRelationship{}, err
	}
	action := "CREATE_RELATIONSHIP"
	if before != nil {
		action = "UPDATE_RELATIONSHIP"
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: meta.Channel, Action: action, Resource: "contact_relationship", ResourceID: relationshipID, Before: before, After: after, IP: meta.IP, RequestID: meta.RequestID, UserAgent: meta.UserAgent})
	return after, nil
}

func (s *Service) DeleteRelationship(ctx context.Context, p *auth.Principal, customerID, relationshipID string, version int, meta crm.RequestMeta) error {
	if err := auth.Require(p, "contact:write"); err != nil {
		return err
	}
	if _, err := s.CRM.GetCustomer(ctx, p, customerID); err != nil {
		return err
	}
	before, err := s.relationship(ctx, relationshipID)
	if err != nil || before.CustomerID != customerID {
		return errors.New("contact relationship not found")
	}
	tag, err := s.DB.Exec(ctx, `DELETE FROM contact_relationships WHERE id=$1 AND customer_id=$2 AND version=$3`, relationshipID, customerID, version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("contact relationship was changed by another user")
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: meta.Channel, Action: "DELETE_RELATIONSHIP", Resource: "contact_relationship", ResourceID: relationshipID, Before: before, IP: meta.IP, RequestID: meta.RequestID, UserAgent: meta.UserAgent})
	return nil
}
