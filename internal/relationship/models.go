package relationship

import (
	"time"

	"github.com/hkjang/relio/internal/crm"
)

type ContactRelationship struct {
	ID               string    `json:"id"`
	CustomerID       string    `json:"customerId"`
	SourceContactID  string    `json:"sourceContactId"`
	SourceName       string    `json:"sourceName"`
	TargetContactID  string    `json:"targetContactId"`
	TargetName       string    `json:"targetName"`
	RelationshipType string    `json:"relationshipType"`
	Strength         int       `json:"strength"`
	Description      string    `json:"description,omitempty"`
	Active           bool      `json:"active"`
	Version          int       `json:"version"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type ContactRelationshipInput struct {
	SourceContactID  string `json:"sourceContactId"`
	TargetContactID  string `json:"targetContactId"`
	RelationshipType string `json:"relationshipType"`
	Strength         *int   `json:"strength"`
	Description      string `json:"description"`
	Active           *bool  `json:"active"`
	Version          int    `json:"version"`
}

type RelationshipMetrics struct {
	Contacts          int     `json:"contacts"`
	DecisionMakers    int     `json:"decisionMakers"`
	Champions         int     `json:"champions"`
	Supporters        int     `json:"supporters"`
	Opponents         int     `json:"opponents"`
	AverageStrength   float64 `json:"averageStrength"`
	RelationshipScore int     `json:"relationshipScore"`
}

type RelationshipGraph struct {
	Customer     crm.Customer          `json:"customer"`
	Nodes        []crm.Contact         `json:"nodes"`
	Edges        []ContactRelationship `json:"edges"`
	Metrics      RelationshipMetrics   `json:"metrics"`
	GeneratedAt  time.Time             `json:"generatedAt"`
	Truncated    bool                  `json:"truncated"`
	MaximumNodes int                   `json:"maximumNodes"`
}

type WhiteSpace struct {
	ID              string  `json:"id"`
	ProductID       string  `json:"productId,omitempty"`
	ProductName     string  `json:"productName"`
	Status          string  `json:"status"`
	PotentialAmount float64 `json:"potentialAmount"`
	Notes           string  `json:"notes,omitempty"`
}

type AccountPlan struct {
	ID                   string       `json:"id,omitempty"`
	CustomerID           string       `json:"customerId"`
	CustomerName         string       `json:"customerName"`
	PlanYear             int          `json:"planYear"`
	Status               string       `json:"status"`
	Strategy             string       `json:"strategy,omitempty"`
	CustomerGoals        []string     `json:"customerGoals"`
	StrategicInitiatives []string     `json:"strategicInitiatives"`
	OurObjectives        []string     `json:"ourObjectives"`
	WhiteSpaces          []WhiteSpace `json:"whiteSpaces"`
	Competitors          []string     `json:"competitors"`
	Risks                []string     `json:"risks"`
	TargetRevenue        float64      `json:"targetRevenue"`
	PotentialRevenue     float64      `json:"potentialRevenue"`
	OwnerID              string       `json:"ownerId,omitempty"`
	OwnerName            string       `json:"ownerName,omitempty"`
	Version              int          `json:"version"`
	CreatedAt            *time.Time   `json:"createdAt,omitempty"`
	UpdatedAt            *time.Time   `json:"updatedAt,omitempty"`
}

type AccountPlanInput struct {
	PlanYear             int          `json:"planYear"`
	Status               string       `json:"status"`
	Strategy             string       `json:"strategy"`
	CustomerGoals        []string     `json:"customerGoals"`
	StrategicInitiatives []string     `json:"strategicInitiatives"`
	OurObjectives        []string     `json:"ourObjectives"`
	WhiteSpaces          []WhiteSpace `json:"whiteSpaces"`
	Competitors          []string     `json:"competitors"`
	Risks                []string     `json:"risks"`
	TargetRevenue        float64      `json:"targetRevenue"`
	PotentialRevenue     float64      `json:"potentialRevenue"`
	Version              int          `json:"version"`
}

type OpportunityMember struct {
	OpportunityID    string    `json:"opportunityId"`
	UserID           string    `json:"userId"`
	Username         string    `json:"username"`
	DisplayName      string    `json:"displayName"`
	Title            string    `json:"title,omitempty"`
	OrganizationID   string    `json:"organizationId,omitempty"`
	OrganizationName string    `json:"organizationName,omitempty"`
	Role             string    `json:"role"`
	Responsibility   string    `json:"responsibility,omitempty"`
	Version          int       `json:"version"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type OpportunityMemberInput struct {
	Role           string `json:"role"`
	Responsibility string `json:"responsibility"`
	Version        int    `json:"version"`
}

type Collaborator struct {
	ID               string `json:"id"`
	Username         string `json:"username"`
	DisplayName      string `json:"displayName"`
	Title            string `json:"title,omitempty"`
	OrganizationID   string `json:"organizationId,omitempty"`
	OrganizationName string `json:"organizationName,omitempty"`
}

type AccountBrief struct {
	Customer360   crm.Customer360   `json:"customer360"`
	Relationships RelationshipGraph `json:"relationships"`
	AccountPlan   AccountPlan       `json:"accountPlan"`
	GeneratedAt   time.Time         `json:"generatedAt"`
}
