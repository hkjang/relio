# Relio REST API & MCP (Model Context Protocol) 명세서

- **문서 버전**: v1.11.4
- **최종 수정일**: 2026년 8월 12일
- **대상**: API Integration Engineer, AI Agent Developer, Solutions Architect
- **문서 개요**: Relio REST API v1 Specification, Personal API Key 인증·권한 변경, MCP Streamable HTTP 어댑터 `/mcp` 및 영업 인텔리전스 Tool Schema

---

## 1. REST API Specification (`/api/v1`)

Relio는 시스템 연동을 위해 OpenAPI 3.0 사양의 REST API를 제공합니다.

### 1.1 기본 정보 및 엔드포인트
- **Base URL**: `https://<relio-host>/api/v1`
- **OpenAPI Schema**: `GET /api/openapi.json`
- **Swagger UI**: `GET /api/docs`
- **Health Check**:
  - `GET /health`: 기본 헬스 체크
  - `GET /health/live`: Liveness Probe (컨테이너 생존 여부)
  - `GET /health/ready`: Readiness Probe (PostgreSQL 및 Master Key 준비 여부)

### 1.2 Personal API Key 인증
REST API 및 MCP 연동을 위해 Bearer Token 방식의 Personal API Key를 사용합니다.

```http
Authorization: Bearer relio_4f30d2a1b7c9_xxxxxxxxxxxxxxxxx
```
- **형식**: `relio_{keyId}_{secret}`
- **검증 매커니즘**: `keyId`로 DB 조회 후 `HMAC-SHA256(secret)` 값으로 무결성 검증.
- **권한 제어**: API Key 생성 시 지정된 Scope와 해당 사용자의 RBAC/Data Scope의 교집합만 허용.
- **발급 후 권한 변경**: `PUT /api/v1/me/keys/{id}`에 `scopes`, `channels`, `version`을 전송합니다. Secret은 회전하지 않으며 변경 권한은 다음 요청부터 적용됩니다.

---

## 2. Model Context Protocol (MCP) Server Specification (`/mcp`)

Relio MCP Server는 AI Agent가 CRM 데이터 및 영업 인텔리전스 분석을 직접 안전하게 수행할 수 있도록 지원하는 **Streamable HTTP MCP 어댑터**입니다.

### 2.1 MCP 연결 및 프로토콜 규격
- **Endpoint**: `POST /mcp`
- **JSON-RPC 버전**: `2.0`
- **MCP Protocol Version**: `2025-11-25` (호환 버전: `2025-06-18`, `2025-03-26`, `2024-11-05`) — 클라이언트가 요청한 버전으로 응답합니다.
- **필수 HTTP Headers**:
  - `Authorization: Bearer relio_<keyId>_<secret>` 또는 Keycloak Access Token(2.4 참고)
  - `Accept: application/json, text/event-stream`
  - `MCP-Protocol-Version: 2025-11-25` — 지원하지 않는 버전이면 HTTP `400`
- `/mcp` 와 `/mcp/` 는 같은 엔드포인트입니다. `GET /mcp` 는 서버 주도 SSE 를 제공하지 않으므로 `405`, `DELETE /mcp` 는 `204` 입니다.

#### 응답 계약
- `tools/call` 결과의 `structuredContent` 는 **항상 JSON 객체**입니다. 목록을 돌려주는 도구는 `{ "items": [...], "count": n }` 으로 감싸며, `content[0].text` 에는 원래 JSON(배열 포함)이 그대로 들어 있습니다. 공식 MCP SDK 기반 클라이언트는 객체가 아닌 `structuredContent` 를 스키마 오류로 거부합니다.
- 도구 인자는 실행 전에 해당 도구의 `inputSchema` 로 검사합니다. 필수 인자 누락, UUID 가 아닌 ID, 알 수 없는 인자, 잘못된 형식은 `isError: true` 결과와 고칠 방법을 담은 메시지로 돌아옵니다(2025-11-25 명세 권고 — 모델이 스스로 고칠 수 있게). `customer_id`, `CustomerID` 처럼 표기만 다른 인자는 `customerId` 로 맞춰 적용하고, `"5"` 같은 숫자 문자열과 `"true"`/`"false"` 는 해당 형식으로 변환합니다.
- 데이터베이스 오류 원문(SQLSTATE 등)은 응답에 포함하지 않습니다. 원인은 요청 ID 와 함께 서버 로그에 남습니다.
- 파싱할 수 없는 본문은 HTTP `400` 과 `"id": null` 인 JSON-RPC 오류로 응답합니다. 배치 요청의 한 항목에서 내부 오류가 나도 나머지 항목은 정상 응답합니다.

### 2.2 MCP 통제 및 Risk Level Annotations
AI Agent의 오작동 및 부적절한 변경을 방지하기 위해 모든 Tool에는 **Risk Level Annotation**이 포함되어 반환됩니다:

| Risk Level | 설명 | 예시 Tool |
|---|---|---|
| **`READ`** | 조회 전용 안전한 도구 | `get_customer`, `get_pipeline` |
| **`ANALYZE`** | 진단 및 위험 원인 설명, 추천 액션 도구 | `find_deals_at_risk`, `explain_deal_risk`, `recommend_next_actions` |
| **`WRITE`** | 영업기회 생성/수정, 활동 등록 도구 | `create_opportunity`, `add_activity`, `build_account_plan` |
| **`APPROVAL`** | high-risk 승인/반려 처리 도구 (추가 검증 필요) | `approve_request`, `reject_request` |

### 2.3 Qwen Code와 OpenCode 설정

- Qwen Code는 `mcpServers.relio.httpUrl`과 `headers.Authorization`을 사용합니다. `url`은 구형 SSE 설정입니다.
- OpenCode는 `mcp.relio.type`을 `remote`로, `url`, `headers`, `oauth: false`를 설정합니다.
- 제품의 **개인 연동 키 → MCP 사용 안내 → 클라이언트 설정**에서 현재 Host와 키 형식이 반영된 예시를 복사할 수 있습니다.

### 2.4 조직 계정 OAuth (Keycloak)

관리자가 켜면(`mcp.oauth_enabled`) Relio 는 MCP Authorization 명세의 OAuth Resource Server 로 동작합니다.

| 단계 | 내용 |
|---|---|
| 1. 401 | 토큰 없이 `POST /mcp` → `401` + `WWW-Authenticate: Bearer realm="Relio MCP", resource_metadata="<service_url>/.well-known/oauth-protected-resource/mcp", scope="profile email relio-mcp"` |
| 2. Protected Resource Metadata (RFC 9728) | `GET /.well-known/oauth-protected-resource/mcp` (루트 `/.well-known/oauth-protected-resource` 도 동일) → `resource`, `authorization_servers: [Keycloak Issuer]`, `scopes_supported` |
| 3. 로그인 | 클라이언트가 Keycloak 메타데이터를 읽고 PKCE(S256)와 `resource`(RFC 8707)로 인가 요청 → 사용자가 조직 계정으로 로그인 |
| 4. 호출 | `Authorization: Bearer <Keycloak Access Token>` 으로 MCP 호출 |

Relio 가 받는 토큰의 조건 — 서명(RS256, Keycloak JWKS), Issuer 일치, 유효기간(`exp`/`nbf`), **ID·Refresh 토큰이 아닐 것**(`typ`), `aud` 에 `<service_url>/mcp`·`<service_url>`·SSO Client ID 중 하나, 필수 Scope(설정 시). 거부 사유는 `WWW-Authenticate` 의 `error="invalid_token"`(다시 로그인) 또는 `403` + `error="insufficient_scope"`(필요 Scope 를 포함해 다시 로그인)로 알려 줍니다. Keycloak 서명 키와 메타데이터는 캐시하며(15분, 모르는 `kid` 는 30초에 한 번까지 갱신, Keycloak 장애 시 6시간 동안 기존 키로 검증) 요청마다 Keycloak 을 호출하지 않습니다.

