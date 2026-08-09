package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hkjang/relio/internal/admin"
	"github.com/hkjang/relio/internal/apikey"
	"github.com/hkjang/relio/internal/approval"
	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/config"
	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/intelligence"
	"github.com/hkjang/relio/internal/job"
	"github.com/hkjang/relio/internal/mcp"
	"github.com/hkjang/relio/internal/oidc"
	"github.com/hkjang/relio/internal/platform/database"
	"github.com/hkjang/relio/internal/platform/secrets"
	"github.com/hkjang/relio/internal/platform/version"
	"github.com/hkjang/relio/internal/server"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		runHealthcheck()
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid startup configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := database.Open(ctx, cfg.PostgresDSN, logger)
	if err != nil {
		logger.Error("PostgreSQL unavailable", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err = database.Migrate(ctx, db); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	secretManager, err := secrets.LoadOrCreate(config.MasterKeyPath)
	if err != nil {
		logger.Error("instance master key unavailable", "error", err)
		os.Exit(1)
	}
	auditService := &audit.Service{DB: db, Log: logger}
	authService := &auth.Service{DB: db, Secrets: secretManager}
	if err = authService.Bootstrap(ctx, cfg); err != nil {
		logger.Error("bootstrap administrator initialization failed", "error", err)
		os.Exit(1)
	}
	crmService := &crm.Service{DB: db, Audit: auditService}
	intelligenceService := &intelligence.Service{DB: db, CRM: crmService, Audit: auditService}
	crmService.StageGuard = intelligenceService
	settingsService := &admin.SettingsService{DB: db, Secrets: secretManager, Audit: auditService}
	keyService := &apikey.Service{DB: db, Secrets: secretManager, Audit: auditService}
	approvalService := &approval.Service{DB: db, Audit: auditService}
	oidcService := &oidc.Service{DB: db, Secrets: secretManager, Auth: authService, Audit: auditService}
	authService.OIDCValidator = oidcService.ValidateAccessToken
	mcpServer := &mcp.Server{DB: db, CRM: crmService, Approvals: approvalService, Intelligence: intelligenceService}
	app := server.New(db, logger, authService, auditService, crmService, settingsService, keyService, approvalService, oidcService, mcpServer, intelligenceService)
	runner := job.New(db, logger)
	runner.Snapshot = intelligenceService.CaptureForecastSnapshots
	go runner.Run(ctx)
	httpServer := &http.Server{Addr: config.ListenAddress, Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 1 << 20}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	info := version.Current()
	logger.Info("Relio started", "address", config.ListenAddress, "version", info.Version, "commit", info.GitCommit, "edition", info.Edition)
	if err = httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("HTTP server stopped unexpectedly", "error", err)
		os.Exit(1)
	}
	logger.Info("Relio stopped")
}

func runHealthcheck() {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:8080/health/ready")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "readiness returned HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}
}
