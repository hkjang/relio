<p align="center">
  <img src="docs/favicon.svg" alt="Relio Logo" width="90"><br><br>
  <h1 align="center">Relio</h1>
</p>

<p align="center">
  <strong>사람을 위한 CRM, 시스템을 위한 API, AI Agent를 위한 MCP</strong><br>
  단일 Docker 컨테이너 기반 사내 에어갭 B2B CRM 및 영업관리 플랫폼.
</p>

<p align="center">
  <a href="https://hkjang.github.io/relio/">🇰🇷 홍보 페이지</a> · <a href="https://hkjang.github.io/relio/index_en.html">🇺🇸 English Page</a> · <a href="https://github.com/sponsors/hkjang">💖 Sponsor</a>
</p>

---

Relio는 인터넷이 차단된 기업 환경에서 단일 Docker Image로 운영하는 B2B CRM 및 영업관리 플랫폼입니다. React 화면, Go API, MCP Server, DB Migration, OpenAPI 문서와 정적 자산을 한 이미지에 포함하며 PostgreSQL 외에 Redis나 별도 런타임을 요구하지 않습니다.

## 핵심 특성

- 애플리케이션 환경변수는 정확히 `POSTGRES_DSN`, `BOOTSTRAP_ADMIN`, `BOOTSTRAP_ADMIN_PASSWORD` 세 개입니다.
- `/app`, `/me`, `/admin`을 분리하고 관리자는 프로필 메뉴를 통해 Admin Console로 이동합니다.
- Bootstrap Admin은 삭제되지 않는 Break Glass 계정이며 최초 생성 후 환경변수로 덮어쓰지 않습니다.
- Keycloak OIDC는 관리자 화면에서 Issuer, Client ID, Client Secret을 저장하고 Discovery, TLS, JWKS, Callback을 검사합니다.
- Function Permission, Data Scope, Personal Key Scope의 교집합을 Web, REST, MCP, Dashboard에 동일하게 적용합니다.
- Customer 360, 중복 탐지·병합, 설명 가능한 Deal Health, Deal Inspection과 팀장 Sales Coaching을 제공합니다.
- Customer 360의 Relationship Map으로 Decision Maker, Champion, 영향력·지지 성향과 담당자 간 연결 관계를 시각화합니다.
- Strategic Account Plan에서 고객 목표, 경쟁사, 위험, 연간 매출 목표와 White Space Cross-sell 기회를 관리합니다.
- Opportunity Team은 Presales, Consultant, Manager, Legal 등 협업 역할을 기록하되 사용자의 Data Scope를 확대하지 않습니다.
- 관리자가 Stage별 Sales Playbook과 Exit Criteria(`OFF`/`WARNING`/`BLOCK`)를 설정하며 영업 담당자는 Opportunity에서 실행 상태를 관리합니다.
- 일별 Forecast Snapshot, Waterfall, Manager Override로 Forecast 변화와 판단 근거를 분리해 기록합니다.
- Raw Personal Key는 저장하지 않습니다. HMAC Digest만 PostgreSQL에 보관하며 사용자 주도 Rotation과 Grace Period를 지원합니다.
- 활성 승인 정책이 없으면 승인 메뉴, 버튼, Status가 나타나지 않습니다. 정책이 있을 때 해당 Entity에만 팀장 검토가 적용됩니다.
- MCP는 `/mcp`의 Streamable HTTP 어댑터이며 CRM Domain Service와 분리되어 있고 관리자 Tool Allowlist·Origin·Rate 정책을 적용합니다.
- 로그인 화면과 프로필 Context Menu에서 Version, Commit을 확인할 수 있습니다.
- 런타임 CDN, 외부 Font, Analytics, Telemetry, License Check, Package Download가 없습니다.

## 빠른 시작

개발용 PostgreSQL과 Relio를 함께 실행합니다.

```bash
docker compose up --build
```

브라우저에서 `http://localhost:8080`을 열고 다음 개발용 계정으로 로그인합니다.

```text
ID: admin
Password: ChangeMe-Relio-2026
```

최초 로그인 직후 비밀번호 변경이 강제됩니다. 운영 환경에서는 반드시 별도의 강한 초기 비밀번호를 사용하세요.

## 운영 실행

Relio Container에 전달하는 환경변수는 세 개뿐입니다.

```bash
docker run -d \
  --name relio \
  -p 8080:8080 \
  -e POSTGRES_DSN="postgres://relio:password@postgres:5432/relio" \
  -e BOOTSTRAP_ADMIN="admin" \
  -e BOOTSTRAP_ADMIN_PASSWORD="ChangeMe-To-A-Strong-Password" \
  -v relio-data:/var/lib/relio \
  relio:v1.2.0
```

