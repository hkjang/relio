package job

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeRow struct {
	locked bool
	err    error
}

func (row fakeRow) Scan(dest ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(dest) == 1 {
		if target, ok := dest[0].(*bool); ok {
			*target = row.locked
		}
	}
	return nil
}

// fakeSession stands in for the one connection the pass runs on, so a test can
// see the whole ordered statement list the connection received.
type fakeSession struct {
	locked     bool
	lockErr    error
	rotated    string
	statements []string
}

func (s *fakeSession) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	s.statements = append(s.statements, sql)
	return fakeRow{locked: s.locked, err: s.lockErr}
}

func (s *fakeSession) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	s.statements = append(s.statements, sql)
	if strings.Contains(sql, "REVOKED") && s.rotated != "" {
		return pgconn.NewCommandTag(s.rotated), nil
	}
	return pgconn.CommandTag{}, nil
}

func testRunner(session *fakeSession) *Runner {
	runner := &Runner{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	runner.Snapshot = func(context.Context) error {
		session.statements = append(session.statements, "snapshot")
		return nil
	}
	runner.Analyze = func(context.Context) error {
		session.statements = append(session.statements, "analyze")
		return nil
	}
	return runner
}

// The unlock has to be the last thing the pass does: releasing it before the
// snapshot and the analysis would let a second container run both at once.
func TestMaintenanceHoldsTheLockForTheWholePass(t *testing.T) {
	session := &fakeSession{locked: true, rotated: "UPDATE 2"}
	testRunner(session).maintain(context.Background(), session)

	if len(session.statements) == 0 || !strings.Contains(session.statements[0], "pg_try_advisory_lock") {
		t.Fatalf("the pass must take the lock first, got %v", session.statements)
	}
	last := session.statements[len(session.statements)-1]
	if !strings.Contains(last, "pg_advisory_unlock") {
		t.Fatalf("the pass must unlock last, got %q", last)
	}
	for _, want := range []string{"personal_keys", "sessions", "oidc_login_states", "idempotency_keys", "snapshot", "analyze"} {
		if !strings.Contains(strings.Join(session.statements, "\n"), want) {
			t.Errorf("the pass never ran %q: %v", want, session.statements)
		}
	}
}

// A pass that loses the race leaves the database alone; unlocking a lock this
// connection does not hold would only make PostgreSQL warn.
func TestMaintenanceStopsWhenAnotherInstanceHoldsTheLock(t *testing.T) {
	session := &fakeSession{locked: false}
	runner := testRunner(session)
	runner.Snapshot = func(context.Context) error {
		t.Error("snapshot ran without the lock")
		return nil
	}
	runner.maintain(context.Background(), session)

	if len(session.statements) != 1 {
		t.Fatalf("only the lock attempt may run, got %v", session.statements)
	}
}

func TestMaintenanceStopsWhenTheLockQueryFails(t *testing.T) {
	session := &fakeSession{locked: true, lockErr: errors.New("connection reset")}
	testRunner(session).maintain(context.Background(), session)

	if len(session.statements) != 1 {
		t.Fatalf("a failed lock query must stop the pass, got %v", session.statements)
	}
}

// A session advisory lock belongs to the connection that took it. Running any
// of these through the pool hands each statement an arbitrary connection, so
// the lock leaks and later passes are skipped.
func TestSessionAdvisoryLocksNeverRunOnAPool(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(body), "\n") {
			locking := strings.Contains(line, "pg_advisory_lock(") ||
				strings.Contains(line, "pg_try_advisory_lock(") ||
				strings.Contains(line, "pg_advisory_unlock(")
			if !locking {
				continue
			}
			for _, pooled := range []string{"DB.Exec", "DB.Query", "pool.Exec", "pool.Query", "Pool.Exec", "Pool.Query"} {
				if strings.Contains(line, pooled) {
					t.Errorf("%s:%d takes a session advisory lock through %s; acquire one connection first", path, i+1, pooled)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
