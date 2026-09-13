-- Silent SSO (OIDC prompt=none). A visitor who already holds a Keycloak
-- session is signed in without seeing the login screen. Off by default: the
-- administrator opts in, and the server refuses a prompt=none request while
-- this is off so the redirect surface stays tied to the setting.
ALTER TABLE oidc_providers
    ADD COLUMN auto_login boolean NOT NULL DEFAULT false;

-- A silent attempt that the provider refuses (login_required) must land on the
-- login screen with a "do not retry" marker instead of an error, and a deep
-- link must return to where the visitor started. Both are decided when the
-- login starts, so they ride along with the state row.
ALTER TABLE oidc_login_states
    ADD COLUMN silent boolean NOT NULL DEFAULT false,
    ADD COLUMN return_to text NOT NULL DEFAULT '/app';
