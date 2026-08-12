package intelligence

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5"
)

// The rule engine. It reads the CRM, decides what is worth saying, and writes
// signals, risks, insights and recommendations.
//
// Three properties matter more than the rules themselves:
//
//   - Idempotent. Every record has a dedupe key derived from (type, entity), so
//     running the engine twice produces the same rows, refreshed, not doubled.
//   - Self-healing. A condition that stops holding resolves its signal and its
//     risk. Intelligence that only ever accumulates is noise within a week.
//   - Human decisions survive. An accepted risk stays accepted and a dismissed
//     recommendation stays dismissed; the engine will not resurrect either.
//
// It runs with no principal: it scans every account, and Data Scope is applied
// when the results are *read*, not when they are produced. A salesperson must
// not change what the company knows by virtue of what they can see.

const (
	noContactDays  = 30 // 30일 이상 접촉 없음
	stalledDays    = 30 // Stage 30일 이상 정체
	renewalDays    = 90 // 계약 만료 D-90
	engagementDays = 14 // 최근 2주 미팅 증가 판정 구간
	quoteDays      = 14 // 최근 견적 요청
)

// Run executes one full pass and records what it changed.
func (s *Service) Run(ctx context.Context, trigger, triggeredBy string) (RunSummary, error) {
	run := RunSummary{ID: ids.New(), StartedAt: time.Now().UTC(), Trigger: trigger}
	if _, err := s.DB.Exec(ctx, `INSERT INTO intelligence_runs(id,started_at,trigger,triggered_by) VALUES($1,$2,$3,$4)`,
		run.ID, run.StartedAt, trigger, nullableID(triggeredBy)); err != nil {
		return run, err
	}
	summary, err := s.scan(ctx, &run)
	finished := time.Now().UTC()
	summary.FinishedAt = &finished
	message := ""
	if err != nil {
		message = err.Error()
		summary.Error = message
	}
	if _, dbErr := s.DB.Exec(ctx, `UPDATE intelligence_runs SET finished_at=$2,accounts_scanned=$3,signals_opened=$4,
		signals_resolved=$5,risks_opened=$6,risks_resolved=$7,insights_generated=$8,recommendations_generated=$9,error=$10 WHERE id=$1`,
		run.ID, finished, summary.AccountsScanned, summary.SignalsOpened, summary.SignalsResolved,
		summary.RisksOpened, summary.RisksResolved, summary.InsightsGenerated, summary.RecommendationsGenerated,
		nullableText(message)); dbErr != nil && err == nil {
		err = dbErr
	}
	return summary, err
}

// accountFacts is everything the rules need about one account, read in a few
// queries instead of a few per account.
type accountFacts struct {
	ID              string
	Name            string
	OwnerID         string
	LastContactAt   *time.Time
	RecentTouches   int // activities in the last engagementDays
	PriorTouches    int // activities in the engagementDays before that
	Contacts        int
	DecisionMakers  int
	OpenVoices      int
	CriticalVoices  int
	CriticalVoiceID string
	RecentQuoteID   string
	RecentQuoteName string
}

type opportunityFacts struct {
	ID             string
	Name           string
	AccountID      string
	StageName      string
	StageEnteredAt time.Time
	Probability    int
	Amount         float64
	Status         string
	LastActivity   *time.Time
	CloseDate      *time.Time
}

type contractFacts struct {
	ID            string
	Title         string
	AccountID     string
	EndDate       time.Time
	RenewalStatus string
	AutoRenew     bool
}

