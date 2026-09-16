package mail

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Notifier is the part of the service a domain package needs: it never blocks
// and never fails the caller's request. A nil *Service satisfies it by doing
// nothing, so callers do not have to check whether mail is wired.
type Notifier interface {
	Notify(ctx context.Context, notification Notification, actorID string, recipients []string)
}

// Directory resolves account identifiers to email addresses. The user table
// already exists, so mail borrows one lookup and keeps no roster of its own.
type Directory interface {
	LookupEmails(ctx context.Context, userIDs []string) (map[string]string, error)
}

// Delivery is one attempt, kept whether or not it worked. It never carries the
// body: subject and recipient are what an administrator needs to answer
// "it never arrived", and a log of bodies would be a leak of its own.
type Delivery struct {
	ID           string    `json:"id"`
	Event        string    `json:"event"`
	Recipient    string    `json:"recipient"`
	Subject      string    `json:"subject"`
	EntityType   string    `json:"entityType,omitempty"`
	EntityID     string    `json:"entityId,omitempty"`
	ActorID      string    `json:"actorId,omitempty"`
	Status       string    `json:"status"`
	Attempts     int       `json:"attempts"`
	ErrorMessage string    `json:"errorMessage,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type Summary struct {
	Total  int            `json:"total"`
	Status map[string]int `json:"status"`
}

type Page struct {
	Items   []Delivery `json:"items"`
	Summary Summary    `json:"summary"`
}

type Service struct {
	DB        *pgxpool.Pool
	Settings  settingsProvider
	Directory Directory
	Log       *slog.Logger
	// send is the transport; tests swap it for a recorder.
	send func(context.Context, Config, Message) error
	now  func() time.Time
	// pending counts background deliveries so tests can wait for them, and
	// observe sees every completed delivery so they can assert on the log
	// without a database.
	pending sync.WaitGroup
	observe func(Delivery)
}

func NewService(db *pgxpool.Pool, settings settingsProvider, directory Directory, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{DB: db, Settings: settings, Directory: directory, Log: log, send: Deliver, now: func() time.Time { return time.Now().UTC() }}
}

// SetSender replaces the transport, which lets tests drive the service without
// a real relay.
func (s *Service) SetSender(sender func(context.Context, Config, Message) error) { s.send = sender }

func (s *Service) Config(ctx context.Context) (Config, error) { return ReadConfig(ctx, s.Settings) }

// Notify resolves the recipients and sends in the background. The caller's
// request is never delayed and never fails because of mail: the settings read,
// the directory lookup and the relay all run after this returns. Recipients
// without an address are skipped quietly; the actor never receives mail about
// their own action.
func (s *Service) Notify(ctx context.Context, notification Notification, actorID string, recipients []string) {
	if s == nil || len(recipients) == 0 {
		return
	}
	background := context.WithoutCancel(ctx)
	s.pending.Add(1)
	go func() {
		defer s.pending.Done()
		s.dispatch(background, notification, actorID, recipients)
	}()
}

// wait blocks until every background delivery has finished. Tests only.
func (s *Service) wait() { s.pending.Wait() }

// dispatch is Notify's background half. Its return is the number of
// deliveries recorded, which the renewal digest uses to decide whether a
// notice has actually been given.
func (s *Service) dispatch(ctx context.Context, notification Notification, actorID string, recipients []string) int {
	config, err := s.Config(ctx)
	if err != nil {
		s.Log.Warn("mail settings were not read", "event", notification.Event, "error", err)
		return 0
	}
	if !config.Enabled || !config.Allows(notification.Event) {
		return 0
	}
	addresses := s.resolve(ctx, recipients, actorID)
	if len(addresses) == 0 {
		return 0
	}
	// Enabled but incomplete: record why nothing left, once per recipient, so
	// the delivery log rather than a server log explains the silence.
	invalid := config.Validate()
	body := notification.Render(config)
	for _, address := range addresses {
		delivery := Delivery{
			ID: ids.New(), Event: notification.Event, Recipient: address, Subject: notification.Subject,
			EntityType: notification.EntityType, EntityID: notification.EntityID, ActorID: actorID, Status: "queued", CreatedAt: s.now(), UpdatedAt: s.now(),
		}
		s.record(ctx, delivery)
		if invalid != nil {
			s.complete(ctx, delivery, invalid)
			continue
		}
		s.deliver(ctx, delivery, config, Message{To: address, Subject: notification.Subject, Body: body})
	}
	return len(addresses)
}

// SendNow delivers immediately and reports the outcome, which is what the
// administrator's test button needs.
func (s *Service) SendNow(ctx context.Context, notification Notification, actorID, recipient string) error {
	config, err := s.Config(ctx)
	if err != nil {
		return err
	}
	if !config.Enabled {
		return ErrDisabled
	}
	delivery := Delivery{ID: ids.New(), Event: notification.Event, Recipient: recipient, Subject: notification.Subject,
		ActorID: actorID, Status: "queued", CreatedAt: s.now(), UpdatedAt: s.now()}
	s.record(ctx, delivery)
	sendContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Timeout+5*time.Second)
	defer cancel()
	delivery.Attempts = 1
	err = s.send(sendContext, config, Message{To: recipient, Subject: notification.Subject, Body: notification.Render(config)})
	s.complete(sendContext, delivery, err)
	return err
}

// deliver retries once, because a relay that briefly refuses a connection is
// common and losing the notification is worse than a short wait.
func (s *Service) deliver(parent context.Context, delivery Delivery, config Config, message Message) {
	ctx, cancel := context.WithTimeout(parent, 2*config.Timeout+15*time.Second)
	defer cancel()
	var err error
	for attempt := 1; attempt <= 2; attempt++ {
		delivery.Attempts = attempt
		attemptContext, cancelAttempt := context.WithTimeout(ctx, config.Timeout+5*time.Second)
		err = s.send(attemptContext, config, message)
		cancelAttempt()
		if err == nil {
			break
		}
		if attempt == 1 {
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}
	s.complete(ctx, delivery, err)
}

func (s *Service) complete(ctx context.Context, delivery Delivery, cause error) {
	status, message := "sent", ""
	if cause != nil {
		status, message = "failed", cause.Error()
		s.Log.Warn("notification mail failed", "event", delivery.Event, "recipient", delivery.Recipient, "error", cause)
	}
	delivery.Status, delivery.ErrorMessage, delivery.Attempts, delivery.UpdatedAt = status, trim(message, 1000), max(delivery.Attempts, 1), s.now()
	if s.observe != nil {
		s.observe(delivery)
	}
	if s.DB == nil {
		return
	}
	if _, err := s.DB.Exec(context.WithoutCancel(ctx), `UPDATE mail_deliveries SET status=$2,attempts=GREATEST(attempts,$3),error_message=NULLIF($4,''),updated_at=$5 WHERE id=$1`,
		delivery.ID, delivery.Status, delivery.Attempts, delivery.ErrorMessage, delivery.UpdatedAt); err != nil {
		s.Log.Warn("mail delivery status was not recorded", "error", err)
	}
}

func (s *Service) record(ctx context.Context, delivery Delivery) {
	if s.DB == nil {
		return
	}
	var actor any
	if strings.TrimSpace(delivery.ActorID) != "" {
		actor = delivery.ActorID
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO mail_deliveries(id,event,recipient,subject,entity_type,entity_id,actor_id,status,attempts,created_at,updated_at)
		VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),$7,'queued',0,$8,$8)`,
		delivery.ID, delivery.Event, delivery.Recipient, trim(delivery.Subject, 300), delivery.EntityType, delivery.EntityID, actor, delivery.CreatedAt); err != nil {
		s.Log.Warn("mail delivery was not recorded", "error", err)
	}
}

