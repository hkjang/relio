-- Personal key scopes and channels can be changed after issuance. Versioning
-- prevents two open browser tabs from silently overwriting each other's
-- security decisions.
ALTER TABLE personal_keys
    ADD COLUMN version integer NOT NULL DEFAULT 1;