func (s *Service) scan(ctx context.Context, run *RunSummary) (RunSummary, error) {
	summary := *run
	accounts, err := s.accountFacts(ctx)
	if err != nil {
		return summary, err
	}
	summary.AccountsScanned = len(accounts)
	opportunities, err := s.opportunityFacts(ctx)
	if err != nil {
		return summary, err
	}
	contracts, err := s.contractFacts(ctx)
	if err != nil {
		return summary, err
	}

	now := time.Now().UTC()
	// live holds every dedupe key the rules produced this pass. Anything open in
	// the database that is not in here no longer holds and gets resolved.
	live := map[string]bool{}
	byAccount := map[string][]Signal{}

	emit := func(signal Signal) {
		live[signalKey(signal)] = true
		byAccount[signal.AccountID] = append(byAccount[signal.AccountID], signal)
	}

	for _, account := range accounts {
		for _, signal := range accountSignals(account, now) {
			emit(signal)
		}
	}
	for _, opportunity := range opportunities {
		for _, signal := range opportunitySignals(opportunity, now) {
			emit(signal)
		}
	}
	for _, contract := range contracts {
		for _, signal := range contractSignals(contract, now) {
			emit(signal)
		}
	}

	opened, err := s.persistSignals(ctx, byAccount)
	if err != nil {
		return summary, err
	}
	summary.SignalsOpened = opened
	resolved, err := s.resolveStaleSignals(ctx, live)
	if err != nil {
		return summary, err
	}
	summary.SignalsResolved = resolved

	// Risks are scored from the signals that just held, so the score and its
	// explanation cannot disagree.
	risks := map[string][]Risk{}
	riskLive := map[string]bool{}
	for accountID, signals := range byAccount {
		for _, risk := range scoreRisks(accountID, signals, accounts[accountID], opportunities) {
			riskLive[risk.dedupeKey()] = true
			risks[accountID] = append(risks[accountID], risk)
		}
	}
	riskOpened, err := s.persistRisks(ctx, risks)
	if err != nil {
		return summary, err
	}
	summary.RisksOpened = riskOpened
	riskResolved, err := s.resolveStaleRisks(ctx, riskLive)
	if err != nil {
		return summary, err
	}
	summary.RisksResolved = riskResolved

	insights, recommendations := 0, 0
	for accountID, signals := range byAccount {
		account := accounts[accountID]
		generated, err := s.persistInsights(ctx, account, signals, risks[accountID], now)
		if err != nil {
			return summary, err
		}
		insights += generated
		made, err := s.persistRecommendations(ctx, account, signals, risks[accountID], now)
		if err != nil {
			return summary, err
		}
		recommendations += made
	}
	summary.InsightsGenerated = insights
	summary.RecommendationsGenerated = recommendations
	if err = s.expireInsights(ctx); err != nil {
		return summary, err
	}
	return summary, nil
}

// ---- rules ----------------------------------------------------------------

