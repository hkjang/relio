# Relio 엔터프라이즈 관리자 가이드 (Admin & Operational Guide)

- **문서 버전**: v1.0.0-ENTERPRISE  
- **작성일자**: 2026년 8월 9일  
- **대상**: 시스템 관리자, Security/DevOps 엔지니어, CISO, 데이터베이스 관리자(DBA)  
- **문서 개요**: Relio 3대 환경변수 부트스트랩, Keycloak OIDC SSO, Break Glass 계정, Volume 유지 보수, HMAC Key Digest 보안 및 Audit Log 운영  

---

## 1. 시스템 부트스트랩 및 필수 환경변수 (Bootstrap Specification)

Relio 컨테이너 프로세스는 정확히 **3개의 애플리케이션 환경변수**만으로 최소 인프라 구축 및 초기 시스템 부트스트랩을 완료합니다.

```bash
# Relio 실행 환경변수 명세
POSTGRES_DSN=postgres://relio:Secr3tPass@10.10.50.5:5432/relio?sslmode=disable
BOOTSTRAP_ADMIN=admin
BOOTSTRAP_ADMIN_PASSWORD=SuperSecretAdminPassword123!
```

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
  -v relio-data:/var/lib/relio \
  relio:v1.0.0
```

### 2.1 볼륨에 저장되는 3대 자산
1. **Instance Master Key**: 비밀 설정 및 시스템 시크릿 암호화(AES-256-GCM)에 사용되는 마스터 키.
2. **업로드 파일 레포지토리**: 영업 관련 첨부파일, 계약서 및 증빙 문서.
3. **Export 임시 데이터**: 대용량 엑셀/CSV 내보내기 시 임시 파싱 버퍼.

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
