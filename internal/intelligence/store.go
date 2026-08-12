package intelligence

import (
	"context"
	"time"

	"github.com/hkjang/relio/internal/platform/ids"
)

// Reading the CRM for the engine, and writing back what it concluded.
//
// The facts are read in one query per entity kind rather than per account: a
// thousand accounts must not become three thousand round trips.

func (s *Service) accountFacts(ctx context.Context) (map[string]*accountFacts, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT c.id::text, c.name, c.owner_id::text,
		       (SELECT max(a.occurred_at) FROM activities a WHERE a.customer_id=c.id),
		       (SELECT count(*) FROM activities a WHERE a.customer_id=c.id AND a.occurred_at >= now() - make_interval(days => $1)),
		       (SELECT count(*) FROM activities a WHERE a.customer_id=c.id
		          AND a.occurred_at >= now() - make_interval(days => $1 * 2)
		          AND a.occurred_at <  now() - make_interval(days => $1)),
		       (SELECT count(*) FROM contacts ct WHERE ct.customer_id=c.id),
		       (SELECT count(*) FROM contacts ct WHERE ct.customer_id=c.id AND ct.decision_maker),
		       (SELECT count(*) FROM customer_voices v WHERE v.customer_id=c.id AND v.status NOT IN ('RESOLVED','CLOSED','REJECTED')),
		       (SELECT count(*) FROM customer_voices v WHERE v.customer_id=c.id AND v.severity='CRITICAL' AND v.status NOT IN ('RESOLVED','CLOSED','REJECTED')),
		       COALESCE((SELECT v.id::text FROM customer_voices v WHERE v.customer_id=c.id AND v.severity='CRITICAL'
		                  AND v.status NOT IN ('RESOLVED','CLOSED','REJECTED') ORDER BY v.occurred_at LIMIT 1),''),
		       COALESCE((SELECT q.id::text FROM quotations q WHERE q.customer_id=c.id
		                  AND q.created_at >= now() - make_interval(days => $2) ORDER BY q.created_at DESC LIMIT 1),''),
		       COALESCE((SELECT q.title FROM quotations q WHERE q.customer_id=c.id
		                  AND q.created_at >= now() - make_interval(days => $2) ORDER BY q.created_at DESC LIMIT 1),'')
		FROM customers c
		WHERE c.active AND c.merged_into_id IS NULL`, engagementDays, quoteDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*accountFacts{}
	for rows.Next() {
		var a accountFacts
		if err = rows.Scan(&a.ID, &a.Name, &a.OwnerID, &a.LastContactAt, &a.RecentTouches, &a.PriorTouches,
			&a.Contacts, &a.DecisionMakers, &a.OpenVoices, &a.CriticalVoices, &a.CriticalVoiceID,
			&a.RecentQuoteID, &a.RecentQuoteName); err != nil {
			return nil, err
		}
		out[a.ID] = &a
	}
	return out, rows.Err()
}

func (s *Service) opportunityFacts(ctx context.Context) (map[string]*opportunityFacts, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT o.id::text, o.name, o.customer_id::text, ps.name, o.stage_entered_at, o.probability,
		       o.base_expected_amount, o.status, o.last_activity_at, o.expected_close_date
		FROM opportunities o JOIN pipeline_stages ps ON ps.id=o.stage_id
		WHERE o.status='OPEN'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*opportunityFacts{}
	for rows.Next() {
		var o opportunityFacts
		if err = rows.Scan(&o.ID, &o.Name, &o.AccountID, &o.StageName, &o.StageEnteredAt, &o.Probability,
			&o.Amount, &o.Status, &o.LastActivity, &o.CloseDate); err != nil {
			return nil, err
		}
		out[o.ID] = &o
	}
	return out, rows.Err()
}

func (s *Service) contractFacts(ctx context.Context) ([]*contractFacts, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id::text, title, customer_id::text, end_date, renewal_status, auto_renew
		FROM contracts
		WHERE status='ACTIVE' AND end_date IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*contractFacts{}
	for rows.Next() {
		var c contractFacts
		if err = rows.Scan(&c.ID, &c.Title, &c.AccountID, &c.EndDate, &c.RenewalStatus, &c.AutoRenew); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// persistSignals upserts on the dedupe key. An IGNORED signal stays ignored:
// a person decided it was not worth seeing, and the engine re-detecting the same
// condition is not new information.
func (s *Service) persistSignals(ctx context.Context, byAccount map[string][]Signal) (int, error) {
	opened := 0
	for _, signals := range byAccount {
		for _, signal := range signals {
			var inserted bool
			err := s.DB.QueryRow(ctx, `
				INSERT INTO signals(id,signal_type,sentiment,severity,entity_type,entity_id,account_id,title,description,
					evidence,detected_at,source_type,source_id,status,dedupe_key)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'ACTIVE',$14)
				ON CONFLICT (dedupe_key) DO UPDATE SET
					sentiment=EXCLUDED.sentiment, severity=EXCLUDED.severity, title=EXCLUDED.title,
					description=EXCLUDED.description, evidence=EXCLUDED.evidence,
					source_type=EXCLUDED.source_type, source_id=EXCLUDED.source_id,
					status=CASE WHEN signals.status='IGNORED' THEN 'IGNORED' ELSE 'ACTIVE' END,
					resolved_at=CASE WHEN signals.status='IGNORED' THEN signals.resolved_at ELSE NULL END,
					updated_at=now()
				RETURNING (xmax = 0)`,
				ids.New(), signal.SignalType, signal.Sentiment, signal.Severity, signal.EntityType, signal.EntityID,
				signal.AccountID, signal.Title, signal.Description, jsonValue(signal.Evidence), signal.DetectedAt,
				signal.SourceType, nullableID(signal.SourceID), signalKey(signal)).Scan(&inserted)
			if err != nil {
				return opened, err
			}
			if inserted {
				opened++
			}
		}
	}
	return opened, nil
}

// resolveStaleSignals closes anything the rules no longer produce. Without this
// the panel would only ever grow, and a signal that says "32일간 접촉 없음" would
// still be there the day after a meeting.
func (s *Service) resolveStaleSignals(ctx context.Context, live map[string]bool) (int, error) {
	keys := make([]string, 0, len(live))
	for key := range live {
		keys = append(keys, key)
	}
	tag, err := s.DB.Exec(ctx, `UPDATE signals SET status='RESOLVED',resolved_at=now(),updated_at=now()
		WHERE status='ACTIVE' AND NOT (dedupe_key = ANY($1))`, keys)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (s *Service) persistRisks(ctx context.Context, byAccount map[string][]Risk) (int, error) {
	opened := 0
	for _, risks := range byAccount {
		for _, risk := range risks {
			var inserted bool
			err := s.DB.QueryRow(ctx, `
				INSERT INTO risks(id,risk_type,entity_type,entity_id,account_id,risk_score,severity,title,description,
					factors,detected_at,status,dedupe_key)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'OPEN',$12)
				ON CONFLICT (dedupe_key) DO UPDATE SET
					risk_score=EXCLUDED.risk_score, severity=EXCLUDED.severity, title=EXCLUDED.title,
					description=EXCLUDED.description, factors=EXCLUDED.factors,
					status=CASE WHEN risks.status='ACCEPTED' THEN 'ACCEPTED' ELSE 'OPEN' END,
					resolved_at=CASE WHEN risks.status='ACCEPTED' THEN risks.resolved_at ELSE NULL END,
					updated_at=now()
				RETURNING (xmax = 0)`,
				ids.New(), risk.RiskType, risk.EntityType, risk.EntityID, risk.AccountID, risk.RiskScore,
				risk.Severity, risk.Title, risk.Description, jsonValue(risk.Factors), risk.DetectedAt,
				risk.dedupeKey()).Scan(&inserted)
			if err != nil {
				return opened, err
			}
			if inserted {
				opened++
			}
		}
	}
	return opened, nil
}

// resolveStaleRisks closes risks whose signals are gone but leaves ACCEPTED ones
// alone — a human decided to live with those.
func (s *Service) resolveStaleRisks(ctx context.Context, live map[string]bool) (int, error) {
	keys := make([]string, 0, len(live))
	for key := range live {
		keys = append(keys, key)
	}
	tag, err := s.DB.Exec(ctx, `UPDATE risks SET status='RESOLVED',resolved_at=now(),updated_at=now()
		WHERE status='OPEN' AND NOT (dedupe_key = ANY($1))`, keys)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (s *Service) persistInsights(ctx context.Context, account *accountFacts, signals []Signal, risks []Risk, now time.Time) (int, error) {
	insight, ok := buildInsight(account, signals, risks)
	if !ok {
		return 0, nil
	}
	key := "ENGAGEMENT_DECLINE|ACCOUNT|" + account.ID
	var inserted bool
	err := s.DB.QueryRow(ctx, `
		INSERT INTO insights(id,account_id,insight_type,title,summary,evidence,confidence,generated_at,expires_at,status,dedupe_key)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'ACTIVE',$10)
		ON CONFLICT (dedupe_key) DO UPDATE SET
			title=EXCLUDED.title, summary=EXCLUDED.summary, evidence=EXCLUDED.evidence,
			confidence=EXCLUDED.confidence, generated_at=EXCLUDED.generated_at,
			expires_at=EXCLUDED.expires_at, status='ACTIVE', updated_at=now()
		RETURNING (xmax = 0)`,
		ids.New(), insight.AccountID, insight.InsightType, insight.Title, insight.Summary,
		jsonValue(insight.Evidence), insight.Confidence, now, now.AddDate(0, 0, 14), key).Scan(&inserted)
	if err != nil {
		return 0, err
	}
	if inserted {
		return 1, nil
	}
	return 0, nil
}

// expireInsights ages out anything past its validity window. An insight is a
// statement about a moment; it should not be presented as current forever.
func (s *Service) expireInsights(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `UPDATE insights SET status='EXPIRED',updated_at=now()
		WHERE status='ACTIVE' AND expires_at IS NOT NULL AND expires_at < now()`)
	return err
}

// persistRecommendations writes advice for the account owner. A recommendation
// the user already answered — accepted, dismissed or completed — is never
// reopened by a later run; only the still-open one is refreshed.
func (s *Service) persistRecommendations(ctx context.Context, account *accountFacts, signals []Signal, risks []Risk, now time.Time) (int, error) {
	generated := 0
	for _, recommendation := range recommend(account, signals, risks, now) {
		key := recommendation.RecommendationType + "|ACCOUNT|" + account.ID
		var inserted bool
		err := s.DB.QueryRow(ctx, `
			INSERT INTO recommendations(id,account_id,recommendation_type,priority,title,description,due_date,
				source_type,source_id,assignee_id,status,generated_at,dedupe_key)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'OPEN',$11,$12)
			ON CONFLICT (dedupe_key) DO UPDATE SET
				priority=EXCLUDED.priority, title=EXCLUDED.title, description=EXCLUDED.description,
				due_date=CASE WHEN recommendations.status='OPEN' THEN EXCLUDED.due_date ELSE recommendations.due_date END,
				assignee_id=CASE WHEN recommendations.status='OPEN' THEN EXCLUDED.assignee_id ELSE recommendations.assignee_id END,
				source_type=EXCLUDED.source_type, source_id=EXCLUDED.source_id,
				generated_at=EXCLUDED.generated_at, updated_at=now()
			RETURNING (xmax = 0)`,
			ids.New(), recommendation.AccountID, recommendation.RecommendationType, recommendation.Priority,
			recommendation.Title, recommendation.Description, recommendation.DueDate, recommendation.SourceType,
			nullableID(recommendation.SourceID), recommendation.AssigneeID, now, key).Scan(&inserted)
		if err != nil {
			return generated, err
		}
		if inserted {
			generated++
		}
	}
	// Advice built on a risk that has gone away is worse than no advice, so open
	// recommendations for this account whose rules no longer fire are withdrawn.
	live := []string{}
	for _, recommendation := range recommend(account, signals, risks, now) {
		live = append(live, recommendation.RecommendationType)
	}
	if _, err := s.DB.Exec(ctx, `DELETE FROM recommendations
		WHERE account_id=$1 AND status='OPEN' AND NOT (recommendation_type = ANY($2))`, account.ID, live); err != nil {
		return generated, err
	}
	return generated, nil
}