func accountSignals(a *accountFacts, now time.Time) []Signal {
	out := []Signal{}
	base := Signal{EntityType: "ACCOUNT", EntityID: a.ID, AccountID: a.ID, AccountName: a.Name,
		SourceType: "ACCOUNT", SourceID: a.ID, DetectedAt: now, Status: "ACTIVE"}

	// Last Contact > 30일 → NEGATIVE
	days := daysSince(a.LastContactAt, now)
	if a.LastContactAt == nil || days >= noContactDays {
		signal := base
		signal.SignalType = "NO_CONTACT"
		signal.Sentiment = "NEGATIVE"
		signal.Severity = "MEDIUM"
		if days >= noContactDays*2 || a.LastContactAt == nil {
			signal.Severity = "HIGH"
		}
		if a.LastContactAt == nil {
			signal.Title = "고객과의 접촉 기록이 없습니다"
			signal.Description = "이 고객에 대해 기록된 활동이 아직 없습니다."
			signal.Evidence = map[string]any{"daysSinceContact": nil}
		} else {
			signal.Title = fmt.Sprintf("%d일간 고객 접촉 없음", days)
			signal.Description = fmt.Sprintf("마지막 접촉은 %s입니다. 관계가 식기 전에 접점을 만드세요.", a.LastContactAt.Format("2006-01-02"))
			signal.Evidence = map[string]any{"daysSinceContact": days, "lastContactAt": a.LastContactAt}
		}
		out = append(out, signal)
	}

	// Decision Maker 없음 → 관계 위험의 근거
	if a.Contacts > 0 && a.DecisionMakers == 0 {
		signal := base
		signal.SignalType = "DECISION_MAKER_MISSING"
		signal.Sentiment = "NEGATIVE"
		signal.Severity = "MEDIUM"
		signal.Title = "의사결정자가 확인되지 않았습니다"
		signal.Description = fmt.Sprintf("담당자 %d명이 등록되어 있지만 의사결정 권한을 가진 사람이 지정되지 않았습니다.", a.Contacts)
		signal.Evidence = map[string]any{"contacts": a.Contacts, "decisionMakers": 0}
		out = append(out, signal)
	}
	if a.Contacts == 0 {
		signal := base
		signal.SignalType = "DECISION_MAKER_MISSING"
		signal.Sentiment = "NEGATIVE"
		signal.Severity = "HIGH"
		signal.Title = "등록된 담당자가 없습니다"
		signal.Description = "고객 담당자가 한 명도 등록되지 않아 의사결정 구조를 알 수 없습니다."
		signal.Evidence = map[string]any{"contacts": 0, "decisionMakers": 0}
		out = append(out, signal)
	}

	// Critical VOC 미해결
	if a.CriticalVoices > 0 {
		signal := base
		signal.SignalType = "CRITICAL_VOC"
		signal.Sentiment = "NEGATIVE"
		signal.Severity = "CRITICAL"
		signal.EntityType = "VOC"
		signal.EntityID = a.CriticalVoiceID
		signal.SourceType = "VOC"
		signal.SourceID = a.CriticalVoiceID
		signal.Title = fmt.Sprintf("미해결 긴급 고객 요청 %d건", a.CriticalVoices)
		signal.Description = "긴급으로 접수된 고객 요청이 아직 해결되지 않았습니다."
		signal.Evidence = map[string]any{"criticalVoices": a.CriticalVoices, "openVoices": a.OpenVoices}
		out = append(out, signal)
	}

	// 고객 미팅 증가 → POSITIVE
	if a.RecentTouches >= 3 && a.RecentTouches > a.PriorTouches {
		signal := base
		signal.SignalType = "ENGAGEMENT_INCREASE"
		signal.Sentiment = "POSITIVE"
		signal.Severity = "LOW"
		signal.Title = fmt.Sprintf("최근 %d일간 고객 접점이 늘었습니다", engagementDays)
		signal.Description = fmt.Sprintf("직전 같은 기간 %d건에서 %d건으로 증가했습니다. 제안을 진전시킬 시점입니다.", a.PriorTouches, a.RecentTouches)
		signal.Evidence = map[string]any{"recent": a.RecentTouches, "prior": a.PriorTouches, "windowDays": engagementDays}
		out = append(out, signal)
	}

	// 견적 요청 발생 → POSITIVE
	if a.RecentQuoteID != "" {
		signal := base
		signal.SignalType = "QUOTE_REQUESTED"
		signal.Sentiment = "POSITIVE"
		signal.Severity = "LOW"
		signal.SourceType = "QUOTATION"
		signal.SourceID = a.RecentQuoteID
		signal.Title = "최근 견적이 발행되었습니다"
		signal.Description = fmt.Sprintf("%s. 결정 단계에 가까워졌으므로 후속 행동을 정하세요.", a.RecentQuoteName)
		signal.Evidence = map[string]any{"quotation": a.RecentQuoteName, "windowDays": quoteDays}
		out = append(out, signal)
	}
	return out
}

