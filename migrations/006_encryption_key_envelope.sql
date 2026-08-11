-- Envelope the Instance Data Key so the credential material in PostgreSQL is no
-- longer tied to a single /var/lib/relio volume. The data key itself never
-- changes; only the key that wraps it does. That lets an operator supply
-- ENCRYPTION_KEY and keep every Personal Key digest, OIDC Client Secret and
-- encrypted setting valid across restarts and container rebuilds.
CREATE TABLE instance_data_key (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    wrapped_dek bytea NOT NULL,
    wrap_origin text NOT NULL CHECK (wrap_origin IN ('ENCRYPTION_KEY', 'FILE')),
    wrap_fingerprint text NOT NULL CHECK (wrap_fingerprint ~ '^[0-9a-f]{64}$'),
    dek_fingerprint text NOT NULL CHECK (dek_fingerprint ~ '^[0-9a-f]{64}$'),
    algorithm text NOT NULL DEFAULT 'AES-256-GCM',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Records every adoption and rotation of the wrapping key so an administrator
-- can prove when protection changed without exposing any key material.
CREATE TABLE instance_data_key_events (
    id uuid PRIMARY KEY,
    event text NOT NULL CHECK (event IN ('INITIALIZED', 'ADOPTED', 'REWRAPPED')),
    from_origin text,
    to_origin text NOT NULL,
    wrap_fingerprint text NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT now()
);