OAuth 로 연결한 에이전트의 도구 범위는 **사용자 Role 권한 ∩ 관리자 Tool 허용목록** 입니다(개인 키 Scope 없음). Keycloak 설정 순서는 [관리자 가이드 4.4](ADMIN_GUIDE.md#44-조직-계정oauth으로-mcp-연결) 를 보세요.

### 2.5 고객 요청 업무 영역 (부서 VOC) 도구

부서 업무 영역(예: 회원사 민원)을 쓰는 사용자에게 보이는 도구입니다. `fields` 인자의 스키마는 호출자가 쓸 수 있는 업무 영역의 항목 정의로 **요청마다 생성** 되며, 선택형 항목은 허용값이 `enum` 으로 들어갑니다. 쓰기 도구는 `readOnlyHint: false` 이고 설명에 "담당자 승인 후 실행" 을 명시합니다.

| 도구 | 필수 인자 | 동작 |
|---|---|---|
| `search_voice_knowledge` | `query` | 해결·종결 건에서 오류코드·증상을 찾습니다. 기본은 `knowledgeStatus=APPROVED`(반영) 건만. `includeUnreviewed: true` 면 미검토·검토중도 포함하며 '제외' 건은 어떤 경우에도 반환하지 않습니다. `fields`(항목값 일치), `causeEvidence` 로 좁힙니다. 결과: `title`, `customerSaid`(원문), `rootCause`, `resolution`, `causeEvidence`, `knowledgeStatus`, `fields`(해결 주체 포함) |
| `get_customer_voice_history` | `customerCode` 또는 `customerId` | 그 고객의 종결 건 전체(해결·종결·반려), 최신순, `limit` 최대 200. 지식 게이트 미적용, 상태 필드는 포함 |
| `file_customer_voice` | `title` | 접수. `customerCode` 로 고객 지정, `category` 에 유형 이름·코드·ID, `fields` 에 접수 항목 |
| `record_voice_response` | `id`, `note` | 처리 이력 추가(append-only). `eventType`: `CUSTOMER_CONTACT`(고객 응대) · `COMMENT`(내부 메모, 회고 요약) · `ESCALATED`(상위 보고) |
| `resolve_customer_voice` | `id`, `resolution` | 해결 처리. `causeEvidence`(지식 게이트 영역은 필수), `rootCause`, `fields`(해결 항목). 접수 상태면 처리 중을 거쳐 해결로 바꾸며 두 단계 모두 이력에 남습니다 |
| `register_workspace_customer` | `workspaceId`, `name`, `customerCode` | 간이 등록. 같은 코드가 있으면 등록하지 않고 `existing` 으로 기존 고객을 돌려줍니다 |
| `import_workspace_customers` | `workspaceId`, `items` | 일괄 등록(최대 5000행). 행마다 `CREATED`·`UPDATED`·`UNCHANGED`·`ERROR` |
| `get_voice_categories` | — | 유형 목록과 업무 영역, 영역별 항목 정의·허용값, SLA 적용 여부 |

**지식 반영 상태를 바꾸는 도구는 없습니다.** REST `PUT /api/v1/voices/{id}/knowledge` 도 `voice:knowledge-review` 권한과 **화면 세션** 을 요구하며, 개인 연동 키·OAuth 토큰으로는 거부됩니다.

REST: `GET /api/v1/voices/workspaces`, `GET /api/v1/voices/knowledge`(`q`, `fieldFilters` JSON, `causeEvidence`, `includeUnreviewed`), `GET /api/v1/voices/history`, `POST /api/v1/voices/workspaces/{id}/customers`, `POST /api/v1/voices/workspaces/{id}/customers/import`. 목록 `GET /api/v1/voices` 는 `workspaceId`, `categoryId`, `knowledgeStatus`, `reviewPending`, `minAgeDays` 필터를 받습니다.

---

## 3. 13가지 핵심 MCP Tools 명세 (MCP Tool Directory)

Relio MCP Server는 Sales Intelligence 6종과 Relationship Intelligence 7종을 포함한 전용 Tool 세트를 제공합니다.

### 3.1 Sales Intelligence MCP Tools (6종)

#### 1. `find_deals_at_risk`
- **Risk Level**: `ANALYZE`
- **설명**: 설명 가능한 판단 규칙(활동 공백, Stage 체류일, Exit Criteria 미흡)으로 지정된 위험 점수 이상의 영업건을 검출합니다.
- **Input Arguments**:
  - `minimum` (integer): 최소 위험 점수 (기본값: 40)
  - `limit` (integer): 최대 결과 건수 (기본값: 25)

#### 2. `explain_deal_risk`
- **Risk Level**: `ANALYZE`
- **설명**: 특정 영업기회의 위험 점수, 점수 산출 근거, 추천 조치 사항 및 최근 7/14일간의 변화 내역을 상세히 설명합니다.
- **Input Arguments**:
  - `id` (string, **필수**): 영업기회 ID
  - `days` (integer): 변화 분석 기간 (기본값: 7)

#### 3. `recommend_next_actions`
- **Risk Level**: `ANALYZE`
- **설명**: Deal Health 신호와 해당 Stage의 Sales Playbook을 결합하여 담당자가 실행해야 할 최적의 다음 행동(Next Action)을 추천합니다.
- **Input Arguments**:
  - `id` (string, **필수**): 영업기회 ID

#### 4. `get_stage_readiness`
- **Risk Level**: `ANALYZE`
- **설명**: 다음 Stage로의 전환 시 필수 조건인 Exit Criteria 충족 여부 및 미흡 항목을 진단합니다.
- **Input Arguments**:
  - `id` (string, **필수**): 영업기회 ID
  - `stageId` (string, **필수**): 이동하려는 Stage ID

#### 5. `explain_forecast_change`
- **Risk Level**: `ANALYZE`
- **설명**: Daily Snapshot 데이터를 비교하여 Commit/Best Case/Pipeline 금액의 변동 원인(신규 추가, Lost, 금액 증감, Slippage)을Waterfall 분석 형태로 설명합니다.
- **Input Arguments**:
  - `days` (integer): 비교 대상 기간 (일수, 기본값: 7)

#### 6. `get_sales_coaching_insights`
- **Risk Level**: `ANALYZE`
- **설명**: 영업 팀장의 관점에서 팀원별 고위험 Deal 및 실행 공백을 분석하여 1:1 Coaching에 필요한 핵심 인사이트를 제공합니다.
- **Input Arguments**: 없음

---

### 3.2 Relationship Intelligence MCP Tools (7종)

#### 7. `get_account_brief`
- **Risk Level**: `READ`
- **설명**: 고객 360, Relationship Map, Strategic Account Plan을 미팅 준비용 종합 브리핑 뷰 형태로 조합해 제공합니다.
- **Input Arguments**:
  - `id` (string, **필수**): 고객 ID
  - `year` (integer): Account Plan 연도

#### 8. `get_account_relationships`
- **Risk Level**: `READ`
- **설명**: 고객사 담당자들의 의사결정 역할(Decision Maker, Champion, Supporter 등), 영향력 및 연결 네트워크 그래프를 제공합니다.
- **Input Arguments**:
  - `id` (string, **필수**): 고객 ID

#### 9. `get_account_plan`
- **Risk Level**: `READ`
- **설명**: 전략 고객의 연간 사업 목표, 당사 영업 전략, 경쟁사 위협 요소 및 White Space Matrix를 조회합니다.
- **Input Arguments**:
  - `id` (string, **필수**): 고객 ID
  - `year` (integer): 계획 연도

#### 10. `find_cross_sell_opportunities`
- **Risk Level**: `ANALYZE`
- **설명**: Account Plan의 White Space 분석을 통해 미제안 제품군 및 Cross-sell/Up-sell 확장 기회를 도출합니다.
- **Input Arguments**:
  - `id` (string, **필수**): 고객 ID
  - `year` (integer): 계획 연도

#### 11. `build_account_plan`
- **Risk Level**: `WRITE`
- **설명**: 전략 고객의 사업 목표, 영업 전략, 목표 매출 및 White Space 계획을 새롭게 수립하거나 갱신합니다.
- **Input Arguments**:
  - `id` (string, **필수**): 고객 ID
  - `planYear` (integer, **필수**): 계획 연도
  - `status` (string, **필수**): `DRAFT`, `ACTIVE`, `ARCHIVED`
  - `version` (integer, **필수**): 낙관적 잠금 버전

#### 12. `get_opportunity_team`
- **Risk Level**: `READ`
- **설명**: 영업기회 Owner 외에 참여 중인 협업 멤버(Presales, Consultant, Manager, Legal)와 담당 책임을 조회합니다.
- **Input Arguments**:
  - `id` (string, **필수**): 영업기회 ID

#### 13. `add_opportunity_member`
- **Risk Level**: `WRITE`
- **설명**: 영업기회 협업 팀에 신규 구성원 및 역할을 추가하거나 담당 업무 내용을 업데이트합니다.
- **Input Arguments**:
  - `id` (string, **필수**): 영업기회 ID
  - `userId` (string, **필수**): 사용자 ID
  - `role` (string, **필수**): 협업 역할 (`PRESALES`, `CONSULTANT`, `MANAGER`, `LEGAL` 등)
  - `version` (integer, **필수**): 낙관적 잠금 버전

---

## 4. MCP JSON-RPC 2.0 호출 예시 (Tool Call Sample)

### 4.1 Request Header & Payload
```http
POST /mcp HTTP/1.1
Host: relio.internal
Authorization: Bearer relio_4f30d2a1b7c9_8a9b0c1d2e3f4a5b
Accept: application/json, text/event-stream
MCP-Protocol-Version: 2025-11-25
Content-Type: application/json

{
  "jsonrpc": "2.0",
  "id": "req-001",
  "method": "tools/call",
  "params": {
    "name": "explain_deal_risk",
    "arguments": {
      "id": "opp-998231",
      "days": 7
    }
  }
}
```

### 4.2 Response Payload
```http
HTTP/1.1 200 OK
Content-Type: application/json
MCP-Protocol-Version: 2025-11-25

{
  "jsonrpc": "2.0",
  "id": "req-001",
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\"opportunityId\":\"opp-998231\",\"riskScore\":78,\"riskLevel\":\"HIGH\",\"factors\":[{\"code\":\"STALE_STAGE\",\"score\":35,\"message\":\"Proposal Stage에서 42일간 체류 중 (평균 대비 +24일)\"},{\"code\":\"MISSING_DECISION_MAKER\",\"score\":25,\"message\":\"Relationship Map 상에 Decision Maker 미지정\"}],\"recommendations\":[\"고객사 Decision Maker 미팅 일정 수립\",\"Exit Criteria 제안서 승인 문서 첨부\"]}"
      }
    ],
    "structuredContent": {
      "opportunityId": "opp-998231",
      "riskScore": 78,
      "riskLevel": "HIGH"
    },
    "isError": false
  }
}
```