`relio-data`에는 Instance Master Key, 업로드 파일과 Export 임시 데이터가 저장됩니다. Container를 교체하거나 Rollback해도 이 Volume을 유지해야 암호화된 설정과 Personal Key Digest 체계가 유지됩니다.

### 오프라인 Release 설치

GitHub Release에서 파일 하나만 반입합니다.

```bash
gunzip -c relio-v1.2.0.tar.gz | docker load
docker image inspect relio:v1.2.0
```

SHA-256은 Release 본문에 기록되며 별도 Checksum Asset은 배포하지 않습니다.

## 화면 영역

| 영역 | 경로 | 목적 |
|---|---|---|
| CRM | `/app/*` | Dashboard, Customer 360, Opportunity, Deal Intelligence, Pipeline, Forecast, 계약 |
| 개인화 | `/me/*` | 프로필, 개인 Dashboard, 목표, 일정, 알림, Personal API/MCP Key, 활동 기록, Version |
| 관리자 | `/admin/*` | 시스템, OIDC, 사용자·조직, RBAC, Pipeline, 승인, Key/API/MCP, 보안, Audit, 운영 |

관리자 설정은 PostgreSQL의 Domain 테이블과 `system_settings` Namespace로 저장됩니다. Secret은 Instance Master Key를 이용한 AES-256-GCM으로 암호화하고, 설정 변경 전후 값·사용자·IP·시각을 Audit에 기록합니다.

## API와 MCP

- REST prefix: `/api/v1`
- OpenAPI JSON: `/api/openapi.json`
- 내장 API 문서: `/api/docs`
- MCP Streamable HTTP: `/mcp`
- Version: `/api/v1/system/version`
- Health: `/health`, `/health/live`, `/health/ready`

Personal Key 예시는 `relio_{keyId}_{secret}`입니다.

```http
Authorization: Bearer relio_4f30d2a1b7c9_xxxxxxxxxxxxxxxxx
```

MCP Client는 `Accept: application/json, text/event-stream`과 협상된 `MCP-Protocol-Version`을 전송해야 합니다. 서버는 Origin, 인증 Channel, `mcp:use`, Tool별 Scope, 사용자 Permission, Data Scope를 순서대로 검사합니다. AI Agent에 직접 SQL 권한을 제공하지 않습니다.

Sales Intelligence MCP는 `find_deals_at_risk`, `explain_deal_risk`, `recommend_next_actions`, `get_stage_readiness`, `explain_forecast_change`, `get_sales_coaching_insights`를 제공합니다. v1.2 Relationship Intelligence는 `get_account_brief`, `get_account_relationships`, `get_account_plan`, `find_cross_sell_opportunities`, `build_account_plan`, `get_opportunity_team`, `add_opportunity_member`를 추가합니다. Tool에는 `READ`, `ANALYZE`, `WRITE`, `APPROVAL` Risk Level Annotation이 포함됩니다.

## 개발

필요 도구는 Go 1.24+, Node.js 24+, Docker입니다.

```bash
make test
make build
make docker VERSION=1.2.0
```

검증 항목:

```bash
./scripts/check-env-contract.sh
./scripts/check-static-assets.sh
./scripts/run-offline-container-test.sh relio:v1.2.0
./scripts/run-upgrade-container-test.sh relio:v1.1.0 relio:v1.2.0
```

오프라인 테스트는 Docker internal network에서 PostgreSQL과 Relio를 시작하고 Migration, Bootstrap 로그인, Relationship Graph, Account Plan/Cross-sell, Opportunity Team, Deal Health, Playbook/Stage Gate, Forecast Intelligence, REST Personal Key, MCP Sales/Relationship Intelligence와 외부 정적 자산 부재를 검사합니다.

## Release

SemVer Tag를 push하면 `.github/workflows/release.yml`이 테스트, 이미지 빌드, 오프라인 검증, `docker save`, gzip 압축과 GitHub Release를 수행합니다.

```bash
git tag v1.2.0
git push origin v1.2.0
```

Relio가 Release Asset으로 직접 업로드하는 파일은 다음 하나뿐입니다.

```text
relio-v1.2.0.tar.gz
```

## 설계 문서

- [Architecture](docs/architecture.md)
- [Security Model](docs/security.md)
- [Administrator Guide](docs/admin-guide.md)
- [REST API and MCP](docs/api-mcp.md)

## 라이선스

Apache License 2.0. 자세한 내용은 [LICENSE](LICENSE)를 확인하세요.
