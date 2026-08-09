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

## 운영 Command Center와 Diagnostics

`관리자 → 운영 Command Center`는 필수 운영 항목을 0~100 준비도로 계산하고 즉시 조치할 항목을 우선순위로 표시합니다. PostgreSQL, Schema Migration, Instance Master Key, Persistent Volume, Service URL, 사용자·조직, RBAC, Pipeline, Background Job은 필수 항목입니다. OIDC와 승인 Workflow는 선택 기능이므로 관리자가 의도적으로 비활성화한 상태를 장애로 계산하지 않습니다.

`관리자 → Diagnostics · Job`에서 각 항목의 근거, Application/Database Version, Offline Runtime Contract와 최근 Background Job을 확인합니다. 운영 지원 자료가 필요하면 `Support Bundle`을 내려받습니다. Bundle에는 Password, Token, Personal Key, PostgreSQL DSN, OIDC Client Secret이 포함되지 않으며 다운로드 자체가 Audit에 기록됩니다.

관리자 메뉴 검색은 상단 검색창 또는 `Ctrl/Command + K`로 시작할 수 있습니다. 모바일에서는 상단 `관리 메뉴` 버튼을 눌러 모든 설정에 접근합니다.

## Audit Log

Actor, Action, Resource, Resource ID와 Request ID를 한 번에 검색하고 Channel로 필터링할 수 있습니다. 행의 `상세`를 열면 요청 Metadata와 변경 전·후 JSON을 비교할 수 있습니다. Support Bundle 다운로드는 `SUPPORT_BUNDLE_EXPORT` Action으로 남습니다.

## Data Quality Center

`관리자 → Data Quality · Config`에서 고객, 담당자, 진행 Opportunity를 8개 규칙으로 점검합니다. 사업자번호 누락, 담당자 없는 고객, 90일 이상 미접촉 고객, 중복 가능 고객, 연락수단 없는 담당자, Next Action 없는 Deal, Decision Maker 미확인, 30일 이상 정체 Deal을 전체 대상 대비 비율과 가중치로 계산해 0~100 Score로 표시합니다.

각 Rule Card를 열면 최대 8개의 문제 표본과 권장 조치를 확인하고 해당 Customer 360, Opportunity 또는 Deal Intelligence로 이동할 수 있습니다. 데이터가 없는 영역은 감점하지 않습니다. Score만 목표로 삼기보다 영업 영향이 큰 `CRITICAL` 항목과 반복 추세를 먼저 관리합니다.

## Configuration Bundle

같은 화면의 `Configuration Bundle` 탭에서 개발·검증·운영 환경 사이에 관리자 정책을 이동할 수 있습니다.

1. `현재 설정 JSON 저장`으로 Source 환경의 Bundle을 내려받습니다.
2. Target 환경에서 JSON을 선택합니다.
3. Format, 2MB 크기 제한, 중복 논리 Key, 금지된 Secret과 참조를 검증합니다.
4. Section별 `CREATE`, `UPDATE`, `SAME` 수와 항목별 변경 전·후 JSON을 확인합니다.
5. 확인 Checkbox를 선택한 뒤 트랜잭션으로 적용합니다.

Bundle에는 비밀값이 아닌 System Setting, Custom Role/Permission, Pipeline/Stage, Custom Field, Deal Health Rule, 승인 정책, Sales Playbook과 Exit Criteria가 포함됩니다. OIDC Client Secret, Password, Token, PostgreSQL DSN, 사용자·조직 Identity, 고객과 영업 업무 데이터는 포함되지 않습니다. Import는 항목을 삭제하지 않으며 논리 Key 기준의 생성·갱신만 수행하고 시스템 관리자 Role을 변경할 수 없습니다. Export와 Apply는 각각 `CONFIGURATION_BUNDLE_EXPORT`, `CONFIGURATION_BUNDLE_APPLY` Audit으로 기록됩니다.

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

## Relationship Intelligence

`관리자 → Relationship Intelligence`에서 Customer 360 Graph 최대 Node 수(10~200), 기본 Account Plan 연도와 Opportunity Team 허용 Role을 설정합니다. 기본 연도를 `0`으로 두면 서버의 현재 연도를 자동 사용합니다. Role 정책과 Graph 제한은 저장 즉시 적용됩니다.

Opportunity Team 구성은 협업 역할만 추가합니다. 구성원이라는 이유로 해당 Opportunity 또는 고객에 대한 USER/TEAM/DEPARTMENT/DIVISION/COMPANY Data Scope가 확대되지 않습니다.

## Backup and Upgrade

- PostgreSQL과 `relio-data` Volume을 함께 Backup합니다.
- 새 Image를 `docker load`하고 기존 Volume과 같은 세 환경변수로 Container만 교체합니다.
- `/admin/overview`에서 운영 준비도와 우선 조치를 확인하고 `/admin/operations`에서 Application Version, Schema Version, Last Migration을 확인합니다.
- Migration 실패 시 Application은 시작하지 않으므로 기존 Image와 DB Backup으로 복구합니다.
