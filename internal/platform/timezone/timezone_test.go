package timezone

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// stubRow answers a single scan the way pgx does, so the loader can be exercised
// without a database.
type stubRow struct {
	value []byte
	err   error
}

func (r stubRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	target, ok := dest[0].(*[]byte)
	if !ok {
		return errors.New("unexpected scan target")
	}
	*target = r.value
	return nil
}

type stubDB struct {
	mu    sync.Mutex
	row   stubRow
	calls int
}

func (d *stubDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	return d.row
}

func (d *stubDB) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func TestResolveAnswersWithAUsableLocation(t *testing.T) {
	// Every case must produce a location: a service asking for today's date has
	// nothing sensible to do with a nil one.
	cases := []struct {
		name    string
		stored  string
		want    string
		wantErr bool
	}{
		{name: "the three zones the admin screen offers", stored: "Asia/Seoul", want: "Asia/Seoul"},
		{name: "UTC", stored: "UTC", want: "UTC"},
		{name: "Tokyo", stored: "Asia/Tokyo", want: "Asia/Tokyo"},
		{name: "surrounding whitespace", stored: "  Asia/Tokyo\n", want: "Asia/Tokyo"},
		{name: "empty", stored: "", want: DefaultName, wantErr: true},
		{name: "only whitespace", stored: "   ", want: DefaultName, wantErr: true},
		{name: "unknown zone", stored: "Mars/Olympus", want: DefaultName, wantErr: true},
		// "Local" loads without error and means the process clock, which is the
		// thing the setting exists to replace.
		{name: "Local", stored: "Local", want: DefaultName, wantErr: true},
		{name: "local in lower case", stored: "local", want: DefaultName, wantErr: true},
		// A relative path would escape the zone database on some systems.
		{name: "path traversal", stored: "../../etc/passwd", want: DefaultName, wantErr: true},
	}
	for _, c := range cases {
		loc, err := Resolve(c.stored)
		if loc == nil {
			t.Fatalf("%s: Resolve(%q) returned a nil location", c.name, c.stored)
		}
		if loc.String() != c.want {
			t.Fatalf("%s: Resolve(%q) chose %q, expected %q", c.name, c.stored, loc.String(), c.want)
		}
		if (err != nil) != c.wantErr {
			t.Fatalf("%s: Resolve(%q) error = %v, expected an error: %v", c.name, c.stored, err, c.wantErr)
		}
	}
}

func TestDefaultIsSeoulAndNotTheProcessClock(t *testing.T) {
	// The zone database is embedded, so this holds on a base image without
	// tzdata as well.
	if Default().String() != DefaultName {
		t.Fatalf("Default() is %q, expected %q", Default().String(), DefaultName)
	}
	_, offset := time.Date(2026, 6, 1, 12, 0, 0, 0, Default()).Zone()
	if offset != 9*3600 {
		t.Fatalf("Asia/Seoul is %d seconds from UTC, expected 32400", offset)
	}
}

func TestDateOfUsesTheCalendarDayOfTheZone(t *testing.T) {
	seoul, err := Resolve("Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-01-01 18:00Z is already 2026-01-02 03:00 in Seoul. This is the window
	// the whole change is about: the process clock is still on yesterday.
	instant := time.Date(2026, 1, 1, 18, 0, 0, 0, time.UTC)
	got := DateOf(instant, seoul)
	want := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("DateOf produced %s, expected %s", got, want)
	}
	if got.Format("20060102") != "20260102" {
		t.Fatalf("date stamp is %q, expected 20260102", got.Format("20060102"))
	}
	// The replaced expression, for comparison: Truncate rounds the absolute
	// instant, so it stays on the UTC day whatever the setting says.
	if truncated := instant.Truncate(24 * time.Hour); truncated.Equal(want) {
		t.Fatal("Truncate(24h) is expected to differ from the configured calendar date here")
	}
	// Midnight UTC is the shape a DATE column arrives in, so the result compares
	// with one directly.
	if got.Location() != time.UTC {
		t.Fatalf("DateOf answered in %s, expected UTC", got.Location())
	}
	// The same instant in UTC is the previous day, and in Tokyo the same day.
	if d := DateOf(instant, time.UTC); d.Day() != 1 {
		t.Fatalf("UTC calendar day is %d, expected 1", d.Day())
	}
	tokyo, err := Resolve("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	if d := DateOf(instant, tokyo); !d.Equal(want) {
		t.Fatalf("Asia/Tokyo produced %s, expected %s", d, want)
	}
}

