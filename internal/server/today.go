package server

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/platform/httpx"
)

// The dashboard used to list metrics, which tells a salesperson how things are
// but not what to do first. This assembles one ranked queue across the
// subsystems that actually generate work: overdue customer requests, deals that
// have gone quiet, next actions that came due, and renewals nobody started.

type todayItem struct {
	Kind     string     `json:"kind"`
	Severity string     `json:"severity"`
	Title    string     `json:"title"`
	Subtitle string     `json:"subtitle"`
	Route    string     `json:"route"`
	DueAt    *time.Time `json:"dueAt,omitempty"`
	// rank orders the queue; it is not part of the API surface.
	rank int
}

var severityRank = map[string]int{"CRITICAL": 0, "HIGH": 1, "WARNING": 2, "INFO": 3}

func (s *Server) collectToday(ctx context.Context, p *auth.Principal) ([]todayItem, error) {
	items := []todayItem{}
	scope := []any{p.DataScope, p.UserID, nullableOrg(p)}

	// 1. Customer requests past their SLA. A breached promise outranks everything.
	if p.Has("voice:read") {
		rows, err := s.DB.Query(ctx, `SELECT v.id,v.title,c.name,v.severity,
			LEAST(COALESCE(v.response_due_at,'infinity'),COALESCE(v.resolution_due_at,'infinity')) AS due
			FROM customer_voices v JOIN customers c ON c.id=v.customer_id
			WHERE `+crm.ScopeSQL("v")+` AND v.status NOT IN ('RESOLVED','CLOSED','REJECTED') AND (
				(v.response_due_at IS NOT NULL AND v.first_responded_at IS NULL AND v.response_due_at < now())
				OR (v.resolution_due_at IS NOT NULL AND v.resolved_at IS NULL AND v.resolution_due_at < now()))
			ORDER BY due LIMIT 12`, scope...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, title, customer, severity string
			var due time.Time
			if err = rows.Scan(&id, &title, &customer, &severity, &due); err != nil {
				rows.Close()
				return nil, err
			}
			level := "HIGH"
			if severity == "CRITICAL" {
				level = "CRITICAL"
			}
			items = append(items, todayItem{Kind: "VOICE_OVERDUE", Severity: level,
				Title: title, Subtitle: customer + " · 응답·해결 기한을 넘겼습니다", Route: "/app/voices", DueAt: &due})
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}

	// 2. Next actions that came due, from the salesperson's own commitments.
	if p.Has("opportunity:read") {
		rows, err := s.DB.Query(ctx, `SELECT o.id,o.name,c.name,o.next_action,o.next_action_date
			FROM opportunities o JOIN customers c ON c.id=o.customer_id
			WHERE `+crm.ScopeSQL("o")+` AND o.status='OPEN' AND o.next_action_date IS NOT NULL
			AND o.next_action_date <= (now()+interval '2 days')::date
			ORDER BY o.next_action_date LIMIT 12`, scope...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, name, customer string
			var action *string
			var due time.Time
			if err = rows.Scan(&id, &name, &customer, &action, &due); err != nil {
				rows.Close()
				return nil, err
			}
			level := "WARNING"
			if due.Before(time.Now().Truncate(24 * time.Hour)) {
				level = "HIGH"
			}
			label := "다음 행동 예정"
			if action != nil && *action != "" {
				label = *action
			}
			items = append(items, todayItem{Kind: "NEXT_ACTION_DUE", Severity: level,
				Title: label, Subtitle: customer + " · " + name, Route: "/app/opportunities", DueAt: &due})
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}

		// 3. Open deals with no next action, or stalled past the stage guidance.
		rows, err = s.DB.Query(ctx, `SELECT o.id,o.name,c.name,
			(o.next_action IS NULL OR o.next_action='') AS no_action,
			EXTRACT(DAY FROM now()-COALESCE(o.last_activity_at,o.created_at))::int AS quiet_days
			FROM opportunities o JOIN customers c ON c.id=o.customer_id
			WHERE `+crm.ScopeSQL("o")+` AND o.status='OPEN' AND (
				o.next_action IS NULL OR o.next_action=''
				OR COALESCE(o.last_activity_at,o.created_at) < now()-interval '30 days')
			ORDER BY COALESCE(o.last_activity_at,o.created_at) LIMIT 12`, scope...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, name, customer string
			var noAction bool
			var quiet int
			if err = rows.Scan(&id, &name, &customer, &noAction, &quiet); err != nil {
				rows.Close()
				return nil, err
			}
			reason := "다음 행동이 정해지지 않았습니다"
			if !noAction {
				reason = "접점 없이 " + itoa(quiet) + "일 경과했습니다"
			}
			items = append(items, todayItem{Kind: "DEAL_STALLED", Severity: "WARNING",
				Title: name, Subtitle: customer + " · " + reason, Route: "/app/opportunities"})
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}

	// 4. Renewals inside the notice window that nobody has picked up.
	if p.Has("contract:read") {
		rows, err := s.DB.Query(ctx, `SELECT ct.id,ct.title,c.name,ct.end_date
			FROM contracts ct JOIN customers c ON c.id=ct.customer_id
			WHERE `+crm.ScopeSQL("ct")+` AND ct.status='ACTIVE' AND ct.end_date IS NOT NULL
			AND ct.end_date <= (now()+make_interval(days => ct.renewal_notice_days))::date
			AND ct.renewal_status='NOT_STARTED'
			ORDER BY ct.end_date LIMIT 8`, scope...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, title, customer string
			var end time.Time
			if err = rows.Scan(&id, &title, &customer, &end); err != nil {
				rows.Close()
				return nil, err
			}
			items = append(items, todayItem{Kind: "RENEWAL_NOT_STARTED", Severity: "HIGH",
				Title: title, Subtitle: customer + " · 갱신 준비가 시작되지 않았습니다", Route: "/app/contracts", DueAt: &end})
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}

	// 5. Approvals waiting on this user, only when a policy is actually active.
	if p.Has("approval:approve") {
		rows, err := s.DB.Query(ctx, `SELECT ar.id,ap.name,req.display_name,ar.requested_at
			FROM approval_requests ar JOIN approval_policies ap ON ap.id=ar.policy_id
			JOIN users req ON req.id=ar.requester_id
			WHERE ar.status='PENDING' AND ar.approver_id=$1 ORDER BY ar.requested_at LIMIT 8`, p.UserID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, policy, requester string
			var at time.Time
			if err = rows.Scan(&id, &policy, &requester, &at); err != nil {
				rows.Close()
				return nil, err
			}
			items = append(items, todayItem{Kind: "APPROVAL_PENDING", Severity: "HIGH",
				Title: policy + " 검토 요청", Subtitle: requester + "님이 요청했습니다", Route: "/app/approvals", DueAt: &at})
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}

	// Rank by urgency, then by how overdue the item is.
	for i := range items {
		items[i].rank = severityRank[items[i].Severity]
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].rank != items[j].rank {
			return items[i].rank < items[j].rank
		}
		if items[i].DueAt != nil && items[j].DueAt != nil {
			return items[i].DueAt.Before(*items[j].DueAt)
		}
		return items[j].DueAt == nil && items[i].DueAt != nil
	})
	if len(items) > 25 {
		items = items[:25]
	}
	return items, nil
}

func itoa(v int) string {
	if v <= 0 {
		return "0"
	}
	digits := []byte{}
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

func nullableOrg(p *auth.Principal) any {
	if p.OrganizationID == "" {
		return nil
	}
	return p.OrganizationID
}

func (s *Server) today(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	items, err := s.collectToday(r.Context(), p)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	counts := map[string]int{}
	for _, item := range items {
		counts[item.Severity]++
	}
	risks := []any{}
	if p.Has("voice:read") {
		if top, err := s.Voices.TopRisks(r.Context(), p, 5); err == nil {
			for _, risk := range top {
				risks = append(risks, risk)
			}
		}
	}
	httpx.JSON(w, 200, map[string]any{"items": items, "counts": counts, "risks": risks})
}

func (s *Server) customerRisk(w http.ResponseWriter, r *http.Request) {
	v, err := s.Voices.Risk(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, v)
}
