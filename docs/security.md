# Relio 보안 모델 및 컴플라이언스 명세서 (Security & Compliance Model)

- **문서 버전**: v1.6.0  
- **최종 수정일**: 2026년 8월 11일  
- **대상**: CISO, 보안 감사자, Security Engineer, Compliance Lead  
- **문서 개요**: Relio의 Zero-Trust 보안 모델, HMAC-SHA256 Personal Key 회전, Envelope Cryptography & Master Key 영속성, Keycloak OIDC SSO, 3중 권한 교집합 및 감사 로그 규격  

---

## 1. Zero-Trust 보안 모델 및 핵심 철학

Relio는 사내 폐쇄망(Air-gapped) 환경에 배포되는 B2B CRM이지만, 내부망 침투 시나리오를 대비한 **Zero-Trust 아키텍처**를 엄격하게 채택하고 있습니다.

### 1.1 5대 보안 원칙 (Core Security Principles)
1. **Raw Credential Non-Persistence (원문 자격증명 저장 불가)**:
   사용자의 비밀번호(Argon2id/Bcrypt) 및 Personal API Key는 절대로 원문 형태로 DB나 파일 시스템에 저장되지 않습니다. API Key는 `HMAC-SHA256` Digest만을 보존합니다.
2. **Fail-Closed Envelope Key Integrity (봉인 키 무결성 검증)**:
   DB 내 저장되는 비밀 정보(SSO Client Secret 등)를 암호화하는 Master Key는 단방향 지문(ID)이 DB에 보관됩니다. 만약 키가 일치하지 않거나 불법 복제된 경우 어플리케이션은 즉시 기동을 중단합니다.
3. **Intersection-Based Authorization (3중 권한 교집합 검증)**:
   모든 API 및 MCP 요청은 `Function Permission ∩ Data Scope ∩ Personal Key Scope`의 교집합 범위 내에서만 실행됩니다.
4. **Air-Gap Strict Cleanliness (외연 통신 완전 배제)**:
   런타임 CDN, 외부 웹폰트, 폰트 다운로드, Telemetry, External License Call, Package Repository 연결이 0%로 완전히 차단됩니다.
5. **Immutable Audit & Tamper Evident (감사 이력 불변성)**:
   모든 관리자 액션 및 데이터 변동은 전후 DTO Snapshot, IP, Actor, Timestamp, Request ID와 함께 Audit 테이블에 기록되며 수정/삭제 API를 제공하지 않습니다.

---

## 2. 자격증명 영속성 및 암호화 모델 (Key Management & Envelope Encryption)

```
+-----------------------------------------------------------------------------------+
|                           Master Key Protection Modes                             |
|                                                                                   |
|  Mode A: ENCRYPTION_KEY Provided (Recommended)                                    |
|  +------------------------+      PBKDF2/HKDF      +--------------------------+  |
|  | Env: ENCRYPTION_KEY    | --------------------> | Envelope Master Key      |  |
|  +------------------------+                       +------------+-------------+  |
|                                                                |                  |
|  Mode B: Volume Only (Default)                                 |                  |
|  +------------------------+                            Unseals |                  |
|  | /var/lib/relio/secrets | -----------------------------------+                  |
|  | /master.key File       |                                    v                  |
+----------------------------------------------------------------|------------------+
                                                                 |
                                                                 v
+-----------------------------------------------------------------------------------+
|                            PostgreSQL Integrity Check                             |
|   1. App computes Master Key ID (12-char SHA-256 HMAC Fingerprint).               |
|   2. Query `master_keys` table.                                                   |
|   3. IF DB has active secret AND Key ID mismatch -> FAIL FAST (Shutdown App).     |
|   4. IF OK -> Decrypt SSO Secrets using AES-256-GCM.                              |
+-----------------------------------------------------------------------------------+
```

### 2.1 `ENCRYPTION_KEY` 환경변수와 Volume 영속성
Relio는 DB 내 Secret 데이터(SSO Client Secret, Data Key)를 보호하기 위해 AES-256-GCM 봉인 암호화를 적용합니다.

| 구성 | 컨테이너 재기동 | 이미지 교체 | `relio-data` Volume 재생성 |
|---|---|---|---|
| **`ENCRYPTION_KEY` 설정 (권장)** | 자격증명 유지 | 자격증명 유지 | **자격증명 완벽 유지 (Volume 독립적)** |
| **`ENCRYPTION_KEY` 미설정** | 자격증명 유지 | 자격증명 유지 | Volume 손실 시 복구 불가 |

