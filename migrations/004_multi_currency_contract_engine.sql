-- v1.5: keep the original transaction currency while all enterprise roll-ups use
-- a locked conversion rate into the instance base currency (KRW by default).
ALTER TABLE opportunities
    ADD COLUMN currency_code text NOT NULL DEFAULT 'KRW' CHECK (currency_code ~ '^[A-Z]{3}$'),
    ADD COLUMN exchange_rate numeric(20,8) NOT NULL DEFAULT 1 CHECK (exchange_rate > 0),
    ADD COLUMN base_expected_amount numeric(24,2)
        GENERATED ALWAYS AS (round(expected_amount * exchange_rate, 2)) STORED,
    ADD COLUMN base_weighted_amount numeric(24,2)
        GENERATED ALWAYS AS (round(expected_amount * exchange_rate * probability / 100, 2)) STORED;

ALTER TABLE quotations
    ADD COLUMN currency_code text NOT NULL DEFAULT 'KRW' CHECK (currency_code ~ '^[A-Z]{3}$'),
    ADD COLUMN exchange_rate numeric(20,8) NOT NULL DEFAULT 1 CHECK (exchange_rate > 0),
    ADD COLUMN base_amount numeric(24,2)
        GENERATED ALWAYS AS (round(amount * exchange_rate, 2)) STORED;

ALTER TABLE contracts
    ADD COLUMN currency_code text NOT NULL DEFAULT 'KRW' CHECK (currency_code ~ '^[A-Z]{3}$'),
    ADD COLUMN exchange_rate numeric(20,8) NOT NULL DEFAULT 1 CHECK (exchange_rate > 0),
    ADD COLUMN base_amount numeric(24,2)
        GENERATED ALWAYS AS (round(amount * exchange_rate, 2)) STORED,
    ADD COLUMN revenue_schedule_type text NOT NULL DEFAULT 'ONE_TIME'
        CHECK (revenue_schedule_type IN ('ONE_TIME','MONTHLY','QUARTERLY','ANNUAL')),
    ADD COLUMN renewal_notice_days integer NOT NULL DEFAULT 90
        CHECK (renewal_notice_days BETWEEN 0 AND 730),
    ADD COLUMN renewal_status text NOT NULL DEFAULT 'NOT_STARTED'
        CHECK (renewal_status IN ('NOT_STARTED','PLANNED','IN_PROGRESS','RENEWED','CHURNED')),
    ADD COLUMN renewal_action text,
    ADD COLUMN activated_at timestamptz;

ALTER TABLE sales
    ADD COLUMN currency_code text NOT NULL DEFAULT 'KRW' CHECK (currency_code ~ '^[A-Z]{3}$'),
    ADD COLUMN exchange_rate numeric(20,8) NOT NULL DEFAULT 1 CHECK (exchange_rate > 0),
    ADD COLUMN base_amount numeric(24,2)
        GENERATED ALWAYS AS (round(amount * exchange_rate, 2)) STORED;

CREATE TABLE revenue_schedules (
    id uuid PRIMARY KEY,
    contract_id uuid NOT NULL REFERENCES contracts(id) ON DELETE CASCADE,
    sequence_no integer NOT NULL CHECK (sequence_no > 0),
    scheduled_date date NOT NULL,
    amount numeric(20,2) NOT NULL CHECK (amount >= 0),
    currency_code text NOT NULL CHECK (currency_code ~ '^[A-Z]{3}$'),
    exchange_rate numeric(20,8) NOT NULL CHECK (exchange_rate > 0),
    base_amount numeric(24,2)
        GENERATED ALWAYS AS (round(amount * exchange_rate, 2)) STORED,
    status text NOT NULL DEFAULT 'PLANNED'
        CHECK (status IN ('PLANNED','RECOGNIZED','CANCELLED')),
    recognized_sale_id uuid REFERENCES sales(id),
    recognized_at timestamptz,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (contract_id, sequence_no)
);
CREATE INDEX revenue_schedules_contract_date_idx
    ON revenue_schedules(contract_id, scheduled_date);
CREATE INDEX revenue_schedules_due_idx
    ON revenue_schedules(status, scheduled_date);

INSERT INTO system_settings(namespace,key,value,value_type) VALUES
('sales_finance','base_currency','"KRW"','string'),
('sales_finance','renewal_radar_days','90','number')
ON CONFLICT(namespace,key) DO NOTHING;
