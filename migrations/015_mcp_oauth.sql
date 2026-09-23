-- MCP over the organisation account (OAuth 2.1 through Keycloak). Relio acts as
-- the OAuth resource server: it publishes Protected Resource Metadata naming
-- Keycloak, and accepts Keycloak access tokens on /mcp. Off by default — an
-- agent holding a user's token acts with that user's full Role, so the
-- administrator opts in, exactly as with silent SSO.
INSERT INTO system_settings(namespace,key,value,value_type) VALUES
('mcp','oauth_enabled','false','boolean'),
('mcp','oauth_required_scope','""','string'),
-- The public client an administrator registered in Keycloak for CLI agents.
-- Relio cannot discover it, and users need it in their client configuration;
-- when empty, the guide explains Dynamic Client Registration instead.
('mcp','oauth_client_id','""','string')
ON CONFLICT (namespace,key) DO NOTHING;

-- Tell Personal Key traffic from OAuth traffic in the request log, and record
-- which OAuth client (Keycloak azp) an agent signed in through.
ALTER TABLE mcp_request_logs
    ADD COLUMN auth_method text,
    ADD COLUMN oauth_client text;