func opportunitySignals(o *opportunityFacts, now time.Time) []Signal {
	out := []Signal{}
	if o.Status != "OPEN" {
		return out
	}
	stalled := int(now.Sub(o.StageEnteredAt).Hours() / 24)
	if stalled >= stalledDays {
		severity := "MEDIUM"
		if stalled >= stalledDays*2 {
			severity = "HIGH"
		}
		out = append(out, Signal{
			SignalType: "DEAL_STALLED", Sentiment: "NEGATIVE", Severity: severity,
			EntityType: "OPPORTUNITY", EntityID: o.ID, AccountID: o.AccountID,
			Title:       fmt.Sprintf("%s · %s 단계에서 %d일 정체", o.Name, o.StageName, stalled),
			Description: fmt.Sprintf("%s 단계 진입 후 %d일간 다음 단계로 이동하지 않았습니다.", o.StageName, stalled),
			Evidence:    map[string]any{"daysInStage": stalled, "stage": o.StageName, "amount": o.Amount},
			DetectedAt:  now, SourceType: "OPPORTUNITY", SourceID: o.ID, Status: "ACTIVE",
		})
	}
	// A deal whose close date has passed while still open is a forecast lie.
	if o.CloseDate != nil && o.CloseDate.Before(now) {
		overdue := int(now.Sub(*o.CloseDate).Hours() / 24)
		out = append(out, Signal{
			SignalType: "CLOSE_DATE_PASSED", Sentiment: "NEGATIVE", Severity: "HIGH",
			EntityType: "OPPORTUNITY", EntityID: o.ID, AccountID: o.AccountID,
			Title:       fmt.Sprintf("%s의 예상 종료일이 %d일 지났습니다", o.Name, overdue),
			Description: "예상 종료일이 지났는데 진행 중입니다. Forecast가 실제와 어긋나 있습니다.",
			Evidence:    map[string]any{"daysOverdue": overdue, "expectedCloseDate": o.CloseDate},
			DetectedAt:  now, SourceType: "OPPORTUNITY", SourceID: o.ID, Status: "ACTIVE",
		})
	}
	return out
}

func contractSignals(c *contractFacts, now time.Time) []Signal {
	remaining := int(c.EndDate.Sub(now).Hours() / 24)
	if remaining > renewalDays || remaining < 0 {
		return nil
	}
	severity := "MEDIUM"
	if remaining <= 30 {
		severity = "HIGH"
	}
	if remaining <= 14 && c.RenewalStatus == "NOT_STARTED" && !c.AutoRenew {
		severity = "CRITICAL"
	}
	return []Signal{{
		SignalType: "CONTRACT_EXPIRING", Sentiment: "NEGATIVE", Severity: severity,
		EntityType: "CONTRACT", EntityID: c.ID, AccountID: c.AccountID,
		Title:       fmt.Sprintf("%s 만료 D-%d", c.Title, remaining),
		Description: fmt.Sprintf("만료일은 %s이며 갱신 상태는 %s입니다.", c.EndDate.Format("2006-01-02"), renewalStatusText(c.RenewalStatus)),
		Evidence:    map[string]any{"daysRemaining": remaining, "endDate": c.EndDate, "renewalStatus": c.RenewalStatus, "autoRenew": c.AutoRenew},
		DetectedAt:  now, SourceType: "CONTRACT", SourceID: c.ID, Status: "ACTIVE",
	}}
}

