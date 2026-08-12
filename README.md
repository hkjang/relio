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

- 애플리케이션 환경변수는 필수 3개(`POSTGRES_DSN`, `BOOTSTRAP_ADMIN`, `BOOTSTRAP_ADMIN_PASSWORD`)와 선택 1개(`ENCRYPTION_KEY`)뿐입니다.
- `ENCRYPTION_KEY`를 설정하면 Personal Key와 SSO Client Secret이 재기동, 이미지 교체, Volume 재생성 후에도 그대로 유지됩니다.
- `/app`, `/me`, `/admin`을 분리하고 관리자는 프로필 메뉴를 통해 Admin Console로 이동합니다.
- Admin Command Center에서 운영 준비도, 우선 조치, PostgreSQL·Schema·Master Key·Storage·OIDC·API/MCP 진단과 Background Job을 확인합니다.
- Secret을 제외한 Support Bundle과 검색·변경 전후 비교가 가능한 Audit Log를 제공합니다.
- Data Quality Center에서 고객·담당자·Opportunity의 완성도와 중복·미접촉·Next Action·Decision Maker 문제를 0~100 Score와 표본으로 진단합니다.
- Secret과 업무 데이터를 제외한 Configuration Bundle을 Export하고 현재 환경과의 생성·갱신 Diff를 확인한 뒤 비파괴 Upsert로 적용합니다.
- Bootstrap Admin은 삭제되지 않는 Break Glass 계정이며 최초 생성 후 환경변수로 덮어쓰지 않습니다.
- Keycloak OIDC는 관리자 화면에서 Issuer, Client ID, Client Secret을 저장하고 Discovery, TLS, JWKS, Callback을 검사합니다.
- Function Permission, Data Scope, Personal Key Scope의 교집합을 Web, REST, MCP, Dashboard에 동일하게 적용합니다.
- 고객의 목소리(VOC)로 불만·요청·문의·이탈 징후를 접수부터 해결, 원인 분석, 재발 방지, 만족도까지 관리합니다.
- 요청 유형별 응답·해결 SLA를 관리자가 정의하고 심각도에 따라 자동 단축하며 기한 초과를 한 곳에서 판정합니다.
- 기한 초과 요청, 기한 도래 다음 행동, 정체 영업기회, 미착수 갱신, 검토 대기를 하나의 우선순위 큐로 제시합니다.
- 이탈 징후·미해결 불만·미착수 갱신·접점 공백을 합산해 고객 이탈 위험도를 근거와 권장 행동과 함께 표시합니다.
- CRM 데이터에서 Signal을 자동 감지하고 0~100 Risk Score로 정량화하며, 임계값을 넘으면 다음 행동을 추천합니다.
- 모든 Risk 점수는 이를 만든 요인과 배점을 그대로 보여주며, 조건이 해소되면 Signal·Risk·추천이 자동으로 회수됩니다.
- 추천을 수락하면 담당자와 기한이 있는 Task로 전환되어 기존 오늘 할 일 큐에 나타나고, 무시할 때는 사유를 남깁니다.
- 감수한 Risk와 무시한 추천은 규칙 엔진이 되살리지 않습니다. 사람의 판단이 자동 분석보다 우선합니다.
- 화면 용어는 한국어를 기준으로 하며 API는 안정적인 코드를 유지합니다.
- 목록의 모든 행을 Tab으로 이동해 Enter로 열 수 있고, 모달과 Drawer는 포커스를 가두며 Escape로 닫힙니다.
- 자주 쓰는 검색 조건을 이름 붙여 저장하고, 고객·영업기회·요청·계약에 즐겨찾기를 답니다.
- Customer 360, 중복 탐지·병합, 설명 가능한 Deal Health, Deal Inspection과 팀장 Sales Coaching을 제공합니다.
- Customer 360에서 고객 담당자를 등록·수정·삭제하고 역할, 영향력, 우리에 대한 성향과 접점 정보를 함께 관리합니다.
- 담당자를 고르는 모든 자리(관계 연결, 고객 요청 접수)에서 화면을 떠나지 않고 담당자를 새로 등록할 수 있습니다.
- Customer 360의 Relationship Map으로 Decision Maker, Champion, 영향력·지지 성향과 담당자 간 연결 관계를 시각화합니다.
- 고객 삭제는 `customer:delete` 권한을 요구하며 영업기회·계약·견적·요청·매출 이력이 있으면 삭제 대신 비활성으로 전환해 기록을 보존합니다.
- Strategic Account Plan에서 고객 목표, 경쟁사, 위험, 연간 매출 목표와 White Space Cross-sell 기회를 관리합니다.
- Opportunity Team은 Presales, Consultant, Manager, Legal 등 협업 역할을 기록하되 사용자의 Data Scope를 확대하지 않습니다.
- 관리자가 Stage별 Sales Playbook과 Exit Criteria(`OFF`/`WARNING`/`BLOCK`)를 설정하며 영업 담당자는 Opportunity에서 실행 상태를 관리합니다.
- 일별 Forecast Snapshot, Waterfall, Manager Override로 Forecast 변화와 판단 근거를 분리해 기록합니다.
- 거래통화 원금과 생성 시점 고정 환율을 보존하고 KRW 기준금액으로 Dashboard, Pipeline, Forecast와 승인 조건을 일관되게 집계합니다.
- 계약 활성화 시 일시·월·분기·연 Revenue Schedule을 자동 생성하며 일정별 매출 인식과 Renewal 다음 행동을 추적합니다.
- Raw Personal Key는 저장하지 않습니다. HMAC Digest만 PostgreSQL에 보관하며 사용자 주도 Rotation과 Grace Period를 지원합니다.
- 활성 승인 정책이 없으면 승인 메뉴, 버튼, Status가 나타나지 않습니다. 정책이 있을 때 해당 Entity에만 팀장 검토가 적용됩니다.
- MCP는 `/mcp`의 Streamable HTTP 어댑터이며 CRM Domain Service와 분리되어 있고 관리자 Tool Allowlist·Origin·Rate 정책을 적용합니다.
- 로그인 화면과 프로필 Context Menu에서 Version, Commit을 확인할 수 있습니다.
- 기본 상태에서 런타임 CDN, 외부 Font, Analytics, Telemetry, License Check, Package Download가 없습니다.
- 사내 Matomo 등 방문자 분석이 필요하면 관리자가 출처별로 명시적으로 허용할 수 있고, 허용한 출처만 Content Security Policy에 추가됩니다.
- 추적 스크립트는 서버가 생성해 자기 출처에서 제공하므로 관리자가 임의 JavaScript를 주입할 수 없습니다.
- 브라우저가 차단한 요청은 관리자 화면에 출처별로 집계되어 콘솔을 보지 않고도 원인을 파악하고 허용할 수 있습니다.

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

