package voice

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/crm"
)

// A customer can be lost for reasons that live in different tables: an open
// churn signal, a complaint nobody answered, a renewal nobody started, silence
// since the last meeting. Each looks survivable alone, so this combines them
// into one score with the evidence attached.

type RiskFactor struct {
	Code   string `json:"code"`
	Label  string `json:"label"`
	Detail string `json:"detail"`
	Points int    `json:"points"`
	Route  string `json:"route,omitempty"`
}

type CustomerRisk struct {
	CustomerID   string       `json:"customerId"`
	CustomerName string       `json:"customerName"`
	Score        int          `json:"score"`
	Level        string       `json:"level"`
	Factors      []RiskFactor `json:"factors"`
	Recommended  string       `json:"recommendedAction,omitempty"`
}

// riskInputs is the raw evidence gathered in a single round trip.
type riskInputs struct {
	customerName      string
	churnSignals      int
	overdueVoices     int
	openComplaints    int
	lowSatisfaction   *float64
	daysSinceActivity *int
	expiringContracts int
	renewalNotStarted int
	openPipeline      float64
}

func riskLevel(score int) string {
	switch {
	case score >= 70:
		return "CRITICAL"
	case score >= 45:
		return "HIGH"
	case score >= 20:
		return "WATCH"
	default:
		return "HEALTHY"
	}
}

func (s *Service) gatherRisk(ctx context.Context, p *auth.Principal, customerID string) (riskInputs, error) {
	var in riskInputs
	err := s.DB.QueryRow(ctx, `SELECT c.name,
		(SELECT count(*) FROM customer_voices v WHERE v.customer_id=c.id AND v.voice_type='CHURN_RISK' AND v.status NOT IN ('RESOLVED','CLOSED','REJECTED')),
		(SELECT count(*) FROM customer_voices v WHERE v.customer_id=c.id AND v.status NOT IN ('RESOLVED','CLOSED','REJECTED') AND (
			(v.response_due_at IS NOT NULL AND v.first_responded_at IS NULL AND v.response_due_at < now())
			OR (v.resolution_due_at IS NOT NULL AND v.resolved_at IS NULL AND v.resolution_due_at < now()))),
		(SELECT count(*) FROM customer_voices v WHERE v.customer_id=c.id AND v.voice_type IN ('COMPLAINT','DEFECT') AND v.status NOT IN ('RESOLVED','CLOSED','REJECTED')),
		(SELECT avg(v.satisfaction_score) FROM customer_voices v WHERE v.customer_id=c.id AND v.satisfaction_score IS NOT NULL),
		(SELECT EXTRACT(DAY FROM now()-max(a.occurred_at))::int FROM activities a WHERE a.customer_id=c.id),
		(SELECT count(*) FROM contracts ct WHERE ct.customer_id=c.id AND ct.status='ACTIVE' AND ct.end_date IS NOT NULL
			AND ct.end_date <= (now()+make_interval(days => ct.renewal_notice_days))::date),
		(SELECT count(*) FROM contracts ct WHERE ct.customer_id=c.id AND ct.status='ACTIVE' AND ct.end_date IS NOT NULL
			AND ct.end_date <= (now()+make_interval(days => ct.renewal_notice_days))::date AND ct.renewal_status='NOT_STARTED'),
		(SELECT COALESCE(sum(o.base_expected_amount),0) FROM opportunities o WHERE o.customer_id=c.id AND o.status='OPEN')
		FROM customers c WHERE c.id=$4 AND `+crm.ScopeSQL("c"),
		p.DataScope, p.UserID, orgArg(p), customerID).
		Scan(&in.customerName, &in.churnSignals, &in.overdueVoices, &in.openComplaints,
			&in.lowSatisfaction, &in.daysSinceActivity, &in.expiringContracts, &in.renewalNotStarted, &in.openPipeline)
	return in, err
}

