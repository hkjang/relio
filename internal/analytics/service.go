// Package analytics lets an operator opt in to their own visitor analytics.
//
// Two rules shape the design. First, no administrator-authored JavaScript is ever
// injected into the page: the loader is generated from validated fields, so the
// admin:write permission cannot become persistent script execution in every
// user's session. Second, the Content Security Policy is derived from the same
// validated origins, so enabling a provider does not require hand-editing a
// header — and cannot silently widen the policy beyond the origins listed here.
package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/auth"
	"github.com/hkjang/relio/internal/platform/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	DB    *pgxpool.Pool
	Audit *audit.Service

	// The policy is needed on every response, so it is cached and refreshed on
	// change rather than queried per request.
	mu       sync.RWMutex
	cached   *Policy
	cachedAt time.Time
}

// cacheTTL bounds how long another replica's change takes to appear. Writes on
// this instance invalidate immediately.
const cacheTTL = 30 * time.Second

type Provider struct {
	ID                string            `json:"id"`
	Provider          string            `json:"provider"`
	Name              string            `json:"name"`
	Enabled           bool              `json:"enabled"`
	SiteID            string            `json:"siteId,omitempty"`
	ScriptOrigin      string            `json:"scriptOrigin,omitempty"`
	ScriptPath        string            `json:"scriptPath,omitempty"`
	CollectOrigins    []string          `json:"collectOrigins"`
	ScriptAttributes  map[string]string `json:"scriptAttributes"`
	RespectDNT        bool              `json:"respectDnt"`
	AuthenticatedOnly bool              `json:"authenticatedOnly"`
	DisplayOrder      int               `json:"displayOrder"`
	UpdatedAt         time.Time         `json:"updatedAt"`
}

// Policy is the set of extra CSP sources the enabled providers require.
type Policy struct {
	ScriptSrc  []string `json:"scriptSrc"`
	ConnectSrc []string `json:"connectSrc"`
	ImgSrc     []string `json:"imgSrc"`
	Enabled    bool     `json:"enabled"`
}

// vendors describes what each supported provider needs, so validation and loader
// generation stay in one place.
var vendors = map[string]struct {
	label        string
	needsSiteID  bool
	needsOrigin  bool
	defaultPath  string
	pixelOrigins []string
}{
	"GA4":       {"Google Analytics 4", true, false, "", []string{"https://www.googletagmanager.com", "https://www.google-analytics.com", "https://region1.google-analytics.com"}},
	"MATOMO":    {"Matomo", true, true, "/matomo.js", nil},
	"PLAUSIBLE": {"Plausible", false, true, "/js/script.js", nil},
	"UMAMI":     {"Umami", true, true, "/script.js", nil},
	"SCRIPT":    {"직접 지정 스크립트", false, true, "", nil},
}

func Vendors() []map[string]any {
	out := []map[string]any{}
	for code, v := range vendors {
		out = append(out, map[string]any{"code": code, "label": v.label,
			"needsSiteId": v.needsSiteID, "needsOrigin": v.needsOrigin, "defaultPath": v.defaultPath})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["code"].(string) < out[j]["code"].(string) })
	return out
}

func (s *Service) List(ctx context.Context, p *auth.Principal) ([]Provider, error) {
	if err := auth.Require(p, "analytics:manage"); err != nil {
		return nil, err
	}
	return s.load(ctx, false)
}

