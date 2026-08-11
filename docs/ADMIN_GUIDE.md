# Relio 엔터프라이즈 관리자 가이드 (Admin & Operational Guide)

- **문서 버전**: v1.0.0-ENTERPRISE  
- **작성일자**: 2026년 8월 9일  
- **대상**: 시스템 관리자, Security/DevOps 엔지니어, CISO, 데이터베이스 관리자(DBA)  
- **문서 개요**: Relio 3대 환경변수 부트스트랩, Keycloak OIDC SSO, Break Glass 계정, Volume 유지 보수, HMAC Key Digest 보안 및 Audit Log 운영  

---

## 1. 시스템 부트스트랩 및 필수 환경변수 (Bootstrap Specification)

Relio 컨테이너 프로세스는 **3개의 필수 환경변수**만으로 최소 인프라 구축 및 초기 시스템 부트스트랩을 완료합니다. 여기에 자격증명 영속성을 위한 **선택 환경변수 1개**를 더할 수 있습니다.

```bash
# Relio 실행 환경변수 명세
POSTGRES_DSN=postgres://relio:Secr3tPass@10.10.50.5:5432/relio?sslmode=disable
BOOTSTRAP_ADMIN=admin
BOOTSTRAP_ADMIN_PASSWORD=SuperSecretAdminPassword123!

# 선택: 설정하면 Volume을 새로 만들어도 API Key와 SSO Secret이 유지됩니다.
# openssl rand -hex 32 으로 최초 1회 생성한 뒤 비밀번호 관리 도구에 보관하세요.
ENCRYPTION_KEY=3f9c1d...64자리16진수
```

> **ENCRYPTION_KEY를 권장하는 이유**:
> 이 값을 설정하지 않으면 Personal Key Digest와 SSO Client Secret을 여는 열쇠가 `relio-data` Volume 안의 파일에만 존재합니다. Volume이 사라지면 모든 API Key를 재발급하고 SSO Client Secret을 다시 입력해야 합니다. `ENCRYPTION_KEY`를 설정하면 컨테이너와 Volume을 새로 만들어도 같은 값만 주입하면 기존 자격증명이 그대로 살아납니다. 운영 중인 환경에 나중에 추가해도 첫 기동에서 자동으로 이관되며 재발급은 필요 없습니다.

> **부트스트랩 원칙**:  
> `BOOTSTRAP_ADMIN`은 시스템 최초 기동 시 계정이 존재하지 않을 때만 자동 생성되는 **Break Glass 비상 계정**입니다. 한번 생성된 후에는 환경변수를 변경하더라도 기존 계정을 덮어쓰지 않으므로 안심하고 운영할 수 있습니다.

---

## 2. 볼륨 마운트 및 마스터 키 유지 관리 (`/var/lib/relio`)

Relio 컨테이너 실행 시 데이터 볼륨 마운트는 필수적입니다.

```bash
docker run -d \
  --name relio \
  -p 8080:8080 \
  -e POSTGRES_DSN="postgres://relio:password@postgres:5432/relio" \
  -e BOOTSTRAP_ADMIN="admin" \
  -e BOOTSTRAP_ADMIN_PASSWORD="StrongAdminPassword123!" \
  -e ENCRYPTION_KEY="$ENCRYPTION_KEY" \
  -v relio-data:/var/lib/relio \
  relio:v1.6.0
```

### 2.1 볼륨에 저장되는 자산
1. **업로드 파일 레포지토리**: 영업 관련 첨부파일, 계약서 및 증빙 문서.
2. **Export 임시 데이터**: 대용량 엑셀/CSV 내보내기 시 임시 파싱 버퍼.
3. **Instance Data Key 파일**: `ENCRYPTION_KEY`를 사용하지 않을 때만 `secrets/master.key`에 보관됩니다. 환경변수를 사용하면 이 파일 없이도 기동합니다.

### 2.2 자격증명 영속성 점검
운영 Command Center의 **Master Key** 진단이 `WARNING`이면 아직 Volume에 종속된 상태입니다. Diagnostics 화면의 `보호 방식` 항목에서 `ENCRYPTION_KEY (Volume 독립)`으로 표시되는지 확인하세요.

### 2.2 Master Key와 DB 연속성 검증

Relio는 Master Key 원문이 아닌 단방향 12자리 식별자를 관리자 진단에 표시하고 전체 지문을 PostgreSQL에 등록합니다. Container 교체 전후 `/admin/operations`에서 Master Key ID가 동일한지 확인하세요. DB에 SSO Secret 또는 활성 Personal Key가 있는데 `/var/lib/relio`가 비어 있거나 다른 Key를 포함하면 Application은 새 Key를 생성하지 않고 `instance master key recovery required`로 종료됩니다.

이 경우 PostgreSQL과 같은 복구 시점의 `relio-data` Volume을 다시 연결해야 합니다. DB만 복원하거나 Volume만 복원해서는 자격증명 연속성을 보장할 수 없습니다.

---

## 3. Keycloak OIDC SSO & RBAC 데이터 스코프 매핑

### 3.1 Keycloak OIDC 연동 설정
- **관리자 경로**: `/admin/security/oidc`
- **입력 항목**:
  - `Issuer URL`: `https://sso.internal/realms/enterprise`
  - `Client ID`: `relio-crm`
  - `Client Secret`: `••••••••••••••••`
- **Redirect URI 등록**: `https://relio.internal/api/v1/auth/oidc/callback`

### 3.2 RBAC 및 Data Scope 교집합 매핑
Relio는 사용자에게 부여된 **Function Permission**(기능 권한), **Data Scope**(조직/팀 데이터 접근 범위), **Personal Key Scope**의 교집합을 적용하여 REST API 및 MCP 도구 호출 시 완벽한 접근 권한을 적용합니다.

---

## 4. Personal Key 보안 & HMAC Digest 무중단 회전 (Rotation)

- **HMAC Digest 상태 보관**: Raw Personal Key는 DB에 저장되지 않으며, `HMAC-SHA256` Digest 값만 PostgreSQL에 보존됩니다.
- **7일 Grace Period 회전**: 사용자가 키를 회전하면 기존 키는 7일간의 유예기간을 갖고 활성화되어 서비스 중단 없는 키 교체가 가능합니다.

---

## 5. 감사 로그 (Audit Trail) 및 백업 가이드

- **감사 로그 레코드**: OIDC 설정 변경, 파이프라인 stage 수정, 승인 정책 변경, 관리자 권한 할당 등 모든 액션은 사용자 ID, IP 주소, 요청 시각, 변경 전후 JSON 값과 함께 감사 테이블에 영구 보존됩니다.
- **DB 백업 스크립트 예시**:
```bash
pg_dump -U relio -h 10.10.50.5 relio | gzip > /backup/relio_db_$(date +%Y%m%m).sql.gz
```