// Risk scores one customer and explains every point it awarded.
func (s *Service) Risk(ctx context.Context, p *auth.Principal, customerID string) (CustomerRisk, error) {
	if err := auth.Require(p, "voice:read"); err != nil {
		return CustomerRisk{}, err
	}
	in, err := s.gatherRisk(ctx, p, customerID)
	if err != nil {
		return CustomerRisk{}, err
	}
	out := CustomerRisk{CustomerID: customerID, CustomerName: in.customerName, Factors: []RiskFactor{}}
	add := func(code, label, detail string, points int, route string) {
		out.Score += points
		out.Factors = append(out.Factors, RiskFactor{Code: code, Label: label, Detail: detail, Points: points, Route: route})
	}
	if in.churnSignals > 0 {
		add("CHURN_SIGNAL", "이탈 징후 접수", fmt.Sprintf("미해결 이탈 징후 %d건", in.churnSignals), min(40, 30+5*(in.churnSignals-1)), "/app/voices")
	}
	if in.overdueVoices > 0 {
		add("VOICE_OVERDUE", "응답·해결 기한 초과", fmt.Sprintf("기한을 넘긴 요청 %d건", in.overdueVoices), min(25, 12*in.overdueVoices), "/app/voices")
	}
	if in.openComplaints > 0 {
		add("OPEN_COMPLAINT", "미해결 불만", fmt.Sprintf("처리 중인 불만·품질 이슈 %d건", in.openComplaints), min(18, 6*in.openComplaints), "/app/voices")
	}
	if in.lowSatisfaction != nil && *in.lowSatisfaction < 3 {
		add("LOW_SATISFACTION", "만족도 저조", fmt.Sprintf("평균 만족도 %.1f점", *in.lowSatisfaction), 15, "/app/voices")
	}
	if in.renewalNotStarted > 0 {
		add("RENEWAL_NOT_STARTED", "갱신 준비 미착수", fmt.Sprintf("갱신 통지 기간에 들어온 계약 %d건이 미착수", in.renewalNotStarted), 20, "/app/contracts")
	} else if in.expiringContracts > 0 {
		add("RENEWAL_WINDOW", "갱신 시점 임박", fmt.Sprintf("갱신 통지 기간 계약 %d건", in.expiringContracts), 8, "/app/contracts")
	}
	if in.daysSinceActivity == nil {
		add("NO_ACTIVITY", "접점 기록 없음", "기록된 고객 접점이 없습니다", 12, "/app/activities")
	} else if *in.daysSinceActivity >= 60 {
		add("CONTACT_GAP", "장기간 접점 공백", fmt.Sprintf("마지막 접점 후 %d일 경과", *in.daysSinceActivity), 18, "/app/activities")
	} else if *in.daysSinceActivity >= 30 {
		add("CONTACT_GAP", "접점 공백", fmt.Sprintf("마지막 접점 후 %d일 경과", *in.daysSinceActivity), 9, "/app/activities")
	}
	if out.Score > 100 {
		out.Score = 100
	}
	out.Level = riskLevel(out.Score)
	sort.SliceStable(out.Factors, func(i, j int) bool { return out.Factors[i].Points > out.Factors[j].Points })
	if len(out.Factors) > 0 {
		out.Recommended = recommend(out.Factors[0].Code, in.openPipeline)
	}
	return out, nil
}

func recommend(topFactor string, openPipeline float64) string {
	switch topFactor {
	case "CHURN_SIGNAL":
		if openPipeline > 0 {
			return "진행 중인 영업기회가 있는 상태의 이탈 징후입니다. 의사결정자와 직접 면담을 먼저 잡으세요."
		}
		return "이탈 징후를 접수한 담당자와 함께 원인 면담을 잡고 상위 보고하세요."
	case "VOICE_OVERDUE":
		return "기한을 넘긴 요청부터 고객에게 현재 상황을 먼저 안내하세요."
	case "OPEN_COMPLAINT":
		return "미해결 불만의 처리 계획과 완료 예정일을 고객에게 확정해 전달하세요."
	case "LOW_SATISFACTION":
		return "만족도가 낮은 건의 근본 원인과 재발 방지 조치를 정리해 공유하세요."
	case "RENEWAL_NOT_STARTED":
		return "갱신 통지 기간입니다. 갱신 담당자를 지정하고 갱신 계획을 착수하세요."
	case "CONTACT_GAP", "NO_ACTIVITY":
		return "접점이 끊겼습니다. 정기 점검 미팅을 잡아 관계를 회복하세요."
	}
	return ""
}

