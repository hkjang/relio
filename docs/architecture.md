# Relio Architecture

## Runtime

필수 구성요소는 Relio Container, PostgreSQL, `relio-data` Volume입니다. Keycloak은 선택 사항이며 장애가 Relio Readiness를 실패시키지 않습니다.

```text
Web / REST / MCP
        ↓
Authentication + Permission + Data Scope
        ↓
Application / CRM Domain Service
        ↓
PostgreSQL Repository
```

React 빌드 결과는 `internal/webui/dist`에서 Go Binary에 embed됩니다. Runtime Image에는 Node.js나 Package Manager가 없습니다. Go Binary는 DB Migration, HTTP API, MCP Adapter, Scheduler, 정적 자산과 Healthcheck를 함께 제공합니다.

## Startup

```text
3개 환경변수 검증
→ PostgreSQL 연결 재시도
→ Advisory Migration Lock
→ Transaction Migration
→ Instance Master Key Load/Create
→ Bootstrap Admin 존재 여부 확인
→ Scheduler / HTTP Start
```

Migration이 실패하면 HTTP Server를 시작하지 않습니다. `schema_migrations`와 PostgreSQL Advisory Lock으로 다중 Instance 시작을 보호합니다.

## Data Scope

데이터 Entity의 `owner_id`, `organization_id`를 쿼리 조건에서 검사합니다.

| Scope | 조건 |
|---|---|
| USER | Owner가 본인 |
| TEAM | 본인 또는 `manager_id`가 본인인 사용자 |
| DEPARTMENT | 사용자의 Department 조직과 모든 하위 조직 |
| DIVISION | 사용자의 Division 조직과 모든 하위 조직 |
| COMPANY | 전사 |

필터는 결과 반환 후가 아니라 PostgreSQL Query에 적용합니다. Customer 360의 하위 데이터와 Dashboard 집계도 같은 범위를 사용합니다.

## Configuration

공통 설정은 Namespace/Key/Type/Version을 가진 `system_settings`에 저장합니다. OIDC, Role/Group Mapping, Pipeline, Approval, Custom Field는 별도 Domain 테이블을 사용합니다. Secret 설정은 `/var/lib/relio/secrets/master.key`로 암호화합니다.

## Approval

`approval_policies`에 활성 정책이 없으면 API의 Workflow Status가 false이고 Web/MCP가 관련 기능을 노출하지 않습니다. 정책은 Entity Snapshot에 대해 평가하고 Request/Step/History를 별도 테이블에 Append합니다. 현재 UI는 팀장 1단계이며 데이터 모델은 다단계를 수용합니다.

## Sales Intelligence

Web, REST와 MCP는 동일한 `internal/intelligence` Application Service를 사용합니다. Deal Health Engine은 PostgreSQL의 활성 Rule을 평가해 점수·Evidence·Recommended Action을 만들며 계산 결과를 제한적으로 Snapshot합니다. Stage 변경은 CRM Service가 Sales Execution Guard를 호출하므로 Adapter를 우회해도 `BLOCK` Exit Criteria를 건너뛸 수 없습니다.

Scheduler는 PostgreSQL에 Owner별 일별 Forecast Snapshot과 Opportunity 항목을 저장합니다. Forecast Waterfall은 두 Snapshot을 비교해 New Pipeline, Won/Lost, Amount Change, Slippage를 설명합니다. Manager Override는 담당자 Forecast를 덮어쓰지 않고 별도 이력과 사유로 유지합니다.

## Relationship Intelligence

`internal/relationship` Application Service는 Web, REST, MCP가 공유하는 고객 관계·Account Plan·Opportunity Team 업무 규칙을 제공합니다. `contact_relationships`는 동일 고객의 Contact 사이 방향성 관계와 강도를 저장하고, Customer 360 Relationship Graph는 Contact의 Decision Maker/Champion/Influence/Sentiment 지표와 결합해 설명 가능한 Relationship Score를 계산합니다.

`account_plans`는 고객·연도별 전략, 목표, 경쟁사, 위험, 목표/잠재 매출과 White Space를 보관합니다. `NOT_OFFERED` 또는 `DISCOVERY` White Space만 Cross-sell 후보로 제공하므로 Web과 AI Agent가 동일한 기준을 사용합니다. `opportunity_members`의 Presales/Consultant/Manager/Legal 역할은 협업 Metadata이며 Data Scope 판정에는 포함하지 않습니다. 모든 조회는 먼저 CRM Domain의 Customer/Opportunity 접근 검사를 통과합니다.

## PostgreSQL-only Operations

- Job/Scheduler: `jobs`, `job_executions`
- Migration/Leader Lock: Advisory Lock
- Session: `sessions`
- Idempotency Result: `idempotency_keys`
- Notification: `notifications`
- Audit: Append-only `audit_logs`

Redis, Elasticsearch, 외부 Queue는 기본 배포에 포함하지 않습니다.
