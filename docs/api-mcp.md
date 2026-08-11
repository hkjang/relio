# Relio REST API & MCP (Model Context Protocol) 명세서

- **문서 버전**: v1.6.0  
- **최종 수정일**: 2026년 8월 11일  
- **대상**: API Integration Engineer, AI Agent Developer, Solutions Architect  
- **문서 개요**: Relio REST API v1 Specification, Personal API Key 인증, MCP Streamable HTTP 어댑터 `/mcp`, Protocol Version 2025-11-25 및 13가지 전용 MCP Tool 상세 Schema  

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

---

## 2. Model Context Protocol (MCP) Server Specification (`/mcp`)

Relio MCP Server는 AI Agent가 CRM 데이터 및 영업 인텔리전스 분석을 직접 안전하게 수행할 수 있도록 지원하는 **Streamable HTTP MCP 어댑터**입니다.

### 2.1 MCP 연결 및 프로토콜 규격
- **Endpoint**: `POST /mcp`
- **JSON-RPC 버전**: `2.0`
- **MCP Protocol Version**: `2025-11-25` (호환 버전: `2025-06-18`, `2025-03-26`)
- **필수 HTTP Headers**:
  - `Authorization: Bearer relio_<keyId>_<secret>`
  - `Accept: application/json, text/event-stream`
  - `MCP-Protocol-Version: 2025-11-25`

### 2.2 MCP 통제 및 Risk Level Annotations
AI Agent의 오작동 및 부적절한 변경을 방지하기 위해 모든 Tool에는 **Risk Level Annotation**이 포함되어 반환됩니다:

| Risk Level | 설명 | 예시 Tool |
|---|---|---|
| **`READ`** | 조회 전용 안전한 도구 | `get_customer`, `get_pipeline` |
| **`ANALYZE`** | 진단 및 위험 원인 설명, 추천 액션 도구 | `find_deals_at_risk`, `explain_deal_risk`, `recommend_next_actions` |
| **`WRITE`** | 영업기회 생성/수정, 활동 등록 도구 | `create_opportunity`, `add_activity`, `build_account_plan` |
| **`APPROVAL`** | high-risk 승인/반려 처리 도구 (추가 검증 필요) | `approve_request`, `reject_request` |

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