Relio Container에 전달하는 환경변수는 필수 3개와 선택 1개입니다.

```bash
# 최초 1회만 생성하고 비밀번호 관리 도구에 보관하세요.
ENCRYPTION_KEY="$(openssl rand -hex 32)"

docker run -d \
  --name relio \
  -p 8080:8080 \
  -e POSTGRES_DSN="postgres://relio:password@postgres:5432/relio" \
  -e BOOTSTRAP_ADMIN="admin" \
  -e BOOTSTRAP_ADMIN_PASSWORD="ChangeMe-To-A-Strong-Password" \
  -e ENCRYPTION_KEY="$ENCRYPTION_KEY" \
  -v relio-data:/var/lib/relio \
  relio:v1.11.0
```

### 자격증명 영속성 (ENCRYPTION_KEY)

Personal Key Digest와 SSO Client Secret은 Instance Data Key로 보호됩니다. 이 Data Key는 한 번 만들어지면 바뀌지 않고, 무엇으로 봉인할지만 선택합니다.

| 구성 | 재기동 | 이미지 교체 | `relio-data` Volume 재생성 |
| --- | --- | --- | --- |
| `ENCRYPTION_KEY` 설정 | 유지 | 유지 | **유지** |
| 미설정 (`master.key` 파일만) | 유지 | 유지 | 복구 필요 |

`ENCRYPTION_KEY`는 64자리 16진수, Base64로 인코딩한 32바이트, 또는 32자 이상 Passphrase를 받습니다. 값 자체는 저장하지 않고 Data Key를 봉인하는 데만 사용하며 PostgreSQL에는 단방향 지문만 남습니다.

