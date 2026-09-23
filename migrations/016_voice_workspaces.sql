-- VOC workspaces. One department can run its own intake on the same VOC
-- engine: its own request types, its own intake and resolution fields, its
-- own customer code, and — when it handles data other departments must not
-- see — isolation from both their screens and the sales metrics that read
-- customer requests. Existing requests keep workspace_id NULL and behave
-- exactly as before.
CREATE TABLE voice_workspaces (
    id uuid PRIMARY KEY,
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    description text,
    -- The owning department. Members of this organisation or any of its
    -- children may see an isolated workspace.
    organization_id uuid REFERENCES organizations(id),
    -- Isolated: invisible outside the owning organisation tree, and never
    -- counted in churn risk, intelligence signals or the Today queue.
    isolated boolean NOT NULL DEFAULT false,
    -- Knowledge gate: resolving requires a cause-evidence level, and reviewed
    -- cases become the only default source for agent knowledge search.
    knowledge_gate boolean NOT NULL DEFAULT false,
    -- How this department names and formats the customer code, e.g.
    -- 회원사코드 / ^[0-9]{12}$. Quick registration requires it when set.
    customer_code_label text,
    customer_code_pattern text,
    active boolean NOT NULL DEFAULT true,
    display_order integer NOT NULL DEFAULT 100,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT voice_workspaces_isolated_needs_owner CHECK (NOT isolated OR organization_id IS NOT NULL)
);

-- Request types belong to a workspace (NULL = the general set). SLA can be
-- switched off per type: no deadline is computed, stored or shown.
ALTER TABLE voice_categories
    ADD COLUMN workspace_id uuid REFERENCES voice_workspaces(id),
    ADD COLUMN sla_enabled boolean NOT NULL DEFAULT true;
CREATE INDEX voice_categories_workspace_idx ON voice_categories(workspace_id);

ALTER TABLE customer_voices
    ADD COLUMN workspace_id uuid REFERENCES voice_workspaces(id),
    -- How well the stated cause is established. Recorded as it is, never
    -- rounded up: "UNIDENTIFIED" is a legitimate way to close a case.
    ADD COLUMN cause_evidence text CHECK (cause_evidence IN ('CONFIRMED', 'PRESUMED', 'UNIDENTIFIED')),
    -- The verification gate. Only a reviewer changes it; new cases start
    -- unreviewed, and reopening a case sends it back there.
    ADD COLUMN knowledge_status text NOT NULL DEFAULT 'UNREVIEWED'
        CHECK (knowledge_status IN ('UNREVIEWED', 'IN_REVIEW', 'APPROVED', 'EXCLUDED')),
    ADD COLUMN knowledge_reviewed_by uuid REFERENCES users(id),
    ADD COLUMN knowledge_reviewed_at timestamptz;
CREATE INDEX customer_voices_workspace_idx ON customer_voices(workspace_id, status);
CREATE INDEX customer_voices_knowledge_idx ON customer_voices(knowledge_status, resolved_at DESC)
    WHERE status IN ('RESOLVED', 'CLOSED');

ALTER TABLE customer_voice_events DROP CONSTRAINT customer_voice_events_event_type_check;
ALTER TABLE customer_voice_events ADD CONSTRAINT customer_voice_events_event_type_check
    CHECK (event_type IN ('CREATED', 'STATUS_CHANGE', 'COMMENT', 'CUSTOMER_CONTACT', 'ASSIGNED', 'ESCALATED',
        'RESOLVED', 'REOPENED', 'SATISFACTION', 'KNOWLEDGE_REVIEW'));

-- Custom fields reach the VOC screens. A VOICE field belongs to a workspace
-- and is collected at intake or at resolution; help text is shown under it.
ALTER TABLE custom_field_definitions
    ADD COLUMN workspace_id uuid REFERENCES voice_workspaces(id) ON DELETE CASCADE,
    ADD COLUMN phase text CHECK (phase IN ('INTAKE', 'RESOLUTION')),
    ADD COLUMN help_text text;

-- A customer code a department already uses (회원사코드 and the like). Unique
-- across the company, so bulk registration and quick registration cannot
-- create the same member twice.
ALTER TABLE customers ADD COLUMN customer_code text;
CREATE UNIQUE INDEX customers_customer_code_key ON customers(customer_code) WHERE customer_code IS NOT NULL;

-- A customer registered through a workspace (quick or bulk registration)
-- remembers it. When that workspace is isolated, the customer is the
-- department's member, not a sales account: other departments do not see it,
-- and the intelligence engine and churn ranking do not treat it as one — a
-- few thousand members must not become a few thousand "no contact in 30 days"
-- recommendations for people who never sell to them.
ALTER TABLE customers ADD COLUMN home_workspace_id uuid REFERENCES voice_workspaces(id);
CREATE INDEX customers_home_workspace_idx ON customers(home_workspace_id) WHERE home_workspace_id IS NOT NULL;
