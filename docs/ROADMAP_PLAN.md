# Relio 제품 로드맵 및 향후 발전 계획 (Product Roadmap Plan)

- **문서 버전**: v1.6.0 (v1.7.0 ~ v2.0.0 로드맵 및 엔터프라이즈 연동 확장 반영)  
- **작성일자**: 2026년 8월 11일  
- **대상**: Product Manager, Lead Developer, 경영진, Enterprise Architect, Customer Success Lead  
- **문서 개요**: Relio v1.0부터 현재 v1.6.0까지의 주요 달성 성과와 함께, B2B 영업 관리를 넘어 **고객 접점/불만 관리(VOC)** 및 **사내 지식(Confluence) & ERP 연동**을 포함한 v1.7.0 ~ v2.0.0 제품 로드맵 및 기술 고도화 계획  

---

## 1. 개요 및 로드맵 수립 원칙

Relio는 **"단일 컨테이너 기반 사내 에어갭 B2B CRM"**이라는 비전 아래, 영업 기회 발굴부터 계약 체결, 고객 접점/불만 관리(VOC), 그리고 **사내 KMS(Confluence) 및 ERP 연동**까지 아우르는 **통합 B2B Enterprise Ecosystem Hub**로 진화하고 있습니다.

```
+-----------------------------------------------------------------------------------+
|                        Relio CRM Strategic Evolution                              |
|                                                                                   |
|  [ Sales Execution ]  ==>  [ Touchpoint & VOC ]  ==>  [ Confluence & ERP Hub ]   |
|   - Customer 360            - Omnichannel Timeline     - Confluence Knowledge Sync|
|   - Sales Playbook          - VOC Lifecycle (Sev 1~4)  - ERP Order-to-Cash         |
|   - Forecast Waterfall      - Churn Risk Interlock     - Credit Limit & AR Check  |
+-----------------------------------------------------------------------------------+
```

### 1.1 4대 제품 개발 원칙
1. **Air-Gap First Architecture**: 모든 연동 어댑터 및 신규 모듈은 외부 인터넷 통신 없이 사내망(Intranet) 환경 내에서 완결 실행되어야 합니다.
2. **End-to-End Enterprise System Interoperability**: Confluence(사내 지식) 및 ERP(SAP, Oracle, 더존 등) 데이터 흐름을 Customer 360 단일 뷰로 완전 통합합니다.
3. **Zero Maintenance Overhead**: 환경변수는 최소화(3+1개)하며 DB 스키마 마이그레이션 및 ERP API 매핑 정책 갱신은 무중단 처리합니다.
4. **AI Native & MCP Protocol Compliance**: 영업/이슈/지식 연동 데이터를 MCP Server 표준 규격(2025-11-25)으로 제공하여 사내 AI Agent의 자율 분석을 지원합니다.

---

## 2. 버전별 히스토리 및 주요 달성 성과 (v1.0.0 ~ v1.6.0)

```
+-----------------------------------------------------------------------------------+
|                           Relio Milestone Achievements                            |
|                                                                                   |
|  [ v1.3.0 ]  -->  [ v1.4.0 ]  -->  [ v1.5.0 ]  -->  [ v1.6.0 (Current) ]        |
|  Customer 360     Sales Playbook    Relationship Map    Data Quality Center       |
|  Personal Keys    Exit Criteria     Account Plan        Configuration Bundle Diff |
|  Keycloak OIDC    Forecast Snap     MCP 13 Tools        ENCRYPTION_KEY Persistence|
+-----------------------------------------------------------------------------------+
```

- **v1.3.0**: Customer 360 Core, Personal API Key (HMAC Digest), Keycloak OIDC SSO 연동.
- **v1.4.0**: Stage별 Sales Playbook, Exit Criteria 단계 제어(`OFF`/`WARNING`/`BLOCK`), Daily Forecast Snapshot & Waterfall 차트.
- **v1.5.0**: Relationship Map (의사결정권자 그래프), Strategic Account Plan & White Space Matrix, Opportunity Team, Sales/Relationship Intelligence MCP 13종.
- **v1.6.0 (현재)**:
  - `ENCRYPTION_KEY` 기반 Master Key 영속성 강화 (Volume 독립성).
  - Operations Command Center (자동 진단 7종 & 우선 조치 항목).
  - Data Quality Center (완성도 0~100점 점수화 및 5대 이상 징후 진단).
  - Configuration Bundle Export / Diff Engine / 비파괴 Upsert.
  - Support Bundle 마스킹 및 Audit Trail 검색 강화.

---

## 3. 향후 로드맵 (v1.7.0 ~ v2.0.0 Plan)

### 3.1 Q4 2026: v1.7.0 Plan - Customer Touchpoint & VOC Issue Management Module

영업 관리 중심의 CRM을 넘어, **고객과의 모든 접점(Touchpoint)을 기록하고 불만/이슈를 원스톱으로 관리하는 4대 신규 엔터프라이즈 모듈**을 도입합니다.

