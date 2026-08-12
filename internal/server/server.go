package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/hkjang/relio/internal/admin"
	"github.com/hkjang/relio/internal/analytics"
	"github.com/hkjang/relio/internal/api"
	"github.com/hkjang/relio/internal/apikey"
	"github.com/hkjang/relio/internal/approval"
	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/intelligence"
	"github.com/hkjang/relio/internal/mcp"
	"github.com/hkjang/relio/internal/oidc"
	"github.com/hkjang/relio/internal/personal"
	"github.com/hkjang/relio/internal/platform/httpx"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/hkjang/relio/internal/relationship"
	"github.com/hkjang/relio/internal/voice"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	DB        *pgxpool.Pool
	Log       *slog.Logger
	Auth      *auth.Service
	Audit     *audit.Service
	CRM       *crm.Service
	Settings  *admin.SettingsService
	Keys      *apikey.Service
	Approvals *approval.Service
	OIDC      *oidc.Service
	MCP       *mcp.Server
	Intel     *intelligence.Service
	Relations *relationship.Service
	Voices    *voice.Service
	Personal  *personal.Service
	Analytics *analytics.Service
	// EncryptionKeyConfigured reports whether the instance data key is wrapped
	// by the ENCRYPTION_KEY environment variable rather than the data volume.
	EncryptionKeyConfigured bool
	started                 time.Time
	limiter                 *loginLimiter
	requests                *requestLimiter
}
type loginLimiter struct {
	mu      sync.Mutex
	entries map[string][]time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{entries: map[string][]time.Time{}} }
func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	recent := l.entries[key][:0]
	for _, t := range l.entries[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 10 {
		l.entries[key] = recent
		return false
	}
	l.entries[key] = append(recent, time.Now())
	return true
}
func (l *loginLimiter) success(key string) { l.mu.Lock(); delete(l.entries, key); l.mu.Unlock() }

type requestBucket struct {
	window time.Time
	count  int
}

type requestLimiter struct {
	mu      sync.Mutex
	entries map[string]requestBucket
}

func newRequestLimiter() *requestLimiter {
	return &requestLimiter{entries: map[string]requestBucket{}}
}

func (l *requestLimiter) allow(key string, limit int) bool {
	if limit <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	bucket := l.entries[key]
	if bucket.window.IsZero() || now.Sub(bucket.window) >= time.Minute {
		bucket = requestBucket{window: now}
	}
	if bucket.count >= limit {
		l.entries[key] = bucket
		return false
	}
	bucket.count++
	l.entries[key] = bucket
	if len(l.entries) > 10000 {
		cutoff := now.Add(-2 * time.Minute)
		for entryKey, entry := range l.entries {
			if entry.window.Before(cutoff) {
				delete(l.entries, entryKey)
			}
		}
	}
	return true
}

