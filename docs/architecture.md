# Relio 시스템 아키텍처 명세서 (Architecture Specification)

- **문서 버전**: v1.6.0  
- **최종 수정일**: 2026년 8월 11일  
- **대상**: 시스템 아키텍트, Lead Developer, DevOps/SRE 엔지니어, Security Auditor  
- **문서 목적**: Relio 사내 에어갭 B2B CRM & 영업관리 플랫폼의 전체 소프트웨어 구조, 도메인 모델, 데이터 흐름, 보안 레이어 및 MCP 아키텍처 명세  

---

## 1. 개요 및 설계 철학 (Architecture Overview)

Relio는 **"사람을 위한 CRM, 시스템을 위한 API, AI Agent를 위한 MCP"**를 기치로 내건 단일 컨테이너 기반 B2B 영업관리 및 CRM 플랫폼입니다. 외부 인터넷 연결이 완전히 차단된 에어갭(Air-gapped) 사내 망 환경에서 추가적인 런타임/미들웨어(Redis, RabbitMQ 등) 없이 **Go 단일 바이너리와 PostgreSQL**만으로 완전한 엔터프라이즈 운영을 지원합니다.

```
+-----------------------------------------------------------------------------------+
|                                 Client Applications                               |
|   +--------------------------+  +-------------------------+  +------------------+   |
|   |  Browser SPA (/app,/admin)|  |  External REST Clients  |  |  AI Agent (MCP)  |   |
|   +------------+-------------+  +------------+------------+  +--------+---------+   |
+----------------|-----------------------------|------------------------|-----------+
                 |                             |                        |
                 v                             v                        v
+-----------------------------------------------------------------------------------+
|                                Relio Single Container                             |
|  +-----------------------------------------------------------------------------+  |
|  |                             HTTP / Web Framework                            |  |
|  |   - SPA Static Asset Embed (webui/assets.go)                                |  |
|  |   - Auth & Session / Bearer Token Inspection                                |  |
|  |   - MCP Streamable HTTP Adapter (/mcp)                                      |  |
|  +-------------------------------------+---------------------------------------+  |
|                                        |                                          |
|  +-------------------------------------v---------------------------------------+  |
|  |                       Core Business Domain Services                         |  |
|  |  +------------------+  +-------------------+  +--------------------------+  |  |
|  |  |  CRM Service     |  |  Intelligence     |  |  Relationship Service    |  |  |
|  |  |  (360, Pipeline) |  |  (Health,Coaching)|  |  (Map, Account Plan)     |  |  |
|  +--+------------------+--+-------------------+--+--------------------------+--+  |
|  |  |  Approval Engine |  |  Admin Diagnostics|  |  API Key & Security      |  |  |
|  |  |  (Workflow, Policy) | (Center, Config)  |  |  (HMAC, Envelope Crypto) |  |  |
|  |  +------------------+  +-------------------+  +--------------------------+  |  |
|  +-------------------------------------+---------------------------------------+  |
|                                        |                                          |
|  +-------------------------------------v---------------------------------------+  |
|  |                     Data Access & Persistence Layer                         |  |
|  |   - Master Key / Envelope Key Encryption Manager                            |  |
|  |   - Database Migrations Engine (embed SQL)                                  |  |
|  |   - Audit Log & Background Job Runner                                       |  |
|  +-------------------------------------+---------------------------------------+  |
+----------------------------------------|------------------------------------------+
                                         v
+-----------------------------------------------------------------------------------+
|                               PostgreSQL Database                                 |
|   - Multi-tenant Domain Tables, Audit Logs, Security Tokens, Master Key Digest    |
+-----------------------------------------------------------------------------------+
```

### 1.1 핵심 아키텍처 원칙
1. **Single Binary & Zero External Runtime**:
   Go 1.24+ 내장 HTTP 서버와 React 19 기반 빌드 정적 자산을 `embed.FS`로 단일 바이너리에 패키징합니다. PostgreSQL 14+ 단일 DB만을 요구합니다.
2. **Minimal & Immutable Environment Configuration**:
   필수 3개(`POSTGRES_DSN`, `BOOTSTRAP_ADMIN`, `BOOTSTRAP_ADMIN_PASSWORD`)와 선택 1개(`ENCRYPTION_KEY`) 환경변수만을 허용합니다. 도메인 및 런타임 설정은 DB `system_settings` 테이블에서 관리되며 어플리케이션 재시작 없이 즉시 반영됩니다.
