CREATE TABLE organizations (
    id uuid PRIMARY KEY,
    parent_id uuid REFERENCES organizations(id),
    name text NOT NULL,
    code text NOT NULL UNIQUE,
    org_type text NOT NULL DEFAULT 'COMPANY',
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX organizations_parent_idx ON organizations(parent_id);

CREATE TABLE users (
    id uuid PRIMARY KEY,
    username text NOT NULL UNIQUE,
    email text,
    display_name text NOT NULL,
    password_hash text,
    auth_source text NOT NULL DEFAULT 'LOCAL',
    oidc_subject text UNIQUE,
    organization_id uuid REFERENCES organizations(id),
    manager_id uuid REFERENCES users(id),
    title text,
    phone text,
    locale text NOT NULL DEFAULT 'ko-KR',
    timezone text NOT NULL DEFAULT 'Asia/Seoul',
    active boolean NOT NULL DEFAULT true,
    is_bootstrap boolean NOT NULL DEFAULT false,
    must_change_password boolean NOT NULL DEFAULT false,
    version integer NOT NULL DEFAULT 1,
    last_login_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE roles (
    id uuid PRIMARY KEY,
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    description text,
    data_scope text NOT NULL DEFAULT 'USER' CHECK (data_scope IN ('USER','TEAM','DEPARTMENT','DIVISION','COMPANY')),
    system_role boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE role_permissions (
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission text NOT NULL,
    PRIMARY KEY (role_id, permission)
);

CREATE TABLE user_roles (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

CREATE TABLE sessions (
    id_hash bytea PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token text NOT NULL,
    auth_method text NOT NULL,
    ip inet,
    user_agent text,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_idx ON sessions(user_id);
CREATE INDEX sessions_expiry_idx ON sessions(expires_at);

CREATE TABLE system_settings (
    namespace text NOT NULL,
    key text NOT NULL,
    value jsonb NOT NULL DEFAULT 'null'::jsonb,
    value_type text NOT NULL DEFAULT 'json',
    secret_yn boolean NOT NULL DEFAULT false,
    restart_required boolean NOT NULL DEFAULT false,
    updated_by uuid REFERENCES users(id),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version integer NOT NULL DEFAULT 1,
    PRIMARY KEY (namespace, key)
);

CREATE TABLE oidc_providers (
    id uuid PRIMARY KEY,
    name text NOT NULL DEFAULT 'Keycloak',
    enabled boolean NOT NULL DEFAULT false,
    issuer_url text NOT NULL,
    client_id text NOT NULL,
    client_secret_encrypted text NOT NULL,
    scopes text[] NOT NULL DEFAULT ARRAY['openid','profile','email'],
    username_claim text NOT NULL DEFAULT 'preferred_username',
    email_claim text NOT NULL DEFAULT 'email',
    name_claim text NOT NULL DEFAULT 'name',
    group_claim text NOT NULL DEFAULT 'groups',
    role_claim text NOT NULL DEFAULT 'realm_access.roles',
    auto_provision boolean NOT NULL DEFAULT false,
    default_role_id uuid REFERENCES roles(id),
    root_ca_pem text,
    discovery jsonb,
    last_tested_at timestamptz,
    last_test_result jsonb,
    updated_by uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oidc_role_mappings (
    id uuid PRIMARY KEY,
    provider_id uuid NOT NULL REFERENCES oidc_providers(id) ON DELETE CASCADE,
    external_role text NOT NULL,
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    UNIQUE(provider_id, external_role)
);

CREATE TABLE oidc_group_mappings (
    id uuid PRIMARY KEY,
    provider_id uuid NOT NULL REFERENCES oidc_providers(id) ON DELETE CASCADE,
    external_group text NOT NULL,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    UNIQUE(provider_id, external_group)
);

CREATE TABLE oidc_login_states (
    state_hash bytea PRIMARY KEY,
    provider_id uuid NOT NULL REFERENCES oidc_providers(id) ON DELETE CASCADE,
    nonce text NOT NULL,
    code_verifier text NOT NULL,
    redirect_uri text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE pipelines (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE pipeline_stages (
    id uuid PRIMARY KEY,
    pipeline_id uuid NOT NULL REFERENCES pipelines(id) ON DELETE CASCADE,
    name text NOT NULL,
    stage_order integer NOT NULL,
    probability numeric(5,2) NOT NULL DEFAULT 0 CHECK (probability >= 0 AND probability <= 100),
    forecast_category text NOT NULL DEFAULT 'PIPELINE',
    is_won boolean NOT NULL DEFAULT false,
    is_lost boolean NOT NULL DEFAULT false,
    active boolean NOT NULL DEFAULT true,
    color text NOT NULL DEFAULT '#64748b',
    min_days integer,
    max_days integer,
    UNIQUE(pipeline_id, stage_order)
);

CREATE TABLE customers (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    registration_no text,
    customer_type text NOT NULL DEFAULT 'PROSPECT',
    grade text,
    industry text,
    website text,
    phone text,
    email text,
    address text,
    owner_id uuid NOT NULL REFERENCES users(id),
    organization_id uuid REFERENCES organizations(id),
    health text NOT NULL DEFAULT 'NORMAL',
    annual_revenue numeric(20,2),
    employee_count integer,
    custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    merged_into_id uuid REFERENCES customers(id),
    active boolean NOT NULL DEFAULT true,
    version integer NOT NULL DEFAULT 1,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX customers_owner_idx ON customers(owner_id);
CREATE INDEX customers_org_idx ON customers(organization_id);
CREATE INDEX customers_name_idx ON customers(lower(name));

CREATE TABLE customer_merge_history (
    id uuid PRIMARY KEY,
    target_customer_id uuid NOT NULL REFERENCES customers(id),
    source_customer_id uuid NOT NULL REFERENCES customers(id),
    merged_by uuid NOT NULL REFERENCES users(id),
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    merged_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE contacts (
    id uuid PRIMARY KEY,
    customer_id uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    name text NOT NULL,
    title text,
    department text,
    email text,
    phone text,
    mobile text,
    decision_maker boolean NOT NULL DEFAULT false,
    primary_contact boolean NOT NULL DEFAULT false,
    owner_id uuid NOT NULL REFERENCES users(id),
    custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    version integer NOT NULL DEFAULT 1,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX contacts_customer_idx ON contacts(customer_id);

CREATE TABLE leads (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    company text,
    email text,
    phone text,
    source text,
    status text NOT NULL DEFAULT 'NEW',
    owner_id uuid NOT NULL REFERENCES users(id),
    organization_id uuid REFERENCES organizations(id),
    converted_customer_id uuid REFERENCES customers(id),
    custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    version integer NOT NULL DEFAULT 1,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE opportunities (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    customer_id uuid NOT NULL REFERENCES customers(id),
    owner_id uuid NOT NULL REFERENCES users(id),
    organization_id uuid REFERENCES organizations(id),
    pipeline_id uuid NOT NULL REFERENCES pipelines(id),
    stage_id uuid NOT NULL REFERENCES pipeline_stages(id),
    expected_amount numeric(20,2) NOT NULL DEFAULT 0,
    probability numeric(5,2) NOT NULL DEFAULT 0,
    weighted_amount numeric(20,2) GENERATED ALWAYS AS (expected_amount * probability / 100) STORED,
    expected_close_date date,
    forecast_category text NOT NULL DEFAULT 'PIPELINE',
    competitor text,
    next_action text,
    next_action_date date,
    status text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN','WON','LOST')),
    lost_reason text,
    win_reason text,
    stage_entered_at timestamptz NOT NULL DEFAULT now(),
    last_activity_at timestamptz,
    custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    version integer NOT NULL DEFAULT 1,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX opportunities_owner_idx ON opportunities(owner_id);
CREATE INDEX opportunities_customer_idx ON opportunities(customer_id);
CREATE INDEX opportunities_stage_idx ON opportunities(stage_id);
CREATE INDEX opportunities_close_idx ON opportunities(expected_close_date);

CREATE TABLE opportunity_history (
    id uuid PRIMARY KEY,
    opportunity_id uuid NOT NULL REFERENCES opportunities(id) ON DELETE CASCADE,
    change_type text NOT NULL,
    before_data jsonb,
    after_data jsonb,
    changed_by uuid NOT NULL REFERENCES users(id),
    changed_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE products (
    id uuid PRIMARY KEY,
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    description text,
    unit_price numeric(20,2) NOT NULL DEFAULT 0,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE opportunity_products (
    opportunity_id uuid NOT NULL REFERENCES opportunities(id) ON DELETE CASCADE,
    product_id uuid NOT NULL REFERENCES products(id),
    quantity numeric(14,3) NOT NULL DEFAULT 1,
    unit_price numeric(20,2) NOT NULL,
    discount_percent numeric(5,2) NOT NULL DEFAULT 0,
    PRIMARY KEY(opportunity_id, product_id)
);

CREATE TABLE activities (
    id uuid PRIMARY KEY,
    customer_id uuid REFERENCES customers(id),
    opportunity_id uuid REFERENCES opportunities(id),
    activity_type text NOT NULL,
    subject text NOT NULL,
    description text,
    occurred_at timestamptz NOT NULL,
    next_action text,
    next_action_date date,
    owner_id uuid NOT NULL REFERENCES users(id),
    organization_id uuid REFERENCES organizations(id),
    version integer NOT NULL DEFAULT 1,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX activities_customer_idx ON activities(customer_id, occurred_at DESC);
CREATE INDEX activities_opportunity_idx ON activities(opportunity_id, occurred_at DESC);

CREATE TABLE tasks (
    id uuid PRIMARY KEY,
    customer_id uuid REFERENCES customers(id),
    opportunity_id uuid REFERENCES opportunities(id),
    title text NOT NULL,
    due_at timestamptz,
    status text NOT NULL DEFAULT 'OPEN',
    priority text NOT NULL DEFAULT 'NORMAL',
    assignee_id uuid NOT NULL REFERENCES users(id),
    organization_id uuid REFERENCES organizations(id),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE quotations (
    id uuid PRIMARY KEY,
    quotation_no text NOT NULL UNIQUE,
    customer_id uuid NOT NULL REFERENCES customers(id),
    opportunity_id uuid REFERENCES opportunities(id),
    owner_id uuid NOT NULL REFERENCES users(id),
    organization_id uuid REFERENCES organizations(id),
    title text NOT NULL,
    amount numeric(20,2) NOT NULL DEFAULT 0,
    discount_percent numeric(5,2) NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'DRAFT',
    valid_until date,
    version integer NOT NULL DEFAULT 1,
    custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE quotation_versions (
    id uuid PRIMARY KEY,
    quotation_id uuid NOT NULL REFERENCES quotations(id) ON DELETE CASCADE,
    version_no integer NOT NULL,
    snapshot jsonb NOT NULL,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(quotation_id, version_no)
);

CREATE TABLE contracts (
    id uuid PRIMARY KEY,
    contract_no text NOT NULL UNIQUE,
    customer_id uuid NOT NULL REFERENCES customers(id),
    opportunity_id uuid REFERENCES opportunities(id),
    owner_id uuid NOT NULL REFERENCES users(id),
    organization_id uuid REFERENCES organizations(id),
    title text NOT NULL,
    amount numeric(20,2) NOT NULL DEFAULT 0,
    start_date date,
    end_date date,
    status text NOT NULL DEFAULT 'DRAFT',
    auto_renew boolean NOT NULL DEFAULT false,
    custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    version integer NOT NULL DEFAULT 1,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX contracts_expiry_idx ON contracts(end_date);

CREATE TABLE sales (
    id uuid PRIMARY KEY,
    customer_id uuid NOT NULL REFERENCES customers(id),
    contract_id uuid REFERENCES contracts(id),
    owner_id uuid NOT NULL REFERENCES users(id),
    organization_id uuid REFERENCES organizations(id),
    amount numeric(20,2) NOT NULL,
    recognized_date date NOT NULL,
    description text,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE targets (
    id uuid PRIMARY KEY,
    user_id uuid REFERENCES users(id),
    organization_id uuid REFERENCES organizations(id),
    period_start date NOT NULL,
    period_end date NOT NULL,
    amount numeric(20,2) NOT NULL,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(user_id, organization_id, period_start, period_end)
);

CREATE TABLE forecast_snapshots (
    id uuid PRIMARY KEY,
    snapshot_date date NOT NULL,
    owner_id uuid REFERENCES users(id),
    organization_id uuid REFERENCES organizations(id),
    metrics jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE approval_policies (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    entity_type text NOT NULL,
    condition_field text,
    condition_operator text,
    condition_value jsonb,
    approver_method text NOT NULL DEFAULT 'MANAGER',
    approver_role_id uuid REFERENCES roles(id),
    approver_org_id uuid REFERENCES organizations(id),
    approval_steps integer NOT NULL DEFAULT 1,
    allow_reject boolean NOT NULL DEFAULT true,
    allow_resubmit boolean NOT NULL DEFAULT true,
    allow_delegate boolean NOT NULL DEFAULT false,
    active boolean NOT NULL DEFAULT true,
    priority integer NOT NULL DEFAULT 100,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE approval_requests (
    id uuid PRIMARY KEY,
    policy_id uuid NOT NULL REFERENCES approval_policies(id),
    entity_type text NOT NULL,
    entity_id uuid NOT NULL,
    requester_id uuid NOT NULL REFERENCES users(id),
    approver_id uuid REFERENCES users(id),
    status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','APPROVED','REJECTED','CANCELLED')),
    current_step integer NOT NULL DEFAULT 1,
    snapshot jsonb NOT NULL,
    reason text,
    requested_at timestamptz NOT NULL DEFAULT now(),
    decided_at timestamptz,
    version integer NOT NULL DEFAULT 1
);
CREATE INDEX approval_pending_idx ON approval_requests(approver_id, status);

CREATE TABLE approval_steps (
    id uuid PRIMARY KEY,
    request_id uuid NOT NULL REFERENCES approval_requests(id) ON DELETE CASCADE,
    step_no integer NOT NULL,
    approver_id uuid REFERENCES users(id),
    status text NOT NULL DEFAULT 'PENDING',
    comment text,
    decided_at timestamptz,
    UNIQUE(request_id, step_no)
);

CREATE TABLE approval_history (
    id uuid PRIMARY KEY,
    request_id uuid NOT NULL REFERENCES approval_requests(id) ON DELETE CASCADE,
    action text NOT NULL,
    actor_id uuid NOT NULL REFERENCES users(id),
    comment text,
    occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE personal_keys (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key_name text NOT NULL,
    key_id text NOT NULL UNIQUE,
    secret_digest bytea NOT NULL,
    scopes text[] NOT NULL,
    channels text[] NOT NULL,
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','ROTATING','REVOKED','EXPIRED')),
    expires_at timestamptz,
    rotation_parent_id uuid REFERENCES personal_keys(id),
    grace_expires_at timestamptz,
    last_used_at timestamptz,
    last_used_ip inet,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);
CREATE INDEX personal_keys_user_idx ON personal_keys(user_id);
CREATE INDEX personal_keys_status_idx ON personal_keys(status, expires_at);

CREATE TABLE personal_key_history (
    id uuid PRIMARY KEY,
    key_id uuid NOT NULL REFERENCES personal_keys(id) ON DELETE CASCADE,
    action text NOT NULL,
    actor_id uuid NOT NULL REFERENCES users(id),
    details jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE custom_field_definitions (
    id uuid PRIMARY KEY,
    entity_type text NOT NULL,
    field_key text NOT NULL,
    label text NOT NULL,
    field_type text NOT NULL,
    required boolean NOT NULL DEFAULT false,
    options jsonb,
    active boolean NOT NULL DEFAULT true,
    display_order integer NOT NULL DEFAULT 100,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(entity_type, field_key)
);

CREATE TABLE attachments (
    id uuid PRIMARY KEY,
    entity_type text NOT NULL,
    entity_id uuid NOT NULL,
    file_name text NOT NULL,
    storage_path text NOT NULL,
    content_type text NOT NULL,
    size_bytes bigint NOT NULL,
    sha256 text NOT NULL,
    uploaded_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE notifications (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notification_type text NOT NULL,
    title text NOT NULL,
    body text,
    resource_type text,
    resource_id uuid,
    read_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_logs (
    id uuid PRIMARY KEY,
    actor_id uuid REFERENCES users(id),
    actor_name text,
    channel text NOT NULL,
    action text NOT NULL,
    resource text NOT NULL,
    resource_id text,
    before_data jsonb,
    after_data jsonb,
    ip inet,
    request_id text,
    user_agent text,
    metadata jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_occurred_idx ON audit_logs(occurred_at DESC);
CREATE INDEX audit_resource_idx ON audit_logs(resource, resource_id);

CREATE TABLE api_request_logs (
    id uuid PRIMARY KEY,
    actor_id uuid REFERENCES users(id),
    key_id uuid REFERENCES personal_keys(id),
    method text NOT NULL,
    path text NOT NULL,
    status integer NOT NULL,
    duration_ms integer NOT NULL,
    request_id text NOT NULL,
    ip inet,
    occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mcp_request_logs (
    id uuid PRIMARY KEY,
    actor_id uuid REFERENCES users(id),
    key_id uuid REFERENCES personal_keys(id),
    method text NOT NULL,
    tool_name text,
    success boolean NOT NULL,
    duration_ms integer NOT NULL,
    request_id text NOT NULL,
    ip inet,
    occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE idempotency_keys (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key text NOT NULL,
    request_hash text NOT NULL,
    status_code integer NOT NULL,
    response_body jsonb NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(user_id, key)
);

CREATE TABLE jobs (
    id uuid PRIMARY KEY,
    job_type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'READY',
    run_at timestamptz NOT NULL DEFAULT now(),
    attempts integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 5,
    locked_at timestamptz,
    locked_by text,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE job_executions (
    id uuid PRIMARY KEY,
    job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    status text NOT NULL,
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    error text
);

INSERT INTO system_settings(namespace, key, value, value_type) VALUES
('system','service_name','"Relio"','string'),
('system','service_url','"http://localhost:8080"','string'),
('system','locale','"ko-KR"','string'),
('system','timezone','"Asia/Seoul"','string'),
('auth','local_login_enabled','true','boolean'),
('security','session_minutes','480','number'),
('security','allowed_origins','[]','json'),
('security','export_enabled','true','boolean'),
('files','max_upload_mb','20','number'),
('api','enabled','true','boolean'),
('api','rate_limit_per_minute','120','number'),
('mcp','enabled','true','boolean'),
('mcp','allowed_origins','[]','json'),
('mcp','tool_allowlist','[]','json'),
('mcp','rate_limit_per_minute','60','number'),
('keys','max_per_user','10','number'),
('keys','default_lifetime_days','365','number'),
('keys','max_lifetime_days','730','number'),
('keys','rotation_grace_hours','24','number'),
('keys','api_enabled','true','boolean'),
('keys','mcp_enabled','true','boolean');