func New(db *pgxpool.Pool, log *slog.Logger, authService *auth.Service, auditService *audit.Service, crmService *crm.Service, settings *admin.SettingsService, keys *apikey.Service, approvals *approval.Service, oidcService *oidc.Service, mcpServer *mcp.Server, intel *intelligence.Service, relations *relationship.Service, voices *voice.Service, personalService *personal.Service, analyticsService *analytics.Service) *Server {
	return &Server{DB: db, Log: log, Auth: authService, Audit: auditService, CRM: crmService, Settings: settings, Keys: keys, Approvals: approvals, OIDC: oidcService, MCP: mcpServer, Intel: intel, Relations: relations, Voices: voices, Personal: personalService, Analytics: analyticsService, started: time.Now(), limiter: newLoginLimiter(), requests: newRequestLimiter()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /health/live", s.live)
	mux.HandleFunc("GET /health/ready", s.ready)
	mux.HandleFunc("GET /api/v1/system/version", s.systemVersion)
	mux.HandleFunc("GET /api/v1/auth/status", s.authStatus)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("GET /api/v1/auth/oidc/start", s.oidcStart)
	mux.HandleFunc("GET /api/v1/auth/oidc/callback", s.oidcCallback)
	mux.Handle("/mcp", s.requireAuth(s.MCP, true))
	mux.HandleFunc("GET /analytics.js", s.analyticsLoader)
	mux.HandleFunc("POST /api/v1/csp-report", s.cspReport)
	mux.HandleFunc("GET /api/openapi.json", func(w http.ResponseWriter, r *http.Request) { httpx.JSON(w, 200, api.OpenAPI()) })
	mux.HandleFunc("GET /api/docs", s.apiDocs)
	// Authenticated application APIs.
	mux.Handle("GET /api/v1/auth/me", s.requireAuth(http.HandlerFunc(s.me), false))
	mux.Handle("POST /api/v1/auth/logout", s.requireAuth(http.HandlerFunc(s.logout), false))
	mux.Handle("POST /api/v1/me/password", s.requireAuth(http.HandlerFunc(s.changePassword), false))
	mux.Handle("GET /api/v1/dashboard", s.requireAuth(http.HandlerFunc(s.dashboard), false))
	mux.Handle("GET /api/v1/search", s.requireAuth(http.HandlerFunc(s.search), false))
	mux.Handle("GET /api/v1/customers", s.requireAuth(http.HandlerFunc(s.listCustomers), false))
	mux.Handle("POST /api/v1/customers", s.requireAuth(http.HandlerFunc(s.createCustomer), false))
	mux.Handle("GET /api/v1/customers/{id}", s.requireAuth(http.HandlerFunc(s.getCustomer), false))
	mux.Handle("PUT /api/v1/customers/{id}", s.requireAuth(http.HandlerFunc(s.updateCustomer), false))
	mux.Handle("DELETE /api/v1/customers/{id}", s.requireAuth(http.HandlerFunc(s.deleteCustomer), false))
	mux.Handle("GET /api/v1/customers/{id}/360", s.requireAuth(http.HandlerFunc(s.customer360), false))
	mux.Handle("GET /api/v1/customers/{id}/relationships", s.requireAuth(http.HandlerFunc(s.customerRelationships), false))
	mux.Handle("POST /api/v1/customers/{id}/relationships", s.requireAuth(http.HandlerFunc(s.saveCustomerRelationship), false))
	mux.Handle("PUT /api/v1/customers/{id}/relationships/{relationshipId}", s.requireAuth(http.HandlerFunc(s.saveCustomerRelationship), false))
	mux.Handle("DELETE /api/v1/customers/{id}/relationships/{relationshipId}", s.requireAuth(http.HandlerFunc(s.deleteCustomerRelationship), false))
	mux.Handle("GET /api/v1/customers/{id}/account-plan", s.requireAuth(http.HandlerFunc(s.accountPlan), false))
	mux.Handle("PUT /api/v1/customers/{id}/account-plan", s.requireAuth(http.HandlerFunc(s.saveAccountPlan), false))
	mux.Handle("GET /api/v1/customers/{id}/cross-sell", s.requireAuth(http.HandlerFunc(s.crossSell), false))
	mux.Handle("GET /api/v1/customers/{id}/duplicates", s.requireAuth(http.HandlerFunc(s.customerDuplicates), false))
	mux.Handle("POST /api/v1/customers/{id}/merge", s.requireAuth(http.HandlerFunc(s.mergeCustomers), false))
	mux.Handle("GET /api/v1/contacts", s.requireAuth(http.HandlerFunc(s.listContacts), false))
	mux.Handle("POST /api/v1/contacts", s.requireAuth(http.HandlerFunc(s.createContact), false))
	mux.Handle("PUT /api/v1/contacts/{id}", s.requireAuth(http.HandlerFunc(s.updateContact), false))
	mux.Handle("DELETE /api/v1/contacts/{id}", s.requireAuth(http.HandlerFunc(s.deleteContact), false))
	mux.Handle("GET /api/v1/leads", s.requireAuth(http.HandlerFunc(s.listLeads), false))
	mux.Handle("POST /api/v1/leads", s.requireAuth(http.HandlerFunc(s.createLead), false))
	mux.Handle("GET /api/v1/opportunities", s.requireAuth(http.HandlerFunc(s.listOpportunities), false))
	mux.Handle("POST /api/v1/opportunities", s.requireAuth(http.HandlerFunc(s.createOpportunity), false))
	mux.Handle("GET /api/v1/opportunities/{id}", s.requireAuth(http.HandlerFunc(s.getOpportunity), false))
	mux.Handle("PUT /api/v1/opportunities/{id}", s.requireAuth(http.HandlerFunc(s.updateOpportunity), false))
	mux.Handle("POST /api/v1/opportunities/{id}/stage", s.requireAuth(http.HandlerFunc(s.changeStage), false))
	mux.Handle("GET /api/v1/opportunities/{id}/health", s.requireAuth(http.HandlerFunc(s.dealHealth), false))
	mux.Handle("GET /api/v1/opportunities/{id}/inspection", s.requireAuth(http.HandlerFunc(s.dealInspection), false))
	mux.Handle("GET /api/v1/opportunities/{id}/playbook", s.requireAuth(http.HandlerFunc(s.opportunityPlaybook), false))
	mux.Handle("PUT /api/v1/opportunities/{id}/playbook/{itemId}", s.requireAuth(http.HandlerFunc(s.updatePlaybookProgress), false))
	mux.Handle("GET /api/v1/opportunities/{id}/stage-readiness", s.requireAuth(http.HandlerFunc(s.stageReadiness), false))
	mux.Handle("GET /api/v1/opportunities/{id}/team", s.requireAuth(http.HandlerFunc(s.opportunityTeam), false))
	mux.Handle("PUT /api/v1/opportunities/{id}/team/{userId}", s.requireAuth(http.HandlerFunc(s.saveOpportunityMember), false))
	mux.Handle("DELETE /api/v1/opportunities/{id}/team/{userId}", s.requireAuth(http.HandlerFunc(s.deleteOpportunityMember), false))
	mux.Handle("GET /api/v1/collaborators", s.requireAuth(http.HandlerFunc(s.collaborators), false))
	mux.Handle("GET /api/v1/deal-intelligence/at-risk", s.requireAuth(http.HandlerFunc(s.dealsAtRisk), false))
	mux.Handle("GET /api/v1/deal-intelligence/coaching", s.requireAuth(http.HandlerFunc(s.coachingDashboard), false))
	mux.Handle("GET /api/v1/pipeline", s.requireAuth(http.HandlerFunc(s.pipelines), false))
	mux.Handle("GET /api/v1/products", s.requireAuth(http.HandlerFunc(s.listProducts), false))
	mux.Handle("POST /api/v1/products", s.requireAuth(http.HandlerFunc(s.createProduct), false))
	mux.Handle("PUT /api/v1/products/{id}", s.requireAuth(http.HandlerFunc(s.updateProduct), false))
	mux.Handle("DELETE /api/v1/products/{id}", s.requireAuth(http.HandlerFunc(s.deleteProduct), false))
	mux.Handle("GET /api/v1/activities", s.requireAuth(http.HandlerFunc(s.listActivities), false))
	mux.Handle("POST /api/v1/activities", s.requireAuth(http.HandlerFunc(s.addActivity), false))
	mux.Handle("GET /api/v1/forecasts", s.requireAuth(http.HandlerFunc(s.forecast), false))
	mux.Handle("GET /api/v1/forecasts/intelligence", s.requireAuth(http.HandlerFunc(s.forecastIntelligence), false))
	mux.Handle("PUT /api/v1/forecasts/overrides/{id}", s.requireAuth(http.HandlerFunc(s.forecastOverride), false))
	mux.Handle("GET /api/v1/sales/kpi", s.requireAuth(http.HandlerFunc(s.salesKPI), false))
	mux.Handle("GET /api/v1/tasks/due", s.requireAuth(http.HandlerFunc(s.dueActions), false))
	mux.Handle("GET /api/v1/quotations", s.requireAuth(http.HandlerFunc(s.listQuotations), false))
	mux.Handle("POST /api/v1/quotations", s.requireAuth(http.HandlerFunc(s.createQuotation), false))
	mux.Handle("GET /api/v1/contracts", s.requireAuth(http.HandlerFunc(s.listContracts), false))
	mux.Handle("POST /api/v1/contracts", s.requireAuth(http.HandlerFunc(s.createContract), false))
	mux.Handle("GET /api/v1/contracts/{id}", s.requireAuth(http.HandlerFunc(s.getContract), false))
	mux.Handle("POST /api/v1/contracts/{id}/activate", s.requireAuth(http.HandlerFunc(s.activateContract), false))
	mux.Handle("GET /api/v1/contracts/{id}/revenue-schedule", s.requireAuth(http.HandlerFunc(s.revenueSchedule), false))
	mux.Handle("PUT /api/v1/contracts/{id}/renewal", s.requireAuth(http.HandlerFunc(s.updateContractRenewal), false))
	mux.Handle("POST /api/v1/revenue-schedules/{id}/recognize", s.requireAuth(http.HandlerFunc(s.recognizeRevenue), false))
	mux.Handle("GET /api/v1/sales", s.requireAuth(http.HandlerFunc(s.listSales), false))
	mux.Handle("POST /api/v1/sales", s.requireAuth(http.HandlerFunc(s.createSale), false))
	mux.Handle("GET /api/v1/targets", s.requireAuth(http.HandlerFunc(s.listTargets), false))
	mux.Handle("POST /api/v1/targets", s.requireAuth(http.HandlerFunc(s.createTarget), false))
	mux.Handle("GET /api/v1/notifications", s.requireAuth(http.HandlerFunc(s.listNotifications), false))
	mux.Handle("POST /api/v1/notifications/{id}/read", s.requireAuth(http.HandlerFunc(s.readNotification), false))
	// 고객의 목소리(VOC): 불만, 요청, 문의와 이탈 징후의 전체 수명주기.
	mux.Handle("GET /api/v1/today", s.requireAuth(http.HandlerFunc(s.today), false))
	mux.Handle("GET /api/v1/customers/{id}/risk", s.requireAuth(http.HandlerFunc(s.customerRisk), false))
	mux.Handle("GET /api/v1/signals", s.requireAuth(http.HandlerFunc(s.listSignals), false))
	mux.Handle("GET /api/v1/signals/{id}", s.requireAuth(http.HandlerFunc(s.getSignal), false))
	mux.Handle("POST /api/v1/signals/{id}/ignore", s.requireAuth(http.HandlerFunc(s.ignoreSignal), false))
	mux.Handle("GET /api/v1/risks", s.requireAuth(http.HandlerFunc(s.listRisks), false))
	mux.Handle("GET /api/v1/risks/{id}", s.requireAuth(http.HandlerFunc(s.getRisk), false))
	mux.Handle("GET /api/v1/risks/{id}/explain", s.requireAuth(http.HandlerFunc(s.explainRisk), false))
	mux.Handle("POST /api/v1/risks/{id}/accept", s.requireAuth(http.HandlerFunc(s.acceptRisk), false))
	mux.Handle("GET /api/v1/insights", s.requireAuth(http.HandlerFunc(s.listInsights), false))
	mux.Handle("GET /api/v1/insights/{id}", s.requireAuth(http.HandlerFunc(s.getInsight), false))
	mux.Handle("GET /api/v1/recommendations", s.requireAuth(http.HandlerFunc(s.listRecommendations), false))
	mux.Handle("GET /api/v1/recommendations/{id}", s.requireAuth(http.HandlerFunc(s.getRecommendation), false))
	mux.Handle("POST /api/v1/recommendations/{id}/accept", s.requireAuth(http.HandlerFunc(s.acceptRecommendation), false))
	mux.Handle("POST /api/v1/recommendations/{id}/dismiss", s.requireAuth(http.HandlerFunc(s.dismissRecommendation), false))
	mux.Handle("GET /api/v1/customers/{id}/intelligence", s.requireAuth(http.HandlerFunc(s.customerIntelligence), false))
	mux.Handle("GET /api/v1/intelligence/status", s.requireAuth(http.HandlerFunc(s.intelligenceStatus), false))
	mux.Handle("POST /api/v1/intelligence/run", s.requireAuth(http.HandlerFunc(s.runIntelligence), false))
	mux.Handle("GET /api/v1/voices/export", s.requireAuth(http.HandlerFunc(s.exportVoices), false))
	mux.Handle("GET /api/v1/voices", s.requireAuth(http.HandlerFunc(s.listVoices), false))
	mux.Handle("POST /api/v1/voices", s.requireAuth(http.HandlerFunc(s.createVoice), false))
	mux.Handle("GET /api/v1/voices/summary", s.requireAuth(http.HandlerFunc(s.voiceSummary), false))
	mux.Handle("GET /api/v1/voices/categories", s.requireAuth(http.HandlerFunc(s.voiceCategories), false))
	mux.Handle("GET /api/v1/voices/{id}", s.requireAuth(http.HandlerFunc(s.getVoice), false))
	mux.Handle("PUT /api/v1/voices/{id}", s.requireAuth(http.HandlerFunc(s.updateVoice), false))
	mux.Handle("POST /api/v1/voices/{id}/events", s.requireAuth(http.HandlerFunc(s.commentVoice), false))
	mux.Handle("GET /api/v1/reports", s.requireAuth(http.HandlerFunc(s.reports), false))
	mux.Handle("GET /api/v1/reports/win-loss", s.requireAuth(http.HandlerFunc(s.winLoss), false))
	mux.Handle("GET /api/v1/approvals", s.requireAuth(http.HandlerFunc(s.listApprovals), false))
	mux.Handle("GET /api/v1/approvals/status", s.requireAuth(http.HandlerFunc(s.approvalStatus), false))
	mux.Handle("GET /api/v1/approvals/capability", s.requireAuth(http.HandlerFunc(s.approvalCapability), false))
	mux.Handle("POST /api/v1/approvals", s.requireAuth(http.HandlerFunc(s.submitApproval), false))
	mux.Handle("GET /api/v1/approvals/{id}", s.requireAuth(http.HandlerFunc(s.getApproval), false))
	mux.Handle("POST /api/v1/approvals/{id}/approve", s.requireAuth(http.HandlerFunc(s.approve), false))
	mux.Handle("POST /api/v1/approvals/{id}/reject", s.requireAuth(http.HandlerFunc(s.reject), false))
	mux.Handle("GET /api/v1/me/keys", s.requireAuth(http.HandlerFunc(s.listKeys), false))
	mux.Handle("POST /api/v1/me/keys", s.requireAuth(http.HandlerFunc(s.createKey), false))
	mux.Handle("POST /api/v1/me/keys/{id}/rotate", s.requireAuth(http.HandlerFunc(s.rotateKey), false))
	mux.Handle("DELETE /api/v1/me/keys/{id}", s.requireAuth(http.HandlerFunc(s.revokeKey), false))
	mux.Handle("GET /api/v1/me/views", s.requireAuth(http.HandlerFunc(s.listSavedViews), false))
	mux.Handle("POST /api/v1/me/views", s.requireAuth(http.HandlerFunc(s.createSavedView), false))
	mux.Handle("PUT /api/v1/me/views/{id}", s.requireAuth(http.HandlerFunc(s.updateSavedView), false))
	mux.Handle("DELETE /api/v1/me/views/{id}", s.requireAuth(http.HandlerFunc(s.deleteSavedView), false))
	mux.Handle("GET /api/v1/me/favorites", s.requireAuth(http.HandlerFunc(s.listFavorites), false))
	mux.Handle("POST /api/v1/me/favorites", s.requireAuth(http.HandlerFunc(s.toggleFavorite), false))
	mux.Handle("GET /api/v1/me/activity", s.requireAuth(http.HandlerFunc(s.myActivity), false))
	mux.Handle("GET /api/v1/me/sessions", s.requireAuth(http.HandlerFunc(s.mySessions), false))
	mux.Handle("DELETE /api/v1/me/sessions/{id}", s.requireAuth(http.HandlerFunc(s.revokeSession), false))
	// Administrator APIs are isolated under /api/v1/admin and require admin permissions in each handler.
	mux.Handle("GET /api/v1/admin/settings", s.requireAuth(http.HandlerFunc(s.listSettings), false))
	mux.Handle("PUT /api/v1/admin/settings/{namespace}/{key}", s.requireAuth(http.HandlerFunc(s.putSetting), false))
	mux.Handle("DELETE /api/v1/admin/settings/{namespace}/{key}", s.requireAuth(http.HandlerFunc(s.deleteSetting), false))
	mux.Handle("GET /api/v1/admin/oidc", s.requireAuth(http.HandlerFunc(s.getOIDC), false))
	mux.Handle("PUT /api/v1/admin/oidc", s.requireAuth(http.HandlerFunc(s.putOIDC), false))
	mux.Handle("POST /api/v1/admin/oidc/test", s.requireAuth(http.HandlerFunc(s.testOIDC), false))
	mux.Handle("GET /api/v1/admin/oidc/mappings", s.requireAuth(http.HandlerFunc(s.getOIDCMappings), false))
	mux.Handle("PUT /api/v1/admin/oidc/mappings", s.requireAuth(http.HandlerFunc(s.putOIDCMappings), false))
	mux.Handle("GET /api/v1/admin/approval-policies", s.requireAuth(http.HandlerFunc(s.listPolicies), false))
	mux.Handle("POST /api/v1/admin/approval-policies", s.requireAuth(http.HandlerFunc(s.savePolicy), false))
	mux.Handle("PUT /api/v1/admin/approval-policies/{id}", s.requireAuth(http.HandlerFunc(s.savePolicy), false))
	mux.Handle("DELETE /api/v1/admin/approval-policies/{id}", s.requireAuth(http.HandlerFunc(s.deletePolicy), false))
	mux.Handle("GET /api/v1/admin/users", s.requireAuth(http.HandlerFunc(s.adminUsers), false))
	mux.Handle("POST /api/v1/admin/users", s.requireAuth(http.HandlerFunc(s.createUser), false))
	mux.Handle("PUT /api/v1/admin/users/{id}", s.requireAuth(http.HandlerFunc(s.updateUser), false))
	mux.Handle("DELETE /api/v1/admin/users/{id}", s.requireAuth(http.HandlerFunc(s.deleteUser), false))
	mux.Handle("PUT /api/v1/admin/users/{id}/roles", s.requireAuth(http.HandlerFunc(s.setUserRoles), false))
	mux.Handle("POST /api/v1/admin/users/{id}/password", s.requireAuth(http.HandlerFunc(s.resetUserPassword), false))
	mux.Handle("GET /api/v1/admin/roles", s.requireAuth(http.HandlerFunc(s.adminRoles), false))
	mux.Handle("POST /api/v1/admin/roles", s.requireAuth(http.HandlerFunc(s.createRole), false))
	mux.Handle("PUT /api/v1/admin/roles/{id}", s.requireAuth(http.HandlerFunc(s.updateRole), false))
	mux.Handle("DELETE /api/v1/admin/roles/{id}", s.requireAuth(http.HandlerFunc(s.deleteRole), false))
	mux.Handle("GET /api/v1/admin/permissions", s.requireAuth(http.HandlerFunc(s.adminPermissionCatalog), false))
	mux.Handle("GET /api/v1/admin/organizations", s.requireAuth(http.HandlerFunc(s.adminOrganizations), false))
	mux.Handle("POST /api/v1/admin/organizations", s.requireAuth(http.HandlerFunc(s.createOrganization), false))
	mux.Handle("PUT /api/v1/admin/organizations/{id}", s.requireAuth(http.HandlerFunc(s.updateOrganization), false))
	mux.Handle("DELETE /api/v1/admin/organizations/{id}", s.requireAuth(http.HandlerFunc(s.deleteOrganization), false))
	mux.Handle("GET /api/v1/admin/audit", s.requireAuth(http.HandlerFunc(s.adminAudit), false))
	mux.Handle("GET /api/v1/admin/operations", s.requireAuth(http.HandlerFunc(s.adminOperations), false))
	mux.Handle("GET /api/v1/admin/operations/support-bundle", s.requireAuth(http.HandlerFunc(s.adminSupportBundle), false))
	mux.Handle("GET /api/v1/admin/data-quality", s.requireAuth(http.HandlerFunc(s.adminDataQuality), false))
	mux.Handle("GET /api/v1/admin/configuration/export", s.requireAuth(http.HandlerFunc(s.exportConfiguration), false))
	mux.Handle("POST /api/v1/admin/configuration/preview", s.requireAuth(http.HandlerFunc(s.previewConfiguration), false))
	mux.Handle("POST /api/v1/admin/configuration/apply", s.requireAuth(http.HandlerFunc(s.applyConfiguration), false))
	mux.Handle("GET /api/v1/admin/custom-fields", s.requireAuth(http.HandlerFunc(s.customFields), false))
	mux.Handle("POST /api/v1/admin/custom-fields", s.requireAuth(http.HandlerFunc(s.createCustomField), false))
	mux.Handle("PUT /api/v1/admin/custom-fields/{id}", s.requireAuth(http.HandlerFunc(s.updateCustomField), false))
	mux.Handle("DELETE /api/v1/admin/custom-fields/{id}", s.requireAuth(http.HandlerFunc(s.deleteCustomField), false))
	mux.Handle("GET /api/v1/admin/pipelines", s.requireAuth(http.HandlerFunc(s.adminPipelines), false))
	mux.Handle("POST /api/v1/admin/pipelines", s.requireAuth(http.HandlerFunc(s.createPipeline), false))
	mux.Handle("PUT /api/v1/admin/pipelines/{id}", s.requireAuth(http.HandlerFunc(s.updatePipeline), false))
	mux.Handle("DELETE /api/v1/admin/pipelines/{id}", s.requireAuth(http.HandlerFunc(s.deletePipeline), false))
	mux.Handle("POST /api/v1/admin/pipelines/{id}/stages", s.requireAuth(http.HandlerFunc(s.createStage), false))
	mux.Handle("PUT /api/v1/admin/stages/{id}", s.requireAuth(http.HandlerFunc(s.updateStage), false))
	mux.Handle("DELETE /api/v1/admin/stages/{id}", s.requireAuth(http.HandlerFunc(s.deleteStage), false))
	mux.Handle("GET /api/v1/admin/sales-execution", s.requireAuth(http.HandlerFunc(s.adminSalesExecution), false))
	mux.Handle("PUT /api/v1/admin/stages/{id}/sales-execution", s.requireAuth(http.HandlerFunc(s.saveSalesExecution), false))
	mux.Handle("GET /api/v1/admin/deal-health-rules", s.requireAuth(http.HandlerFunc(s.adminDealHealthRules), false))
	mux.Handle("PUT /api/v1/admin/deal-health-rules/{id}", s.requireAuth(http.HandlerFunc(s.saveDealHealthRule), false))
	mux.Handle("GET /api/v1/admin/analytics", s.requireAuth(http.HandlerFunc(s.listAnalyticsProviders), false))
	mux.Handle("POST /api/v1/admin/analytics", s.requireAuth(http.HandlerFunc(s.saveAnalyticsProvider), false))
	mux.Handle("PUT /api/v1/admin/analytics/{id}", s.requireAuth(http.HandlerFunc(s.saveAnalyticsProvider), false))
	mux.Handle("DELETE /api/v1/admin/analytics/{id}", s.requireAuth(http.HandlerFunc(s.deleteAnalyticsProvider), false))
	mux.Handle("POST /api/v1/admin/analytics/violations/resolve", s.requireAuth(http.HandlerFunc(s.resolveCSPViolation), false))
	mux.Handle("GET /api/v1/admin/voice-categories", s.requireAuth(http.HandlerFunc(s.adminVoiceCategories), false))
	mux.Handle("POST /api/v1/admin/voice-categories", s.requireAuth(http.HandlerFunc(s.createVoiceCategory), false))
	mux.Handle("PUT /api/v1/admin/voice-categories/{id}", s.requireAuth(http.HandlerFunc(s.updateVoiceCategory), false))
	mux.Handle("DELETE /api/v1/admin/voice-categories/{id}", s.requireAuth(http.HandlerFunc(s.deleteVoiceCategory), false))
	mux.Handle("GET /api/v1/admin/personal-keys", s.requireAuth(http.HandlerFunc(s.adminPersonalKeys), false))
	mux.Handle("DELETE /api/v1/admin/personal-keys/{id}", s.requireAuth(http.HandlerFunc(s.adminRevokeKey), false))
	mux.Handle("POST /api/v1/admin/users/{id}/keys/revoke-all", s.requireAuth(http.HandlerFunc(s.adminRevokeAllKeys), false))
	mux.Handle("/", s.spaHandler())
	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" || len(requestID) > 100 {
			requestID = ids.New()
		}
		ctx := httpx.WithRequestID(r.Context(), requestID)
		r = r.WithContext(ctx)
		w.Header().Set("X-Request-ID", requestID)
		correlationID := strings.TrimSpace(r.Header.Get("X-Correlation-ID"))
		if correlationID == "" || len(correlationID) > 100 {
			correlationID = requestID
		}
		w.Header().Set("X-Correlation-ID", correlationID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", s.contentSecurityPolicy(r))
		defer func() {
			if v := recover(); v != nil {
				s.Log.Error("request panic", "panic", v, "stack", string(debug.Stack()), "requestId", requestID)
				httpx.ErrorJSON(w, r, 500, "internal_error", "요청 처리 중 오류가 발생했습니다.", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (s *Server) requireAuth(next http.Handler, mcpChannel bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		p, err := s.Auth.Authenticate(r)
		if err != nil {
			httpx.ErrorJSON(w, r, http.StatusUnauthorized, "authentication_required", "로그인이 필요합니다.", nil)
			return
		}
		if mcpChannel {
			if !p.ChannelAllowed("MCP") {
				httpx.ErrorJSON(w, r, 403, "channel_denied", "MCP 채널이 허용되지 않은 키입니다.", nil)
				return
			}
			if !s.policyEnabled(r.Context(), "mcp", "enabled", true) {
				httpx.ErrorJSON(w, r, 403, "mcp_disabled", "관리자 정책에 의해 MCP가 비활성화되어 있습니다.", nil)
				return
			}
		} else if p.KeyID != "" && !p.ChannelAllowed("REST") {
			httpx.ErrorJSON(w, r, 403, "channel_denied", "REST 채널이 허용되지 않은 키입니다.", nil)
			return
		} else if p.KeyID != "" && !s.policyEnabled(r.Context(), "api", "enabled", true) {
			httpx.ErrorJSON(w, r, 403, "api_disabled", "관리자 정책에 의해 Personal Key API가 비활성화되어 있습니다.", nil)
			return
		}
		namespace, fallbackLimit := "api", 120
		if mcpChannel {
			namespace, fallbackLimit = "mcp", 60
		}
		limit := s.policyLimit(r.Context(), namespace, "rate_limit_per_minute", fallbackLimit)
		identity := p.UserID + ":" + p.AuthMethod + ":" + p.KeyID + ":" + httpx.ClientIP(r)
		if !s.requests.allow(namespace+":"+identity, limit) {
			w.Header().Set("Retry-After", "60")
			httpx.ErrorJSON(w, r, http.StatusTooManyRequests, "rate_limit_exceeded", "분당 요청 한도를 초과했습니다.", nil)
			return
		}
		if p.MustChangePassword && !strings.HasSuffix(r.URL.Path, "/me/password") && !strings.HasSuffix(r.URL.Path, "/auth/logout") && !strings.HasSuffix(r.URL.Path, "/auth/me") {
			httpx.ErrorJSON(w, r, 403, "password_change_required", "계속하려면 초기 비밀번호를 변경해야 합니다.", nil)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && p.AuthMethod != "PERSONAL_KEY" && p.AuthMethod != "OIDC_ACCESS_TOKEN" {
			if r.Header.Get("X-CSRF-Token") == "" || r.Header.Get("X-CSRF-Token") != p.CSRFToken {
				httpx.ErrorJSON(w, r, 403, "csrf_failed", "CSRF 검증에 실패했습니다.", nil)
				return
			}
		}
		r = r.WithContext(auth.WithPrincipal(r.Context(), p))
		rec := &statusRecorder{ResponseWriter: w}
		if !mcpChannel && isMutation(r.Method) && strings.TrimSpace(r.Header.Get("Idempotency-Key")) != "" {
			s.serveIdempotent(rec, r, p, next)
		} else if !mcpChannel && r.Method == http.MethodGet && strings.TrimSpace(r.URL.Query().Get("fields")) != "" {
			s.serveProjected(rec, r, next)
		} else {
			next.ServeHTTP(rec, r)
		}
		if !mcpChannel && strings.HasPrefix(r.URL.Path, "/api/") {
			var key any
			if p.KeyDBID != "" {
				key = p.KeyDBID
			}
			_, _ = s.DB.Exec(context.Background(), `INSERT INTO api_request_logs(id,actor_id,key_id,method,path,status,duration_ms,request_id,ip) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,'')::inet)`, ids.New(), p.UserID, key, r.Method, r.URL.Path, func() int {
				if rec.status == 0 {
					return 200
				}
				return rec.status
			}(), int(time.Since(start).Milliseconds()), httpx.RequestID(r.Context()), httpx.ClientIP(r))
		}
	})
}

func (s *Server) policyEnabled(ctx context.Context, namespace, key string, fallback bool) bool {
	var value bool
	if err := s.DB.QueryRow(ctx, `SELECT (value #>> '{}')::boolean FROM system_settings WHERE namespace=$1 AND key=$2`, namespace, key).Scan(&value); err != nil {
		return fallback
	}
	return value
}

func (s *Server) policyLimit(ctx context.Context, namespace, key string, fallback int) int {
	var value int
	if err := s.DB.QueryRow(ctx, `SELECT (value #>> '{}')::integer FROM system_settings WHERE namespace=$1 AND key=$2`, namespace, key).Scan(&value); err != nil || value < 0 {
		return fallback
	}
	return value
}

func isMutation(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch
}

func projectionFields(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	if len(parts) == 0 || len(parts) > 50 {
		return nil, errors.New("fields must contain between 1 and 50 field names")
	}
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		field := strings.TrimSpace(part)
		if field == "" || len(field) > 80 {
			return nil, errors.New("invalid field selection")
		}
		for _, char := range field {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_') {
				return nil, errors.New("invalid field selection")
			}
		}
		if !seen[field] {
			out = append(out, field)
			seen[field] = true
		}
	}
	return out, nil
}

func projectObject(value map[string]any, fields []string) map[string]any {
	out := make(map[string]any, len(fields))
	for _, field := range fields {
		if selected, ok := value[field]; ok {
			out[field] = selected
		}
	}
	return out
}

func (s *Server) serveProjected(w http.ResponseWriter, r *http.Request, next http.Handler) {
	fields, err := projectionFields(r.URL.Query().Get("fields"))
	if err != nil {
		httpx.ErrorJSON(w, r, http.StatusBadRequest, "invalid_fields", err.Error(), nil)
		return
	}
	recorder := httptest.NewRecorder()
	next.ServeHTTP(recorder, r)
	result := recorder.Result()
	defer result.Body.Close()
	body := recorder.Body.Bytes()
	if result.StatusCode >= 200 && result.StatusCode < 300 && json.Valid(body) {
		var payload any
		if json.Unmarshal(body, &payload) == nil {
			switch value := payload.(type) {
			case map[string]any:
				if items, ok := value["items"].([]any); ok {
					selected := make([]any, 0, len(items))
					for _, item := range items {
						if object, ok := item.(map[string]any); ok {
							selected = append(selected, projectObject(object, fields))
						}
					}
					value["items"] = selected
					body, _ = json.Marshal(value)
				} else {
					body, _ = json.Marshal(projectObject(value, fields))
				}
			case []any:
				selected := make([]any, 0, len(value))
				for _, item := range value {
					if object, ok := item.(map[string]any); ok {
						selected = append(selected, projectObject(object, fields))
					}
				}
				body, _ = json.Marshal(selected)
			}
		}
	}
	for name, values := range result.Header {
		if strings.EqualFold(name, "Content-Length") {
			continue
		}
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(result.StatusCode)
	_, _ = w.Write(body)
}

func (s *Server) serveIdempotent(w http.ResponseWriter, r *http.Request, p *auth.Principal, next http.Handler) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) < 8 || len(key) > 200 {
		httpx.ErrorJSON(w, r, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key는 8~200자여야 합니다.", nil)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		httpx.ErrorJSON(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "요청 본문이 너무 큽니다.", nil)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	sum := sha256.Sum256(append([]byte(r.Method+"\n"+r.URL.RequestURI()+"\n"), body...))
	requestHash := hex.EncodeToString(sum[:])
	var storedHash string
	var status int
	var storedBody []byte
	err = s.DB.QueryRow(r.Context(), `SELECT request_hash,status_code,response_body FROM idempotency_keys WHERE user_id=$1 AND key=$2 AND expires_at>now()`, p.UserID, key).Scan(&storedHash, &status, &storedBody)
	if err == nil {
		if storedHash != requestHash {
			httpx.ErrorJSON(w, r, http.StatusConflict, "idempotency_conflict", "같은 Idempotency-Key가 다른 요청에 사용되었습니다.", nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Idempotency-Replayed", "true")
		w.WriteHeader(status)
		_, _ = w.Write(storedBody)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		s.serviceError(w, r, err)
		return
	}
	recorder := httptest.NewRecorder()
	next.ServeHTTP(recorder, r)
	result := recorder.Result()
	defer result.Body.Close()
	responseBody := recorder.Body.Bytes()
	if result.StatusCode < 500 && len(responseBody) > 0 && json.Valid(responseBody) {
		_, _ = s.DB.Exec(r.Context(), `INSERT INTO idempotency_keys(user_id,key,request_hash,status_code,response_body,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '24 hours') ON CONFLICT(user_id,key) DO NOTHING`, p.UserID, key, requestHash, result.StatusCode, responseBody)
	}
	for name, values := range result.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(result.StatusCode)
	_, _ = w.Write(responseBody)
}

func (s *Server) meta(r *http.Request) crm.RequestMeta {
	return crm.RequestMeta{Channel: func() string {
		p := auth.FromContext(r.Context())
		if p != nil && (p.AuthMethod == "PERSONAL_KEY" || p.AuthMethod == "OIDC_ACCESS_TOKEN") {
			return "API"
		}
		return "WEB"
	}(), IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()}
}
func principal(r *http.Request) *auth.Principal { return auth.FromContext(r.Context()) }
func (s *Server) serviceError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	status := http.StatusBadRequest
	code := "invalid_request"
	if errors.Is(err, pgx.ErrNoRows) || strings.Contains(msg, "not found") {
		status = 404
		code = "not_found"
		if errors.Is(err, pgx.ErrNoRows) || msg == "no rows in result set" {
			// The driver's phrasing reached API and MCP clients verbatim.
			msg = "요청한 데이터를 찾을 수 없거나 접근 권한이 없습니다."
		}
	} else if strings.Contains(msg, "permission") || strings.Contains(msg, "access denied") || strings.Contains(msg, "designated approver") {
		status = 403
		code = "forbidden"
	} else if strings.Contains(msg, "another user") || strings.Contains(msg, "already") || strings.Contains(msg, "pending") {
		status = 409
		code = "conflict"
	}
	if status >= 500 {
		s.Log.Error("service error", "error", err, "requestId", httpx.RequestID(r.Context()))
		msg = "서버 오류가 발생했습니다."
	}
	httpx.ErrorJSON(w, r, status, code, msg, nil)
}
