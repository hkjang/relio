CREATE TABLE contact_relationships (
    id uuid PRIMARY KEY,
    customer_id uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    source_contact_id uuid NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    target_contact_id uuid NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    relationship_type text NOT NULL CHECK (relationship_type IN ('REPORTS_TO','INFLUENCES','WORKS_WITH','BLOCKS','TRUSTS','ADVISES','OTHER')),
    strength integer NOT NULL DEFAULT 50 CHECK (strength >= 0 AND strength <= 100),
    description text,
    active boolean NOT NULL DEFAULT true,
    version integer NOT NULL DEFAULT 1,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (source_contact_id <> target_contact_id),
    UNIQUE (source_contact_id, target_contact_id, relationship_type)
);
CREATE INDEX contact_relationships_customer_idx ON contact_relationships(customer_id, active);

CREATE TABLE account_plans (
    id uuid PRIMARY KEY,
    customer_id uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    plan_year integer NOT NULL CHECK (plan_year BETWEEN 2000 AND 2200),
    status text NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','ACTIVE','ARCHIVED')),
    strategy text,
    customer_goals jsonb NOT NULL DEFAULT '[]'::jsonb,
    strategic_initiatives jsonb NOT NULL DEFAULT '[]'::jsonb,
    our_objectives jsonb NOT NULL DEFAULT '[]'::jsonb,
    white_spaces jsonb NOT NULL DEFAULT '[]'::jsonb,
    competitors jsonb NOT NULL DEFAULT '[]'::jsonb,
    risks jsonb NOT NULL DEFAULT '[]'::jsonb,
    target_revenue numeric(20,2) NOT NULL DEFAULT 0,
    potential_revenue numeric(20,2) NOT NULL DEFAULT 0,
    owner_id uuid NOT NULL REFERENCES users(id),
    organization_id uuid REFERENCES organizations(id),
    version integer NOT NULL DEFAULT 1,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (customer_id, plan_year)
);
CREATE INDEX account_plans_owner_idx ON account_plans(owner_id, plan_year);

CREATE TABLE opportunity_members (
    opportunity_id uuid NOT NULL REFERENCES opportunities(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    member_role text NOT NULL CHECK (member_role IN ('PRESALES','CONSULTANT','MANAGER','EXECUTIVE_SPONSOR','LEGAL','DELIVERY','OTHER')),
    responsibility text,
    version integer NOT NULL DEFAULT 1,
    added_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (opportunity_id, user_id)
);
CREATE INDEX opportunity_members_user_idx ON opportunity_members(user_id, opportunity_id);

INSERT INTO system_settings(namespace,key,value,value_type) VALUES
('relationship_intelligence','graph_max_nodes','100','number'),
('relationship_intelligence','default_plan_year','0','number'),
('relationship_intelligence','allowed_opportunity_roles','["PRESALES","CONSULTANT","MANAGER","EXECUTIVE_SPONSOR","LEGAL","DELIVERY","OTHER"]','json')
ON CONFLICT(namespace,key) DO NOTHING;
