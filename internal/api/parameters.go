package api

import (
	"fmt"
	"strings"
)

// The document described paths and summaries and nothing else. That left two
// gaps a client cannot close by itself.
//
// The first is a spec violation: OpenAPI requires every `{id}` in a path to have
// a matching path parameter, and none of the ~50 templated paths declared one,
// so a generated client had no argument to put the id in. describeParameters
// derives those from the template itself, which is the only way a new path
// cannot forget one.
//
// The second is the query string. /customers takes a cursor, /opportunities
// sorts by -expectedAmount, limit caps out at 200 and every authenticated GET
// accepts `fields` — none of it was written down, so the only way to learn it
// was to read the Go source. queryParameters records it, and the contract test
// in internal/server compares the table with the handler the router sends each
// path to, in both directions.

// query is one query string key an operation reads.
type query struct {
	name        string
	description string
	kind        string
	deflt       any
	min, max    any
	enum        []string
}

func (q query) parameter() map[string]any {
	schema := map[string]any{"type": q.kind}
	if q.min != nil {
		schema["minimum"] = q.min
	}
	if q.max != nil {
		schema["maximum"] = q.max
	}
	if q.deflt != nil {
		schema["default"] = q.deflt
	}
	if len(q.enum) > 0 {
		schema["enum"] = q.enum
	}
	return map[string]any{"name": q.name, "in": "query", "required": false, "description": q.description, "schema": schema}
}

func text(name, description string) query {
	return query{name: name, description: description, kind: "string"}
}

func choice(name, description string, values ...string) query {
	return query{name: name, description: description, kind: "string", enum: values}
}

// flag is a key the handler compares against the literal "true", so anything
// else — including "1" — reads as false.
func flag(name, description string) query {
	return query{name: name, description: description, kind: "boolean", deflt: false}
}

// number mirrors httpx.IntQuery: a value outside the accepted range falls back
// rather than failing, and the fallback itself may sit outside that range to
// mean "no filter", in which case it is not a schema default. The few keys read
// with httpx.ClampQuery instead say so in their own description.
func number(name, description string, fallback, low, high int) query {
	q := query{name: name, description: description, kind: "integer", min: low, max: high}
	if fallback >= low && fallback <= high {
		q.deflt = fallback
	}
	return q
}

func limit(fallback, high int) query {
	return number("limit", fmt.Sprintf("한 번에 받을 최대 건수입니다. 범위를 벗어나면 %d건이 적용됩니다.", fallback), fallback, 1, high)
}

func cursor() query {
	return text("cursor", "이전 응답의 nextCursor를 그대로 넘겨 다음 페이지를 이어 받습니다. 이 서버가 발급하지 않은 값은 거부됩니다.")
}

func sortBy(values ...string) query {
	return choice("sort", "정렬 기준입니다. 앞의 -는 내림차순을 뜻하며, 목록에 없는 값은 무시되고 최근 수정 순이 적용됩니다.", values...)
}

func versionFilter() query {
	return number("version", "낙관적 잠금 버전입니다. 생략하면 버전 검사를 건너뜁니다.", -1, 0, 1_000_000)
}

func year() query {
	return number("year", "대상 연도입니다. 생략하면 올해를 사용합니다.", 0, 2000, 2200)
}

func months(fallback, high int) query {
	return number("months", "최근 몇 개월을 집계할지 정합니다.", fallback, 1, high)
}

func accountFilters() []query {
	return []query{
		text("accountId", "이 고객의 항목만 반환합니다."),
		choice("entityType", "분석 대상 자원 유형입니다.", "ACCOUNT", "OPPORTUNITY", "VOC", "CONTRACT"),
		text("entityId", "분석 대상 자원 ID입니다."),
	}
}

func voiceFilters(fallbackLimit int) []query {
	return []query{
		text("customerId", "이 고객의 요청만 반환합니다."),
		choice("status", "처리 상태입니다.", "RECEIVED", "IN_REVIEW", "IN_PROGRESS", "PENDING_CUSTOMER", "RESOLVED", "CLOSED", "REJECTED"),
		choice("voiceType", "요청 유형입니다.", "COMPLAINT", "REQUEST", "INQUIRY", "DEFECT", "PRAISE", "CHURN_RISK"),
		choice("severity", "심각도입니다.", "LOW", "NORMAL", "HIGH", "CRITICAL"),
		text("ownerId", "이 담당자에게 배정된 요청만 반환합니다."),
		flag("overdue", "응답 또는 해결 기한을 넘긴 요청만 반환합니다."),
		flag("open", "아직 종결되지 않은 요청만 반환합니다."),
		limit(fallbackLimit, 200),
	}
}

// fieldsParameter is shared because requireAuth applies the projection to every
// authenticated GET before the handler ever runs, not per endpoint.
var fieldsParameter = map[string]any{
	"name": "fields", "in": "query", "required": false,
	"description": "쉼표로 구분한 필드 이름 1~50개입니다. 지정하면 응답 객체(목록이면 items의 각 항목)를 그 필드만으로 줄여 돌려줍니다.",
	"schema":      map[string]any{"type": "string"},
}

