package analytics

import (
	"strings"
	"testing"
)

// Momento is the in-house collector, the only provider whose data stays on the
// network. It leads the list, and with the same-origin proxy on it must leave
// the Content Security Policy exactly as shipped.

func momento(proxy bool) Provider {
	return Provider{Provider: ProviderMomento, Name: "사내 Momento", Enabled: true, SiteID: "relio-prd",
		ScriptOrigin: "https://momento.example.com", ScriptPath: "/tracker.js", SameOriginProxy: proxy}
}

func TestMomentoLeadsTheVendorList(t *testing.T) {
	list := Vendors()
	if len(list) == 0 || list[0]["code"] != ProviderMomento {
		t.Fatalf("Momento must be the first vendor offered, got %v", list)
	}
	for _, vendor := range list {
		if supports, _ := vendor["supportsProxy"].(bool); supports != (vendor["code"] == ProviderMomento) {
			t.Fatalf("only Momento supports the same-origin proxy: %v", vendor)
		}
	}
	// The rest stay alphabetical so the picker does not reorder between releases.
	for i := 2; i < len(list); i++ {
		if list[i-1]["code"].(string) > list[i]["code"].(string) {
			t.Fatalf("vendors after Momento must be alphabetical: %v", list)
		}
	}
}

func TestValidateAppliesMomentoDefaultsAndLimitsTheProxyToMomento(t *testing.T) {
	in := Provider{Provider: "momento", Name: "m", SiteID: "site-1", ScriptOrigin: "https://momento.example.com/", SameOriginProxy: true}
	if err := validate(&in); err != nil {
		t.Fatal(err)
	}
	if in.Provider != ProviderMomento || in.ScriptPath != "/tracker.js" || in.ScriptOrigin != "https://momento.example.com" || !in.SameOriginProxy {
		t.Fatalf("Momento defaults not applied: %+v", in)
	}
	for name, bad := range map[string]Provider{
		"no site id": {Provider: ProviderMomento, Name: "m", ScriptOrigin: "https://momento.example.com"},
		"no origin":  {Provider: ProviderMomento, Name: "m", SiteID: "site-1"},
		"wildcard":   {Provider: ProviderMomento, Name: "m", SiteID: "site-1", ScriptOrigin: "https://*.example.com"},
	} {
		candidate := bad
		if err := validate(&candidate); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
	// A stale checkbox on another vendor is dropped, not fatal: the proxy has
	// exactly one upstream and only Momento's tracker takes a data-endpoint.
	other := Provider{Provider: "MATOMO", Name: "m", SiteID: "1", ScriptOrigin: "https://matomo.example.com", SameOriginProxy: true}
	if err := validate(&other); err != nil {
		t.Fatal(err)
	}
	if other.SameOriginProxy {
		t.Fatal("the same-origin proxy must be cleared for a non-Momento provider")
	}
}

func TestProxiedMomentoKeepsTheShippedPolicy(t *testing.T) {
	policy := buildPolicy([]Provider{momento(true)})
	if policy.Enabled || len(policy.ScriptSrc) > 0 || len(policy.ConnectSrc) > 0 || len(policy.ImgSrc) > 0 {
		t.Fatalf("through the proxy the collector is same-origin; the policy must not widen: %+v", policy)
	}
	// Without the proxy the collector is an external origin like any other.
	direct := buildPolicy([]Provider{momento(false)})
	if !contains(direct.ScriptSrc, "https://momento.example.com") || !contains(direct.ConnectSrc, "https://momento.example.com") {
		t.Fatalf("a direct Momento provider needs its origin in script-src and connect-src: %+v", direct)
	}
}

func TestMomentoUpstreamFollowsEnabledProxiedProvidersOnly(t *testing.T) {
	if got := momentoUpstream(nil); got != "" {
		t.Fatalf("no provider, no upstream; got %q", got)
	}
	disabled := momento(true)
	disabled.Enabled = false
	direct := momento(false)
	if got := momentoUpstream([]Provider{disabled, direct}); got != "" {
		t.Fatalf("a disabled or direct provider must not open the proxy; got %q", got)
	}
	// Rows arrive in display order; the first proxied one is the upstream.
	second := momento(true)
	second.ScriptOrigin = "https://momento-2.example.com"
	if got := momentoUpstream([]Provider{direct, momento(true), second}); got != "https://momento.example.com" {
		t.Fatalf("expected the first proxied provider, got %q", got)
	}
	// Any other vendor never becomes the upstream even with the flag set.
	matomo := Provider{Provider: "MATOMO", Enabled: true, ScriptOrigin: "https://matomo.example.com", SameOriginProxy: true}
	if got := momentoUpstream([]Provider{matomo}); got != "" {
		t.Fatalf("only Momento may be proxied; got %q", got)
	}
}

func TestMomentoLoaderPointsAtTheProxyWhenAskedAndAtTheCollectorOtherwise(t *testing.T) {
	proxied, err := renderProvider(momento(true))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`inject("/momento/tracker.js"`, `"data-endpoint":"/momento"`, `"data-site-id":"relio-prd"`, `"data-environment":"prd"`, `"data-contract-version":"1"`} {
		if !strings.Contains(proxied, want) {
			t.Fatalf("proxied loader must contain %s:\n%s", want, proxied)
		}
	}
	if strings.Contains(proxied, "momento.example.com") {
		t.Fatalf("the proxied loader must not name the collector:\n%s", proxied)
	}

	direct, err := renderProvider(momento(false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(direct, `inject("https://momento.example.com/tracker.js"`) || strings.Contains(direct, "data-endpoint") {
		t.Fatalf("a direct loader must load from the collector and leave the endpoint alone:\n%s", direct)
	}

	// An administrator may point a staging deployment at a staging environment
	// through the attributes, but the site id is always the validated one.
	custom := momento(true)
	custom.ScriptAttributes = map[string]string{"data-environment": "stg", "data-site-id": "spoofed"}
	rendered, err := renderProvider(custom)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, `"data-environment":"stg"`) || !strings.Contains(rendered, `"data-site-id":"relio-prd"`) || strings.Contains(rendered, "spoofed") {
		t.Fatalf("attributes override the environment but never the site id:\n%s", rendered)
	}

	if _, err := renderProvider(Provider{Provider: ProviderMomento, ScriptOrigin: "https://momento.example.com"}); err == nil {
		t.Fatal("a Momento row without a site id must be skipped, not rendered")
	}
}
