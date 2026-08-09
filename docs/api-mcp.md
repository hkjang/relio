# REST API and MCP

## REST

REST Prefix는 `/api/v1`입니다. JSON, Cursor Token, Filter, Optimistic Version, Request ID, 표준 Error Envelope를 사용합니다. OpenAPI는 `/api/openapi.json`, 사람이 읽는 내장 문서는 `/api/docs`입니다.

고객과 Opportunity 목록은 `cursor`, `limit`, `q`, Resource별 Filter와 `sort`를 지원합니다. 내림차순은 `-expectedAmount`처럼 `-`를 붙입니다. GET 응답의 `fields=id,name,updatedAt`은 목록의 각 항목 또는 단일 Resource를 지정한 필드로 투영합니다. 변경 요청은 8~200자의 `Idempotency-Key`를 보내면 24시간 동안 같은 결과를 안전하게 재사용합니다.

오류 형식:

```json
{
  "error": {
    "code": "forbidden",
    "message": "permission customer:write is required",
    "requestId": "..."
  }
}
```

## MCP

Endpoint는 `/mcp`이며 JSON-RPC 2.0 Streamable HTTP를 제공합니다. 최신 구현 프로토콜은 `2025-11-25`이고 `2025-06-18`, `2025-03-26` Header를 호환합니다. MCP 전송 어댑터는 `internal/mcp`에 격리되어 CRM Core가 Session/Transport 변경에 의존하지 않습니다.

Initialize 예시:

```bash
curl -X POST http://relio.example/mcp \
  -H 'Authorization: Bearer relio_...' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"agent","version":"1"}}}'
```

Tool 호출에는 협상된 버전을 Header에 포함합니다.

```bash
curl -X POST http://relio.example/mcp \
  -H 'Authorization: Bearer relio_...' \
  -H 'MCP-Protocol-Version: 2025-11-25' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_customer_360","arguments":{"id":"..."}}}'
```

승인 Tool은 활성 승인 정책이 하나 이상 있을 때만 `tools/list`에 나타납니다. 모든 Tool Result는 사용자 Permission과 Data Scope로 제한됩니다.
