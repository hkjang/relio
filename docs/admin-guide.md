# Relio 엔터프라이즈 관리자 및 운영 매뉴얼 (Administrator Guide)

- **문서 버전**: v1.6.0  
- **최종 수정일**: 2026년 8월 11일  
- **대상**: System Administrator, DevOps Engineer, Database Administrator (DBA), Infrastructure Lead  
- **문서 개요**: Relio 단일 컨테이너 배포, 필수 환경변수 부트스트랩, Command Center 진단, Data Quality Center, Configuration Bundle 관리, Audit Log 운영 및 백업 절차  

---

## 1. 개요 및 빠른 설치 (Quick Start & Deployment)

Relio는 별도의 런타임 설치 없이 단일 Docker Image와 PostgreSQL 데이터베이스만으로 동작합니다.

### 1.1 개발 및 테스트 실행 (Docker Compose)
```bash
# Repository 클론 후 개발 환경 실행
docker compose up --build
```
- **접속 URL**: `http://localhost:8080`
- **초기 개발 계정**: ID `admin` / Password `ChangeMe-Relio-2026`
- **최초 로그인**: 초기 로그인 성공 즉시 신규 비밀번호 변경 창으로 강제 이동됩니다.

### 1.2 엔터프라이즈 프로덕션 배포 명령 (Docker Run)
```bash
# 1. Master Key 봉인을 위한 ENCRYPTION_KEY 생성 (최초 1회 실행 후 Vault/비밀번호 관리 도구 보관)
ENCRYPTION_KEY="$(openssl rand -hex 32)"

# 2. Relio 컨테이너 실행
docker run -d \
  --name relio \
  -p 8080:8080 \
  -e POSTGRES_DSN="postgres://relio:Secr3tPass123@10.10.50.5:5432/relio?sslmode=disable" \
  -e BOOTSTRAP_ADMIN="admin" \
  -e BOOTSTRAP_ADMIN_PASSWORD="StrongSuperAdminPassword2026!" \
  -e ENCRYPTION_KEY="$ENCRYPTION_KEY" \
  -v relio-data:/var/lib/relio \
  relio:v1.6.0
```

---

## 2. 필수 환경변수 및 자격증명 영속성 명세 (Environment Configuration)

Relio는 오직 **3개의 필수 환경변수**와 **1개의 선택 환경변수**만 사용합니다.

### 2.1 환경변수 명세 표
| 환경변수명 | 구별 | 설명 | 예시 |
|---|---|---|---|
| `POSTGRES_DSN` | **필수** | PostgreSQL 데이터베이스 접속 DSN | `postgres://user:pass@db:5432/relio` |
| `BOOTSTRAP_ADMIN` | **필수** | 최초 기동 시 생성되는 비상 수퍼관리자 ID | `admin` |
| `BOOTSTRAP_ADMIN_PASSWORD` | **필수** | 비상 수퍼관리자 초기 비밀번호 (최소 12자) | `ComplexPasswd123!` |
| `ENCRYPTION_KEY` | 선택 | Master Key Envelope 봉인 키 (Volume 독립성 제공) | `64자리 16진수 hex / Base64 / 32자+` |

### 2.2 `ENCRYPTION_KEY`와 볼륨(Volume) 종속성 완화
- **`ENCRYPTION_KEY` 미설정 시**:
  Master Key가 `/var/lib/relio/secrets/master.key` 파일로 관리됩니다. 컨테이너 교체는 가능하나 `relio-data` 볼륨이 삭제되면 기존 SSO Client Secret 및 API Key를 복구할 수 없습니다.
- **`ENCRYPTION_KEY` 설정 시 (권장)**:
  볼륨이 재생성되거나 새 서버로 이관되더라도 동일한 `ENCRYPTION_KEY` 환경변수만 주입하면 기존 DB의 암호화 데이터 및 자격증명이 완전하게 온전하게 유지됩니다.

---

## 3. Operations Command Center (`/admin/operations`)

운영 관리자는 `/admin/operations` 콘솔에서 시스템 전반의 상태를 실시간으로 진단하고 조치할 수 있습니다.

```
+-----------------------------------------------------------------------------------+
|                        Admin Operations Command Center                            |
|                                                                                   |
|  [ Readiness Score: 100% ]    [ System Health: Healthy ]   [ Schema: v1.6.0 ]     |
|                                                                                   |
|  +-----------------------------------------------------------------------------+  |
|  |  Operational Diagnostics (진단 7종)                                         |  |
|  |   1. PostgreSQL Connection & Pool Status                [ PASS ]            |  |
|  |   2. Database Schema Migration Integrity                  [ PASS ]            |  |
|  |   3. Envelope Master Key & ENCRYPTION_KEY Mode            [ PASS (Env Key) ]  |  |
|  |   4. Storage Directory (/var/lib/relio) Writable          [ PASS ]            |  |
|  |   5. OIDC SSO Issuer & JWKS Health                         [ PASS ]            |  |
|  |   6. REST API & MCP Adapter Readiness                      [ PASS ]            |  |
|  |   7. Background Job Runner Loop Status                     [ PASS ]            |  |
|  +-----------------------------------------------------------------------------+  |
|                                                                                   |
|  +-----------------------------------------------------------------------------+  |
|  |  Prioritized Action Items (우선 조치 권고사항)                              |  |
|  |   - [INFO] ENCRYPTION_KEY environment variable is active.                     |  |
|  |   - [INFO] Keycloak OIDC Provider is configured and active.                   |  |
|  +-----------------------------------------------------------------------------+  |
+-----------------------------------------------------------------------------------+
```