// resolve turns account identifiers into unique addresses, dropping the actor
// so nobody is told about their own action.
func (s *Service) resolve(ctx context.Context, recipients []string, actorID string) []string {
	wanted := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		trimmed := strings.TrimSpace(recipient)
		if trimmed == "" || strings.EqualFold(trimmed, strings.TrimSpace(actorID)) {
			continue
		}
		wanted = append(wanted, trimmed)
	}
	if len(wanted) == 0 || s.Directory == nil {
		return nil
	}
	emails, err := s.Directory.LookupEmails(ctx, wanted)
	if err != nil {
		s.Log.Warn("mail recipients were not resolved", "error", err)
		return nil
	}
	seen, addresses := map[string]struct{}{}, make([]string, 0, len(wanted))
	for _, recipient := range wanted {
		address := strings.TrimSpace(emails[strings.ToLower(recipient)])
		if address == "" {
			continue
		}
		key := strings.ToLower(address)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		addresses = append(addresses, address)
	}
	return addresses
}

// Filter narrows the delivery log.
type Filter struct {
	Status string
	Event  string
	Limit  int
}

// Deliveries lists what was sent, newest first, with a status breakdown of the
// whole log so the screen can say "3 failed" without paging.
func (s *Service) Deliveries(ctx context.Context, filter Filter) (Page, error) {
	limit := filter.Limit
	if limit < 1 || limit > 200 {
		limit = 50
	}
	page := Page{Items: []Delivery{}, Summary: Summary{Status: map[string]int{}}}
	if s == nil || s.DB == nil {
		return page, nil
	}
	rows, err := s.DB.Query(ctx, `SELECT id::text,event,recipient,subject,COALESCE(entity_type,''),COALESCE(entity_id,''),COALESCE(actor_id::text,''),status,attempts,COALESCE(error_message,''),created_at,updated_at
		FROM mail_deliveries WHERE ($1='' OR status=$1) AND ($2='' OR event=$2) ORDER BY created_at DESC, id LIMIT $3`,
		strings.TrimSpace(filter.Status), strings.TrimSpace(filter.Event), limit)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item Delivery
		if err := rows.Scan(&item.ID, &item.Event, &item.Recipient, &item.Subject, &item.EntityType, &item.EntityID, &item.ActorID,
			&item.Status, &item.Attempts, &item.ErrorMessage, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return Page{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	counts, err := s.DB.Query(ctx, `SELECT status, count(*) FROM mail_deliveries GROUP BY 1`)
	if err != nil {
		return Page{}, err
	}
	defer counts.Close()
	for counts.Next() {
		var key string
		var count int
		if err := counts.Scan(&key, &count); err != nil {
			return Page{}, err
		}
		page.Summary.Status[key] = count
		page.Summary.Total += count
	}
	return page, counts.Err()
}

func trim(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
