package crm

import (
	"context"
	"testing"
	"time"

	"github.com/hkjang/relio/internal/platform/timezone"
)

// 15:30 UTC on 12 August is already 00:30 on 13 August in Seoul, so the two
// zones disagree about today. expected_close_date is a DATE column and arrives
// at midnight UTC; comparing it against an instant answered "overdue" for every
// deal due today from one second after midnight, and answered it on whichever
// calendar the process clock happened to be on.
var splitInstant = time.Date(2026, time.August, 12, 15, 30, 0, 0, time.UTC)

func dateUTC(day int) time.Time {
	return time.Date(2026, time.August, day, 0, 0, 0, 0, time.UTC)
}

func hasFlag(flags []string, want string) bool {
	for _, flag := range flags {
		if flag == want {
			return true
		}
	}
	return false
}

func openDeal(due time.Time) Opportunity {
	activity := splitInstant.Add(-time.Hour)
	return Opportunity{Status: "OPEN", ExpectedCloseDate: &due, NextAction: "제안서 발송",
		StageEnteredAt: activity, LastActivityAt: &activity}
}

func TestCloseDateIsOverdueOnlyOnceTheDayHasPassed(t *testing.T) {
	today := dateUTC(12)
	for _, c := range []struct {
		name    string
		due     int
		overdue bool
	}{
		{"due today", 12, false},
		{"due tomorrow", 13, false},
		{"due yesterday", 11, true},
	} {
		got := opportunityHealth(openDeal(dateUTC(c.due)), splitInstant, today)
		if hasFlag(got, "CLOSE_DATE_OVERDUE") != c.overdue {
			t.Fatalf("%s: health = %v, want CLOSE_DATE_OVERDUE present=%v", c.name, got, c.overdue)
		}
	}
	// A closed deal is never flagged, whatever its close date says.
	won := openDeal(dateUTC(11))
	won.Status = "WON"
	if got := opportunityHealth(won, splitInstant, dateUTC(12)); hasFlag(got, "CLOSE_DATE_OVERDUE") {
		t.Fatalf("a won deal must not be overdue: %v", got)
	}
}

func TestOpportunityHealthReadsTheConfiguredCalendar(t *testing.T) {
	ctx := context.Background()
	deal := openDeal(dateUTC(12))
	seoul := (&Service{Clock: clockIn("Asia/Seoul")}).Clock.DateAt(ctx, splitInstant)
	utc := (&Service{Clock: clockIn("UTC")}).Clock.DateAt(ctx, splitInstant)
	if !hasFlag(opportunityHealth(deal, splitInstant, seoul), "CLOSE_DATE_OVERDUE") {
		t.Fatalf("Seoul is on %s, so a deal due 8/12 is overdue there", seoul.Format("2006-01-02"))
	}
	if hasFlag(opportunityHealth(deal, splitInstant, utc), "CLOSE_DATE_OVERDUE") {
		t.Fatalf("UTC is on %s, so a deal due 8/12 is not overdue there", utc.Format("2006-01-02"))
	}
	// Nothing wires a Clock in most of these tests, and in production the field
	// is set once in main; the fallback must be the seeded default zone.
	if fallback := (&Service{}).Clock.DateAt(ctx, splitInstant); !fallback.Equal(seoul) {
		t.Fatalf("without a Clock health was judged against %s, want the %s date %s",
			fallback.Format("2006-01-02"), timezone.DefaultName, seoul.Format("2006-01-02"))
	}
}

// The other three flags are elapsed-time questions, which are the same duration
// in every zone. They must keep reading now, not the calendar date.
func TestElapsedTimeFlagsAreUnaffectedByTheCalendarDate(t *testing.T) {
	stale := splitInstant.AddDate(0, 0, -31)
	deal := Opportunity{Status: "OPEN", StageEnteredAt: stale, LastActivityAt: &stale}
	for _, today := range []time.Time{dateUTC(12), dateUTC(13)} {
		got := opportunityHealth(deal, splitInstant, today)
		for _, want := range []string{"NO_RECENT_ACTIVITY", "NO_NEXT_ACTION", "STAGE_STALLED"} {
			if !hasFlag(got, want) {
				t.Fatalf("today=%s: health = %v, want %s", today.Format("2006-01-02"), got, want)
			}
		}
	}
}
