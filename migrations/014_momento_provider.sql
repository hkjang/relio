-- Momento is the in-house, self-hosted visitor collector: the only provider whose
-- data never leaves the network, so it goes first in the list. Its tracker is a
-- single <script async src=".../tracker.js" data-site-id=...> tag, which the
-- generated loader already knows how to inject.
ALTER TABLE analytics_providers
    DROP CONSTRAINT analytics_providers_provider_check;
ALTER TABLE analytics_providers
    ADD CONSTRAINT analytics_providers_provider_check
    CHECK (provider IN ('MOMENTO', 'GA4', 'MATOMO', 'PLAUSIBLE', 'UMAMI', 'SCRIPT'));

-- Same-origin proxy. When set, Relio forwards /momento/* to the collector and
-- the tracker is told to report to /momento, so no external origin has to be
-- added to the Content Security Policy at all — which is what makes tracking
-- possible where the policy cannot be widened. Only Momento supports this; the
-- column stays false for every other vendor. Existing rows are untouched.
ALTER TABLE analytics_providers
    ADD COLUMN same_origin_proxy boolean NOT NULL DEFAULT false;
