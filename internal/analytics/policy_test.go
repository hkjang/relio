package analytics

import (
	"strings"
	"testing"
)

func TestBuildPolicyOnlyIncludesEnabledProviders(t *testing.T) {
	policy := buildPolicy([]Provider{
		{Provider: "MATOMO", Enabled: true, ScriptOrigin: "https://matomo.example.com", ScriptPath: "/matomo.js"},
		{Provider: "SCRIPT", Enabled: false, ScriptOrigin: "https://disabled.example.com", ScriptPath: "/t.js"},
	})
	if contains(policy.ScriptSrc, "https://disabled.example.com") {
		t.Fatal("a disabled provider must not widen the policy")
	}
	if !contains(policy.ScriptSrc, "https://matomo.example.com") {
		t.Fatal("the enabled script origin must be allowed")
	}
	// A vendor almost always posts events back to the host that served its
	// script, so that is allowed without extra configuration. This is exactly the
	// case that produced the reported connect-src failure.
	if !contains(policy.ConnectSrc, "https://matomo.example.com") {
		t.Fatal("the script origin must also be allowed for connect-src")
	}
	if !policy.Enabled {
		t.Fatal("policy must report that it widened something")
	}
}

func TestBuildPolicyAllowsASeparateCollectorHost(t *testing.T) {
	// The reported error was a tracker served from one host posting to another.
	policy := buildPolicy([]Provider{{
		Provider: "SCRIPT", Enabled: true,
		ScriptOrigin:   "https://cdn.example.com",
		ScriptPath:     "/tracker.js",
		CollectOrigins: []string{"https://momento.kubagents-ofc.example.com"},
	}})
	if !contains(policy.ConnectSrc, "https://momento.kubagents-ofc.example.com") {
		t.Fatal("the collect origin must be allowed for connect-src")
	}
	if contains(policy.ScriptSrc, "https://momento.kubagents-ofc.example.com") {
		t.Fatal("a collect origin must not become a script source")
	}
}

func TestBuildPolicyAddsGoogleOriginsForGA4(t *testing.T) {
	policy := buildPolicy([]Provider{{Provider: "GA4", Enabled: true, SiteID: "G-ABC123"}})
	for _, origin := range []string{"https://www.googletagmanager.com", "https://www.google-analytics.com"} {
		if !contains(policy.ScriptSrc, origin) || !contains(policy.ConnectSrc, origin) {
			t.Fatalf("GA4 needs %s in script-src and connect-src", origin)
		}
	}
	// GA still uses tracking pixels on some paths.
	if !contains(policy.ImgSrc, "https://www.google-analytics.com") {
		t.Fatal("GA4 needs its pixel origin in img-src")
	}
}

func TestEmptyPolicyKeepsTheOfflineDefault(t *testing.T) {
	policy := buildPolicy(nil)
	if policy.Enabled || len(policy.ScriptSrc) > 0 || len(policy.ConnectSrc) > 0 || len(policy.ImgSrc) > 0 {
		t.Fatalf("with nothing enabled the policy must stay empty: %+v", policy)
	}
}

func TestPolicySourcesAreSafeToJoinIntoAHeader(t *testing.T) {
	// buildPolicy output is joined with spaces into the header, so no source may
	// contain a separator even if a bad value somehow reached the database.
	policy := buildPolicy([]Provider{{
		Provider: "GA4", Enabled: true, SiteID: "G-1",
	}, {
		Provider: "MATOMO", Enabled: true, ScriptOrigin: "https://matomo.example.com:8443", ScriptPath: "/matomo.js",
	}})
	for _, group := range [][]string{policy.ScriptSrc, policy.ConnectSrc, policy.ImgSrc} {
		for _, source := range group {
			if strings.ContainsAny(source, " ;,'\"\r\n") {
				t.Fatalf("source %q would break the header", source)
			}
		}
	}
}

func TestValidateRejectsAWildcardScriptOrigin(t *testing.T) {
	// A wildcard cannot be turned into a concrete script URL.
	in := Provider{Provider: "MATOMO", Name: "m", SiteID: "1", ScriptOrigin: "https://*.example.com"}
	if err := validate(&in); err == nil {
		t.Fatal("a wildcard script origin must be rejected")
	}
	// It is still fine as a collect origin, where only CSP matching happens.
	ok := Provider{Provider: "MATOMO", Name: "m", SiteID: "1", ScriptOrigin: "https://matomo.example.com",
		CollectOrigins: []string{"https://*.example.com"}}
	if err := validate(&ok); err != nil {
		t.Fatalf("a wildcard collect origin must be accepted: %v", err)
	}
}

