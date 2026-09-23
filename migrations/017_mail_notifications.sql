-- Event notifications through a company SMTP relay. Off by default: a fresh
-- install changes nothing until an administrator turns mail.enabled on. The
-- keys follow the shared mail standard so an operator learns them once.
--
-- mail.password is deliberately not seeded. Secret settings are encrypted with
-- the instance data key when the administrator first types one, and the
-- settings API only ever reports that it is configured.
INSERT INTO system_settings(namespace,key,value,value_type) VALUES
('mail','enabled','false','boolean'),
('mail','smtp_host','""','string'),
('mail','smtp_port','25','number'),
('mail','security','"auto"','string'),
('mail','skip_tls_verify','false','boolean'),
('mail','username','""','string'),
('mail','from_address','""','string'),
('mail','from_name','"Relio"','string'),
('mail','base_url','""','string'),
('mail','timeout_seconds','10','number'),
('mail','notify_approval_requested','true','boolean'),
('mail','notify_approval_decided','true','boolean'),
('mail','notify_voice_assigned','true','boolean'),
('mail','notify_contract_renewal','true','boolean')
ON CONFLICT(namespace,key) DO NOTHING;

-- Every attempt is recorded, sent or not, so an administrator can answer "it
-- never arrived". The body is not stored: subject and recipient are enough, and
-- a log that carries bodies is a leak in its own right.
CREATE TABLE mail_deliveries (
    id uuid PRIMARY KEY,
    event text NOT NULL,
    recipient text NOT NULL,
    subject text NOT NULL,
    entity_type text,
    entity_id text,
    actor_id uuid REFERENCES users(id),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','sent','failed')),
    attempts integer NOT NULL DEFAULT 0,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mail_deliveries_created_idx ON mail_deliveries(created_at DESC);
CREATE INDEX mail_deliveries_status_idx ON mail_deliveries(status);

-- Scheduled notices (a contract entering its renewal window) fire once per
-- entity. This ledger is what keeps the maintenance tick from telling the same
-- owner about the same contract every minute.
CREATE TABLE mail_notices (
    event text NOT NULL,
    entity_id text NOT NULL,
    notified_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event, entity_id)
);
