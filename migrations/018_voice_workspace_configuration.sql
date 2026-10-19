-- Department VOC configuration must preserve the department's own codes and
-- labels instead of baking example values into the product.  Select fields
-- therefore keep stable stored codes (options), display labels, and an
-- optional configured default separately.
ALTER TABLE custom_field_definitions
    ADD COLUMN option_labels jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN default_value jsonb;

-- The original key constraint pre-dated VOC workspaces and prevented two
-- departments from using the same semantic keys (service_type, error_code,
-- resolver).  General fields remain unique globally; workspace fields are
-- unique inside their own workspace.
ALTER TABLE custom_field_definitions
    DROP CONSTRAINT custom_field_definitions_entity_type_field_key_key;
CREATE UNIQUE INDEX custom_field_definitions_general_key
    ON custom_field_definitions(entity_type, field_key)
    WHERE workspace_id IS NULL;
CREATE UNIQUE INDEX custom_field_definitions_workspace_key
    ON custom_field_definitions(entity_type, workspace_id, field_key)
    WHERE workspace_id IS NOT NULL;

-- A department decides whether its member code is unique company-wide or
-- only in that workspace. Existing installations keep their previous GLOBAL
-- behaviour. New workspace setup makes this choice explicit.
ALTER TABLE voice_workspaces
    ADD COLUMN customer_code_unique_scope text NOT NULL DEFAULT 'GLOBAL'
        CHECK (customer_code_unique_scope IN ('GLOBAL', 'WORKSPACE'));

ALTER TABLE customers ADD COLUMN customer_code_scope text NOT NULL DEFAULT 'GLOBAL';
DROP INDEX customers_customer_code_key;
CREATE UNIQUE INDEX customers_customer_code_scope_key
    ON customers(customer_code_scope, customer_code)
    WHERE customer_code IS NOT NULL;
CREATE INDEX customers_workspace_code_search_idx
    ON customers(home_workspace_id, customer_code)
    WHERE active AND merged_into_id IS NULL;
