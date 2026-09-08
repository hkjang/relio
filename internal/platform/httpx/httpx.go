package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
)

type Error struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"requestId,omitempty"`
}

type errorEnvelope struct {
	Error Error `json:"error"`
}

func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func ErrorJSON(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	JSON(w, status, errorEnvelope{Error: Error{Code: code, Message: message, Details: details, RequestID: RequestID(r.Context())}})
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			ErrorJSON(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "요청 본문이 너무 큽니다.", nil)
			return false
		}
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_json", "JSON 요청 형식이 올바르지 않습니다.", map[string]any{"cause": err.Error()})
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_json", "요청에는 JSON 객체 하나만 허용됩니다.", nil)
		return false
	}
	return true
}

// ClientIP is the address a request arrived from, canonicalised so that it is
// both a stable key and a value PostgreSQL accepts for an `inet` column.
//
// Every caller that stores it binds it into an `::inet` cast that maps only the
// empty string to NULL, and an address the cast refuses does not lose one
// column — it fails the whole statement. That drops the audit event,
// LOGIN_FAILED included, and in auth.Login it fails the session insert, so the
// client cannot log in at all.
// RemoteAddr carries the zone of a link-local address (`[fe80::1%eth0]:52000`),
// which `inet` rejects, so the zone is dropped here; an address that is not an
// address at all (a Unix socket peer is "@") becomes "" and is stored as NULL
// instead of taking its row down with it. An IPv4-mapped form is unmapped so
// one client keys and reads the same way on a dual-stack listener.
func ClientIP(r *http.Request) string {
	raw := r.RemoteAddr
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return ""
	}
	return addr.WithZone("").Unmap().String()
}

func Bearer(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(v) < 8 || !strings.EqualFold(v[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(v[7:])
}

// IntQuery reads a bounded integer from the query string. A key that is absent,
// is not a decimal integer, or lands outside [min, max] yields fallback.
//
// The parse is strict on purpose. fmt.Sscan stops at the first byte it cannot
// use and reports no error about the rest, so it read "1e3" as 1, "0x10" as 16
// and "50abc" as 50: a caller was answered with a different number than it sent,
// under a schema this server publishes as `type: integer` with a minimum and a
// maximum. Surrounding spaces are still accepted because a query string carries
// a "+" as a space.
func IntQuery(r *http.Request, key string, fallback, min, max int) int {
	n, ok := intQuery(r, key)
	if !ok || n < min || n > max {
		return fallback
	}
	return n
}

// ClampQuery is IntQuery for a filter whose fallback switches the filter off.
// Falling back there answers a request to narrow with everything the caller did
// not ask for, so an out-of-range value is pulled to the nearest bound instead
// and only an unreadable one yields fallback.
func ClampQuery(r *http.Request, key string, fallback, min, max int) int {
	n, ok := intQuery(r, key)
	switch {
	case !ok:
		return fallback
	case n < min:
		return min
	case n > max:
		return max
	}
	return n
}

func intQuery(r *http.Request, key string) (int, bool) {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}