- **`ENCRYPTION_KEY` 입력 규격**: 64자리 16진수 hex 문자열, Base64 인코딩 32바이트, 또는 32자 이상의 Passphrase.
- **기동 및 무결성 검증 (Fail-Closed)**:
  어플리케이션은 기동 시 Master Key의 단방향 지문(12자)을 계산해 PostgreSQL의 `master_keys` 테이블과 비교합니다. DB에 활성 SSO Secret 또는 API Key Digest가 존재하는데 Key ID가 일치하지 않으면 `instance encryption key integrity check failed` 로그를 남기고 즉시 프로세스를 종료하여 암호화 데이터 훼손 및 오작동을 방지합니다.

---

## 3. Personal API Key 보안 및 무중단 회전 (Grace Period Rotation)

### 3.1 Personal Key 구조
Personal Key는 `relio_{keyId}_{secret}` 형식으로 발급됩니다.
- `keyId`: 12자리 공개 식별자 (DB 검색용)
- `secret`: 32자리 고엔트로피 비밀 값

### 3.2 HMAC-SHA256 Digest 보안
서버는 DB에 Raw Secret을 저장하지 않습니다. 서버가 보유한 HMAC Key와 결합해 `HMAC-SHA256(secret)` Digest만을 보존하므로, DB가 유출되더라도 API Key 원문을 복원할 수 없습니다.

### 3.3 7일 Grace Period 회전 (Dual-Active Rotation)
운영 중인 서비스의 API Key 교체 시 시스템 중단을 방지하기 위해 Dual-Active 회전 매커니즘을 지원합니다.
1. 사용자가 API Key 회전(Rotate)을 요청하면 새 `keyId`와 `secret`이 발급됩니다.
2. 기존 Key는 즉시 파기되지 않고 **7일간의 Grace Period(유예 기간)** 동안 Dual-Active 상태로 유지됩니다.
3. 7일 경과 후 백그라운드 잡(`job.Runner`)이 구버전 Key Digest를 자동으로 무효화합니다.

---

## 4. Keycloak OIDC SSO 및 Break Glass 비상 계정

### 4.1 Keycloak OIDC 연동
- **프로토콜**: OpenID Connect (Authorization Code Flow with PKCE).
- **TLS & Discovery**: Issuer URL 등록 시 Discovery Document (`.well-known/openid-configuration`), TLS Certificate 유효성, JWKS URI 검증을 관리자 콘솔에서 실행합니다.
- **Redirect URI**: `https://<relio-domain>/api/v1/auth/oidc/callback`

### 4.2 Break Glass 계정 (`BOOTSTRAP_ADMIN`)
- **개념**: OIDC SSO 서버 장애, Network isolation, 인증 체계 마비 시 시스템 접근을 보장하는 비상 수퍼관리자 계정입니다.
- **보호 매커니즘**:
  - `BOOTSTRAP_ADMIN` 계정은 시스템 최초 기동 시 DB에 계정이 없을 때만 단 1회 생성됩니다.
  - 최초 생성 후 컨테이너 환경변수(`BOOTSTRAP_ADMIN_PASSWORD`)를 변경하더라도 기존 계정 비밀번호를 덮어쓰지 않습니다.
  - UI 및 API 상에서 삭제가 완전히 금지(Immutable Account)되어 비상 접근권을 상시 유지합니다.

---

## 5. 3중 권한 교집합 검증 모델 (Triple Intersection Authorization)

Relio는 모든 요청에 대해 다음 3개 영역의 **교집합(Intersection)**을 계산하여 엄격하게 통제합니다.

```
       +------------------------------------+
       |   Function Permission              |
       |   (e.g., opportunity:write)        |
       +-----------------+------------------+
                         |
                         v
       +-----------------+------------------+
       |   Data Scope                       |
       |   (SELF / TEAM / ALL)              |
       +-----------------+------------------+
                         |
                         v
       +-----------------+------------------+
       |   Personal Key Scope               |
       |   (e.g., mcp:use, read-only)       |
       +-----------------+------------------+
                         |
                         v
           [ FINAL PERMITTED ACTION ]
```

