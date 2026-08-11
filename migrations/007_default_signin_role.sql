-- A user provisioned by Keycloak SSO used to arrive with no Role at all, so
-- every CRM screen answered HTTP 403 even though the login itself succeeded.
-- One Role is now marked as the default for new sign-ins and Relio ships two
-- ready-to-use Roles so SSO works before an administrator configures mappings.
ALTER TABLE roles
    ADD COLUMN is_default boolean NOT NULL DEFAULT false;

CREATE UNIQUE INDEX roles_single_default_idx ON roles(is_default) WHERE is_default;

INSERT INTO roles (id, code, name, description, data_scope, system_role, is_default)
VALUES
    ('9f1d6c2a-7b34-4c58-9e21-51a0c7d4e601', 'SALES_USER', '영업 담당자', 'SSO로 처음 로그인한 사용자에게 부여되는 기본 Role입니다. 본인이 담당하는 데이터만 조회합니다.', 'USER', false, true),
    ('9f1d6c2a-7b34-4c58-9e21-51a0c7d4e602', 'SALES_MANAGER', '영업 팀장', '팀 데이터 조회와 팀장 검토, Forecast Override를 수행합니다.', 'TEAM', false, false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission)
SELECT r.id, permission
FROM roles r
CROSS JOIN (VALUES
    ('customer:read'), ('customer:write'),
    ('contact:read'), ('contact:write'),
    ('lead:read'), ('lead:write'),
    ('opportunity:read'), ('opportunity:write'),
    ('activity:read'), ('activity:write'),
    ('product:read'),
    ('quotation:read'), ('quotation:write'),
    ('contract:read'),
    ('sales:read'),
    ('target:read'),
    ('forecast:read'),
    ('notification:read'), ('notification:write'),
    ('report:read'),
    ('approval:request'),
    ('mcp:use')
) AS granted(permission)
WHERE r.code IN ('SALES_USER', 'SALES_MANAGER')
ON CONFLICT DO NOTHING;

-- A team lead additionally reviews requests and owns the committed number.
INSERT INTO role_permissions (role_id, permission)
SELECT r.id, permission
FROM roles r
CROSS JOIN (VALUES
    ('approval:approve'),
    ('forecast:write'),
    ('contract:write'),
    ('sales:write'),
    ('target:write'),
    ('product:write')
) AS granted(permission)
WHERE r.code = 'SALES_MANAGER'
ON CONFLICT DO NOTHING;

-- An installation that already defined its own SALES_USER Role keeps it, so the
-- seed above is skipped and no Role carries the default flag yet. Promote that
-- existing Role instead of leaving SSO sign-ins without one.
UPDATE roles
SET is_default = true
WHERE code = 'SALES_USER'
  AND NOT EXISTS (SELECT 1 FROM roles WHERE is_default);

-- Existing SSO users that were provisioned before this migration have no Role
-- and therefore cannot use Relio at all. Attach the default Role so they are
-- usable immediately after the upgrade instead of after a support call.
INSERT INTO user_roles (user_id, role_id)
SELECT u.id, d.id
FROM users u
CROSS JOIN (SELECT id FROM roles WHERE is_default LIMIT 1) AS d
WHERE u.auth_source = 'OIDC'
  AND u.active
  AND NOT EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = u.id)
ON CONFLICT DO NOTHING;
