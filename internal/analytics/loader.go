package analytics

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hkjang/relio/internal/platform/ids"
)

// The loader is served from Relio's own origin, so script-src 'self' already
// covers it and only the vendor's host needs to be added to the policy. Every
// value interpolated below has been validated by origin.go, and each is emitted
// as a JSON string literal, so a configuration value cannot close the literal and
// inject code.

// Loader renders the JavaScript served at /analytics.js. It returns an empty
// string when nothing is enabled, which keeps the default build free of any
// external request.
func (s *Service) Loader(ctx context.Context) (string, error) {
	providers, err := s.load(ctx, true)
	if err != nil {
		return "", err
	}
	if len(providers) == 0 {
		return "/* 활성화된 방문자 분석 공급자가 없습니다. */\n", nil
	}
	var b strings.Builder
	b.WriteString("/* Relio 방문자 분석 로더 — 관리자 설정에서 생성되었습니다. */\n")
	b.WriteString("(function(){\n")
	b.WriteString("  var relioSignedIn = document.cookie.indexOf('relio_session=') !== -1;\n")
	b.WriteString("  function inject(src, attributes){\n")
	b.WriteString("    var tag = document.createElement('script');\n")
	b.WriteString("    tag.async = true; tag.src = src;\n")
	b.WriteString("    Object.keys(attributes || {}).forEach(function(name){ tag.setAttribute(name, attributes[name]) });\n")
	b.WriteString("    document.head.appendChild(tag);\n")
	b.WriteString("  }\n")
	for _, provider := range providers {
		snippet, err := renderProvider(provider)
		if err != nil {
			// One bad row must not take the whole loader down.
			b.WriteString(fmt.Sprintf("  /* %s 건너뜀: %s */\n", jsString(provider.Name), jsString(err.Error())))
			continue
		}
		b.WriteString("  (function(){\n")
		if provider.RespectDNT {
			b.WriteString("    if (navigator.doNotTrack === '1' || window.doNotTrack === '1') return;\n")
		}
		if provider.AuthenticatedOnly {
			b.WriteString("    if (!relioSignedIn) return;\n")
		}
		b.WriteString(snippet)
		b.WriteString("  })();\n")
	}
	b.WriteString("})();\n")
	return b.String(), nil
}

func renderProvider(p Provider) (string, error) {
	switch p.Provider {
	case "GA4":
		if p.SiteID == "" {
			return "", fmt.Errorf("measurement id가 없습니다")
		}
		return fmt.Sprintf(`    window.dataLayer = window.dataLayer || [];
    function gtag(){ window.dataLayer.push(arguments) }
    gtag('js', new Date());
    gtag('config', %s, { anonymize_ip: true });
    inject('https://www.googletagmanager.com/gtag/js?id=' + encodeURIComponent(%s), {});
`, jsString(p.SiteID), jsString(p.SiteID)), nil

	case "MATOMO":
		if p.ScriptOrigin == "" || p.SiteID == "" {
			return "", fmt.Errorf("origin 또는 site id가 없습니다")
		}
		return fmt.Sprintf(`    var paq = window._paq = window._paq || [];
    paq.push(['trackPageView']);
    paq.push(['enableLinkTracking']);
    paq.push(['setTrackerUrl', %s]);
    paq.push(['setSiteId', %s]);
    inject(%s, {});
`, jsString(p.ScriptOrigin+"/matomo.php"), jsString(p.SiteID), jsString(p.ScriptOrigin+p.ScriptPath)), nil

	case "PLAUSIBLE":
		if p.ScriptOrigin == "" {
			return "", fmt.Errorf("origin이 없습니다")
		}
		attributes := map[string]string{"data-domain": p.SiteID}
		for name, value := range p.ScriptAttributes {
			attributes[name] = value
		}
		return fmt.Sprintf("    inject(%s, %s);\n", jsString(p.ScriptOrigin+p.ScriptPath), jsObject(attributes)), nil

	case "UMAMI":
		if p.ScriptOrigin == "" || p.SiteID == "" {
			return "", fmt.Errorf("origin 또는 website id가 없습니다")
		}
		attributes := map[string]string{"data-website-id": p.SiteID}
		for name, value := range p.ScriptAttributes {
			attributes[name] = value
		}
		return fmt.Sprintf("    inject(%s, %s);\n", jsString(p.ScriptOrigin+p.ScriptPath), jsObject(attributes)), nil

	case "SCRIPT":
		if p.ScriptOrigin == "" || p.ScriptPath == "" {
			return "", fmt.Errorf("스크립트 주소가 없습니다")
		}
		return fmt.Sprintf("    inject(%s, %s);\n", jsString(p.ScriptOrigin+p.ScriptPath), jsObject(p.ScriptAttributes)), nil
	}
	return "", fmt.Errorf("지원하지 않는 공급자입니다")
}

