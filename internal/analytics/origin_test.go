package analytics

import (
	"strings"
	"testing"
)

// Every accepted origin is concatenated into a Content-Security-Policy header and
// into generated JavaScript. A value that can carry a space, a semicolon or a
// quote could rewrite the policy — including disabling it — or close a string
// literal, so these tests pin the shape rather than trusting escaping.

func TestParseOriginNormalisesAcceptableForms(t *testing.T) {
	cases := map[string]string{
		"https://matomo.example.com":       "https://matomo.example.com",
		"HTTPS://Matomo.Example.COM":       "https://matomo.example.com",
		" https://matomo.example.com/ ":    "https://matomo.example.com",
		"https://matomo.example.com:8443":  "https://matomo.example.com:8443",
		"http://analytics.internal":        "http://analytics.internal",
		"https://*.example.com":            "https://*.example.com",
		"https://momento.kubagents-ofc.io": "https://momento.kubagents-ofc.io",
		"https://[2001:db8::1]":            "https://[2001:db8::1]",
	}
	for input, want := range cases {
		got, err := ParseOrigin(input)
		if err != nil {
			t.Fatalf("%q must be accepted: %v", input, err)
		}
		if got != want {
			t.Fatalf("%q normalised to %q, expected %q", input, got, want)
		}
	}
}

func TestParseOriginRejectsPolicyBreakingValues(t *testing.T) {
	// Each of these would let an administrator escape the source expression and
	// rewrite the rest of the policy.
	for _, hostile := range []string{
		"https://evil.example.com; script-src *",
		"https://evil.example.com 'unsafe-inline'",
		"https://evil.example.com,https://other.example.com",
		"https://evil.example.com\nscript-src *",
		"https://evil.example.com\r\nX-Injected: 1",
		"https://evil.example.com'",
		`https://evil.example.com"`,
		"'unsafe-eval'",
		"*",
		"https://*",
		"https://ev*il.example.com",
		"https://*.*.example.com",
	} {
		if got, err := ParseOrigin(hostile); err == nil {
			t.Fatalf("%q must be rejected, normalised to %q", hostile, got)
		}
	}
}

func TestParseOriginRejectsNonOriginURLs(t *testing.T) {
	for _, invalid := range []string{
		"",
		"   ",
		"matomo.example.com",                 // no scheme
		"javascript:alert(1)",                // wrong scheme
		"data:text/javascript,alert(1)",      // wrong scheme
		"file:///etc/passwd",                 // wrong scheme
		"https://user:pw@matomo.example.com", // credentials
		"https://matomo.example.com/collect/v1/events", // path
		"https://matomo.example.com?a=1",               // query
		"https://matomo.example.com#x",                 // fragment
		"https://matomo.example.com:abc",               // non-numeric port
		"https://matomo.example.com:",                  // empty port
		"https://.example.com",                         // empty label
		"https://example..com",                         // empty label
		"https://-example.com",                         // leading hyphen
		"https://example-.com",                         // trailing hyphen
		"https://" + strings.Repeat("a", 300),          // too long
	} {
		if _, err := ParseOrigin(invalid); err == nil {
			t.Fatalf("%q must be rejected", invalid)
		}
	}
}

func TestParsePathStaysWithinTheOrigin(t *testing.T) {
	for input, want := range map[string]string{
		"":                "",
		"/matomo.js":      "/matomo.js",
		"matomo.js":       "/matomo.js",
		"/js/script.js":   "/js/script.js",
		"/collect?v=1":    "/collect?v=1",
		"/tracker.min.js": "/tracker.min.js",
	} {
		got, err := ParsePath(input)
		if err != nil {
			t.Fatalf("%q must be accepted: %v", input, err)
		}
		if got != want {
			t.Fatalf("%q became %q, expected %q", input, got, want)
		}
	}
	for _, hostile := range []string{
		"//evil.example.com/x.js", // protocol-relative: leaves the origin
		"/../../etc/passwd",
		`/x.js" onload="alert(1)`,
		"/x.js'",
		"/x.js\nX: 1",
		"/x.js<script>",
		"/x.js\\",
		"/" + strings.Repeat("a", 300),
	} {
		if got, err := ParsePath(hostile); err == nil {
			t.Fatalf("%q must be rejected, became %q", hostile, got)
		}
	}
}

func TestParseSiteIDAllowsOnlyLiteralSafeCharacters(t *testing.T) {
	for _, valid := range []string{"G-ABC123XYZ", "1", "site_42", "my.site-id"} {
		if _, err := ParseSiteID(valid); err != nil {
			t.Fatalf("%q must be accepted: %v", valid, err)
		}
	}
	for _, hostile := range []string{
		`X"});alert(1);//`,
		"X'+alert(1)+'",
		"X</script>",
		"X\nY",
		"X Y",
		strings.Repeat("a", 100),
	} {
		if _, err := ParseSiteID(hostile); err == nil {
			t.Fatalf("%q must be rejected", hostile)
		}
	}
}

func TestParseAttributeNameOnlyAllowsDataAttributes(t *testing.T) {
	if _, err := ParseAttributeName("data-website-id"); err != nil {
		t.Fatal(err)
	}
	if got, _ := ParseAttributeName(" DATA-Domain "); got != "data-domain" {
		t.Fatalf("attribute name must be normalised, got %q", got)
	}
	// onerror would execute; src would repoint the script.
	for _, hostile := range []string{"onerror", "src", "onload", "data-x=y", "data-x\"", ""} {
		if _, err := ParseAttributeName(hostile); err == nil {
			t.Fatalf("%q must be rejected", hostile)
		}
	}
	for _, hostile := range []string{"a\"b", "a'b", "a<b", "a\nb"} {
		if _, err := ParseAttributeValue(hostile); err == nil {
			t.Fatalf("value %q must be rejected", hostile)
		}
	}
}
