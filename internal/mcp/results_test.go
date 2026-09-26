package mcp

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// unreachableQueryError is the error a tool carries when PostgreSQL itself could
// not be reached: a real *pgxpool.Pool aimed at a port nothing answers on,
// queried for real. The listener is opened only to reserve the port, then
// closed. The same construction is in internal/server/service_error_test.go and
// internal/intelligence/health_lookup_test.go — a *pgconn.ConnectError cannot be
// built by hand, because the wrapped error is an unexported field, and a
// stand-in would not prove the production wiring reads the real type. The port
// is returned so a test can assert it appears nowhere in the sentence.
func unreachableQueryError(t *testing.T) (error, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close the reserved port: %v", err)
	}
	config, err := pgxpool.ParseConfig("postgres://relio:relio@" + addr + "/relio?connect_timeout=2&sslmode=disable")
	if err != nil {
		t.Fatalf("parse the pool config: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("build the pool: %v", err)
	}
	t.Cleanup(pool.Close)
	rows, queryErr := pool.Query(context.Background(), "SELECT 1")
	if queryErr == nil {
		rows.Close()
		t.Fatal("a query to a port nobody listens on must fail")
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split the reserved address: %v", err)
	}
	return queryErr, port
}

// captureDefaultLogger points slog's default logger at a buffer for one test, so
// the test can read what sanitizeToolError logged. The function logs through the
// package-level slog rather than an injected logger, so this is the only way to
// see it without changing that signature.
func captureDefaultLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	logs := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}

// TestSanitizeToolErrorFoldsAnUnreachableDatabase pins the one PostgreSQL
// failure that carries no SQLSTATE. The driver never reached the server, so the
// errors.As/"SQLSTATE" test above the table cannot see it and the function fell
// through to its last line — `return message` — handing the model the driver's
// own sentence, which names the database role, the database and the internal
// host:port. The operator keeps the original, in the log.
func TestSanitizeToolErrorFoldsAnUnreachableDatabase(t *testing.T) {
	queryErr, port := unreachableQueryError(t)
	t.Logf("the error under test: %T %v", queryErr, queryErr)
	logs := captureDefaultLogger(t)
	message := sanitizeToolError(fmt.Errorf("담당자 목록을 읽을 수 없습니다: %w", queryErr), "req-42")
	if message != "데이터 처리 중 오류가 발생했습니다. 요청 ID: req-42" {
		t.Fatalf("got %q, want the generic sentence this function already uses for a database error", message)
	}
	for _, secret := range []string{"failed to connect", "user=", "database=", "dial error", "127.0.0.1", port} {
		if strings.Contains(message, secret) {
			t.Fatalf("the sentence given to the model still carries %q: %s", secret, message)
		}
	}
	for _, kept := range []string{"mcp tool database error", "failed to connect", "req-42"} {
		if !strings.Contains(logs.String(), kept) {
			t.Fatalf("log lost %q: %s", kept, logs.String())
		}
	}
}
