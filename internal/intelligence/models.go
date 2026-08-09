package intelligence

import "time"

type HealthRule struct {
	ID                string         `json:"id"`
	Code              string         `json:"code"`
	Name              string         `json:"name"`
	Description       string         `json:"description"`
	RuleType          string         `json:"ruleType"`
	Threshold         map[string]any `json:"threshold"`
	RiskScore         int            `json:"riskScore"`
	RecommendedAction string         `json:"recommendedAction"`
	Active            bool           `json:"active"`
	Priority          int            `json:"priority"`
	Version           int            `json:"version"`
}

type HealthRuleInput struct {
	Name              string         `json:"name"`
	Description       string         `json:"description"`
	Threshold         map[string]any `json:"threshold"`
	RiskScore         int            `json:"riskScore"`
	RecommendedAction string         `json:"recommendedAction"`
	Active            bool           `json:"active"`
	Priority          int            `json:"priority"`
	Version           int            `json:"version"`
}

type HealthFactor struct {
	Code              string         `json:"code"`
	Name              string         `json:"name"`
	Description       string         `json:"description"`
	RiskScore         int            `json:"riskScore"`
	Evidence          map[string]any `json:"evidence"`
	RecommendedAction string         `json:"recommendedAction"`
}

type DealHealth struct {
	OpportunityID   string         `json:"opportunityId"`
	OpportunityName string         `json:"opportunityName"`
	CustomerID      string         `json:"customerId"`
	CustomerName    string         `json:"customerName"`
	OwnerID         string         `json:"ownerId"`
	OwnerName       string         `json:"ownerName"`
	RiskScore       int            `json:"riskScore"`
	HealthScore     int            `json:"healthScore"`
	RiskLevel       string         `json:"riskLevel"`
	Factors         []HealthFactor `json:"factors"`
	Recommendations []string       `json:"recommendations"`
	CalculatedAt    time.Time      `json:"calculatedAt"`
}

type DealChange struct {
	Field     string    `json:"field"`
	Before    any       `json:"before,omitempty"`
	After     any       `json:"after,omitempty"`
	ChangedAt time.Time `json:"changedAt"`
	ChangedBy string    `json:"changedBy,omitempty"`
}

type DealInspection struct {
	Health      DealHealth   `json:"health"`
	PeriodDays  int          `json:"periodDays"`
	Changes     []DealChange `json:"changes"`
	ChangeCount int          `json:"changeCount"`
}

type PlaybookItem struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Description  string     `json:"description,omitempty"`
	ItemType     string     `json:"itemType"`
	FieldKey     string     `json:"fieldKey,omitempty"`
	Required     bool       `json:"required"`
	DisplayOrder int        `json:"displayOrder"`
	Completed    bool       `json:"completed"`
	Notes        string     `json:"notes,omitempty"`
	CompletedBy  string     `json:"completedBy,omitempty"`
	CompletedAt  *time.Time `json:"completedAt,omitempty"`
}

type Playbook struct {
	ID            string         `json:"id,omitempty"`
	StageID       string         `json:"stageId"`
	StageName     string         `json:"stageName"`
	Name          string         `json:"name,omitempty"`
	Guidance      string         `json:"guidance,omitempty"`
	Active        bool           `json:"active"`
	Items         []PlaybookItem `json:"items"`
	Completed     int            `json:"completed"`
	RequiredTotal int            `json:"requiredTotal"`
	RequiredDone  int            `json:"requiredDone"`
}

type CriterionInput struct {
	ID            string `json:"id,omitempty"`
	Name          string `json:"name"`
	CriterionType string `json:"criterionType"`
	FieldKey      string `json:"fieldKey,omitempty"`
	Operator      string `json:"operator,omitempty"`
	ExpectedValue any    `json:"expectedValue,omitempty"`
	Enforcement   string `json:"enforcement"`
	Message       string `json:"message,omitempty"`
	Active        bool   `json:"active"`
	DisplayOrder  int    `json:"displayOrder"`
}

type PlaybookItemInput struct {
	ID           string `json:"id,omitempty"`
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	ItemType     string `json:"itemType"`
	FieldKey     string `json:"fieldKey,omitempty"`
	Required     bool   `json:"required"`
	DisplayOrder int    `json:"displayOrder"`
}

type StageExecutionInput struct {
	PlaybookName string              `json:"playbookName"`
	Guidance     string              `json:"guidance"`
	Active       bool                `json:"active"`
	Items        []PlaybookItemInput `json:"items"`
	Criteria     []CriterionInput    `json:"criteria"`
}

type StageExecution struct {
	PipelineID   string           `json:"pipelineId"`
	PipelineName string           `json:"pipelineName"`
	StageID      string           `json:"stageId"`
	StageName    string           `json:"stageName"`
	StageOrder   int              `json:"stageOrder"`
	Playbook     Playbook         `json:"playbook"`
	Criteria     []CriterionInput `json:"criteria"`
}

type ForecastMovement struct {
	Type          string  `json:"type"`
	Count         int     `json:"count"`
	Amount        float64 `json:"amount"`
	OpportunityID string  `json:"opportunityId,omitempty"`
	Name          string  `json:"name,omitempty"`
}

type ForecastIntelligence struct {
	FromDate       string             `json:"fromDate"`
	ToDate         string             `json:"toDate"`
	PreviousAmount float64            `json:"previousAmount"`
	CurrentAmount  float64            `json:"currentAmount"`
	ChangeAmount   float64            `json:"changeAmount"`
	RepCommit      float64            `json:"repCommit"`
	ManagerCommit  float64            `json:"managerCommit"`
	Weighted       float64            `json:"weighted"`
	Movements      []ForecastMovement `json:"movements"`
	GeneratedAt    time.Time          `json:"generatedAt"`
}

type ForecastOverrideInput struct {
	ForecastCategory string   `json:"forecastCategory"`
	Probability      *float64 `json:"probability"`
	Amount           *float64 `json:"amount"`
	Reason           string   `json:"reason"`
	Version          int      `json:"version"`
}

type CoachingOwner struct {
	OwnerID       string  `json:"ownerId"`
	OwnerName     string  `json:"ownerName"`
	OpenDeals     int     `json:"openDeals"`
	AtRiskDeals   int     `json:"atRiskDeals"`
	NoNextAction  int     `json:"noNextAction"`
	StalledDeals  int     `json:"stalledDeals"`
	Pipeline      float64 `json:"pipeline"`
	Weighted      float64 `json:"weighted"`
	AverageHealth float64 `json:"averageHealth"`
}

type CoachingDashboard struct {
	RiskThreshold int             `json:"riskThreshold"`
	Attention     []DealHealth    `json:"attention"`
	Owners        []CoachingOwner `json:"owners"`
	GeneratedAt   time.Time       `json:"generatedAt"`
}