3. **Envelope Encryption & Key Persistence**:
   DB 내 저장되는 비밀값(SSO Secret 등)은 AES-256-GCM Envelope Encryption으로 암호화됩니다. `ENCRYPTION_KEY`를 지정할 경우 Volume 재발생 시에도 Key continuous를 완벽히 유지합니다.
4. **Air-Gap Strict Compliance**:
   런타임 CDN, 외부 Google Font 다운로드, Telemetry, License Validation 등 외부 통신 시도를 0%로 통제합니다.

---

## 2. 계층별 아키텍처 (Layered Architecture)

### 2.1 Web UI Layer (`web/src` & `internal/webui`)
- **React SPA Engine**: React 19, TypeScript, Vite로 구성된 프론트엔드로, `/app`(CRM 메인), `/me`(개인화 설정), `/admin`(관리자 콘솔)의 독립적인 라우팅 영역으로 나뉩니다.
- **Embedded Asset Serving**: `internal/webui/assets.go`에서 `embed.FS`를 통해 빌드된 정적 HTML/JS/CSS 자산을 서빙하며 gzip 압축 및 캐시 제어를 지원합니다.

### 2.2 Server & Route Handling (`internal/server`)
- **HTTP Engine**: Go `net/http` 기반 서버로 `httpx` 패키지를 사용해 컨텍스트 기반 Logging, Request ID 추적, CORS, IP 파싱을 수행합니다.
- **REST Endpoints (`/api/v1/*`)**:
  - `/api/v1/auth/*`: Session 로그인/로그아웃, Keycloak OIDC Callback
  - `/api/v1/crm/*`: Customer 360, Opportunity, Stage Playbook, Pipeline, Dynamic Currency, Revenue Schedule
  - `/api/v1/intelligence/*`: Deal Health Score, Risk Explainer, Exit Criteria 준비도 검사, Manager Coaching
  - `/api/v1/relationship/*`: Relationship Map, Strategic Account Plan, Opportunity Team Members
  - `/api/v1/admin/*`: Operations Command Center, Data Quality Center, Configuration Bundle Diff, Support Bundle Export
  - `/api/v1/me/*`: Personal API Key 생성/회전, Personal Dashboard, Audit Log

### 2.3 Business Domain Layer (`internal/crm`, `internal/intelligence`, `internal/relationship`, `internal/approval`, `internal/admin`)
- **CRM Service (`crm.Service`)**:
  - Customer 360 Aggregate조회 및 CRUD.
  - Multi-Currency 지원: Original Currency + Fixed Exchange Rate (생성시점 고정) + KRW Base Amount 변환 집계.
  - Revenue Schedules: 계약 승인/활성화 시 일시, 월, 분기, 연 단위 일정 자동 분할 생성 및 갱신 파이프라인 관리.
- **Intelligence Service (`intelligence.Service`)**:
  - Deal Health Score (0~100) 산출 engine (활동 빈도, Stage 체류일, Exit Criteria 이행률 종합).
  - Risk Explainer: 위험 원인 분석 및 추천 액션 제공.
  - Daily Forecast Snapshot & Waterfall: snapshot 간 변화량(신규, 금액 변경, Stage 이동, Slippage, Closed Lost) 비교 분석.
  - Manager Override: Forecast 판단 근거와 금액을 영업 담당자의 추정치와 분리하여 기록.
- **Relationship Service (`relationship.Service`)**:
  - Relationship Map: Decision Maker, Champion, Neutral, Block, Supporter 간 영향력 그래프 및 담당자 매핑.
  - Strategic Account Plan: 연간 목표, 전략 과제, 경쟁사 위협, White Space Cross-sell 기회 Matrix.
  - Opportunity Team: Owner 외 협업 역할(Presales, Consultant, Manager, Legal) 기록 (Data Scope 비확대 원칙 준수).
- **Approval Engine (`approval.Service`)**:
  - Entity별(Opportunity, Quotation, Contract, Customer) 조건부 팀장 승인 workflow.
  - Active Policy가 존재할 때만 관련 UI/API 승인 절차 활성화.
- **Admin & Operations Service (`admin.SettingsManager`)**:
  - System Settings (`system_settings`) 관리.
  - Configuration Bundle: Export, Diff Engine (CREATE/UPDATE/NO_CHANGE), 비파괴 Upsert.
  - Data Quality Center: 완성도 점수 계산 및 5대 문제 이슈(중복, 미접촉, Next Action 미설정 등) 표본 검출.

