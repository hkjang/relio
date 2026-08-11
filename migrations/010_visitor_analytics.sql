-- Relio ships with no runtime external requests, and that stays the default.
-- Some operators still need their own visitor analytics (a self-hosted Matomo, an
-- internal collector). This lets an administrator opt in explicitly, per origin,
-- and records what the browser blocked so a misconfiguration is visible in the
-- console-free admin screen instead of only in each user's devtools.

CREATE TABLE analytics_providers (
    id uuid PRIMARY KEY,
    -- Known vendors get their loader generated from validated fields so no
    -- administrator-authored JavaScript is ever injected into the page.
    provider text NOT NULL CHECK (provider IN ('GA4', 'MATOMO', 'PLAUSIBLE', 'UMAMI', 'SCRIPT')),
    name text NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    -- Measurement id, site id, or website id depending on the vendor.
    site_id text,
    -- Origin that serves the vendor script, e.g. https://matomo.example.com.
    script_origin text,
    -- Path of the script on that origin. Vendors differ: /matomo.js, /script.js.
    script_path text,
    -- Extra origins the script posts events to, when different from script_origin.
    collect_origins text[] NOT NULL DEFAULT '{}',
    -- data-* attributes for vendors configured through the script tag.
    script_attributes jsonb NOT NULL DEFAULT '{}'::jsonb,
    respect_dnt boolean NOT NULL DEFAULT true,
    -- Track only signed-in application pages, never the login screen.
    authenticated_only boolean NOT NULL DEFAULT false,
    display_order integer NOT NULL DEFAULT 100,
    updated_by uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX analytics_providers_enabled_idx ON analytics_providers(enabled, display_order);

-- Browsers report blocked resources here. Surfacing them turns "it silently does
-- not work" into a named origin an administrator can allow in one click.
CREATE TABLE csp_violations (
    id uuid PRIMARY KEY,
    directive text NOT NULL,
    blocked_origin text NOT NULL,
    document_uri text,
    -- Rolled up rather than appended so one broken page cannot flood the table.
    occurrences integer NOT NULL DEFAULT 1,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    resolved boolean NOT NULL DEFAULT false,
    UNIQUE (directive, blocked_origin)
);
CREATE INDEX csp_violations_recent_idx ON csp_violations(resolved, last_seen_at DESC);

-- Configuring analytics means widening the Content Security Policy for every
-- user, so it is a distinct duty from ordinary administration.
INSERT INTO role_permissions (role_id, permission)
SELECT r.id, 'analytics:manage'
FROM roles r
WHERE r.code = 'SYSTEM_ADMIN'
ON CONFLICT DO NOTHING;
