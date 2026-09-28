package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// deadEndPool builds a real *pgxpool.Pool aimed at a port nothing answers on.
// The listener is opened only to reserve the port, then closed. It repeats the
// construction inside unreachableQueryError in results_test.go rather than
// reshaping it, so that helper stays as the unreachable-database test left it:
// here the pool is only a way to reach the driver, and what the query fails on
// is the context, not the port.
func deadEndPool(t *testing.T) *pgxpool.Pool {
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
	return pool
}

// interruptedQueryError is the error a tool carries when its query was cut short
// instead of answered: a real *pgxpool.Pool asked for real with a context that
// is already done. pgx hands back ctx.Err() from the connection-acquisition
// step, so the chain carries context.Canceled or context.DeadlineExceeded and
// neither a *pgconn.PgError nor a *pgconn.ConnectError — the shape that used to
// fall through to this function's last line, `return message`.
func interruptedQueryError(t *testing.T, done context.Context, want error) error {
	t.Helper()
	pool := deadEndPool(t)
	rows, queryErr := pool.Query(done, "SELECT 1")
	if queryErr == nil {
		rows.Close()
		t.Fatal("a query on a context that is already done must fail")
	}
	if !errors.Is(queryErr, want) {
		t.Fatalf("the driver returned %T %v, which is not %v — this test no longer reproduces an interrupted query", queryErr, queryErr, want)
	}
	return queryErr
}

// assertInterruptedSentence pins what the model is told about a query that was
// cut short: the generic sentence this function already gives for a database
// error, with the original kept in the log. Handing the model "context canceled"
// invites it to retry or to explain a server-side event as bad input, and it is
// the same wording the REST door now answers with.
func assertInterruptedSentence(t *testing.T, queryErr error, leaked string) {
	t.Helper()
	logs := captureDefaultLogger(t)
	message := sanitizeToolError(fmt.Errorf("담당자 목록을 읽을 수 없습니다: %w", queryErr), "req-42")
	if message != "데이터 처리 중 오류가 발생했습니다. 요청 ID: req-42" {
		t.Fatalf("got %q, want the generic sentence this function already uses for a database error", message)
	}
	for _, secret := range []string{leaked, "context deadline exceeded", "context canceled"} {
		if strings.Contains(message, secret) {
			t.Fatalf("the sentence given to the model still carries %q: %s", secret, message)
		}
	}
	for _, kept := range []string{"mcp tool database error", leaked, "req-42"} {
		if !strings.Contains(logs.String(), kept) {
			t.Fatalf("log lost %q: %s", kept, logs.String())
		}
	}
}

// TestSanitizeToolErrorFoldsACanceledQuery covers the interruption that happens
// today: a client that goes away cancels the request context, and every query
// still in flight on it fails with context.Canceled.
func TestSanitizeToolErrorFoldsACanceledQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	queryErr := interruptedQueryError(t, ctx, context.Canceled)
	t.Logf("the error under test: %T %v", queryErr, queryErr)
	assertInterruptedSentence(t, queryErr, "context canceled")
}

// TestSanitizeToolErrorFoldsAQueryPastItsDeadline is the deadline half. Unlike
// REST, this package does put a deadline on a context (internal/mcp/server.go),
// so a producer exists here.
func TestSanitizeToolErrorFoldsAQueryPastItsDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	queryErr := interruptedQueryError(t, ctx, context.DeadlineExceeded)
	t.Logf("the error under test: %T %v", queryErr, queryErr)
	assertInterruptedSentence(t, queryErr, "context deadline exceeded")
}
