package httpx

import (
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

// The Go server sets RemoteAddr from the accepted connection, so every case here
// is a form that field can hold rather than anything a client sends in a header.
//
// TestClientIPKeepsTheAddressPostgresAccepts covers the ordinary forms: what
// comes back has to be the same address, without the port.
func TestClientIPKeepsTheAddressPostgresAccepts(t *testing.T) {
	cases := map[string]string{
		"192.0.2.10:52000":  "192.0.2.10",
		"[2001:db8::1]:443": "2001:db8::1",
		"203.0.113.4":       "203.0.113.4",
		"2001:db8::2":       "2001:db8::2",
	}
	for raw, want := range cases {
		r := httptest.NewRequest("GET", "/x", nil)
		r.RemoteAddr = raw
		if got := ClientIP(r); got != want {
			t.Errorf("ClientIP(RemoteAddr=%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestClientIPDropsTheZoneOfALinkLocalAddress is the case that took whole rows
// down. A peer reached over IPv6 link-local arrives as `[fe80::1%eth0]:52000`,
// and `'fe80::1%eth0'::inet` is a syntax error, so the audit insert that binds
// it failed and the event — LOGIN_FAILED among them — was never written.
func TestClientIPDropsTheZoneOfALinkLocalAddress(t *testing.T) {
	cases := map[string]string{
		"[fe80::1%eth0]:52000":   "fe80::1",
		"[fe80::1%25eth0]:52000": "fe80::1",
		"fe80::1%eth0":           "fe80::1",
		"[fe80::a12:3%2]:80":     "fe80::a12:3",
	}
	for raw, want := range cases {
		r := httptest.NewRequest("GET", "/x", nil)
		r.RemoteAddr = raw
		got := ClientIP(r)
		if got != want {
			t.Errorf("ClientIP(RemoteAddr=%q) = %q, want %q", raw, got, want)
		}
		if strings.Contains(got, "%") {
			t.Errorf("ClientIP(RemoteAddr=%q) = %q, which inet rejects", raw, got)
		}
	}
}

// TestClientIPUnmapsAnIPv4MappedAddress keeps one client on one key. A
// dual-stack listener reports an IPv4 peer as `::ffff:192.0.2.10`, which would
// otherwise be a second login-limiter bucket and a second-looking address in the
// audit log for the same machine.
func TestClientIPUnmapsAnIPv4MappedAddress(t *testing.T) {
	for _, raw := range []string{"[::ffff:192.0.2.10]:52000", "::ffff:192.0.2.10"} {
		r := httptest.NewRequest("GET", "/x", nil)
		r.RemoteAddr = raw
		if got := ClientIP(r); got != "192.0.2.10" {
			t.Errorf("ClientIP(RemoteAddr=%q) = %q, want %q", raw, got, "192.0.2.10")
		}
	}
}

// TestClientIPYieldsEmptyForWhatIsNotAnAddress is the fallback the storing
// callers rely on: every one of them casts the result to inet after mapping the
// empty string to NULL, so "" is stored as NULL and the row survives. Returning the raw string, as
// this used to, made the whole statement fail instead — a lost audit event, and
// in auth.Login a login that could not complete.
func TestClientIPYieldsEmptyForWhatIsNotAnAddress(t *testing.T) {
	for _, raw := range []string{"@", "", "localhost:8080", "not-an-address", "192.0.2.300:1", "192.0.2.0/24"} {
		r := httptest.NewRequest("GET", "/x", nil)
		r.RemoteAddr = raw
		if got := ClientIP(r); got != "" {
			t.Errorf("ClientIP(RemoteAddr=%q) = %q, want the empty string", raw, got)
		}
	}
}

// TestClientIPAlwaysReturnsSomethingInetCanHold states the contract once for a
// wide spread of inputs rather than case by case: a bare address with no zone,
// or nothing at all.
func TestClientIPAlwaysReturnsSomethingInetCanHold(t *testing.T) {
	raws := []string{
		"192.0.2.10:52000", "[2001:db8::1]:443", "[fe80::1%eth0]:52000", "::ffff:192.0.2.10",
		"@", "", "localhost:8080", "1.2.3", "[::]:0", "0.0.0.0:0", "256.1.1.1:5", "[2001:db8::1]",
	}
	for _, raw := range raws {
		r := httptest.NewRequest("GET", "/x", nil)
		r.RemoteAddr = raw
		got := ClientIP(r)
		if got == "" {
			continue
		}
		addr, err := netip.ParseAddr(got)
		if err != nil {
			t.Errorf("ClientIP(RemoteAddr=%q) = %q, which is not an address: %v", raw, got, err)
			continue
		}
		if addr.Zone() != "" {
			t.Errorf("ClientIP(RemoteAddr=%q) = %q, which still carries a zone", raw, got)
		}
	}
}
