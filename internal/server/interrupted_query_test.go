package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// deadEndPool builds a real *pgxpool.Pool aimed at a port nothing answers on.
// The listener is opened only to reserve the port, then closed. It is the same
// construction as unreachableQueryError in service_error_test.go, kept separate
// so that helper stays exactly as the unreachable-database tests left it: here
// the pool is only a way to reach the driver, and what the query fails on is the
// context, not the port.
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

// interruptedQueryError is the error a request carries when its query was cut
// short instead of answered: a real *pgxpool.Pool asked for real with a context
// that is already done. pgx hands back ctx.Err() from the connection-acquisition
// step, so the chain carries context.Canceled or context.DeadlineExceeded and
// neither a *pgconn.PgError nor a *pgconn.ConnectError — which is exactly the
// shape that used to fall past every case in serviceError's chain to the default
// 400. No PostgreSQL is needed to produce it, and a hand-built context error
// would not prove the production wiring reads what the driver actually returns.
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

// assertInterruptedVerdict pins what a client receives for a query that was cut
// short. It is a server-side event, not a malformed request, so the answer is a
// 500 with the generic sentence: a 4xx tells the caller, the logs and the
// metrics that the input was wrong, and it is also below the status floor that
// keeps serveIdempotent from storing the answer for 24 hours. The driver's own
// words stay in the log, next to the request id.
func assertInterruptedVerdict(t *testing.T, queryErr error, leaked string) {
	t.Helper()
	result := runServiceError(t, fmt.Errorf("담당자 목록을 읽을 수 없습니다: %w", queryErr))
	if result.status != http.StatusInternalServerError || result.code != "internal_error" {
		t.Fatalf("got %d %s, want 500 internal_error (%s)", result.status, result.code, result.body)
	}
	if result.message != "서버 오류가 발생했습니다." {
		t.Fatalf("message: got %q, want the generic sentence", result.message)
	}
	for _, secret := range []string{leaked, "context deadline exceeded", "context canceled", "담당자 목록"} {
		if strings.Contains(result.body, secret) {
			t.Fatalf("response body still carries %q: %s", secret, result.body)
		}
	}
	for _, kept := range []string{leaked, "req-42"} {
		if !strings.Contains(result.logs, kept) {
			t.Fatalf("log lost %q: %s", kept, result.logs)
		}
	}
}

// TestServiceErrorFoldsACanceledQuery covers the interruption this product
// actually produces today: net/http cancels r.Context() when the client goes
// away, so every in-flight query on that request fails with context.Canceled.
func TestServiceErrorFoldsACanceledQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	queryErr := interruptedQueryError(t, ctx, context.Canceled)
	t.Logf("the error under test: %T %v", queryErr, queryErr)
	assertInterruptedVerdict(t, queryErr, "context canceled")
}

// TestServiceErrorFoldsAQueryPastItsDeadline is the defensive half of the pair.
// No REST handler puts a per-request deadline on r.Context() today — the only
// context.WithTimeout callers are internal/mail and internal/mcp — so this is
// pinning the verdict before a producer appears, not a failure seen in the wild.
func TestServiceErrorFoldsAQueryPastItsDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	queryErr := interruptedQueryError(t, ctx, context.DeadlineExceeded)
	t.Logf("the error under test: %T %v", queryErr, queryErr)
	assertInterruptedVerdict(t, queryErr, "context deadline exceeded")
}
