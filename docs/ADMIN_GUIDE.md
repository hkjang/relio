# Relio 엔터프라이즈 관리자 가이드 (Admin & Operational Guide)

- **문서 버전**: v1.0.0-ENTERPRISE  
- **대상**: 시스템 관리자, Security/DevOps 엔지니어, CISO  
- **문서 개요**: Relio 3대 환경변수 부트스트랩, Keycloak OIDC SSO, RBAC & Data Scope, HMAC Key Digest 관리 및 감사 로그 운영  

---

## 1. 시스템 부트스트랩 (Bootstrap Environment Variables)

Relio 컨테이너 프로세스는 오직 **3개의 필수 환경변수**만으로 최소 인프라 구축을 완료합니다.

```bash
# Docker run 환경변수 예시
POSTGRES_DSN=postgres://relio:Secr3tPass@10.10.50.5:5432/relio?sslmode=disable
BOOTSTRAP_ADMIN=admin
BOOTSTRAP_ADMIN_PASSWORD=SuperSecretAdminPassword123!
```

> **볼륨 백업 (Volume Backup Notice)**:  
> `/var/lib/relio` 볼륨은 정기 백업 대상입니다. 이 공간에는 Instance Master Key, 업로드된 파일 및 임시 데이터가 보관되며, 컨테이너 교체 시에도 유지되어야 AES-256-GCM 복호화가 가능합니다.

---

## 2. Keycloak OIDC SSO 및 Break Glass 관리자

- **Break Glass 계정**: `BOOTSTRAP_ADMIN` 계정은 삭제되지 않는 비상 계정으로 OIDC 장애 발생 시 복구 목적으로 사용됩니다.
- **Keycloak OIDC 설정**: `관리자 콘솔 ➔ 시스템 ➔ OIDC` 메뉴에서 Issuer URL, Client ID, Client Secret 등록 및 Valid Redirect URI (`https://relio.internal/api/v1/auth/oidc/callback`) 검증.

---

## 3. Personal Key 보안 & HMAC Digest 무중단 회전

- **HMAC Digest 저장**: 원문 키는 절대로 저장되지 않으며 HMAC Digest 상태로만 PostgreSQL에 보존됩니다.
- **7일 Grace Period**: 사용자가 키를 회전(Rotation)할 때 이전 키가 7일간 유효하게 유지되는 무중단 유예기간 체계를 지원합니다.

---

## 4. 감사 로그 (Audit Trail) & MCP Allowlist

- **감사 로그 (Audit Log)**: OIDC 설정 변경, 파이프라인 stage 수정, 승인 정책 변경, 관리자 권한 할당 등 모든 조작이 변경 전후 값 및 IP 정보와 함께 감사 레코드에 영구 저장됩니다.
- **MCP Tool Allowlist**: 관리자 대시보드에서 AI 에이전트에 노출할 MCP Tool 목록, Rate Limiting 및 Origin 요청을 동적으로 승인/제어합니다.
