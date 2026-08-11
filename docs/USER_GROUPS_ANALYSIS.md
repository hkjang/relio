# Relio 사용자 그룹 및 페르소나 분석 명세서 (User Groups & Persona Analysis)

- **문서 버전**: v1.6.0  
- **작성일자**: 2026년 8월 11일  
- **대상**: UX Designer, Product Manager, Business Analyst, Customer Success  
- **문서 개요**: Relio B2B CRM의 5대 주요 페르소나(영업대표, 영업팀장, CISO/보안엔지니어, 시스템관리자, AI Agent 개발자)별 니즈, Pain Points, 핵심 활용 기능 및 권한 매핑 분석  

---

## 1. 개요 및 사용자 타겟 체계

Relio는 사내 폐쇄망 B2B 영업 조직의 성공을 위해 서로 다른 이해관계를 가진 5대 사용자 그룹을 정의하고 각 페르소나에 최적화된 UX 및 보안 모델을 제공합니다.

---

## 2. 5대 사용자 페르소나 분석 (User Personas)

```
+-----------------------------------------------------------------------------------+
|                            Relio 5 Key User Personas                              |
|                                                                                   |
|  [ 1. Sales Rep ]   [ 2. Sales Manager ]  [ 3. CISO/Security ]  [ 4. Admin ]  [ 5. AI Agent ] |
|  - Customer 360     - Forecast Waterfall  - Air-Gap 100%        - Command Center - MCP Tools |
|  - Playbook Guide   - Coaching Insights   - HMAC Digest        - Config Bundle  - Risk Level|
+-----------------------------------------------------------------------------------+
```

### 2.1 Persona 1: 영업 대표 (Sales Representative - "김영업")
- **역할 및 배경**: B2B 대형 솔루션 영업 담당자. 다수의 고객사 담당자와 장기 영업건(Sales Cycle 3~6개월) 관리.
- **Pain Points**: 번거로운 입력 작업, 딜 위험 인지 지연, 의사결정권자 매핑 부재로 인한 딜 Loss.
- **Relio 제공 가치**:
  - **Customer 360 & Relationship Map**: 고객사 의사결정권자(Decision Maker, Champion) 영향력 시각화.
  - **Sales Playbook**: Stage별 이행 가이드 및 다음 행동(Next Action) 추천.
  - **Dynamic Currency**: 해외 거래 건 원금 및 환율 자동 변환.

### 2.2 Persona 2: 영업 팀장 (Sales Manager - "이팀장")
- **역할 및 배경**: 영업 팀 실적 관리, 매출 예측(Forecast) 및 팀원 1:1 코칭 담당.
- **Pain Points**: 팀원들의 과장되거나 불확실한 매출 추정치, 고위험 딜 감지 어려움.
- **Relio 제공 가치**:
  - **Forecast Snapshot & Waterfall**: daily Snapshot 기반 변동 원인(Slippage, Loss, 금액 증감) 분석.
  - **Manager Override**: 팀장의 독립적인 판단 금액과 사유 분리 기록.
  - **Sales Coaching Insights**: 고위험 딜 및 실행 공백 자동 감지.

### 2.3 Persona 3: 정보보안책임자 (CISO / Security Auditor - "박보안")
- **역할 및 배경**: 기업 내 데이터 유출 방지 및 보안 컴플라이언스 준수 책임자.
- **Pain Points**: SaaS CRM으로 인한 사내 중요 영업 기밀 유출 위험, 외부 통신 통제 불능.
- **Relio 제공 가치**:
  - **100% Air-gapped Architecture**: 런타임 CDN, 외부 웹폰트, Telemetry 0% 완전 차단.
  - **HMAC Digest & Master Key Integrity**: Raw Key 저장 배제 및 Key mismatch 시 Fail-Closed 기동 중단.
  - **Audit Trail**: 모든 행위의 전후 DTO 시각적 비교 검증.

### 2.4 Persona 4: 시스템 및 인프라 관리자 (DevOps Admin - "최관리")
- **역할 및 배경**: 사내 IT 인프라, Docker 서버 및 DB 운영 담당자.
- **Pain Points**: 복잡한 미들웨어(Redis 등) 관리 부담, 서비스 이관 시 자격증명 재발급 번거로움.
- **Relio 제공 가치**:
  - **3+1 최소 환경변수**: 단일 Docker 컨테이너 및 PostgreSQL 단일 DB 간편 운영.
  - **`ENCRYPTION_KEY` 자격증명 영속성**: Volume 재생성 시에도 Key continuous 완벽 유지.
  - **Operations Command Center & Data Quality**: 7대 자동 진단 및 비파괴 Configuration Bundle Upsert.

### 2.5 Persona 5: AI Agent 개발자 (AI Systems Engineer - "정AI")
- **역할 및 배경**: 사내 LLM 및 업무 자동화 AI Agent 개발자.
- **Pain Points**: Direct DB SQL 접근 시 보안 위험, AI Agent의 오작동 및 데이터 파괴 염려.
- **Relio 제공 가치**:
  - **MCP Streamable HTTP Server (`/mcp`)**: Standard MCP Protocol (2025-11-25) 지원.
  - **Risk Level Annotations**: `READ`, `ANALYZE`, `WRITE`, `APPROVAL` 4단계 리스크 명시.
  - **13가지 전용 CRM Tools**: Sales & Relationship Intelligence 전용 도구 제공.

---

## 3. 사용자 그룹별 RBAC 및 Data Scope 권한 Matrix

| 사용자 그룹 | Function Permission 주요 범위 | Data Scope 기본값 | Personal Key 생성 권한 |
|---|---|---|---|
| **Sales Rep** | `customer:read/write`, `opportunity:read/write`, `activity:write` | `SELF` | 허용 (`mcp:use`, read/write) |
| **Sales Manager** | `customer:read/write`, `opportunity:*`, `forecast:*`, `approval:*` | `TEAM` | 허용 (팀 관리 Scope 포함) |
| **Executive** | `customer:read`, `opportunity:read`, `forecast:read` | `ALL` | 허용 (조회 전용 Scope) |
| **Admin** | `admin:*`, `system:*`, `user:*` | `ALL` | 허용 (관리자 전체 Scope) |
| **Auditor** | `audit:read`, `system:read` | `ALL` | 허용 (감사 전용 Scope) |