// scoreRisks turns the signals that held into scored risks. Each risk names the
// signals that produced it, so a score is always explainable.
func scoreRisks(accountID string, signals []Signal, account *accountFacts, opportunities map[string]*opportunityFacts) []Risk {
	name := ""
	if account != nil {
		name = account.Name
	}
	byType := map[string][]Signal{}
	for _, signal := range signals {
		byType[signal.SignalType] = append(byType[signal.SignalType], signal)
	}
	out := []Risk{}

	add := func(riskType, entityType, entityID, title string, factors []RiskFactor) {
		score := 0
		for _, factor := range factors {
			score += factor.Points
		}
		if score <= 0 {
			return
		}
		if score > 100 {
			score = 100
		}
		details := make([]string, 0, len(factors))
		for _, factor := range factors {
			details = append(details, factor.Detail)
		}
		out = append(out, Risk{
			RiskType: riskType, EntityType: entityType, EntityID: entityID,
			AccountID: accountID, AccountName: name, RiskScore: score, Severity: severityForScore(score),
			Title: title, Description: strings.Join(details, " · "), Factors: factors,
			DetectedAt: time.Now().UTC(), Status: "OPEN",
		})
	}

	// RELATIONSHIP_RISK — 접촉 감소와 의사결정자 부재
	relationship := []RiskFactor{}
	for _, signal := range byType["NO_CONTACT"] {
		points := 35
		if signal.Severity == "HIGH" {
			points = 50
		}
		relationship = append(relationship, RiskFactor{Signal: "NO_CONTACT", Detail: signal.Title, Points: points})
	}
	for _, signal := range byType["DECISION_MAKER_MISSING"] {
		points := 25
		if signal.Severity == "HIGH" {
			points = 35
		}
		relationship = append(relationship, RiskFactor{Signal: "DECISION_MAKER_MISSING", Detail: signal.Title, Points: points})
	}
	if len(byType["ENGAGEMENT_INCREASE"]) > 0 && len(relationship) > 0 {
		// Rising engagement is evidence against a relationship risk, so it
		// subtracts rather than being ignored.
		relationship = append(relationship, RiskFactor{Signal: "ENGAGEMENT_INCREASE",
			Detail: byType["ENGAGEMENT_INCREASE"][0].Title, Points: -20})
	}
	add("RELATIONSHIP_RISK", "ACCOUNT", accountID, "고객 관계가 약화되고 있습니다", relationship)

	// VOC_RISK — 미해결 Critical VOC
	voc := []RiskFactor{}
	for _, signal := range byType["CRITICAL_VOC"] {
		voc = append(voc, RiskFactor{Signal: "CRITICAL_VOC", Detail: signal.Title, Points: 75})
	}
	if len(voc) > 0 {
		add("VOC_RISK", "ACCOUNT", accountID, "미해결 긴급 요청이 관계를 위협합니다", voc)
	}

	// RENEWAL_RISK — 만료 임박 + 갱신 미착수
	for _, signal := range byType["CONTRACT_EXPIRING"] {
		points := 40
		switch signal.Severity {
		case "HIGH":
			points = 60
		case "CRITICAL":
			points = 85
		}
		factors := []RiskFactor{{Signal: "CONTRACT_EXPIRING", Detail: signal.Title, Points: points}}
		if status, _ := signal.Evidence["renewalStatus"].(string); status == "NOT_STARTED" {
			factors = append(factors, RiskFactor{Signal: "RENEWAL_NOT_STARTED", Detail: "갱신 계획이 아직 수립되지 않았습니다", Points: 15})
		}
		add("RENEWAL_RISK", "CONTRACT", signal.EntityID, signal.Title, factors)
	}

	// DEAL_RISK — 정체와 종료일 경과. Deal 단위로 별도 채점한다.
	dealFactors := map[string][]RiskFactor{}
	for _, signalType := range []string{"DEAL_STALLED", "CLOSE_DATE_PASSED"} {
		for _, signal := range byType[signalType] {
			points := 45
			if signalType == "CLOSE_DATE_PASSED" {
				points = 40
			}
			if signal.Severity == "HIGH" {
				points += 15
			}
			dealFactors[signal.EntityID] = append(dealFactors[signal.EntityID],
				RiskFactor{Signal: signalType, Detail: signal.Title, Points: points})
		}
	}
	dealIDs := make([]string, 0, len(dealFactors))
	for id := range dealFactors {
		dealIDs = append(dealIDs, id)
	}
	sort.Strings(dealIDs)
	for _, id := range dealIDs {
		title := "영업기회가 진전되지 않고 있습니다"
		if deal := opportunities[id]; deal != nil {
			title = fmt.Sprintf("%s 진행이 멈춰 있습니다", deal.Name)
		}
		add("DEAL_RISK", "OPPORTUNITY", id, title, dealFactors[id])
	}
	return out
}

