# Relio 타겟 사용자군 분석 보고서 (Target User Groups Analysis)

- **문서 버전**: v1.0.0-ENTERPRISE  
- **작성일자**: 2026년 8월 9일  
- **문서 분류**: 페르소나 및 사용자군 가치 분석 보고서 (Target User Groups Analysis)  

---

## 1. 개요 및 사용자 분류 체계

Relio 플랫폼은 사내 5대 핵심 직군(C-Level 경영진, 영업본부장/팀장, 영업 담당자, SRE/DevOps, CISO/보안팀)의 요구사항과 페인 포인트를 완벽하게 충족합니다.

```
==================================================================================================
                           [Relio 5대 핵심 사용자 직군 및 가치]
==================================================================================================
 [1. C-Level 경영진]     ➔ CRM SaaS 구독 수수료 TCO 70% 절감 및 사내 영업 데이터 주권
 [2. 영업본부장 / 팀장]  ➔ Customer 360, Deal Health 스코어링 및 Forecast 예측 가시화
 [3. 영업 담당자]        ➔ /app 파이프라인 관리, 중복 레코드 병합 및 영업활동 이력 자동화
 [4. SRE / DevOps]       ➔ 단일 non-root Docker 컨테이너 및 3대 환경변수 최소 운용
 [5. CISO / 보안팀]       ➔ 100% 사내 오프라인망(Air-Gapped) 구동, Keycloak OIDC & HMAC Key
==================================================================================================
```

---

## 2. 페르소나별 상세 시나리오 및 As-Is vs To-Be 대조 분석

### 2.1 C-Level 경영진 및 CFO
- **As-Is**: Salesforce 등 외산 CRM SaaS의 유저당 높은 과금 체계와 사내 B2B 기밀 영업 데이터의 외부 클라우드 전송 위험.
- **To-Be (Relio)**: 단일 Docker 패키지로 사내 온프레미스 인프라에 배포하여 수수료 70% 절감 및 100% 원천 데이터 소유권 확보.

### 2.2 영업본부장 & 영업 팀장 (Sales Executive)
- **As-Is**: 영업 담당자별 파이프라인 진척도 파악의 불확실성 및 매출 Forecast 오차.
- **To-Be (Relio)**: Deal Health 건강도 점수 자동 시각화 및 스테이지별 Forecast 이행률 분석.

### 2.3 영업 담당자 (Sales Representative)
- **As-Is**: 복잡하고 느린 CRM UI 및 중복 데이터 등록으로 인한 영업활동 기록 지연.
- **To-Be (Relio)**: 직관적인 `/app` 파이프라인 뷰, 중복 고객 자동 제안·병합 및 AI MCP 연동.

### 2.4 SRE / DevOps 엔지니어
- **As-Is**: Redis, ElasticSearch 등 복잡한 멀티 컨테이너 관리로 인한 유지보수 부담.
- **To-Be (Relio)**: 단 3개의 필수 환경변수와 PostgreSQL 1개만으로 동작하는 에어갭 단일 Docker 이미지.

### 2.5 CISO 및 보안팀 (Security Officer)
- **As-Is**: Personal Key 원문 보관 및 AI 에이전트의 직접 DB접근으로 인한 기밀 유출 위험.
- **To-Be (Relio)**: HMAC Digest 저장, 7일 Grace Period, Break Glass 비상 계정 및 Audit Trail 연동.

---

## 3. 결론

Relio 플랫폼은 경영진의 비용 절감 및 데이터 주권 확보 요구부터 영업 리더의 파이프라인 예측 정확도 향상, 담당자의 편의성 및 보안팀의 에어갭 컴플라이언스 준수를 완벽하게 보장합니다.
