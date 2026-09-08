package crm

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/hkjang/relio/internal/platform/timezone"
	"github.com/jackc/pgx/v5"
)

// settingRow answers the one query timezone.Loader makes, so a service can be
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

var (
	contractNoPattern = regexp.MustCompile(`^C-(\d{8})-[0-9A-F]{6}$`)
	// ids.Token is base64url, so the uppercased tail can carry "_" and "-" too.
	quotationNoPattern = regexp.MustCompile(`^Q-(\d{8})-[0-9A-Z_-]+$`)
)

// Pacific/Kiritimati is UTC+14 and Pacific/Midway is UTC-11. The 25 hours
// between them mean the two are never on the same calendar date, so this holds
// whenever the test runs — and neither matches the process clock at all times,
// which is what the numbers used to carry.
const (
	aheadZone  = "Pacific/Kiritimati"
	behindZone = "Pacific/Midway"
)

func TestGeneratedNumbersCarryTheConfiguredCalendarDate(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		pattern *regexp.Regexp
		make    func(s *Service) string
	}{
		{name: "contract", pattern: contractNoPattern, make: func(s *Service) string { return s.contractNumber(ctx) }},
		{name: "quotation", pattern: quotationNoPattern, make: func(s *Service) string { return s.quotationNumber(ctx) }},
	}
	for _, c := range cases {
		ahead := c.make(&Service{Clock: clockIn(aheadZone)})
		behind := c.make(&Service{Clock: clockIn(behindZone)})
		aheadMatch, behindMatch := c.pattern.FindStringSubmatch(ahead), c.pattern.FindStringSubmatch(behind)
		if aheadMatch == nil || behindMatch == nil {
			t.Fatalf("%s number does not have the documented shape: %q, %q", c.name, ahead, behind)
		}
		if aheadMatch[1] == behindMatch[1] {
			t.Fatalf("%s number stamped %s for both %s and %s, so the setting was not read",
				c.name, aheadMatch[1], aheadZone, behindZone)
		}
		for _, x := range []struct {
			zone, stamp string
		}{{aheadZone, aheadMatch[1]}, {behindZone, behindMatch[1]}} {
			loc, err := timezone.Resolve(x.zone)
			if err != nil {
				t.Fatal(err)
			}
			if want := timezone.DateOf(time.Now(), loc).Format("20060102"); x.stamp != want {
				t.Fatalf("%s number stamped %s for %s, expected %s", c.name, x.stamp, x.zone, want)
			}
		}
	}
}

func TestGeneratedNumbersFallBackToSeoulWithoutAClock(t *testing.T) {
	// Nothing wires a Clock in these tests, and the fallback must be the seeded
	// default rather than the container's UTC clock.
	ctx := context.Background()
	s := &Service{}
	seoul := timezone.DateOf(time.Now(), timezone.Default()).Format("20060102")
	for _, got := range []struct {
		name    string
		number  string
		pattern *regexp.Regexp
	}{
		{"contract", s.contractNumber(ctx), contractNoPattern},
		{"quotation", s.quotationNumber(ctx), quotationNoPattern},
	} {
		match := got.pattern.FindStringSubmatch(got.number)
		if match == nil {
			t.Fatalf("%s number does not have the documented shape: %q", got.name, got.number)
		}
		if match[1] != seoul {
			t.Fatalf("%s number stamped %s, expected the %s date %s", got.name, match[1], timezone.DefaultName, seoul)
		}
	}
}
