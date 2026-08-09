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

`/var/lib/relio` Volume을 잃으면 기존 암호화 Secret을 복호화할 수 없습니다. Backup은 PostgreSQL과 `relio-data`를 같은 복구 지점으로 관리해야 합니다. Master Key, Personal Key Raw Secret과 Bootstrap Password를 로그에 출력하지 않습니다.
