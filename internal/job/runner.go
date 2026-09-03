package job

import (
	"context"
	"log/slog"
	"time"

	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maintenanceLockID int64 = 733_541_122_020_269

// session is the part of one pooled connection the maintenance pass uses. An
// advisory lock belongs to the session that took it, so the lock, the work it
// guards and the unlock all have to run on the same connection.
type session interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type Runner struct {
	DB         *pgxpool.Pool
	Log        *slog.Logger
	InstanceID string
	Snapshot   func(context.Context) error
	// Analyze rebuilds signals, risks and recommendations. It throttles itself
	// on the last completed run, so calling it every tick is safe.
	Analyze func(context.Context) error
}

func New(db *pgxpool.Pool, log *slog.Logger) *Runner {
	return &Runner{DB: db, Log: log, InstanceID: ids.New()}
}
func (r *Runner) Run(ctx context.Context) {
	r.maintenance(ctx)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.maintenance(ctx)
		}
	}
}
func (r *Runner) maintenance(ctx context.Context) {
	// Taking the lock through the pool left it held by whichever connection the
	// pool happened to hand out, and sent the unlock to another one: the lock
	// leaked and every later pass that drew a different connection was skipped.
	conn, err := r.DB.Acquire(ctx)
	if err != nil {
		if ctx.Err() == nil {
			r.Log.Error("acquire maintenance connection", "error", err)
		}
		return
	}
	defer conn.Release()
	r.maintain(ctx, conn)
}
func (r *Runner) maintain(ctx context.Context, db session) {
	// Advisory locking keeps maintenance single-writer when several Relio
	// containers share one PostgreSQL database.
	var locked bool
	if err := db.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, maintenanceLockID).Scan(&locked); err != nil {
		r.Log.Error("take maintenance lock", "error", err)
		return
	}
	if !locked {
		return
	}
	defer func() { _, _ = db.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, maintenanceLockID) }()
	if tag, err := db.Exec(ctx, `UPDATE personal_keys SET status='REVOKED',revoked_at=now() WHERE status='ROTATING' AND grace_expires_at<=now()`); err != nil {
		r.Log.Error("expire rotated keys", "error", err)
	} else if tag.RowsAffected() > 0 {
		r.Log.Info("expired rotated keys", "count", tag.RowsAffected())
	}
	_, _ = db.Exec(ctx, `UPDATE personal_keys SET status='EXPIRED' WHERE status IN ('ACTIVE','ROTATING') AND expires_at<=now()`)
	_, _ = db.Exec(ctx, `DELETE FROM sessions WHERE expires_at<now()-interval '1 day'`)
	_, _ = db.Exec(ctx, `DELETE FROM oidc_login_states WHERE expires_at<now()`)
	_, _ = db.Exec(ctx, `DELETE FROM idempotency_keys WHERE expires_at<now()`)
	if r.Snapshot != nil {
		if err := r.Snapshot(ctx); err != nil {
			r.Log.Error("capture forecast snapshot", "error", err)
		}
	}
	if r.Analyze != nil {
		if err := r.Analyze(ctx); err != nil {
			r.Log.Error("run intelligence analysis", "error", err)
		}
	}
}
