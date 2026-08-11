# Security Model

## Authentication

- Local Password: Argon2id, 사용자별 Salt, Raw Password 미저장
- OIDC: Authorization Code + PKCE + State + Nonce, RS256 JWKS 검증
- Browser Session: Random 256-bit Token의 SHA-256 Digest만 저장, HttpOnly/SameSite Cookie, HTTPS일 때 Secure
- Personal Key: Random Secret을 한 번만 표시, Master Key 기반 HMAC-SHA-256 Digest만 저장
- Bootstrap Admin: 최초 한 번 생성, 재기동 환경변수로 수정하지 않음, 별도 Audit

OIDC ID Token은 Issuer, Audience, Expiry, Nonce와 RSA Signature를 검사합니다. 사내 Root CA는 관리자 화면에서 등록하며 Client Secret과 마찬가지로 화면에 다시 노출하지 않습니다.

## Authorization

최종 접근 권한은 사용자 Function Permission, Data Scope, Personal Key Scope/Channel의 교집합입니다. Personal Key에 넓은 Scope를 넣어도 사용자 Role 권한을 넘지 못합니다. MCP의 `approve_request`, `reject_request`는 `approval:approve`를 별도로 요구합니다.

## Browser and HTTP

- Mutating Session 요청은 `X-CSRF-Token` 검증
- CSP, frame-ancestors, X-Content-Type-Options, Referrer-Policy, Permissions-Policy
- JSON Body Size 제한 및 Unknown Field 거부
- Parameter Binding
- Request ID와 표준 Error Envelope
- 로그인 기본 Rate Limit
- 관리자 정책 기반 REST/MCP 분당 Rate Limit
- 변경 요청의 `Idempotency-Key` 결과 재사용 및 충돌 검사

Reverse Proxy가 TLS를 종료하면 `X-Forwarded-Proto: https`를 전달해야 Secure Cookie가 설정됩니다.

## MCP

MCP Adapter는 다음 순서로 요청을 검사합니다.

```text
Origin → Bearer Authentication → MCP Channel → mcp:use
→ Tool Scope → User Permission → Data Scope → Validation → Domain Service → Audit
```

Origin이 있으면 관리자 `mcp.allowed_origins` 또는 Service URL Origin과 일치해야 합니다. Origin이 없는 비브라우저 Agent 요청은 인증을 계속 검사합니다. `mcp.tool_allowlist`가 설정되면 허용 목록 밖 Tool은 목록과 직접 호출 모두에서 차단됩니다. 서버는 Stateless JSON 응답을 사용하며 서버 주도 SSE GET은 405를 반환합니다.

## Persistent Volume

## Instance Data Key와 ENCRYPTION_KEY

Secret 설정, OIDC Client Secret 암호화와 Personal Key HMAC Digest는 모두 하나의 **Instance Data Key**를 사용합니다. 이 Data Key는 설치 시 한 번 생성된 뒤 변하지 않으며, 무엇으로 봉인(Wrap)하는지만 선택합니다. 봉인된 Data Key는 `instance_data_key` 테이블에 AES-256-GCM 암호문으로 보관합니다.

| 봉인 방식 | 저장 위치 | Volume 재생성 시 |
| --- | --- | --- |
| `ENCRYPTION_KEY` 환경변수 | 어디에도 저장하지 않음 (지문만) | 같은 값만 주면 그대로 복구 |
| `master.key` 파일 | `/var/lib/relio/secrets/master.key` | 복구 필요 |

`ENCRYPTION_KEY`는 64자리 16진수 또는 Base64로 인코딩한 32바이트를 그대로 사용하고, 그 밖의 값은 32자 이상일 때만 HKDF-SHA256으로 도출합니다. Key 자체는 로그, Support Bundle, Configuration Bundle, Admin API 어디에도 나타나지 않습니다.

Data Key와 Wrapping Key 모두 Domain-separated SHA-256 지문만 PostgreSQL에 남습니다. 시작 시 다음을 검증합니다.

- Master Key 파일이 32바이트 일반 파일인지 확인하고 Symlink를 거부합니다.
- 봉인된 Data Key를 제시된 Wrapping Key로 실제로 열 수 있는지 확인합니다.
- DB에 등록된 Data Key ID와 열어낸 Data Key ID를 상수 시간으로 비교합니다.
- 등록 전 업그레이드 환경은 기존 OIDC/System Secret 암호문을 실제 복호화한 뒤 Key ID를 등록합니다.
- 활성 Personal Key 또는 암호화 Secret이 있는데 `ENCRYPTION_KEY`도 Key 파일도 없으면 새 Key를 생성하지 않습니다.
- Key 불일치나 암호문 손상은 `instance encryption key recovery required`로 Fail-Closed 처리합니다.

### Wrapping Key 이관과 회전

기존 Volume이 연결된 상태에서 `ENCRYPTION_KEY`를 처음 설정하면, 파일 Key로 Data Key를 연 뒤 새 Key로 다시 봉인합니다. Data Key 자체는 바뀌지 않으므로 **이미 발급된 Personal Key와 저장된 SSO Client Secret은 계속 유효합니다.** 이관과 회전은 `instance_data_key_events`에 기록되며 Key 값은 남기지 않습니다.

반대로 열 수 있는 Key가 하나도 없으면 기동을 중단합니다. 이 Fail-Closed 동작은 잘못된 Volume이나 잘못된 Key로 기동한 상태를 정상으로 오인해 SSO와 모든 Personal Key를 동시에 무효화하는 사고를 방지합니다.

## Persistent Volume Backup

`ENCRYPTION_KEY`를 사용하지 않는 배포에서 `/var/lib/relio` Volume을 잃으면 기존 암호화 Secret을 복호화할 수 없습니다. 이때 Backup은 PostgreSQL과 `relio-data`를 같은 복구 지점으로 관리해야 합니다. Master Key, Personal Key Raw Secret과 Bootstrap Password를 로그에 출력하지 않습니다.

Admin Operations에서는 원본 지문이 아닌 12자리 Data Key ID, Wrapping Key ID, 봉인 방식과 보호 자격증명 건수만 표시합니다.
