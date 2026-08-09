package audit

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Event struct {
	ActorID, ActorName, Channel, Action, Resource, ResourceID string
	Before, After, Metadata                                   any
	IP, RequestID, UserAgent                                  string
}

type Service struct {
	DB  *pgxpool.Pool
	Log *slog.Logger
}

func nullableJSON(v any) []byte {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

func (s *Service) Record(ctx context.Context, e Event) {
	var actorID any
	if e.ActorID != "" {
		actorID = e.ActorID
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO audit_logs(id,actor_id,actor_name,channel,action,resource,resource_id,before_data,after_data,ip,request_id,user_agent,metadata)
		VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,NULLIF($10,'')::inet,$11,$12,$13)`, ids.New(), actorID, e.ActorName, e.Channel, e.Action, e.Resource, e.ResourceID, nullableJSON(e.Before), nullableJSON(e.After), e.IP, e.RequestID, e.UserAgent, nullableJSON(e.Metadata))
	if err != nil {
		s.Log.Error("write audit event", "error", err, "action", e.Action, "resource", e.Resource)
	}
}