func (s *Service) load(ctx context.Context, enabledOnly bool) ([]Provider, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,provider,name,enabled,COALESCE(site_id,''),COALESCE(script_origin,''),
		COALESCE(script_path,''),collect_origins,script_attributes,respect_dnt,authenticated_only,display_order,updated_at
		FROM analytics_providers WHERE (enabled OR NOT $1) ORDER BY display_order,name`, enabledOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Provider{}
	for rows.Next() {
		var x Provider
		var raw []byte
		if err = rows.Scan(&x.ID, &x.Provider, &x.Name, &x.Enabled, &x.SiteID, &x.ScriptOrigin, &x.ScriptPath,
			&x.CollectOrigins, &raw, &x.RespectDNT, &x.AuthenticatedOnly, &x.DisplayOrder, &x.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &x.ScriptAttributes)
		if x.ScriptAttributes == nil {
			x.ScriptAttributes = map[string]string{}
		}
		if x.CollectOrigins == nil {
			x.CollectOrigins = []string{}
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// validate normalises every field that reaches a header or generated script.
func validate(in *Provider) error {
	in.Provider = strings.ToUpper(strings.TrimSpace(in.Provider))
	vendor, ok := vendors[in.Provider]
	if !ok {
		return fmt.Errorf("unsupported provider %q", in.Provider)
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 60 {
		return errors.New("name is required and must be 60 characters or fewer")
	}
	siteID, err := ParseSiteID(in.SiteID)
	if err != nil {
		return err
	}
	in.SiteID = siteID
	if vendor.needsSiteID && in.SiteID == "" {
		return fmt.Errorf("%s requires a site or measurement id", vendor.label)
	}
	if in.ScriptOrigin != "" {
		origin, err := ParseOrigin(in.ScriptOrigin)
		if err != nil {
			return err
		}
		in.ScriptOrigin = origin
	}
	if vendor.needsOrigin && in.ScriptOrigin == "" {
		return fmt.Errorf("%s requires the origin that serves its script", vendor.label)
	}
	// A wildcard origin cannot be used to build a concrete script URL.
	if strings.Contains(in.ScriptOrigin, "*.") {
		return errors.New("the script origin must be an exact host, not a wildcard")
	}
	path, err := ParsePath(in.ScriptPath)
	if err != nil {
		return err
	}
	if path == "" {
		path = vendor.defaultPath
	}
	if in.Provider == "SCRIPT" && path == "" {
		return errors.New("직접 지정 스크립트는 스크립트 경로가 필요합니다")
	}
	in.ScriptPath = path

	normalized := []string{}
	seen := map[string]bool{}
	for _, raw := range in.CollectOrigins {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		origin, err := ParseOrigin(raw)
		if err != nil {
			return err
		}
		if !seen[origin] {
			seen[origin] = true
			normalized = append(normalized, origin)
		}
	}
	if len(normalized) > 10 {
		return errors.New("at most 10 collect origins may be listed")
	}
	in.CollectOrigins = normalized

	attributes := map[string]string{}
	for name, value := range in.ScriptAttributes {
		key, err := ParseAttributeName(name)
		if err != nil {
			return err
		}
		clean, err := ParseAttributeValue(value)
		if err != nil {
			return err
		}
		attributes[key] = clean
	}
	if len(attributes) > 15 {
		return errors.New("at most 15 script attributes may be set")
	}
	in.ScriptAttributes = attributes
	return nil
}

func (s *Service) Save(ctx context.Context, p *auth.Principal, in Provider, m Meta) (Provider, error) {
	if err := auth.Require(p, "analytics:manage"); err != nil {
		return Provider{}, err
	}
	if err := validate(&in); err != nil {
		return Provider{}, err
	}
	attributes, _ := json.Marshal(in.ScriptAttributes)
	id := in.ID
	creating := id == ""
	if creating {
		id = ids.New()
		_, err := s.DB.Exec(ctx, `INSERT INTO analytics_providers(id,provider,name,enabled,site_id,script_origin,script_path,
			collect_origins,script_attributes,respect_dnt,authenticated_only,display_order,updated_by)
			VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),$8,$9,$10,$11,$12,$13)`,
			id, in.Provider, in.Name, in.Enabled, in.SiteID, in.ScriptOrigin, in.ScriptPath,
			in.CollectOrigins, attributes, in.RespectDNT, in.AuthenticatedOnly, in.DisplayOrder, p.UserID)
		if err != nil {
			return Provider{}, err
		}
	} else {
		command, err := s.DB.Exec(ctx, `UPDATE analytics_providers SET provider=$2,name=$3,enabled=$4,site_id=NULLIF($5,''),
			script_origin=NULLIF($6,''),script_path=NULLIF($7,''),collect_origins=$8,script_attributes=$9,
			respect_dnt=$10,authenticated_only=$11,display_order=$12,updated_by=$13,updated_at=now() WHERE id=$1`,
			id, in.Provider, in.Name, in.Enabled, in.SiteID, in.ScriptOrigin, in.ScriptPath,
			in.CollectOrigins, attributes, in.RespectDNT, in.AuthenticatedOnly, in.DisplayOrder, p.UserID)
		if err != nil {
			return Provider{}, err
		}
		if command.RowsAffected() == 0 {
			return Provider{}, errors.New("analytics provider not found")
		}
	}
	s.invalidate()
	// Widening the policy for every user is worth a loud audit entry.
	action := "ANALYTICS_PROVIDER_UPDATE"
	if creating {
		action = "ANALYTICS_PROVIDER_CREATE"
	}
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: action,
		Resource: "analytics_provider", ResourceID: id,
		After: map[string]any{"provider": in.Provider, "name": in.Name, "enabled": in.Enabled,
			"scriptOrigin": in.ScriptOrigin, "collectOrigins": in.CollectOrigins, "siteId": in.SiteID},
		IP: m.IP, RequestID: m.RequestID, UserAgent: m.UserAgent})
	in.ID = id
	return in, nil
}

type Meta struct{ IP, RequestID, UserAgent string }

func (s *Service) Delete(ctx context.Context, p *auth.Principal, id string, m Meta) error {
	if err := auth.Require(p, "analytics:manage"); err != nil {
		return err
	}
	var name, provider string
	if err := s.DB.QueryRow(ctx, `SELECT name,provider FROM analytics_providers WHERE id=$1`, id).Scan(&name, &provider); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("analytics provider not found")
		}
		return err
	}
	if _, err := s.DB.Exec(ctx, `DELETE FROM analytics_providers WHERE id=$1`, id); err != nil {
		return err
	}
	s.invalidate()
	s.Audit.Record(ctx, audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN",
		Action: "ANALYTICS_PROVIDER_DELETE", Resource: "analytics_provider", ResourceID: id,
		Before: map[string]any{"name": name, "provider": provider},
		IP:     m.IP, RequestID: m.RequestID, UserAgent: m.UserAgent})
	return nil
}

func (s *Service) invalidate() {
	s.mu.Lock()
	s.cached = nil
	s.mu.Unlock()
}

// CurrentPolicy returns the extra CSP sources for the enabled providers. It is
// called on every response, so a failure degrades to the strict default rather
// than blocking the request.
func (s *Service) CurrentPolicy(ctx context.Context) Policy {
	s.mu.RLock()
	cached, at := s.cached, s.cachedAt
	s.mu.RUnlock()
	if cached != nil && time.Since(at) < cacheTTL {
		return *cached
	}
	providers, err := s.load(ctx, true)
	if err != nil {
		if cached != nil {
			return *cached
		}
		return Policy{}
	}
	policy := buildPolicy(providers)
	s.mu.Lock()
	s.cached, s.cachedAt = &policy, time.Now()
	s.mu.Unlock()
	return policy
}

func buildPolicy(providers []Provider) Policy {
	script, connect, img := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, x := range providers {
		if !x.Enabled {
			continue
		}
		if x.ScriptOrigin != "" {
			script[x.ScriptOrigin] = true
			// A vendor almost always posts events back to the host that served
			// its script, so allow that without extra configuration.
			connect[x.ScriptOrigin] = true
		}
		for _, origin := range x.CollectOrigins {
			connect[origin] = true
		}
		for _, origin := range vendors[x.Provider].pixelOrigins {
			script[origin] = true
			connect[origin] = true
			img[origin] = true
		}
	}
	policy := Policy{ScriptSrc: keys(script), ConnectSrc: keys(connect), ImgSrc: keys(img)}
	policy.Enabled = len(policy.ScriptSrc) > 0 || len(policy.ConnectSrc) > 0
	return policy
}

func keys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
