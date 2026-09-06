package httpx

import (
	"net/http/httptest"
	"net/url"
	"testing"
)

// urlValue writes the raw value into a query string the way a client would, so
// each case exercises the same decoding the server does at runtime.
func urlValue(raw string) string { return url.QueryEscape(raw) }

// TestIntQueryRefusesAValueThatIsNotADecimalInteger pins the parse that used to
// be fmt.Sscan. Sscan consumes as much as it can and says nothing about the
// rest, so each of these came back as a number the caller never sent — "1e3" as
// 1, "0x10" as 16, "50abc" as 50, "030" as octal 24 — while the published schema
// calls the key an integer with a minimum and a maximum. The fallback here is 7
// so that no case can pass by landing on the value it was asked for.
func TestIntQueryRefusesAValueThatIsNotADecimalInteger(t *testing.T) {
	for _, raw := range []string{"1e3", "0x10", "50abc", "50 999", "1_000", "abc", "3.5", ""} {
		r := httptest.NewRequest("GET", "/x?days="+urlValue(raw), nil)
		if got := IntQuery(r, "days", 7, 1, 365); got != 7 {
			t.Errorf("IntQuery(days=%q) = %d, want the fallback 7", raw, got)
		}
	}
}

// TestIntQueryReadsEveryFormAQueryStringCanCarry guards the other side: the
// strict parse must not start rejecting values clients legitimately send. A
// query string encodes "+" as a space, so "?days=+30" arrives as " 30".
func TestIntQueryReadsEveryFormAQueryStringCanCarry(t *testing.T) {
	cases := map[string]int{"30": 30, " 30": 30, "+30": 30, "030": 30, "1": 1, "365": 365}
	for raw, want := range cases {
		r := httptest.NewRequest("GET", "/x?days="+urlValue(raw), nil)
		if got := IntQuery(r, "days", 7, 1, 365); got != want {
			t.Errorf("IntQuery(days=%q) = %d, want %d", raw, got, want)
		}
	}
}

func TestIntQueryFallsBackOutsideItsRange(t *testing.T) {
	for _, raw := range []string{"0", "-3", "201", "999999999999999999999"} {
		r := httptest.NewRequest("GET", "/x?limit="+urlValue(raw), nil)
		if got := IntQuery(r, "limit", 50, 1, 200); got != 50 {
			t.Errorf("IntQuery(limit=%q) = %d, want the fallback 50", raw, got)
		}
	}
}

// TestClampQueryKeepsAFilterOnAboveItsBound covers the reason ClampQuery exists:
// for expiringDays the fallback is 0, and 0 turns the end-date filter off. A
// caller asking to narrow to 5000 days must not be handed every contract in
// scope instead.
func TestClampQueryKeepsAFilterOnAboveItsBound(t *testing.T) {
	cases := map[string]int{"5000": 3650, "3651": 3650, "3650": 3650, "90": 90, "-1": 0, "0": 0}
	for raw, want := range cases {
		r := httptest.NewRequest("GET", "/x?expiringDays="+urlValue(raw), nil)
		if got := ClampQuery(r, "expiringDays", 0, 0, 3650); got != want {
			t.Errorf("ClampQuery(expiringDays=%q) = %d, want %d", raw, got, want)
		}
	}
}

// An unreadable value has no side to be pulled towards, so it still falls back.
func TestClampQueryFallsBackWhenTheValueIsUnreadable(t *testing.T) {
	for _, raw := range []string{"", "abc", "1e4"} {
		r := httptest.NewRequest("GET", "/x?expiringDays="+urlValue(raw), nil)
		if got := ClampQuery(r, "expiringDays", 0, 0, 3650); got != 0 {
			t.Errorf("ClampQuery(expiringDays=%q) = %d, want the fallback 0", raw, got)
		}
	}
}
