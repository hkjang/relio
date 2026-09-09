package intelligence

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hkjang/relio/internal/platform/timezone"
	"github.com/jackc/pgx/v5"
)

// settingRow answers the one query timezone.Loader makes, so the engine can be
// pinned to a zone without a database.
type settingRow struct{ zone string }

func (r settingRow) Scan(dest ...any) error {
	*(dest[0].(*[]byte)) = []byte(`"` + r.zone + `"`)
	return nil
}

type settingDB struct{ zone string }

func (d settingDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return settingRow{zone: d.zone}
}

func clockIn(zone string) *timezone.Loader { return &timezone.Loader{DB: settingDB{zone: zone}} }

// 15:30 UTC on 12 August is already 00:30 on 13 August in Seoul. Every instant
// in that nine-hour window used to make the engine count D-days on a calendar
// the business was no longer on: a contract expiring on the 15th was announced
// as D-3 when the salesperson reading it had two days left, and a deal that
// went past its close date at the last midnight was still called on time.
var splitInstant = time.Date(2026, time.August, 12, 15, 30, 0, 0, time.UTC)

func calendarOf(t *testing.T, zone string) time.Time {
	t.Helper()
	return (&Service{Clock: clockIn(zone)}).Clock.DateAt(context.Background(), splitInstant)
}

func TestConfiguredZoneDecidesTheDayTheEngineCountsFrom(t *testing.T) {
	seoul, utc := calendarOf(t, "Asia/Seoul"), calendarOf(t, "UTC")
	if !seoul.After(utc) {
		t.Fatalf("Seoul is on %s and UTC on %s at %s; the two must differ here or the rest of this file proves nothing",
			seoul.Format("2006-01-02"), utc.Format("2006-01-02"), splitInstant)
	}
	// A Service that was never given a loader must still answer on the seeded
	// default rather than falling back to the container's UTC clock.
	if fallback := (&Service{}).Clock.DateAt(context.Background(), splitInstant); !fallback.Equal(seoul) {
		t.Fatalf("without a Clock the engine counted from %s, want the %s date %s",
			fallback.Format("2006-01-02"), timezone.DefaultName, seoul.Format("2006-01-02"))
	}
}

func TestContractDDayCountsOnTheConfiguredCalendar(t *testing.T) {
	// end_date is a DATE column, so it arrives at midnight UTC on 15 August.
	endDate := time.Date(2026, time.August, 15, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		zone      string
		label     string
		remaining int
	}{
		{"Asia/Seoul", "만료 D-2", 2},
		{"UTC", "만료 D-3", 3},
	} {
		signals := contractSignals(&contractFacts{ID: "k", Title: "계약", AccountID: "a",
			EndDate: endDate, RenewalStatus: "IN_PROGRESS"}, splitInstant, calendarOf(t, c.zone))
		if len(signals) != 1 {
			t.Fatalf("%s: end_date 2026-08-15 produced %d signals", c.zone, len(signals))
		}
		if !strings.HasSuffix(signals[0].Title, c.label) {
			t.Fatalf("%s: title = %q, want it to end with %q", c.zone, signals[0].Title, c.label)
		}
		if got := signals[0].Evidence["daysRemaining"]; got != c.remaining {
			t.Fatalf("%s: daysRemaining = %v, want %d", c.zone, got, c.remaining)
		}
	}
}

func TestCloseDatePassedCountsOnTheConfiguredCalendar(t *testing.T) {
	due := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	deal := func() *opportunityFacts {
		return &opportunityFacts{ID: "o", Name: "딜", AccountID: "a", StageName: "제안",
			StageEnteredAt: splitInstant.AddDate(0, 0, -1), Status: "OPEN", CloseDate: &due}
	}
	late, ok := signalTypes(opportunitySignals(deal(), splitInstant, calendarOf(t, "Asia/Seoul")))["CLOSE_DATE_PASSED"]
	if !ok {
		t.Fatal("Seoul is already on 8/13, so a deal due 8/12 has missed its close date there")
	}
	if got := late.Evidence["daysOverdue"]; got != 1 {
		t.Fatalf("daysOverdue = %v, want 1", got)
	}
	if _, ok = signalTypes(opportunitySignals(deal(), splitInstant, calendarOf(t, "UTC")))["CLOSE_DATE_PASSED"]; ok {
		t.Fatal("UTC is still on 8/12, so a deal due 8/12 is not late there")
	}
}
