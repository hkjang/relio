-- Relio managed the selling side of a customer relationship but nothing after
-- the contract: complaints, requests and questions had no home, so they lived in
-- email and were invisible to the account owner. This adds the post-sale half of
-- the lifecycle as first-class records that share the same Data Scope rules as
-- customers and opportunities.

CREATE TABLE voice_categories (
    id uuid PRIMARY KEY,
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    voice_type text NOT NULL CHECK (voice_type IN ('COMPLAINT', 'REQUEST', 'INQUIRY', 'DEFECT', 'PRAISE', 'CHURN_RISK')),
    -- Target hours from intake to first response and to resolution. The SLA is a
    -- category property so an administrator can tighten it without code changes.
    response_hours integer NOT NULL DEFAULT 8 CHECK (response_hours > 0),
    resolution_hours integer NOT NULL DEFAULT 72 CHECK (resolution_hours > 0),
    active boolean NOT NULL DEFAULT true,
    display_order integer NOT NULL DEFAULT 100,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE customer_voices (
    id uuid PRIMARY KEY,
    voice_no text NOT NULL UNIQUE,
    customer_id uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    contact_id uuid REFERENCES contacts(id),
    opportunity_id uuid REFERENCES opportunities(id),
    contract_id uuid REFERENCES contracts(id),
    category_id uuid REFERENCES voice_categories(id),
    voice_type text NOT NULL CHECK (voice_type IN ('COMPLAINT', 'REQUEST', 'INQUIRY', 'DEFECT', 'PRAISE', 'CHURN_RISK')),
    channel text NOT NULL DEFAULT 'PHONE' CHECK (channel IN ('PHONE', 'EMAIL', 'VISIT', 'PORTAL', 'CHAT', 'PARTNER', 'OTHER')),
    title text NOT NULL,
    body text,
    severity text NOT NULL DEFAULT 'NORMAL' CHECK (severity IN ('LOW', 'NORMAL', 'HIGH', 'CRITICAL')),
    status text NOT NULL DEFAULT 'RECEIVED' CHECK (status IN ('RECEIVED', 'IN_REVIEW', 'IN_PROGRESS', 'PENDING_CUSTOMER', 'RESOLVED', 'CLOSED', 'REJECTED')),
    owner_id uuid NOT NULL REFERENCES users(id),
    organization_id uuid REFERENCES organizations(id),
    occurred_at timestamptz NOT NULL DEFAULT now(),
    -- Denormalised SLA deadlines so overdue filtering stays a single index scan.
    response_due_at timestamptz,
    resolution_due_at timestamptz,
    first_responded_at timestamptz,
    resolved_at timestamptz,
    closed_at timestamptz,
    resolution text,
    root_cause text,
    -- Recurrence prevention: what we changed so this does not happen again.
    preventive_action text,
    satisfaction_score integer CHECK (satisfaction_score BETWEEN 1 AND 5),
    satisfaction_comment text,
    linked_activity_id uuid REFERENCES activities(id),
    custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    version integer NOT NULL DEFAULT 1,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX customer_voices_customer_idx ON customer_voices(customer_id, occurred_at DESC);
CREATE INDEX customer_voices_owner_idx ON customer_voices(owner_id, status);
CREATE INDEX customer_voices_open_idx ON customer_voices(status, resolution_due_at)
    WHERE status NOT IN ('RESOLVED', 'CLOSED', 'REJECTED');
CREATE INDEX customer_voices_org_idx ON customer_voices(organization_id);

-- Every status change and customer contact is appended, never overwritten, so
-- the handling history of a complaint is auditable end to end.
CREATE TABLE customer_voice_events (
    id uuid PRIMARY KEY,
    voice_id uuid NOT NULL REFERENCES customer_voices(id) ON DELETE CASCADE,
    event_type text NOT NULL CHECK (event_type IN ('CREATED', 'STATUS_CHANGE', 'COMMENT', 'CUSTOMER_CONTACT', 'ASSIGNED', 'ESCALATED', 'RESOLVED', 'REOPENED', 'SATISFACTION')),
    from_status text,
    to_status text,
    note text,
    actor_id uuid NOT NULL REFERENCES users(id),
    occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX customer_voice_events_voice_idx ON customer_voice_events(voice_id, occurred_at);

-- Sequence for the human-readable ticket number (VOC-2026-000001).
CREATE SEQUENCE customer_voice_no_seq;

INSERT INTO voice_categories (id, code, name, voice_type, response_hours, resolution_hours, display_order)
VALUES
    ('b2f4a1c0-0001-4a10-9c01-000000000001', 'DELIVERY_DELAY', '납기 지연', 'COMPLAINT', 4, 48, 10),
    ('b2f4a1c0-0001-4a10-9c01-000000000002', 'QUALITY_DEFECT', '품질 불량', 'DEFECT', 2, 24, 20),
    ('b2f4a1c0-0001-4a10-9c01-000000000003', 'BILLING_ISSUE', '청구·정산 오류', 'COMPLAINT', 8, 72, 30),
    ('b2f4a1c0-0001-4a10-9c01-000000000004', 'FEATURE_REQUEST', '기능 개선 요청', 'REQUEST', 24, 720, 40),
    ('b2f4a1c0-0001-4a10-9c01-000000000005', 'USAGE_INQUIRY', '사용 문의', 'INQUIRY', 8, 48, 50),
    ('b2f4a1c0-0001-4a10-9c01-000000000006', 'SUPPORT_RESPONSE', '지원 응대 불만', 'COMPLAINT', 4, 48, 60),
    ('b2f4a1c0-0001-4a10-9c01-000000000007', 'CONTRACT_TERMS', '계약 조건 협의', 'REQUEST', 24, 168, 70),
    ('b2f4a1c0-0001-4a10-9c01-000000000008', 'CHURN_SIGNAL', '이탈 징후', 'CHURN_RISK', 2, 24, 80),
    ('b2f4a1c0-0001-4a10-9c01-000000000009', 'COMPLIMENT', '감사·칭찬', 'PRAISE', 48, 168, 90)
ON CONFLICT (code) DO NOTHING;

-- VOC handling is a distinct duty from selling, so it gets its own permissions
-- and is granted to the roles that ship with Relio.
INSERT INTO role_permissions (role_id, permission)
SELECT r.id, permission
FROM roles r
CROSS JOIN (VALUES ('voice:read'), ('voice:write')) AS granted(permission)
WHERE r.code IN ('SALES_USER', 'SALES_MANAGER')
ON CONFLICT DO NOTHING;

-- Resolving other people's tickets and editing categories is a lead activity.
INSERT INTO role_permissions (role_id, permission)
SELECT r.id, 'voice:manage'
FROM roles r
WHERE r.code = 'SALES_MANAGER'
ON CONFLICT DO NOTHING;

INSERT INTO system_settings (namespace, key, value, value_type, secret_yn, restart_required)
VALUES
    ('voice', 'escalate_severity', '"HIGH"'::jsonb, 'string', false, false),
    ('voice', 'churn_risk_days', '30'::jsonb, 'number', false, false),
    ('voice', 'satisfaction_survey_enabled', 'true'::jsonb, 'boolean', false, false)
ON CONFLICT (namespace, key) DO NOTHING;
