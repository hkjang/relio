package intelligence

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// unreachablePool is a real *pgxpool.Pool whose server does not exist: the
// listener is opened only to reserve a port nothing is answering on, then
// closed. Every Query through it fails the way a database that went away
// fails, which is the case these lookups used to answer with an empty map.
func unreachablePool(t *testing.T) *pgxpool.Pool {
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

// TestHealthLookupsReportFailureInsteadOfAnsweringNobody pins the reason the
// two lookups carry an error at all. A contacts read that fails used to come
// back as an empty map, which every deal in the set then reads as "no decision
// maker, no champion" — NO_DECISION_MAKER and NO_CHAMPION fire on deals that
// have both, and saveHealthSnapshots writes those verdicts down. Same for the
// stage limits: an unread max_days silently swaps the configured stall
// threshold for the default one.
func TestHealthLookupsReportFailureInsteadOfAnsweringNobody(t *testing.T) {
	service := &Service{DB: unreachablePool(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	roles, err := service.contactRoles(ctx, []string{"11111111-1111-1111-1111-111111111111"})
	if err == nil {
		t.Fatalf("contactRoles answered %v with no error; an unread contacts table must not read as a customer with no decision maker", roles)
	}
	limits, err := service.stageLimits(ctx, []string{"22222222-2222-2222-2222-222222222222"})
	if err == nil {
		t.Fatalf("stageLimits answered %v with no error; an unread pipeline_stages table must not read as a stage with no limit", limits)
	}
}

// TestHealthLookupsSkipTheQueryForAnEmptySet keeps the early return: scoring a
// set with no customers or no stages must not reach the database at all, so a
// pool that cannot answer is still not an error there.
func TestHealthLookupsSkipTheQueryForAnEmptySet(t *testing.T) {
	service := &Service{DB: unreachablePool(t)}
	roles, err := service.contactRoles(context.Background(), nil)
	if err != nil || len(roles) != 0 {
		t.Fatalf("contactRoles(nil) = %v, %v; want an empty map and no query", roles, err)
	}
	limits, err := service.stageLimits(context.Background(), nil)
	if err != nil || len(limits) != 0 {
		t.Fatalf("stageLimits(nil) = %v, %v; want an empty map and no query", limits, err)
	}
}
