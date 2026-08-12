package intelligence

import "time"

// The four intelligence records. A Signal is an observation ("no contact for 32
// days"), a Risk is a scored judgement built from signals, an Insight explains a
// group of them in one sentence, and a Recommendation says what to do about it.
//
// All four are derived data. They are rebuilt by the engine and carry a
// dedupeKey so a re-run refreshes rather than duplicates. The only fields a
// human writes are the decisions: accepting a risk, accepting or dismissing a
// recommendation.

type Signal struct {
	ID          string         `json:"id"`
	SignalType  string         `json:"signalType"`
	Sentiment   string         `json:"sentiment"`
	Severity    string         `json:"severity"`
	EntityType  string         `json:"entityType"`
	EntityID    string         `json:"entityId"`
	AccountID   string         `json:"accountId"`
	AccountName string         `json:"accountName"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Evidence    map[string]any `json:"evidence"`
	DetectedAt  time.Time      `json:"detectedAt"`
	SourceType  string         `json:"sourceType"`
	SourceID    string         `json:"sourceId,omitempty"`
	Status      string         `json:"status"`
	ResolvedAt  *time.Time     `json:"resolvedAt,omitempty"`
}

type Risk struct {
	ID           string       `json:"id"`
	RiskType     string       `json:"riskType"`
	EntityType   string       `json:"entityType"`
	EntityID     string       `json:"entityId"`
	AccountID    string       `json:"accountId"`
	AccountName  string       `json:"accountName"`
	RiskScore    int          `json:"riskScore"`
	Severity     string       `json:"severity"`
	Title        string       `json:"title"`
	Description  string       `json:"description"`
	Factors      []RiskFactor `json:"factors"`
	DetectedAt   time.Time    `json:"detectedAt"`
	ResolvedAt   *time.Time   `json:"resolvedAt,omitempty"`
	AcceptedNote string       `json:"acceptedNote,omitempty"`
	Status       string       `json:"status"`
}

// RiskFactor is one contribution to a score. Points are the arithmetic; detail
// is the sentence a person reads.
type RiskFactor struct {
	Signal string `json:"signal"`
	Detail string `json:"detail"`
	Points int    `json:"points"`
}

type Insight struct {
	ID            string    `json:"id"`
	AccountID     string    `json:"accountId"`
	AccountName   string    `json:"accountName"`
	OpportunityID string    `json:"opportunityId,omitempty"`
	InsightType   string    `json:"insightType"`
	Title         string    `json:"title"`
	Summary       string    `json:"summary"`
	Evidence      []string  `json:"evidence"`
	Confidence    int       `json:"confidence"`
	GeneratedAt   time.Time `json:"generatedAt"`
	ExpiresAt     time.Time `json:"expiresAt,omitempty"`
	Status        string    `json:"status"`
}

type Recommendation struct {
	ID                 string     `json:"id"`
	AccountID          string     `json:"accountId"`
	AccountName        string     `json:"accountName"`
	OpportunityID      string     `json:"opportunityId,omitempty"`
	RecommendationType string     `json:"recommendationType"`
	Priority           string     `json:"priority"`
	Title              string     `json:"title"`
	Description        string     `json:"description"`
	DueDate            *time.Time `json:"dueDate,omitempty"`
	SourceType         string     `json:"sourceType"`
	SourceID           string     `json:"sourceId,omitempty"`
	AssigneeID         string     `json:"assigneeId"`
	AssigneeName       string     `json:"assigneeName"`
	Status             string     `json:"status"`
	TaskID             string     `json:"taskId,omitempty"`
	DismissReason      string     `json:"dismissReason,omitempty"`
	GeneratedAt        time.Time  `json:"generatedAt"`
	DecidedAt          *time.Time `json:"decidedAt,omitempty"`
}

// AccountIntelligence is what a Customer 360 or Opportunity screen renders: the
// worst score, what drives it, and what to do next.
type AccountIntelligence struct {
	AccountID       string           `json:"accountId"`
	RiskScore       int              `json:"riskScore"`
	Severity        string           `json:"severity"`
	Signals         []Signal         `json:"signals"`
	Risks           []Risk           `json:"risks"`
	Insights        []Insight        `json:"insights"`
	Recommendations []Recommendation `json:"recommendations"`
	AnalyzedAt      *time.Time       `json:"analyzedAt,omitempty"`
}

// RunSummary reports what one engine pass changed.
type RunSummary struct {
	ID                       string     `json:"id"`
	StartedAt                time.Time  `json:"startedAt"`
	FinishedAt               *time.Time `json:"finishedAt,omitempty"`
	Trigger                  string     `json:"trigger"`
	AccountsScanned          int        `json:"accountsScanned"`
	SignalsOpened            int        `json:"signalsOpened"`
	SignalsResolved          int        `json:"signalsResolved"`
	RisksOpened              int        `json:"risksOpened"`
	RisksResolved            int        `json:"risksResolved"`
	InsightsGenerated        int        `json:"insightsGenerated"`
	RecommendationsGenerated int        `json:"recommendationsGenerated"`
	Error                    string     `json:"error,omitempty"`
}

// SignalFilter and friends carry list filters. Every filter narrows the caller's
// Data Scope; none widens it.
type SignalFilter struct {
	AccountID  string
	EntityType string
	EntityID   string
	SignalType string
	Severity   string
	Sentiment  string
	Status     string
	Limit      int
	Cursor     string
}

type RiskFilter struct {
	AccountID  string
	EntityType string
	EntityID   string
	RiskType   string
	Severity   string
	Status     string
	MinScore   int
	Limit      int
	Cursor     string
}

type InsightFilter struct {
	AccountID     string
	OpportunityID string
	InsightType   string
	Status        string
	Limit         int
	Cursor        string
}

type RecommendationFilter struct {
	AccountID     string
	OpportunityID string
	AssigneeID    string
	Mine          bool
	Priority      string
	Status        string
	Limit         int
	Cursor        string
}

// Severity ordering, shared by scoring and by list sorting.
var severityRank = map[string]int{"LOW": 0, "MEDIUM": 1, "HIGH": 2, "CRITICAL": 3}

// severityForScore is the single place the score-to-severity thresholds live.
// The requirement fixes two of them: 70 is HIGH, 90 is CRITICAL.
func severityForScore(score int) string {
	switch {
	case score >= 90:
		return "CRITICAL"
	case score >= 70:
		return "HIGH"
	case score >= 40:
		return "MEDIUM"
	default:
		return "LOW"
	}
}
