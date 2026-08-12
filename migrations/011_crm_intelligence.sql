-- Relio recorded what happened but never said what it meant. Deal Health and
-- churn risk were computed on demand and forgotten, so nothing accumulated: a
-- customer that had been silent for a month looked identical to one contacted
-- yesterday until somebody opened the record and worked it out.
--
-- These four tables make that reasoning durable. Signals are observations, Risks
-- are scored judgements, Insights explain them in a sentence, and
-- Recommendations say what to do next and can become real work.
--
-- None of them carry owner_id or organization_id. Everything here is derived
-- from a customer, so visibility is resolved by joining customers and applying
-- the existing Data Scope predicate. Copying the owner onto derived rows would
-- let intelligence outlive a reassignment and show a salesperson an account
-- they no longer hold.

CREATE TABLE signals (
    id uuid PRIMARY KEY,
    signal_type text NOT NULL,
    sentiment text NOT NULL DEFAULT 'NEUTRAL' CHECK (sentiment IN ('POSITIVE', 'NEGATIVE', 'NEUTRAL')),
    severity text NOT NULL DEFAULT 'LOW' CHECK (severity IN ('LOW', 'MEDIUM', 'HIGH', 'CRITICAL')),
    entity_type text NOT NULL CHECK (entity_type IN ('ACCOUNT', 'OPPORTUNITY', 'VOC', 'CONTRACT')),
    entity_id uuid NOT NULL,
    account_id uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    -- The numbers behind the sentence: days since contact, days in stage, and so
    -- on. Kept so a screen can explain a signal without recomputing it.
    evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
    detected_at timestamptz NOT NULL DEFAULT now(),
    source_type text NOT NULL,
    source_id uuid,
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'RESOLVED', 'IGNORED')),
    resolved_at timestamptz,
    -- One open signal per (type, entity). Re-running the engine refreshes the
    -- existing row rather than stacking duplicates every time it runs.
    dedupe_key text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX signals_account_idx ON signals(account_id, status, severity);
CREATE INDEX signals_entity_idx ON signals(entity_type, entity_id, status);
CREATE INDEX signals_detected_idx ON signals(detected_at DESC);

CREATE TABLE risks (
    id uuid PRIMARY KEY,
    risk_type text NOT NULL,
    entity_type text NOT NULL CHECK (entity_type IN ('ACCOUNT', 'OPPORTUNITY', 'VOC', 'CONTRACT')),
    entity_id uuid NOT NULL,
    account_id uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    risk_score integer NOT NULL DEFAULT 0 CHECK (risk_score BETWEEN 0 AND 100),
    severity text NOT NULL DEFAULT 'LOW' CHECK (severity IN ('LOW', 'MEDIUM', 'HIGH', 'CRITICAL')),
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    -- Which signals drove the score, so "why 72?" has an answer.
    factors jsonb NOT NULL DEFAULT '[]'::jsonb,
    detected_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    -- An accepted risk is one a human looked at and decided to live with. The
    -- engine must not quietly reopen it, so the note records who and why.
    accepted_by uuid REFERENCES users(id),
    accepted_note text,
    status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'RESOLVED', 'ACCEPTED')),
    dedupe_key text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX risks_account_idx ON risks(account_id, status, risk_score DESC);
CREATE INDEX risks_entity_idx ON risks(entity_type, entity_id, status);

CREATE TABLE insights (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    opportunity_id uuid REFERENCES opportunities(id) ON DELETE CASCADE,
    insight_type text NOT NULL,
    title text NOT NULL,
    summary text NOT NULL DEFAULT '',
    -- The signal titles the summary is built from, shown as "근거".
    evidence jsonb NOT NULL DEFAULT '[]'::jsonb,
    confidence integer NOT NULL DEFAULT 50 CHECK (confidence BETWEEN 0 AND 100),
    generated_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'EXPIRED')),
    dedupe_key text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX insights_account_idx ON insights(account_id, status, generated_at DESC);

CREATE TABLE recommendations (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    opportunity_id uuid REFERENCES opportunities(id) ON DELETE CASCADE,
    recommendation_type text NOT NULL,
    priority text NOT NULL DEFAULT 'MEDIUM' CHECK (priority IN ('LOW', 'MEDIUM', 'HIGH')),
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    due_date date,
    source_type text NOT NULL CHECK (source_type IN ('SIGNAL', 'INSIGHT', 'RISK')),
    source_id uuid,
    -- A recommendation is addressed to whoever owns the account today. It is
    -- resolved at generation time and refreshed when ownership changes, so "내
    -- 추천 행동" never needs to walk the ownership chain.
    assignee_id uuid NOT NULL REFERENCES users(id),
    status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'ACCEPTED', 'DISMISSED', 'COMPLETED')),
    -- Accepting a recommendation creates real work; this is the link to it.
    task_id uuid REFERENCES tasks(id) ON DELETE SET NULL,
    decided_by uuid REFERENCES users(id),
    decided_at timestamptz,
    dismiss_reason text,
    generated_at timestamptz NOT NULL DEFAULT now(),
    dedupe_key text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX recommendations_assignee_idx ON recommendations(assignee_id, status, priority);
CREATE INDEX recommendations_account_idx ON recommendations(account_id, status);

-- The engine's own bookkeeping: when it last ran and what it produced. Without
-- this a screen cannot tell "no risks" from "never analysed".
CREATE TABLE intelligence_runs (
    id uuid PRIMARY KEY,
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    trigger text NOT NULL DEFAULT 'SCHEDULE' CHECK (trigger IN ('SCHEDULE', 'MANUAL', 'STARTUP')),
    triggered_by uuid REFERENCES users(id),
    accounts_scanned integer NOT NULL DEFAULT 0,
    signals_opened integer NOT NULL DEFAULT 0,
    signals_resolved integer NOT NULL DEFAULT 0,
    risks_opened integer NOT NULL DEFAULT 0,
    risks_resolved integer NOT NULL DEFAULT 0,
    insights_generated integer NOT NULL DEFAULT 0,
    recommendations_generated integer NOT NULL DEFAULT 0,
    error text
);
CREATE INDEX intelligence_runs_started_idx ON intelligence_runs(started_at DESC);

-- The shipped sales Roles get to see intelligence about the accounts they
-- already own, and to act on their own recommendations. Running the engine is
-- an administrator action (admin:* covers it) because it loads the database.
INSERT INTO role_permissions (role_id, permission)
SELECT r.id, permission
FROM roles r
CROSS JOIN (VALUES ('intelligence:read'), ('intelligence:write')) AS granted(permission)
WHERE r.code IN ('SALES_USER', 'SALES_MANAGER')
ON CONFLICT DO NOTHING;
