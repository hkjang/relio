# Relio 엔터프라이즈 중장기 기술 로드맵 (Product Roadmap Plan)

- **문서 버전**: v1.0.0 ~ v2.0-VISION  
- **작성일자**: 2026년 8월 9일  
- **문서 분류**: 비즈니스 및 아키텍처 중장기 로드맵 (Strategic Product Roadmap)  

---

## 1. 비전 및 발전 마일스톤 개요

Relio 플랫폼은 에어갭 B2B CRM 수집 및 파이프라인 관리를 시작으로, 사내 AI 데이터 에이전트와 대화형으로 영업 지표를 제어·분석하는 차세대 Autonomous Sales Platform으로 진화합니다.

```
========================================================================================
                          [Relio 단계별 마일스톤 아키텍처]
========================================================================================
 [Phase 1: v1.0.0] (완료) ➔ Customer 360, Deal Health, HMAC Key Digest, Streamable MCP
 [Phase 2: v1.5.0] (진행) ➔ Multi-Currency Enterprise Forecast & Automated Contract Engine
 [Phase 3: v2.0.0] (2026 Q4) ➔ Autonomous Sales Copilot (NL-to-CRM Action MCP 2.0)
 [Phase 4: v3.0.0] (2027)    ➔ Global Multi-Region Air-Gapped Sync & Predictive Churn AI
========================================================================================
```

---

## 2. Phase별 세부 기술 명세

### 2.1 Phase 1: v1.0.0 오프라인 CRM 및 MCP 엔진 구축 (완료)
- **Customer 360 & Deal Health**: 고객사 통합 뷰, 중복 탐지·병합, Deal Health 점수 계산.
- **3개 구역 분리**: `/app`, `/me`, `/admin` 분리 및 Break Glass 비상 계정.
- **Keycloak OIDC & HMAC Key Digest**: PKCE SSO, HMAC Digest 저장 및 7일 유예기간 회전.
- **Streamable HTTP MCP**: Tool Allowlist 및 Scope 교집합 검사 기반 AI 연동.

### 2.2 Phase 2: v1.5.0 계약 파이프라인 & 다중 통화 Forecast (2026 Q3)
- **다중 통화(Multi-Currency) 환율 적용**: 글로벌 B2B 영업을 위한 실시간/고정 환율 파이프라인.
- **계약 및 매출 이행 자동화**: 계약 승인 시 자동 매출 일정 파싱 및 이행 상태 트래킹.

### 2.3 Phase 3: v2.0.0 AI 자율 영업 코파일럿 (2026 Q4)
- **NL-to-CRM Action (MCP 2.0)**: AI 에이전트가 "A사 미팅 결과 기록하고 Opportunity 스테이지를 Negotiation으로 변경해줘" 질의 시 권한 검증 후 자동 업데이트.

---

## 3. 리소스 및 품질 관리 전략

- **100% 테스트 자동화**: Go 백엔드 유닛 테스트, React UI 빌드 및 오프라인 컨테이너 검증 자동화.
- **무중단 마이그레이션**: PostgreSQL Migration 자동 스키마 적용 및 백워드 호환성 보장.
