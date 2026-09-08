// Package timezone resolves the system.timezone setting an administrator picks
// into a *time.Location, so the dates this server derives from the current
// instant are the dates the business is actually on.
//
// The admin screen has offered Asia/Seoul, UTC and Asia/Tokyo since the first
// migration, but no Go code read the stored value: every date came from the
// process clock, which is UTC in the container. Between 00:00 and 09:00 in
// Seoul that clock is still on yesterday, so a contract created at 08:00 was
// numbered C-<yesterday>, a quotation likewise, the customer-voice export was
// named after yesterday, and the "오늘" queue compared a next action due today
// against yesterday's midnight and called it overdue.
package timezone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	// The zone database is embedded rather than read from the host so the
	// setting keeps working on any base image; a missing /usr/share/zoneinfo
	// would otherwise silently degrade every date back to the process clock.
	_ "time/tzdata"

	"github.com/jackc/pgx/v5"
)

// DefaultName is the zone the first migration stores and the one this package
// falls back to. Falling back to UTC instead would reintroduce, for the most
// common deployment, exactly the behaviour this package exists to remove.
const DefaultName = "Asia/Seoul"

// cacheTTL keeps a settings row out of the path of every contract number while
// still letting an administrator's change take effect without a restart.
const cacheTTL = 30 * time.Second

var defaultLocation = func() *time.Location {
	loc, err := time.LoadLocation(DefaultName)
	if err != nil {
		return time.UTC
	}
	return loc
}()

// Default is the location used when the setting cannot be read or does not name
// a zone this build knows.
func Default() *time.Location { return defaultLocation }

// Resolve turns a stored setting value into a location. It always returns a
// usable location; the error only says why the fallback was needed.
func Resolve(name string) (*time.Location, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return Default(), errors.New("system.timezone is empty")
	}
	// time.LoadLocation("Local") answers with the process zone, which is the one
	// thing this setting exists to pin down, so it is not an acceptable value.
	if strings.EqualFold(trimmed, "Local") {
		return Default(), errors.New(`system.timezone must name an IANA zone, not "Local"`)
	}
	loc, err := time.LoadLocation(trimmed)
	if err != nil {
		return Default(), fmt.Errorf("system.timezone %q is not a known IANA zone name: %w", name, err)
	}
	return loc, nil
}

// DateOf is the calendar date loc is on at instant t, as midnight UTC.
// PostgreSQL DATE columns arrive that way — pgx decodes them at midnight UTC —
// so a value from here compares with one directly and encodes back to a DATE
// parameter unchanged.
func DateOf(t time.Time, loc *time.Location) time.Time {
	local := t.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}

// Querier is the part of *pgxpool.Pool this package uses.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Loader reads system.timezone and caches the result. The zero value and a nil
// *Loader both answer with the default, so a service that has not been wired to
// one still produces Seoul dates rather than panicking.
type Loader struct {
	DB  Querier
	Log *slog.Logger

	mu       sync.Mutex
	cached   *time.Location
	cachedAt time.Time
	warned   string
}

// Location is the configured zone.
func (l *Loader) Location(ctx context.Context) *time.Location {
	if l == nil || l.DB == nil {
		return Default()
	}
	l.mu.Lock()
	cached, at := l.cached, l.cachedAt
	l.mu.Unlock()
	if cached != nil && time.Since(at) < cacheTTL {
		return cached
	}
	loc, err := l.load(ctx)
	if err != nil {
		// A settings read that fails must not move the calendar under a caller:
		// keep the last known zone, and the default before there is one.
		l.warn("system.timezone could not be read", err)
		if cached != nil {
			return cached
		}
		return Default()
	}
	l.mu.Lock()
	l.cached, l.cachedAt = loc, time.Now()
	l.mu.Unlock()
	return loc
}

// Now is the current instant in the configured zone.
func (l *Loader) Now(ctx context.Context) time.Time { return time.Now().In(l.Location(ctx)) }

// Date is today's calendar date in the configured zone, as midnight UTC.
func (l *Loader) Date(ctx context.Context) time.Time {
	return DateOf(time.Now(), l.Location(ctx))
}

// DateStamp is today's calendar date in the configured zone as YYYYMMDD, the
// form contract numbers, quotation numbers and export filenames use.
func (l *Loader) DateStamp(ctx context.Context) string { return l.Date(ctx).Format("20060102") }

func (l *Loader) load(ctx context.Context) (*time.Location, error) {
	var raw []byte
	err := l.DB.QueryRow(ctx, `SELECT value FROM system_settings WHERE namespace='system' AND key='timezone'`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		// A deleted setting means the compiled-in default applies again, the
		// same contract admin.SettingsService.Delete documents.
		return Default(), nil
	}
	if err != nil {
		return nil, err
	}
	var name string
	if err = json.Unmarshal(raw, &name); err != nil {
		// A stored value of the wrong shape is a configuration problem, not a
		// transient one: cache the fallback instead of re-reading it forever.
		l.warn("system.timezone is not a JSON string", err)
		return Default(), nil
	}
	loc, err := Resolve(name)
	if err != nil {
		l.warn("system.timezone is not usable", err)
	}
	return loc, nil
}

// warn reports a configuration problem once per distinct cause. The value is
// re-read every cacheTTL, so logging every failure would repeat the same line
// twice a minute for as long as the setting stays wrong.
func (l *Loader) warn(message string, cause error) {
	text := message + ": " + cause.Error()
	l.mu.Lock()
	repeat := l.warned == text
	l.warned = text
	l.mu.Unlock()
	if repeat || l.Log == nil {
		return
	}
	l.Log.Warn(message, "error", cause, "fallback", DefaultName)
}
