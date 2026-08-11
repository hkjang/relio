-- Bind PostgreSQL to the exact Instance Master Key in the companion
-- /var/lib/relio volume. The one-way fingerprint is registered by the
-- application after the migration and never contains recoverable key data.
CREATE TABLE instance_key_registry (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    registered_at timestamptz NOT NULL DEFAULT now()
);

-- Prevent concurrent administrator tabs from overwriting a newer OIDC
-- configuration or accidentally replacing its encrypted Client Secret.
ALTER TABLE oidc_providers
    ADD COLUMN version integer NOT NULL DEFAULT 1 CHECK (version > 0);
