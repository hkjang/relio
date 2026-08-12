package crm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hkjang/relio/internal/auth"
	"github.com/jackc/pgx/v5"
)

// Contacts could be created but never corrected or removed, which made the
// relationship map unusable in practice: a typo in a name was permanent, and a
// person who left the account stayed in the decision graph forever.

var (
	contactRoles      = map[string]bool{"DECISION_MAKER": true, "CHAMPION": true, "INFLUENCER": true, "USER": true, "PROCUREMENT": true}
	contactInfluences = map[string]bool{"HIGH": true, "MEDIUM": true, "LOW": true}
	contactSentiments = map[string]bool{"SUPPORT": true, "NEUTRAL": true, "OPPOSE": true}
)

// normalizeContact applies the shared defaults and validation so create and
// update cannot drift apart.
func normalizeContact(in *ContactInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("name is required")
	}
	in.Name = strings.TrimSpace(in.Name)
	in.RelationshipRole = strings.ToUpper(strings.TrimSpace(in.RelationshipRole))
	if in.RelationshipRole == "" {
		in.RelationshipRole = "USER"
	}
	if !contactRoles[in.RelationshipRole] {
		return errors.New("invalid relationshipRole")
	}
	in.Influence = strings.ToUpper(strings.TrimSpace(in.Influence))
	if in.Influence == "" {
		in.Influence = "MEDIUM"
	}
	if !contactInfluences[in.Influence] {
		return errors.New("invalid influence")
	}
	in.Sentiment = strings.ToUpper(strings.TrimSpace(in.Sentiment))
	if in.Sentiment == "" {
		in.Sentiment = "NEUTRAL"
	}
	if !contactSentiments[in.Sentiment] {
		return errors.New("invalid sentiment")
	}
	if in.RelationshipStrength != nil && (*in.RelationshipStrength < 0 || *in.RelationshipStrength > 100) {
		return errors.New("relationshipStrength must be between 0 and 100")
	}
	if in.DecisionPower != nil && (*in.DecisionPower < 0 || *in.DecisionPower > 100) {
		return errors.New("decisionPower must be between 0 and 100")
	}
	return nil
}

// UpdateContact edits a contact the caller can already see through their Data
// Scope, which is enforced by re-reading the owning customer.
func (s *Service) UpdateContact(ctx context.Context, p *auth.Principal, id string, in ContactInput, m RequestMeta) (Contact, error) {
	if err := auth.Require(p, "contact:write"); err != nil {
		return Contact{}, err
	}
	before, err := s.contact(ctx, p, id)
	if err != nil {
		return Contact{}, err
	}
	if err = normalizeContact(&in); err != nil {
		return Contact{}, err
	}
	strength, power := 50, 50
	if in.RelationshipStrength != nil {
		strength = *in.RelationshipStrength
	} else {
		strength = before.RelationshipStrength
	}
	if in.DecisionPower != nil {
		power = *in.DecisionPower
	} else {
		power = before.DecisionPower
	}
	_, err = s.DB.Exec(ctx, `UPDATE contacts SET name=$2,title=$3,department=$4,email=$5,phone=$6,mobile=$7,
		decision_maker=$8,primary_contact=$9,relationship_role=$10,influence=$11,sentiment=$12,
		relationship_strength=$13,decision_power=$14,updated_by=$15,updated_at=now() WHERE id=$1`,
		id, in.Name, nullable(in.Title), nullable(in.Department), nullable(in.Email), nullable(in.Phone), nullable(in.Mobile),
		in.DecisionMaker, in.PrimaryContact, in.RelationshipRole, in.Influence, in.Sentiment, strength, power, p.UserID)
	if err != nil {
		return Contact{}, err
	}
	after, err := s.contact(ctx, p, id)
	if err != nil {
		return Contact{}, err
	}
	s.audit(ctx, p, m, "UPDATE", "contact", id, before, after)
	return after, nil
}