var queryParameters = map[string][]query{
	"GET /auth/oidc/callback": {
		text("code", "Keycloak가 돌려준 Authorization Code입니다."),
		text("state", "로그인 시작 시 발급한 state 값입니다."),
		text("error", "Keycloak가 로그인을 거부했을 때의 오류 코드입니다."),
	},
	"GET /search": {text("q", "고객·영업기회·리드를 함께 찾는 검색어입니다."), limit(10, 50)},
	"GET /customers": {
		text("q", "고객명 또는 사업자번호 부분 일치 검색어입니다."),
		text("customerType", "고객 유형으로 좁힙니다."),
		text("grade", "고객 등급으로 좁힙니다."),
		cursor(),
		sortBy("name", "-name", "annualRevenue", "-annualRevenue", "createdAt", "-createdAt", "updatedAt", "-updatedAt"),
		limit(50, 200),
	},
	"GET /customers/{id}/account-plan":                      {year()},
	"GET /customers/{id}/cross-sell":                        {year()},
	"GET /customers/{id}/intelligence":                      {text("opportunityId", "이 영업기회에 한정해 분석 결과를 좁힙니다.")},
	"DELETE /customers/{id}/relationships/{relationshipId}": {versionFilter()},
	"GET /contacts": {
		text("q", "담당자 이름·직책·연락처 검색어입니다."),
		text("customerId", "이 고객의 담당자만 반환합니다."),
		limit(50, 200),
	},
	"GET /leads": {text("q", "리드 검색어입니다."), limit(50, 200)},
	"GET /opportunities": {
		text("q", "영업기회명 또는 고객명 검색어입니다."),
		text("customerId", "이 고객의 영업기회만 반환합니다."),
		choice("status", "영업기회 상태입니다.", "OPEN", "WON", "LOST"),
		text("stageId", "이 Pipeline Stage의 영업기회만 반환합니다."),
		choice("forecastCategory", "매출 전망 범주입니다.", "COMMIT", "BEST_CASE", "PIPELINE", "CLOSED"),
		cursor(),
		sortBy("name", "-name", "expectedAmount", "-expectedAmount", "probability", "-probability", "expectedCloseDate", "-expectedCloseDate", "updatedAt", "-updatedAt"),
		limit(50, 200),
		flag("stale", "30일 이상 접점이 없는 영업기회만 반환합니다."),
	},
	"GET /opportunities/{id}/inspection":       {number("days", "최근 며칠 사이의 변화를 분석할지 정합니다.", 7, 1, 365)},
	"GET /opportunities/{id}/stage-readiness":  {text("stageId", "이 Stage로 넘어갈 수 있는지 판정합니다.")},
	"DELETE /opportunities/{id}/team/{userId}": {versionFilter()},
	"GET /collaborators":                       {text("q", "사용자 이름·이메일 검색어입니다."), limit(100, 200)},
	"GET /deal-intelligence/at-risk": {
		number("minimum", "이 위험 점수 이상인 Deal만 반환합니다.", 40, 1, 100),
		limit(25, 100),
	},
	"GET /activities": {
		text("customerId", "이 고객의 활동만 반환합니다."),
		text("opportunityId", "이 영업기회의 활동만 반환합니다."),
		text("type", "활동 유형으로 좁힙니다."),
		limit(50, 200),
	},
	"GET /products":               {text("q", "상품명·코드 검색어입니다."), limit(100, 500)},
	"GET /forecasts/intelligence": {number("days", "Snapshot을 비교할 기간입니다.", 7, 1, 365)},
	"GET /tasks/due": {
		number("days", "기한이 며칠 안에 도래하는 활동까지 볼지 정합니다.", 7, 0, 365),
		limit(50, 200),
	},
	"GET /quotations": {text("customerId", "이 고객의 견적만 반환합니다."), limit(50, 200)},
	"GET /contracts": {
		text("customerId", "이 고객의 계약만 반환합니다."),
		number("expiringDays", "이 일수 안에 종료되는 계약만 반환합니다. 0이면 종료일로 좁히지 않으며, 최대값보다 큰 값은 최대값으로 줄여 적용합니다.", 0, 0, 3650),
		flag("renewalOnly", "갱신 통지 기간에 들어온 계약만 반환합니다."),
		limit(50, 200),
	},
	"GET /sales":            {limit(100, 500)},
	"GET /notifications":    {flag("unread", "읽지 않은 알림만 반환합니다."), limit(100, 200)},
	"GET /reports":          {months(12, 60)},
	"GET /reports/win-loss": {months(12, 60)},
	"GET /signals": append(accountFilters(),
		text("signalType", "Signal 유형으로 좁힙니다."),
		choice("severity", "심각도입니다.", "LOW", "MEDIUM", "HIGH", "CRITICAL"),
		choice("sentiment", "감성 분류입니다.", "POSITIVE", "NEGATIVE", "NEUTRAL"),
		choice("status", "처리 상태입니다.", "ACTIVE", "RESOLVED", "IGNORED"),
		limit(50, 200)),
	"GET /risks": append(accountFilters(),
		text("riskType", "Risk 유형으로 좁힙니다."),
		choice("severity", "심각도입니다.", "LOW", "MEDIUM", "HIGH", "CRITICAL"),
		choice("status", "처리 상태입니다.", "OPEN", "RESOLVED", "ACCEPTED"),
		number("minScore", "이 점수 이상인 Risk만 반환합니다.", 0, 0, 100),
		limit(50, 200)),
	"GET /insights": {
		text("accountId", "이 고객의 Insight만 반환합니다."),
		text("opportunityId", "이 영업기회의 Insight만 반환합니다."),
		text("insightType", "Insight 유형으로 좁힙니다."),
		choice("status", "유효 상태입니다.", "ACTIVE", "EXPIRED"),
		limit(50, 200),
	},
	"GET /recommendations": {
		text("accountId", "이 고객의 추천만 반환합니다."),
		text("opportunityId", "이 영업기회의 추천만 반환합니다."),
		text("assigneeId", "이 담당자에게 배정된 추천만 반환합니다."),
		flag("mine", "나에게 배정된 추천만 반환합니다."),
		choice("priority", "우선순위입니다.", "LOW", "MEDIUM", "HIGH"),
		choice("status", "처리 상태입니다.", "OPEN", "ACCEPTED", "DISMISSED", "COMPLETED"),
		limit(50, 200),
	},
	"GET /voices":            voiceFilters(50),
	"GET /voices/export":     voiceFilters(200),
	"GET /voices/summary":    {text("customerId", "이 고객의 요약만 반환합니다.")},
	"GET /voices/categories": {flag("includeInactive", "사용 중지된 유형도 함께 반환합니다.")},
	"GET /approvals": {
		choice("status", "승인 요청 상태입니다.", "PENDING", "APPROVED", "REJECTED", "CANCELLED"),
	},
	"GET /approvals/capability": {
		text("entityType", "판정 대상 자원 유형입니다."),
		text("entityId", "판정 대상 자원 ID입니다."),
	},
	"GET /me/views":                {text("resource", "이 자원의 저장된 검색만 반환합니다.")},
	"GET /me/favorites":            {text("resource", "이 자원의 즐겨찾기만 반환합니다.")},
	"GET /admin/settings":          {text("namespace", "이 Namespace의 설정만 반환합니다.")},
	"GET /admin/approval-policies": {text("entityType", "이 자원 유형의 정책만 반환합니다.")},
	"GET /admin/personal-keys":     {text("userId", "이 사용자의 Key만 반환합니다.")},
	"GET /admin/audit": {
		text("q", "행위자·자원 ID 부분 일치 검색어입니다."),
		text("channel", "기록된 채널로 좁힙니다."),
		text("resource", "기록된 자원 이름으로 좁힙니다."),
		text("action", "기록된 동작 이름으로 좁힙니다."),
		limit(100, 500),
	},
}

