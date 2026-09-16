package mail

import (
	"context"
	"fmt"
	"time"
)

// NotifyRenewals mails each contract owner once about every active contract
// that has entered its renewal window without anybody starting the renewal —
// the same rows the 오늘 queue calls RENEWAL_NOT_STARTED. It runs from the
// maintenance tick, which already holds the single-writer lock, and is
// idempotent: a contract is noticed once (mail_notices) and then left alone.
//
// Nothing is noticed while mail is off or incomplete, so turning it on later
// tells owners about everything currently pending rather than silently
// skipping it. An owner without an address is not noticed either, so the
// digest arrives once an administrator adds one.
func (s *Service) NotifyRenewals(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return nil
	}
	config, err := s.Config(ctx)
	if err != nil {
		return err
	}
	if !config.Enabled || !config.Allows(EventContractRenewal) {
		return nil
	}
	if err := config.Validate(); err != nil {
		return err
	}
	rows, err := s.DB.Query(ctx, `SELECT ct.id::text,ct.contract_no,ct.title,c.name,ct.end_date,(ct.end_date-current_date)::int,ct.owner_id::text
		FROM contracts ct JOIN customers c ON c.id=ct.customer_id
		WHERE ct.status='ACTIVE' AND ct.end_date IS NOT NULL
		AND ct.end_date <= (now()+make_interval(days => ct.renewal_notice_days))::date
		AND ct.renewal_status='NOT_STARTED'
		AND NOT EXISTS (SELECT 1 FROM mail_notices n WHERE n.event=$1 AND n.entity_id=ct.id::text)
		ORDER BY ct.owner_id, ct.end_date, ct.id LIMIT 500`, EventContractRenewal)
	if err != nil {
		return err
	}
	byOwner := map[string][]RenewalContract{}
	owners := []string{}
	for rows.Next() {
		var contract RenewalContract
		var owner string
		if err := rows.Scan(&contract.ID, &contract.Number, &contract.Title, &contract.Customer, &contract.EndDate, &contract.DaysLeft, &owner); err != nil {
			rows.Close()
			return err
		}
		if _, seen := byOwner[owner]; !seen {
			owners = append(owners, owner)
		}
		byOwner[owner] = append(byOwner[owner], contract)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, owner := range owners {
		contracts := byOwner[owner]
		// The digest is scheduled, not somebody's action, so there is no actor
		// to exclude: the owner is told even when they created the contract.
		if s.dispatch(ctx, ContractsDueForRenewal(contracts), "", []string{owner}) == 0 {
			continue
		}
		for _, contract := range contracts {
			if _, err := s.DB.Exec(ctx, `INSERT INTO mail_notices(event,entity_id,notified_at) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, EventContractRenewal, contract.ID, s.now()); err != nil {
				return fmt.Errorf("record renewal notice: %w", err)
			}
		}
	}
	return nil
}

// noticeAge is how long the renewal ledger keeps a row after the contract has
// left the window, so a contract that is renewed and later comes due again is
// noticed again.
const noticeAge = 400 * 24 * time.Hour

// PruneNotices drops ledger rows for contracts that are no longer in the
// window, once they are old enough that the same contract cannot still be the
// one that was noticed.
func (s *Service) PruneNotices(ctx context.Context) {
	if s == nil || s.DB == nil {
		return
	}
	_, _ = s.DB.Exec(ctx, `DELETE FROM mail_notices n WHERE n.notified_at < $1 AND NOT EXISTS (
		SELECT 1 FROM contracts ct WHERE ct.id::text=n.entity_id AND ct.status='ACTIVE' AND ct.renewal_status='NOT_STARTED')`, s.now().Add(-noticeAge))
}