func TestValidateAppliesVendorRequirementsAndDefaults(t *testing.T) {
	matomo := Provider{Provider: "matomo", Name: "사내 Matomo", SiteID: "3", ScriptOrigin: "https://matomo.example.com"}
	if err := validate(&matomo); err != nil {
		t.Fatal(err)
	}
	if matomo.Provider != "MATOMO" || matomo.ScriptPath != "/matomo.js" {
		t.Fatalf("vendor default path not applied: %+v", matomo)
	}
	for name, in := range map[string]Provider{
		"no name":     {Provider: "GA4", SiteID: "G-1"},
		"no site id":  {Provider: "GA4", Name: "ga"},
		"no origin":   {Provider: "MATOMO", Name: "m", SiteID: "1"},
		"no path":     {Provider: "SCRIPT", Name: "s", ScriptOrigin: "https://t.example.com"},
		"bad vendor":  {Provider: "SPYWARE", Name: "x"},
		"bad attr":    {Provider: "GA4", Name: "ga", SiteID: "G-1", ScriptAttributes: map[string]string{"onerror": "alert(1)"}},
		"bad collect": {Provider: "GA4", Name: "ga", SiteID: "G-1", CollectOrigins: []string{"https://x.example.com; script-src *"}},
	} {
		candidate := in
		if err := validate(&candidate); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

func TestLoaderNeverEmitsRawConfiguration(t *testing.T) {
	// A site id is emitted as a string literal. Even if validation changed, the
	// literal must stay closed.
	snippet, err := renderProvider(Provider{Provider: "GA4", SiteID: "G-ABC123"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snippet, `"G-ABC123"`) {
		t.Fatalf("site id must be quoted: %s", snippet)
	}
	// The invariant is that no quote inside the literal is unescaped, not that
	// the characters are absent.
	if unescapedQuote(jsString(`x");alert(1);//`)) {
		t.Fatalf("literal contains an unescaped quote: %s", jsString(`x");alert(1);//`))
	}
	if unescapedQuote(jsString(`plain`)) {
		t.Fatal("a plain value must produce a well-formed literal")
	}
	if strings.Contains(jsString("</script>"), "</script>") {
		t.Fatal("a closing script tag must be escaped")
	}
	if jsObject(nil) != "{}" {
		t.Fatal("an empty attribute map must render as an empty object")
	}
	if jsObject(map[string]string{"b": "2", "a": "1"}) != `{"a":"1","b":"2"}` {
		t.Fatalf("attribute order must be stable, got %s", jsObject(map[string]string{"b": "2", "a": "1"}))
	}
}

func TestSanitizeTokenStripsControlCharactersAndCaps(t *testing.T) {
	if got := sanitizeToken("script-src\n\r\telem", 60); strings.ContainsAny(got, "\n\r\t") {
		t.Fatalf("control characters must be stripped, got %q", got)
	}
	if got := sanitizeToken(strings.Repeat("a", 500), 40); len(got) > 40 {
		t.Fatalf("value must be capped, got %d characters", len(got))
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// unescapedQuote reports whether the body of a rendered literal contains a quote
// that is not preceded by a backslash, which would end the literal early.
func unescapedQuote(literal string) bool {
	if len(literal) < 2 {
		return true
	}
	body := literal[1 : len(literal)-1]
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' {
			i++ // skip whatever this escapes
			continue
		}
		if body[i] == '"' {
			return true
		}
	}
	return false
}

func TestOriginOfReducesAReportedURLToItsHost(t *testing.T) {
	// This is what a browser actually sends in blocked-uri.
	for input, want := range map[string]string{
		"https://momento.example.com/collect/v1/events": "https://momento.example.com",
		"https://momento.example.com":                   "https://momento.example.com",
		"https://momento.example.com:8443/x?y=1":        "https://momento.example.com:8443",
	} {
		got, ok := OriginOf(input)
		if !ok || got != want {
			t.Fatalf("%q reduced to %q (ok=%v), expected %q", input, got, ok, want)
		}
	}
	// Keywords browsers report instead of a URL must not masquerade as origins.
	for _, keyword := range []string{"inline", "eval", "data", "", "blob"} {
		if _, ok := OriginOf(keyword); ok {
			t.Fatalf("%q must not be treated as an allowable origin", keyword)
		}
	}
}