// buildInsight turns the account's signals into one readable paragraph. This is
// where an LLM would go later; the shape of the record does not change when it
// does, only the sentence.
func buildInsight(account *accountFacts, signals []Signal, risks []Risk) (Insight, bool) {
	negative := []Signal{}
	for _, signal := range signals {
		if signal.Sentiment == "NEGATIVE" {
			negative = append(negative, signal)
		}
	}
	if len(negative) < 2 {
		return Insight{}, false
	}
	sort.SliceStable(negative, func(i, j int) bool {
		return severityRank[negative[i].Severity] > severityRank[negative[j].Severity]
	})
	evidence := make([]string, 0, len(negative))
	for _, signal := range negative {
		evidence = append(evidence, signal.Title)
	}
	worst := 0
	for _, risk := range risks {
		if risk.RiskScore > worst {
			worst = risk.RiskScore
		}
	}
	// Confidence rises with corroboration: three independent negative signals
	// are a pattern, one is an anecdote.
	confidence := 40 + len(negative)*15
	if confidence > 95 {
		confidence = 95
	}
	return Insight{
		AccountID: account.ID, AccountName: account.Name, InsightType: "ENGAGEMENT_DECLINE",
		Title:    fmt.Sprintf("%s의 관계 신호가 약해지고 있습니다", account.Name),
		Summary:  fmt.Sprintf("부정 신호 %d건이 동시에 관측되었습니다. 가장 높은 위험 점수는 %d점입니다.", len(negative), worst),
		Evidence: evidence, Confidence: confidence, Status: "ACTIVE",
	}, true
}

// recommend maps risks and signals onto concrete next actions. Recommendations
// are only produced above the HIGH threshold — advice nobody acts on trains
// people to ignore the panel.
func recommend(account *accountFacts, signals []Signal, risks []Risk, now time.Time) []Recommendation {
	out := []Recommendation{}
	seen := map[string]bool{}
	push := func(recommendationType, priority, title, description, sourceType, sourceID string, due time.Time) {
		if seen[recommendationType] {
			return
		}
		seen[recommendationType] = true
		dueDate := due
		out = append(out, Recommendation{
			AccountID: account.ID, AccountName: account.Name, RecommendationType: recommendationType,
			Priority: priority, Title: title, Description: description,
			SourceType: sourceType, SourceID: sourceID, AssigneeID: account.OwnerID,
			DueDate: &dueDate, Status: "OPEN", GeneratedAt: now,
		})
	}
	byType := map[string][]Signal{}
	for _, signal := range signals {
		byType[signal.SignalType] = append(byType[signal.SignalType], signal)
	}

	for _, risk := range risks {
		if risk.RiskScore < 70 {
			continue
		}
		priority := "HIGH"
		days := 7
		if risk.RiskScore >= 90 {
			days = 3
		}
		switch risk.RiskType {
		case "RELATIONSHIP_RISK":
			title := fmt.Sprintf("%s 담당자와 미팅 일정을 잡으세요", account.Name)
			if len(byType["DECISION_MAKER_MISSING"]) > 0 {
				title = fmt.Sprintf("%s의 의사결정자를 확인하고 미팅을 잡으세요", account.Name)
			}
			push("SCHEDULE_MEETING", priority, title, risk.Description, "RISK", risk.ID, now.AddDate(0, 0, days))
		case "VOC_RISK":
			push("RESOLVE_VOC", priority, "긴급 고객 요청을 해결하세요", risk.Description, "RISK", risk.ID, now.AddDate(0, 0, days))
		case "RENEWAL_RISK":
			push("CREATE_RENEWAL", priority, "갱신 영업기회를 생성하고 갱신 계획을 세우세요", risk.Description, "RISK", risk.ID, now.AddDate(0, 0, days))
		case "DEAL_RISK":
			push("ADVANCE_DEAL", priority, "정체된 영업기회의 다음 단계를 확정하세요", risk.Description, "RISK", risk.ID, now.AddDate(0, 0, days))
		}
	}
	// A positive signal deserves an action too: momentum decays if nobody uses it.
	if len(byType["QUOTE_REQUESTED"]) > 0 && !seen["ADVANCE_DEAL"] {
		signal := byType["QUOTE_REQUESTED"][0]
		push("FOLLOW_UP_QUOTE", "MEDIUM", "발행한 견적의 후속 확인을 진행하세요", signal.Description,
			"SIGNAL", signal.SourceID, now.AddDate(0, 0, 5))
	}
	return out
}