```
+-----------------------------------------------------------------------------------+
|               v1.7.0 Customer Touchpoint & Issue Management Architecture          |
|                                                                                   |
|  +-----------------------------------------------------------------------------+  |
|  |  1. Omnichannel Touchpoint Log & Single Timeline                             |  |
|  |     - 영업 활동 + 기술지원 요청 + 서비스 장애 + 고객 피드백 단일 타임라인 통합  |  |
|  +-----------------------------------------------------------------------------+  |
|  |  2. VOC & Complaint Lifecycle Engine                                        |  |
|  |     - 이슈 접수 -> SLA 심각도(Sev 1~4) 평가 -> 담당자 자동 배정 -> 조치 및 검증 |  |
|  +-----------------------------------------------------------------------------+  |
|  |  3. Churn Risk Interlock & Escalation Workflow                              |  |
|  |     - 미해결 이슈 발생 시 Customer 360 Churn Risk Score(0~100) 자동 상향        |  |
|  |     - 영업대표 및 팀장에 비상 Escalation 알림 및 Renewal Forecast 즉시 연동    |  |
|  +-----------------------------------------------------------------------------+  |
|  |  4. Customer Service MCP Intelligence Tools (3종 추가)                       |  |
|  |     - `get_customer_issues`, `explain_churn_risk`, `recommend_issue_resolution`|  |
|  +-----------------------------------------------------------------------------+  |
+-----------------------------------------------------------------------------------+
```

1. **Omnichannel Touchpoint Log & Integrated Timeline (옴니채널 접점 타임라인)**:
   - 미팅, 통화, 이메일 등 기존 영업 활동 외에도 기술지원 요청, 서비스 이슈, 정기 만족도 조사 등 고객사와의 모든 접점을 Customer 360 단일 타임라인에 통합 기록합니다.
2. **VOC & Complaint Lifecycle Engine (불만 및 이슈 생애주기 관리)**:
   - 불만 및 이슈 접수 -> SLA 심각도 레벨(Sev 1: Critical ~ Sev 4: Minor) 자동 분류 -> 전담 담당자 자동 할당 -> 원인 파악 및 조치 -> 고객 만족도(CSAT) 검증의 5단계 워크플로우 지원.
3. **Customer Churn Risk & Sales Interlock (이탈 위험 및 영업 연동)**:
   - 미해결 불만이나 SLA 초과 이슈 발생 시 **Churn Risk Score (이탈 위험 점수, 0~100점)**를 자동 상향 조정하고 Renewal Pipeline에 위험 경고를 노출합니다.
4. **Customer Service Intelligence MCP Tools (AI Agent 전용 도구 3종 추가)**:
   - `get_customer_issues`, `explain_churn_risk`, `recommend_issue_resolution` 도구를 통해 사내 AI Agent의 이슈 자율 분석 지원.

---

### 3.2 Q1 2027: v1.8.0 Plan - Enterprise System Integration Hub (Confluence & ERP 연동)

사내 지식 관리 솔루션(Confluence, Notion 등) 및 엔터프라이즈 ERP(SAP, Oracle, 더존 등)와의 양방향 커넥터를 도입하여 **기업 데이터 파이프라인의 수직 통합**을 달성합니다.

```
+-----------------------------------------------------------------------------------+
|               v1.8.0 Enterprise System Integration Hub Architecture               |
|                                                                                   |
|   +-----------------------+     REST / Webhook / gRPC    +---------------------+  |
|   |  Confluence / Notion  | <--------------------------> |  Relio CRM Hub      |  |
|   |  (Proposal & Wiki)    |                              |  (Customer 360)     |  |
|   +-----------------------+                              +----------+----------+  |
|                                                                     |             |
|   +-----------------------+      RFC / oData / API                  |             |
|   |  SAP / Oracle / ERP   | <---------------------------------------+             |
|   |  (Order & Credit AR)  |                                                       |
|   +-----------------------+                                                       |
+-----------------------------------------------------------------------------------+
```

1. **Confluence & Internal KMS Sync Gateway (사내 지식 관리 연동)**:
   - **사내 위키 & 제안서 실시간 연동**: Customer 360, Strategic Account Plan, Sales Playbook 문서가 사내 Confluence 공간(Space)과 양방향 자동 동기화됩니다.
   - **AI Agent 사내 지식 학습 지원**: Confluence 내 제안서 템플릿, 제품 기술 스펙, 고객 성공 사례, 불만 조치 가이드를 수집하여 Relio MCP Server 및 Local RAG 엔진에 전달, AI Agent가 맞춤형 제안서와 불만 처리 답변을 자동 작성하도록 지원합니다.
2. **Enterprise ERP Financial & Order Connector (SAP / Oracle / 더존 / 영림원 ERP 연동)**:
   - **거래처 마스터 자동 동기화**: ERP의 법인/사업자 정보 및 거래처 코드와 CRM Customer 360 간 실시간 매핑.
   - **Order-to-Cash (계약-주문-매출 전표) 연동**: Relio에서 계약 승인 완료 시 ERP 영업 주문(Sales Order) 및 세금계산서/매출 전표가 재입력 없이 자동 연동 생성.
   - **미수금 및 여신 한도 (Credit Limit & AR Interlock)**: ERP의 수금 잔액, 미수금, 여신 한도 초과 여부를 CRM Customer 360 및 승인 워크플로우에 연동하여 부실 채권 위험을 사전에 방지합니다.
3. **Territory & Commission Engine**:
   - 지역/산업군별 영업 구역 자동 할당 및 신규 수주/고객 유지율(Retention Rate) 기반 인센티브 계산 엔진 제공.

---

### 3.3 Q2 2027: v2.0.0 Plan - Relio Enterprise Distributed Hub & Spoke
- **Multi-Region Distributed Hub & Spoke Synchronization**: 본사 및 계열사/지사 간 사내망 분산 Relio 인스턴스의 영업, VOC, ERP 연동 데이터 안전 동기화.
- **High-Availability Postgres Cluster Manager**: PG Auto-Failover 및 Read-Replica 로드 밸런싱 내장.
