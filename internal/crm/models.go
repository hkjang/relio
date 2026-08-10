package crm

import "time"

type Customer struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	RegistrationNo string         `json:"registrationNo,omitempty"`
	CustomerType   string         `json:"customerType"`
	Grade          string         `json:"grade,omitempty"`
	Industry       string         `json:"industry,omitempty"`
	Website        string         `json:"website,omitempty"`
	Phone          string         `json:"phone,omitempty"`
	Email          string         `json:"email,omitempty"`
	Address        string         `json:"address,omitempty"`
	OwnerID        string         `json:"ownerId"`
	OwnerName      string         `json:"ownerName,omitempty"`
	OrganizationID string         `json:"organizationId,omitempty"`
	Health         string         `json:"health"`
	AnnualRevenue  float64        `json:"annualRevenue,omitempty"`
	EmployeeCount  int            `json:"employeeCount,omitempty"`
	CustomFields   map[string]any `json:"customFields"`
	Version        int            `json:"version"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
}
type CustomerInput struct {
	Name           string         `json:"name"`
	RegistrationNo string         `json:"registrationNo"`
	CustomerType   string         `json:"customerType"`
	Grade          string         `json:"grade"`
	Industry       string         `json:"industry"`
	Website        string         `json:"website"`
	Phone          string         `json:"phone"`
	Email          string         `json:"email"`
	Address        string         `json:"address"`
	OwnerID        string         `json:"ownerId"`
	Health         string         `json:"health"`
	AnnualRevenue  float64        `json:"annualRevenue"`
	EmployeeCount  int            `json:"employeeCount"`
	CustomFields   map[string]any `json:"customFields"`
	Version        int            `json:"version"`
}

type Contact struct {
	ID                   string     `json:"id"`
	CustomerID           string     `json:"customerId"`
	Name                 string     `json:"name"`
	Title                string     `json:"title,omitempty"`
	Department           string     `json:"department,omitempty"`
	Email                string     `json:"email,omitempty"`
	Phone                string     `json:"phone,omitempty"`
	Mobile               string     `json:"mobile,omitempty"`
	DecisionMaker        bool       `json:"decisionMaker"`
	PrimaryContact       bool       `json:"primaryContact"`
	RelationshipRole     string     `json:"relationshipRole"`
	Influence            string     `json:"influence"`
	Sentiment            string     `json:"sentiment"`
	RelationshipStrength int        `json:"relationshipStrength"`
	DecisionPower        int        `json:"decisionPower"`
	LastContactAt        *time.Time `json:"lastContactAt,omitempty"`
	OwnerID              string     `json:"ownerId"`
	CreatedAt            time.Time  `json:"createdAt"`
}

type Stage struct {
	ID               string  `json:"id"`
	PipelineID       string  `json:"pipelineId"`
	Name             string  `json:"name"`
	Order            int     `json:"order"`
	Probability      float64 `json:"probability"`
	ForecastCategory string  `json:"forecastCategory"`
	IsWon            bool    `json:"isWon"`
	IsLost           bool    `json:"isLost"`
	Active           bool    `json:"active"`
	Color            string  `json:"color"`
	MinDays          *int    `json:"minDays,omitempty"`
	MaxDays          *int    `json:"maxDays,omitempty"`
}
type Pipeline struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Active  bool    `json:"active"`
	Default bool    `json:"default"`
	Stages  []Stage `json:"stages"`
}

type Opportunity struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	CustomerID         string         `json:"customerId"`
	CustomerName       string         `json:"customerName"`
	OwnerID            string         `json:"ownerId"`
	OwnerName          string         `json:"ownerName"`
	OrganizationID     string         `json:"organizationId,omitempty"`
	PipelineID         string         `json:"pipelineId"`
	StageID            string         `json:"stageId"`
	StageName          string         `json:"stageName"`
	StageColor         string         `json:"stageColor"`
	ExpectedAmount     float64        `json:"expectedAmount"`
	CurrencyCode       string         `json:"currencyCode"`
	ExchangeRate       float64        `json:"exchangeRate"`
	BaseExpectedAmount float64        `json:"baseExpectedAmount"`
	Probability        float64        `json:"probability"`
	WeightedAmount     float64        `json:"weightedAmount"`
	BaseWeightedAmount float64        `json:"baseWeightedAmount"`
	ExpectedCloseDate  *time.Time     `json:"expectedCloseDate,omitempty"`
	ForecastCategory   string         `json:"forecastCategory"`
	Competitor         string         `json:"competitor,omitempty"`
	NextAction         string         `json:"nextAction,omitempty"`
	NextActionDate     *time.Time     `json:"nextActionDate,omitempty"`
	Status             string         `json:"status"`
	LostReason         string         `json:"lostReason,omitempty"`
	WinReason          string         `json:"winReason,omitempty"`
	StageEnteredAt     time.Time      `json:"stageEnteredAt"`
	LastActivityAt     *time.Time     `json:"lastActivityAt,omitempty"`
	Health             []string       `json:"health"`
	CustomFields       map[string]any `json:"customFields"`
	Version            int            `json:"version"`
	CreatedAt          time.Time      `json:"createdAt"`
	UpdatedAt          time.Time      `json:"updatedAt"`
}
type OpportunityInput struct {
	Name              string         `json:"name"`
	CustomerID        string         `json:"customerId"`
	OwnerID           string         `json:"ownerId"`
	PipelineID        string         `json:"pipelineId"`
	StageID           string         `json:"stageId"`
	ExpectedAmount    float64        `json:"expectedAmount"`
	CurrencyCode      string         `json:"currencyCode"`
	ExchangeRate      *float64       `json:"exchangeRate"`
	Probability       *float64       `json:"probability"`
	ExpectedCloseDate *string        `json:"expectedCloseDate"`
	ForecastCategory  string         `json:"forecastCategory"`
	Competitor        string         `json:"competitor"`
	NextAction        string         `json:"nextAction"`
	NextActionDate    *string        `json:"nextActionDate"`
	Status            string         `json:"status"`
	LostReason        string         `json:"lostReason"`
	WinReason         string         `json:"winReason"`
	CustomFields      map[string]any `json:"customFields"`
	Version           int            `json:"version"`
}

type StageGateIssue struct {
	CriterionID string `json:"criterionId"`
	Name        string `json:"name"`
	Enforcement string `json:"enforcement"`
	Message     string `json:"message"`
}

type StageGateResult struct {
	Allowed  bool             `json:"allowed"`
	Blocked  []StageGateIssue `json:"blocked"`
	Warnings []StageGateIssue `json:"warnings"`
}

type Activity struct {
	ID             string     `json:"id"`
	CustomerID     string     `json:"customerId,omitempty"`
	OpportunityID  string     `json:"opportunityId,omitempty"`
	ActivityType   string     `json:"activityType"`
	Subject        string     `json:"subject"`
	Description    string     `json:"description,omitempty"`
	OccurredAt     time.Time  `json:"occurredAt"`
	NextAction     string     `json:"nextAction,omitempty"`
	NextActionDate *time.Time `json:"nextActionDate,omitempty"`
	OwnerID        string     `json:"ownerId"`
	OwnerName      string     `json:"ownerName"`
	CreatedAt      time.Time  `json:"createdAt"`
}
type ActivityInput struct {
	CustomerID     string     `json:"customerId"`
	OpportunityID  string     `json:"opportunityId"`
	ActivityType   string     `json:"activityType"`
	Subject        string     `json:"subject"`
	Description    string     `json:"description"`
	OccurredAt     *time.Time `json:"occurredAt"`
	NextAction     string     `json:"nextAction"`
	NextActionDate *string    `json:"nextActionDate"`
}

type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
	HasMore    bool   `json:"hasMore"`
}
