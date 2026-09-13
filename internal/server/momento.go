package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/analytics"
	"github.com/hkjang/relio/internal/platform/httpx"
)

// The Momento same-origin proxy.
//
// A tracker that loads from and reports to the collector's own origin needs
// that origin in script-src and connect-src. Where the policy cannot be widened
// — or where nobody wants an external host in it — the tracker is served from
// /momento/tracker.js and told (data-endpoint="/momento") to post events there
// too, and this handler forwards both to the collector. From the browser's
// point of view everything is same-origin, so `script-src 'self'` stays exactly
// as shipped.
//
// The upstream is only ever a script origin an administrator saved through
// validate(): an exact http(s) host, no wildcard, no path. It is resolved per
// request from the same cached snapshot the policy uses, so disabling the
// provider closes the proxy at the same moment it leaves the header.

// momentoBodyLimit caps what a visitor can push through the proxy. Event
// batches are a few kilobytes; anything larger is not tracking data.
const momentoBodyLimit = 256 << 10

// momentoUpstreamTimeout bounds a request to the collector so a slow collector
// cannot hold Relio's connections — a page must never wait on analytics.
const momentoUpstreamTimeout = 10 * time.Second

// momentoStrippedRequestHeaders never reach the collector. The session cookie is
// the important one: the browser attaches it to every same-origin request, and
// forwarding it would hand every visitor's Relio session to the collector.
var momentoStrippedRequestHeaders = []string{"Cookie", "Authorization", "X-CSRF-Token"}

// momentoStrippedResponseHeaders never reach the browser. A collector must not
// plant cookies on Relio's origin, and its own policy headers would be added to
// (not replace) the ones the middleware already set on this response.
var momentoStrippedResponseHeaders = []string{"Set-Cookie", "Content-Security-Policy", "Content-Security-Policy-Report-Only", "Strict-Transport-Security"}

// momentoProxy builds the /momento/* handler. upstream returns the collector
// origin for this request or "" when the proxy is off, in which case the path
// answers 404 like any other unrouted path — nothing about the collector is
// revealed to a visitor while it is not configured.
func momentoProxy(upstream func(context.Context) string, log *slog.Logger, transport http.RoundTripper) http.Handler {
	if transport == nil {
		transport = momentoTransport()
	}
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			target := r.In.Context().Value(momentoTargetKey{}).(*url.URL)
			r.SetURL(target)
			// SetURL joins the target path with the incoming one; the incoming
			// path still carries the /momento prefix, which the collector does
			// not know.
			r.Out.URL.Path = strings.TrimPrefix(r.In.URL.Path, analytics.MomentoProxyPath)
			r.Out.URL.RawPath = ""
			if r.Out.URL.Path == "" {
				r.Out.URL.Path = "/"
			}
			r.Out.Host = target.Host
			for _, name := range momentoStrippedRequestHeaders {
				r.Out.Header.Del(name)
			}
			// The collector attributes visits by address; behind the proxy it
			// would otherwise see only Relio.
			r.SetXForwarded()
		},
		ModifyResponse: func(resp *http.Response) error {
			for _, name := range momentoStrippedResponseHeaders {
				resp.Header.Del(name)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if log != nil {
				log.Warn("momento proxy upstream failed", "error", err, "path", r.URL.Path, "requestId", httpx.RequestID(r.Context()))
			}
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := upstream(r.Context())
		target, err := url.Parse(origin)
		if origin == "" || err != nil || target.Host == "" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodOptions:
		default:
			// The tracker fetches a script and posts events; nothing else
			// belongs on this path.
			w.Header().Set("Allow", "GET, HEAD, POST, OPTIONS")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, momentoBodyLimit)
		// Analytics responses are per visitor and must never be cached by a
		// shared proxy in front of Relio; the tracker script may be, briefly.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Cache-Control", "no-store")
		}
		proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), momentoTargetKey{}, target)))
	})
}

// momentoTargetKey carries the resolved collector from the handler into the
// Rewrite hook, which has no other way to learn it per request.
type momentoTargetKey struct{}

func momentoTransport() http.RoundTripper {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		ResponseHeaderTimeout: momentoUpstreamTimeout,
		TLSHandshakeTimeout:   5 * time.Second,
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
	}
}

// momentoProxyHandler wires the proxy to the analytics snapshot.
func (s *Server) momentoProxyHandler() http.Handler {
	return momentoProxy(func(ctx context.Context) string {
		if s.Analytics == nil {
			return ""
		}
		return s.Analytics.MomentoUpstream(ctx)
	}, s.Log, nil)
}