### 2.4 Model Context Protocol Layer (`internal/mcp`)
- **Streamable HTTP Adapter (`/mcp`)**:
  - MCP JSON-RPC 2.0 및 Protocol Version (`2025-11-25`, `2025-06-18`, `2025-03-26`) 규격 지원.
  - Header 검증: `Accept: application/json, text/event-stream` 및 `MCP-Protocol-Version`.
- **Permission & Risk Annotation Engine**:
  - 사용자 권한 ∩ Data Scope ∩ Personal Key Scope 3중 교집합 적용.
  - Tool Annotations: `readOnlyHint`, `destructiveHint`, `idempotentHint` 및 Risk Level (`READ`, `ANALYZE`, `WRITE`, `APPROVAL`).
  - Total 13종 전용 MCP Tool 제공 (Sales Intelligence + Relationship Intelligence).

### 2.5 Security & Persistence Layer (`internal/platform`)
- **Secrets Management (`platform/secrets`)**:
  - Envelope Encryption: DB Data Key 보호를 위한 Master Key 관리.
  - `ENCRYPTION_KEY` 환경변수가 주어지면 Key derivation 후 Volume 독립적 복구 보장.
  - Master Key Integrity ID (단방향 12자리 지문) DB 검증으로 잘못된 Volume/Key 연결 시 Fail-Closed 동작.
- **Personal Key Digest (`apikey.Service`)**:
  - Raw Personal Key 미저장 (PostgreSQL에는 `HMAC-SHA256` Digest만 보관).
  - Key Rotation 시 7일 Grace Period dual-active 지원.
- **Database Engine (`platform/database`)**:
  - `pgx/v5` Connection Pool.
  - Go `embed` SQL 쿼리 기반 Schema Migration engine (`migrations/*.sql`).

---

## 3. 데이터베이스 스키마 및 도메인 모델 (Database Architecture)

Relio PostgreSQL 스키마는 크게 6개 핵심 파트로 구분됩니다.

| 영역 | 주요 테이블 | 역할 |
|---|---|---|
| **Core Admin & Security** | `users`, `organizations`, `system_settings`, `api_keys`, `master_keys`, `audit_logs` | 사용자, 조직, RBAC, HMAC Key Digest, Master Key 무결성, Audit |
| **CRM Core** | `customers`, `contacts`, `opportunities`, `stage_history`, `activities`, `contracts`, `revenue_schedules` | Customer 360, Stage 전환, 활동, 계약, 매출 인식 스케줄 |
| **Sales Intelligence** | `stage_playbooks`, `deal_health_logs`, `forecast_snapshots`, `manager_overrides` | Playbook, Exit Criteria, Health 로그, Forecast Waterfall, Override |
| **Relationship** | `contact_relationships`, `account_plans`, `opportunity_team_members` | 관계 망 Graph, Strategic Plan, White Space, 협업 팀원 |
| **Approval System** | `approval_policies`, `approval_requests`, `approval_logs` | 팀장 승인 정책, 승인 요청 및 승인/반려 이력 |
| **MCP Operations** | `mcp_request_logs` | MCP 도구 호출 이력, 실행 시간, 쿼리, IP 및 결과 감사 |

---

## 4. 백그라운드 잡 런너 (Background Job Architecture)

`internal/job/runner.go`는 외부 Scheduler 없이 Go Goroutine 기반의 안전한 락(Lock) 매커니즘을 사용해 주기적 작업을 처리합니다.

1. **Forecast Daily Snapshot Task**:
   - 매일 자정 전체 Open Opportunity의 Stage, 금액, Close Date 상태를 캡처하여 `forecast_snapshots`에 저장.
2. **Personal Key Grace Period Expire Task**:
   - Rotation 후 7일 유예기간이 만료된 구버전 API Key Digest를 자동으로 무효화.
3. **Data Quality Score Recalculation Task**:
   - 6시간 간격으로 데이터 완성도 및 5대 이상 징후 표본 데이터 갱신.

---

## 5. 에어갭 및 컴플라이언스 보장 (Air-Gap Verification Architecture)

Relio는 폐쇄망 배포를 보장하기 위해 다음과 같은 아키텍처적 검증 스크립트를 포함하고 있습니다:
- `scripts/check-static-assets.sh`: `web/src` 및 `docs`에 외부 CDN URL(`http://`, `https://`) 포함 여부 정적 검사.
- `scripts/run-offline-container-test.sh`: Docker internal network (`--network none` 수준의 인터넷 격리 network) 환경에서 DB 조인, 로그인, API, MCP, Upgrade 수행 여부 자동 검증.