이미 운영 중인 환경에 `ENCRYPTION_KEY`를 추가하면 기존 `relio-data` Volume이 연결된 상태의 첫 기동에서 자동으로 이관됩니다. **기존 API Key와 SSO 설정은 그대로 유지되며 재발급이 필요 없습니다.** 이후에는 Volume 없이도 같은 키만 주면 기동됩니다.

`relio-data`에는 업로드 파일과 Export 임시 데이터가 저장되며, `ENCRYPTION_KEY`를 쓰지 않을 때만 Data Key 파일도 함께 보관합니다.

Relio는 Data Key의 단방향 ID를 PostgreSQL에 등록합니다. SSO Secret 또는 활성 Personal Key가 있는 DB에 다른 키·다른 Volume을 연결하면 새 키를 조용히 만들지 않고 기동을 중단합니다. 로그의 `instance encryption key integrity check failed`를 확인하고 원래 `ENCRYPTION_KEY` 값 또는 같은 복구 시점의 `relio-data`를 다시 연결하세요.

### 오프라인 Release 설치

GitHub Release에서 파일 하나만 반입합니다.

```bash
gunzip -c relio-v1.11.0.tar.gz | docker load
docker image inspect relio:v1.11.0
```

SHA-256은 Release 본문에 기록되며 별도 Checksum Asset은 배포하지 않습니다.

## 화면 영역

| 영역 | 경로 | 목적 |
|---|---|---|
| CRM | `/app/*` | Dashboard, Customer 360, Opportunity, Deal Intelligence, Pipeline, Forecast, 계약 |
| 개인화 | `/me/*` | 프로필, 개인 Dashboard, 목표, 일정, 알림, Personal API/MCP Key, 활동 기록, Version |
| 관리자 | `/admin/*` | 운영 Command Center, Diagnostics, 시스템, OIDC, 사용자·조직, RBAC, 영업 정책, Key/API/MCP, 보안, Audit |

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

Sales Intelligence MCP는 `find_deals_at_risk`, `explain_deal_risk`, `recommend_next_actions`, `get_stage_readiness`, `explain_forecast_change`, `get_sales_coaching_insights`를 제공합니다. CRM Intelligence는 `get_customer_signals`, `get_customer_risks`, `get_deal_insights`, `get_recommendations`, `explain_risk`, `accept_recommendation`, `dismiss_recommendation`을 추가하며 REST와 동일한 권한을 요구합니다. v1.2 Relationship Intelligence는 `get_account_brief`, `get_account_relationships`, `get_account_plan`, `find_cross_sell_opportunities`, `build_account_plan`, `get_opportunity_team`, `add_opportunity_member`를 추가합니다. Tool에는 `READ`, `ANALYZE`, `WRITE`, `APPROVAL` Risk Level Annotation이 포함됩니다.

## 개발

필요 도구는 Go 1.24+, Node.js 24+, Docker입니다.

```bash
make test
make build
make docker VERSION=1.11.0
```

검증 항목:

```bash
./scripts/check-env-contract.sh
./scripts/check-static-assets.sh
./scripts/run-offline-container-test.sh relio:v1.11.0
./scripts/run-upgrade-container-test.sh relio:v1.10.0 relio:v1.11.0
```

오프라인 테스트는 Docker internal network에서 PostgreSQL과 Relio를 시작하고 Migration, Bootstrap 로그인, CRM/영업 기능, REST Personal Key, MCP, Admin Operations, Data Quality, Configuration Bundle 왕복, Support Bundle/Audit 검색과 외부 정적 자산 부재를 검사합니다. Container 교체 후 SSO Secret·Personal Key 유지와 잘못된 Volume의 Fail-Closed 동작도 검증합니다.

## Release

SemVer Tag를 push하면 `.github/workflows/release.yml`이 테스트, 이미지 빌드, 오프라인 검증, `docker save`, gzip 압축과 GitHub Release를 수행합니다.

```bash
git tag v1.11.0
git push origin v1.11.0
```

Relio가 Release Asset으로 직접 업로드하는 파일은 다음 하나뿐입니다.

```text
relio-v1.11.0.tar.gz
```

## 설계 문서

- [Architecture](docs/architecture.md)
- [Security Model](docs/security.md)
- [Administrator Guide](docs/admin-guide.md)
- [REST API and MCP](docs/api-mcp.md)

## 라이선스

Apache License 2.0. 자세한 내용은 [LICENSE](LICENSE)를 확인하세요.