// TopRisks ranks the customers most likely to be lost, for the dashboard.
func (s *Service) TopRisks(ctx context.Context, p *auth.Principal, limit int) ([]CustomerRisk, error) {
	if err := auth.Require(p, "voice:read"); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 50 {
		limit = 5
	}
	// Only score customers that already show at least one signal, so a large
	// book of business does not turn into a full table scan of risk maths.
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT c.id FROM customers c
		WHERE c.active=true AND c.merged_into_id IS NULL AND `+crm.ScopeSQL("c")+` AND (
			EXISTS(SELECT 1 FROM customer_voices v WHERE v.customer_id=c.id AND v.status NOT IN ('RESOLVED','CLOSED','REJECTED'))
			OR EXISTS(SELECT 1 FROM contracts ct WHERE ct.customer_id=c.id AND ct.status='ACTIVE' AND ct.end_date IS NOT NULL
				AND ct.end_date <= (now()+make_interval(days => ct.renewal_notice_days))::date)
			OR NOT EXISTS(SELECT 1 FROM activities a WHERE a.customer_id=c.id AND a.occurred_at > now()-interval '30 days')
		) LIMIT 200`, p.DataScope, p.UserID, orgArg(p))
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := []CustomerRisk{}
	for _, id := range ids {
		risk, err := s.Risk(ctx, p, id)
		if err != nil {
			continue
		}
		if risk.Score >= 20 {
			out = append(out, risk)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---------------------------------------------------------------- CSV export

// csvLabels keeps the exported spreadsheet readable for a Korean team. The API
// still returns the stable codes; only this rendering is localised.
var csvLabels = map[string]string{
	"COMPLAINT": "불만", "REQUEST": "요청", "INQUIRY": "문의", "DEFECT": "품질 이슈",
	"PRAISE": "감사·칭찬", "CHURN_RISK": "이탈 징후",
	"PHONE": "전화", "EMAIL": "이메일", "VISIT": "방문", "PORTAL": "고객포털",
	"CHAT": "채팅", "PARTNER": "파트너 경유", "OTHER": "기타",
	"LOW": "낮음", "NORMAL": "보통", "HIGH": "높음", "CRITICAL": "긴급",
	"RECEIVED": "접수", "IN_REVIEW": "내용 확인", "IN_PROGRESS": "진행 중",
	"PENDING_CUSTOMER": "고객 회신 대기", "RESOLVED": "해결", "CLOSED": "종결", "REJECTED": "반려",
}

func csvLabel(code string) string {
	if korean, ok := csvLabels[code]; ok {
		return korean
	}
	return code
}

// CSV renders the current filter as a spreadsheet. Excel on a Korean desktop
// assumes the system codepage, so the caller writes a BOM first.
func (s *Service) CSV(ctx context.Context, p *auth.Principal, q Query) (string, int, error) {
	items, err := s.List(ctx, p, q)
	if err != nil {
		return "", 0, err
	}
	header := []string{"접수번호", "고객", "요청 담당자", "유형", "세부 분류", "접수 경로", "제목",
		"심각도", "상태", "담당자", "접수 시각", "응답 기한", "응답 완료", "해결 기한", "해결 시각",
		"경과일", "기한 초과", "해결 내용", "근본 원인", "재발 방지", "만족도"}
	var b strings.Builder
	b.WriteString(strings.Join(header, ",") + "\r\n")
	for _, v := range items {
		overdue := ""
		if v.ResponseOverdue {
			overdue = "응답 초과"
		}
		if v.ResolutionOverdue {
			if overdue != "" {
				overdue += " / "
			}
			overdue += "해결 초과"
		}
		satisfaction := ""
		if v.SatisfactionScore != nil {
			satisfaction = fmt.Sprintf("%d", *v.SatisfactionScore)
		}
		row := []string{v.VoiceNo, v.CustomerName, v.ContactName, csvLabel(v.VoiceType), v.CategoryName, csvLabel(v.Channel), v.Title,
			csvLabel(v.Severity), csvLabel(v.Status), v.OwnerName, stamp(&v.OccurredAt), stamp(v.ResponseDueAt), stamp(v.FirstRespondedAt),
			stamp(v.ResolutionDueAt), stamp(v.ResolvedAt), fmt.Sprintf("%d", v.OpenDays), overdue,
			v.Resolution, v.RootCause, v.PreventiveAction, satisfaction}
		for i, cell := range row {
			row[i] = csvCell(cell)
		}
		b.WriteString(strings.Join(row, ",") + "\r\n")
	}
	return b.String(), len(items), nil
}

func stamp(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

// csvCell quotes any value that could otherwise break the row, and neutralises
// leading characters spreadsheets would evaluate as a formula.
func csvCell(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.NewReplacer("\n", " ", "\r", " ").Replace(value)
	if value != "" && strings.ContainsRune("=+-@\t", rune(value[0])) {
		value = "'" + value
	}
	if strings.ContainsAny(value, `",`) {
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return value
}