### 3.1 7대 자동 진단 항목
1. **PostgreSQL Connection**: DB 연결 응답 시간, Active Connection Pool 수치 검사.
2. **Schema Migration**: 최신 마이그레이션 적용 상태 및 버전 무결성.
3. **Master Key Protection**: Envelope Key 보호 모드 (`ENCRYPTION_KEY` 사용 여부) 검사.
4. **Storage Permissions**: `/var/lib/relio` 데이터 디렉토리 쓰기 권한.
5. **OIDC SSO Endpoint**: Issuer URL Discovery 및 Discovery Endpoints 응답 검사.
6. **API / MCP Endpoint**: REST API 및 MCP `/mcp` 라우터 준비 상태.
7. **Background Job Loop**: Daily Snapshot 및 Expire Runner의 작동 여부.

---

## 4. Data Quality Center (`/admin/data-quality`)

고객, 담당자, 영업기회 데이터의 완전성을 0~100점 점수 및 이상 징후 표본으로 측정합니다.

### 4.1 5대 데이터 이슈 진단 및 개선 가이드
1. **Unassigned Customers (담당자 미지정 고객)**: 담당 영업대표가 할당되지 않아 관리 공백이 발생한 고객.
2. **Duplicate Customer Candidates (중복 의심 고객)**: 동일 사업자번호 또는 상호 유사도가 높은 고객 표본.
3. **Stale Accounts (30일 이상 미접촉 고객)**: 최근 30일간 미팅, 통화 등 영업 활동 기록이 없는 고객.
4. **Missing Next Actions (다음 행동 미설정 Deal)**: Open 상태의 Opportunity 중 `Next Action` 및 `Next Action Date`가 비어있는 건.
5. **Missing Decision Makers (의사결정자 미지정 Deal)**: Opportunity Relationship Map 상에 Decision Maker 역할이 지정되지 않은 건.

---

## 5. Configuration Bundle 관리 (`/admin/system/config-bundle`)

Relio는 운영 환경 간(예: Staging -> Production) 시스템 설정을 안전하게 이전할 수 있는 **Configuration Bundle**을 지원합니다.

### 5.1 Export 및 Safety Non-Destructive Upsert
- **Export 범위**: Pipeline Stages, Stage Playbooks, Exit Criteria, Sales Coaching Rules, Approval Policies, OIDC Issuer Configuration (비밀번호 및 Business Data 제외).
- **Import & Diff Engine**:
  1. JSON 파일 업로드 시 현재 환경과의 차이점을 `CREATE`(신규 생성), `UPDATE`(변경), `NO_CHANGE`(동일) 상태로 표시합니다.
  2. 관리자가 Diff를 확인한 뒤 **Apply**를 누르면 비파괴 방식(Non-Destructive Upsert)으로 덮어쓰기 적용되며 기존 업무 데이터는 훼손되지 않습니다.

---

## 6. Audit Trail & Support Bundle

### 6.1 Audit Log 검색 (`/admin/audit`)
- **검색 조건**: Channel(WEB, REST, MCP, ADMIN), Resource(customer, opportunity, oidc, user), Action, Actor Name, Request ID.
- **Diff Viewer**: 변경 전(`before_data`)과 변경 후(`after_data`) JSON 구조체를 시각적으로 비교할 수 있습니다.

### 6.2 Support Bundle 다운로드 (`/admin/operations/support-bundle`)
- 기술 지원이나 문제 해결을 위해 시스템 진단 정보를 단일 JSON 파일로 Export합니다.
- **자동 마스킹**: 비밀번호, SSO Secret, Personal Key Digest, PII 데이터는 자동으로 마스킹 처리됩니다.

---

## 7. 백업 및 재해 복구 (Backup & Disaster Recovery)

### 7.1 데이터베이스 백업 스크립트 예시
```bash
#!/bin/bash
# Relio PostgreSQL 백업 스크립트
BACKUP_DIR="/backup/relio"
DATE=$(date +%Y%m%d_%H%M%S)
mkdir -p "$BACKUP_DIR"

pg_dump -h 10.10.50.5 -U relio -d relio -F c -b -v -f "$BACKUP_DIR/relio_db_$DATE.dump"
echo "Relio Backup completed: relio_db_$DATE.dump"
```

### 7.2 재해 복구 (Recovery) 절차
1. 동일한 `ENCRYPTION_KEY` 환경변수를 사용하여 신규 Relio 컨테이너를 준비합니다.
2. PostgreSQL DB를 복원합니다: `pg_restore -h <db-host> -U relio -d relio relio_db_<date>.dump`.
3. Relio 컨테이너를 기동합니다.
