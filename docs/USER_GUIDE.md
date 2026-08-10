# Relio 엔터프라이즈 사용자 가이드 (User Guide & Sales Manual)

- **문서 버전**: v1.0.0-ENTERPRISE  
- **작성일자**: 2026년 8월 9일  
- **대상**: 영업 담당자, 영업 본부장/팀장, SRE 엔지니어, AI MCP 클라이언트 사용자  
- **문서 개요**: Customer 360, Opportunity 파이프라인 & Deal Health, Forecast 분석, 3개 구역 분리 (`/app`, `/me`, `/admin`), Personal Key 발급 및 8개 MCP Tools 연동 매뉴얼  

---

## 1. 시스템 개요 및 3개 구역 분리 체계

Relio는 업무 역할과 접근 보안을 보장하기 위해 3개의 주요 영역으로 명확히 구율 분리되어 운영됩니다.

```
==================================================================================================
                              [Relio 3대 화면 영역 분리 체계]
==================================================================================================
  [1] CRM 영업 영역 (/app/*)   ➔ Dashboard, Customer 360, Opportunity, Pipeline, Forecast, 계약
  [2] 개인화 영역 (/me/*)       ➔ 프로필, 개인 목표/일정/알림, Personal API/MCP Key, 보안 기록
  [3] 통합 관리자 콘솔 (/admin/*) ➔ 시스템 설정, OIDC, 사용자/조직 RBAC, 승인 정책, 감사 로그
==================================================================================================
```

---

## 2. Customer 360 & 중복 탐지·병합 워크플로우

### 2.1 Customer 360 통합 뷰
- **기본 정보 탭**: 고객사 명칭, 사업자등록번호, 업종, 주소 및 대표 연락처 통합 조회.
- **활동 기록(Activity Log)**: 미팅 일시, 전화 통화 내역, 이메일 수발신 기록, 제안서 전달 내역 타임라인 관리.
- **연관 영업 기회(Opportunities)**: 진행 중인 파이프라인 금액 및 예상 수주 일자 연동 트래킹.

### 2.2 중복 고객 탐지 및 안전 병합 (Duplicate Detection & Merge)
1. 영업 담당자가 신규 고객 등록 시 상호명 유사도 또는 사업자번호 중복을 자동 감지합니다.
2. 중복 제안 알림 창에서 **[상세 대조]**를 눌러 주 마스터 레코드(Primary Record)를 지정합니다.
3. 병합 실행 시 서브 레코드의 모든 미팅 이력 및 Opportunity가 주 레코드로 무손실 전이됩니다.

---

## 3. Opportunity 파이프라인 & Deal Health 헬스스코어

### 3.1 Deal Health 건강도 점수화 모델
- **점수 계산 알고리즘**: 최근 미팅 발생 주기(40%) + 담당자 반응도(30%) + 수주 예상일 남은 시간(30%)을 0~100점으로 정밀 계산.
- **상태 구간**:
  - `90 ~ 100점` (HEALTHY): 수주 가능성 매우 높음 (녹색)
  - `60 ~ 89점` (ATTENTION): 추가 영업 활동 필요 (노란색)
  - `0 ~ 59점` (AT RISK): 수주 지연 위험 / 관리자 개입 필요 (빨간색)

### 3.2 파이프라인 스테이지 전이
- Draft ➔ Qualification ➔ Proposal ➔ Negotiation ➔ Closed Won/Lost 순서로 드래그 앤 드롭 전이 처리합니다.

---

## 4. 다중 통화 Forecast와 계약 Revenue 실행

### 4.1 외화 Opportunity 등록
1. Opportunity 생성 시 거래통화와 원거래 금액을 입력합니다.
2. KRW가 아닌 경우 사내 재무 기준의 `1 거래통화 = KRW` 고정 환율을 입력합니다.
3. Pipeline과 Forecast 합계는 KRW 기준금액으로 비교하고, Deal 상세에서는 원거래 금액과 환산금액을 함께 확인합니다.

실시간 외부 환율 서비스는 에어갭 운영과 과거 Forecast 재현성을 훼손하므로 사용하지 않습니다. 환율 변경은 기존 거래를 소급 변경하지 않고 새 거래 또는 명시적인 Deal 수정에 반영합니다.

### 4.2 계약 활성화와 Revenue Schedule
1. 계약 화면에서 고객, 계약 기간, 거래통화, 금액과 매출 인식 주기를 입력해 Draft를 만듭니다.
2. 계약 조건을 검토한 뒤 **계약 활성화 · 일정 생성**을 실행합니다.
3. 일시·월·분기·연 주기에 따라 생성된 Revenue Schedule의 예정일과 금액을 확인합니다.
4. 이행이 완료된 일정에서 **매출 인식**을 실행하면 Sales Ledger와 계약 이행률이 함께 갱신됩니다.

### 4.3 Renewal Radar
- `EXPIRING`은 계약별 사전 점검 일수 안에 진입한 Active 계약입니다.
- 갱신 진행상태와 다음 행동을 반드시 함께 기록해 단순 알림을 실행 가능한 Account Plan으로 전환합니다.
- `RENEWED`와 `CHURNED`는 최종 결과이며, 갱신 사유와 후속 Opportunity는 Customer 360에 함께 남기는 것을 권장합니다.

---

## 5. Personal API / MCP Key 발급 및 AI 에이전트 연동

### 5.1 Personal Key 발급 절차
1. 우측 상단 프로필 클릭 ➔ **`/me/keys` (개인 API/MCP 키)** 이동.
2. **[신규 Personal Key 발급]** 클릭 ➔ Key Name 및 Expiry 설정 후 생성.
3. 발급된 Raw Key(`relio_4f30d2a1b7c9_xxxxxxxxxxxxxxxxx`)를 안전한 장소에 복사 (최초 1회만 표시됨).

### 5.2 Claude Desktop 및 Cursor MCP 연동
설정 파일(`claude_desktop_config.json`)에 아래 Streamable HTTP MCP 클라이언트 구성을 추가합니다:

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

### 5.3 제공되는 핵심 MCP Tools 목록
1. `relio_get_customer_360`: 특정 고객사의 360도 통합 정보 조회
2. `relio_list_opportunities`: 권한 내 영업 기회 목록 및 Deal Health 스코어 파싱
3. `relio_create_activity`: 미팅 및 전화 영업 활동 기록 추가
4. `relio_get_forecast`: 파이프라인 매출 예상액 분석 지표 파싱
5. `relio_search_customers`: 상호명 및 키워드 기반 고객사 검색

---

## 6. 자주 묻는 질문 및 문제 해결 (Troubleshooting FAQ)

| 증상 | 원인 | 조치 방법 |
| :--- | :--- | :--- |
| **Personal Key 인증 실패 (401)** | 키 만료 또는 유예기간(7일) 경과 | `/me/keys` 메뉴에서 신규 키 발급 및 회전(Rotation) 실행 |
| **MCP 연동 시 일부 도구 안 보임** | 관리자 Tool Allowlist 미승인 | `/admin/mcp` 대시보드에서 해당 사용자의 MCP Tool 권한 확인 |
| **승인 버튼이 표시되지 않음** | 영업 기회 승인 정책 미활성화 | 해당 Entity에 승인 정책이 없을 경우 승인 버튼은 자동 숨김 처리됨 |
| **Keycloak 로그인 후 권한 메뉴가 없음** | OIDC Default Role 또는 Role Claim Mapping 미지정 | 관리자가 OIDC Default Role/Mapping을 지정합니다. 화면은 빈 화면 대신 권한 제한 상태로 정상 표시됩니다. |