// ---- helpers ---------------------------------------------------------------

// renewalStatusText keeps enum codes out of Korean prose. The API keeps the
// code; the sentence a person reads gets the word.
func renewalStatusText(status string) string {
	switch status {
	case "NOT_STARTED":
		return "미착수"
	case "IN_PROGRESS":
		return "진행 중"
	case "RENEWED":
		return "갱신 완료"
	case "CHURNED":
		return "이탈"
	}
	return status
}

func signalKey(s Signal) string {
	return strings.Join([]string{s.SignalType, s.EntityType, s.EntityID}, "|")
}

func (r Risk) dedupeKey() string {
	return strings.Join([]string{r.RiskType, r.EntityType, r.EntityID}, "|")
}

func daysSince(at *time.Time, now time.Time) int {
	if at == nil {
		return 1 << 30
	}
	return int(now.Sub(*at).Hours() / 24)
}

func nullableID(id string) any {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return id
}

func nullableText(text string) any {
	if text == "" {
		return nil
	}
	return text
}

func jsonValue(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return raw
}

// RunIntelligence is the entry point used by the scheduler and by the admin
// "지금 분석" button. It requires the same permission as reading intelligence
// plus write, so an ordinary reader cannot force load onto the database.
func (s *Service) RunIntelligence(ctx context.Context, p *auth.Principal, trigger string) (RunSummary, error) {
	if err := auth.Require(p, "intelligence:run"); err != nil {
		return RunSummary{}, err
	}
	return s.Run(ctx, trigger, p.UserID)
}

// LastRun reports the most recent pass so a screen can say when the numbers were
// produced, and distinguish "nothing found" from "never ran".
func (s *Service) LastRun(ctx context.Context) (*RunSummary, error) {
	var run RunSummary
	var finished *time.Time
	var errText *string
	err := s.DB.QueryRow(ctx, `SELECT id,started_at,finished_at,trigger,accounts_scanned,signals_opened,signals_resolved,
		risks_opened,risks_resolved,insights_generated,recommendations_generated,error
		FROM intelligence_runs ORDER BY started_at DESC LIMIT 1`).
		Scan(&run.ID, &run.StartedAt, &finished, &run.Trigger, &run.AccountsScanned, &run.SignalsOpened,
			&run.SignalsResolved, &run.RisksOpened, &run.RisksResolved, &run.InsightsGenerated,
			&run.RecommendationsGenerated, &errText)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	run.FinishedAt = finished
	if errText != nil {
		run.Error = *errText
	}
	return &run, nil
}

// RunIfDue is the scheduler's entry point. The engine is a full-table sweep, so
// it is throttled by when it last finished rather than by how often the job
// runner ticks. An administrator can disable it or change the interval without
// a redeploy.
func (s *Service) RunIfDue(ctx context.Context) error {
	var enabled bool
	var minutes int
	if err := s.DB.QueryRow(ctx, `SELECT
		COALESCE((SELECT value::text::boolean FROM system_settings WHERE namespace='intelligence' AND key='auto_analyze_enabled'),true),
		COALESCE((SELECT value::text::int FROM system_settings WHERE namespace='intelligence' AND key='auto_analyze_minutes'),15)`).
		Scan(&enabled, &minutes); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	if minutes < 1 {
		minutes = 15
	}
	var due bool
	if err := s.DB.QueryRow(ctx, `SELECT NOT EXISTS(
		SELECT 1 FROM intelligence_runs WHERE finished_at IS NOT NULL AND finished_at > now() - ($1 || ' minutes')::interval)`,
		minutes).Scan(&due); err != nil {
		return err
	}
	if !due {
		return nil
	}
	_, err := s.Run(ctx, "SCHEDULE", "")
	return err
}
