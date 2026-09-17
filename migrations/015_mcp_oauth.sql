-- MCP over SSO (OAuth 2.1 resource server, RFC 9728). /mcp keeps accepting
-- Personal Keys; with this switched on it also accepts a Keycloak access token
-- issued for this deployment, so an MCP client can be handed the URL alone and
-- sign the person in itself. Off by default: a fresh install publishes no
-- metadata and refuses tokens exactly as before. The issuer, client id and
-- username claim are the web sign-in's (oidc_providers) — nothing is duplicated.
--
-- The keys are spelled mcp.oauth.<name> in every product that carries this
-- feature, so an operator learns them once. Here that is namespace "mcp" with a
-- dotted key.
INSERT INTO system_settings(namespace, key, value, value_type) VALUES
('mcp','oauth.enabled','false','boolean'),
-- Resource identifier (RFC 8707): the public MCP URL the token's aud must name.
-- Empty derives <system.service_url>/mcp.
('mcp','oauth.resource','""','string'),
-- Space-separated extra audiences. A token whose aud or azp names one of these
-- is accepted without an Audience mapper (Keycloak puts the client id in azp).
('mcp','oauth.audience','""','string'),
-- Space-separated scopes an SSO subject receives, intersected with the
-- person's own permissions exactly as a Personal Key's scopes are. Read-only
-- by default.
('mcp','oauth.scopes','"mcp:use customer:read contact:read lead:read opportunity:read activity:read product:read quotation:read contract:read sales:read target:read forecast:read notification:read report:read voice:read intelligence:read"','string')
ON CONFLICT (namespace, key) DO NOTHING;
