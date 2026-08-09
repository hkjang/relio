ALTER TABLE contacts
    ADD COLUMN IF NOT EXISTS relationship_role text NOT NULL DEFAULT 'USER',
    ADD COLUMN IF NOT EXISTS influence text NOT NULL DEFAULT 'MEDIUM',
    ADD COLUMN IF NOT EXISTS sentiment text NOT NULL DEFAULT 'NEUTRAL',
    ADD COLUMN IF NOT EXISTS relationship_strength integer NOT NULL DEFAULT 50,
    ADD COLUMN IF NOT EXISTS decision_power integer NOT NULL DEFAULT 50,
    ADD COLUMN IF NOT EXISTS last_contact_at timestamptz;

CREATE TABLE stage_exit_criteria (
    id uuid PRIMARY KEY,
    stage_id uuid NOT NULL REFERENCES pipeline_stages(id) ON DELETE CASCADE,
    name text NOT NULL,
    criterion_type text NOT NULL CHECK (criterion_type IN ('FIELD_PRESENT','DECISION_MAKER','RECENT_ACTIVITY','PLAYBOOK_COMPLETE','CUSTOM_FIELD')),
    field_key text,
    operator text NOT NULL DEFAULT 'PRESENT',
    expected_value jsonb,
    enforcement text NOT NULL DEFAULT 'WARNING' CHECK (enforcement IN ('OFF','WARNING','BLOCK')),
    message text,
    active boolean NOT NULL DEFAULT true,
    display_order integer NOT NULL DEFAULT 100,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX stage_exit_criteria_stage_idx ON stage_exit_criteria(stage_id, active, display_order);

CREATE TABLE sales_playbooks (
    id uuid PRIMARY KEY,
    stage_id uuid NOT NULL UNIQUE REFERENCES pipeline_stages(id) ON DELETE CASCADE,
    name text NOT NULL,
    guidance text,
    active boolean NOT NULL DEFAULT true,
    created_by uuid NOT NULL REFERENCES users(id),
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sales_playbook_items (
    id uuid PRIMARY KEY,
    playbook_id uuid NOT NULL REFERENCES sales_playbooks(id) ON DELETE CASCADE,
    title text NOT NULL,
    description text,
    item_type text NOT NULL DEFAULT 'CHECKLIST' CHECK (item_type IN ('CHECKLIST','ACTION','FIELD')),
    field_key text,
    required boolean NOT NULL DEFAULT false,
    display_order integer NOT NULL DEFAULT 100,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sales_playbook_items_playbook_idx ON sales_playbook_items(playbook_id, display_order);

CREATE TABLE opportunity_playbook_progress (
    opportunity_id uuid NOT NULL REFERENCES opportunities(id) ON DELETE CASCADE,
    item_id uuid NOT NULL REFERENCES sales_playbook_items(id) ON DELETE CASCADE,
    completed boolean NOT NULL DEFAULT false,
    notes text,
    completed_by uuid REFERENCES users(id),
    completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(opportunity_id, item_id)
);

CREATE TABLE deal_health_rules (
    id uuid PRIMARY KEY,
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    description text,
    rule_type text NOT NULL,
    threshold jsonb NOT NULL DEFAULT '{}'::jsonb,
    risk_score integer NOT NULL CHECK (risk_score >= 0 AND risk_score <= 100),
    recommended_action text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    priority integer NOT NULL DEFAULT 100,
    version integer NOT NULL DEFAULT 1,
    created_by uuid REFERENCES users(id),
    updated_by uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE opportunity_health_snapshots (
    id uuid PRIMARY KEY,
    opportunity_id uuid NOT NULL REFERENCES opportunities(id) ON DELETE CASCADE,
    risk_score integer NOT NULL,
    health_score integer NOT NULL,
    risk_level text NOT NULL,
    factors jsonb NOT NULL,
    recommendations jsonb NOT NULL,
    calculated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX opportunity_health_snapshot_idx ON opportunity_health_snapshots(opportunity_id, calculated_at DESC);

CREATE UNIQUE INDEX forecast_snapshots_owner_day_uniq
    ON forecast_snapshots(snapshot_date, owner_id)
    WHERE owner_id IS NOT NULL;

CREATE TABLE forecast_snapshot_items (
    snapshot_id uuid NOT NULL REFERENCES forecast_snapshots(id) ON DELETE CASCADE,
    opportunity_id uuid NOT NULL REFERENCES opportunities(id) ON DELETE CASCADE,
    owner_id uuid NOT NULL REFERENCES users(id),
    organization_id uuid REFERENCES organizations(id),
    forecast_category text NOT NULL,
    status text NOT NULL,
    stage_id uuid NOT NULL REFERENCES pipeline_stages(id),
    stage_name text NOT NULL,
    expected_amount numeric(20,2) NOT NULL,
    weighted_amount numeric(20,2) NOT NULL,
    probability numeric(5,2) NOT NULL,
    expected_close_date date,
    PRIMARY KEY(snapshot_id, opportunity_id)
);
CREATE INDEX forecast_snapshot_items_owner_idx ON forecast_snapshot_items(owner_id, snapshot_id);

CREATE TABLE forecast_overrides (
    id uuid PRIMARY KEY,
    opportunity_id uuid NOT NULL REFERENCES opportunities(id) ON DELETE CASCADE,
    manager_id uuid NOT NULL REFERENCES users(id),
    forecast_category text,
    probability numeric(5,2) CHECK (probability >= 0 AND probability <= 100),
    amount numeric(20,2),
    reason text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    version integer NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(opportunity_id, manager_id)
);

INSERT INTO deal_health_rules(id,code,name,description,rule_type,threshold,risk_score,recommended_action,priority) VALUES
('11111111-1111-4111-8111-111111111101','NO_RECENT_ACTIVITY','장기 미접촉','최근 고객 접촉이 없습니다.','NO_ACTIVITY','{"days":14}',25,'고객 Follow-up 미팅 또는 통화를 등록하세요.',10),
('11111111-1111-4111-8111-111111111102','CLOSE_DATE_OVERDUE','계약 예정일 경과','예상 계약일이 지났습니다.','CLOSE_DATE_PASSED','{}',30,'계약 가능성을 재확인하고 예상 계약일을 갱신하세요.',20),
('11111111-1111-4111-8111-111111111103','NO_NEXT_ACTION','다음 행동 미지정','구체적인 후속 행동이 없습니다.','NO_NEXT_ACTION','{}',20,'다음 행동과 실행 일자를 등록하세요.',30),
('11111111-1111-4111-8111-111111111104','STAGE_STALLED','Stage 장기 정체','권장 체류기간을 초과했습니다.','STAGE_STALLED','{"defaultDays":30}',15,'Stage 진입 조건과 장애요인을 팀장과 점검하세요.',40),
('11111111-1111-4111-8111-111111111105','CLOSE_DATE_SLIPPAGE','계약일 반복 연기','예상 계약일이 반복해서 연기됐습니다.','CLOSE_DATE_SLIPPAGE','{"count":3,"days":180}',25,'의사결정 일정과 구매 프로세스를 다시 확인하세요.',50),
('11111111-1111-4111-8111-111111111106','AMOUNT_DROP','금액 급감','예상 금액이 이전 값보다 크게 감소했습니다.','AMOUNT_DROP','{"percent":30,"days":90}',20,'범위 축소 원인과 경쟁 상황을 확인하세요.',60),
('11111111-1111-4111-8111-111111111107','PROBABILITY_DROP','성공확률 급락','성공확률이 크게 하락했습니다.','PROBABILITY_DROP','{"points":20,"days":90}',20,'확률 하락 근거와 복구 계획을 기록하세요.',70),
('11111111-1111-4111-8111-111111111108','NO_DECISION_MAKER','Decision Maker 미확인','고객 의사결정자가 확인되지 않았습니다.','NO_DECISION_MAKER','{}',20,'Decision Maker를 식별하고 관계 활동을 계획하세요.',80),
('11111111-1111-4111-8111-111111111109','NO_CHAMPION','Champion 미확인','고객 내부 협력자가 확인되지 않았습니다.','NO_CHAMPION','{}',15,'고객 내부 Champion 후보를 발굴하세요.',90)
ON CONFLICT(code) DO NOTHING;

INSERT INTO system_settings(namespace,key,value,value_type) VALUES
('sales_intelligence','snapshot_enabled','true','boolean'),
('sales_intelligence','risk_threshold','40','number'),
('sales_intelligence','inspection_days','7','number')
ON CONFLICT(namespace,key) DO NOTHING;

INSERT INTO role_permissions(role_id,permission)
SELECT id,'forecast:write' FROM roles WHERE code='SYSTEM_ADMIN'
ON CONFLICT DO NOTHING;