func TestLoaderReadsTheSettingAndCachesIt(t *testing.T) {
	db := &stubDB{row: stubRow{value: []byte(`"UTC"`)}}
	l := &Loader{DB: db}
	if got := l.Location(context.Background()); got.String() != "UTC" {
		t.Fatalf("Location is %q, expected UTC", got.String())
	}
	for i := 0; i < 5; i++ {
		if got := l.Location(context.Background()); got.String() != "UTC" {
			t.Fatalf("Location is %q, expected UTC", got.String())
		}
	}
	// A contract number must not cost a settings query.
	if db.count() != 1 {
		t.Fatalf("the setting was read %d times, expected 1 within the cache window", db.count())
	}
	before := time.Now().UTC().Format("20060102")
	stamp := l.DateStamp(context.Background())
	if after := time.Now().UTC().Format("20060102"); stamp != before && stamp != after {
		t.Fatalf("DateStamp is %q, expected %q", stamp, before)
	}
	if now := l.Now(context.Background()); now.Location().String() != "UTC" {
		t.Fatalf("Now answered in %s, expected UTC", now.Location())
	}
}

func TestLoaderFallsBackWithoutLosingAKnownZone(t *testing.T) {
	cases := []struct {
		name string
		row  stubRow
	}{
		{name: "the setting row was deleted", row: stubRow{err: pgx.ErrNoRows}},
		{name: "the value is not a JSON string", row: stubRow{value: []byte(`{"zone":"UTC"}`)}},
		{name: "the value is not a zone", row: stubRow{value: []byte(`"Mars/Olympus"`)}},
		{name: "the value is empty", row: stubRow{value: []byte(`""`)}},
	}
	for _, c := range cases {
		l := &Loader{DB: &stubDB{row: c.row}}
		if got := l.Location(context.Background()); got.String() != DefaultName {
			t.Fatalf("%s: Location is %q, expected the %s fallback", c.name, got.String(), DefaultName)
		}
	}

	// A failing query is transient, so the zone already in hand is kept rather
	// than moving every date the server reports.
	db := &stubDB{row: stubRow{value: []byte(`"UTC"`)}}
	l := &Loader{DB: db}
	if got := l.Location(context.Background()); got.String() != "UTC" {
		t.Fatalf("Location is %q, expected UTC", got.String())
	}
	db.mu.Lock()
	db.row = stubRow{err: errors.New("connection refused")}
	db.mu.Unlock()
	l.mu.Lock()
	l.cachedAt = time.Now().Add(-2 * cacheTTL)
	l.mu.Unlock()
	if got := l.Location(context.Background()); got.String() != "UTC" {
		t.Fatalf("a failed read changed the zone to %q, expected the cached UTC", got.String())
	}
	// With nothing cached, the same failure gives the default.
	fresh := &Loader{DB: &stubDB{row: stubRow{err: errors.New("connection refused")}}}
	if got := fresh.Location(context.Background()); got.String() != DefaultName {
		t.Fatalf("Location is %q, expected the %s fallback", got.String(), DefaultName)
	}
}

func TestUnwiredLoaderStillAnswersTheDefault(t *testing.T) {
	// crm.Service and relationship.Service are built as struct literals in tests
	// that never set Clock, and a nil field must not panic or hand back the
	// process clock.
	var missing *Loader
	if got := missing.Location(context.Background()); got.String() != DefaultName {
		t.Fatalf("a nil Loader answered %q, expected %s", got.String(), DefaultName)
	}
	if stamp := missing.DateStamp(context.Background()); stamp != DateOf(time.Now(), Default()).Format("20060102") {
		t.Fatalf("a nil Loader stamped %q", stamp)
	}
	if got := (&Loader{}).Location(context.Background()); got.String() != DefaultName {
		t.Fatalf("a Loader without a database answered %q, expected %s", got.String(), DefaultName)
	}
	if year := missing.Date(context.Background()).Year(); year != time.Now().In(Default()).Year() {
		t.Fatalf("a nil Loader reported year %d", year)
	}
}

func TestQueryNamesTheSettingTheAdminScreenWrites(t *testing.T) {
	// AdminPages.tsx saves namespace "system", key "timezone"; migration 001
	// seeds the same pair. A rename on either side must fail here rather than
	// quietly leaving every date on the fallback.
	captured := &capturingDB{}
	l := &Loader{DB: captured}
	l.Location(context.Background())
	for _, fragment := range []string{"system_settings", "namespace='system'", "key='timezone'", "SELECT value"} {
		if !strings.Contains(captured.sql, fragment) {
			t.Fatalf("the settings query %q does not contain %q", captured.sql, fragment)
		}
	}
}

type capturingDB struct{ sql string }

func (d *capturingDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	d.sql = sql
	return stubRow{value: []byte(`"Asia/Tokyo"`)}
}
