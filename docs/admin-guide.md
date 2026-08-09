# Administrator Guide

## 최초 구축

1. 세 환경변수와 영속 Volume을 지정해 Relio를 시작합니다.
2. Bootstrap Admin으로 로그인하고 초기 비밀번호를 변경합니다.
3. Admin Console의 시스템 설정에서 Service URL, Locale, Timezone을 저장합니다.
4. 조직, Role/Permission/Data Scope, 사용자를 구성합니다.
5. 필요할 때만 Keycloak OIDC와 승인 Workflow를 활성화합니다.

## Keycloak

1. Keycloak에서 Confidential OIDC Client를 생성합니다.
2. Relio Admin → Keycloak OIDC에 Realm Issuer URL, Client ID, Client Secret을 입력합니다.
3. 화면에 표시된 Callback URL을 Keycloak Valid Redirect URI로 등록합니다.
4. 저장 후 연결 테스트를 실행합니다.
5. Claim과 Default Role을 확인하고 Auto Provision 여부를 결정합니다.
6. Role Mapping에서 Keycloak Role을 Relio Role로, Group Mapping에서 Keycloak Group을 Relio 조직으로 연결합니다.
7. 테스트가 성공한 후 SSO를 활성화합니다.

Keycloak 장애는 `/health/ready`를 실패시키지 않습니다. Bootstrap Admin은 SSO 활성화 후에도 유지합니다.

## Approval

정책이 없을 때가 기본 상태입니다. 이때 사용자는 승인 관련 메뉴와 Status를 볼 수 없습니다. 정책을 추가할 때 Entity, 조건 Field/Operator/Value와 승인자를 지정합니다. 정책 조건은 요청 시점 Entity Snapshot에 평가됩니다.

## Sales Execution

Admin → Sales Execution에서 Pipeline Stage별 Playbook과 Exit Criteria를 설정합니다. Playbook은 Guidance, Checklist/Action/Field 항목과 필수 여부로 구성합니다. Exit Criteria는 Field 입력, Decision Maker, 최근 Activity, 필수 Playbook 완료, Custom Field를 검사하며 `OFF`, `WARNING`, `BLOCK` 중 강제 수준을 선택합니다.

`BLOCK` 조건이 충족되지 않으면 Web, REST API, MCP 어느 채널에서도 Stage를 변경할 수 없습니다. `WARNING`은 준비도 응답과 화면에 표시하지만 저장을 차단하지 않습니다. 설정과 사용자 완료 이력은 Audit에 기록됩니다.

Forecast Override는 담당자의 Forecast와 팀장의 판단을 분리합니다. 변경 사유가 필수이며 최신 Manager 판단은 Forecast Intelligence의 Manager Commit에 반영됩니다.

## Personal Key

관리자는 최대 Key 수, 기본/최대 만료일, Rotation Grace, REST/MCP 채널 허용 여부를 구성합니다. 관리자는 Key ID, Scope, 만료, 최근 IP 같은 Metadata만 확인할 수 있고 Secret은 볼 수 없습니다.

같은 화면에서 REST/MCP 활성화와 분당 요청 한도, MCP 허용 Origin 및 Tool Allowlist를 관리합니다. Allowlist가 비어 있으면 사용자에게 권한이 있는 Tool 전체를 제공하고, 값이 있으면 목록과 권한을 모두 만족하는 Tool만 제공합니다.

일반 Local Login을 비활성화해도 Bootstrap Admin 로그인은 차단되지 않습니다. 이는 SSO 장애 시 복구 경로를 보장하기 위한 의도된 Break Glass 예외입니다.

## Backup and Upgrade

- PostgreSQL과 `relio-data` Volume을 함께 Backup합니다.
- 새 Image를 `docker load`하고 기존 Volume과 같은 세 환경변수로 Container만 교체합니다.
- `/admin/overview`에서 Application Version, Schema Version, Last Migration을 확인합니다.
- Migration 실패 시 Application은 시작하지 않으므로 기존 Image와 DB Backup으로 복구합니다.
