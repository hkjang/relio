package job

import (
	"context"
	"log/slog"
	"time"

	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Runner struct {
	DB         *pgxpool.Pool
	Log        *slog.Logger
	InstanceID string
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
	// Advisory locking keeps maintenance single-writer when several Relio
	// containers share one PostgreSQL database.
	var locked bool
	if err := r.DB.QueryRow(ctx, `SELECT pg_try_advisory_lock(733541122020269)`).Scan(&locked); err != nil || !locked {
		return
	}
	defer r.DB.Exec(context.Background(), `SELECT pg_advisory_unlock(733541122020269)`)
	if tag, err := r.DB.Exec(ctx, `UPDATE personal_keys SET status='REVOKED',revoked_at=now() WHERE status='ROTATING' AND grace_expires_at<=now()`); err != nil {
		r.Log.Error("expire rotated keys", "error", err)
	} else if tag.RowsAffected() > 0 {
		r.Log.Info("expired rotated keys", "count", tag.RowsAffected())
	}
	_, _ = r.DB.Exec(ctx, `UPDATE personal_keys SET status='EXPIRED' WHERE status IN ('ACTIVE','ROTATING') AND expires_at<=now()`)
	_, _ = r.DB.Exec(ctx, `DELETE FROM sessions WHERE expires_at<now()-interval '1 day'`)
	_, _ = r.DB.Exec(ctx, `DELETE FROM oidc_login_states WHERE expires_at<now()`)
	_, _ = r.DB.Exec(ctx, `DELETE FROM idempotency_keys WHERE expires_at<now()`)
}