// DeleteContact removes a contact. Relationship edges reference it, so those are
// removed with it: an edge to a person who is gone has no meaning. Anything that
// records history — a filed customer request — blocks the delete instead.
func (s *Service) DeleteContact(ctx context.Context, p *auth.Principal, id string, m RequestMeta) (map[string]any, error) {
	if err := auth.Require(p, "contact:write"); err != nil {
		return nil, err
	}
	before, err := s.contact(ctx, p, id)
	if err != nil {
		return nil, err
	}
	var voices int
	if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM customer_voices WHERE contact_id=$1`, id).Scan(&voices); err != nil {
		return nil, err
	}
	if voices > 0 {
		return nil, fmt.Errorf("이 담당자로 접수된 고객 요청 %d건이 있어 삭제할 수 없습니다. 요청의 담당자를 먼저 변경하세요", voices)
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var edges int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM contact_relationships WHERE source_contact_id=$1 OR target_contact_id=$1`, id).Scan(&edges); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM contact_relationships WHERE source_contact_id=$1 OR target_contact_id=$1`, id); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM contacts WHERE id=$1`, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.audit(ctx, p, m, "DELETE", "contact", id, before, map[string]any{"removedRelationships": edges})
	return map[string]any{"deleted": true, "removedRelationships": edges}, nil
}

// contact re-reads one contact under the caller's Data Scope.
func (s *Service) contact(ctx context.Context, p *auth.Principal, id string) (Contact, error) {
	var customerID string
	err := s.DB.QueryRow(ctx, `SELECT customer_id::text FROM contacts WHERE id=$1`, id).Scan(&customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Contact{}, errors.New("contact not found")
	}
	if err != nil {
		return Contact{}, err
	}
	// Reading the owning customer applies the scope predicate for us.
	if _, err = s.GetCustomer(ctx, p, customerID); err != nil {
		return Contact{}, errors.New("contact not found")
	}
	items, err := s.SearchContacts(ctx, p, "", customerID, 200)
	if err != nil {
		return Contact{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return Contact{}, errors.New("contact not found")
}

// DeleteCustomer removes a customer that has no history, and deactivates one
// that does. Opportunities, contracts and requests are the record of what
// happened with an account, so they are never silently discarded.
func (s *Service) DeleteCustomer(ctx context.Context, p *auth.Principal, id string, m RequestMeta) (map[string]any, error) {
	// Deleting an account is a separate permission from editing one: it is an
	// administrator action, so it is not part of the default sales Role.
	if err := auth.Require(p, "customer:delete"); err != nil {
		return nil, err
	}
	before, err := s.GetCustomer(ctx, p, id)
	if err != nil {
		return nil, err
	}
	var opportunities, contracts, quotations, voices, sales int
	err = s.DB.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM opportunities WHERE customer_id=$1),
		(SELECT count(*) FROM contracts WHERE customer_id=$1),
		(SELECT count(*) FROM quotations WHERE customer_id=$1),
		(SELECT count(*) FROM customer_voices WHERE customer_id=$1),
		(SELECT count(*) FROM sales WHERE customer_id=$1)`, id).
		Scan(&opportunities, &contracts, &quotations, &voices, &sales)
	if err != nil {
		return nil, err
	}
	history := opportunities + contracts + quotations + voices + sales
	if history > 0 {
		if _, err = s.DB.Exec(ctx, `UPDATE customers SET active=false,updated_by=$2,updated_at=now(),version=version+1 WHERE id=$1`, id, p.UserID); err != nil {
			return nil, err
		}
		s.audit(ctx, p, m, "DEACTIVATE", "customer", id, before, map[string]any{"active": false,
			"opportunities": opportunities, "contracts": contracts, "quotations": quotations, "voices": voices, "sales": sales})
		return map[string]any{"deactivated": true,
			"references": map[string]int{"opportunities": opportunities, "contracts": contracts, "quotations": quotations, "voices": voices, "sales": sales},
			"note":       "영업기회, 계약, 견적, 고객 요청 이력이 있어 목록에서 숨김 처리했습니다. 기록은 그대로 보존됩니다."}, nil
	}
	// Nothing references the customer, so it can go completely. Contacts,
	// activities and plans belong to it and are removed with it.
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	for _, statement := range []string{
		// A converted lead is history in its own right, so it is detached rather
		// than deleted. contact_relationships, account_plans and customer_voices
		// cascade with the customer by schema.
		`UPDATE leads SET converted_customer_id=NULL WHERE converted_customer_id=$1`,
		`DELETE FROM contacts WHERE customer_id=$1`,
		`DELETE FROM activities WHERE customer_id=$1`,
		`DELETE FROM tasks WHERE customer_id=$1`,
		`DELETE FROM customers WHERE id=$1`,
	} {
		if _, err = tx.Exec(ctx, statement, id); err != nil {
			return nil, fmt.Errorf("고객을 삭제할 수 없습니다. 연결된 데이터를 먼저 정리하세요: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.audit(ctx, p, m, "DELETE", "customer", id, before, nil)
	return map[string]any{"deleted": true, "note": "연결된 이력이 없어 완전히 삭제했습니다."}, nil
}
