package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The /momento proxy makes the collector same-origin for the browser. The
// things that must hold: it is closed until configured, it forwards only what a
// tracker sends, and the visitor's Relio session never travels with the event.

type collectorCall struct {
	method, path, query, host, cookie, authorization, forwardedFor, body string
}

// collectorLog is what the fake collector saw. The proxy may abort an upload
// while the collector handler is still reading it, so access is locked.
type collectorLog struct {
	mu    sync.Mutex
	calls []collectorCall
}

func (l *collectorLog) snapshot() []collectorCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]collectorCall(nil), l.calls...)
}

func fakeCollector(t *testing.T) (*httptest.Server, *collectorLog) {
	t.Helper()
	log := &collectorLog{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		log.mu.Lock()
		log.calls = append(log.calls, collectorCall{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, host: r.Host,
			cookie: r.Header.Get("Cookie"), authorization: r.Header.Get("Authorization"),
			forwardedFor: r.Header.Get("X-Forwarded-For"), body: string(body)})
		log.mu.Unlock()
		w.Header().Set("Set-Cookie", "momento_visitor=abc; Path=/")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write([]byte("window.momento=1;"))
	}))
	t.Cleanup(server.Close)
	return server, log
}

func proxyTo(origin string) http.Handler {
	return momentoProxy(func(context.Context) string { return origin }, nil, nil)
}

func TestMomentoProxyIsClosedUntilConfigured(t *testing.T) {
	w := httptest.NewRecorder()
	proxyTo("").ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/momento/tracker.js", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("without an upstream the path must be a plain 404, got %d", w.Code)
	}
}

func TestMomentoProxyForwardsTheTrackerWithoutTheSession(t *testing.T) {
	collector, log := fakeCollector(t)
	handler := proxyTo(collector.URL)
	req := httptest.NewRequest(http.MethodGet, "/momento/tracker.js?v=2", nil)
	req.Header.Set("Cookie", "relio_session=secret; csrf=x")
	req.Header.Set("Authorization", "Bearer personal-key")
	req.Header.Set("X-CSRF-Token", "token")
	req.RemoteAddr = "203.0.113.9:4444"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "window.momento=1") {
		t.Fatalf("the tracker must come back from the collector: %d %s", w.Code, w.Body.String())
	}
	calls := log.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected one upstream call, got %d", len(calls))
	}
	call := calls[0]
	if call.path != "/tracker.js" || call.query != "v=2" {
		t.Fatalf("the /momento prefix must be stripped and the query kept: %+v", call)
	}
	if call.host != strings.TrimPrefix(collector.URL, "http://") {
		t.Fatalf("the Host header must be the collector's, got %q", call.host)
	}
	if call.cookie != "" || call.authorization != "" {
		t.Fatalf("credentials must never reach the collector: %+v", call)
	}
	if call.forwardedFor != "203.0.113.9" {
		t.Fatalf("the visitor address should be forwarded for attribution, got %q", call.forwardedFor)
	}
	// The collector's cookies and policy headers stay on its side.
	if w.Header().Get("Set-Cookie") != "" {
		t.Fatal("a collector must not plant cookies on Relio's origin")
	}
	if w.Header().Get("Content-Security-Policy") != "" {
		t.Fatal("the collector's policy header must not be forwarded; the middleware sets Relio's")
	}
}

func TestMomentoProxyForwardsEventsAndRefusesOtherMethods(t *testing.T) {
	collector, log := fakeCollector(t)
	handler := proxyTo(collector.URL)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/momento/collect", strings.NewReader(`{"events":[]}`)))
	if calls := log.snapshot(); w.Code != http.StatusOK || len(calls) != 1 || calls[0].method != http.MethodPost || calls[0].body != `{"events":[]}` {
		t.Fatalf("an event batch must be posted through unchanged: %d %+v", w.Code, calls)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("event responses must not be cacheable")
	}

	// The bare endpoint path is what data-endpoint="/momento" may post to.
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/momento", strings.NewReader(`{}`)))
	if calls := log.snapshot(); w.Code != http.StatusOK || len(calls) != 2 || calls[1].path != "/" {
		t.Fatalf("the bare proxy path must forward to the collector root: %d %+v", w.Code, calls)
	}

	for _, method := range []string{http.MethodDelete, http.MethodPut, http.MethodPatch} {
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "/momento/anything", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s must be refused, got %d", method, w.Code)
		}
	}
	if calls := log.snapshot(); len(calls) != 2 {
		t.Fatalf("refused methods must not reach the collector: %+v", calls)
	}
}

func TestMomentoProxyCapsTheBodyAndReportsAnUnreachableCollector(t *testing.T) {
	collector, log := fakeCollector(t)

	oversized := strings.Repeat("x", momentoBodyLimit+1)
	w := httptest.NewRecorder()
	proxyTo(collector.URL).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/momento/collect", strings.NewReader(oversized)))
	if w.Code == http.StatusOK {
		t.Fatalf("a body over the limit must not be relayed successfully, got %d", w.Code)
	}
	for _, call := range log.snapshot() {
		if len(call.body) > momentoBodyLimit {
			t.Fatalf("the collector received %d bytes, over the %d limit", len(call.body), momentoBodyLimit)
		}
	}

	// A collector that is down answers 502, never a hang or a panic.
	closed := httptest.NewServer(http.NotFoundHandler())
	origin := closed.URL
	closed.Close()
	w = httptest.NewRecorder()
	proxyTo(origin).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/momento/tracker.js", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("an unreachable collector must answer 502, got %d", w.Code)
	}
}