1. **Function Permission**: 사용자의 역할(Role: Admin, Sales Manager, Sales Rep, Auditor)에 부여된 기능 권한.
2. **Data Scope**:
   - `SELF`: 본인이 Owner인 데이터만 접근 가능.
   - `TEAM`: 본인 소속 부서/팀의 데이터 접근 가능.
   - `ALL`: 전체 조직 데이터 접근 가능.
3. **Personal Key Scope**: REST API 또는 MCP Key 생성 시 사용자가 지정한 제한적 Scope (예: `read-only`, `mcp:use`).

---

## 6. Audit Trail & Support Bundle (감사 및 진단 보안)

### 6.1 Audit Log 기록 항목
모든 관리자 설정 변경, 권한 할당, 승인 처리, Stage Playbook 수정 등은 `audit_logs` 테이블에 무경고 영구 수집됩니다:
- `actor_id` & `actor_name`: 수행 사용자
- `channel`: WEB, REST, MCP, ADMIN
- `action`: 수행한 행위 코드 (예: `OIDC_SETTING_UPDATE`)
- `before_data` & `after_data`: 변경 전후 JSON DTO (비밀 정보는 마스킹 처리)
- `ip` & `user_agent`: 클라이언트 IP 및 User-Agent
- `request_id`: Tracing용 UUID

### 6.2 Support Bundle 마스킹 (Masking Policy)
관리자 콘솔에서 시스템 진단을 위한 **Support Bundle Export** 시, 다음 정보는 자동 마스킹 및 제거 처리되어 제출됩니다:
- `POSTGRES_DSN` 내 비밀번호 문자열
- OIDC `Client Secret`
- API Key Digest 및 Password Hash
- 고객 개인식별정보(PII)

## Content Security Policy와 방문자 분석

기본 정책은 자기 출처만 허용합니다.

```
default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none';
base-uri 'self'; form-action 'self'; script-src 'self'; connect-src 'self';
img-src 'self' data:; report-uri /api/v1/csp-report
```

관리자가 방문자 분석 공급자를 활성화하면 `script-src`, `connect-src`, `img-src`에 **허용한 출처만** 추가됩니다. 정책은 요청 시점에 조립되며 30초 캐시와 변경 즉시 무효화를 사용합니다.

### 관리자 권한이 스크립트 실행 권한이 되지 않도록

추적 스크립트는 관리자가 붙여넣은 JavaScript가 아니라, 검증된 필드로 **서버가 생성**해 `/analytics.js`에서 제공합니다. 따라서 `script-src 'self'`가 로더를 그대로 포함하고, 관리자 계정이 전체 사용자 세션에 대한 임의 코드 실행 수단이 되지 않습니다.

입력값은 헤더와 생성 스크립트에 들어가기 전에 좁은 형태로 검증합니다.

- 출처는 스킴 + 호스트(+ 포트)만 허용하고 경로, 질의, 자격증명, 정책을 끊을 수 있는 문자(공백, `;`, `,`, 따옴표, 개행 등)를 거부합니다. 하위 도메인 와일드카드는 선행 `*.` 한 번만 허용하며, 스크립트 출처에는 와일드카드를 쓸 수 없습니다.
- 스크립트 경로는 `//`와 `..`를 거부해 출처를 벗어나지 못하게 합니다.
- 사이트 ID는 문자열 리터럴을 끝낼 수 없는 문자만 허용하고, 생성 시 다시 이스케이프합니다.
- 태그 속성은 `data-*`만 허용합니다. `src`, `onerror` 같은 속성은 거부합니다.

이 설정은 `analytics:manage` 권한이 필요하며 일반 관리자 권한과 분리됩니다. 모든 변경은 출처 목록과 함께 감사 로그에 기록됩니다.

### 차단된 요청 진단

브라우저 위반 보고를 `/api/v1/csp-report`에서 받습니다. 보고 본문은 신뢰하지 않습니다. 크기를 제한하고 값을 다시 검증하며, 전체 URL을 출처로 축약해 `(directive, origin)` 단위로 집계합니다. 따라서 임의 클라이언트가 표를 키울 수 없습니다.

관리자 화면은 차단된 출처와 발생 횟수를 보여주고, 유효한 출처라면 한 번의 클릭으로 허용 설정을 열 수 있습니다. 사용자 콘솔에만 남던 `violates the following Content Security Policy directive` 오류가 운영 화면에서 원인과 조치로 이어집니다.
