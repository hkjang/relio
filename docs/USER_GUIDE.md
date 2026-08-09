# Relio 엔터프라이즈 사용자 가이드 (User Guide & Sales Manual)

- **문서 버전**: v1.0.0-ENTERPRISE  
- **대상**: 영업 담당자, 영업 본부장/팀장, AI MCP 클라이언트 사용자  
- **문서 개요**: Customer 360 고객 관리, Opportunity 파이프라인 & Deal Health, Forecast 분석, Personal Key 관리 및 MCP 활용 매뉴얼  

---

## 1. 개요 및 화면 구역 체계 (`/app`, `/me`, `/admin`)

Relio는 사용자 목적에 따라 3개의 영역으로 명확히 분리됩니다.

- **`/app/*` (CRM 영업 전용)**: Dashboard, Customer 360, Opportunity, Pipeline, Activity, Forecast, 계약 및 매출 관리.
- **`/me/*` (개인화)**: 개인 Dashboard, 목표, 일정, 알림, Personal API/MCP Key 발급 및 보안 활동 기록.
- **`/admin/*` (통합 관리자 콘솔)**: 시스템, OIDC, 사용자·조직 RBAC, 파이프라인 stage 설정, 승인 정책, Audit 로그.

---

## 2. Customer 360 & 중복 탐지·병합

### 2.1 Customer 360 통합 뷰
- 고객사 메인 정보, 담당자 연락처, 진행 중인 Opportunity, 과거 거래 이력 및 최근 Activity(미팅, 전화, 메일)를 한 화면에서 입체적으로 확인합니다.

### 2.2 중복 탐지 및 안전 병합 (Duplicate Merge)
- 시스템이 상호명, 사업자번호, 대표 이메일을 기준으로 중복 고객 후보를 자동 제안합니다.
- 영업 담당자는 주 마스터 레코드를 지정하고 안전하게 이력을 병합할 수 있습니다.

---

## 3. Opportunity Pipeline & Deal Health

- **Deal Health Score (건강도 점수)**: 최근 미팅 발생 주기, 담당자 반응, 계약 예정일과의 시차 등을 종합 계산하여 0~100점 점수로 표시됩니다.
- **Pipeline Stage 전이**: Draft ➔ Qualification ➔ Proposal ➔ Negotiation ➔ Closed Won/Lost 단계로 드래그 앤 드롭 전이 처리합니다.

---

## 4. Personal API / MCP Key 발급 및 AI 연동

1. 우측 상단 프로필 ➔ **`/me/keys` (개인 API/MCP 키)** 이동.
2. **[신규 Personal Key 발급]** 클릭 ➔ `relio_4f30d2a1b7c9_xxxxxxxxxxxxxxxxx` 형식 키 생성.
3. Claude Desktop 또는 Cursor 설정에 MCP 서버를 등록하여 자연어로 영업 지표 및 파이프라인 조회:

```json
{
  "mcpServers": {
    "relio": {
      "command": "curl",
      "args": [
        "-X", "POST",
        "-H", "Authorization: Bearer relio_4f30d2a1b7c9_xxxxxxxxxxxxxxxxx",
        "-H", "Accept: application/json, text/event-stream",
        "https://relio.internal/mcp"
      ]
    }
  }
}
```

> **보안 정책**: Relio MCP는 AI Agent에 직접 SQL 작성 권한을 절대 주지 않으며, 호출 사용자의 Function Permission과 Data Scope 교집합 내에서 안전하게 도구를 렌더링합니다.