// jsString emits a JavaScript string literal. The inputs are already restricted
// to a safe character set; this is the second line of defence so that a future
// change to validation cannot turn into script injection.
func jsString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, char := range value {
		switch char {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '<':
			// Escaped so the literal can never close the surrounding script tag.
			b.WriteString(`\u003c`)
		case '>':
			b.WriteString(`\u003e`)
		case '&':
			b.WriteString(`\u0026`)
		default:
			if char < 0x20 {
				b.WriteString(fmt.Sprintf(`\u%04x`, char))
				continue
			}
			b.WriteRune(char)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func jsObject(values map[string]string) string {
	if len(values) == 0 {
		return "{}"
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	// Stable output keeps the served loader byte-identical between requests.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s:%s", jsString(name), jsString(values[name])))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// ---------------------------------------------------------------- violations

type Violation struct {
	Directive     string    `json:"directive"`
	BlockedOrigin string    `json:"blockedOrigin"`
	DocumentURI   string    `json:"documentUri,omitempty"`
	Occurrences   int       `json:"occurrences"`
	FirstSeenAt   time.Time `json:"firstSeenAt"`
	LastSeenAt    time.Time `json:"lastSeenAt"`
	Resolved      bool      `json:"resolved"`
	// Suggested is true when the blocked value is a usable origin an
	// administrator could allow directly.
	Suggested bool `json:"suggested"`
}

// RecordViolation rolls a browser report up by directive and origin. The report
// body is attacker-controlled in the sense that anyone can POST to the endpoint,
// so nothing is trusted: the origin is re-parsed and the row is capped by the
// unique constraint rather than appended.
func (s *Service) RecordViolation(ctx context.Context, directive, blocked, document string) error {
	directive = sanitizeToken(directive, 60)
	if directive == "" {
		return nil
	}
	// Reports carry the full blocked URL, so reduce it to the origin an
	// administrator would actually add to the policy.
	origin, ok := OriginOf(blocked)
	if !ok {
		// Keep unparseable values, truncated, so "inline" and "data" still show.
		origin = sanitizeToken(blocked, 120)
	}
	if origin == "" {
		return nil
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO csp_violations(id,directive,blocked_origin,document_uri)
		VALUES($1,$2,$3,NULLIF($4,''))
		ON CONFLICT(directive,blocked_origin) DO UPDATE SET occurrences=csp_violations.occurrences+1,
			last_seen_at=now(),resolved=false`,
		ids.New(), directive, origin, sanitizeToken(document, 200))
	return err
}

// sanitizeToken strips anything that is not safe to store and display.
func sanitizeToken(value string, limit int) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			continue
		}
		b.WriteRune(char)
		if b.Len() >= limit {
			break
		}
	}
	return b.String()
}

func (s *Service) Violations(ctx context.Context) ([]Violation, error) {
	rows, err := s.DB.Query(ctx, `SELECT directive,blocked_origin,COALESCE(document_uri,''),occurrences,
		first_seen_at,last_seen_at,resolved FROM csp_violations ORDER BY resolved,last_seen_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Violation{}
	for rows.Next() {
		var v Violation
		var firstSeen, lastSeen time.Time
		if err = rows.Scan(&v.Directive, &v.BlockedOrigin, &v.DocumentURI, &v.Occurrences, &firstSeen, &lastSeen, &v.Resolved); err != nil {
			return nil, err
		}
		v.FirstSeenAt, v.LastSeenAt = firstSeen, lastSeen
		// Only a parseable origin can be turned into a policy source, so the UI
		// offers a one-click allow just for those.
		_, parseErr := ParseOrigin(v.BlockedOrigin)
		v.Suggested = parseErr == nil
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Service) ResolveViolation(ctx context.Context, directive, origin string) error {
	_, err := s.DB.Exec(ctx, `UPDATE csp_violations SET resolved=true WHERE directive=$1 AND blocked_origin=$2`, directive, origin)
	return err
}