// publicPaths are answered before authentication, so the projection parameter
// requireAuth adds to every other GET does not reach them.
var publicPaths = map[string]bool{
	"/system/version":     true,
	"/auth/status":        true,
	"/auth/login":         true,
	"/auth/oidc/start":    true,
	"/auth/oidc/callback": true,
	"/csp-report":         true,
}

var httpMethods = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}

func describeParameters(paths map[string]any) {
	for path, item := range paths {
		operations, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for method, value := range operations {
			operation, ok := value.(map[string]any)
			if !ok || !httpMethods[method] {
				continue
			}
			parameters := []any{}
			for _, q := range queryParameters[strings.ToUpper(method)+" "+path] {
				parameters = append(parameters, q.parameter())
			}
			if method == "get" && !publicPaths[path] {
				parameters = append(parameters, map[string]any{"$ref": "#/components/parameters/fields"})
			}
			if len(parameters) > 0 {
				operation["parameters"] = parameters
			}
		}
		if declared := pathParameters(path); len(declared) > 0 {
			operations["parameters"] = declared
		}
	}
}

// pathParameters reads the `{name}` segments straight out of the template, so a
// path added later declares its parameters without anyone remembering to.
func pathParameters(path string) []any {
	out := []any{}
	rest := path
	for {
		_, after, found := strings.Cut(rest, "{")
		if !found {
			return out
		}
		name, remainder, closed := strings.Cut(after, "}")
		if !closed {
			return out
		}
		rest = remainder
		out = append(out, map[string]any{"name": name, "in": "path", "required": true,
			"description": name + " 경로 값", "schema": map[string]any{"type": "string"}})
	}
}
